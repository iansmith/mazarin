package sys

import (
	"syscall"
	"unsafe"

	merror "mazzy/mazarin/error"
)

// MazLoadResult is filled in by the kernel when loading a .maz binary.
// Layout must match kmazarin/ksyscall/loadmaz.go exactly.
type MazLoadResult struct {
	EntryPoint uint64 // Address of main.MazarinMain in loaded .maz
	LoadBase   uint64 // Base VA where .maz was loaded
	LoadSize   uint64 // Total VA size of loaded segments
}

// LoadMaz loads a .maz PIE ELF into the calling priest's address space.
// The kernel reads the file from disk, loads segments into the priest's
// page table, applies PIE relocations, and resolves runtime imports
// against the priest's cached symbol table.
//
// On success, result.EntryPoint contains the address of main.MazarinMain
// in the loaded .maz, ready to be called as a goroutine.
func LoadMaz(filename string) (*MazLoadResult, *merror.Error) {
	filenameBytes := append([]byte(filename), 0)
	var result MazLoadResult

	r1, _, _ := syscall.RawSyscall6(
		sysLoadMaz,
		uintptr(unsafe.Pointer(&filenameBytes[0])),
		uintptr(unsafe.Pointer(&result)),
		0, 0, 0, 0,
	)

	if r1 != 0 {
		if e := merror.FromCode(merror.ErrorCode(r1)); e != nil {
			return nil, e
		}
		return nil, merror.ErrInvalidELF
	}

	return &result, nil
}
