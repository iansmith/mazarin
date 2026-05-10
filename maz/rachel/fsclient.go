package main

import (
	"fmt"
	"os"
	"sync"

	"mazzy/mazarin/fsclient"
	"mazzy/shared/ipc"
)

// sharedFSClient wraps an fsclient.Client so that two logical callers
// (rachel and fontsvc) can share a single fs IPC connection while keeping
// their reqID spaces partitioned. Responses are routed by a background
// goroutine that checks the high bit of ReqID.
type sharedFSClient struct {
	mu    sync.Mutex
	inner *fsclient.Client

	rachelPart  *fsPartition
	fontsvcPart *fsPartition
}

// fsPartition is one half of a sharedFSClient. It owns a private response
// channel and a reqID base so that the router goroutine can deliver
// responses to the correct caller.
type fsPartition struct {
	shared  *sharedFSClient
	respCh  chan any
	reqBase uint32
	label   string
}

// newSharedFSClient creates an fsclient.Client and two partitions.
// rachel uses IDs 0..0x7FFFFFFF; fontsvc uses IDs 0x80000000..0xFFFFFFFF.
func newSharedFSClient(fsSID int) *sharedFSClient {
	inner := fsclient.New(fsSID)
	sc := &sharedFSClient{inner: inner}
	sc.rachelPart = &fsPartition{
		shared:  sc,
		respCh:  make(chan any, 1),
		reqBase: 0,
		label:   "rachel",
	}
	sc.fontsvcPart = &fsPartition{
		shared:  sc,
		respCh:  make(chan any, 1),
		reqBase: 0x80000000,
		label:   "fontsvc",
	}
	return sc
}

// Connect sets the response ring and performs the fs handshake, then
// starts the response router goroutine.
func (sc *sharedFSClient) Connect(respRing int) error {
	sc.inner.RespRing = uint8(respRing)
	if err := sc.inner.Connect(); err != nil {
		return fmt.Errorf("fsclient Connect: %w", err)
	}
	fmt.Printf("[rachel:fs] connected to fs on ring %d\n", respRing)
	sc.startRouter()
	return nil
}

// FontSvcPartition returns the partition intended for fontsvc's use.
func (sc *sharedFSClient) FontSvcPartition() *fsPartition {
	return sc.fontsvcPart
}

// startRouter launches a goroutine that reads decoded fs responses from
// the inner client's RespCh and routes them to the correct partition
// based on ReqID bit 31.
func (sc *sharedFSClient) startRouter() {
	go func() {
		defer fmt.Printf("[rachel:fs] router goroutine exited\n")
		for raw := range sc.inner.RespCh {
			resp, ok := raw.(ipc.FSIPCRespPayload)
			if !ok {
				fmt.Printf("[rachel:fs] router: unexpected type %T, dropping\n", raw)
				continue
			}
			partIdx := resp.ReqID >> 31
			var part *fsPartition
			switch partIdx {
			case 0:
				part = sc.rachelPart
			case 1:
				part = sc.fontsvcPart
			default:
				fmt.Printf("[rachel:fs] router: reqID=%d out of range, dropping\n", resp.ReqID)
				continue
			}
			fmt.Printf("[rachel:fs] router: reqID=%d → %s\n", resp.ReqID, part.label)
			part.respCh <- raw
		}
	}()
}

// call sets up the inner client's RespChOverride and ReqIDOffset for one
// request/reply cycle, then invokes fn (which must be a single fsclient
// method call). The caller must hold shared.mu.
func (p *fsPartition) call(fn func()) {
	p.shared.inner.RespChOverride = p.respCh
	p.shared.inner.ReqIDOffset = p.reqBase
	fn()
}

// LoadFile reads an entire file into memory via fsclient (Open + chunked
// Read + Close). It replaces sys.LoadFile for fontsvc's use.
func (p *fsPartition) LoadFile(path string) ([]byte, error) {
	p.shared.mu.Lock()
	defer p.shared.mu.Unlock()

	fmt.Printf("[rachel:fs:%s] LoadFile(%s)\n", p.label, path)

	var handle uint32
	var size uint32
	var err error

	p.call(func() {
		var inum uint32
		var ftype uint8
		handle, inum, ftype, size, err = p.shared.inner.Open(path, 0, 0)
		_ = inum
		_ = ftype
	})
	if err != nil {
		return nil, fmt.Errorf("Open: %w", err)
	}

	buf := make([]byte, size)
	var offset int64
	for offset < int64(size) {
		var n int
		p.call(func() {
			n, err = p.shared.inner.Read(handle, offset, buf[offset:])
		})
		if err != nil {
			p.call(func() { _ = p.shared.inner.Close(handle) })
			return nil, fmt.Errorf("Read: %w", err)
		}
		if n == 0 {
			break
		}
		offset += int64(n)
	}

	p.call(func() { err = p.shared.inner.Close(handle) })
	if err != nil {
		fmt.Printf("[rachel:fs:%s] Close(%s) warning: %v\n", p.label, path, err)
	}

	fmt.Printf("[rachel:fs:%s] LoadFile(%s) ok, %d bytes\n", p.label, path, offset)
	return buf[:offset], nil
}

// rachelLoadFile is the convenience wrapper used by rachel's own code.
func (sc *sharedFSClient) rachelLoadFile(path string) ([]byte, error) {
	return sc.rachelPart.LoadFile(path)
}

// Ensure os.Getpid is available (used by fsclient.callLocked internally).
var _ = os.Getpid
