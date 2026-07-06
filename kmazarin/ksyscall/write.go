
package ksyscall

// write.go — SyscallWrite implementation.
//
// For CONSOLE fd 1/2 (stdout/stderr, per-process redirect bit clear): pushes
// bytes to the PL011 TX ring buffer (actual UART output), then fire-and-forget
// delegates to the linux shepherd for display routing (line accumulation,
// linux-ui). If the TX ring is completely full, the thread blocks on
// WaitingIO; the TX interrupt top-half drains from the thread's kernel buffer
// and wakes it when done.
//
// For REDIRECTED fd 1/2 (redirect bit set — a fork/exec child whose stdio was
// dup3'd to a pipe/file, MAZ-149): the write is a BLOCKING delegate on the
// file lane; the linux shepherd routes it via its FD table to the capture
// pipe and replies the byte count. See proc.Shepherd.StdioRedirectMask.
//
// For eventfd writes: wakes the netpoll thread (epoll_wait).
//
// All other fds: returns EBADF (file writes are delegated at the dispatch
// level and never reach this handler).

import (
	"mazzy/kmazarin/kmem"
	"mazzy/kmazarin/proc"
	"mazzy/kmazarin/serial"
	"mazzy/shared/linuxabi"
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
		return syscallWriteDelegate(fd, bufPtr, uint64(written))
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

	// MAZ-149 — thinned stdio, redirect-flag split: a delegated shepherd's
	// fd 1/2 write takes one of two routes, decided by the per-process
	// redirect mask (computed by sysExecve from the child's FD table and
	// stored at creation — proc.Shepherd.StdioRedirectMask):
	//
	//   - bit SET (redirected to a pipe/file, or fd closed): BLOCKING
	//     delegate on the handler's default (file-lane) ring. The shepherd
	//     routes it via its FD table (KindPipeWrite → the capture pipe) and
	//     replies the actual byte count. Blocking is safe: a redirected
	//     write parks on its parent reader (pipe backpressure), never on fs,
	//     so no cross-shepherd cycle can form.
	//
	//   - bit CLEAR (console — every boot shepherd, and fork/exec children
	//     without redirected stdio): the kernel UART fast path below plus a
	//     fire-and-forget display delegate (syscallWriteDelegate). No
	//     shep.mu, no blocking — this is what avoids the deadlock the first
	//     MAZ-149 impl had (fs.maz printf inside a delegate handler →
	//     blocking console delegate → stdout lane wedged on a shepherd mu
	//     that fs holds while fs waits on its own write).
	//
	// Non-delegated writers (the linux shepherd itself — IsDelegated is
	// false when caller==handler, the deadlock guard — early boot, kernel
	// klog) always take the fast path.
	if IsDelegated(SysIDWrite, getCurrentThreadSID()) && stdioRedirected(p, fd) {
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
		// Short write or full write — forward what we pushed to the TX ring.
		return syscallWriteDelegate(fd, bufPtr, userBytesConsumed)
	}

	// Ring completely full: block on WaitingIO.
	return syscallWriteBlock(fd, bufPtr, count)
}

// syscallWriteDelegate forwards a CONSOLE fd 1/2 write to the linux shepherd
// for display routing (linux-ui line accumulator), with the byte count
// adjusted to what was actually pushed to the TX ring. The delegate is
// fire-and-forget — DelegateSyscall's console-stdio path returns immediately
// and the shepherd's stdout lane consumes the bytes and releases the data
// page; the serial emit already happened on the fast path above, so nothing
// blocks and nothing double-emits. Redirected writes never reach here (they
// short-circuit to a blocking DelegateSyscall before the UART push). If no
// delegate is registered (the linux shepherd's own writes, early boot),
// returns the count directly.
//
//go:noinline
func syscallWriteDelegate(fd, bufPtr, count uint64) int64 {
	callerSID := getCurrentThreadSID()
	if IsDelegated(SysIDWrite, callerSID) {
		return DelegateSyscall(sysid.Write, fd, bufPtr, count, 0, 0, 0)
	}
	// Linux shepherd's own writes: no delegation needed.
	return int64(count)
}

// stdioRedirected reports whether the caller's per-process stdio redirect bit
// for fd (1 or 2) is set — i.e. this write must take the blocking delegate
// route instead of the console fast path. Bit assignments are the wire-format
// constants linuxabi.StdioRedirectFd1/Fd2 (set at CloneExec child creation;
// zero for boot shepherds and the kernel).
//
//go:nosplit
func stdioRedirected(p *proc.Shepherd, fd uint64) bool {
	if p == nil {
		return false
	}
	bit := linuxabi.StdioRedirectFd1
	if fd == 2 {
		bit = linuxabi.StdioRedirectFd2
	}
	return p.StdioRedirectMask&bit != 0
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
