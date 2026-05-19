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
	"fmt"
	"sync"
	"sync/atomic"

	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/transport/udp"
	"gvisor.dev/gvisor/pkg/waiter"

	"mazzy/mazarin/uring"
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
}

// clientConn pairs a per-client connID with the gvisor endpoint it
// resolves to. The waiter.Queue is what the per-endpoint RX reader
// goroutine sleeps on once Phase 3 step 4 lands.
type clientConn struct {
	connID uint32
	sid    int16
	ep     tcpip.Endpoint
	wq     *waiter.Queue
}

var (
	clientsMu sync.Mutex
	clients   = make(map[int16]*clientState)
)

// Debug counters bumped via atomic.AddUint64.
var (
	dbgNetIPCReceived uint64
	dbgNetIPCUnknown  uint64
	dbgConnects       uint64
	dbgBinds          uint64
	dbgNotConnected   uint64
	dbgSendRespFail   uint64
)

func handleNetIPC(msg *ipc.UringIPCMsg) {
	atomic.AddUint64(&dbgNetIPCReceived, 1)
	switch netproto.MsgTypeOf(msg) {
	case netproto.NetMsgConnect:
		handleConnect(msg)
	case netproto.NetMsgBindUDP:
		handleBindUDP(msg)
	case netproto.NetMsgSendDgram:
		rejectNotConnected(msg, "SendDgram")
	case netproto.NetMsgRelease:
		// Release has no response; just log if unconnected (drop).
		if !clientConnected(msg.SenderSID) {
			fmt.Printf("[gvisor:netipc] Release from unconnected SID=%d, dropping\n", msg.SenderSID)
		}
	case netproto.NetMsgClose:
		rejectNotConnected(msg, "Close")
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

// clientConnected reports whether the client has sent NetMsgConnect.
// Used by stub handlers in this step; later steps will look up
// per-endpoint state under the same lock.
func clientConnected(sid int16) bool {
	clientsMu.Lock()
	_, ok := clients[sid]
	clientsMu.Unlock()
	return ok
}

// rejectNotConnected is a placeholder for Bind/Send/Close while their
// real handlers are still being wired in. Once each lands, the
// corresponding case in handleNetIPC stops calling this and synthesizes
// a typed response (BindUDPResp / SendDgramResp / CloseResp) with
// ErrCode=NetErrInvalid on the unconnected path.
func rejectNotConnected(msg *ipc.UringIPCMsg, op string) {
	atomic.AddUint64(&dbgNotConnected, 1)
	if !clientConnected(msg.SenderSID) {
		fmt.Printf("[gvisor:netipc] %s from unconnected SID=%d, dropping\n", op, msg.SenderSID)
		return
	}
	fmt.Printf("[gvisor:netipc] %s from connected SID=%d (handler stub)\n", op, msg.SenderSID)
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
		bindUDPError(msg.SenderSID, 0, req.ReqID, netproto.NetErrInvalid, 0)
		fmt.Printf("[gvisor:netipc] BindUDP from unconnected SID=%d\n", msg.SenderSID)
		return
	}

	if globalStack == nil {
		bindUDPError(msg.SenderSID, cs.respRing, req.ReqID, netproto.NetErrUnknown, 0)
		return
	}

	wq := &waiter.Queue{}
	ep, terr := globalStack.NewEndpoint(udp.ProtocolNumber, ipv4.ProtocolNumber, wq)
	if terr != nil {
		bindUDPError(msg.SenderSID, cs.respRing, req.ReqID, netproto.NetErrUnknown, 0)
		fmt.Printf("[gvisor:netipc] BindUDP NewEndpoint err=%v SID=%d\n", terr, msg.SenderSID)
		return
	}

	addr := tcpip.FullAddress{Port: req.LocalPort}
	if req.LocalIP != [4]byte{} {
		addr.Addr = tcpip.AddrFrom4(req.LocalIP)
	}
	if terr := ep.Bind(addr); terr != nil {
		ep.Close()
		bindUDPError(msg.SenderSID, cs.respRing, req.ReqID, netproto.NetErrAddrInUse, 0)
		fmt.Printf("[gvisor:netipc] BindUDP Bind err=%v SID=%d\n", terr, msg.SenderSID)
		return
	}
	bound, terr := ep.GetLocalAddress()
	if terr != nil {
		ep.Close()
		bindUDPError(msg.SenderSID, cs.respRing, req.ReqID, netproto.NetErrUnknown, 0)
		return
	}

	clientsMu.Lock()
	cs.nextConnID++
	if cs.nextConnID == 0 {
		cs.nextConnID = 1
	}
	connID := cs.nextConnID
	if cs.conns == nil {
		cs.conns = make(map[uint32]*clientConn)
	}
	cs.conns[connID] = &clientConn{connID: connID, sid: msg.SenderSID, ep: ep, wq: wq}
	respRing := cs.respRing
	clientsMu.Unlock()

	resp := netproto.BindUDPResp{
		ReqID:     req.ReqID,
		ConnID:    connID,
		LocalPort: bound.Port,
		ErrCode:   netproto.NetErrNone,
	}
	sendResp(msg.SenderSID, respRing, netproto.EncodeBindUDPResp(&resp))
	fmt.Printf("[gvisor:netipc] BindUDP SID=%d connID=%d port=%d\n",
		msg.SenderSID, connID, bound.Port)
}

func bindUDPError(targetSID int16, respRing uint8, reqID uint32, code int16, connID uint32) {
	resp := netproto.BindUDPResp{ReqID: reqID, ConnID: connID, ErrCode: code}
	sendResp(targetSID, respRing, netproto.EncodeBindUDPResp(&resp))
}
