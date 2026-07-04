package ksyscall

// uart_write.go — SysUartWrite syscall.
//
// Allows userspace shepherds (particularly linux) to push bytes directly
// into the UART output path. This decouples screen rendering (fast, handled
// by linux via delegated SyscallWrite) from serial output.
//
// Bytes go through the interrupt-driven TX ring (serial.QueueByteTry), the same
// fast path SyscallWrite's fd 1/2 fast path uses — NOT the byte-at-a-time
// polling path. A byte that doesn't fit the ring falls back to serial.PollWrite
// so a status message is never silently dropped (matches the old guaranteed-
// delivery contract, but only pays the slow-poll cost under ring pressure).
// This matters because the linux shepherd routes real program stdout/stderr
// through SysUartWrite once fd 1/2 writes are thinned onto the FD table
// (MAZ-149): that traffic must not land on the slow direct-poll path.

import (
	"mazzy/kmazarin/kmem"
	"mazzy/kmazarin/serial"
)

// SyscallUartWrite writes bytes from a user buffer to the UART TX ring buffer.
// Non-blocking: pushes what fits, drops the rest. Interrupt-driven drain.
//
// NOTE: This syscall is NOT gated by suppressSerial. The caller (linux shepherd)
// explicitly wants to write to UART — the suppressSerial flag only controls
// whether SyscallWrite's ring-buffer path auto-echoes to serial.
//
// arg0 = bufPtr (user VA)
// arg1 = count (bytes to write)
// Returns: number of bytes actually written, or negative errno.
//
//go:noinline
func SyscallUartWrite(arg0, arg1, _, _, _, _ uint64) int64 {
	bufPtr := arg0
	count := arg1

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
			// Fast path: push onto the interrupt-driven TX ring. Only if the
			// ring is full (or not yet initialized) fall back to the slow,
			// guaranteed byte-poll so the byte is never dropped.
			if !serial.QueueByteTry(chunk[i]) {
				serial.PollWrite(chunk[i])
			}
			written++
		}
		offset += n
		remaining -= n
	}

	return int64(written)
}

