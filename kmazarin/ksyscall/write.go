
package ksyscall

// write.go — SyscallWrite implementation.
//
// For fd 1/2 (stdout/stderr): pushes bytes to the PL011 TX ring buffer
// (actual UART output), then delegates to the linux shepherd for display
// routing (line accumulation, linux-ui). If the TX ring is completely full,
// the thread blocks on WaitingIO; the TX interrupt top-half drains from the
// thread's kernel buffer and wakes it when done.
//
// For eventfd writes: wakes the netpoll thread (epoll_wait).
//
// All other fds: returns EBADF (file writes are delegated at the dispatch
// level and never reach this handler).

import (
	"mazzy/kmazarin/kmem"
	"mazzy/kmazarin/serial"
	"mazzy/shared/sysid"
	"sync/atomic"
	_ "unsafe" // for go:linkname
)

// SyscallWrite implements the write(2) syscall.
//
// For fd 1/2: pushes bytes to the PL011 TX ring buffer, then delegates to
// the linux shepherd with the actual byte count written. Returns a short
// write if the ring can only accept some bytes. Blocks on WaitingIO if the
// ring is completely full.
//
//go:nosplit
func SyscallWrite(fd, bufPtr, count, _, _, _ uint64) int64 {
	// Handle eventfd writes — Go's netpollBreak mechanism. callerShepherd
	// (not CurrentShepherd): the KERNEL runtime's netpollBreak writes to the
	// kernel's own eventfd (proc.KernelShepherd); with a nil shepherd this
	// fell through to the stdout/stderr path and returned EBADF/EFAULT
	// (MAZ-136 netpoll family).
	p, _ := callerShepherd()
	if p.EventFd != 0 && fd == uint64(p.EventFd) {
		waiterTID := p.NetpollWaiterTID
		if waiterTID != 0 {
			WakeNetpollThread(waiterTID)
		} else {
			atomic.StoreUint32(&p.EventFdPending, 1)
		}
		return int64(count)
	}

	// Only support stdout/stderr
	if fd != 1 && fd != 2 {
		return -1 // EBADF
	}

	if count == 0 {
		return 0
	}

	if !isValidUserAddr(bufPtr) {
		return -14 // EFAULT
	}

	// Check if this is a re-entry after WaitingIO wake.
	// The TX interrupt handler has already pushed all bytes to the TX ring;
	// just report the byte count.
	complete, written := checkAndClearWaitingIO()
	if complete {
		return syscallWriteDelegate(uint64(written))
	}

	// Detect gctrace output: "gc N @..." on stderr (first 3 bytes).
	// We check here before the ring push loop for simplicity.
	if fd == 2 && count >= 3 {
		var peek [3]byte
		if kmem.CopyFromUser(peek[:], uintptr(bufPtr), 3) {
			if peek[0] == 'g' && peek[1] == 'c' && peek[2] == ' ' {
				sid := getCurrentThreadSID()
				if sid >= 0 && int(sid) < len(GCCountBySID) {
					atomic.AddUint64(&GCCountBySID[sid], 1)
				}
			}
		}
	}

	// MAZ-149 — thinned stdio: when fd 1/2 writes are delegated to a shepherd
	// (the caller is NOT the Write handler itself), route this write through the
	// blocking delegate so the linux shepherd's FD table decides where the bytes
	// go (console vs a redirected pipe/file). Skip the kernel UART push entirely
	// — the shepherd emits its own console output (via SysUartWrite) for
	// KindStdout/KindStderr. The kernel UART fast path below stays ONLY for
	// non-delegated writers: the linux shepherd's own output (it can't delegate
	// to itself — IsDelegated returns false when caller==handler), early boot,
	// or before a Write handler has registered.
	if IsDelegated(SysIDWrite, getCurrentThreadSID()) {
		return DelegateSyscall(sysid.Write, fd, bufPtr, count, 0, 0, 0)
	}

	// Push bytes to PL011 TX ring buffer.
	// Insert \r before each \n for serial terminal compatibility.
	// Track user bytes consumed (not counting inserted CRs).
	userBytesConsumed := uint64(0)
	remaining := count
	offset := uint64(0)
	for remaining > 0 {
		var chunk [256]byte
		n := remaining
		if n > 256 {
			n = 256
		}
		if !kmem.CopyFromUser(chunk[:n], uintptr(bufPtr+offset), int(n)) {
			if userBytesConsumed > 0 {
				break // partial copy is a short write
			}
			return -14 // EFAULT
		}
		for i := uint64(0); i < n; i++ {
			c := chunk[i]
			if c == '\n' {
				if !serial.QueueByteTry('\r') {
					goto doneRingPush
				}
			}
			if !serial.QueueByteTry(c) {
				goto doneRingPush
			}
			userBytesConsumed++
		}
		offset += n
		remaining -= n
	}
doneRingPush:

	if userBytesConsumed > 0 {
		// Short write or full write — report what we pushed to the TX ring.
		return syscallWriteDelegate(userBytesConsumed)
	}

	// Ring completely full: block on WaitingIO.
	return syscallWriteBlock(fd, bufPtr, count)
}

