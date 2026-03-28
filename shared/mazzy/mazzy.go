// Package mazzy defines Mazzy-specific syscall numbers.
// Both the kernel (kmazarin) and userspace (mazarin) import this package
// so there is a single source of truth for syscall numbering.
package mazzy

// MazzySyscallBase is the starting number for Mazzy-specific syscalls.
// Uses numbers starting at 0x1000 to avoid Linux's 0-512 range.
const MazzySyscallBase = 0x1000

const (
	SysGetTime         = MazzySyscallBase + 0  // 0x1000 - Get current time
	SysLaunch          = MazzySyscallBase + 1  // 0x1001 - Launch a shepherd from ELF file
	SysBootstrapRunElf = MazzySyscallBase + 2  // 0x1002 - Bootstrap: load ELF from disk
	SysAllocPages      = MazzySyscallBase + 3  // 0x1003 - Allocate pages for userspace
	SysExit            = MazzySyscallBase + 4  // 0x1004 - Exit program
	SysReap            = MazzySyscallBase + 5  // 0x1005 - Reap terminated program
	SysDebugPrint      = MazzySyscallBase + 6  // 0x1006 - Debug print arguments
	SysGetFramebuffer  = MazzySyscallBase + 7  // 0x1007 - Get framebuffer info
	SysWaitKernelAsync = MazzySyscallBase + 8  // 0x1008 - Wait for kernel async message
	// slot 9 freed (was RegisterAsyncPreempt)
	SysWaitSoftIRQ       = MazzySyscallBase + 10 // 0x100A - Wait for soft IRQ events on a slot
	SysRegisterSoftIRQ   = MazzySyscallBase + 11 // 0x100B - Register an IRQ on a soft IRQ slot
	SysQueryInputDevices = MazzySyscallBase + 12 // 0x100C - Query available input devices
	SysFlushFramebuffer  = MazzySyscallBase + 13 // 0x100D - Flush framebuffer region to display
	SysSetTimerDeadline  = MazzySyscallBase + 14 // 0x100E - Set timer deadline on soft IRQ slot
	SysSetScanoutOffset  = MazzySyscallBase + 15 // 0x100F - Set scanout Y offset for hardware scrolling
	SysTransferPages     = MazzySyscallBase + 16 // 0x1010 - Transfer pages between shepherds
	SysMapSharedPage     = MazzySyscallBase + 17 // 0x1011 - Map shared page from another shepherd
	SysLoadMaz           = MazzySyscallBase + 18 // 0x1012 - Load .maz PIE ELF into shepherd's address space
	SysIPCCall           = MazzySyscallBase + 19 // 0x1013 - Send IPC request (blocks for reply)
	SysIPCRecv           = MazzySyscallBase + 20 // 0x1014 - Receive IPC request (blocks)
	SysIPCReply          = MazzySyscallBase + 21 // 0x1015 - Reply to IPC request
	SysBlockRead         = MazzySyscallBase + 22 // 0x1016 - Read disk sectors (deprecated)
	SysRegisterSyscallHandler = MazzySyscallBase + 23 // 0x1017 - Register shepherd as handler for a SysID
	SysDelegatedRecv          = MazzySyscallBase + 24 // 0x1018 - Receive a delegated syscall request
	SysSyscallReply           = MazzySyscallBase + 25 // 0x1019 - Reply to a delegated syscall
	SysUartWrite              = MazzySyscallBase + 26 // 0x101A - Write to UART (non-blocking)
	SysUartWriteDirect        = MazzySyscallBase + 27 // 0x101B - Write to UART via PollWrite (synchronous)
	SysShepherdInfo           = MazzySyscallBase + 28 // 0x101C - Get info about running shepherds
	SysSetReady               = MazzySyscallBase + 29 // 0x101D - Signal shepherd is ready
	SysLoadFile               = MazzySyscallBase + 30 // 0x101E - Load file via fs delegate
	SysRunMaz                 = MazzySyscallBase + 31 // 0x101F - Load .maz ELF from caller's pages
	SysRunShepherd            = MazzySyscallBase + 32 // 0x1020 - Create new shepherd from caller's pages
	SysAttrCreate             = MazzySyscallBase + 33 // 0x1021 - Create attribute with URI
	SysAttrWrite              = MazzySyscallBase + 34 // 0x1022 - Write value by slot index
	SysAttrWriteURI           = MazzySyscallBase + 35 // 0x1023 - Write value by URI string
	SysAttrAddDep             = MazzySyscallBase + 36 // 0x1024 - Add single dependency edge
	SysAttrUpdateDeps         = MazzySyscallBase + 37 // 0x1025 - Replace full dependency set
	SysAttrRegisterQuery      = MazzySyscallBase + 38 // 0x1026 - Register find pattern, get query result slot
	SysAttrWriteResult        = MazzySyscallBase + 39 // 0x1027 - Write constraint evaluation result
	SysAttrWriteString        = MazzySyscallBase + 40 // 0x1028 - Write string value
	SysAttrSetEager           = MazzySyscallBase + 41 // 0x1029 - Set/clear eager notification
	SysAttrWaitDirty          = MazzySyscallBase + 42 // 0x102A - Wait for dirty notifications
	SysAttrIncrementI64       = MazzySyscallBase + 43 // 0x102B - Atomically increment int64 attribute
	SysRequestWindowManager   = MazzySyscallBase + 44 // 0x102C - Claim window manager role
	SysSetInputFocus          = MazzySyscallBase + 45 // 0x102D - Set input focus for device class
	SysWaitInputEvent         = MazzySyscallBase + 46 // 0x102E - Wait for input events
	SysMailboxMapPage         = MazzySyscallBase + 47 // 0x102F - Map caller's page into target shepherd's space
	SysMailboxSend            = MazzySyscallBase + 48 // 0x1030 - Send mailbox notification
	SysMailboxRecv            = MazzySyscallBase + 49 // 0x1031 - Wait for mailbox notification
	SysRegisterCursor         = MazzySyscallBase + 50 // 0x1032 - Register cursor image
	SysSetCursor              = MazzySyscallBase + 51 // 0x1033 - Switch active cursor by ID
	SysGetReady               = MazzySyscallBase + 52 // 0x1034 - Check if named shepherd is ready
	// slots 53-54 freed (were RegisterDMAPool/UnregisterDMAPool)
	SysBlockSubmit     = MazzySyscallBase + 55 // 0x1037 - Async block I/O submit (returns IOTag)
	SysReadFilePages   = MazzySyscallBase + 56 // 0x1038 - Read file data into caller's DMA pages
)
