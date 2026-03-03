
package ksyscall

import (
	"mazzy/kmazarin/console"
	"mazzy/kmazarin/serial"
)

// MazzySyscallBase is the starting number for Mazzy-specific syscalls.
// Uses numbers starting at 0x1000 to avoid Linux's 0-512 range.
// Numbers are assigned alphabetically by name.
const MazzySyscallBase = 0x1000

// Mazzy syscall numbers (numerically ordered)
const (
	SysGetTime             = MazzySyscallBase + 0 // 0x1000 - Get current time
	SysLaunch              = MazzySyscallBase + 1 // 0x1001 - Launch a priest from ELF file
	SysBootstrapRunElf     = MazzySyscallBase + 2 // 0x1002 - Bootstrap: load ELF from disk (disk manager only)
	SysAllocPages          = MazzySyscallBase + 3 // 0x1003 - Allocate pages for userspace
	SysExit                = MazzySyscallBase + 4 // 0x1004 - Exit program (Mazzy-specific)
	SysReap                = MazzySyscallBase + 5 // 0x1005 - Reap terminated program
	SysDebugPrint          = MazzySyscallBase + 6 // 0x1006 - Debug print arguments
	SysGetFramebuffer      = MazzySyscallBase + 7 // 0x1007 - Get framebuffer info
	SysWaitKernelAsync     = MazzySyscallBase + 8 // 0x1008 - Wait for kernel async message
	SysWaitSoftIRQ          = MazzySyscallBase + 10 // 0x100A - Wait for soft IRQ events on a slot
	SysRegisterSoftIRQ      = MazzySyscallBase + 11 // 0x100B - Register an IRQ on a soft IRQ slot
	SysQueryInputDevices    = MazzySyscallBase + 12 // 0x100C - Query available input devices
	SysFlushFramebuffer     = MazzySyscallBase + 13 // 0x100D - Flush framebuffer region to display
	SysSetTimerDeadline     = MazzySyscallBase + 14 // 0x100E - Set timer deadline on soft IRQ slot
	SysSetScanoutOffset     = MazzySyscallBase + 15 // 0x100F - Set scanout Y offset for hardware scrolling
	SysTransferPages        = MazzySyscallBase + 16 // 0x1010 - Transfer pages between priests
	SysMapSharedPage        = MazzySyscallBase + 17 // 0x1011 - Map shared page from another priest
	SysLoadMaz              = MazzySyscallBase + 18 // 0x1012 - Load .maz PIE ELF into priest's address space
)

// mazzySyscallTable holds Mazzy-specific syscall handlers.
// Indexed by (syscallNum - MazzySyscallBase).
var mazzySyscallTable = [64]SyscallHandler{
	0: SyscallGetTime,              // GetTime = 0x1000
	1: SyscallLaunch,               // Launch = 0x1001
	2: SyscallBootstrapRunElf,      // BootstrapRunElf = 0x1002
	3: nil,                         // AllocPages = 0x1003 (not yet implemented)
	4: SyscallMazzyExit,            // Exit = 0x1004
	5: nil,                         // Reap = 0x1005 (not yet implemented)
	6: SyscallDebugPrint,           // DebugPrint = 0x1006
	7: SyscallGetFramebuffer,       // GetFramebuffer = 0x1007
	8: SyscallWaitKernelAsync,      // WaitKernelAsync = 0x1008
	// slot 9 removed (was RegisterAsyncPreempt)
	10: SyscallWaitSoftIRQ,          // WaitSoftIRQ = 0x100A
	11: SyscallRegisterSoftIRQ,      // RegisterSoftIRQ = 0x100B
	12: SyscallQueryInputDevices,    // QueryInputDevices = 0x100C
	13: SyscallFlushFramebuffer,    // FlushFramebuffer = 0x100D
	14: SyscallSetTimerDeadline,    // SetTimerDeadline = 0x100E
	15: SyscallSetScanoutOffset,    // SetScanoutOffset = 0x100F
	16: SyscallTransferPages,      // TransferPages = 0x1010
	17: SyscallMapSharedPage,      // MapSharedPage = 0x1011
	18: SyscallLoadMaz,            // LoadMaz = 0x1012
}

// SyscallDebugPrint prints debug arguments from userspace.
// arg0 = marker/id, arg1-arg5 = values to print
// Special case: if v1-v5 are all 0 and marker < 256, print marker as a character
//
//go:nosplit
func SyscallDebugPrint(marker, v1, v2, v3, v4, v5 uint64) int64 {
	// Special case: single character output — use same routing as SyscallWrite.
	if v1 == 0 && v2 == 0 && v3 == 0 && v4 == 0 && v5 == 0 && marker < 256 {
		ownerPID := getUartSlotPriestID()
		if ownerPID >= 0 && getCurrentThreadPID() != ownerPID {
			console.KWriteByte(byte(marker))
		} else {
			serial.PollWrite(byte(marker))
		}
		return 0
	}
	// Full debug print — disabled during investigation
	_ = marker
	_ = v1
	_ = v2
	_ = v3
	_ = v4
	_ = v5
	return 0
}
