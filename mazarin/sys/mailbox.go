package sys

import (
	"errors"
	"unsafe"
)

const (
	sysMailboxMapPage = 0x102F
	sysMailboxSend    = 0x1030
	sysMailboxRecv    = 0x1031
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
	r1, _, errno := RawSyscall(sysMailboxMapPage,
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
	r1, _, errno := RawSyscall(sysMailboxSend,
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
// Uses entersyscall/exitsyscall to release the P while blocking.
func MailboxRecv() (MailboxNotification, error) {
	var notif MailboxNotification
	// Touch the struct to ensure the page is demand-faulted before the kernel writes to it.
	*(*byte)(unsafe.Pointer(&notif)) = 0
	runtime_entersyscall()
	r1, _, errno := RawSyscall(sysMailboxRecv,
		uintptr(unsafe.Pointer(&notif)),
		0, 0, 0, 0, 0)
	runtime_exitsyscall()

	if errno != 0 || int64(r1) < 0 {
		return MailboxNotification{}, errors.New("MailboxRecv failed")
	}
	return notif, nil
}
