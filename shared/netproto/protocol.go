// Package netproto defines the uring IPC protocol between client shepherds
// and net.elf for socket-shaped networking. Phase 3 of MAZ-29 lit up the
// UDP path; Phase 6 adds the TCP surface (BindTCP/Listen/Accept/TCPConnect/
// StreamSend/StreamRx/StreamShutdown).
//
// # Page lifetimes
//
// TX (client → wire): the client allocates a 1-page MAZARIN_CONTIGUOUS clump
// via mem.AllocContiguous, fills it at offset = Headroom, then calls
// sys.TransferDMAClump to hand the page to net.elf. Net.elf owns the page
// after the transfer and releases it after virtio TX completion. The IPC
// payload (SendDgramReq) carries the net.elf-side VA returned by the
// transfer syscall.
//
// RX (wire → client): net.elf receives a packet into one of its own RX pages,
// parses through gvisor, then maps the payload page into the client via the
// existing sys.SharePagesWithTarget primitive. Net.elf retains ownership; the
// client returns the page when done via NetIPCRelease. If the client never
// releases, net.elf reclaims on client death.
//
// # Routing and connID/listenID
//
// All requests carry implicit routing via UringIPCMsg.SenderSID; net.elf
// maintains per-client state keyed by SID. Within one client, ConnIDs and
// ListenIDs are u32 handles allocated by net.elf as a monotonic per-client
// counter (never reused within a client's lifetime). UDP "connections" are
// just bound endpoints — the same Conn handle accepts both Connect+Send and
// SendTo with an explicit destination.
//
// # Connect / response ring
//
// Before any other operation the client sends NetMsgConnect, declaring the
// IPC ring index on its own side that net.elf should write responses to
// (RespRing). Net.elf records that index in per-client state and uses
// uring.SendWithRing(clientSID, msg, respRing) for every subsequent
// response and unsolicited RecvDgram / StreamRx delivery. This mirrors
// fs's OpConnect pattern exactly — the server doesn't care which ring the
// client picked, it just stores the number. NetMsgConnect must precede
// NetMsgBindUDP/NetMsgBindTCP/NetMsgTCPConnect for a given client; later
// Connects re-arm the ring index and re-grant the watermark. NetMsgConnect
// is the NetIPC handshake; NetMsgTCPConnect is the unrelated TCP active
// open.
//
// # TCP stream send page
//
// At Accept-success and TCPConnect-success net.elf maps a 1-page RW send
// scratch into the client (via SyscallShareNetPageWithClient). Its
// client-side VA rides back in AcceptResp.SendPageVA /
// TCPConnectResp.SendPageVA. To transmit, the client writes payload bytes
// into the page starting at offset 0, then sends StreamSendReq carrying
// the byte length. Net.elf reads those bytes and hands them to gvisor's
// Endpoint.Write. For v1 there is at most one StreamSend in flight per
// stream — the client must await StreamSendResp before reusing the page.
// Real ring semantics (producer/consumer cursors on the shared page) are
// a future optimization.
//
// # TCP stream RX
//
// Stream-side RX uses the same page-loan shape as UDP. Net.elf allocates
// an RX page, reads up to one page of stream bytes into it via gvisor's
// Endpoint.Read, maps it into the client via SharePagesWithTarget, and
// sends an unsolicited StreamRx{ConnID, PageVA, Offset, Length, Flags}.
// The client must Release(ConnID, PageVA) when done. Flags bit 0
// (StreamRxFlagEOF) means the peer half-closed the write side; an
// EOF-only delivery may carry Length=0 and PageVA=0 (no Release needed).
//
// # Watermark
//
// Each client may have at most Watermark TX pages in flight at net.elf at
// any time. Clients request a value at NetMsgConnect; net.elf clamps to
// [1, MaxTxWatermark] and reports the granted value back in
// NetMsgConnectResp. A request of 0 means "use default"
// (DefaultTxWatermark). Watermark is a per-client policy that applies
// across all of the client's endpoints, not per-endpoint.
//
// # Request / response correlation
//
// Every request type carries a uint32 ReqID at body offset 0; net.elf
// MUST echo that ReqID verbatim in the corresponding response. Clients
// drain a single uring response ring across all in-flight requests
// (BindUDP, SendDgram, Close are independently pipelineable) and a
// response is matched to its request by ReqID, not by arrival order.
// Unsolicited messages (currently only RecvDgram) carry no ReqID and
// are demuxed by MsgType + ConnID instead.
//
// Failing to echo ReqID or routing a response to a guessed ring (e.g.
// before Connect has declared one) silently strands the client —
// responses go into the void and the client times out. Handlers that
// can't resolve a respRing (no Connect state) MUST drop+log rather than
// guess.
//
// # Wire format
//
// All messages fit in the 112-byte UringIPCMsg.Payload. Layout per slot:
//
//	[0:4]   uint32 MsgType (NetMsg* constants below)
//	[4:108] message body (≤108 bytes; struct definitions in types.go)
package netproto

