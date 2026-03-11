package ksyscall

// uart_write.go — SysUartWrite and SysUartWriteBlocking syscalls.
//
// These allow userspace priests (particularly stdio) to push bytes directly
// into the UART output path. This decouples screen rendering (fast, handled
// by stdio via delegated SyscallWrite) from serial output (slow, UART speed).
//
// Two variants:
//   - SysUartWrite (non-blocking): writes what fits, drops the rest, returns count written
//   - SysUartWriteBlocking: writes all bytes, blocking until UART has space

import (
	"mazzy/kmazarin/kmem"
	"mazzy/kmazarin/serial"
	"sync/atomic"
)

// SyscallUartWrite writes bytes from a user buffer to the UART.
// Non-blocking: writes what the hardware can accept, returns count written.
// Drops bytes that don't fit (caller should not retry dropped bytes).
//
// arg0 = bufPtr (user VA)
// arg1 = count (bytes to write)
// Returns: number of bytes actually written, or negative errno.
//
//go:noinline
func SyscallUartWrite(arg0, arg1, _, _, _, _ uint64) int64 {
	bufPtr := arg0
	count := arg1

	// When serial is suppressed, pretend we wrote everything.
	// Stdio priest's display is the primary output path.
	if atomic.LoadUint32(&suppressSerial) != 0 {
		return int64(count)
	}

	if count == 0 {
		return 0
	}
	if !isValidUserAddr(bufPtr) {
		return -14 // EFAULT
	}
	if count > 4096 {
		count = 4096
	}

	var chunk [256]byte
	written := uint64(0)
	remaining := count
	offset := uint64(0)

	for remaining > 0 {
		n := remaining
		if n > 256 {
			n = 256
		}
		if !kmem.CopyFromUser(chunk[:n], uintptr(bufPtr+offset), int(n)) {
			if written > 0 {
				return int64(written)
			}
			return -14 // EFAULT
		}
		for i := uint64(0); i < n; i++ {
			// TODO: When PL011 TX interrupt is wired, use txBuf.WriteByte
			// and return early if txBuf is full (non-blocking).
			// For now, use PollWrite which busy-waits per byte.
			serial.PollWrite(chunk[i])
			written++
		}
		offset += n
		remaining -= n
	}

	return int64(written)
}

// SyscallUartWriteBlocking writes all bytes from a user buffer to the UART.
// Blocks until all bytes are written (waits for UART FIFO space).
//
// arg0 = bufPtr (user VA)
// arg1 = count (bytes to write)
// Returns: count on success, or negative errno.
//
//go:noinline
func SyscallUartWriteBlocking(arg0, arg1, _, _, _, _ uint64) int64 {
	bufPtr := arg0
	count := arg1

	// When serial is suppressed, pretend we wrote everything.
	if atomic.LoadUint32(&suppressSerial) != 0 {
		return int64(count)
	}

	if count == 0 {
		return 0
	}
	if !isValidUserAddr(bufPtr) {
		return -14 // EFAULT
	}
	if count > 4096 {
		count = 4096
	}

	var chunk [256]byte
	remaining := count
	offset := uint64(0)

	for remaining > 0 {
		n := remaining
		if n > 256 {
			n = 256
		}
		if !kmem.CopyFromUser(chunk[:n], uintptr(bufPtr+offset), int(n)) {
			return -14 // EFAULT
		}
		for i := uint64(0); i < n; i++ {
			// PollWrite already busy-waits for FIFO space.
			// TODO: When PL011 TX interrupt is wired, use txBuf and
			// block the thread (via BlockOnSlot) when txBuf is full,
			// waking when TX interrupt drains some bytes.
			serial.PollWrite(chunk[i])
		}
		offset += n
		remaining -= n
	}

	return int64(count)
}
