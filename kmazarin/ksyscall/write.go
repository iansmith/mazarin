
package ksyscall

import (
	"mazzy/kmazarin/kmem"
	"mazzy/kmazarin/proc"
	"mazzy/kmazarin/serial"
	"sync/atomic"
	_ "unsafe" // for go:linkname
)

// suppressSerial mirrors the runtime.suppressSerial flag.
// When 0, userspace write() output is echoed to the serial port.
// When 1, output goes only to the ring buffer (normal production mode).
//
//go:linkname suppressSerial runtime.suppressSerial
var suppressSerial uint32

// SyscallWrite implements the write(2) syscall.
// For now, we only support stdout/stderr (fd 1 and 2).
//
// Routing logic:
//   - If the caller is the linux shepherd (UART ring owner), writes are
//     silently dropped (it cannot push into its own ring without deadlock).
//   - If the caller is any other shepherd, bytes are pushed through the
//     ring buffer so the linux shepherd can display them. The fd number
//     is carried through the ring so linux can color stderr differently.
//   - If no shepherd owns the UART slot yet (early boot), writes are dropped.
//
//go:nosplit
func SyscallWrite(fd, bufPtr, count, _, _, _ uint64) int64 {
	// Handle eventfd writes (fd 11) — Go's netpollBreak mechanism.
	// When Go's runtime calls write(eventfd, ...) it is trying to wake
	// a thread sleeping in epoll_wait (our SyscallEpollPwait).
	// We look up the shepherd's NetpollWaiterTID and wake that thread.
	if fd == 11 {
		p := proc.CurrentShepherd()
		if p != nil {
			waiterTID := p.NetpollWaiterTID
			if waiterTID != 0 {
				WakeNetpollThread(waiterTID)
			}
		}
		return int64(count) // Success — pretend we wrote the bytes
	}

	// Only support stdout/stderr for now
	if fd != 1 && fd != 2 {
		return -1 // EBADF
	}

	if count == 0 {
		return 0
	}

	// Validate user buffer address - reject NULL and kernel addresses
	if !isValidUserAddr(bufPtr) {
		return -14 // EFAULT
	}

	// Route to ring buffer for display by the linux shepherd.
	// The linux shepherd itself (UART ring owner) cannot use the ring
	// (it would deadlock consuming its own output).
	// If no owner registered yet, fall back to direct serial output
	// so early panic messages are visible.
	useRing := false
	useDirect := false
	ownerSID := getUartSlotShepherdID()
	echoToSerial := atomic.LoadUint32(&suppressSerial) == 0
	if ownerSID >= 0 {
		callerSID := getCurrentThreadSID()
		if callerSID != ownerSID {
			useRing = true
		} else {
			// linux shepherd: can't use ring (deadlock), always write direct to serial
			useDirect = true
		}
	} else {
		// No ring owner yet — write directly to serial
		useDirect = true
	}

	remaining := count
	offset := uint64(0)
	fdByte := byte(fd)
	for remaining > 0 {
		var chunk [256]byte
		n := remaining
		if n > 256 {
			n = 256
		}
		if !kmem.CopyFromUser(chunk[:n], uintptr(bufPtr+offset), int(n)) {
			return -14 // EFAULT
		}
		// Detect gctrace output: "gc N @..." on stderr.
		// Increment per-shepherd GC counter on first chunk only.
		if offset == 0 && fd == 2 && n >= 3 && chunk[0] == 'g' && chunk[1] == 'c' && chunk[2] == ' ' {
			pid := getCurrentThreadSID()
			if pid >= 0 && int(pid) < len(GCCountBySID) {
				atomic.AddUint64(&GCCountBySID[pid], 1)
			}
		}
		if useRing {
			for i := uint64(0); i < n; i++ {
				c := chunk[i]
				if c == '\n' {
					pushByteToUartRing(fdByte, '\r')
				}
				pushByteToUartRing(fdByte, c)
			}
			// Echo to serial for diagnostic output (non-owner shepherds).
			// stdout: interrupt-driven TX ring buffer, gated by echoToSerial.
			// stderr: synchronous PollWrite, always (panics/tracebacks must reach serial).
			if fd == 2 {
				for i := uint64(0); i < n; i++ {
					c := chunk[i]
					if c == '\n' {
						serial.PollWrite('\r')
					}
					serial.PollWrite(c)
				}
			} else if echoToSerial {
				for i := uint64(0); i < n; i++ {
					c := chunk[i]
					if c == '\n' {
						serial.QueueByte('\r')
					}
					serial.QueueByte(c)
				}
			}
		} else if useDirect {
			// linux shepherd's own writes: fd-based routing.
			// stderr: always PollWrite (guaranteed delivery).
			// stdout: QueueByte (interrupt-driven).
			if fd == 2 {
				for i := uint64(0); i < n; i++ {
					c := chunk[i]
					if c == '\n' {
						serial.PollWrite('\r')
					}
					serial.PollWrite(c)
				}
			} else {
				for i := uint64(0); i < n; i++ {
					c := chunk[i]
					if c == '\n' {
						serial.QueueByte('\r')
					}
					serial.QueueByte(c)
				}
			}
		}
		offset += n
		remaining -= n
	}

	if useRing {
		flushUartRingWake()
	}

	return int64(count)
}

// getUartSlotShepherdID returns the shepherd ID that owns the UART serial slot.
// Returns -1 if no shepherd has registered.
//
//go:nosplit
//go:linkname getUartSlotShepherdID main.GetUartSlotShepherdID
func getUartSlotShepherdID() int16

// pushByteToUartRing pushes a byte into the UART ring buffer with fd info.
// The fd is carried in the HIDEvent.Code field so the consumer can
// distinguish stdout (1) from stderr (2).
//
//go:linkname pushByteToUartRing main.PushByteToUartRing
func pushByteToUartRing(fd byte, b byte)

// flushUartRingWake wakes the UART slot consumer after pushing bytes.
//
//go:linkname flushUartRingWake main.FlushUartRingWake
func flushUartRingWake()