import (
	"mazzy/shared/ipc"
	"unsafe"
)

// --- Watermark constants ---

const (
	// DefaultTxWatermark is the per-client outstanding-TX-page cap if the
	// client doesn't specify one at BindUDP. See MAZ-29 findings.md §3 for
	// the rationale (8 × 1500 B = 12 KB in-flight per client; bounded
	// net.elf-side state per misbehaving client).
	DefaultTxWatermark uint8 = 8
	// MaxTxWatermark is the kernel-imposed upper bound, matching the
	// client's MaxDMAClumps = 16 per-shepherd ceiling. Clients requesting
	// more get clamped to this value.
	MaxTxWatermark uint8 = 16
)

// --- Error codes (echoed in response ErrCode fields) ---
//
// Values match Linux errno conventions where applicable, so the linux
// shepherd's POSIX veneer can pass them through. Negative because clients
// read them as int16 (sign-preserving).

const (
	NetErrNone        int16 = 0
	NetErrInvalid     int16 = -22  // EINVAL
	NetErrNoConn      int16 = -2   // ENOENT — unknown ConnID for this client
	NetErrNoMemory    int16 = -12  // ENOMEM
	NetErrTryAgain    int16 = -11  // EAGAIN — TX watermark reached, retry after a release/completion
	NetErrAddrInUse   int16 = -98  // EADDRINUSE
	NetErrPipe        int16 = -32  // EPIPE — write on a stream whose write side is closed
	NetErrConnReset   int16 = -104 // ECONNRESET — peer reset the connection
	NetErrConnAborted int16 = -103 // ECONNABORTED — accept-side abort
	NetErrNotConn     int16 = -107 // ENOTCONN — operation on a not-yet-established stream
	NetErrConnRefused int16 = -111 // ECONNREFUSED — peer refused the connect
	NetErrUnknown     int16 = -1   // generic
)

// --- MsgType discriminators ---
//
// Request and response IDs share a single namespace per protocol direction
// so a future "any-decode" helper can branch on the constant alone.

// Request types (client → net.elf, via ProtoNetIPCReq).
const (
	NetMsgConnect        uint32 = 1
	NetMsgBindUDP        uint32 = 2
	NetMsgSendDgram      uint32 = 3
	NetMsgRelease        uint32 = 4
	NetMsgClose          uint32 = 5
	NetMsgBindTCP        uint32 = 6
	NetMsgListen         uint32 = 7
	NetMsgAccept         uint32 = 8
	NetMsgTCPConnect     uint32 = 9
	NetMsgStreamSend     uint32 = 10
	NetMsgStreamShutdown uint32 = 11
)

