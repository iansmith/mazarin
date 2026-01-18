
package ksyscall

// MazzySyscallBase is the starting number for Mazzy-specific syscalls.
// Uses numbers starting at 0x1000 to avoid Linux's 0-512 range.
// Numbers are assigned alphabetically by name.
const MazzySyscallBase = 0x1000

// Mazzy syscall numbers
const (
	SysGetTime = MazzySyscallBase + iota // 0x1000
	SysLaunch                            // 0x1001 - Create new priest from ELF
	SysRun                               // 0x1002 - Create thin client in current priest
	SysAllocPages                        // 0x1003 - Allocate page-aligned memory
	SysExit                              // 0x1004 - Terminate current thin or priest
	SysReap                              // 0x1005 - Clean up zombie thin
	SysStartThin                         // 0x1006 - Start a thin client
	SysEventHandled                      // 0x1007 - Signal event handling complete
	SysCreateThread                      // 0x1008 - Create new kernel thread
	SysExitThread                        // 0x1009 - Terminate current thread
	SysStartPriest                       // 0x100A - Start a priest's first thread
	SysKill                              // 0x100B - Terminate a priest or thin
	SysWait                              // 0x100C - Wait for priest or thin to exit
)

// mazzySyscallTable holds Mazzy-specific syscall handlers.
// Indexed by (syscallNum - MazzySyscallBase).
var mazzySyscallTable = [64]SyscallHandler{
	0:  SyscallGetTime,      // 0x1000 - GetTime
	1:  SyscallLaunch,       // 0x1001 - Launch priest
	2:  SyscallRun,          // 0x1002 - Run thin client
	3:  SyscallAllocPages,   // 0x1003 - Allocate pages
	4:  SyscallMazzyExit,    // 0x1004 - Exit (Mazzy version)
	5:  SyscallReap,         // 0x1005 - Reap zombie thin
	6:  SyscallStartThin,    // 0x1006 - Start thin
	7:  SyscallEventHandled, // 0x1007 - Event handled
	8:  SyscallCreateThread, // 0x1008 - Create thread
	9:  SyscallExitThread,   // 0x1009 - Exit thread
	10: SyscallStartPriest,  // 0x100A - Start priest
	11: SyscallMazzyKill,    // 0x100B - Kill (Mazzy version)
	12: SyscallMazzyWait,    // 0x100C - Wait (Mazzy version)
}
