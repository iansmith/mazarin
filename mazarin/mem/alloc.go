// Package mem provides userspace memory management APIs for Mazzy shepherds.
//
// Functions in this package handle page allocation, contiguous DMA memory,
// and block I/O submission. For direct system call wrappers (IPC, delegates,
// soft IRQ, etc.), see mazarin/sys.
package mem

import (
	"errors"
	"mazzy/shared/constants"
	"mazzy/shared/mazzy"
	"syscall"
	"unsafe"
)

// Page type constants for AllocPages.
const (
	PageIPC       = constants.UserPageIPC
	PageFontCache = constants.UserPageFontCache
	PageShared    = constants.UserPageShared
	PageRamdisk   = constants.UserPageRamdisk
)

// AllocPages allocates kernel-tracked pages that are properly typed and
// immediately backed by physical frames. Returns a pointer to a contiguous
// virtual address region of count*4096 bytes.
//
// Unlike Go's make([]byte, ...) which allocates from the Go heap and
// demand-faults as PageUserHeap, these pages are born with the correct
// PageType for accounting. Use this for IPC ring buffers, font caches,
// and other shared-purpose memory.
//
// The returned pointer is page-aligned. The backing pages are zeroed.
func AllocPages(count int, pageType int) (unsafe.Pointer, error) {
	r1, _, errno := syscall.RawSyscall6(mazzy.SysAllocPages,
		uintptr(count),
		uintptr(pageType),
		0, 0, 0, 0)
	if errno != 0 || int64(r1) < 0 {
		return nil, errors.New("AllocPages failed")
	}
	return unsafe.Pointer(r1), nil
}

// AllocPagesSlice is like AllocPages but returns a []byte slice over the
// allocated region. The slice length and capacity are both count*4096.
func AllocPagesSlice(count int, pageType int) ([]byte, error) {
	ptr, err := AllocPages(count, pageType)
	if err != nil {
		return nil, err
	}
	size := count * 4096
	return unsafe.Slice((*byte)(ptr), size), nil
}

// FreePages releases pages previously allocated by AllocPages or AllocPagesSlice.
// The pointer must be the exact base returned by AllocPages. The count must match
// the original allocation size (partial frees from the base are allowed but the
// caller is responsible for tracking the remainder).
func FreePages(ptr unsafe.Pointer, count int) error {
	r1, _, errno := syscall.RawSyscall6(mazzy.SysFreePages,
		uintptr(ptr),
		uintptr(count),
		0, 0, 0, 0)
	if errno != 0 || int64(r1) < 0 {
		return errors.New("FreePages failed")
	}
	return nil
}