// Response / unsolicited types (net.elf → client, via ProtoNetIPCResp).
const (
	NetMsgConnectResp        uint32 = 50
	NetMsgBindUDPResp        uint32 = 51
	NetMsgSendDgramResp      uint32 = 52
	NetMsgCloseResp          uint32 = 53
	NetMsgBindTCPResp        uint32 = 54
	NetMsgListenResp         uint32 = 55
	NetMsgAcceptResp         uint32 = 56
	NetMsgTCPConnectResp     uint32 = 57
	NetMsgStreamSendResp     uint32 = 58
	NetMsgStreamShutdownResp uint32 = 59
	NetMsgRecvDgram          uint32 = 60 // unsolicited (UDP RX delivery)
	NetMsgStreamRx           uint32 = 61 // unsolicited (TCP RX delivery)
)

// --- Encode helpers (request side) ---

func EncodeConnect(r *NetIPCConnectReq, senderSID int16) ipc.UringIPCMsg {
	var msg ipc.UringIPCMsg
	msg.Protocol = ipc.ProtoNetIPCReq
	msg.SenderSID = senderSID
	*(*uint32)(unsafe.Pointer(&msg.Payload[0])) = NetMsgConnect
	*(*NetIPCConnectReq)(unsafe.Pointer(&msg.Payload[4])) = *r
	return msg
}

func EncodeBindUDP(r *BindUDPReq, senderSID int16) ipc.UringIPCMsg {
	var msg ipc.UringIPCMsg
	msg.Protocol = ipc.ProtoNetIPCReq
	msg.SenderSID = senderSID
	*(*uint32)(unsafe.Pointer(&msg.Payload[0])) = NetMsgBindUDP
	*(*BindUDPReq)(unsafe.Pointer(&msg.Payload[4])) = *r
	return msg
}

func EncodeSendDgram(r *SendDgramReq, senderSID int16) ipc.UringIPCMsg {
	var msg ipc.UringIPCMsg
	msg.Protocol = ipc.ProtoNetIPCReq
	msg.SenderSID = senderSID
	*(*uint32)(unsafe.Pointer(&msg.Payload[0])) = NetMsgSendDgram
	*(*SendDgramReq)(unsafe.Pointer(&msg.Payload[4])) = *r
	return msg
}

func EncodeRelease(r *ReleaseReq, senderSID int16) ipc.UringIPCMsg {
	var msg ipc.UringIPCMsg
	msg.Protocol = ipc.ProtoNetIPCReq
	msg.SenderSID = senderSID
	*(*uint32)(unsafe.Pointer(&msg.Payload[0])) = NetMsgRelease
	*(*ReleaseReq)(unsafe.Pointer(&msg.Payload[4])) = *r
	return msg
}

func EncodeClose(r *CloseReq, senderSID int16) ipc.UringIPCMsg {
	var msg ipc.UringIPCMsg
	msg.Protocol = ipc.ProtoNetIPCReq
	msg.SenderSID = senderSID
	*(*uint32)(unsafe.Pointer(&msg.Payload[0])) = NetMsgClose
	*(*CloseReq)(unsafe.Pointer(&msg.Payload[4])) = *r
	return msg
}

func EncodeBindTCP(r *BindTCPReq, senderSID int16) ipc.UringIPCMsg {
	var msg ipc.UringIPCMsg
	msg.Protocol = ipc.ProtoNetIPCReq
	msg.SenderSID = senderSID
	*(*uint32)(unsafe.Pointer(&msg.Payload[0])) = NetMsgBindTCP
	*(*BindTCPReq)(unsafe.Pointer(&msg.Payload[4])) = *r
	return msg
}

func EncodeListen(r *ListenReq, senderSID int16) ipc.UringIPCMsg {
	var msg ipc.UringIPCMsg
	msg.Protocol = ipc.ProtoNetIPCReq
	msg.SenderSID = senderSID
	*(*uint32)(unsafe.Pointer(&msg.Payload[0])) = NetMsgListen
	*(*ListenReq)(unsafe.Pointer(&msg.Payload[4])) = *r
	return msg
}

