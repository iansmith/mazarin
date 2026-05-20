// netipc.go — NetIPC dispatcher inside the gvisor.maz plugin.
//
// Net.elf accepts ProtoNetIPCReq on its default IPC ring 0 and forwards
// each message to handleNetIPC. handleNetIPC switches on the MsgType
// discriminator and dispatches to a typed handler. Handlers run in
// net.elf's NetIPC reader goroutine; the msg pointer is only valid for
// the lifetime of the call, so handlers copy what they need.
//
// The dispatcher keeps a per-client state map keyed by SenderSID. Each
// client must send NetMsgConnect first; the handler records the
// declared response-ring index and the granted watermark. Subsequent
// requests look up the client by SID — if no state exists, the request
// is dropped (with a log line in dev) or rejected with NetErrInvalid.
package main

import (
	"bytes"
	"fmt"
	"sync"
	"sync/atomic"
	"unsafe"

	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/transport/udp"
	"gvisor.dev/gvisor/pkg/waiter"

	"mazzy/mazarin/mem"
	"mazzy/mazarin/sys"
	"mazzy/mazarin/uring"
	"mazzy/shared/constants"
	"mazzy/shared/ipc"
	"mazzy/shared/netproto"
)

// clientState holds the per-client NetIPC state, keyed by SenderSID in
// the clients map. respRing and watermark are set at NetMsgConnect.
// conns is populated by NetMsgBindUDP (and NetMsgConnect/TCP in Phase 6);
// rxLoaned by the per-endpoint RX bridge.
type clientState struct {
	sid       int16
	respRing  uint8
	watermark uint8

	// nextConnID is the per-client monotonic ConnID counter. ConnIDs
	// start at 1 (0 is reserved for "invalid") and never wrap.
	nextConnID uint32
	conns      map[uint32]*clientConn

	// txInFlight is the count of TX pages this client has handed off to
	// net.elf that have not yet been TX-completed. Compared against
	// watermark on SendDgram; bumped on accept, decremented in the
	// virtio TX-completion path.
	txInFlight int32

	// rxLoaned is the count of RX pages net.elf currently shares with
	// the client. Bumped by the RX bridge before SendWithRing; step 6's
	// Release handler decrements after the client returns the page.
	// Counts against the same watermark as txInFlight.
	rxLoaned int32
}

// clientConn pairs a per-client connID with the gvisor endpoint it
// resolves to. rxClosed is set under rxMu so a racing deliverOneRxDgram
// either observes the close and frees its own copy, or gets its page
// tracked in rxPages for Close's drain — no orphan mappings.
//
// One struct serves three flavors:
//   - UDP datagram conn (BindUDP): rx bridge fields used; listener
//     fields nil.
//   - TCP listener (BindTCP+Listen): listener fields used; rx bridge
//     fields unused but stay zero-valued.
//   - TCP stream (Accept / TCPConnect): rx bridge fields used (RX
//     bridge lights up in step 5); listener fields nil.
type clientConn struct {
	connID uint32
	sid    int16
	ep     tcpip.Endpoint
	wq     *waiter.Queue

	rxStop  chan struct{}
	rxRearm chan struct{}

	rxMu     sync.Mutex
	rxPages  map[uint64]uintptr
	rxClosed bool

	// TCP listener state — populated when this conn is a listener.
	// acceptStop is closed by handleClose to unblock the accept bridge
	// and any awaitAccept goroutines parked on acceptCh. acceptCh is
	// unbuffered so the bridge naturally throttles gvisor's accept loop
	// to one-at-a-time.
	isListener bool
	acceptStop chan struct{}
	acceptCh   chan acceptedEP

	// TCP stream send page — populated for accepted and active streams.
	// sendPagePtr is the net.elf-side address (used by handleStreamSend
	// to slice the bytes the client wrote); sendPageVA is the client-side
	// VA reported back in AcceptResp/TCPConnectResp and used by the
	// client's StreamSend to copy payload into the page. handleClose
	// frees the page (mem.FreePages on sendPagePtr) when non-zero.
	sendPagePtr uintptr
	sendPageVA  uint64
}

// acceptedEP carries one accepted gvisor endpoint from the accept
// bridge to awaitAccept. Peer is filled in by gvisor's ep.Accept call.
type acceptedEP struct {
	ep   tcpip.Endpoint
	wq   *waiter.Queue
	peer tcpip.FullAddress
}

