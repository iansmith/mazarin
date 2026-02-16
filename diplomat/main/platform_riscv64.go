//go:build riscv64

// diplomat/main/platform_riscv64.go - RISC-V 64-bit/UEFI default platform instances

package main

import (
	"mazzy/shared/blockdev"
	"mazzy/shared/fs/fat32"
)

var defaultPlatform = PlatformOps{
	PrintChar:            printCharRISCV,
	DebugPortOut:         debugPortOut,
	AllocatePages:        UEFIAllocatePages,
	FreePages:            UEFIFreePages,
	AllocatePagesForMmap: AllocatePagesForMmap,
	ZeroMemory:           zeroMemory,
	ReadCR3:              readCR3Wrapper,  // RISC-V uses SATP, not CR3
	WriteCR3:             writeCR3Wrapper, // RISC-V uses SATP, not CR3
	DisableWriteProtect:  disableWriteProtect,
	EnableWriteProtect:   enableWriteProtect,
	ExitBootServices:     UEFIExitBootServices,
	HandleProtocol:       defaultHandleProtocol,
	BlockIORead:          uefiBlockIOReadWrapper,
	BlockIOWrite:         uefiBlockIOWriteWrapper,
}

var defaultBootSequence = BootSequence{
	InitSpans:       InitSpansRISCV,       // RISC-V: parse FDT + init spans
	GetBlockDevice:  GetBootDeviceRISCV,    // RISC-V uses VirtIO, not UEFI
	MountFilesystem: fat32Mount,
	LoadKernel:      LoadKernelRISCVWrapper, // RISC-V: no-error version for early boot
	MapKernel:       addKernelMappingToCurrentPT,
	JumpToKernel:    func(entry uint64) { jumpToEntry(entry) },

	// New boot phases
	ReadConfig:          ReadConfigDefaults,    // allocation-free: no Go heap in diplomat
	QueryHardware:       QueryHardwareRISCV,    // RISC-V: hardcoded for QEMU virt
	PrepareKernelVM:     PrepareKernelVMRISCV,  // RISC-V: bump allocator version
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

// readCR3Wrapper adapts RISC-V's readSATP to the PlatformOps interface.
// On RISC-V, SATP (Supervisor Address Translation and Protection) is the
// equivalent of x86's CR3 or ARM64's TTBR0.
func readCR3Wrapper() uint64 {
	return readSATP()
}

// writeCR3Wrapper adapts RISC-V's writeSATP to the PlatformOps interface.
// On RISC-V, SATP is the equivalent of x86's CR3 or ARM64's TTBR0.
func writeCR3Wrapper(val uint64) {
	writeSATP(val)
}

// defaultHandleProtocol wraps uefiHandleProtocol to match the PlatformOps signature.
// On RISC-V, the assembly reads the function pointer from the global systemTable.
func defaultHandleProtocol(handle, protocol, iface uintptr) EFI_STATUS {
	if systemTable == nil || systemTable.BootServices == nil {
		return EFI_NOT_READY
	}
	return uefiHandleProtocol(handle, protocol, iface)
}

// uefiBlockIOReadWrapper adapts RISC-V's uefiBlockIORead to the PlatformOps signature.
// The RISC-V assembly reads the function pointer from the protocol structure,
// so the funcPtr parameter is ignored.
func uefiBlockIOReadWrapper(protocol uintptr, mediaId uint32, lba, bufferSize uint64, buffer, funcPtr uintptr) EFI_STATUS {
	_ = funcPtr // RISC-V reads funcPtr from protocol structure
	return uefiBlockIORead(protocol, mediaId, lba, bufferSize, buffer)
}

// uefiBlockIOWriteWrapper adapts RISC-V's uefiBlockIOWrite to the PlatformOps signature.
// The RISC-V assembly reads the function pointer from the protocol structure,
// so the funcPtr parameter is ignored.
func uefiBlockIOWriteWrapper(protocol uintptr, mediaId uint32, lba, bufferSize uint64, buffer, funcPtr uintptr) EFI_STATUS {
	_ = funcPtr // RISC-V reads funcPtr from protocol structure
	return uefiBlockIOWrite(protocol, mediaId, lba, bufferSize, buffer)
}

// jumpToKmazarinWithStack sets up stacks, installs kmazarin's STVEC, and jumps.
// Implemented in pagetable_riscv64.s
func jumpToKmazarinWithStack(entry, g0StackPtr, excStackTop, stvec uint64)

// RISC-V UEFI call helpers - implemented in uefi_calls_riscv64.s

//go:noescape
func uefiHandleProtocol(handle, protocol, iface uintptr) EFI_STATUS

//go:noescape
func uefiBlockIORead(protocol uintptr, mediaId uint32, lba, bufferSize uint64, buffer uintptr) EFI_STATUS

//go:noescape
func uefiBlockIOWrite(protocol uintptr, mediaId uint32, lba, bufferSize uint64, buffer uintptr) EFI_STATUS

//go:noescape
func uefiLocateProtocol(protocol, registration, iface uintptr) EFI_STATUS

//go:noescape
func uefiMPGetNumberOfProcessors(protocol, numProcs, numEnabled uintptr) EFI_STATUS

// RISC-V page table operations - implemented in pagetable_riscv64.s

//go:noescape
func readSATP() uint64

//go:noescape
func writeSATP(val uint64)

//go:noescape
func flushTLB()

//go:noescape
func disableWriteProtect()

//go:noescape
func enableWriteProtect()

// RISC-V TLS operations - implemented in tls_riscv64.s

//go:noescape
func readTP() uint64

//go:noescape
func writeTP(val uint64)

// printCharRISCV prints a character using either UEFI (if available) or direct UART.
// RISC-V can run in two modes:
//   - UEFI mode: systemTable is valid, use printChar (UEFI ConOut)
//   - Direct mode (-bios none): systemTable is nil, use printCharDirect (UART)
//
//go:nosplit
func printCharRISCV(c uint16) {
	if systemTable != nil && systemTable.ConOut != nil {
		// UEFI mode: use UEFI ConOut
		printChar(c)
	} else {
		// Direct mode: use direct UART
		printCharDirect(c)
	}
}

// ReadBlockVirtIO reads a block from VirtIO MMIO device (RISC-V specific)
//
//go:nosplit
func ReadBlockVirtIO(lba uint64, buf []byte) error {
	// TODO: Implement actual VirtIO virtqueue I/O
	// For now, return a stub error to see if we get called
	printString("DEBUG: ReadBlockVirtIO called for LBA ")
	printHex(lba)
	printString("\r\n")

	// Stub: fill with zeros for now
	for i := range buf {
		buf[i] = 0
	}

	return nil
}

// ReadBlockVirtIONoError reads a block without returning an error interface.
// Used for early boot on RISC-V to avoid allocation issues.
// Panics/loops on error instead of returning.
func ReadBlockVirtIONoError(lba uint64, buf []byte) {
	readBlockVirtIO(lba, buf)
}

func init() {
	// Initialize RISC-V-specific platform operations
	plat.ReadBlockVirtIO = ReadBlockVirtIO
	plat.ReadBlockVirtIONoError = ReadBlockVirtIONoError
}

// LoadKernelRISCVWrapper wraps LoadKernelNoError to match the BootSequence signature.
// Used for RISC-V early boot to avoid error interface allocation.
func LoadKernelRISCVWrapper(fs *fat32.FileSystem, path string) (*LoadedKernel, error) {
	// Call no-error version (will panic on failure instead of returning error)
	kernel := LoadKernelNoError(fs, path)
	return kernel, nil
}

// getExceptionHandlerForJump returns the STVEC address for the jump to kmazarin.
// On RISC-V, we use main.trapEntry (not ExceptionVectorTable which is ARM64/AMD64).
func getExceptionHandlerForJump(kernel *LoadedKernel, relocDelta uint64) uint64 {
	addr := findKernelSymbol(kernel, "main.trapEntry")
	if addr == 0 {
		// Fall back to ExceptionVectorTable if trapEntry not found
		addr = findKernelSymbol(kernel, "main.ExceptionVectorTable")
	}
	if addr == 0 {
		printString("ERROR: neither main.trapEntry nor main.ExceptionVectorTable found\r\n")
		for {}
	}
	addr += relocDelta
	printString("STVEC: kmazarin trapEntry = ")
	printHex(addr)
	printString("\r\n")
	return addr
}

// mountFAT32OrDie uses the allocation-free FAT32 mount path on RISC-V.
// This avoids error interface returns that would trigger heap allocation
// before the runtime is fully initialized.
func mountFAT32OrDie(dev blockdev.BlockDevice) *fat32.FileSystem {
	return fat32MountOrDie(dev)
}

