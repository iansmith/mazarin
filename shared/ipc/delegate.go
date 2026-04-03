// delegate.go — Payload structs for uring-based FS syscall delegation.
//
// ProtoFSDelegateReq messages carry a delegated syscall from the kernel to
// the handler shepherd. ProtoFSDelegateResp messages carry the reply back
// (handler → kernel → caller).
//
// Both payloads fit within the 112-byte UringIPCMsg.Payload field.
package ipc

import "unsafe"

// FSDelegateReqPayload is the payload for ProtoFSDelegateReq messages.
// Sent by the kernel when forwarding a delegated syscall to the handler.
//
// Layout (72 bytes):
//
//	[0:2]   SysID     — platform-independent syscall identifier
//	[2:4]   CallerSID — shepherd that made the syscall
//	[4:6]   CallerTID — thread that made the syscall (for reply routing)
//	[6:8]   _pad
//	[8:56]  Args      — 6 uint64 syscall arguments
//	[56:64] DataVA    — VA of shared data page in handler's address space (0 = none)
//	[64:68] DataLen   — bytes of valid data in the page
//	[68:72] _pad2
type FSDelegateReqPayload struct {
	SysID     uint16
	CallerSID int16
	CallerTID int16
	_pad      uint16
	Args      [6]uint64
	DataVA    uint64
	DataLen   uint32
	_pad2     uint32
}

// FSDelegateRespPayload is the payload for ProtoFSDelegateResp messages.
// Sent by the handler shepherd to reply to a delegated syscall. The kernel
// intercepts the reply to do copy-back (Read) and page reclamation before
// waking the caller.
//
// Layout (48 bytes):
//
//	[0:2]   CallerSID  — original caller (for reply routing)
//	[2:4]   CallerTID  — original caller thread
//	[4:8]   _pad
//	[8:16]  ReturnVal  — syscall return value (bytes read/written, fd, errno)
//	[16:24] Extra0     — LoadFile: targetVA
//	[24:32] Extra1     — LoadFile: numPages
//	[32:40] Extra2     — LoadFile: bytesRead
//	[40:48] _pad2
type FSDelegateRespPayload struct {
	CallerSID int16
	CallerTID int16
	_pad      uint32
	ReturnVal int64
	Extra0    uint64 // LoadFile: targetVA
	Extra1    uint64 // LoadFile: numPages
	Extra2    uint64 // LoadFile: bytesRead
	_pad2     uint64
}

// Compile-time size assertions — payloads must fit in 112-byte Payload field.
var _ [112]byte = [unsafe.Sizeof(FSDelegateReqPayload{}) + 40]byte{} // 72 + 40 = 112
var _ [112]byte = [unsafe.Sizeof(FSDelegateRespPayload{}) + 64]byte{} // 48 + 64 = 112

// EncodeFSDelegateReq packs a request payload into a UringIPCMsg.
func EncodeFSDelegateReq(p *FSDelegateReqPayload) UringIPCMsg {
	var msg UringIPCMsg
	msg.Protocol = ProtoFSDelegateReq
	*(*FSDelegateReqPayload)(unsafe.Pointer(&msg.Payload[0])) = *p
	return msg
}

// DecodeFSDelegateReq extracts the request payload from a UringIPCMsg.
func DecodeFSDelegateReq(msg *UringIPCMsg) *FSDelegateReqPayload {
	return (*FSDelegateReqPayload)(unsafe.Pointer(&msg.Payload[0]))
}

// EncodeFSDelegateResp packs a response payload into a UringIPCMsg.
func EncodeFSDelegateResp(p *FSDelegateRespPayload) UringIPCMsg {
	var msg UringIPCMsg
	msg.Protocol = ProtoFSDelegateResp
	*(*FSDelegateRespPayload)(unsafe.Pointer(&msg.Payload[0])) = *p
	return msg
}

// DecodeFSDelegateResp extracts the response payload from a UringIPCMsg.
func DecodeFSDelegateResp(msg *UringIPCMsg) *FSDelegateRespPayload {
	return (*FSDelegateRespPayload)(unsafe.Pointer(&msg.Payload[0]))
}