func EncodeAccept(r *AcceptReq, senderSID int16) ipc.UringIPCMsg {
	var msg ipc.UringIPCMsg
	msg.Protocol = ipc.ProtoNetIPCReq
	msg.SenderSID = senderSID
	*(*uint32)(unsafe.Pointer(&msg.Payload[0])) = NetMsgAccept
	*(*AcceptReq)(unsafe.Pointer(&msg.Payload[4])) = *r
	return msg
}

func EncodeTCPConnect(r *TCPConnectReq, senderSID int16) ipc.UringIPCMsg {
	var msg ipc.UringIPCMsg
	msg.Protocol = ipc.ProtoNetIPCReq
	msg.SenderSID = senderSID
	*(*uint32)(unsafe.Pointer(&msg.Payload[0])) = NetMsgTCPConnect
	*(*TCPConnectReq)(unsafe.Pointer(&msg.Payload[4])) = *r
	return msg
}

func EncodeStreamSend(r *StreamSendReq, senderSID int16) ipc.UringIPCMsg {
	var msg ipc.UringIPCMsg
	msg.Protocol = ipc.ProtoNetIPCReq
	msg.SenderSID = senderSID
	*(*uint32)(unsafe.Pointer(&msg.Payload[0])) = NetMsgStreamSend
	*(*StreamSendReq)(unsafe.Pointer(&msg.Payload[4])) = *r
	return msg
}

func EncodeStreamShutdown(r *StreamShutdownReq, senderSID int16) ipc.UringIPCMsg {
	var msg ipc.UringIPCMsg
	msg.Protocol = ipc.ProtoNetIPCReq
	msg.SenderSID = senderSID
	*(*uint32)(unsafe.Pointer(&msg.Payload[0])) = NetMsgStreamShutdown
	*(*StreamShutdownReq)(unsafe.Pointer(&msg.Payload[4])) = *r
	return msg
}

// --- Encode helpers (response / notification side) ---

func EncodeConnectResp(r *NetIPCConnectResp) ipc.UringIPCMsg {
	var msg ipc.UringIPCMsg
	msg.Protocol = ipc.ProtoNetIPCResp
	*(*uint32)(unsafe.Pointer(&msg.Payload[0])) = NetMsgConnectResp
	*(*NetIPCConnectResp)(unsafe.Pointer(&msg.Payload[4])) = *r
	return msg
}

func EncodeBindUDPResp(r *BindUDPResp) ipc.UringIPCMsg {
	var msg ipc.UringIPCMsg
	msg.Protocol = ipc.ProtoNetIPCResp
	*(*uint32)(unsafe.Pointer(&msg.Payload[0])) = NetMsgBindUDPResp
	*(*BindUDPResp)(unsafe.Pointer(&msg.Payload[4])) = *r
	return msg
}

func EncodeSendDgramResp(r *SendDgramResp) ipc.UringIPCMsg {
	var msg ipc.UringIPCMsg
	msg.Protocol = ipc.ProtoNetIPCResp
	*(*uint32)(unsafe.Pointer(&msg.Payload[0])) = NetMsgSendDgramResp
	*(*SendDgramResp)(unsafe.Pointer(&msg.Payload[4])) = *r
	return msg
}

func EncodeCloseResp(r *CloseResp) ipc.UringIPCMsg {
	var msg ipc.UringIPCMsg
	msg.Protocol = ipc.ProtoNetIPCResp
	*(*uint32)(unsafe.Pointer(&msg.Payload[0])) = NetMsgCloseResp
	*(*CloseResp)(unsafe.Pointer(&msg.Payload[4])) = *r
	return msg
}

func EncodeRecvDgram(n *RecvDgram) ipc.UringIPCMsg {
	var msg ipc.UringIPCMsg
	msg.Protocol = ipc.ProtoNetIPCResp
	*(*uint32)(unsafe.Pointer(&msg.Payload[0])) = NetMsgRecvDgram
	*(*RecvDgram)(unsafe.Pointer(&msg.Payload[4])) = *n
	return msg
}

