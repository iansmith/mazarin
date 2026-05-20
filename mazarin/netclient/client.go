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
	return pageSlice(r.Page, r.Offset, r.Length)
}

// StreamChunk is one chunk of inbound stream data delivered by
// ReadStream. Page is the client-side VA where the chunk bytes live;
// callers MUST eventually pass it to ReleaseRX *unless* this is an
// EOF-only chunk (EOF=true, Length=0, Page=0). EOF reports the peer
// half-closing the write side; subsequent ReadStream calls return
// "stream closed" once any buffered chunks are drained.
type StreamChunk struct {
	Page   uint64
	Offset uint16
	Length uint16
	EOF    bool
}

// Payload returns a slice over the chunk's bytes. Empty if Length=0.
// The slice is valid until ReleaseRX is called on Page.
func (s StreamChunk) Payload() []byte {
	return pageSlice(s.Page, s.Offset, s.Length)
}

// pageSlice maps a (page-VA, offset, length) triple onto a Go slice
// over the mapped page memory. The single audited unsafe spelling for
// all client-side reads of net.elf-shared pages.
func pageSlice(page uint64, offset, length uint16) []byte {
	if length == 0 {
		return nil
	}
	return unsafe.Slice(
		(*byte)(unsafe.Pointer(uintptr(page)+uintptr(offset))),
		int(length),
	)
}

// NetClient is the public surface. Methods are safe to call from
// multiple goroutines; in-flight sync calls don't serialize on each
// other.
type NetClient interface {
	Connect(respRing uint8, watermark uint8) error

	// UDP
	BindUDP(localIP [4]byte, localPort uint16) (connID uint32, boundPort uint16, err error)
	SendTo(connID uint32, dst netproto.Addr, payload []byte) error
	RecvFrom(connID uint32) (RxDgram, error)

	// TCP
	BindTCP(localIP [4]byte, localPort uint16) (connID uint32, boundPort uint16, err error)
	Listen(connID uint32, backlog uint16) error
	Accept(connID uint32) (newConnID uint32, peer netproto.Addr, err error)
	TCPConnect(localIP [4]byte, localPort uint16, dst netproto.Addr) (connID uint32, boundPort uint16, err error)
	StreamSend(connID uint32, payload []byte) (n int, err error)
	ReadStream(connID uint32) (StreamChunk, error)
	Shutdown(connID uint32, how uint8) error

	// Page reclaim — works for both UDP datagram pages and TCP stream
	// chunks; net.elf demuxes by (ConnID, PageVA).
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
	// recvCh is non-nil for UDP datagram conns (BindUDP), nil otherwise.
	recvCh chan *netproto.RecvDgram
	// streamRxCh is non-nil for TCP stream conns (post-Accept /
	// post-TCPConnect), nil otherwise.
	streamRxCh chan *netproto.StreamRx
	// sendPageVA is the client-side VA of the per-stream send page net.elf
	// mapped RW into this client at Accept/Connect time. Zero for UDP
	// conns and TCP listeners.
	sendPageVA uint64
	// stop is closed by Close to wake any RecvFrom/ReadStream blocked on
	// this conn. We never close recvCh / streamRxCh: a racing
	// deliverRecv / deliverStreamRx could still hold the conn pointer
	// (looked up before Close took connsMu) and the non-blocking send
	// would panic on a closed channel.
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
// the matching pending-call channel; unsolicited RecvDgrams /
// StreamRxs go to the destination conn's buffered recv channel.
//
// AcceptResp and TCPConnectResp register their new stream conn under
// connsMu *before* delivering to the pending caller, so a StreamRx that
// races behind the response on the same ring will find its conn ready.
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
	case netproto.NetMsgBindTCPResp:
		r := *netproto.DecodeBindTCPResp(msg)
		c.deliverSync(r.ReqID, &r)
	case netproto.NetMsgListenResp:
		r := *netproto.DecodeListenResp(msg)
		c.deliverSync(r.ReqID, &r)
	case netproto.NetMsgAcceptResp:
		r := *netproto.DecodeAcceptResp(msg)
		if r.ErrCode == netproto.NetErrNone {
			c.registerStreamConn(r.NewConnID, r.SendPageVA)
		}
		c.deliverSync(r.ReqID, &r)
	case netproto.NetMsgTCPConnectResp:
		r := *netproto.DecodeTCPConnectResp(msg)
		if r.ErrCode == netproto.NetErrNone {
			c.registerStreamConn(r.ConnID, r.SendPageVA)
		}
		c.deliverSync(r.ReqID, &r)
	case netproto.NetMsgStreamSendResp:
		r := *netproto.DecodeStreamSendResp(msg)
		c.deliverSync(r.ReqID, &r)
	case netproto.NetMsgStreamShutdownResp:
		r := *netproto.DecodeStreamShutdownResp(msg)
		c.deliverSync(r.ReqID, &r)
	case netproto.NetMsgStreamRx:
		r := *netproto.DecodeStreamRx(msg)
		c.deliverStreamRx(&r)
	}
}

