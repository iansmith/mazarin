//go:build arm64

// diplomat/main/platform_arm64.go - ARM64/UEFI default platform instances

package main

import (
	"mazzy/shared/blockdev"
	"mazzy/shared/fs/fat32"
)

var defaultPlatform = PlatformOps{
	PrintChar:            printChar,
	DebugPortOut:         debugPortOut,
	AllocatePages:        UEFIAllocatePages,
	FreePages:            UEFIFreePages,
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
	GetBlockDevice:  GetBootDeviceBlockIO,
	MountFilesystem: fat32Mount,
	LoadKernel:      LoadKernel,
	MapKernel:       addKernelMappingToCurrentPT,
	JumpToKernel:    func(entry uint64) { jumpToEntry(entry) },

	// New boot phases
	ReadConfig:         ReadConfig,
	QueryHardware:      QueryHardware,
	PrepareKernelVM:    PrepareKernelVM,
	InstallFaultHandler: InstallFaultHandler,
	BuildStartupEnv:    BuildStartupEnv,
	JumpToKernelWithEnv: jumpToKmazarinWithStack,
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

// jumpToKmazarinWithStack sets up stacks, installs kmazarin's VBAR, and jumps.
// Implemented in pagetable_arm64.s
func jumpToKmazarinWithStack(entry, g0StackPtr, excStackTop, vbar uint64)

// ARM64 UEFI call helpers - implemented in uefi_calls_arm64.s

//go:noescape
func uefiHandleProtocol(handle, protocol, iface uintptr) EFI_STATUS

//go:noescape
func uefiBlockIORead(protocol uintptr, mediaId uint32, lba, bufferSize uint64, buffer uintptr) EFI_STATUS

//go:noescape
func uefiBlockIOWrite(protocol uintptr, mediaId uint32, lba, bufferSize uint64, buffer uintptr) EFI_STATUS

// getExceptionHandlerForJump returns the VBAR_EL1 address for the jump to kmazarin.
// On ARM64, we use main.ExceptionVectorTable.
func getExceptionHandlerForJump(kernel *LoadedKernel, relocDelta uint64) uint64 {
	addr := findKernelSymbol(kernel, "main.ExceptionVectorTable")
	if addr == 0 {
		printString("ERROR: ExceptionVectorTable symbol not found\r\n")
		for {}
	}
	addr += relocDelta
	printString("VBAR: kmazarin ExceptionVectorTable = ")
	printHex(addr)
	printString("\r\n")
	return addr
}

// ReadBlockVirtIO stub - ARM64 uses UEFI BlockIO, not VirtIO MMIO
func readBlockVirtIOStub(lba uint64, buf []byte) error {
	panic("ReadBlockVirtIO called on ARM64 - should use UEFI BlockIO")
}

func init() {
	// Initialize ARM64-specific platform operations
	plat.ReadBlockVirtIO = readBlockVirtIOStub
}

// saveTextChecksum is a no-op on ARM64 — only used by RISC-V for verifying
// code integrity through page table transitions.
func saveTextChecksum(physBase uint64, textFilesz uint64) {}

// The following stubs satisfy references from non-build-tagged files.
// On ARM64, these code paths are never reached (UEFI boot uses different functions),
// but the compiler requires the symbols to exist.

// ReadBlockVirtIONoError reads a block using the UEFI block device on ARM64.
// Called from shared code (fat32_walk.go, elf_loader.go) that was written for
// RISC-V's allocation-free boot. On ARM64, delegates to the UEFI block I/O.
func ReadBlockVirtIONoError(lba uint64, buf []byte) {
	err := globalBlockDev.ReadBlock(lba, buf)
	if err != nil {
		printString("FATAL: ReadBlock failed at LBA ")
		printHex(lba)
		printString("\r\n")
		for {
		}
	}
}

func readBlockVirtIO(lba uint64, buf []byte) {
	panic("readBlockVirtIO called on ARM64")
}

// allocatePhysPagesNoError allocates physical pages via UEFI AllocatePages on ARM64.
func allocatePhysPagesNoError(pages uint64) uint64 {
	var addr uint64
	// AllocateAnyPages=0, EfiLoaderData=2
	status := UEFIAllocatePages(0, 2, pages, &addr)
	if status != 0 {
		printString("FATAL: AllocatePages failed\r\n")
		for {
		}
	}
	return addr
}

// readBlockVirtIOBulk reads multiple consecutive sectors via UEFI block I/O on ARM64.
func readBlockVirtIOBulk(lba uint64, buf []byte) {
	// Read sector by sector through UEFI
	sectorSize := uint64(512)
	for offset := uint64(0); offset < uint64(len(buf)); offset += sectorSize {
		end := offset + sectorSize
		if end > uint64(len(buf)) {
			end = uint64(len(buf))
		}
		ReadBlockVirtIONoError(lba+offset/sectorSize, buf[offset:end])
	}
}

// mountFAT32OrDie uses the normal UEFI-based FAT32 mount path on ARM64.
// The Go runtime is initialized enough for error interfaces and allocations.
func mountFAT32OrDie(dev blockdev.BlockDevice) *fat32.FileSystem {
	fs, err := fat32Mount(dev)
	if err != nil {
		printString("ERROR: FAT32 mount: ")
		printString(err.Error())
		printString("\r\n")
		for {
		}
	}
	return fs
}
