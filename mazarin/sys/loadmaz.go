package sys

import (
	"fmt"
	"syscall"
	"unsafe"

	merror "mazzy/mazarin/error"
)

// runtime_entersyscall releases the Go P before a potentially-blocking syscall.
//
//go:linkname runtime_entersyscall runtime.entersyscall
func runtime_entersyscall()

// runtime_exitsyscall reacquires a Go P after returning from a syscall.
//
//go:linkname runtime_exitsyscall runtime.exitsyscall
func runtime_exitsyscall()

// MazLoadResult is filled in by the kernel when loading a .maz binary.
// Layout must match kmazarin/ksyscall/loadmaz.go exactly.
type MazLoadResult struct {
	EntryPoint     uint64 // Address of main.MazarinMain in loaded .maz
	LoadBase       uint64 // Base VA where .maz was loaded
	LoadSize       uint64 // Total VA size of loaded segments
	ModuledataAddr uint64 // Address of runtime.firstmoduledata in loaded .maz (0 if not found)
	PriestInitAddr uint64 // Address of main.MazarinPriest in loaded .maz (0 if not found)
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
	// Force the page containing result to be demand-paged before the syscall.
	// Go's allocator relies on mmap returning zeroed pages and may not write
	// to fresh heap spans, leaving them unmapped. The kernel's ValidateWritablePtr
	// walks the page table and rejects unmapped pages. Writing a sentinel here
	// triggers the page fault in userspace so the page is mapped when the kernel
	// validates it.
	result.EntryPoint = ^uint64(0)

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

// RegisterMazModule registers the .maz's moduledata with the Go runtime so that
// findfunc(pc) can resolve .maz PCs to function names, enabling stack traces.
// Call this after LoadMaz but before calling the .maz's entry point.
func RegisterMazModule(result *MazLoadResult) {
	if result.ModuledataAddr == 0 {
		fmt.Println("[sys] RegisterMazModule: no moduledata available (stack traces will not include .maz frames)")
		return
	}
	fmt.Printf("[sys] RegisterMazModule: registering moduledata at 0x%X\n", result.ModuledataAddr)
	registerMazModuledata(uintptr(result.ModuledataAddr))
}

//go:linkname registerMazModuledata RegisterMazModuledata
func registerMazModuledata(mdPtr uintptr)