// allocConnID allocates the next per-client ConnID. ConnID 0 is the
// "invalid" sentinel — on uint32 wraparound, jump straight to 1.
// Caller must hold clientsMu.
func (cs *clientState) allocConnID() uint32 {
	cs.nextConnID++
	if cs.nextConnID == 0 {
		cs.nextConnID = 1
	}
	if cs.conns == nil {
		cs.conns = make(map[uint32]*clientConn)
	}
	return cs.nextConnID
}

// createBoundEndpoint creates a gvisor endpoint of the given transport
// protocol, binds it to (localIP, localPort), and returns the endpoint,
// its waiter queue, and the actually-bound port (ephemeral if the
// caller passed 0). On failure returns nil + a NetErr* code; the
// endpoint is already Close()d on the error path so callers just
// reply and return.
func createBoundEndpoint(proto tcpip.TransportProtocolNumber, localIP [4]byte, localPort uint16) (tcpip.Endpoint, *waiter.Queue, uint16, int16) {
	if globalStack == nil {
		return nil, nil, 0, netproto.NetErrUnknown
	}
	wq := &waiter.Queue{}
	ep, terr := globalStack.NewEndpoint(proto, ipv4.ProtocolNumber, wq)
	if terr != nil {
		return nil, nil, 0, netproto.NetErrUnknown
	}
	addr := tcpip.FullAddress{Port: localPort}
	if localIP != [4]byte{} {
		addr.Addr = tcpip.AddrFrom4(localIP)
	}
	if terr := ep.Bind(addr); terr != nil {
		ep.Close()
		return nil, nil, 0, netproto.NetErrAddrInUse
	}
	bound, terr := ep.GetLocalAddress()
	if terr != nil {
		ep.Close()
		return nil, nil, 0, netproto.NetErrUnknown
	}
	return ep, wq, bound.Port, netproto.NetErrNone
}

var (
	clientsMu sync.Mutex
	clients   = make(map[int16]*clientState)
)

var (
	dbgNetIPCReceived uint64
	dbgNetIPCUnknown  uint64
	dbgConnects       uint64
	dbgBinds          uint64
	dbgSendDgrams     uint64
	dbgSendDgramOK    uint64
	dbgRecvDgrams     uint64
	dbgRecvDropped    uint64
	dbgRxAllocFail    uint64
	dbgRxShareFail    uint64
	dbgReleases       uint64
	dbgReleaseMisses  uint64
	dbgCloses         uint64
	dbgSendRespFail   uint64
	dbgMunmapFail     uint64
)

func handleNetIPC(msg *ipc.UringIPCMsg) {
	atomic.AddUint64(&dbgNetIPCReceived, 1)
	switch netproto.MsgTypeOf(msg) {
	case netproto.NetMsgConnect:
		handleConnect(msg)
	case netproto.NetMsgBindUDP:
		handleBindUDP(msg)
	case netproto.NetMsgSendDgram:
		handleSendDgram(msg)
	case netproto.NetMsgRelease:
		handleRelease(msg)
	case netproto.NetMsgClose:
		handleClose(msg)
	case netproto.NetMsgBindTCP:
		handleBindTCP(msg)
	case netproto.NetMsgListen:
		handleListen(msg)
	case netproto.NetMsgAccept:
		handleAccept(msg)
	case netproto.NetMsgTCPConnect:
		handleTCPConnect(msg)
	case netproto.NetMsgStreamSend:
		handleStreamSend(msg)
	case netproto.NetMsgStreamShutdown:
		handleShutdown(msg)
	default:
		atomic.AddUint64(&dbgNetIPCUnknown, 1)
		fmt.Printf("[gvisor:netipc] unknown type=%d SID=%d\n",
			netproto.MsgTypeOf(msg), msg.SenderSID)
	}
}

