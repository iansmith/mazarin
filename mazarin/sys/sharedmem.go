package sys

import (
	"mazzy/shared/mazzy"
	"syscall"
)

// TransferPages transfers ownership of contiguous pages from the calling shepherd
// to a target shepherd. The pages are unmapped from the caller and mapped into the
// target's address space. Returns the target VA base address.
//
// sourceVA must be page-aligned. numPages must be 1..MaxTransferPages
// (see kmazarin/ksyscall/share_pages.go).
// elfFlags controls target mapping permissions (0 = read-write).
func TransferPages(targetPID int, sourceVA uintptr, numPages int, elfFlags uint32) (uintptr, error) {
	r1, _, errno := syscall.RawSyscall6(
		mazzy.SysTransferPages,
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
// This is the zero-copy page transfer primitive used by fsclient IPC.
func TransferAndUnmap(targetPID int, sourceVA uintptr, numPages int) (uintptr, error) {
	return TransferPages(targetPID, sourceVA, numPages, 0) // elfFlags=0 → RW
}

// SharePagesWithTarget maps a range of the caller's pages into a target
// shepherd's address space as shared pages. The caller retains ownership.
// Physical pages need not be contiguous; VAs are contiguous in both spaces.
// Returns the target VA base address.
//
// va must be page-aligned. numPages must be 1..4096.
func SharePagesWithTarget(targetPID int, va uintptr, numPages int) (uintptr, error) {
	r1, _, errno := syscall.RawSyscall6(
		mazzy.SysSharePagesWithTarget,
		uintptr(targetPID),
		va,
		uintptr(numPages),
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

// TransferDMAClump transfers ownership of one whole MAZARIN_CONTIGUOUS clump
// from the caller to a target shepherd. The clump is unmapped from the caller's
// address space, its pages' PageDescriptor.Owner is updated to the target, and
// the pages are mapped into the target at a contiguous VA range. The clump
// entry is removed from the caller's per-shepherd clump table.
//
// This is the page-handoff primitive that NetIPC uses for client→net.elf TX
// pages: the client allocates a 1-page clump via mem.AllocContiguous, fills it
// with payload, then calls this to hand the page over.
//
// clumpStartVA must match an existing DMAClump.StartVA exactly (i.e. the VA
// returned by mem.AllocContiguous). elfFlags controls target mapping
// permissions (0 = read-write).
//
// Returns the target VA base on success.
func TransferDMAClump(targetPID int, clumpStartVA uintptr, elfFlags uint32) (uintptr, error) {
	r1, _, errno := syscall.RawSyscall6(
		mazzy.SysTransferDMAClump,
		uintptr(targetPID),
		clumpStartVA,
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

// ShareNetPageWithClient maps a page owned by the caller (intended to be net.elf)
// into a client shepherd's address space. The caller retains ownership; the
// page's refcount is incremented and PD_NET_OWNED_SHARED is set in addition to
// PD_SHARED. The page survives the client's death as long as the caller holds
// its own mapping.
//
// Direction is flipped from MapSharedPage: the *caller* is the owner, ownerVA
// is in the caller's address space, clientPID identifies the receiver.
//
// ownerVA must be page-aligned. elfFlags controls client mapping permissions
// (0 = read-write).
//
// Returns the client VA on success.
func ShareNetPageWithClient(clientPID int, ownerVA uintptr, elfFlags uint32) (uintptr, error) {
	r1, _, errno := syscall.RawSyscall6(
		mazzy.SysShareNetPageWithClient,
		uintptr(clientPID),
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

// MapSharedPage creates a shared mapping of a page owned by another shepherd
// into the calling shepherd's address space. Both shepherds can access the same
// physical page. The page's refcount is incremented. Returns the caller VA.
//
// ownerVA must be page-aligned.
// elfFlags controls caller's mapping permissions (0 = read-write).
func MapSharedPage(ownerPID int, ownerVA uintptr, elfFlags uint32) (uintptr, error) {
	r1, _, errno := syscall.RawSyscall6(
		mazzy.SysMapSharedPage,
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
