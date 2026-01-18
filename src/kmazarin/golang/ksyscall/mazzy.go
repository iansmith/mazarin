
package ksyscall

// MazzySyscallBase is the starting number for Mazzy-specific syscalls.
// Uses numbers starting at 0x1000 to avoid Linux's 0-512 range.
// Numbers are assigned alphabetically by name.
const MazzySyscallBase = 0x1000

// Mazzy syscall numbers (alphabetically ordered)
const (
	SysAllocPages   = MazzySyscallBase + iota // 0x1000 - Allocate page-aligned memory
	SysCreateThread                           // 0x1001 - Create new kernel thread
	SysEventHandled                           // 0x1002 - Signal event handling complete
	SysExit                                   // 0x1003 - Terminate current thin or priest
	SysExitThread                             // 0x1004 - Terminate current thread
	SysGetTime                                // 0x1005 - Get current time
	SysKill                                   // 0x1006 - Terminate a priest or thin
	SysLaunch                                 // 0x1007 - Create new priest from ELF
	SysReap                                   // 0x1008 - Clean up zombie thin
	SysRun                                    // 0x1009 - Create thin client in current priest
	SysStartPriest                            // 0x100A - Start a priest's first thread
	SysStartThin                              // 0x100B - Start a thin client
	SysWait                                   // 0x100C - Wait for priest or thin to exit
)

// mazzySyscallTable holds Mazzy-specific syscall handlers.
// Indexed by (syscallNum - MazzySyscallBase).
var mazzySyscallTable = [64]SyscallHandler{
	0:  SyscallAllocPages,   // 0x1000 - AllocPages
	1:  SyscallCreateThread, // 0x1001 - CreateThread
	2:  SyscallEventHandled, // 0x1002 - EventHandled
	3:  SyscallMazzyExit,    // 0x1003 - Exit (Mazzy version)
	4:  SyscallExitThread,   // 0x1004 - ExitThread
	5:  SyscallGetTime,      // 0x1005 - GetTime
	6:  SyscallMazzyKill,    // 0x1006 - Kill (Mazzy version)
	7:  SyscallLaunch,       // 0x1007 - Launch priest
	8:  SyscallReap,         // 0x1008 - Reap zombie thin
	9:  SyscallRun,          // 0x1009 - Run thin client
	10: SyscallStartPriest,  // 0x100A - StartPriest
	11: SyscallStartThin,    // 0x100B - StartThin
	12: SyscallMazzyWait,    // 0x100C - Wait (Mazzy version)
}