// handleConnect records the client's response ring and clamps the
// requested watermark, then acks via uring.SendWithRing on the client's
// declared respRing. Subsequent Connects from the same SID re-arm the
// recorded values (used by clients that repurpose their IPC rings).
func handleConnect(msg *ipc.UringIPCMsg) {
	atomic.AddUint64(&dbgConnects, 1)
	req := netproto.DecodeConnectReq(msg)
	reqID := req.ReqID
	respRing := req.RespRing
	watermark := req.Watermark
	if watermark == 0 {
		watermark = netproto.DefaultTxWatermark
	}
	if watermark > netproto.MaxTxWatermark {
		watermark = netproto.MaxTxWatermark
	}

	clientsMu.Lock()
	cs, ok := clients[msg.SenderSID]
	if !ok {
		cs = &clientState{sid: msg.SenderSID}
		clients[msg.SenderSID] = cs
	}
	cs.respRing = respRing
	cs.watermark = watermark
	clientsMu.Unlock()

	resp := netproto.NetIPCConnectResp{
		ReqID:     reqID,
		ErrCode:   netproto.NetErrNone,
		Watermark: watermark,
	}
	sendResp(msg.SenderSID, respRing, netproto.EncodeConnectResp(&resp))
	fmt.Printf("[gvisor:netipc] Connect SID=%d respRing=%d watermark=%d\n",
		msg.SenderSID, respRing, watermark)
}

// sendResp routes a typed response back via the client's declared
// response ring. Logs on failure so a wrong respRing or a dead client
// shows up in serial output.
func sendResp(targetSID int16, respRing uint8, msg ipc.UringIPCMsg) {
	if err := uring.SendWithRing(int(targetSID), &msg, int(respRing)); err != nil {
		atomic.AddUint64(&dbgSendRespFail, 1)
		fmt.Printf("[gvisor:netipc] response send failed: targetSID=%d ring=%d err=%v\n",
			targetSID, respRing, err)
	}
}

// handleBindUDP creates a gvisor UDP endpoint bound to (LocalIP,
// LocalPort), allocates a per-client ConnID, records it in clientState,
// and replies with BindUDPResp carrying the ConnID and the actual bound
// port (which may have been ephemerally assigned if the client passed
// LocalPort=0).
//
// The endpoint exists but is not yet draining received datagrams — that
// per-endpoint RX reader goroutine lands in Phase 3 step 4. SendDgram
// would land in step 4 as well; for now BindUDP is purely structural.
func handleBindUDP(msg *ipc.UringIPCMsg) {
	atomic.AddUint64(&dbgBinds, 1)
	req := *netproto.DecodeBindUDPReq(msg)

	clientsMu.Lock()
	cs, ok := clients[msg.SenderSID]
	clientsMu.Unlock()
	if !ok {
		// No Connect ⇒ no declared respRing. Sending to a guessed ring
		// would route into the void; the client wouldn't see the error.
		// Drop+log so the client's request times out — same shape as
		// SendDgram/Close on unconnected SIDs.
		fmt.Printf("[gvisor:netipc] BindUDP from unconnected SID=%d, dropping\n",
			msg.SenderSID)
		return
	}

	ep, wq, boundPort, errCode := createBoundEndpoint(udp.ProtocolNumber, req.LocalIP, req.LocalPort)
	if errCode != netproto.NetErrNone {
		bindUDPError(msg.SenderSID, cs.respRing, req.ReqID, errCode, 0)
		return
	}

	clientsMu.Lock()
	connID := cs.allocConnID()
	conn := &clientConn{
		connID:  connID,
		sid:     msg.SenderSID,
		ep:      ep,
		wq:      wq,
		rxStop:  make(chan struct{}),
		rxRearm: make(chan struct{}, 1),
		rxPages: make(map[uint64]uintptr),
	}
	cs.conns[connID] = conn
	respRing := cs.respRing
	clientsMu.Unlock()

	go runRxBridge(conn, cs)

	resp := netproto.BindUDPResp{
		ReqID:     req.ReqID,
		ConnID:    connID,
		LocalPort: boundPort,
		ErrCode:   netproto.NetErrNone,
	}
	sendResp(msg.SenderSID, respRing, netproto.EncodeBindUDPResp(&resp))
	fmt.Printf("[gvisor:netipc] BindUDP SID=%d connID=%d port=%d\n",
		msg.SenderSID, connID, boundPort)
}

func bindUDPError(targetSID int16, respRing uint8, reqID uint32, code int16, connID uint32) {
	resp := netproto.BindUDPResp{ReqID: reqID, ConnID: connID, ErrCode: code}
	sendResp(targetSID, respRing, netproto.EncodeBindUDPResp(&resp))
}

