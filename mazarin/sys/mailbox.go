package sys

import (
	"errors"
	"mazzy/shared/mazzy"
	"unsafe"
)

// MailboxNotification is the userspace representation of a received notification.
type MailboxNotification struct {
	Code      int64  // WMNotify or ShepherdNotify
	SenderSID int64  // Who sent this
	RingAddr  uint64 // Translated VA of the ring buffer in receiver's space
}

// MailboxMapPage maps a page from the caller's address space into a target
// shepherd's space. The callerVA need not be page-aligned — the offset is
// preserved. Returns the target's VA on success.
func MailboxMapPage(targetSID int, callerVA uintptr) (uintptr, error) {
	r1, _, errno := RawSyscall(mazzy.SysMailboxMapPage,
		uintptr(targetSID),
		callerVA,
		0, 0, 0, 0)
	if errno != 0 || int64(r1) < 0 {
		return 0, errors.New("MailboxMapPage failed")
	}
	return r1, nil
}

// MailboxSend sends a notification to a target shepherd.
// callerVA is the ring buffer address in the caller's address space —
// the kernel translates it to the target's VA using the cached mapping.
func MailboxSend(targetSID int, code int64, callerVA uintptr) error {
	r1, _, errno := RawSyscall(mazzy.SysMailboxSend,
		uintptr(targetSID),
		uintptr(code),
		callerVA,
		0, 0, 0)
	if errno != 0 || int64(r1) < 0 {
		return errors.New("MailboxSend failed")
	}
	return nil
}

// MailboxRecv blocks until a mailbox notification arrives.
// Uses BlockingSyscall (entersyscallblock) because this is a known-blocking
// call — the kernel holds the thread until a notification arrives.
func MailboxRecv() (MailboxNotification, error) {
	var notif MailboxNotification
	// Touch the struct to ensure the page is demand-faulted before the kernel writes to it.
	*(*byte)(unsafe.Pointer(&notif)) = 0
	r1, _, errno := BlockingSyscall(mazzy.SysMailboxRecv,
		uintptr(unsafe.Pointer(&notif)),
		0, 0, 0, 0, 0)

	if errno != 0 || int64(r1) < 0 {
		return MailboxNotification{}, errors.New("MailboxRecv failed")
	}
	return notif, nil
}
