package sys

import (
	"fmt"
	"syscall"
	"unsafe"

	merror "mazzy/mazarin/error"
)

// ProgramControl contains information about a loaded program.
// Allocated by priest, filled in by kernel during SysRun.
type ProgramControl struct {
	// ProgramID is the kernel-assigned identifier for this program.
	// Used for subsequent control operations (stop, kill, suspend).
	ProgramID uint32

	// LoadAddress is the base address where the program was loaded.
	// All program addresses are relative to this base (PIE).
	LoadAddress uint64

	// EntryPoint is the address of main.MazarinMain().
	// Priest converts this to a function pointer and calls it.
	EntryPoint uint64

	// Reserved for future use (stack info, etc.)
	Reserved [8]uint64
}

// Run loads a .maz program into this priest's address space.
// pc must point to writable memory (heap, data, BSS, or stack).
// priestSyscallEntry is the address of priest's syscall handler.
func Run(filename string, priestSyscallEntry uintptr, pc *ProgramControl) error {
	filenameBytes := append([]byte(filename), 0)

	result, _, _ := syscall.RawSyscall6(
		sysRun,
		uintptr(unsafe.Pointer(&filenameBytes[0])),
		priestSyscallEntry,
		uintptr(unsafe.Pointer(pc)),
		0, 0, 0,
	)

	if merror.IsError(uint64(result)) {
		return fmt.Errorf("SysRun failed: %s", merror.String(uint64(result)))
	}

	return nil
}
