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
	"fmt"
	"mazzy/shared/ipc"
	"mazzy/shared/mazzy"
	"syscall"
	"time"
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
func Send(targetSID int, msg *ipc.UringIPCMsg) error {
	return SendWithRing(targetSID, msg, 0)
}

// sendMaxAttempts caps wall-time budget at sendMaxAttempts * sendPerAttemptMs
// (~30 ms). The kernel parks senders on the static deadline queue for
// sendPerAttemptMs each time the target ring is full and we're the first
// blocker; subsequent senders fast-bounce with EAGAIN. Userspace pacing
// converts those fast bounces into a real off-CPU sleep so we don't spin.
const (
	sendMaxAttempts  = 3
	sendPerAttemptMs = 10
	// sendFastBounceMs is the threshold below which we treat the EAGAIN as
	// a fast bounce (kernel didn't park us for the full deadline) and pace.
	// At/above this we assume the kernel already waited the deadline and
	// retry immediately.
	sendFastBounceMs = 8
)

// SendWithRing sends a 128-byte message to a specific ring on the target shepherd.
// ringIdx selects which ring on the target (0 = default, 1-2 = additional).
//
// On EAGAIN the kernel either parked us for ~10 ms on the target slot's
// blocker queue (slow path) or returned immediately because another sender
// was already parked / no thread was available (fast bounce). We retry up
// to sendMaxAttempts times, sleeping ~sendPerAttemptMs between attempts
// only if the previous attempt was a fast bounce — total wall-time budget
// ~30 ms. Logs only on retry-success or final exhaustion.
func SendWithRing(targetSID int, msg *ipc.UringIPCMsg, ringIdx int) error {
	var lastErr error
	for attempt := 0; attempt < sendMaxAttempts; attempt++ {
		start := time.Now()
		r1, _, errno := syscall.RawSyscall6(mazzy.SysUringSend,
			uintptr(targetSID),
			uintptr(unsafe.Pointer(msg)),
			uintptr(ringIdx),
			0, 0, 0)
		var err error
		if errno != 0 {
			err = errno
		} else if int64(r1) < 0 {
			err = syscall.Errno(-int64(r1))
		}
		if err == nil {
			if attempt > 0 {
				fmt.Printf("[uring.Send] target=%d ring=%d retried, attempts=%d\n",
					targetSID, ringIdx, attempt+1)
			}
			return nil
		}
		if err != syscall.EAGAIN {
			return err
		}
		lastErr = err
		if attempt+1 >= sendMaxAttempts {
			break
		}
		// Conditional pacing: only sleep if the kernel bounced us fast.
		// If it held us for the full deadline already, retry immediately.
		if time.Since(start) < sendFastBounceMs*time.Millisecond {
			time.Sleep(sendPerAttemptMs * time.Millisecond)
		}
	}
	fmt.Printf("[uring.Send] target=%d ring=%d FAILED after %d attempts, err=%v\n",
		targetSID, ringIdx, sendMaxAttempts, lastErr)
	return lastErr
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
