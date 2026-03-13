// diplomat/main/platform.go - Function pointer tables for platform abstraction
//
// Two-level table design:
//   PlatformOps  — low-level hardware/UEFI primitives
//   BootSequence — high-level boot flow steps
//   SyscallTable — syscall handler dispatch
//
// DiplomatEntry walks BootSequence methods linearly.
// A different platform provides a different PlatformOps/BootSequence.

package main

import (
	"mazzy/shared/blockdev"
	"mazzy/shared/bootloader"
	"mazzy/shared/fs/fat32"
)

// Re-export shared types for use throughout diplomat
type LoadedKernel = bootloader.LoadedKernel

// PlatformOps contains all hardware/UEFI primitive operations.
// A different platform (e.g., ARM64, non-UEFI) would provide a different instance.
type PlatformOps struct {
	// Console
	PrintChar    func(c uint16)
	DebugPortOut func(c byte)

	// Memory — UEFI page allocation
	AllocatePages       func(allocType, memoryType uint32, pages uint64, memory *uint64) EFI_STATUS
	FreePages           func(memory uint64, pages uint64) EFI_STATUS
	ZeroMemory func(addr, size uint64)

	// CPU — page table manipulation
	ReadCR3             func() uint64
	WriteCR3            func(val uint64)
	DisableWriteProtect func()
	EnableWriteProtect  func()

	// Boot services lifecycle
	ExitBootServices func() EFI_STATUS

	// Protocol discovery
	HandleProtocol func(handle, protocol, iface uintptr) EFI_STATUS

	// Block I/O
	BlockIORead  func(protocol uintptr, mediaId uint32, lba, bufferSize uint64, buffer, funcPtr uintptr) EFI_STATUS
	BlockIOWrite func(protocol uintptr, mediaId uint32, lba, bufferSize uint64, buffer, funcPtr uintptr) EFI_STATUS

	// VirtIO MMIO block I/O (RISC-V bare-metal only)
	ReadBlockVirtIO       func(lba uint64, buf []byte) error
	ReadBlockVirtIONoError func(lba uint64, buf []byte) // No error return - for early boot
}

// BootSequence contains the high-level boot steps.
// Each function performs one logical phase of the boot process.
type BootSequence struct {
	GetBlockDevice func() (*UEFIBlockDevice, error)
	MountFilesystem func(dev blockdev.BlockDevice) (*fat32.FileSystem, error)
	LoadKernel      func(fs *fat32.FileSystem, path string) (*LoadedKernel, error)
	MapKernel       func(virtBase, physBase, size uint64) error
	JumpToKernel    func(entry uint64)

	// New boot phases for full kernel environment setup
	ReadConfig         func(fs *fat32.FileSystem) (*KmazarinConfig, error)
	QueryHardware      func(config *KmazarinConfig) (*HardwareInfo, error)
	PrepareKernelVM    func(hw *HardwareInfo, kernel *LoadedKernel) (*KernelVM, error)
	InstallFaultHandler func(vm *KernelVM) error
	BuildStartupEnv    func(vm *KernelVM, hw *HardwareInfo, kernel *LoadedKernel, cfg *KmazarinConfig) (uint64, error)
	JumpToKernelWithEnv func(entry, stackPtr, excStackTop, vbar uint64)
}

