// netipc_tcp.go — TCP NetIPC handlers inside the gvisor.maz plugin.
//
// Surface: BindTCP (create listener-shaped endpoint), Listen (turn it
// into a listening socket and start the accept bridge), Accept (await
// one inbound stream), TCPConnect (active open). Handler entrypoints
// live in this file; the dispatcher in netipc.go switches into them.
//
// Async paths spawn goroutines rather than blocking the NetIPC reader:
// handleAccept always defers to awaitAccept, and handleTCPConnect spawns
// a waiter goroutine when gvisor returns ErrConnectStarted.
package main

import (
	"fmt"
	"sync/atomic"

	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
	"gvisor.dev/gvisor/pkg/waiter"

	"mazzy/shared/ipc"
	"mazzy/shared/netproto"
)

var (
	dbgBindTCP        uint64
	dbgListen         uint64
	dbgAccept         uint64
	dbgAcceptDelivers uint64
	dbgAcceptAborts   uint64
	dbgTCPConnect     uint64
	dbgTCPConnectAOK  uint64
)

// handleBindTCP creates a TCP endpoint at (LocalIP, LocalPort), Binds
// it, and registers it as a listener-shaped clientConn. The endpoint is
// not yet listening — handleListen flips it on with ep.Listen.
func handleBindTCP(msg *ipc.UringIPCMsg) {
	atomic.AddUint64(&dbgBindTCP, 1)
	req := *netproto.DecodeBindTCPReq(msg)

	clientsMu.Lock()
	cs, ok := clients[msg.SenderSID]
	clientsMu.Unlock()
	if !ok {
		fmt.Printf("[gvisor:netipc] BindTCP from unconnected SID=%d, dropping\n",
			msg.SenderSID)
		return
	}

	ep, wq, boundPort, errCode := createBoundEndpoint(tcp.ProtocolNumber, req.LocalIP, req.LocalPort)
	if errCode != netproto.NetErrNone {
		bindTCPError(msg.SenderSID, cs.respRing, req.ReqID, errCode, 0)
		return
	}

	clientsMu.Lock()
	connID := cs.allocConnID()
	// Listener-conns leave rxStop/rxRearm/rxPages nil — no RX bridge
	// ever runs against a listener; handleClose's listener branch
	// keys off isListener and never touches the rx fields.
	conn := &clientConn{
		connID:     connID,
		sid:        msg.SenderSID,
		ep:         ep,
		wq:         wq,
		isListener: true,
	}
	cs.conns[connID] = conn
	respRing := cs.respRing
	clientsMu.Unlock()

	resp := netproto.BindTCPResp{
		ReqID:     req.ReqID,
		ConnID:    connID,
		LocalPort: boundPort,
		ErrCode:   netproto.NetErrNone,
	}
	sendResp(msg.SenderSID, respRing, netproto.EncodeBindTCPResp(&resp))
	fmt.Printf("[gvisor:netipc] BindTCP SID=%d connID=%d port=%d\n",
		msg.SenderSID, connID, boundPort)
}

func bindTCPError(targetSID int16, respRing uint8, reqID uint32, code int16, connID uint32) {
	resp := netproto.BindTCPResp{ReqID: reqID, ConnID: connID, ErrCode: code}
	sendResp(targetSID, respRing, netproto.EncodeBindTCPResp(&resp))
}