// handleSendDgram sends one UDP datagram from a page the client just
// transferred via sys.TransferDMAClump. The unconnected-SID path can't
// route a response, so it drops + logs instead of replying; every other
// path replies. Every path frees the page — net.elf owns it after the
// transfer and the buddy allocator must get it back.
//
// gvisor's Endpoint.Write copies the payload into its own PacketBuffer
// (see MAZ-29 findings — accepted for v1), so the page is unreferenced
// the instant Write returns.
func handleSendDgram(msg *ipc.UringIPCMsg) {
	atomic.AddUint64(&dbgSendDgrams, 1)
	req := netproto.DecodeSendDgramReq(msg)

	clientsMu.Lock()
	cs, ok := clients[msg.SenderSID]
	if !ok {
		clientsMu.Unlock()
		fmt.Printf("[gvisor:netipc] SendDgram from unconnected SID=%d (dropping; freeing page)\n",
			msg.SenderSID)
		freeTransferredPage(req.PageVA)
		return
	}
	respRing := cs.respRing
	watermark := cs.watermark
	var conn *clientConn
	if cs.conns != nil {
		conn = cs.conns[req.ConnID]
	}
	clientsMu.Unlock()

	fail := func(code int16) {
		resp := netproto.SendDgramResp{ReqID: req.ReqID, ConnID: req.ConnID, ErrCode: code}
		sendResp(msg.SenderSID, respRing, netproto.EncodeSendDgramResp(&resp))
		freeTransferredPage(req.PageVA)
	}

	if conn == nil {
		fail(netproto.NetErrNoConn)
		return
	}
	if uint32(req.Offset)+uint32(req.Length) > constants.PAGE_SIZE {
		fail(netproto.NetErrInvalid)
		return
	}

	if cur := atomic.AddInt32(&cs.txInFlight, 1); cur > int32(watermark) {
		atomic.AddInt32(&cs.txInFlight, -1)
		fail(netproto.NetErrTryAgain)
		return
	}
	defer atomic.AddInt32(&cs.txInFlight, -1)

	payload := unsafe.Slice(
		(*byte)(unsafe.Pointer(uintptr(req.PageVA)+uintptr(req.Offset))),
		int(req.Length),
	)
	to := tcpip.FullAddress{
		Addr: tcpip.AddrFrom4(req.Dst.IP4),
		Port: req.Dst.Port,
	}
	_, terr := conn.ep.Write(bytes.NewReader(payload), tcpip.WriteOptions{To: &to})
	freeTransferredPage(req.PageVA)

	code := netproto.NetErrNone
	if terr != nil {
		code = mapGvisorWriteErr(terr)
		fmt.Printf("[gvisor:netipc] SendDgram Write err=%v SID=%d connID=%d\n",
			terr, msg.SenderSID, req.ConnID)
	} else {
		atomic.AddUint64(&dbgSendDgramOK, 1)
	}
	resp := netproto.SendDgramResp{ReqID: req.ReqID, ConnID: req.ConnID, ErrCode: code}
	sendResp(msg.SenderSID, respRing, netproto.EncodeSendDgramResp(&resp))
}

// freeTransferredPage returns a net.elf-owned transferred page to the
// kernel via munmap. pageVA must be the base VA returned by
// sys.TransferDMAClump; munmap will fail loudly otherwise.
func freeTransferredPage(pageVA uint64) {
	if err := mem.Munmap(uintptr(pageVA), constants.PAGE_SIZE); err != nil {
		atomic.AddUint64(&dbgMunmapFail, 1)
		fmt.Printf("[gvisor:netipc] Munmap(0x%x) failed: %v\n", pageVA, err)
	}
}

// runRxBridge drains inbound datagrams from conn.ep until rxStop closes.
// gvisor's depth-1 ChannelNotifier coalesces bursts, so each wake-up
// must drain to ErrWouldBlock or packets get stuck behind a missed edge.
// rxRearm is the Release-side wake when the bridge had been parked at
// the watermark.
func runRxBridge(conn *clientConn, cs *clientState) {
	waitEntry, notifyCh := waiter.NewChannelEntry(waiter.ReadableEvents)
	conn.wq.EventRegister(&waitEntry)
	defer conn.wq.EventUnregister(&waitEntry)
	respRing := cs.respRing

	// Initial drain in case a packet landed between Bind and EventRegister.
	for deliverOneRxDgram(conn, cs, respRing) {
	}
	for {
		select {
		case <-conn.rxStop:
			return
		case <-conn.rxRearm:
		case <-notifyCh:
		}
		for deliverOneRxDgram(conn, cs, respRing) {
		}
	}
}

