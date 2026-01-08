//go:build qemuvirt && aarch64

package ksyscall

import (
	"unsafe"
)

// SyscallWrite implements the write(2) syscall
// For now, we only support stdout/stderr (fd 1 and 2) which write to UART
//
//go:nosplit
func SyscallWrite(fd, bufPtr, count, _, _, _ uint64) int64 {
	uartBase := getUartBase()

	// Debug marker - entry
	*(*byte)(unsafe.Pointer(uartBase)) = 'W'

	// Only support stdout/stderr for now
	if fd != 1 && fd != 2 {
		*(*byte)(unsafe.Pointer(uartBase)) = 'X'
		return -1 // EBADF
	}

	if count == 0 {
		*(*byte)(unsafe.Pointer(uartBase)) = '0'
		return 0
	}

	buf := unsafe.Pointer(uintptr(bufPtr))

	for i := uint64(0); i < count; i++ {
		c := *(*byte)(unsafe.Pointer(uintptr(buf) + uintptr(i)))

		// Auto-convert LF to CRLF for proper terminal display
		if c == '\n' {
			*(*byte)(unsafe.Pointer(uartBase)) = '\r'
		}

		*(*byte)(unsafe.Pointer(uartBase)) = c
	}

	// Debug marker - exit
	*(*byte)(unsafe.Pointer(uartBase)) = 'w'
	return int64(count)
}
