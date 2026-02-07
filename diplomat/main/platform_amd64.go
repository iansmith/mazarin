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