// handleListen calls ep.Listen on a listener-shaped conn and spawns the
// accept bridge. Subsequent NetMsgAccept requests get fed from the
// bridge's acceptCh.
func handleListen(msg *ipc.UringIPCMsg) {
	atomic.AddUint64(&dbgListen, 1)
	req := *netproto.DecodeListenReq(msg)

	clientsMu.Lock()
	cs, ok := clients[msg.SenderSID]
	var conn *clientConn
	if ok && cs.conns != nil {
		conn = cs.conns[req.ConnID]
	}
	clientsMu.Unlock()
	if !ok {
		fmt.Printf("[gvisor:netipc] Listen from unconnected SID=%d, dropping\n",
			msg.SenderSID)
		return
	}
	respRing := cs.respRing
	if conn == nil {
		listenReply(msg.SenderSID, respRing, req.ReqID, netproto.NetErrNoConn)
		return
	}
	if !conn.isListener {
		listenReply(msg.SenderSID, respRing, req.ReqID, netproto.NetErrInvalid)
		return
	}

	backlog := int(req.Backlog)
	if backlog <= 0 {
		backlog = 1
	}
	if terr := conn.ep.Listen(backlog); terr != nil {
		listenReply(msg.SenderSID, respRing, req.ReqID, netproto.NetErrUnknown)
		fmt.Printf("[gvisor:netipc] Listen ep.Listen err=%v SID=%d connID=%d\n",
			terr, msg.SenderSID, req.ConnID)
		return
	}

	clientsMu.Lock()
	// Idempotent: a re-Listen on the same conn just re-uses the bridge.
	if conn.acceptStop == nil {
		conn.acceptStop = make(chan struct{})
		conn.acceptCh = make(chan acceptedEP)
		go runAcceptBridge(conn)
	}
	clientsMu.Unlock()

	listenReply(msg.SenderSID, respRing, req.ReqID, netproto.NetErrNone)
	fmt.Printf("[gvisor:netipc] Listen SID=%d connID=%d backlog=%d\n",
		msg.SenderSID, req.ConnID, backlog)
}

func listenReply(targetSID int16, respRing uint8, reqID uint32, code int16) {
	resp := netproto.ListenResp{ReqID: reqID, ErrCode: code}
	sendResp(targetSID, respRing, netproto.EncodeListenResp(&resp))
}

// runAcceptBridge drains gvisor's listener via Accept loop and pushes
// each accepted endpoint to acceptCh (unbuffered — naturally throttles
// to one-at-a-time). Exits on acceptStop.
func runAcceptBridge(conn *clientConn) {
	waitEntry, notifyCh := waiter.NewChannelEntry(waiter.ReadableEvents)
	conn.wq.EventRegister(&waitEntry)
	defer conn.wq.EventUnregister(&waitEntry)

	for {
		for {
			var peer tcpip.FullAddress
			newEP, newWQ, terr := conn.ep.Accept(&peer)
			if terr != nil {
				if _, ok := terr.(*tcpip.ErrWouldBlock); ok {
					break
				}
				// Anything other than ErrWouldBlock is terminal — the
				// listener is in a bad state and retrying on every
				// notifyCh edge would tight-spin.
				fmt.Printf("[gvisor:netipc] accept bridge terminal err=%v listener=%d\n",
					terr, conn.connID)
				return
			}
			select {
			case conn.acceptCh <- acceptedEP{ep: newEP, wq: newWQ, peer: peer}:
			case <-conn.acceptStop:
				newEP.Close()
				return
			}
		}
		select {
		case <-conn.acceptStop:
			return
		case <-notifyCh:
		}
	}
}

// handleAccept spawns awaitAccept so the dispatcher returns immediately
// — Accept can block for arbitrary duration waiting for an inbound
// connection.
func handleAccept(msg *ipc.UringIPCMsg) {
	atomic.AddUint64(&dbgAccept, 1)
	req := *netproto.DecodeAcceptReq(msg)
	sid := msg.SenderSID

	clientsMu.Lock()
	cs, ok := clients[sid]
	var listener *clientConn
	if ok && cs.conns != nil {
		listener = cs.conns[req.ConnID]
	}
	clientsMu.Unlock()
	if !ok {
		fmt.Printf("[gvisor:netipc] Accept from unconnected SID=%d, dropping\n", sid)
		return
	}
	respRing := cs.respRing
	if listener == nil {
		acceptError(sid, respRing, req.ReqID, netproto.NetErrNoConn)
		return
	}
	if !listener.isListener || listener.acceptCh == nil {
		acceptError(sid, respRing, req.ReqID, netproto.NetErrInvalid)
		return
	}

	go awaitAccept(cs, listener, sid, req.ReqID, respRing)
}

