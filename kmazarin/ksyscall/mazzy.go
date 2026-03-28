
package ksyscall

import (
	"mazzy/kmazarin/serial"
)

// mazzySyscallTable holds Mazzy-specific syscall handlers.
// Indexed by (syscallNum - mazzy.MazzySyscallBase).
var mazzySyscallTable = [64]SyscallHandler{
	0: SyscallGetTime,              // GetTime = 0x1000
	1: SyscallLaunch,               // Launch = 0x1001
	2: SyscallBootstrapRunElf,      // BootstrapRunElf = 0x1002
	3: SyscallAllocPages,           // AllocPages = 0x1003
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
	22: SyscallBlockRead,                // BlockRead = 0x1016
	23: SyscallRegisterSyscallHandler,    // RegisterSyscallHandler = 0x1017
	24: SyscallDelegatedRecv,             // DelegatedRecv = 0x1018
	25: SyscallReply,                      // SyscallReply = 0x1019
	26: SyscallUartWrite,                 // UartWrite = 0x101A
	27: SyscallUartWriteDirect,           // UartWriteDirect = 0x101B
	28: SyscallShepherdInfo,                // ShepherdInfo = 0x101C
	29: SyscallSetReady,                  // SetReady = 0x101D
	30: SyscallLoadFile,                  // LoadFile = 0x101E
	31: SyscallRunMaz,                    // RunMaz = 0x101F
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
	45: SyscallSetInputFocus,            // SetInputFocus = 0x102D
	46: SyscallWaitInputEvent,           // WaitInputEvent = 0x102E
	47: SyscallMailboxMapPage,           // MailboxMapPage = 0x102F
	48: SyscallMailboxSend,              // MailboxSend = 0x1030
	49: SyscallMailboxRecv,              // MailboxRecv = 0x1031
	50: SyscallRegisterCursor,          // RegisterCursor = 0x1032
	51: SyscallSetCursor,               // SetCursor = 0x1033
	52: SyscallGetReady,               // GetReady = 0x1034
	// slots 53-54 freed (were RegisterDMAPool/UnregisterDMAPool)
	55: SyscallBlockSubmit,     // BlockSubmit = 0x1037
	56: SyscallReadFilePages,   // ReadFilePages = 0x1038
}

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
	// Full debug print — disabled during investigation
	_ = marker
	_ = v1
	_ = v2
	_ = v3
	_ = v4
	_ = v5
	return 0
}
