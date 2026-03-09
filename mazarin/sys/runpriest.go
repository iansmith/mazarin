package sys

import (
	"syscall"
	"unsafe"

	merror "mazzy/mazarin/error"
)

// RunPriest creates a new priest from ELF data in the caller's pages.
// A new address space is created, the ELF is loaded into it, and the
// priest's main thread is started.
//
// The raw ELF pages (from LoadFile) are implicitly unmapped from the caller.
//
// Usage:
//
//	lf, err := sys.LoadFile("/stdio.elf")
//	if err == nil {
//	    err = sys.RunPriest("stdio", uintptr(lf.StartVA), int(lf.NumPages), int(lf.BytesRead))
//	}
func RunPriest(name string, startVA uintptr, numPages int, totalBytes int) *merror.Error {
	nameBytes := append([]byte(name), 0)

	r1, _, _ := syscall.RawSyscall6(
		sysRunPriest,
		uintptr(unsafe.Pointer(&nameBytes[0])),
		startVA,
		uintptr(numPages),
		uintptr(totalBytes),
		0, 0,
	)

	if r1 != 0 {
		if e := merror.FromCode(merror.ErrorCode(r1)); e != nil {
			return e
		}
		return merror.ErrInvalidELF
	}

	return nil
}
