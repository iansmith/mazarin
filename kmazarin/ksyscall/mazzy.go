
package ksyscall

import (
	"mazzy/kmazarin/kmem"
	"mazzy/kmazarin/serial"
)

// mazzySyscallTable holds Mazzy-specific syscall handlers.
// Indexed by (syscallNum - mazzy.MazzySyscallBase).
var mazzySyscallTable = [74]SyscallHandler{
	0: SyscallGetTime,              // GetTime = 0x1000
	1: SyscallSubscribeDeaths,      // SubscribeDeaths = 0x1001
	2: SyscallFreePages,            // FreePages = 0x1002
	3: SyscallAllocPages,           // AllocPages = 0x1003
	4: SyscallMazzyExit,            // Exit = 0x1004
	5: nil,                         // Reap = 0x1005 (not yet implemented)
	6: SyscallDebugPrint,           // DebugPrint = 0x1006
	7: SyscallGetFramebuffer,       // GetFramebuffer = 0x1007
	8: SyscallWaitKernelAsync,      // WaitKernelAsync = 0x1008
	9: SyscallUringSetup,            // UringSetup = 0x1009
	10: SyscallWaitSoftIRQ,          // WaitSoftIRQ = 0x100A
	11: SyscallRegisterSoftIRQ,      // RegisterSoftIRQ = 0x100B
	12: SyscallQueryInputDevices,    // QueryInputDevices = 0x100C
	13: SyscallFlushFramebuffer,    // FlushFramebuffer = 0x100D
	14: SyscallSetTimerDeadline,    // SetTimerDeadline = 0x100E
	15: SyscallSetScanoutOffset,    // SetScanoutOffset = 0x100F
	16: SyscallTransferPages,      // TransferPages = 0x1010
	17: SyscallMapSharedPage,      // MapSharedPage = 0x1011
	// slot 18 freed (was SyscallLoadMaz = 0x1012 — retired with mazdl/Phase 5)
	19: SyscallUringConnect,  // UringConnect = 0x1013
	20: SyscallUringSend,     // UringSend = 0x1014
	21: SyscallUringRecv,     // UringRecv = 0x1015
	22: SyscallUringRelease,  // UringRelease = 0x1016
	23: SyscallRegisterSyscallHandler,    // RegisterSyscallHandler = 0x1017
	// slot 24 freed (was SyscallDelegatedRecv = 0x1018, handlers now use SysUringRecv)
	25: SyscallReply,                      // SyscallReply = 0x1019
	26: SyscallUartWrite,                 // UartWrite = 0x101A
	// slot 27 freed (was SyscallUartWriteDirect)
	28: SyscallShepherdInfo,                // ShepherdInfo = 0x101C
	29: SyscallSetReady,                  // SetReady = 0x101D
	// slot 30 freed (was SyscallLoadFile — retired 2026-05-09, all file I/O now via fsclient IPC)
	// slot 31 freed (was SyscallRunMaz — retired with mazdl/Phase 5)
	32: SyscallRunShepherd,                 // RunShepherd = 0x1020
	33: SyscallAttrCreate,                // AttrCreate = 0x1021
	34: SyscallAttrWrite,                 // AttrWrite = 0x1022
	35: SyscallAttrWriteURI,              // AttrWriteURI = 0x1023
	36: SyscallAttrAddDep,                // AttrAddDep = 0x1024
	37: SyscallAttrUpdateDeps,            // AttrUpdateDeps = 0x1025
	38: SyscallAttrRegisterQuery,         // AttrRegisterQuery = 0x1026
	39: SyscallAttrWriteResult,           // AttrWriteResult = 0x1027
	40: SyscallAttrWriteString,           // AttrWriteString = 0x1028
	41: SyscallAttrSetEager,              // AttrSetEager = 0x1029
	42: SyscallAttrWaitDirty,             // AttrWaitDirty = 0x102A
	43: SyscallAttrIncrementI64,          // AttrIncrementI64 = 0x102B
	44: SyscallRequestWindowManager,     // RequestWindowManager = 0x102C
	45: SyscallAttrWriteCollI64,         // AttrWriteCollI64 = 0x102D
	// slot 46 freed (was WaitInputEvent — rachel uses completion ring now)
	47: SyscallSharePages,               // SharePages = 0x102F
	// slots 48-49 freed (were MailboxSend/MailboxRecv — all IPC uses uring now)
	50: SyscallRegisterCursor,          // RegisterCursor = 0x1032
	51: SyscallSetCursor,               // SetCursor = 0x1033
	52: SyscallGetReady,               // GetReady = 0x1034
	// slots 53-54 freed (were RegisterDMAPool/UnregisterDMAPool)
	55: SyscallBlockSubmit,            // BlockSubmit = 0x1037
	// slot 56 freed (was SyscallReadFilePages — retired 2026-05-09, all file I/O now via fsclient IPC)
	57: SyscallRegisterCompletionRing, // RegisterCompletionRing = 0x1039
	58: SyscallIOUringSetup,           // IOUringSetup = 0x103A
	59: SyscallIOUringEnter,           // IOUringEnter = 0x103B
	60: SyscallSharePagesWithTarget,  // SharePagesWithTarget = 0x103C
	61: SyscallAttrSwap,              // AttrSwap = 0x103D
	62: SyscallAttrDelete,            // AttrDelete = 0x103E
	63: SyscallDeathAck,              // DeathAck = 0x103F
	64: SyscallGetOwnExports,        // GetOwnExports = 0x1040
	65: SyscallReleaseDelegatePage, // ReleaseDelegatePage = 0x1041 (pipe-buffered Write cleanup)
	66: SyscallRegisterStdioWriteRing, // RegisterStdioWriteRing = 0x1042 (split stdio onto its own ring)
	67: SyscallNetReadRxLatencyUs,    // NetReadRxLatencyUs = 0x1043 (MAZ-28 step 2)
	68: SyscallTransferDMAClump,     // TransferDMAClump = 0x1044 (MAZ-29 client→net.elf page handoff)
	69: SyscallShareNetPageWithClient, // ShareNetPageWithClient = 0x1045 (MAZ-29 net.elf→client per-stream send ring)
	70: SyscallUnshareFromTarget,    // UnshareFromTarget = 0x1046 (MAZ-53 Mode 2 Release: revoke a previously-shared mapping)
	71: SyscallCloneExec,           // CloneExec = 0x1047 (MAZ-120: combined clone+execve from a staged ELF + buffered intent)
	72: SyscallReapVforkTransient,  // ReapVforkTransient = 0x1048 (MAZ-63: reap transient after successful vfork execve)
	73: SyscallGetVforkReservedPID, // GetVforkReservedPID = 0x1049 (MAZ-63: query reserved child PID for a vfork transient TID)
}

