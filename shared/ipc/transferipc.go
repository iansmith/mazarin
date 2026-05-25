// transferipc.go — Payload structs for mazarin/transfer Mode 1 IPC (MAZ-50).
//
// Mode 1 is a two-phase client → server transfer:
//
//  1. Client sends ProtoTransferReq{Op: OpReserve, Kind, Size} — asks the
//     server to allocate `Size` bytes of pages and map them into the client.
//     Server responds with ProtoTransferResp{VA, Pages} carrying the
//     client-side VA.
//
//  2. Client fills the pages via a Writer over [VA, VA+Pages*PageSize),
//     then sends ProtoTransferReq{Op: OpCommit, VA, Pages} after
//     sys.TransferAndUnmap has moved the pages back to the server's space.
//     The Commit message is the "wake up your Wait()" signal; the page
//     ownership transfer is already done by the time the message arrives
//     (release-first ordering, per MAZ-50's pinned resolution #4).
//
// See design/mazarin-transfer-state-machine.md for the full state machine.
package ipc

import "unsafe"

// TransferOp distinguishes the two phases of a Mode 1 transfer.
type TransferOp uint16

const (
	TransferOpReserve TransferOp = 1
	TransferOpCommit  TransferOp = 2
)

// TransferReqPayload is the payload for ProtoTransferReq messages.
//
// Layout (40 bytes):
//
//	[0:2]   Op       — TransferOpReserve | TransferOpCommit
//	[2:4]   _pad0
//	[4:8]   Kind     — caller-defined tag (opaque to the transport)
//	[8:16]  Size     — bytes (Reserve); 0 on Commit
//	[16:24] VA       — 0 (Reserve); handle VA in server's space (Commit)
//	[24:28] Pages    — 0 (Reserve); handle page count (Commit)
//	[28:32] ReqID    — for response correlation
//	[32:33] RespRing — ring index for the server to send the Resp on
//	[33:40] _pad1
type TransferReqPayload struct {
	Op       TransferOp
	_pad0    uint16
	Kind     uint32
	Size     uint64
	VA       uint64
	Pages    uint32
	ReqID    uint32
	RespRing uint8
	_pad1    [7]byte
}

// TransferRespPayload is the payload for ProtoTransferResp messages.
//
// Layout (24 bytes):
//
//	[0:4]   ReqID — matching request ID
//	[4:8]   Err   — 0 on success, negative errno on failure
//	[8:16]  VA    — client-side VA of the mapped pages (Reserve); 0 (Commit)
//	[16:20] Pages — page count visible to the client (Reserve); 0 (Commit)
//	[20:24] _pad
type TransferRespPayload struct {
	ReqID uint32
	Err   int32
	VA    uint64
	Pages uint32
	_pad  [4]byte
}

// Compile-time guarantee that both payloads fit in UringIPCMsg.Payload.
// If a future field addition overflows, this fails to compile rather than
// corrupting adjacent ring data at runtime. Self-updating: derived from the
// container field's actual size, so a UringIPCMsg.Payload resize is also
// re-checked against these payloads automatically.
var _ [unsafe.Sizeof(UringIPCMsg{}.Payload) - unsafe.Sizeof(TransferReqPayload{})]byte
var _ [unsafe.Sizeof(UringIPCMsg{}.Payload) - unsafe.Sizeof(TransferRespPayload{})]byte

// EncodeTransferReq packs a request payload into a UringIPCMsg.
func EncodeTransferReq(p *TransferReqPayload, senderSID int16) UringIPCMsg {
	var msg UringIPCMsg
	msg.Protocol = ProtoTransferReq
	msg.SenderSID = senderSID
	*(*TransferReqPayload)(unsafe.Pointer(&msg.Payload[0])) = *p
	return msg
}

// DecodeTransferReq extracts the request payload from a UringIPCMsg.
func DecodeTransferReq(msg *UringIPCMsg) *TransferReqPayload {
	return (*TransferReqPayload)(unsafe.Pointer(&msg.Payload[0]))
}

// EncodeTransferResp packs a response payload into a UringIPCMsg.
func EncodeTransferResp(p *TransferRespPayload, senderSID int16) UringIPCMsg {
	var msg UringIPCMsg
	msg.Protocol = ProtoTransferResp
	msg.SenderSID = senderSID
	*(*TransferRespPayload)(unsafe.Pointer(&msg.Payload[0])) = *p
	return msg
}

// DecodeTransferResp extracts the response payload from a UringIPCMsg.
func DecodeTransferResp(msg *UringIPCMsg) *TransferRespPayload {
	return (*TransferRespPayload)(unsafe.Pointer(&msg.Payload[0]))
}