func EncodeBindTCPResp(r *BindTCPResp) ipc.UringIPCMsg {
	var msg ipc.UringIPCMsg
	msg.Protocol = ipc.ProtoNetIPCResp
	*(*uint32)(unsafe.Pointer(&msg.Payload[0])) = NetMsgBindTCPResp
	*(*BindTCPResp)(unsafe.Pointer(&msg.Payload[4])) = *r
	return msg
}

func EncodeListenResp(r *ListenResp) ipc.UringIPCMsg {
	var msg ipc.UringIPCMsg
	msg.Protocol = ipc.ProtoNetIPCResp
	*(*uint32)(unsafe.Pointer(&msg.Payload[0])) = NetMsgListenResp
	*(*ListenResp)(unsafe.Pointer(&msg.Payload[4])) = *r
	return msg
}

func EncodeAcceptResp(r *AcceptResp) ipc.UringIPCMsg {
	var msg ipc.UringIPCMsg
	msg.Protocol = ipc.ProtoNetIPCResp
	*(*uint32)(unsafe.Pointer(&msg.Payload[0])) = NetMsgAcceptResp
	*(*AcceptResp)(unsafe.Pointer(&msg.Payload[4])) = *r
	return msg
}

func EncodeTCPConnectResp(r *TCPConnectResp) ipc.UringIPCMsg {
	var msg ipc.UringIPCMsg
	msg.Protocol = ipc.ProtoNetIPCResp
	*(*uint32)(unsafe.Pointer(&msg.Payload[0])) = NetMsgTCPConnectResp
	*(*TCPConnectResp)(unsafe.Pointer(&msg.Payload[4])) = *r
	return msg
}

func EncodeStreamSendResp(r *StreamSendResp) ipc.UringIPCMsg {
	var msg ipc.UringIPCMsg
	msg.Protocol = ipc.ProtoNetIPCResp
	*(*uint32)(unsafe.Pointer(&msg.Payload[0])) = NetMsgStreamSendResp
	*(*StreamSendResp)(unsafe.Pointer(&msg.Payload[4])) = *r
	return msg
}

func EncodeStreamShutdownResp(r *StreamShutdownResp) ipc.UringIPCMsg {
	var msg ipc.UringIPCMsg
	msg.Protocol = ipc.ProtoNetIPCResp
	*(*uint32)(unsafe.Pointer(&msg.Payload[0])) = NetMsgStreamShutdownResp
	*(*StreamShutdownResp)(unsafe.Pointer(&msg.Payload[4])) = *r
	return msg
}

func EncodeStreamRx(n *StreamRx) ipc.UringIPCMsg {
	var msg ipc.UringIPCMsg
	msg.Protocol = ipc.ProtoNetIPCResp
	*(*uint32)(unsafe.Pointer(&msg.Payload[0])) = NetMsgStreamRx
	*(*StreamRx)(unsafe.Pointer(&msg.Payload[4])) = *n
	return msg
}

// --- Decode helpers ---
//
// Each decoder returns a pointer into the message payload so callers can
// read fields without copying. Dispatchers first read MsgTypeOf to pick
// which decoder to call — no interface boxing on the hot path. The
// returned pointer is valid for the lifetime of the message slot.

// MsgTypeOf returns the MsgType discriminator from a NetIPC message payload.
// Dispatchers peek this to pick the right typed decoder.
func MsgTypeOf(msg *ipc.UringIPCMsg) uint32 {
	return *(*uint32)(unsafe.Pointer(&msg.Payload[0]))
}

func DecodeConnectReq(msg *ipc.UringIPCMsg) *NetIPCConnectReq {
	return (*NetIPCConnectReq)(unsafe.Pointer(&msg.Payload[4]))
}

func DecodeBindUDPReq(msg *ipc.UringIPCMsg) *BindUDPReq {
	return (*BindUDPReq)(unsafe.Pointer(&msg.Payload[4]))
}

