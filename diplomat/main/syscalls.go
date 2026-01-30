// diplomat/main/syscalls.go - Basic syscall stubs for Go runtime
//
// These are minimal implementations to support Go runtime initialization.
// Most return success or reasonable default values.

package main

import "unsafe"

// DiplomatWrite implements write syscall
// For diplomat, we only support stdout/stderr (fd 1/2) via UEFI ConOut
//
//go:nosplit
func DiplomatWrite(fd int32, buf unsafe.Pointer, count uint64) int64 {
	// Only support stdout (1) and stderr (2)
	if fd != 1 && fd != 2 {
		return -1 // EBADF
	}

	// Can't write if no system table
	if systemTable == nil || systemTable.ConOut == nil {
		return -1
	}

	// Convert buffer to []byte
	bytes := unsafe.Slice((*byte)(buf), count)

	// Write each character via UEFI ConOut
	for _, b := range bytes {
		plat.PrintChar(uint16(b))
	}

	return int64(count) // Return bytes written
}

// DiplomatRead implements read syscall (stub)
// Always return EOF for now
//
//go:nosplit
func DiplomatRead(fd int32, buf unsafe.Pointer, count uint64) int64 {
	return 0 // EOF
}

// DiplomatOpen implements open syscall (stub)
//
//go:nosplit
func DiplomatOpen(path unsafe.Pointer, flags int32, mode int32) int64 {
	return -1 // ENOENT - file not found
}

// DiplomatClose implements close syscall (stub)
//
//go:nosplit
func DiplomatClose(fd int32) int64 {
	return 0 // Success
}
