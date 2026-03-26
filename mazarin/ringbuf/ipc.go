package ringbuf

import (
	"errors"
	"mazzy/mazarin/mem"
	"mazzy/mazarin/sys"
	"unsafe"
)

// New creates a ring buffer for IPC with a target shepherd.
// If pageAddr is 0, a page is allocated via SysAllocPages (kernel-tracked, PageIPCBuffer).
// The page is then mapped into targetSID's address space via the kernel.
// slotSize is the byte size of each message slot.
// slotCount must be a power of 2.
//
// Returns the initialized RingBuffer (in the caller's VA space).
// The target shepherd will receive the translated VA via a mailbox notification.
func New(targetSID int, pageAddr uintptr, slotSize, slotCount uint32) (*RingBuffer, error) {
	if pageAddr == 0 {
		ptr, err := mem.AllocPages(1, mem.PageIPC)
		if err != nil {
			return nil, errors.New("ringbuf: AllocPages failed")
		}
		pageAddr = uintptr(ptr)
	}

	// Touch the page to ensure it's faulted (AllocPages pages are pre-mapped,
	// but callers passing their own pageAddr may not have faulted it).
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
