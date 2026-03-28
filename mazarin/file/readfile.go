// Package file provides userspace APIs for file operations on Mazzy.
//
// ReadFilePages reads file data directly into caller-provided DMA pages
// (zero-copy from disk). LoadFile uses kernel-allocated pages with
// ownership transfer.
package file

import (
	"syscall"
	"unsafe"

	merror "mazzy/mazarin/error"
	"mazzy/shared/mazzy"
)

// ReadFilePagesResult holds the result of a ReadFilePages call.
type ReadFilePagesResult struct {
	BytesRead int
}

// ReadFilePages reads file data directly into caller-provided DMA pages.
// destVA must point to a MAZARIN_CONTIGUOUS buffer allocated via
// mem.AllocContiguous. The fs shepherd DMA's data directly into these
// pages — no intermediate copies.
//
// Parameters:
//
//	destVA     — VA of destination buffer (must be in a MAZARIN_CONTIGUOUS clump)
//	destSize   — buffer size in bytes
//	path       — file path on FAT32 (e.g., "/kmazarin.toml")
//	fileOffset — byte offset into file to start reading
//	readLen    — bytes to read (0 = fill buffer or until EOF)
//
// Returns bytes read, or an error.
func ReadFilePages(destVA uintptr, destSize int, path string, fileOffset int64, readLen int) (int, error) {
	pathBytes := append([]byte(path), 0)

	r1, _, _ := syscall.RawSyscall6(
		mazzy.SysReadFilePages,
		uintptr(unsafe.Pointer(&pathBytes[0])),
		destVA,
		uintptr(destSize),
		uintptr(fileOffset),
		uintptr(readLen),
		0,
	)

	if r1 != 0 {
		if e := merror.FromCode(merror.ErrorCode(r1)); e != nil {
			return 0, e
		}
		return 0, syscall.Errno(r1)
	}

	// TODO: The delegate reply should communicate bytesRead back.
	// For now, return destSize as an upper bound.
	return destSize, nil
}

// LoadWholeFile reads an entire file into caller-provided DMA pages.
// Convenience wrapper: fileOffset=0, readLen=0 (read until EOF or buffer full).
func LoadWholeFile(path string, destVA uintptr, destSize int) (int, error) {
	return ReadFilePages(destVA, destSize, path, 0, 0)
}

// LoadFileResult is filled in by the kernel when loading a file via fs delegate.
// Layout must match the kernel's writeU64ToUser sequence in delegate.go.
type LoadFileResult struct {
	StartVA   uint64 // VA where file pages are mapped in caller's address space
	NumPages  uint64 // Number of 4KB pages
	BytesRead uint64 // Actual file size in bytes
}

// LoadFile reads a file from the filesystem via the fs delegate.
// The file contents are mapped as pages in the caller's address space
// via zero-copy page transfer (the fs allocates pages and transfers ownership).
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