// awaitAccept blocks on the listener's acceptCh until either an inbound
// stream arrives (reply AcceptResp{NewConnID, ...}) or the listener is
// closed (reply NetErrConnAborted).
func awaitAccept(cs *clientState, listener *clientConn, sid int16, reqID uint32, respRing uint8) {
	select {
	case accepted := <-listener.acceptCh:
		clientsMu.Lock()
		newConnID := cs.allocConnID()
		newConn := &clientConn{
			connID:  newConnID,
			sid:     sid,
			ep:      accepted.ep,
			wq:      accepted.wq,
			rxStop:  make(chan struct{}),
			rxRearm: make(chan struct{}, 1),
			rxPages: make(map[uint64]uintptr),
		}
		cs.conns[newConnID] = newConn
		clientsMu.Unlock()

		peer := netproto.Addr{Port: accepted.peer.Port}
		if accepted.peer.Addr.Len() == 4 {
			peer.IP4 = accepted.peer.Addr.As4()
		}
		// SendPageVA is zero until per-stream send pages land; clients
		// must wait on that mapping before calling StreamSend.
		resp := netproto.AcceptResp{
			ReqID:     reqID,
			NewConnID: newConnID,
			Peer:      peer,
			ErrCode:   netproto.NetErrNone,
		}
		sendResp(sid, respRing, netproto.EncodeAcceptResp(&resp))
		atomic.AddUint64(&dbgAcceptDelivers, 1)
		fmt.Printf("[gvisor:netipc] Accept SID=%d listener=%d → connID=%d peer=%v:%d\n",
			sid, listener.connID, newConnID, peer.IP4, peer.Port)
	case <-listener.acceptStop:
		atomic.AddUint64(&dbgAcceptAborts, 1)
		acceptError(sid, respRing, reqID, netproto.NetErrConnAborted)
	}
}

func acceptError(sid int16, respRing uint8, reqID uint32, code int16) {
	resp := netproto.AcceptResp{ReqID: reqID, ErrCode: code}
	sendResp(sid, respRing, netproto.EncodeAcceptResp(&resp))
}

// handleTCPConnect performs an active open against Dst. The waiter is
// armed *before* ep.Connect so a fast (loopback) ESTABLISHED transition
// doesn't slip past us. Sync success / sync failure paths reply inline;
// ErrConnectStarted spawns a waiter goroutine that handles the
// completion notify.
func handleTCPConnect(msg *ipc.UringIPCMsg) {
	atomic.AddUint64(&dbgTCPConnect, 1)
	req := *netproto.DecodeTCPConnectReq(msg)
	sid := msg.SenderSID

	clientsMu.Lock()
	cs, ok := clients[sid]
	clientsMu.Unlock()
	if !ok {
		fmt.Printf("[gvisor:netipc] TCPConnect from unconnected SID=%d, dropping\n", sid)
		return
	}
	respRing := cs.respRing

	if globalStack == nil {
		tcpConnectError(sid, respRing, req.ReqID, netproto.NetErrUnknown)
		return
	}

	wq := &waiter.Queue{}
	ep, terr := globalStack.NewEndpoint(tcp.ProtocolNumber, ipv4.ProtocolNumber, wq)
	if terr != nil {
		tcpConnectError(sid, respRing, req.ReqID, netproto.NetErrUnknown)
		fmt.Printf("[gvisor:netipc] TCPConnect NewEndpoint err=%v SID=%d\n", terr, sid)
		return
	}

	if req.LocalPort != 0 || req.LocalIP != [4]byte{} {
		addr := tcpip.FullAddress{Port: req.LocalPort}
		if req.LocalIP != [4]byte{} {
			addr.Addr = tcpip.AddrFrom4(req.LocalIP)
		}
		if terr := ep.Bind(addr); terr != nil {
			ep.Close()
			tcpConnectError(sid, respRing, req.ReqID, netproto.NetErrAddrInUse)
			return
		}
	}

	waitEntry, notifyCh := waiter.NewChannelEntry(
		waiter.WritableEvents | waiter.EventErr | waiter.EventHUp)
	wq.EventRegister(&waitEntry)

	remote := tcpip.FullAddress{
		Addr: tcpip.AddrFrom4(req.Dst.IP4),
		Port: req.Dst.Port,
	}
	terr = ep.Connect(remote)
	if terr == nil {
		wq.EventUnregister(&waitEntry)
		completeTCPConnect(cs, sid, respRing, req.ReqID, ep, wq)
		return
	}
	if _, async := terr.(*tcpip.ErrConnectStarted); !async {
		wq.EventUnregister(&waitEntry)
		ep.Close()
		tcpConnectError(sid, respRing, req.ReqID, mapGvisorConnectErr(terr))
		fmt.Printf("[gvisor:netipc] TCPConnect ep.Connect sync err=%v SID=%d\n", terr, sid)
		return
	}

	go func() {
		<-notifyCh
		wq.EventUnregister(&waitEntry)
		if cerr := ep.LastError(); cerr != nil {
			ep.Close()
			tcpConnectError(sid, respRing, req.ReqID, mapGvisorConnectErr(cerr))
			fmt.Printf("[gvisor:netipc] TCPConnect async err=%v SID=%d\n", cerr, sid)
			return
		}
		completeTCPConnect(cs, sid, respRing, req.ReqID, ep, wq)
	}()
}

