
package ksyscall

import "kmazarin/console"

// haltForever loops forever using WFI (implemented in assembly)
func haltForever()

// SyscallExit implements the exit(2) syscall (syscall 93)
// Gracefully halts the CPU when a process exits.
//
//go:nosplit
func SyscallExit(status, _, _, _, _, _ uint64) int64 {
	console.KWriteString("\r\n=== EXIT CALLED ===\r\nStatus: ")

	// Print status as decimal
	hexChars := "0123456789ABCDEF"
	tens := (status / 10) % 10
	ones := status % 10
	if tens > 0 {
		console.KWriteByte(hexChars[tens])
	}
	console.KWriteByte(hexChars[ones])
	console.KWriteString("\r\n=== PROCESS EXITED, HALTING ===\r\n")

	haltForever()
	return 0 // unreachable
}

// SyscallExitGroup implements the exit_group(2) syscall (syscall 94)
// Same as exit for a single-threaded kernel.
//
//go:nosplit
func SyscallExitGroup(status, _, _, _, _, _ uint64) int64 {
	console.KWriteString("\r\n=== EXIT GROUP CALLED ===\r\nStatus: ")

	// Print status as decimal
	hexChars := "0123456789ABCDEF"
	tens := (status / 10) % 10
	ones := status % 10
	if tens > 0 {
		console.KWriteByte(hexChars[tens])
	}
	console.KWriteByte(hexChars[ones])
	console.KWriteString("\r\n=== PROCESS EXITED, HALTING ===\r\n")

	haltForever()
	return 0 // unreachable
}

// SyscallMazzyExit implements the Mazzy SysExit syscall (0x1004)
// This is called by userspace programs through priest to cleanly exit.
// For now, just prints the status and panics the kernel.
//
// arg0: exit status code
func SyscallMazzyExit(status, _, _, _, _, _ uint64) int64 {
	console.KWriteString("\r\n")
	console.KWriteString("=== PROGRAM EXIT (SysExit) ===\r\n")
	console.KWriteString("Status: ")
	printDecimalNonRecursive(status)
	console.KWriteString("\r\n")

	// For now, just panic the kernel
	// TODO: Clean up program resources, notify priest, continue execution
	KernelPanic("SysExit called - program terminated")

	return 0 // unreachable
}

// printDecimalNonRecursive prints a uint64 as decimal to console
// Uses a fixed-size buffer to avoid recursion (for nosplit compatibility)
func printDecimalNonRecursive(n uint64) {
	// Max uint64 is 18446744073709551615 (20 digits)
	var buf [20]byte
	i := len(buf) - 1

	// Handle zero specially
	if n == 0 {
		console.KWriteByte('0')
		return
	}

	// Build digits from right to left
	for n > 0 {
		buf[i] = byte('0' + n%10)
		n /= 10
		i--
	}

	// Print digits from left to right
	for j := i + 1; j < len(buf); j++ {
		console.KWriteByte(buf[j])
	}
}
