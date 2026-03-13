//go:build amd64

// diplomat/main/platform_amd64.go - x86_64/UEFI default platform instances

package main

import (
	"mazzy/shared/blockdev"
	"mazzy/shared/fs/fat32"
)

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



// saveTextChecksum is a no-op on AMD64 — only used by RISC-V for verifying
// code integrity through page table transitions.
func saveTextChecksum(physBase uint64, textFilesz uint64) {}

// The following stubs satisfy references from non-build-tagged files.
// On AMD64, these code paths are never reached (UEFI boot uses different functions),
// but the compiler requires the symbols to exist.

// ReadBlockVirtIONoError reads a block using the UEFI block device on AMD64.
// Called from shared code (fat32_walk.go, elf_loader.go) that was written for
// RISC-V's allocation-free boot. On AMD64, delegates to the UEFI block I/O.
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
	panic("readBlockVirtIO called on AMD64")
}

// allocatePhysPagesNoError allocates physical pages via UEFI AllocatePages on AMD64.
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

// readBlockVirtIOBulk reads multiple consecutive sectors via UEFI block I/O on AMD64.
func readBlockVirtIOBulk(lba uint64, buf []byte) {
	sectorSize := uint64(512)
	for offset := uint64(0); offset < uint64(len(buf)); offset += sectorSize {
		end := offset + sectorSize
		if end > uint64(len(buf)) {
			end = uint64(len(buf))
		}
		ReadBlockVirtIONoError(lba+offset/sectorSize, buf[offset:end])
	}
}

// mountFAT32OrDie uses the normal UEFI-based FAT32 mount path on AMD64.
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
