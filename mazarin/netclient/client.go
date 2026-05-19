// Package netclient is the client-side library for talking to net.elf
// over NetIPC. Each shepherd that wants UDP networking creates a
// NetClient, wires it into its uring Dispatcher, then calls Connect to
// declare a response-ring index and a per-client TX-page watermark.
// Subsequent BindUDP / SendTo / RecvFrom / ReleaseRX / Close drive
// individual endpoints.
//
//	disp := uring.NewDispatcher()
//	nc := netclient.New(netSID)
//	disp.OnFunc(ipc.ProtoNetIPCResp, netclient.DecodeAny, nc.HandleResp)
//	disp.Start()
//	nc.Connect(0, 0)
//	connID, port, _ := nc.BindUDP([4]byte{}, 0)
//	nc.SendTo(connID, dst, payload)
//	rx, _ := nc.RecvFrom(connID)
//	defer nc.ReleaseRX(connID, rx.Page)
//	consume(rx.Payload())
//
// Sync requests (Connect / BindUDP / SendTo / Close) are routed back to
// their callers by ReqID via a pending-call map — multiple in-flight
// requests are allowed and matched independently. Unsolicited RecvDgram
// deliveries go to a per-conn buffered channel that RecvFrom drains.
package netclient

import (
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"mazzy/mazarin/mem"
	"mazzy/mazarin/sys"
	"mazzy/mazarin/uring"
	"mazzy/shared/ipc"
	"mazzy/shared/netproto"
)

const (
	// udpHeadroom is the per-page byte offset where SendTo writes its
	// payload. 42 bytes for eth+IP+UDP rounded up to 64 for alignment.
	udpHeadroom uint16 = 64

	// recvChanBuffer is the per-conn RX queue depth. Sized above the
	// default watermark so burst arrivals don't drop at the dispatcher.
	recvChanBuffer = 32

	// callTimeout bounds every synchronous round-trip. Long enough to
	// outlive normal scheduling jitter; short enough that a wedged
	// net.elf doesn't hang the caller indefinitely.
	callTimeout = 5 * time.Second
)

// RxDgram is one inbound datagram delivered by RecvFrom. Page is the
// client-side VA where the payload page lives; callers MUST eventually
// pass it to ReleaseRX. Offset+Length delimit the payload within the
// page; Payload() returns a slice view.
type RxDgram struct {
	Page   uint64
	Offset uint16
	Length uint16
	Src    netproto.Addr
}

// Payload returns a slice over the datagram's payload bytes. The slice
// is valid until ReleaseRX is called on Page.
func (r RxDgram) Payload() []byte {
	return unsafe.Slice(
		(*byte)(unsafe.Pointer(uintptr(r.Page)+uintptr(r.Offset))),
		int(r.Length),
	)
}

// NetClient is the public surface. Methods are safe to call from
// multiple goroutines; in-flight sync calls don't serialize on each
// other.
type NetClient interface {
	Connect(respRing uint8, watermark uint8) error
	BindUDP(localIP [4]byte, localPort uint16) (connID uint32, boundPort uint16, err error)
	SendTo(connID uint32, dst netproto.Addr, payload []byte) error
	RecvFrom(connID uint32) (RxDgram, error)
	ReleaseRX(connID uint32, pageVA uint64) error
	Close(connID uint32) error

	// HandleResp is the uring.Dispatcher callback target. Wire via
	// disp.OnFunc(ipc.ProtoNetIPCResp, netclient.DecodeAny, nc.HandleResp).
	HandleResp(v any)
}

// DecodeAny is the identity decoder for uring.Dispatcher.OnFunc. We
// pull the raw message through and demux by MsgType inside HandleResp.
func DecodeAny(msg *ipc.UringIPCMsg) any { return msg }

type clientImpl struct {
	netSID    int
	nextReqID atomic.Uint32

	pendingMu sync.Mutex
	pending   map[uint32]chan any

	connsMu sync.Mutex
	conns   map[uint32]*conn
}

type conn struct {
	recvCh chan *netproto.RecvDgram
	// stop is closed by Close to wake any RecvFrom blocked on this conn.
	// We never close recvCh: a racing deliverRecv could still hold the
	// conn pointer (looked up before Close took connsMu) and the
	// non-blocking send at deliverRecv would panic on a closed channel.
	stop chan struct{}
}

// New constructs a NetClient targeting the given net.elf SID. Wire it
// into a uring.Dispatcher with HandleResp before calling Connect.
func New(netSID int) NetClient {
	return &clientImpl{
		netSID:  netSID,
		pending: make(map[uint32]chan any),
		conns:   make(map[uint32]*conn),
	}
}

// nextID returns a non-zero ReqID. ReqID=0 is reserved as "invalid".
func (c *clientImpl) nextID() uint32 {
	for {
		id := c.nextReqID.Add(1)
		if id != 0 {
			return id
		}
	}
}

