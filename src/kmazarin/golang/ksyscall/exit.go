//go:build qemuvirt && aarch64

package ksyscall

import "unsafe"

// SyscallExit implements the exit(2) syscall (syscall 93)
// For now, just panic - we don't have a clean shutdown mechanism yet
//
//go:nosplit
func SyscallExit(status, _, _, _, _, _ uint64) int64 {
	// Print exit status directly to UART before calling KernelPanic
	uartBase := getUartBase()

	*(*byte)(unsafe.Pointer(uartBase)) = '\r'
	*(*byte)(unsafe.Pointer(uartBase)) = '\n'
	*(*byte)(unsafe.Pointer(uartBase)) = '='
	*(*byte)(unsafe.Pointer(uartBase)) = '='
	*(*byte)(unsafe.Pointer(uartBase)) = '='
	*(*byte)(unsafe.Pointer(uartBase)) = ' '
	*(*byte)(unsafe.Pointer(uartBase)) = 'E'
	*(*byte)(unsafe.Pointer(uartBase)) = 'X'
	*(*byte)(unsafe.Pointer(uartBase)) = 'I'
	*(*byte)(unsafe.Pointer(uartBase)) = 'T'
	*(*byte)(unsafe.Pointer(uartBase)) = ' '
	*(*byte)(unsafe.Pointer(uartBase)) = 'C'
	*(*byte)(unsafe.Pointer(uartBase)) = 'A'
	*(*byte)(unsafe.Pointer(uartBase)) = 'L'
	*(*byte)(unsafe.Pointer(uartBase)) = 'L'
	*(*byte)(unsafe.Pointer(uartBase)) = 'E'
	*(*byte)(unsafe.Pointer(uartBase)) = 'D'
	*(*byte)(unsafe.Pointer(uartBase)) = ' '
	*(*byte)(unsafe.Pointer(uartBase)) = '='
	*(*byte)(unsafe.Pointer(uartBase)) = '='
	*(*byte)(unsafe.Pointer(uartBase)) = '='
	*(*byte)(unsafe.Pointer(uartBase)) = '\r'
	*(*byte)(unsafe.Pointer(uartBase)) = '\n'
	*(*byte)(unsafe.Pointer(uartBase)) = 'S'
	*(*byte)(unsafe.Pointer(uartBase)) = 't'
	*(*byte)(unsafe.Pointer(uartBase)) = 'a'
	*(*byte)(unsafe.Pointer(uartBase)) = 't'
	*(*byte)(unsafe.Pointer(uartBase)) = 'u'
	*(*byte)(unsafe.Pointer(uartBase)) = 's'
	*(*byte)(unsafe.Pointer(uartBase)) = ':'
	*(*byte)(unsafe.Pointer(uartBase)) = ' '

	// Print status as decimal
	hexChars := "0123456789ABCDEF"
	tens := (status / 10) % 10
	ones := status % 10
	if tens > 0 {
		*(*byte)(unsafe.Pointer(uartBase)) = hexChars[tens]
	}
	*(*byte)(unsafe.Pointer(uartBase)) = hexChars[ones]
	*(*byte)(unsafe.Pointer(uartBase)) = '\r'
	*(*byte)(unsafe.Pointer(uartBase)) = '\n'

	KernelPanic("Runtime called exit() during initialization")
	return 0 // unreachable
}

// SyscallExitGroup implements the exit_group(2) syscall (syscall 94)
// Same as exit for a single-threaded kernel
//
//go:nosplit
func SyscallExitGroup(status, _, _, _, _, _ uint64) int64 {
	KernelPanic("Process called exit_group()")
	return 0 // unreachable
}
