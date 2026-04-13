package sys

import (
	"syscall"
	"unsafe"

	merror "mazzy/mazarin/error"
	"mazzy/shared/mazzy"
)

// RunShepherd creates a new shepherd from ELF data in the caller's pages.
// A new address space is created, the ELF is loaded into it, and the
// shepherd's main thread is started.
//
// The raw ELF pages (from LoadFile) are implicitly unmapped from the caller.
//
// Optional args are passed as command-line arguments to the new shepherd.
// The new shepherd sees os.Args = ["/name.elf", "<shepherdNum>", args...].
//
// Usage:
//
//	lf, err := sys.LoadFile("/linux.elf")
//	if err == nil {
//	    err = sys.RunShepherd("linux", uintptr(lf.StartVA), int(lf.NumPages), int(lf.BytesRead))
//	}
//
//	// With arguments:
//	err = sys.RunShepherd("myapp", startVA, numPages, totalBytes, "--port", "8080")
func RunShepherd(name string, startVA uintptr, numPages int, totalBytes int, args ...string) *merror.Error {
	nameBytes := append([]byte(name), 0)

	var argsPtr uintptr
	var argsLen uintptr
	var packed []byte

	if len(args) > 0 {
		// Pack args as null-separated strings.
		for i, a := range args {
			if i > 0 {
				packed = append(packed, 0)
			}
			packed = append(packed, []byte(a)...)
		}
		argsPtr = uintptr(unsafe.Pointer(&packed[0]))
		argsLen = uintptr(len(packed))
	}

	r1, _, _ := syscall.RawSyscall6(
		mazzy.SysRunShepherd,
		uintptr(unsafe.Pointer(&nameBytes[0])),
		startVA,
		uintptr(numPages),
		uintptr(totalBytes),
		argsPtr,
		argsLen,
	)

	if r1 != 0 {
		if e := merror.FromCode(merror.ErrorCode(r1)); e != nil {
			return e
		}
		return merror.ErrInvalidELF
	}

	return nil
}
