package sys

import (
	"syscall"
)

// Shared memory IPC syscall numbers.
// Must match kernel-side definitions in ksyscall/mazzy.go.
const (
	sysTransferPages = 0x1010
	sysMapSharedPage = 0x1011
)

// TransferPages transfers ownership of contiguous pages from the calling shepherd
// to a target shepherd. The pages are unmapped from the caller and mapped into the
// target's address space. Returns the target VA base address.
//
// sourceVA must be page-aligned. numPages must be 1..256.
// elfFlags controls target mapping permissions (0 = read-write).
func TransferPages(targetPID int, sourceVA uintptr, numPages int, elfFlags uint32) (uintptr, error) {
	r1, _, errno := syscall.RawSyscall6(
		sysTransferPages,
		uintptr(targetPID),
		sourceVA,
		uintptr(numPages),
		uintptr(elfFlags),
		0, 0,
	)

	if errno != 0 {
		return 0, errno
	}
	if int64(r1) < 0 {
		return 0, syscall.Errno(-int64(r1))
	}
	return r1, nil
}

// TransferAndUnmap transfers ownership of pages from this shepherd to a target
// shepherd, unmapping them from the caller. The pages are mapped into the target's
// address space at a kernel-chosen VA. Returns the target VA base address.
// This is the zero-copy page transfer primitive used by LoadFile and IPC.
func TransferAndUnmap(targetPID int, sourceVA uintptr, numPages int) (uintptr, error) {
	return TransferPages(targetPID, sourceVA, numPages, 0) // elfFlags=0 → RW
}

// MapSharedPage creates a shared mapping of a page owned by another shepherd
// into the calling shepherd's address space. Both shepherds can access the same
// physical page. The page's refcount is incremented. Returns the caller VA.
//
// ownerVA must be page-aligned.
// elfFlags controls caller's mapping permissions (0 = read-write).
func MapSharedPage(ownerPID int, ownerVA uintptr, elfFlags uint32) (uintptr, error) {
	r1, _, errno := syscall.RawSyscall6(
		sysMapSharedPage,
		uintptr(ownerPID),
		ownerVA,
		uintptr(elfFlags),
		0, 0, 0,
	)

	if errno != 0 {
		return 0, errno
	}
	if int64(r1) < 0 {
		return 0, syscall.Errno(-int64(r1))
	}
	return r1, nil
}