func (c *clientImpl) registerPending(reqID uint32) chan any {
	ch := make(chan any, 1)
	c.pendingMu.Lock()
	c.pending[reqID] = ch
	c.pendingMu.Unlock()
	return ch
}

func (c *clientImpl) clearPending(reqID uint32) {
	c.pendingMu.Lock()
	delete(c.pending, reqID)
	c.pendingMu.Unlock()
}

// HandleResp demuxes incoming NetIPC messages: typed responses go to
// the matching pending-call channel; unsolicited RecvDgrams go to the
// destination conn's buffered recvCh.
func (c *clientImpl) HandleResp(v any) {
	msg, ok := v.(*ipc.UringIPCMsg)
	if !ok {
		return
	}
	switch netproto.MsgTypeOf(msg) {
	case netproto.NetMsgConnectResp:
		r := *netproto.DecodeConnectResp(msg)
		c.deliverSync(r.ReqID, &r)
	case netproto.NetMsgBindUDPResp:
		r := *netproto.DecodeBindUDPResp(msg)
		c.deliverSync(r.ReqID, &r)
	case netproto.NetMsgSendDgramResp:
		r := *netproto.DecodeSendDgramResp(msg)
		c.deliverSync(r.ReqID, &r)
	case netproto.NetMsgCloseResp:
		r := *netproto.DecodeCloseResp(msg)
		c.deliverSync(r.ReqID, &r)
	case netproto.NetMsgRecvDgram:
		r := *netproto.DecodeRecvDgram(msg)
		c.deliverRecv(&r)
	}
}

func (c *clientImpl) deliverSync(reqID uint32, v any) {
	c.pendingMu.Lock()
	ch, ok := c.pending[reqID]
	c.pendingMu.Unlock()
	if !ok {
		// Stale response (the caller already timed out and cleared its
		// pending entry). Dropping is correct.
		return
	}
	select {
	case ch <- v:
	default:
	}
}

func (c *clientImpl) deliverRecv(r *netproto.RecvDgram) {
	c.connsMu.Lock()
	conn, ok := c.conns[r.ConnID]
	c.connsMu.Unlock()
	if !ok {
		fmt.Printf("[netclient] RecvDgram for unknown connID=%d, dropping\n", r.ConnID)
		return
	}
	select {
	case conn.recvCh <- r:
	default:
		// Recv queue full: client is too slow to drain. Dropping
		// preserves dispatcher liveness; the loaned page stays mapped
		// at the client side and counts against the watermark.
		fmt.Printf("[netclient] recvCh full for connID=%d, dropping\n", r.ConnID)
	}
}

// callSync sends a request and waits for the matching response (by
// ReqID) or callTimeout, whichever comes first. build() encodes the
// request with the chosen ReqID; callers do their own type assertion
// and field validation on the returned value.
func (c *clientImpl) callSync(label string, build func(reqID uint32) ipc.UringIPCMsg) (any, error) {
	reqID := c.nextID()
	done := c.registerPending(reqID)
	defer c.clearPending(reqID)

	msg := build(reqID)
	if err := uring.Send(c.netSID, &msg); err != nil {
		return nil, fmt.Errorf("netclient: %s Send: %w", label, err)
	}
	t := time.NewTimer(callTimeout)
	defer t.Stop()
	select {
	case v := <-done:
		return v, nil
	case <-t.C:
		return nil, fmt.Errorf("netclient: %s timeout", label)
	}
}

func (c *clientImpl) Connect(respRing uint8, watermark uint8) error {
	v, err := c.callSync("Connect", func(reqID uint32) ipc.UringIPCMsg {
		req := netproto.NetIPCConnectReq{ReqID: reqID, RespRing: respRing, Watermark: watermark}
		return netproto.EncodeConnect(&req, int16(os.Getpid()))
	})
	if err != nil {
		return err
	}
	resp, ok := v.(*netproto.NetIPCConnectResp)
	if !ok {
		return fmt.Errorf("netclient: Connect wrong resp type %T", v)
	}
	if resp.ErrCode != netproto.NetErrNone {
		return fmt.Errorf("netclient: Connect rejected: %d", resp.ErrCode)
	}
	return nil
}

