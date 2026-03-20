
package ksyscall

import (
	"mazzy/kmazarin/serial"
)

// MazzySyscallBase is the starting number for Mazzy-specific syscalls.
// Uses numbers starting at 0x1000 to avoid Linux's 0-512 range.
// Numbers are assigned alphabetically by name.
const MazzySyscallBase = 0x1000

// Mazzy syscall numbers (numerically ordered)
const (
	SysGetTime             = MazzySyscallBase + 0 // 0x1000 - Get current time
	SysLaunch              = MazzySyscallBase + 1 // 0x1001 - Launch a shepherd from ELF file
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
	SysTransferPages        = MazzySyscallBase + 16 // 0x1010 - Transfer pages between shepherds
	SysMapSharedPage        = MazzySyscallBase + 17 // 0x1011 - Map shared page from another shepherd
	SysLoadMaz              = MazzySyscallBase + 18 // 0x1012 - Load .maz PIE ELF into shepherd's address space
	SysBlockRead            = MazzySyscallBase + 22 // 0x1016 - Read disk sectors (block device owner only)
	SysRegisterSyscallHandler = MazzySyscallBase + 23 // 0x1017 - Register shepherd as handler for a SysID
	SysDelegatedRecv        = MazzySyscallBase + 24 // 0x1018 - Receive a delegated syscall request (blocks)
	SysDelegatedReply       = MazzySyscallBase + 25 // 0x1019 - Reply to a delegated syscall
	SysUartWrite            = MazzySyscallBase + 26 // 0x101A - Write bytes to UART txBuf (non-blocking, drops on overflow)
	SysUartWriteDirect      = MazzySyscallBase + 27 // 0x101B - Write bytes to UART via PollWrite (synchronous, guaranteed delivery)
	SysShepherdInfo           = MazzySyscallBase + 28 // 0x101C - Get info about running shepherds
	SysSetReady             = MazzySyscallBase + 29 // 0x101D - Signal shepherd is ready for delegated work
	SysLoadFile             = MazzySyscallBase + 30 // 0x101E - Load file via fs.maz delegate
	SysRunMaz               = MazzySyscallBase + 31 // 0x101F - Load .maz ELF from caller's pages
	SysRunShepherd            = MazzySyscallBase + 32 // 0x1020 - Create new shepherd from caller's pages
	SysAttrCreate           = MazzySyscallBase + 33 // 0x1021 - Create attribute with URI
	SysAttrWrite            = MazzySyscallBase + 34 // 0x1022 - Write value by slot index
	SysAttrWriteURI         = MazzySyscallBase + 35 // 0x1023 - Write value by URI string
	SysAttrAddDep           = MazzySyscallBase + 36 // 0x1024 - Add single dependency edge
	SysAttrUpdateDeps       = MazzySyscallBase + 37 // 0x1025 - Replace full dependency set
	SysAttrRegisterQuery    = MazzySyscallBase + 38 // 0x1026 - Register find pattern, get query result slot
	SysAttrWriteResult      = MazzySyscallBase + 39 // 0x1027 - Write constraint evaluation result (scalar/composite)
	SysAttrWriteString      = MazzySyscallBase + 40 // 0x1028 - Write string value (value or constraint result)
	SysAttrSetEager         = MazzySyscallBase + 41 // 0x1029 - Set/clear eager notification on attribute
	SysAttrWaitDirty        = MazzySyscallBase + 42 // 0x102A - Wait for dirty notifications
	SysAttrIncrementI64     = MazzySyscallBase + 43 // 0x102B - Atomically increment int64 attribute
	SysRequestWindowManager = MazzySyscallBase + 44 // 0x102C - Claim window manager role
	SysSetInputFocus        = MazzySyscallBase + 45 // 0x102D - Set input focus for device class
	SysWaitInputEvent       = MazzySyscallBase + 46 // 0x102E - Wait for input events on device class queue
	SysMailboxMapPage       = MazzySyscallBase + 47 // 0x102F - Map caller's page into target shepherd's space
	SysMailboxSend          = MazzySyscallBase + 48 // 0x1030 - Send mailbox notification to target shepherd
	SysMailboxRecv          = MazzySyscallBase + 49 // 0x1031 - Wait for mailbox notification
	SysRegisterCursor       = MazzySyscallBase + 50 // 0x1032 - Register cursor image, get cursor ID
	SysSetCursor            = MazzySyscallBase + 51 // 0x1033 - Switch active cursor by ID
	SysGetReady             = MazzySyscallBase + 52 // 0x1034 - Check if named shepherd is ready
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
	22: SyscallBlockRead,                // BlockRead = 0x1016
	23: SyscallRegisterSyscallHandler,    // RegisterSyscallHandler = 0x1017
	24: SyscallDelegatedRecv,             // DelegatedRecv = 0x1018
	25: SyscallDelegatedReply,            // DelegatedReply = 0x1019
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
