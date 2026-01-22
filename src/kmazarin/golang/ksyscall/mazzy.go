
package ksyscall

import "kmazarin/console"

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
	SysWaitSoftIRQ         = MazzySyscallBase + 8 // 0x1008 - Wait for soft IRQ
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
	8: SyscallWaitSoftIRQ,          // WaitSoftIRQ = 0x1008
	9: SyscallRegisterAsyncPreempt, // RegisterAsyncPreempt = 0x1009
}

// SyscallDebugPrint prints debug arguments from userspace.
// arg0 = marker/id, arg1-arg5 = values to print
//
//go:nosplit
func SyscallDebugPrint(marker, v1, v2, v3, v4, v5 uint64) int64 {
	console.KWriteString("\r\n[DBG ")
	printHexDigits(marker)
	console.KWriteString("] ")
	printHexDigits(v1)
	console.KWriteString(" ")
	printHexDigits(v2)
	console.KWriteString(" ")
	printHexDigits(v3)
	console.KWriteString(" ")
	printHexDigits(v4)
	console.KWriteString(" ")
	printHexDigits(v5)
	console.KWriteString("\r\n")
	return 0
}
