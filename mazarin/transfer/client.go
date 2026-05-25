package transfer

import (
	"errors"
	"os"
	"sync"
	"sync/atomic"

	"mazzy/mazarin/sys"
	"mazzy/mazarin/uring"
	"mazzy/shared/ipc"
)

// ErrServerError is returned when the server's response carries a non-zero Err.
var ErrServerError = errors.New("transfer: server returned error")

// ErrUnknownHandle is returned by Handle.Commit when the package has no
// outstanding record of the handle — usually because Commit was called twice
// or on a Handle that was never produced by Reserve.
var ErrUnknownHandle = errors.New("transfer: unknown handle (already committed?)")

// ErrReqIDMismatch is returned by Reserve when the response's ReqID doesn't
// match the request's. Currently can only happen if the server misbehaves
// or the IPC layer reorders responses; the per-server client mutex serializes
// in-flight Reserves on the client side.
var ErrReqIDMismatch = errors.New("transfer: response ReqID does not match request")

// outstandingRecord tracks per-handle state the client needs at Commit time
// but doesn't want to expose on the public Handle struct.
type outstandingRecord struct {
	serverSID ShepherdID
	reqID     uint32
}

var (
	outstandingMu sync.Mutex
	outstanding   = make(map[uintptr]outstandingRecord)

	clientsMu sync.Mutex
	clients   = make(map[ShepherdID]*transferClient)
)

// transferClient holds the per-server connection state for the client side
// of Mode 1. One instance per (serverSID); shared across goroutines.
type transferClient struct {
	serverSID ShepherdID
	respCh    chan any
	mu        sync.Mutex // serializes Reserve calls so a single in-flight reqID owns the next respCh value
	nextID    atomic.Uint32
}

func getClient(server ShepherdID) *transferClient {
	clientsMu.Lock()
	defer clientsMu.Unlock()
	if c, ok := clients[server]; ok {
		return c
	}
	c := &transferClient{
		serverSID: server,
		respCh:    make(chan any, 1),
	}
	clients[server] = c
	return c
}

// DecodeResp is the uring Dispatcher decoder for ProtoTransferResp messages.
// Register with `dispatcher.On(ipc.ProtoTransferResp, transfer.DecodeResp, ch)`
// where ch is the channel returned by RespCh(serverSID).
func DecodeResp(msg *ipc.UringIPCMsg) any {
	return *ipc.DecodeTransferResp(msg)
}

// RespCh returns the response channel for a given server. The caller wires
// this into a uring Dispatcher via `dispatcher.On(ProtoTransferResp, DecodeResp, RespCh(server))`.
// Idempotent — returns the same channel for repeated calls with the same server.
func RespCh(server ShepherdID) chan any {
	return getClient(server).respCh
}

// Reserve asks `server` to allocate `size` bytes of pages and map them into
// the caller's address space. Returns a Handle covering the mapped region;
// fill via h.Writer() or h.Bytes(), then call h.Commit() to transfer the
// pages back to the server and unblock the server's Slab.Wait().
//
// `kind` is opaque to the transfer layer — passed through to the server's
// handler for application-level routing.
func Reserve(server ShepherdID, kind uint32, size int) (Handle, error) {
	if size < 0 {
		return Handle{}, errors.New("transfer: negative size")
	}
	pages := (size + PageSize - 1) / PageSize
	if pages == 0 {
		pages = 1
	}
	if pages > MaxPagesPerSlab {
		return Handle{}, ErrPageCapExceeded
	}

	c := getClient(server)
	c.mu.Lock()
	defer c.mu.Unlock()

	reqID := c.nextID.Add(1)
	req := ipc.TransferReqPayload{
		Op:    ipc.TransferOpReserve,
		Kind:  kind,
		Size:  uint64(size),
		ReqID: reqID,
	}
	msg := ipc.EncodeTransferReq(&req, int16(os.Getpid()))
	if err := uring.Send(int(server), &msg); err != nil {
		return Handle{}, err
	}

	raw := <-c.respCh
	resp, ok := raw.(ipc.TransferRespPayload)
	if !ok {
		return Handle{}, errors.New("transfer: unexpected response type on respCh")
	}
	if resp.ReqID != reqID {
		return Handle{}, ErrReqIDMismatch
	}
	if resp.Err != 0 {
		return Handle{}, ErrServerError
	}

	h := Handle{VA: uintptr(resp.VA), Pages: int(resp.Pages)}

	outstandingMu.Lock()
	outstanding[h.VA] = outstandingRecord{serverSID: server, reqID: reqID}
	outstandingMu.Unlock()

	return h, nil
}

// Commit releases the pages from the client's address space (atomically
// transferring them back to the server) and notifies the server that the
// fill is done. The server's matching Slab.Wait() returns after this.
//
// Release-first: sys.TransferAndUnmap completes before the Commit IPC is
// sent, so the server can't observe torn writes through a stale TLB.
//
// The outstanding-handle entry is retained until BOTH TransferAndUnmap and
// the notify Send succeed. A transient failure on either step leaves the
// entry in place so the caller can retry Commit.
func (h Handle) Commit() error {
	outstandingMu.Lock()
	rec, ok := outstanding[h.VA]
	outstandingMu.Unlock()
	if !ok {
		return ErrUnknownHandle
	}

	// Release-first: pages move atomically back to the server here.
	serverVA, err := sys.TransferAndUnmap(int(rec.serverSID), h.VA, h.Pages)
	if err != nil {
		return err
	}

	// Notify the server that the pages have arrived. Fire-and-forget — the
	// server's dispatch handler updates its Slab and signals Wait().
	req := ipc.TransferReqPayload{
		Op:    ipc.TransferOpCommit,
		VA:    uint64(serverVA),
		Pages: uint32(h.Pages),
		ReqID: rec.reqID,
	}
	msg := ipc.EncodeTransferReq(&req, int16(os.Getpid()))
	if err := uring.Send(int(rec.serverSID), &msg); err != nil {
		return err
	}

	// Both irreversible steps succeeded — safe to drop the outstanding entry.
	outstandingMu.Lock()
	delete(outstanding, h.VA)
	outstandingMu.Unlock()
	return nil
}
