package sys

// delegate.go — Userspace API for syscall delegation.
//
// A shepherd registers to handle one or more syscalls (by sysid.ID).
// The kernel forwards matching syscalls from other shepherds to the handler.
// The handler receives all requests on a single channel and replies via Reply().
//
// Usage:
//
//	ch, err := sys.HandleSyscalls(sysid.Write, sysid.Read, sysid.Openat, sysid.Close)
//	for req := range ch {
//	    switch req.SysID {
//	    case sysid.Write:
//	        data := req.Data()
//	        n := doWrite(req.Arg0(), data)
//	        req.Reply(int64(n))
//	    case sysid.Read:
//	        buf := req.DataBuf()    // empty page to fill
//	        n := doRead(req.Arg0(), buf)
//	        req.Reply(int64(n))
//	    case sysid.Close:
//	        err := doClose(req.Arg0())
//	        req.Reply(errToInt64(err))
//	    }
//	}

import (
	"mazzy/shared/ipc"
	"mazzy/shared/mazzy"
	"mazzy/shared/sysid"
	"unsafe"
)

// SyscallRequest represents a delegated syscall delivered to a handler shepherd.
type SyscallRequest struct {
	SysID     sysid.ID // Which syscall
	CallerPID int16    // Who made the call
	CallerTID int16    // Caller's thread ID
	Args      [6]uint64
	dataVA    uintptr // VA of data page in our address space
	dataLen   uint32
}

// Arg0 returns the first syscall argument (e.g. fd for write/read/close).
func (r *SyscallRequest) Arg0() uint64 { return r.Args[0] }

// Data returns the data buffer that was copied from the caller's address space.
// For Write: contains the bytes the caller wants written.
// For Read: returns nil (use DataBuf instead to get the writable buffer).
func (r *SyscallRequest) Data() []byte {
	if r.dataVA == 0 || r.dataLen == 0 {
		return nil
	}
	// nolint: gosec — dataVA is a kernel-provided page VA mapped into our address
	// space by the delegation infrastructure; converting to pointer is required.
	return unsafe.Slice((*byte)(unsafe.Pointer(r.dataVA)), r.dataLen)
}

// DataBuf returns the data buffer as a writable slice.
// For Read: the handler fills this buffer with the read result.
// The kernel will copy it back to the caller's buffer on Reply.
func (r *SyscallRequest) DataBuf() []byte {
	if r.dataVA == 0 || r.dataLen == 0 {
		return nil
	}
	// nolint: gosec — same as Data(); kernel-provided VA, bounds-checked by dataLen.
	return unsafe.Slice((*byte)(unsafe.Pointer(r.dataVA)), r.dataLen)
}

// PathString returns the data buffer as a null-terminated string.
// For Openat: contains the pathname from the caller.
func (r *SyscallRequest) PathString() string {
	d := r.Data()
	if d == nil {
		return ""
	}
	for i, b := range d {
		if b == 0 {
			return string(d[:i])
		}
	}
	return string(d)
}

// Reply sends the return value back to the blocked caller.
// For Write: returnVal = number of bytes written (or negative errno).
// For Read: returnVal = number of bytes read (kernel copies that many from DataBuf).
// For Close: returnVal = 0 on success, negative errno on error.
func (r *SyscallRequest) Reply(returnVal int64) {
	RawSyscall(mazzy.SysSyscallReply,
		uintptr(r.CallerPID),
		uintptr(r.CallerTID),
		uintptr(uint64(returnVal)),
		0, 0, 0)
}

// LoadFileReply sends the return value and file result back to the blocked caller.
// For LoadFile: the kernel writes (targetVA, numPages, bytesRead) to the caller's
// LoadFileResult struct before waking the caller.
func (r *SyscallRequest) LoadFileReply(returnVal int64, targetVA, numPages, bytesRead uint64) {
	RawSyscall(mazzy.SysSyscallReply,
		uintptr(r.CallerPID),
		uintptr(r.CallerTID),
		uintptr(uint64(returnVal)),
		uintptr(targetVA),
		uintptr(numPages),
		uintptr(bytesRead))
}

// DecodeFSDelegateReq is a uring Dispatcher decoder for ProtoFSDelegateReq messages.
// Returns a SyscallRequest that the handler can process identically to the old
// delegate recv loop.
func DecodeFSDelegateReq(msg *ipc.UringIPCMsg) any {
	p := ipc.DecodeFSDelegateReq(msg)
	return SyscallRequest{
		SysID:     sysid.ID(p.SysID),
		CallerPID: p.CallerSID,
		CallerTID: p.CallerTID,
		Args:      p.Args,
		dataVA:    uintptr(p.DataVA),
		dataLen:   p.DataLen,
	}
}

// NewSyscallRequest creates a SyscallRequest from uring-delivered payload fields.
// Used by the handler's uring Dispatcher to convert ProtoFSDelegateReq messages
// into the SyscallRequest type consumed by the delegate handler goroutine.
func NewSyscallRequest(sysID sysid.ID, callerSID, callerTID int16, args [6]uint64, dataVA uintptr, dataLen uint32) SyscallRequest {
	return SyscallRequest{
		SysID:     sysID,
		CallerPID: callerSID,
		CallerTID: callerTID,
		Args:      args,
		dataVA:    dataVA,
		dataLen:   dataLen,
	}
}

// RegisterSyscallHandlers registers the calling shepherd as the handler for
// the given syscalls without starting a recv goroutine. The handler receives
// delegated requests via its uring Dispatcher (ProtoFSDelegateReq).
func RegisterSyscallHandlers(ids ...sysid.ID) error {
	for _, id := range ids {
		r1, _, errno := RawSyscall(mazzy.SysRegisterSyscallHandler,
			uintptr(id), 0, 0, 0, 0, 0)
		if errno != 0 {
			return errno
		}
		if int64(r1) < 0 {
			return errFromR1(r1)
		}
	}
	return nil
}

// errNo implements error for negative syscall return values.
type errNo uintptr

func (e errNo) Error() string { return "syscall error" }

func errFromR1(r1 uintptr) error {
	return errNo(-int64(r1))
}

// UartWrite writes bytes to the UART (non-blocking). Drops bytes that don't fit.
// Returns the number of bytes actually written.
func UartWrite(data []byte) int {
	if len(data) == 0 {
		return 0
	}
	r1, _, _ := RawSyscall(mazzy.SysUartWrite,
		uintptr(unsafe.Pointer(&data[0])),
		uintptr(len(data)),
		0, 0, 0, 0)
	return int(r1)
}

// UartWriteString is a convenience wrapper for writing a string to the UART.
func UartWriteString(s string) int {
	if len(s) == 0 {
		return 0
	}
	return UartWrite([]byte(s))
}