func (c *clientImpl) BindUDP(localIP [4]byte, localPort uint16) (uint32, uint16, error) {
	v, err := c.callSync("BindUDP", func(reqID uint32) ipc.UringIPCMsg {
		req := netproto.BindUDPReq{ReqID: reqID, LocalPort: localPort, LocalIP: localIP}
		return netproto.EncodeBindUDP(&req, int16(os.Getpid()))
	})
	if err != nil {
		return 0, 0, err
	}
	resp, ok := v.(*netproto.BindUDPResp)
	if !ok {
		return 0, 0, fmt.Errorf("netclient: BindUDP wrong resp type %T", v)
	}
	if resp.ErrCode != netproto.NetErrNone {
		return 0, 0, fmt.Errorf("netclient: BindUDP failed: %d", resp.ErrCode)
	}
	// Register the conn before returning so the caller can RecvFrom
	// immediately without racing the first inbound delivery.
	cn := &conn{
		recvCh: make(chan *netproto.RecvDgram, recvChanBuffer),
		stop:   make(chan struct{}),
	}
	c.connsMu.Lock()
	c.conns[resp.ConnID] = cn
	c.connsMu.Unlock()
	return resp.ConnID, resp.LocalPort, nil
}

// MaxPayloadSize is the largest UDP payload SendTo accepts. A page is
// 4096 bytes; udpHeadroom (64) reserves the front for the layered
// header stack. UDP itself caps at 65507 bytes but per-page transfer
// limits us to one MTU's worth.
const MaxPayloadSize = 4096 - int(udpHeadroom)

func (c *clientImpl) SendTo(connID uint32, dst netproto.Addr, payload []byte) error {
	if len(payload) > MaxPayloadSize {
		return fmt.Errorf("netclient: payload too large (%d > %d)", len(payload), MaxPayloadSize)
	}
	clump, err := mem.AllocContiguous(4096)
	if err != nil {
		return fmt.Errorf("netclient: AllocContiguous: %w", err)
	}
	copy(clump.Buf[udpHeadroom:], payload)
	netSideVA, err := sys.TransferDMAClump(c.netSID, clump.Addr, 0)
	if err != nil {
		return fmt.Errorf("netclient: TransferDMAClump: %w", err)
	}

	v, err := c.callSync("SendTo", func(reqID uint32) ipc.UringIPCMsg {
		req := netproto.SendDgramReq{
			ReqID:  reqID,
			ConnID: connID,
			Dst:    dst,
			PageVA: uint64(netSideVA),
			Offset: udpHeadroom,
			Length: uint16(len(payload)),
		}
		return netproto.EncodeSendDgram(&req, int16(os.Getpid()))
	})
	if err != nil {
		return err
	}
	resp, ok := v.(*netproto.SendDgramResp)
	if !ok {
		return fmt.Errorf("netclient: SendTo wrong resp type %T", v)
	}
	if resp.ErrCode != netproto.NetErrNone {
		return fmt.Errorf("netclient: SendTo failed: %d", resp.ErrCode)
	}
	return nil
}

func (c *clientImpl) RecvFrom(connID uint32) (RxDgram, error) {
	c.connsMu.Lock()
	cn, ok := c.conns[connID]
	c.connsMu.Unlock()
	if !ok {
		return RxDgram{}, fmt.Errorf("netclient: RecvFrom unknown connID=%d", connID)
	}
	select {
	case rx := <-cn.recvCh:
		return RxDgram{
			Page:   rx.PageVA,
			Offset: rx.Offset,
			Length: rx.Length,
			Src:    rx.Src,
		}, nil
	case <-cn.stop:
		return RxDgram{}, fmt.Errorf("netclient: RecvFrom on closed connID=%d", connID)
	}
}

func (c *clientImpl) ReleaseRX(connID uint32, pageVA uint64) error {
	req := netproto.ReleaseReq{ConnID: connID, PageVA: pageVA}
	msg := netproto.EncodeRelease(&req, int16(os.Getpid()))
	if err := uring.Send(c.netSID, &msg); err != nil {
		return fmt.Errorf("netclient: ReleaseRX Send: %w", err)
	}
	return nil
}

func (c *clientImpl) Close(connID uint32) error {
	v, err := c.callSync("Close", func(reqID uint32) ipc.UringIPCMsg {
		req := netproto.CloseReq{ReqID: reqID, ConnID: connID}
		return netproto.EncodeClose(&req, int16(os.Getpid()))
	})
	if err != nil {
		return err
	}
	resp, ok := v.(*netproto.CloseResp)
	if !ok {
		return fmt.Errorf("netclient: Close wrong resp type %T", v)
	}
	if resp.ErrCode != netproto.NetErrNone {
		return fmt.Errorf("netclient: Close failed: %d", resp.ErrCode)
	}
	// Drop the conn from the table and signal stop so any blocked
	// RecvFrom returns "closed". Leaving recvCh open (rather than
	// closing it) avoids a send-on-closed panic if a deliverRecv is
	// in-flight with a stale conn pointer from before the map delete.
	c.connsMu.Lock()
	cn := c.conns[connID]
	delete(c.conns, connID)
	c.connsMu.Unlock()
	if cn != nil {
		close(cn.stop)
	}
	return nil
}