// EpochStatusDumpFn, if non-nil, is invoked when userspace sends a
// DebugPrint with marker == DebugMarkerStatusDump (0xDB7). Wired by
// kmazarin's main init to RequestEpochStatusDump so a .maz program
// can ask "what is the kernel doing right now?" on demand.
var EpochStatusDumpFn func()

// DebugMarkerStatusDump is the SyscallDebugPrint marker that triggers
// an immediate [status] dump. Userspace sends it via
// mazarin/sys.DumpKernelStatus.
const DebugMarkerStatusDump = 0xDB7

// DebugMarkerSetMapFailNext arms kmem.MapPageInProcessFailNext from
// userspace — used by xfertest to exercise the TransferDMAClump rollback
// path. v1 != 0 arms the one-shot, v1 == 0 disarms. Userspace wrapper:
// mazarin/sys.SetMapFailInjection.
const DebugMarkerSetMapFailNext = 0xDB8

// DebugMarkerSetMapFailAfter arms kmem.MapPageInProcessFailAfter from
// userspace — sibling to DebugMarkerSetMapFailNext, but supports
// firing on the Nth forward call rather than the very next one. v1 is
// re-cast to int32 (sign-preserving) and stored verbatim; see
// kmem.MapPageInProcessFailAfter for the arming convention. Used by
// xfertest to exercise SyscallTransferPages mid-clump rollback (MAZ-37).
// Userspace wrapper: mazarin/sys.SetMapFailAfter.
const DebugMarkerSetMapFailAfter = 0xDB9

// DebugMarkerGetPTEFlags reads the caller's L3 PTE flags for a userspace VA
// and returns ELF permission bits (ELF_PF_R/W/X) via the syscall's int64
// return value. v1 = VA (page-aligned or anywhere within the page). Returns
// 0 with no error if the page is unmapped; for a valid mapping returns the
// flags with ELF_PF_R always set. Test-only — used by xfertest to verify
// the TransferPages / TransferDMAClump rollback preserves caller PTE flags
// (MAZ-39). Userspace wrapper: mazarin/sys.GetPTEFlags.
const DebugMarkerGetPTEFlags = 0xDBA

// SyscallDebugPrint prints debug arguments from userspace.
// arg0 = marker/id, arg1-arg5 = values to print
// Special case: if v1-v5 are all 0 and marker < 256, print marker as a character
//
//go:nosplit
func SyscallDebugPrint(marker, v1, v2, v3, v4, v5 uint64) int64 {
	// Special case: single character output — always go to serial for debugging.
	if v1 == 0 && v2 == 0 && v3 == 0 && v4 == 0 && v5 == 0 && marker < 256 {
		serial.PollWrite(byte(marker))
		return 0
	}
	if marker == DebugMarkerStatusDump && EpochStatusDumpFn != nil {
		EpochStatusDumpFn()
		return 0
	}
	if marker == DebugMarkerSetMapFailNext {
		kmem.MapPageInProcessFailNext.Store(v1 != 0)
		return 0
	}
	if marker == DebugMarkerSetMapFailAfter {
		// Round-trip uint64 -> uint32 -> int32 to preserve the bit
		// pattern of the int32 the userspace wrapper packed in. The
		// counter convention treats values <= 0 as "disarmed", so
		// negative arguments are explicit disarms — same as 0.
		kmem.MapPageInProcessFailAfter.Store(int32(uint32(v1)))
		return 0
	}
	if marker == DebugMarkerGetPTEFlags {
		// v1 = userspace VA. Walk the active page table (the caller's,
		// since we're in a syscall) and return its ELF permission flags.
		// l0PA=0 makes GetUserPTEFlags fall back to readCurrentL0PA().
		flags, _ := kmem.GetUserPTEFlags(uintptr(v1), 0)
		return int64(flags)
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
