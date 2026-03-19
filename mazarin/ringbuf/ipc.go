package ringbuf

import (
	"errors"
	"mazzy/mazarin/sys"
	"syscall"
	"unsafe"
)

// New creates a ring buffer for IPC with a target shepherd.
// If pageAddr is 0, a page is automatically allocated (via mmap + demand fault).
// The page is then mapped into targetSID's address space via the kernel.
// slotSize is the byte size of each message slot.
// slotCount must be a power of 2.
//
// Returns the initialized RingBuffer (in the caller's VA space).
// The target shepherd will receive the translated VA via a mailbox notification.
func New(targetSID int, pageAddr uintptr, slotSize, slotCount uint32) (*RingBuffer, error) {
	if pageAddr == 0 {
		va, _, errno := syscall.RawSyscall6(
			syscall.SYS_MMAP, 0, 4096,
			syscall.PROT_READ|syscall.PROT_WRITE,
			syscall.MAP_PRIVATE|syscall.MAP_ANONYMOUS,
			^uintptr(0), 0)
		if errno != 0 || int64(va) < 0 {
			return nil, errors.New("ringbuf: mmap failed")
		}
		pageAddr = va
	}

	// Touch the page to ensure it's demand-faulted before mapping
	p := (*byte)(unsafe.Pointer(pageAddr))
	*p = 0

	// Map the page into the target shepherd's address space
	_, err := sys.MailboxMapPage(targetSID, pageAddr)
	if err != nil {
		return nil, err
	}

	// Initialize the ring buffer header on the page
	rb := Init(pageAddr, slotSize, slotCount)
	return rb, nil
}
