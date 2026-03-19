package sys

import (
	"syscall"
	"unsafe"

	merror "mazzy/mazarin/error"
)

// ProgramControl contains information about a loaded program.
// Allocated by shepherd, filled in by kernel during BootstrapRunElf.
type ProgramControl struct {
	// ProgramID is the kernel-assigned identifier for this program.
	// Used for subsequent control operations (stop, kill, suspend).
	ProgramID uint32

	// LoadAddress is the base address where the program was loaded.
	// All program addresses are relative to this base (PIE).
	LoadAddress uint64

	// EntryPoint is the address of main.MazarinMain().
	// Shepherd converts this to a function pointer and calls it.
	EntryPoint uint64

	// Reserved for future use (stack info, etc.)
	Reserved [8]uint64
}

// BootstrapRunElf loads an ELF from disk into this shepherd's address space.
// This is a bootstrap-only call used to get the disk manager off the disk.
// pc must point to writable memory (heap, data, BSS, or stack).
// shepherdSyscallEntry is the address of shepherd's syscall handler.
func BootstrapRunElf(filename string, shepherdSyscallEntry uintptr, pc *ProgramControl) *merror.Error {
	filenameBytes := append([]byte(filename), 0)

	result, _, _ := syscall.RawSyscall6(
		sysBootstrapRunElf,
		uintptr(unsafe.Pointer(&filenameBytes[0])),
		shepherdSyscallEntry,
		uintptr(unsafe.Pointer(pc)),
		0, 0, 0,
	)

	if result != 0 {
		if e := merror.FromCode(merror.ErrorCode(result)); e != nil {
			return e
		}
		return merror.ErrInvalidELF // fallback for unrecognized codes
	}

	return nil
}
