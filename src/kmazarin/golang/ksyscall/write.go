
package ksyscall

import (
	"kmazarin/console"
	"unsafe"
)

// SyscallWrite implements the write(2) syscall
// For now, we only support stdout/stderr (fd 1 and 2) which write to UART
// Uses console abstraction which provides spinlock protection
//
//go:nosplit
func SyscallWrite(fd, bufPtr, count, _, _, _ uint64) int64 {
	// Only support stdout/stderr for now
	if fd != 1 && fd != 2 {
		return -1 // EBADF
	}

	if count == 0 {
		return 0
	}

	buf := unsafe.Pointer(uintptr(bufPtr))

	for i := uint64(0); i < count; i++ {
		c := *(*byte)(unsafe.Pointer(uintptr(buf) + uintptr(i)))

		// Auto-convert LF to CRLF for proper terminal display
		if c == '\n' {
			console.Breadcrumb('\r')
		}

		// Use Breadcrumb instead of KWriteByte to ensure output even before
		// console is initialized (Breadcrumb has hardcoded UART address)
		console.Breadcrumb(c)
	}

	return int64(count)
}
