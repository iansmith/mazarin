package sys

import (
	"syscall"
	"unsafe"

	merror "mazzy/mazarin/error"
	"mazzy/shared/mazzy"
)

// LoadFileResult is filled in by the kernel when loading a file via fs.maz.
// Layout must match the kernel's writeU64ToUser sequence in delegate.go.
type LoadFileResult struct {
	StartVA   uint64 // VA where file pages are mapped in caller's address space
	NumPages  uint64 // Number of 4KB pages
	BytesRead uint64 // Actual file size in bytes
}

// LoadFile reads a file from the filesystem via the fs.maz delegate.
// The file contents are mapped as pages in the caller's address space
// via zero-copy page transfer.
//
// Returns the result struct on success, or an error if the file could not
// be loaded (no delegate, not ready, file not found, etc.).
func LoadFile(path string) (*LoadFileResult, *merror.Error) {
	pathBytes := append([]byte(path), 0)
	var result LoadFileResult
	// Force demand paging of the result struct before the syscall.
	result.StartVA = ^uint64(0)

	r1, _, _ := syscall.RawSyscall6(
		mazzy.SysLoadFile,
		uintptr(unsafe.Pointer(&pathBytes[0])),
		uintptr(unsafe.Pointer(&result)),
		0, 0, 0, 0,
	)

	if r1 != 0 {
		if e := merror.FromCode(merror.ErrorCode(r1)); e != nil {
			return nil, e
		}
		return nil, merror.ErrFileNotFound
	}

	return &result, nil
}
