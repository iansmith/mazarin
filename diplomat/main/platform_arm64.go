//go:build arm64

// diplomat/main/platform_arm64.go - ARM64/UEFI default platform instances

package main

var defaultPlatform = PlatformOps{
	PrintChar:            printChar,
	DebugPortOut:         debugPortOut,
	AllocatePages:        UEFIAllocatePages,
	FreePages:            UEFIFreePages,
	AllocatePagesForMmap: AllocatePagesForMmap,
	ZeroMemory:           zeroMemory,
	ReadCR3:              readCR3Wrapper,  // ARM64 uses TTBR0, not CR3
	WriteCR3:             writeCR3Wrapper, // ARM64 uses TTBR0, not CR3
	DisableWriteProtect:  disableWriteProtect,
	EnableWriteProtect:   enableWriteProtect,
	ExitBootServices:     UEFIExitBootServices,
	HandleProtocol:       defaultHandleProtocol,
	BlockIORead:          uefiBlockIOReadWrapper,
	BlockIOWrite:         uefiBlockIOWriteWrapper,
}

var defaultBootSequence = BootSequence{
	InitSpans:       InitializeSpans,
	GetBlockDevice:  GetBootDeviceBlockIO,
	MountFilesystem: fat32Mount,
	LoadKernel:      LoadKernel,
	MapKernel:       addKernelMappingToCurrentPT,
	JumpToKernel:    func(entry uint64) { jumpToEntry(entry) },
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

// readCR3Wrapper adapts ARM64's readTTBR0 to the PlatformOps interface.
// On ARM64, TTBR0_EL1 is the equivalent of x86's CR3.
func readCR3Wrapper() uint64 {
	return readTTBR0()
}

// writeCR3Wrapper adapts ARM64's writeTTBR0 to the PlatformOps interface.
// On ARM64, TTBR0_EL1 is the equivalent of x86's CR3.
func writeCR3Wrapper(val uint64) {
	writeTTBR0(val)
}

// defaultHandleProtocol wraps uefiHandleProtocol to match the PlatformOps signature.
// On ARM64, the assembly reads the function pointer from the global systemTable.
func defaultHandleProtocol(handle, protocol, iface uintptr) EFI_STATUS {
	if systemTable == nil || systemTable.BootServices == nil {
		return EFI_NOT_READY
	}
	return uefiHandleProtocol(handle, protocol, iface)
}

// uefiBlockIOReadWrapper adapts ARM64's uefiBlockIORead to the PlatformOps signature.
// The ARM64 assembly reads the function pointer from the protocol structure,
// so the funcPtr parameter is ignored.
func uefiBlockIOReadWrapper(protocol uintptr, mediaId uint32, lba, bufferSize uint64, buffer, funcPtr uintptr) EFI_STATUS {
	_ = funcPtr // ARM64 reads funcPtr from protocol structure
	return uefiBlockIORead(protocol, mediaId, lba, bufferSize, buffer)
}

// uefiBlockIOWriteWrapper adapts ARM64's uefiBlockIOWrite to the PlatformOps signature.
// The ARM64 assembly reads the function pointer from the protocol structure,
// so the funcPtr parameter is ignored.
func uefiBlockIOWriteWrapper(protocol uintptr, mediaId uint32, lba, bufferSize uint64, buffer, funcPtr uintptr) EFI_STATUS {
	_ = funcPtr // ARM64 reads funcPtr from protocol structure
	return uefiBlockIOWrite(protocol, mediaId, lba, bufferSize, buffer)
}

// ARM64 UEFI call helpers - implemented in uefi_calls_arm64.s

//go:noescape
func uefiHandleProtocol(handle, protocol, iface uintptr) EFI_STATUS

//go:noescape
func uefiBlockIORead(protocol uintptr, mediaId uint32, lba, bufferSize uint64, buffer uintptr) EFI_STATUS

//go:noescape
func uefiBlockIOWrite(protocol uintptr, mediaId uint32, lba, bufferSize uint64, buffer uintptr) EFI_STATUS
