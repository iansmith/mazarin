
package ksyscall

import "mazzy/kmazarin/console"

// MazzySyscallBase is the starting number for Mazzy-specific syscalls.
// Uses numbers starting at 0x1000 to avoid Linux's 0-512 range.
// Numbers are assigned alphabetically by name.
const MazzySyscallBase = 0x1000

// Mazzy syscall numbers (numerically ordered)
const (
	SysGetTime             = MazzySyscallBase + 0 // 0x1000 - Get current time
	SysLaunch              = MazzySyscallBase + 1 // 0x1001 - Launch a priest from ELF file
	SysRun                 = MazzySyscallBase + 2 // 0x1002 - Load a .maz program into priest's address space
	SysAllocPages          = MazzySyscallBase + 3 // 0x1003 - Allocate pages for userspace
	SysExit                = MazzySyscallBase + 4 // 0x1004 - Exit program (Mazzy-specific)
	SysReap                = MazzySyscallBase + 5 // 0x1005 - Reap terminated program
	SysDebugPrint          = MazzySyscallBase + 6 // 0x1006 - Debug print arguments
	SysGetFramebuffer      = MazzySyscallBase + 7 // 0x1007 - Get framebuffer info
	SysWaitKernelAsync     = MazzySyscallBase + 8 // 0x1008 - Wait for kernel async message
	SysRegisterAsyncPreempt = MazzySyscallBase + 9 // 0x1009 - Register asyncPreempt address for goroutine preemption
)

// mazzySyscallTable holds Mazzy-specific syscall handlers.
// Indexed by (syscallNum - MazzySyscallBase).
var mazzySyscallTable = [64]SyscallHandler{
	0: SyscallGetTime,              // GetTime = 0x1000
	1: SyscallLaunch,               // Launch = 0x1001
	2: SyscallRun,                  // Run = 0x1002
	3: nil,                         // AllocPages = 0x1003 (not yet implemented)
	4: SyscallMazzyExit,            // Exit = 0x1004
	5: nil,                         // Reap = 0x1005 (not yet implemented)
	6: SyscallDebugPrint,           // DebugPrint = 0x1006
	7: SyscallGetFramebuffer,       // GetFramebuffer = 0x1007
	8: SyscallWaitKernelAsync,      // WaitKernelAsync = 0x1008
	9: SyscallRegisterAsyncPreempt, // RegisterAsyncPreempt = 0x1009
}

// SyscallDebugPrint prints debug arguments from userspace.
// arg0 = marker/id, arg1-arg5 = values to print
// Special case: if v1-v5 are all 0 and marker < 256, print marker as a character
//
//go:nosplit
func SyscallDebugPrint(marker, v1, v2, v3, v4, v5 uint64) int64 {
	// Special case: single character output (no lock overhead)
	if v1 == 0 && v2 == 0 && v3 == 0 && v4 == 0 && v5 == 0 && marker < 256 {
		console.Breadcrumb(byte(marker))
		return 0
	}
	// Full debug print
	console.KWriteString("\r\n[DBG ")
	console.KPrintHex64(marker)
	console.KWriteString("] ")
	console.KPrintHex64(v1)
	console.KWriteString(" ")
	console.KPrintHex64(v2)
	console.KWriteString(" ")
	console.KPrintHex64(v3)
	console.KWriteString(" ")
	console.KPrintHex64(v4)
	console.KWriteString(" ")
	console.KPrintHex64(v5)
	console.KWriteString("\r\n")
	return 0
}
