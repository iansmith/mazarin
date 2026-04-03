// Package uring provides userspace access to the kernel's IPC uring ring system.
//
// Each shepherd gets a kernel-allocated message ring at launch. Shepherds
// connect to each other's rings via UringID (discoverable through ShepherdInfo),
// then send 128-byte messages. A dedicated reader goroutine receives messages
// and dispatches them to typed Go channels by protocol.
package uring

import (
	"mazzy/shared/ipc"
	"mazzy/shared/mazzy"
	"syscall"
	"unsafe"
)

// entersyscallblock tells the Go runtime "I know I'm going to block."
// Unlike entersyscall (used by Syscall6), this immediately calls handoffp()
// which hands the P to another M via startm(). This ensures goroutines
// parked on channels (e.g. wmEventLoop) can run while we're blocked in
// the kernel waiting for a uring message.
//
//go:linkname runtime_entersyscallblock runtime.entersyscallblock
func runtime_entersyscallblock()

//go:linkname runtime_exitsyscall runtime.exitsyscall
func runtime_exitsyscall()

// Connect establishes a connection to a target shepherd's uring ring by uring ID.
// Returns a handle (small integer) for use with Release.
//
// Uses Syscall (P released) because this routes through KernelSVCWorker.
func Connect(targetUringID uint64) (handle int, err error) {
	r1, _, errno := syscall.Syscall6(mazzy.SysUringConnect,
		uintptr(targetUringID),
		0, 0, 0, 0, 0)
	if errno != 0 {
		return -1, errno
	}
	if int64(r1) < 0 {
		return -1, syscall.Errno(-int64(r1))
	}
	return int(r1), nil
}

// Send sends a 128-byte message to a target shepherd's uring ring.
// The msg must be exactly 128 bytes (ipc.UringIPCMsg). The kernel stamps
// the SenderSID and SenderID fields automatically.
//
// Uses RawSyscall (P held) because this is a fast non-blocking operation.
func Send(targetSID int, msg *ipc.UringIPCMsg) error {
	r1, _, errno := syscall.RawSyscall6(mazzy.SysUringSend,
		uintptr(targetSID),
		uintptr(unsafe.Pointer(msg)),
		0, 0, 0, 0)
	if errno != 0 {
		return errno
	}
	if int64(r1) < 0 {
		return syscall.Errno(-int64(r1))
	}
	return nil
}

// Recv blocks until a message arrives on the caller's own uring ring.
// The received message is written into msg.
//
// Uses entersyscallblock (not entersyscall) because this is a known-blocking
// operation. entersyscallblock immediately hands off the P via handoffp() →
// startm(), so other goroutines (e.g. event loops parked on channels) can
// run while this M is blocked in the kernel. When the kernel wakes us
// (priority queue, SVC rewind), exitsyscall reacquires the P.
func Recv(msg *ipc.UringIPCMsg) error {
	// Touch the buffer to ensure demand-fault before kernel writes.
	*(*byte)(unsafe.Pointer(msg)) = 0
	runtime_entersyscallblock()
	r1, _, errno := syscall.RawSyscall6(mazzy.SysUringRecv,
		uintptr(unsafe.Pointer(msg)),
		0, 0, 0, 0, 0)
	runtime_exitsyscall()
	if errno != 0 {
		return errno
	}
	if int64(r1) < 0 {
		return syscall.Errno(-int64(r1))
	}
	return nil
}

// Release releases a connection handle obtained from Connect.
// Decrements the refcount; if it reaches 0, the connection is freed.
//
// Uses RawSyscall (P held) — this is a fast non-blocking operation.
func Release(handle int) error {
	r1, _, errno := syscall.RawSyscall6(mazzy.SysUringRelease,
		uintptr(handle),
		0, 0, 0, 0, 0)
	if errno != 0 {
		return errno
	}
	if int64(r1) < 0 {
		return syscall.Errno(-int64(r1))
	}
	return nil
}