func deliverOneRxDgram(conn *clientConn, cs *clientState, respRing uint8) bool {
	if atomic.LoadInt32(&cs.rxLoaned) >= int32(cs.watermark) {
		return false
	}

	pageBytes, err := mem.AllocPagesSlice(1, mem.PageShared)
	if err != nil {
		atomic.AddUint64(&dbgRxAllocFail, 1)
		fmt.Printf("[gvisor:netipc] RX AllocPagesSlice failed: %v\n", err)
		return false
	}
	pagePtr := unsafe.Pointer(&pageBytes[0])
	sw := tcpip.SliceWriter(pageBytes)
	res, terr := conn.ep.Read(&sw, tcpip.ReadOptions{NeedRemoteAddr: true})
	if terr != nil {
		_ = mem.FreePages(pagePtr, 1)
		if _, ok := terr.(*tcpip.ErrWouldBlock); !ok {
			atomic.AddUint64(&dbgRecvDropped, 1)
			fmt.Printf("[gvisor:netipc] RX Read err=%v connID=%d\n", terr, conn.connID)
		}
		return false
	}

	clientVA, err := sys.SharePagesWithTarget(int(conn.sid), uintptr(pagePtr), 1)
	if err != nil {
		atomic.AddUint64(&dbgRxShareFail, 1)
		fmt.Printf("[gvisor:netipc] RX SharePagesWithTarget(sid=%d) failed: %v\n",
			conn.sid, err)
		_ = mem.FreePages(pagePtr, 1)
		return false
	}

	conn.rxMu.Lock()
	if conn.rxClosed {
		conn.rxMu.Unlock()
		// Close raced ahead of us; drop our side. The client's side is
		// an orphan that lives until shepherd death.
		_ = mem.FreePages(pagePtr, 1)
		return false
	}
	conn.rxPages[uint64(clientVA)] = uintptr(pagePtr)
	conn.rxMu.Unlock()
	atomic.AddInt32(&cs.rxLoaned, 1)

	src := netproto.Addr{Port: res.RemoteAddr.Port}
	if res.RemoteAddr.Addr.Len() == 4 {
		src.IP4 = res.RemoteAddr.Addr.As4()
	}
	notif := netproto.RecvDgram{
		ConnID: conn.connID,
		Length: uint16(res.Count),
		Src:    src,
		PageVA: uint64(clientVA),
	}
	notifyMsg := netproto.EncodeRecvDgram(&notif)
	if err := uring.SendWithRing(int(conn.sid), &notifyMsg, int(respRing)); err != nil {
		atomic.AddUint64(&dbgSendRespFail, 1)
		fmt.Printf("[gvisor:netipc] RecvDgram send failed: sid=%d ring=%d err=%v\n",
			conn.sid, respRing, err)
		// Page is recorded; Close (or shepherd death) will reclaim.
		return true
	}
	atomic.AddUint64(&dbgRecvDgrams, 1)
	return true
}

func handleRelease(msg *ipc.UringIPCMsg) {
	atomic.AddUint64(&dbgReleases, 1)
	req := netproto.DecodeReleaseReq(msg)

	clientsMu.Lock()
	cs, ok := clients[msg.SenderSID]
	if !ok {
		clientsMu.Unlock()
		atomic.AddUint64(&dbgReleaseMisses, 1)
		fmt.Printf("[gvisor:netipc] Release from unconnected SID=%d, dropping\n",
			msg.SenderSID)
		return
	}
	var conn *clientConn
	if cs.conns != nil {
		conn = cs.conns[req.ConnID]
	}
	clientsMu.Unlock()
	if conn == nil {
		atomic.AddUint64(&dbgReleaseMisses, 1)
		fmt.Printf("[gvisor:netipc] Release unknown connID=%d SID=%d, dropping\n",
			req.ConnID, msg.SenderSID)
		return
	}

	conn.rxMu.Lock()
	netPtr, found := conn.rxPages[req.PageVA]
	if found {
		delete(conn.rxPages, req.PageVA)
	}
	conn.rxMu.Unlock()
	if !found {
		atomic.AddUint64(&dbgReleaseMisses, 1)
		fmt.Printf("[gvisor:netipc] Release unknown PageVA=0x%x connID=%d, dropping\n",
			req.PageVA, req.ConnID)
		return
	}

	if err := mem.FreePages(unsafe.Pointer(netPtr), 1); err != nil {
		fmt.Printf("[gvisor:netipc] Release FreePages(0x%x) failed: %v\n",
			uint64(netPtr), err)
	}
	// Only wake the bridge when we transition out of the watermark — at
	// any other depth the bridge is either running or will be woken by
	// gvisor's own notifyCh on the next inbound packet.
	if atomic.AddInt32(&cs.rxLoaned, -1) == int32(cs.watermark)-1 {
		select {
		case conn.rxRearm <- struct{}{}:
		default:
		}
	}
}

