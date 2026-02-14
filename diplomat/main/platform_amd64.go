//go:build amd64

// diplomat/main/platform_amd64.go - x86_64/UEFI default platform instances

package main

// uefiHandleProtocol is implemented in uefi_calls_amd64.s
// Calls EFI_BOOT_SERVICES.HandleProtocol using MS x64 ABI
//
//go:noescape
func uefiHandleProtocol(handle, protocol, iface, funcPtr uintptr) EFI_STATUS

var defaultPlatform = PlatformOps{
	PrintChar:            printChar,
	DebugPortOut:         debugPortOut,
	AllocatePages:        UEFIAllocatePages,
	FreePages:            UEFIFreePages,
	AllocatePagesForMmap: AllocatePagesForMmap,
	ZeroMemory:           zeroMemory,
	ReadCR3:              readCR3,
	WriteCR3:             writeCR3,
	DisableWriteProtect:  disableWriteProtect,
	EnableWriteProtect:   enableWriteProtect,
	ExitBootServices:     UEFIExitBootServices,
	HandleProtocol:       defaultHandleProtocol,
	BlockIORead:          uefiCallBlockIORead,
	BlockIOWrite:         uefiCallBlockIOWrite,
}

var defaultBootSequence = BootSequence{
	InitSpans:       InitializeSpans,
	GetBlockDevice:  GetBootDeviceBlockIO,
	MountFilesystem: fat32Mount,
	LoadKernel:      LoadKernel,
	MapKernel:       addKernelMappingToCurrentPT,
	JumpToKernel:    func(entry uint64) { jumpToEntry(entry) },

	// New boot phases (matching ARM64 flow)
	ReadConfig:          ReadConfig,
	QueryHardware:       QueryHardware,
	PrepareKernelVM:     PrepareKernelVM,
	InstallFaultHandler: InstallFaultHandler,
	BuildStartupEnv:     BuildStartupEnv,
	JumpToKernelWithEnv: jumpToKmazarinWithStack,
}

var defaultSyscalls = SyscallTable{
	Mmap:    DiplomatMmap,
	Munmap:  DiplomatMunmap,
	Madvise: DiplomatMadvise,
	Brk:     DiplomatBrk,
	Write:   DiplomatWrite,
	Read:    DiplomatRead,
	Open:    DiplomatOpen,
	Close:   DiplomatClose,
	Futex:   DiplomatFutex,
}

// jumpToKmazarinWithStack sets up RSP, optionally loads IDT, and jumps to kernel.
// Implemented in uefi_calls_amd64.s
func jumpToKmazarinWithStack(entry, g0StackPtr, excStackTop, idtBase uint64)

// defaultHandleProtocol wraps uefiHandleProtocol to match the PlatformOps signature.
// The raw assembly function takes an extra funcPtr parameter; this wrapper
// supplies it from the boot services table.
func defaultHandleProtocol(handle, protocol, iface uintptr) EFI_STATUS {
	if systemTable == nil || systemTable.BootServices == nil {
		return EFI_NOT_READY
	}
	return uefiHandleProtocol(handle, protocol, iface, systemTable.BootServices.HandleProtocol)
}

// getExceptionHandlerForJump returns the IDT base / exception vector address
// for the jump to kmazarin. On AMD64, we use main.ExceptionVectorTable.
func getExceptionHandlerForJump(kernel *LoadedKernel, relocDelta uint64) uint64 {
	addr := findKernelSymbol(kernel, "main.ExceptionVectorTable")
	if addr == 0 {
		printString("ERROR: ExceptionVectorTable symbol not found\r\n")
		for {}
	}
	addr += relocDelta
	printString("IDT: kmazarin ExceptionVectorTable = ")
	printHex(addr)
	printString("\r\n")
	return addr
}

// ReadBlockVirtIO stub - AMD64 uses UEFI BlockIO, not VirtIO MMIO
func readBlockVirtIOStub(lba uint64, buf []byte) error {
	panic("ReadBlockVirtIO called on AMD64 - should use UEFI BlockIO")
}

func init() {
	// Initialize AMD64-specific platform operations
	plat.ReadBlockVirtIO = readBlockVirtIOStub
}

// The following stubs satisfy references from non-build-tagged files.
// On AMD64, these code paths are never reached (UEFI boot uses different functions),
// but the compiler requires the symbols to exist.

func ReadBlockVirtIONoError(lba uint64, buf []byte) {
	panic("ReadBlockVirtIONoError called on AMD64")
}

func readBlockVirtIO(lba uint64, buf []byte) {
	panic("readBlockVirtIO called on AMD64")
}

func allocatePhysPagesNoError(pages uint64) uint64 {
	panic("allocatePhysPagesNoError called on AMD64")
}

func readBlockVirtIOBulk(lba uint64, buf []byte) {
	panic("readBlockVirtIOBulk called on AMD64")
}
