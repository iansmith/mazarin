// delegate.go — Payload structs for uring-based syscall delegation.
//
// ProtoFSDelegateReq messages carry a delegated syscall from the kernel to
// the handler shepherd. Replies go directly through the kernel's SyscallReply
// path (not through a uring response message).
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

// Compile-time size assertion — payload must fit in 112-byte Payload field.
var _ [112]byte = [unsafe.Sizeof(FSDelegateReqPayload{}) + 40]byte{} // 72 + 40 = 112

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