// handleClose tears down a connID. Unconnected SID = drop+log (no
// respRing to route a reply through).
func handleClose(msg *ipc.UringIPCMsg) {
	atomic.AddUint64(&dbgCloses, 1)
	req := netproto.DecodeCloseReq(msg)

	clientsMu.Lock()
	cs, ok := clients[msg.SenderSID]
	if !ok {
		clientsMu.Unlock()
		fmt.Printf("[gvisor:netipc] Close from unconnected SID=%d, dropping\n",
			msg.SenderSID)
		return
	}
	respRing := cs.respRing
	var conn *clientConn
	if cs.conns != nil {
		conn = cs.conns[req.ConnID]
		if conn != nil {
			delete(cs.conns, req.ConnID)
		}
	}
	clientsMu.Unlock()
	if conn == nil {
		resp := netproto.CloseResp{ReqID: req.ReqID, ErrCode: netproto.NetErrNoConn}
		sendResp(msg.SenderSID, respRing, netproto.EncodeCloseResp(&resp))
		return
	}

	if conn.isListener {
		if conn.acceptStop != nil {
			close(conn.acceptStop)
		}
	} else {
		close(conn.rxStop)
	}
	conn.ep.Close()

	conn.rxMu.Lock()
	conn.rxClosed = true
	freed := 0
	for _, netPtr := range conn.rxPages {
		if err := mem.FreePages(unsafe.Pointer(netPtr), 1); err != nil {
			fmt.Printf("[gvisor:netipc] Close FreePages(0x%x) failed: %v\n",
				uint64(netPtr), err)
			continue
		}
		freed++
	}
	conn.rxPages = nil
	conn.rxMu.Unlock()
	if freed > 0 {
		atomic.AddInt32(&cs.rxLoaned, -int32(freed))
	}

	// Free the per-stream send page if this is a TCP stream conn.
	// PD_NET_OWNED_SHARED keeps the client's mapping alive until shepherd
	// death; net.elf-side FreePages just drops our own RefCount on the
	// page, which is correct since we no longer need access.
	if conn.sendPagePtr != 0 {
		if err := mem.FreePages(unsafe.Pointer(conn.sendPagePtr), 1); err != nil {
			fmt.Printf("[gvisor:netipc] Close send-page FreePages(0x%x) failed: %v\n",
				uint64(conn.sendPagePtr), err)
		}
	}

	resp := netproto.CloseResp{ReqID: req.ReqID, ErrCode: netproto.NetErrNone}
	sendResp(msg.SenderSID, respRing, netproto.EncodeCloseResp(&resp))
	fmt.Printf("[gvisor:netipc] Close SID=%d connID=%d (reclaimed %d RX pages)\n",
		msg.SenderSID, req.ConnID, freed)
}

func mapGvisorWriteErr(terr tcpip.Error) int16 {
	switch terr.(type) {
	case *tcpip.ErrInvalidEndpointState,
		*tcpip.ErrInvalidOptionValue,
		*tcpip.ErrMessageTooLong,
		*tcpip.ErrBadBuffer,
		*tcpip.ErrDestinationRequired:
		return netproto.NetErrInvalid
	case *tcpip.ErrNoBufferSpace:
		return netproto.NetErrNoMemory
	case *tcpip.ErrWouldBlock:
		return netproto.NetErrTryAgain
	case *tcpip.ErrClosedForSend:
		return netproto.NetErrPipe
	case *tcpip.ErrConnectionReset:
		return netproto.NetErrConnReset
	case *tcpip.ErrNotConnected:
		return netproto.NetErrNotConn
	default:
		return netproto.NetErrUnknown
	}
}
