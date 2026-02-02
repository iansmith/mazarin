
package ksyscall

import (
	"mazzy/kmazarin/console"
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

	// Validate user buffer address - reject NULL and kernel addresses
	if !isValidUserAddr(bufPtr) {
		return -14 // EFAULT
	}

	buf := unsafe.Pointer(uintptr(bufPtr))

	for i := uint64(0); i < count; i++ {
		c := *(*byte)(unsafe.Pointer(uintptr(buf) + uintptr(i)))

		// Auto-convert LF to CRLF for proper terminal display
		if c == '\n' {
			console.Breadcrumb('\r')
		}

		// SyscallWrite handles BOTH userspace priest output AND the kernel's
		// own fmt.Println (via DispatchFromOverlay). Using Breadcrumb (direct
		// MMIO) is correct here because:
		// 1. Userspace priests must not write into the ring buffer — the stdio
		//    priest consumes that ring, so it would deadlock on its own output.
		// 2. Kernel fmt.Println output that should go through the ring already
		//    uses console.KPrintf / console.KWriteByte directly.
		// 3. Breadcrumb works before console is initialized.
		console.Breadcrumb(c)
	}

	return int64(count)
}
