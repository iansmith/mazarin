// Package bootloader defines shared types for bootloader function pointer tables.
//
// Both cardinal (ARM64 bare-metal) and diplomat (x86_64 UEFI) import these
// types, ensuring a unified abstraction for platform operations, boot sequences,
// and syscall dispatch.
package bootloader

import (
	"mazzy/shared/blockdev"
	"mazzy/shared/fs/fat32"
	"unsafe"
)

// PlatformOps contains low-level platform primitive operations.
// Each platform (UEFI, bare-metal ARM64, etc.) provides a different instance.
type PlatformOps struct {
	// Console
	PrintChar func(c byte)
	DebugOut  func(c byte)

	// Physical memory
	AllocPhysPages func(numPages uint64) (physAddr uint64, err error)
	FreePhysPages  func(physAddr uint64, numPages uint64) error
	ZeroMemory     func(addr, size uint64)

	// Virtual memory
	VM VMOps

	// Boot services lifecycle
	ExitBootServices func() error

	// Block device discovery
	GetBlockDevice func() (blockdev.BlockDevice, error)
}

// VMOps contains virtual memory / page table operations.
type VMOps struct {
	MapPage             func(virt, phys uint64, flags uint64) error
	UnmapPage           func(virt uint64) error
	MapRegion           func(virtBase, physBase, size uint64, flags uint64) error
	AllocAndMap         func(virt uint64, flags uint64) (phys uint64, err error)
	FlushTLB            func(virt uint64)
	ReadPTRoot          func() uint64
	WritePTRoot         func(val uint64)
	DisableWriteProtect func() // x86: CR0.WP clear; ARM: no-op
	EnableWriteProtect  func() // x86: CR0.WP set; ARM: no-op
}

// BootSequence contains the high-level boot flow steps.
// Each function performs one logical phase of the boot process.
type BootSequence struct {
	InitMemory      func() error
	InitMMU         func() error
	GetBlockDevice  func() (blockdev.BlockDevice, error)
	MountFilesystem func(dev blockdev.BlockDevice) (*fat32.FileSystem, error)
	LoadKernel      func(fs *fat32.FileSystem, path string) (*LoadedKernel, error)
	MapKernel       func(virtBase, physBase, size uint64) error
	PrepareKernel   func(kernel *LoadedKernel) error
	JumpToKernel    func(kernel *LoadedKernel)
}

// SyscallTable contains syscall handler functions.
type SyscallTable struct {
	Mmap    func(addr uintptr, length uint64, prot, flags, fd int32, offset int64) int64
	Munmap  func(addr uintptr, length uint64) int64
	Madvise func(addr uintptr, length uint64, advice int32) int64
	Brk     func(addr uintptr) int64
	Write   func(fd int32, buf unsafe.Pointer, count uint64) int64
	Read    func(fd int32, buf unsafe.Pointer, count uint64) int64
	Open    func(path unsafe.Pointer, flags, mode int32) int64
	Close   func(fd int32) int64
	Futex   func(uaddr unsafe.Pointer, op int32, val uint32, timeout, uaddr2 unsafe.Pointer, val3 uint32) int64
}

// KernelSymbol holds a named symbol extracted from the kernel ELF.
type KernelSymbol struct {
	Name  [64]byte
	Value uint64
}

// LoadedKernel contains information about a loaded kernel image.
type LoadedKernel struct {
	Entry       uint64           // Entry point virtual address
	LowestVirt  uint64           // Lowest virtual address (from ELF LOAD segments)
	HighestVirt uint64           // Highest virtual address (exclusive)
	PhysBase    uint64           // Physical base address where kernel was loaded
	Symbols     [16]KernelSymbol // Extracted symbols
	NumSymbols  int              // Number of valid entries in Symbols
}

// LoadSegment describes one ELF LOAD segment parsed from headers.
// The caller handles memory allocation and copying.
type LoadSegment struct {
	Vaddr  uint64
	Paddr  uint64
	Offset uint64 // file offset
	Filesz uint64
	Memsz  uint64
	Flags  uint32
}
