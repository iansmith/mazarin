package sys

import (
	"errors"
	"mazzy/shared/iouring"
	"mazzy/shared/mazzy"
	"syscall"
	"unsafe"
)

// IOUringSetup creates an io_uring instance backed by the given ring page.
// The page must be pre-allocated via mem.AllocPages and page-aligned.
// Returns the ringID (small integer) on success.
func IOUringSetup(ringPage *iouring.IORing, flags uint32) (int, error) {
	r1, _, errno := RawSyscall(mazzy.SysIOUringSetup,
		0, // capacity hint (ignored)
		uintptr(unsafe.Pointer(ringPage)),
		uintptr(flags), 0, 0, 0)

	if errno != 0 || int64(r1) < 0 {
		return -1, errors.New("IOUringSetup failed")
	}
	return int(r1), nil
}

// IOUringEnter submits SQEs and waits for completions.
// toSubmit: number of SQEs to consume from the SQ ring.
// minComplete: minimum CQEs to wait for (0 = don't wait).
// Returns the number of CQEs available, or an error.
//
// Uses RawSyscall — the Go P is held throughout. The kernel thread sleeps
// until completions arrive (IRQ direct wake) or the 10ms timeout expires.
// Suitable for short-duration waits (block I/O) where holding the P is acceptable.
func IOUringEnter(ringID int, toSubmit, minComplete, flags uint32) (int, error) {
	r1, _, errno := RawSyscall(mazzy.SysIOUringEnter,
		uintptr(ringID),
		uintptr(toSubmit),
		uintptr(minComplete),
		uintptr(flags), 0, 0)

	if errno != 0 || int64(r1) < 0 {
		return int(r1), errors.New("IOUringEnter failed")
	}
	return int(r1), nil
}

// IOUringEnterBlocking is like IOUringEnter but releases the P before blocking.
// Uses entersyscallblock so the Go runtime can schedule other goroutines while
// this thread waits for completions. Use this for long-duration waits (e.g.
// waiting for HID input events) where holding the P would freeze the shepherd.
func IOUringEnterBlocking(ringID int, toSubmit, minComplete, flags uint32) (int, error) {
	runtime_entersyscallblock()
	r1, _, errno := syscall.RawSyscall6(mazzy.SysIOUringEnter,
		uintptr(ringID),
		uintptr(toSubmit),
		uintptr(minComplete),
		uintptr(flags), 0, 0)
	runtime_exitsyscall()

	if errno != 0 || int64(r1) < 0 {
		return int(r1), errors.New("IOUringEnter failed")
	}
	return int(r1), nil
}