// registerStreamConn allocates the per-conn state for a newly-accepted
// or newly-connected TCP stream. Idempotent — a duplicate response from
// a buggy server is logged + ignored rather than overwriting state.
func (c *clientImpl) registerStreamConn(connID uint32, sendPageVA uint64) {
	cn := &conn{
		streamRxCh: make(chan *netproto.StreamRx, recvChanBuffer),
		sendPageVA: sendPageVA,
		stop:       make(chan struct{}),
	}
	c.connsMu.Lock()
	if _, dup := c.conns[connID]; dup {
		c.connsMu.Unlock()
		fmt.Printf("[netclient] duplicate stream conn registration connID=%d, ignoring\n", connID)
		return
	}
	c.conns[connID] = cn
	c.connsMu.Unlock()
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
	if !ok || conn.recvCh == nil {
		fmt.Printf("[netclient] RecvDgram for unknown/non-UDP connID=%d, dropping\n", r.ConnID)
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

func (c *clientImpl) deliverStreamRx(r *netproto.StreamRx) {
	c.connsMu.Lock()
	conn, ok := c.conns[r.ConnID]
	c.connsMu.Unlock()
	if !ok || conn.streamRxCh == nil {
		fmt.Printf("[netclient] StreamRx for unknown/non-TCP connID=%d, dropping\n", r.ConnID)
		return
	}
	select {
	case conn.streamRxCh <- r:
	default:
		fmt.Printf("[netclient] streamRxCh full for connID=%d, dropping\n", r.ConnID)
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
	// RecvFrom / ReadStream returns "closed". Leaving the recv channels
	// open (rather than closing them) avoids a send-on-closed panic if
	// a deliver is in-flight with a stale conn pointer from before the
	// map delete.
	c.connsMu.Lock()
	cn := c.conns[connID]
	delete(c.conns, connID)
	c.connsMu.Unlock()
	if cn != nil {
		close(cn.stop)
	}
	return nil
}

// --- TCP surface ---

// BindTCP creates a TCP listener-shaped conn bound to (localIP,
// localPort). localPort=0 requests an ephemeral assignment. The
// returned connID identifies the listener for Listen/Accept/Close.
// There's no client-side conn registration for listeners — they
// receive no data.
func (c *clientImpl) BindTCP(localIP [4]byte, localPort uint16) (uint32, uint16, error) {
	v, err := c.callSync("BindTCP", func(reqID uint32) ipc.UringIPCMsg {
		req := netproto.BindTCPReq{ReqID: reqID, LocalPort: localPort, LocalIP: localIP}
		return netproto.EncodeBindTCP(&req, int16(os.Getpid()))
	})
	if err != nil {
		return 0, 0, err
	}
	resp, ok := v.(*netproto.BindTCPResp)
	if !ok {
		return 0, 0, fmt.Errorf("netclient: BindTCP wrong resp type %T", v)
	}
	if resp.ErrCode != netproto.NetErrNone {
		return 0, 0, fmt.Errorf("netclient: BindTCP failed: %d", resp.ErrCode)
	}
	return resp.ConnID, resp.LocalPort, nil
}

// Listen activates a listener-conn created by BindTCP with the given
// backlog (gvisor's accept queue depth).
func (c *clientImpl) Listen(connID uint32, backlog uint16) error {
	v, err := c.callSync("Listen", func(reqID uint32) ipc.UringIPCMsg {
		req := netproto.ListenReq{ReqID: reqID, ConnID: connID, Backlog: backlog}
		return netproto.EncodeListen(&req, int16(os.Getpid()))
	})
	if err != nil {
		return err
	}
	resp, ok := v.(*netproto.ListenResp)
	if !ok {
		return fmt.Errorf("netclient: Listen wrong resp type %T", v)
	}
	if resp.ErrCode != netproto.NetErrNone {
		return fmt.Errorf("netclient: Listen failed: %d", resp.ErrCode)
	}
	return nil
}

// Accept blocks until a new inbound stream arrives on the listener
// identified by connID. Returns the new stream's connID and the peer
// address. The per-stream send page is recorded in client-side state
// during HandleResp; subsequent StreamSend calls use it implicitly.
func (c *clientImpl) Accept(connID uint32) (uint32, netproto.Addr, error) {
	v, err := c.callSync("Accept", func(reqID uint32) ipc.UringIPCMsg {
		req := netproto.AcceptReq{ReqID: reqID, ConnID: connID}
		return netproto.EncodeAccept(&req, int16(os.Getpid()))
	})
	if err != nil {
		return 0, netproto.Addr{}, err
	}
	resp, ok := v.(*netproto.AcceptResp)
	if !ok {
		return 0, netproto.Addr{}, fmt.Errorf("netclient: Accept wrong resp type %T", v)
	}
	if resp.ErrCode != netproto.NetErrNone {
		return 0, netproto.Addr{}, fmt.Errorf("netclient: Accept failed: %d", resp.ErrCode)
	}
	return resp.NewConnID, resp.Peer, nil
}

// TCPConnect performs an active open against dst. localIP/localPort
// are usually zero (let net.elf pick an ephemeral source). On success
// returns the new stream's connID and the bound source port.
func (c *clientImpl) TCPConnect(localIP [4]byte, localPort uint16, dst netproto.Addr) (uint32, uint16, error) {
	v, err := c.callSync("TCPConnect", func(reqID uint32) ipc.UringIPCMsg {
		req := netproto.TCPConnectReq{
			ReqID:     reqID,
			Dst:       dst,
			LocalIP:   localIP,
			LocalPort: localPort,
		}
		return netproto.EncodeTCPConnect(&req, int16(os.Getpid()))
	})
	if err != nil {
		return 0, 0, err
	}
	resp, ok := v.(*netproto.TCPConnectResp)
	if !ok {
		return 0, 0, fmt.Errorf("netclient: TCPConnect wrong resp type %T", v)
	}
	if resp.ErrCode != netproto.NetErrNone {
		return 0, 0, fmt.Errorf("netclient: TCPConnect failed: %d", resp.ErrCode)
	}
	return resp.ConnID, resp.LocalPort, nil
}

// MaxStreamSendSize is the largest payload StreamSend accepts in one
// call.
const MaxStreamSendSize = 4096

// StreamSend writes payload onto the stream identified by connID.
// Returns the number of bytes gvisor's Endpoint.Write accepted (may be
// less than len(payload); callers re-send the tail). For v1 at most
// one StreamSend may be in flight per connID — the client waits for
// the response before reusing the send page.
func (c *clientImpl) StreamSend(connID uint32, payload []byte) (int, error) {
	if len(payload) == 0 {
		return 0, nil
	}
	if len(payload) > MaxStreamSendSize {
		return 0, fmt.Errorf("netclient: StreamSend payload too large (%d > %d)",
			len(payload), MaxStreamSendSize)
	}
	c.connsMu.Lock()
	cn, ok := c.conns[connID]
	c.connsMu.Unlock()
	if !ok || cn.sendPageVA == 0 {
		return 0, fmt.Errorf("netclient: StreamSend unknown or non-stream connID=%d", connID)
	}
	copy(pageSlice(cn.sendPageVA, 0, MaxStreamSendSize), payload)

	v, err := c.callSync("StreamSend", func(reqID uint32) ipc.UringIPCMsg {
		req := netproto.StreamSendReq{
			ReqID:  reqID,
			ConnID: connID,
			Length: uint16(len(payload)),
		}
		return netproto.EncodeStreamSend(&req, int16(os.Getpid()))
	})
	if err != nil {
		return 0, err
	}
	resp, ok := v.(*netproto.StreamSendResp)
	if !ok {
		return 0, fmt.Errorf("netclient: StreamSend wrong resp type %T", v)
	}
	if resp.ErrCode != netproto.NetErrNone {
		return int(resp.BytesWritten), fmt.Errorf("netclient: StreamSend failed: %d", resp.ErrCode)
	}
	return int(resp.BytesWritten), nil
}

// ReadStream blocks until the next StreamRx chunk arrives on connID
// or the conn is Closed. See StreamChunk for EOF semantics.
func (c *clientImpl) ReadStream(connID uint32) (StreamChunk, error) {
	c.connsMu.Lock()
	cn, ok := c.conns[connID]
	c.connsMu.Unlock()
	if !ok || cn.streamRxCh == nil {
		return StreamChunk{}, fmt.Errorf("netclient: ReadStream unknown or non-stream connID=%d", connID)
	}
	select {
	case rx := <-cn.streamRxCh:
		return StreamChunk{
			Page:   rx.PageVA,
			Offset: rx.Offset,
			Length: rx.Length,
			EOF:    rx.Flags&netproto.StreamRxFlagEOF != 0,
		}, nil
	case <-cn.stop:
		return StreamChunk{}, fmt.Errorf("netclient: ReadStream on closed connID=%d", connID)
	}
}

// Shutdown half-closes a TCP stream. how is a bitmap of
// netproto.ShutdownRead / netproto.ShutdownWrite.
func (c *clientImpl) Shutdown(connID uint32, how uint8) error {
	v, err := c.callSync("Shutdown", func(reqID uint32) ipc.UringIPCMsg {
		req := netproto.StreamShutdownReq{ReqID: reqID, ConnID: connID, How: how}
		return netproto.EncodeStreamShutdown(&req, int16(os.Getpid()))
	})
	if err != nil {
		return err
	}
	resp, ok := v.(*netproto.StreamShutdownResp)
	if !ok {
		return fmt.Errorf("netclient: Shutdown wrong resp type %T", v)
	}
	if resp.ErrCode != netproto.NetErrNone {
		return fmt.Errorf("netclient: Shutdown failed: %d", resp.ErrCode)
	}
	return nil
}
