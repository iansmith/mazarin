package sys

// delegate.go — Userspace API for syscall delegation.
//
// A priest registers to handle one or more syscalls (by sysid.ID).
// The kernel forwards matching syscalls from other priests to the handler.
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
	"mazzy/shared/sysid"
	"runtime"
	"unsafe"
)

// SyscallRequest represents a delegated syscall delivered to a handler priest.
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
	return unsafe.Slice((*byte)(unsafe.Pointer(r.dataVA)), r.dataLen)
}

// DataBuf returns the data buffer as a writable slice.
// For Read: the handler fills this buffer with the read result.
// The kernel will copy it back to the caller's buffer on Reply.
func (r *SyscallRequest) DataBuf() []byte {
	if r.dataVA == 0 || r.dataLen == 0 {
		return nil
	}
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
	RawSyscall(sysDelegatedReply,
		uintptr(r.CallerPID),
		uintptr(r.CallerTID),
		uintptr(uint64(returnVal)),
		0, 0, 0)
}

// LoadFileReply sends the return value and file result back to the blocked caller.
// For LoadFile: the kernel writes (targetVA, numPages, bytesRead) to the caller's
// LoadFileResult struct before waking the caller.
func (r *SyscallRequest) LoadFileReply(returnVal int64, targetVA, numPages, bytesRead uint64) {
	RawSyscall(sysDelegatedReply,
		uintptr(r.CallerPID),
		uintptr(r.CallerTID),
		uintptr(uint64(returnVal)),
		uintptr(targetVA),
		uintptr(numPages),
		uintptr(bytesRead))
}

// delegateRecvResult matches the kernel-side layout written by writeDelegateRecvResult.
type delegateRecvResult struct {
	SysID     uint16
	CallerPID int16
	CallerTID int16
	_pad      uint16
	Args      [6]uint64
	DataVA    uint64
	DataLen   uint64
}

// HandleSyscalls registers the calling priest as the handler for the given
// syscalls and returns a single channel that delivers all incoming requests.
//
// The caller should process each request and call req.Reply() to unblock the
// original caller. Failing to reply will leave the caller permanently blocked.
func HandleSyscalls(ids ...sysid.ID) (<-chan SyscallRequest, error) {
	for _, id := range ids {
		r1, _, errno := RawSyscall(sysRegisterSyscallHandler,
			uintptr(id), 0, 0, 0, 0, 0)
		if errno != 0 {
			return nil, errno
		}
		if int64(r1) < 0 {
			return nil, errFromR1(r1)
		}
	}

	ch := make(chan SyscallRequest, 4)
	go delegateRecvLoop(ch)
	return ch, nil
}

// HandleSyscall is a convenience wrapper for handling a single syscall.
func HandleSyscall(id sysid.ID) (<-chan SyscallRequest, error) {
	return HandleSyscalls(id)
}

// delegateRecvLoop runs in a dedicated goroutine, calling SysDelegatedRecv
// in a loop. The kernel returns whichever SysID has a pending request first.
func delegateRecvLoop(ch chan<- SyscallRequest) {
	runtime.LockOSThread()
	var result delegateRecvResult
	for {
		runtime_entersyscall()
		r1, _, errno := RawSyscall(sysDelegatedRecv,
			uintptr(unsafe.Pointer(&result)),
			0, 0, 0, 0, 0)
		runtime_exitsyscall()

		if errno != 0 || int64(r1) < 0 {
			continue
		}

		req := SyscallRequest{
			SysID:     sysid.ID(result.SysID),
			CallerPID: result.CallerPID,
			CallerTID: result.CallerTID,
			dataVA:    uintptr(result.DataVA),
			dataLen:   uint32(result.DataLen),
		}
		req.Args = result.Args

		ch <- req
	}
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
	r1, _, _ := RawSyscall(sysUartWrite,
		uintptr(unsafe.Pointer(&data[0])),
		uintptr(len(data)),
		0, 0, 0, 0)
	return int(r1)
}

// UartWriteBlocking writes all bytes to the UART, blocking until complete.
func UartWriteBlocking(data []byte) int {
	if len(data) == 0 {
		return 0
	}
	r1, _, _ := RawSyscall(sysUartWriteBlocking,
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
