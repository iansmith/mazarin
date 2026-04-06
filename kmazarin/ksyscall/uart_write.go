package ksyscall

// uart_write.go — SysUartWrite syscall.
//
// Allows userspace shepherds (particularly linux) to push bytes directly
// into the UART output path. This decouples screen rendering (fast, handled
// by linux via delegated SyscallWrite) from serial output (slow, UART speed).

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
			serial.PollWrite(chunk[i])
			written++
		}
		offset += n
		remaining -= n
	}

	return int64(written)
}

