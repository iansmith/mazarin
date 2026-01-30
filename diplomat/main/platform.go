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
	"mazzy/shared/fs/fat32"
	"unsafe"
)

// PlatformOps contains all hardware/UEFI primitive operations.
// A different platform (e.g., ARM64, non-UEFI) would provide a different instance.
type PlatformOps struct {
	// Console
	PrintChar    func(c uint16)
	DebugPortOut func(c byte)

	// Memory — UEFI page allocation
	AllocatePages       func(allocType, memoryType uint32, pages uint64, memory *uint64) EFI_STATUS
	FreePages           func(memory uint64, pages uint64) EFI_STATUS
	AllocatePagesForMmap func(addr uintptr, size uintptr, fixed bool) (uintptr, bool)
	ZeroMemory          func(addr, size uint64)

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
}

// BootSequence contains the high-level boot steps.
// Each function performs one logical phase of the boot process.
type BootSequence struct {
	InitSpans       func() bool
	GetBlockDevice  func() (*UEFIBlockDevice, error)
	MountFilesystem func(dev blockdev.BlockDevice) (*fat32.FileSystem, error)
	LoadKernel      func(fs *fat32.FileSystem, path string) (*LoadedKernel, error)
	MapKernel       func(virtBase, physBase, size uint64) error
	JumpToKernel    func(entry uint64)
}

// SyscallTable contains all syscall handler functions.
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