func DecodeSendDgramReq(msg *ipc.UringIPCMsg) *SendDgramReq {
	return (*SendDgramReq)(unsafe.Pointer(&msg.Payload[4]))
}

func DecodeReleaseReq(msg *ipc.UringIPCMsg) *ReleaseReq {
	return (*ReleaseReq)(unsafe.Pointer(&msg.Payload[4]))
}

func DecodeCloseReq(msg *ipc.UringIPCMsg) *CloseReq {
	return (*CloseReq)(unsafe.Pointer(&msg.Payload[4]))
}

func DecodeConnectResp(msg *ipc.UringIPCMsg) *NetIPCConnectResp {
	return (*NetIPCConnectResp)(unsafe.Pointer(&msg.Payload[4]))
}

func DecodeBindUDPResp(msg *ipc.UringIPCMsg) *BindUDPResp {
	return (*BindUDPResp)(unsafe.Pointer(&msg.Payload[4]))
}

func DecodeSendDgramResp(msg *ipc.UringIPCMsg) *SendDgramResp {
	return (*SendDgramResp)(unsafe.Pointer(&msg.Payload[4]))
}

func DecodeCloseResp(msg *ipc.UringIPCMsg) *CloseResp {
	return (*CloseResp)(unsafe.Pointer(&msg.Payload[4]))
}

func DecodeRecvDgram(msg *ipc.UringIPCMsg) *RecvDgram {
	return (*RecvDgram)(unsafe.Pointer(&msg.Payload[4]))
}

func DecodeBindTCPReq(msg *ipc.UringIPCMsg) *BindTCPReq {
	return (*BindTCPReq)(unsafe.Pointer(&msg.Payload[4]))
}

func DecodeListenReq(msg *ipc.UringIPCMsg) *ListenReq {
	return (*ListenReq)(unsafe.Pointer(&msg.Payload[4]))
}

func DecodeAcceptReq(msg *ipc.UringIPCMsg) *AcceptReq {
	return (*AcceptReq)(unsafe.Pointer(&msg.Payload[4]))
}

func DecodeTCPConnectReq(msg *ipc.UringIPCMsg) *TCPConnectReq {
	return (*TCPConnectReq)(unsafe.Pointer(&msg.Payload[4]))
}

func DecodeStreamSendReq(msg *ipc.UringIPCMsg) *StreamSendReq {
	return (*StreamSendReq)(unsafe.Pointer(&msg.Payload[4]))
}

func DecodeStreamShutdownReq(msg *ipc.UringIPCMsg) *StreamShutdownReq {
	return (*StreamShutdownReq)(unsafe.Pointer(&msg.Payload[4]))
}

func DecodeBindTCPResp(msg *ipc.UringIPCMsg) *BindTCPResp {
	return (*BindTCPResp)(unsafe.Pointer(&msg.Payload[4]))
}

func DecodeListenResp(msg *ipc.UringIPCMsg) *ListenResp {
	return (*ListenResp)(unsafe.Pointer(&msg.Payload[4]))
}

func DecodeAcceptResp(msg *ipc.UringIPCMsg) *AcceptResp {
	return (*AcceptResp)(unsafe.Pointer(&msg.Payload[4]))
}

func DecodeTCPConnectResp(msg *ipc.UringIPCMsg) *TCPConnectResp {
	return (*TCPConnectResp)(unsafe.Pointer(&msg.Payload[4]))
}

func DecodeStreamSendResp(msg *ipc.UringIPCMsg) *StreamSendResp {
	return (*StreamSendResp)(unsafe.Pointer(&msg.Payload[4]))
}

func DecodeStreamShutdownResp(msg *ipc.UringIPCMsg) *StreamShutdownResp {
	return (*StreamShutdownResp)(unsafe.Pointer(&msg.Payload[4]))
}

func DecodeStreamRx(msg *ipc.UringIPCMsg) *StreamRx {
	return (*StreamRx)(unsafe.Pointer(&msg.Payload[4]))
}
