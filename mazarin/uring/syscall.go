// Package uring provides userspace access to the kernel's IPC uring ring system.
//
// Each shepherd gets a kernel-allocated message ring at launch. Shepherds
// connect to each other's rings via UringID (discoverable through ShepherdInfo),
// then send 128-byte messages. A dedicated reader goroutine receives messages
// and dispatches them to typed Go channels by protocol.
//
// Ring 0 is created automatically at shepherd startup. Shepherds that need
// multiple independent readers (e.g. the linux shepherd) can create additional
// rings (1, 2) via Setup and use the WithRing variants.
package uring

import (
	"mazzy/shared/ipc"
	"mazzy/shared/mazzy"
	"runtime"
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

// Setup creates an additional uring ring for the calling shepherd.
// ringIdx must be 1 or 2 (ring 0 is created automatically at startup).
// Returns nil on success, or an error.
func Setup(ringIdx int) error {
	r1, _, errno := syscall.RawSyscall6(mazzy.SysUringSetup,
		uintptr(ringIdx),
		0, 0, 0, 0, 0)
	if errno != 0 {
		return errno
	}
	if int64(r1) < 0 {
		return syscall.Errno(-int64(r1))
	}
	return nil
}

// Connect establishes a connection to a target shepherd's uring ring 0.
func Connect(targetUringID uint64) (handle int, err error) {
	return ConnectWithRing(targetUringID, 0)
}

// ConnectWithRing establishes a connection to a specific ring on the target shepherd.
// ringIdx selects which ring on the target (0 = default, 1-2 = additional).
//
// Uses Syscall (P released) because this routes through KernelSVCWorker.
func ConnectWithRing(targetUringID uint64, ringIdx int) (handle int, err error) {
	r1, _, errno := syscall.Syscall6(mazzy.SysUringConnect,
		uintptr(targetUringID),
		uintptr(ringIdx),
		0, 0, 0, 0)
	if errno != 0 {
		return -1, errno
	}
	if int64(r1) < 0 {
		return -1, syscall.Errno(-int64(r1))
	}
	return int(r1), nil
}

// Send sends a 128-byte message to a target shepherd's uring ring 0.
//
// Retries on EAGAIN (target ring full) up to sendRetryLimit times, yielding
// the goroutine between attempts so the receiver has a chance to drain.
// On Linux, write to a full pipe blocks; the closest equivalent here is a
// bounded userspace retry. After the retry budget is exhausted the EAGAIN
// is surfaced to the caller — at which point fontsvc / similar reply paths
// have a real problem (receiver is wedged, not just slow) and dropping the
// message preserves the prior behavior.
func Send(targetSID int, msg *ipc.UringIPCMsg) error {
	return SendWithRing(targetSID, msg, 0)
}

// sendRetryLimit caps how long we'll spin on EAGAIN. At the budget below
// (~256 yields), even a heavily-loaded receiver gets ample opportunity to
// drain a single 128-byte slot before we give up.
const sendRetryLimit = 256

// SendWithRing sends a 128-byte message to a specific ring on the target shepherd.
// ringIdx selects which ring on the target (0 = default, 1-2 = additional).
//
// Uses RawSyscall (P held) because this is a fast non-blocking operation.
// Retries on EAGAIN (target ring full) up to sendRetryLimit times with a
// runtime.Gosched between attempts. Without this retry, transient bursts
// (e.g. linux-ui asking fontsvc for several fonts during boot) fail
// silently when the target's reader hasn't yet drained the prior message.
func SendWithRing(targetSID int, msg *ipc.UringIPCMsg, ringIdx int) error {
	_, err := SendWithStats(targetSID, msg, ringIdx)
	return err
}

// SendWithStats is identical to SendWithRing but also returns the number of
// EAGAIN-driven retry attempts that elapsed before the call returned.
// attempts==0 means the very first SysUringSend syscall succeeded.
// Used by call sites that want to surface ring-full pressure in their logs.
func SendWithStats(targetSID int, msg *ipc.UringIPCMsg, ringIdx int) (attempts int, err error) {
	for attempt := 0; ; attempt++ {
		r1, _, errno := syscall.RawSyscall6(mazzy.SysUringSend,
			uintptr(targetSID),
			uintptr(unsafe.Pointer(msg)),
			uintptr(ringIdx),
			0, 0, 0)
		if errno != 0 {
			if errno == syscall.EAGAIN && attempt < sendRetryLimit {
				runtime.Gosched()
				continue
			}
			return attempt, errno
		}
		if int64(r1) < 0 {
			e := syscall.Errno(-int64(r1))
			if e == syscall.EAGAIN && attempt < sendRetryLimit {
				runtime.Gosched()
				continue
			}
			return attempt, e
		}
		return attempt, nil
	}
}

// Recv blocks until a message arrives on the caller's uring ring 0.
func Recv(msg *ipc.UringIPCMsg) error {
	return RecvWithRing(msg, 0)
}

// RecvWithRing blocks until a message arrives on the specified ring.
// ringIdx selects which ring to receive from (0 = default, 1-2 = additional).
//
// Uses entersyscallblock (not entersyscall) because this is a known-blocking
// operation. entersyscallblock immediately hands off the P via handoffp() →
// startm(), so other goroutines (e.g. event loops parked on channels) can
// run while this M is blocked in the kernel. When the kernel wakes us
// (priority queue, SVC rewind), exitsyscall reacquires the P.
func RecvWithRing(msg *ipc.UringIPCMsg, ringIdx int) error {
	// Touch the buffer to ensure demand-fault before kernel writes.
	*(*byte)(unsafe.Pointer(msg)) = 0
	runtime_entersyscallblock()
	r1, _, errno := syscall.RawSyscall6(mazzy.SysUringRecv,
		uintptr(unsafe.Pointer(msg)),
		uintptr(ringIdx),
		0, 0, 0, 0)
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
