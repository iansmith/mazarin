package sys

import (
	"syscall"
	"unsafe"

	merror "mazzy/mazarin/error"
)

// RunMaz processes pages in the caller's address space as a .maz ELF binary.
// The ELF is parsed, segments are loaded, relocations applied, and imports
// resolved against the caller's symbol table.
//
// The raw ELF pages (from LoadFile) are implicitly unmapped from the caller.
// On success, result contains the entry point and metadata for the loaded .maz.
//
// Usage:
//
//	lf, err := sys.LoadFile("/helloworld.maz")
//	if err == nil {
//	    result, err := sys.RunMaz(uintptr(lf.StartVA), int(lf.NumPages), int(lf.BytesRead))
//	}
func RunMaz(startVA uintptr, numPages int, totalBytes int) (*MazLoadResult, *merror.Error) {
	var result MazLoadResult
	result.EntryPoint = ^uint64(0) // force demand paging

	r1, _, _ := syscall.RawSyscall6(
		sysRunMaz,
		startVA,
		uintptr(numPages),
		uintptr(totalBytes),
		uintptr(unsafe.Pointer(&result)),
		0, 0,
	)

	if r1 != 0 {
		if e := merror.FromCode(merror.ErrorCode(r1)); e != nil {
			return nil, e
		}
		return nil, merror.ErrInvalidELF
	}

	return &result, nil
}