// syscallWriteDelegate returns the byte count for a write that already went out
// the kernel UART fast path. Post-MAZ-149 it is only reached by NON-delegated
// writers — a delegated fd 1/2 write short-circuits to a blocking DelegateSyscall
// above, before the UART push — so there is nothing left to delegate here (and
// re-delegating a WaitingIO-resumed write, whose bytes are already on the TX
// ring, would double-emit now that the shepherd is the serial emitter). The two
// call sites (WaitingIO re-entry, post-ring-push) are both non-delegated by
// construction, so this just reports what was written.
//
//go:noinline
func syscallWriteDelegate(count uint64) int64 {
	return int64(count)
}

// syscallWriteBlock handles the case where the TX ring is completely full.
// Copies user data into a CR-expanded kernel buffer, sets up WaitingIO state,
// and blocks the thread. The TX interrupt top-half will drain from the kernel
// buffer into the TX ring and wake the thread when done.
//
//go:noinline
func syscallWriteBlock(fd, bufPtr, count uint64) int64 {
	if count > 4096 {
		count = 4096
	}

	// Copy user data into a temporary buffer first.
	var raw [4096]byte
	if !kmem.CopyFromUser(raw[:count], uintptr(bufPtr), int(count)) {
		return -14 // EFAULT
	}

	// Count newlines to size the CR-expanded buffer.
	nlCount := 0
	for i := uint64(0); i < count; i++ {
		if raw[i] == '\n' {
			nlCount++
		}
	}

	// Build CR-expanded kernel buffer.
	expandedLen := int(count) + nlCount
	kbuf := make([]byte, expandedLen)
	j := 0
	for i := uint64(0); i < count; i++ {
		if raw[i] == '\n' {
			kbuf[j] = '\r'
			j++
		}
		kbuf[j] = raw[i]
		j++
	}

	// Set up WaitingIO state on the current thread.
	prepareWaitingIO(kbuf, expandedLen, int(count), byte(fd))

	// Block — the TX interrupt handler will drain kbuf into the TX ring.
	// When fully consumed, the thread is woken via RewindToSyscall and
	// re-enters SyscallWrite, where checkAndClearWaitingIO returns true.
	ctx := blockForWaitingIO()
	if ctx == 0 {
		return -11 // EAGAIN — no other thread to switch to
	}
	SetSyscallSwitchTarget(ctx)
	return 0
}

// Linkname bridges to main package (kmazarin/kmazarin).

//go:linkname checkAndClearWaitingIO main.CheckAndClearWaitingIO
func checkAndClearWaitingIO() (bool, int)

//go:linkname prepareWaitingIO main.PrepareWaitingIO
func prepareWaitingIO(buf []byte, total int, userBytes int, fd byte)

//go:linkname blockForWaitingIO main.BlockForWaitingIO
func blockForWaitingIO() uintptr