// completeTCPConnect finishes registration for a successfully-connected
// stream endpoint: allocates a per-client connID, records clientConn
// state, and replies TCPConnectResp{ConnID, LocalPort}. SendPageVA stays
// 0 until step 4 wires the per-stream send page.
func completeTCPConnect(cs *clientState, sid int16, respRing uint8, reqID uint32,
	ep tcpip.Endpoint, wq *waiter.Queue) {

	bound, terr := ep.GetLocalAddress()
	if terr != nil {
		ep.Close()
		tcpConnectError(sid, respRing, reqID, netproto.NetErrUnknown)
		return
	}

	clientsMu.Lock()
	connID := cs.allocConnID()
	conn := &clientConn{
		connID:  connID,
		sid:     sid,
		ep:      ep,
		wq:      wq,
		rxStop:  make(chan struct{}),
		rxRearm: make(chan struct{}, 1),
		rxPages: make(map[uint64]uintptr),
	}
	cs.conns[connID] = conn
	clientsMu.Unlock()

	// SendPageVA is zero until per-stream send pages land; clients
	// must wait on that mapping before calling StreamSend.
	resp := netproto.TCPConnectResp{
		ReqID:     reqID,
		ConnID:    connID,
		LocalPort: bound.Port,
		ErrCode:   netproto.NetErrNone,
	}
	sendResp(sid, respRing, netproto.EncodeTCPConnectResp(&resp))
	atomic.AddUint64(&dbgTCPConnectAOK, 1)
	fmt.Printf("[gvisor:netipc] TCPConnect SID=%d connID=%d localPort=%d\n",
		sid, connID, bound.Port)
}

func tcpConnectError(sid int16, respRing uint8, reqID uint32, code int16) {
	resp := netproto.TCPConnectResp{ReqID: reqID, ErrCode: code}
	sendResp(sid, respRing, netproto.EncodeTCPConnectResp(&resp))
}

// mapGvisorConnectErr translates the gvisor errors a TCP Connect can
// surface into the wire-protocol NetErr* codes. Anything not explicitly
// listed falls back to NetErrUnknown so debug logs catch new cases.
func mapGvisorConnectErr(terr tcpip.Error) int16 {
	switch terr.(type) {
	case *tcpip.ErrConnectionRefused:
		return netproto.NetErrConnRefused
	case *tcpip.ErrConnectionReset:
		return netproto.NetErrConnReset
	case *tcpip.ErrConnectionAborted:
		return netproto.NetErrConnAborted
	case *tcpip.ErrAlreadyConnected:
		return netproto.NetErrInvalid
	case *tcpip.ErrHostUnreachable, *tcpip.ErrNetworkUnreachable, *tcpip.ErrHostDown:
		return netproto.NetErrConnRefused
	case *tcpip.ErrTimeout:
		return netproto.NetErrConnRefused
	case *tcpip.ErrInvalidEndpointState, *tcpip.ErrBadLocalAddress:
		return netproto.NetErrInvalid
	default:
		return netproto.NetErrUnknown
	}
}
