// free_pages.go - SysFreePages syscall: release pages allocated by SysAllocPages.
//
// This is the inverse of SyscallAllocPages. For each page in the range:
// unmap from the caller's page table, release the physical frame, and
// remove the span from the caller's tracked spans.
package ksyscall

import (
	"mazzy/kmazarin/kmem"
	"mazzy/kmazarin/proc"
)

// SyscallFreePages releases pages previously allocated by SysAllocPages.
//
// arg0 = base VA (must be page-aligned, returned by SysAllocPages)
// arg1 = number of pages to free
//
// Returns: 0 on success, negative errno on failure.
//
//go:noinline
func SyscallFreePages(arg0, arg1, _, _, _, _ uint64) int64 {
	baseVA := uintptr(arg0)
	numPages := int(arg1)

	if baseVA == 0 || baseVA&0xFFF != 0 {
		return -22 // EINVAL
	}
	if numPages < 1 || numPages > MaxAllocPages {
		return -22 // EINVAL
	}

	shepherd := proc.CurrentShepherd()
	if shepherd == nil {
		return -1 // EPERM
	}

	unmapUserPages(baseVA, numPages, shepherd.PageTableL0PA, int16(shepherd.PID))

	kmem.TlbiVMALLE1()
	kmem.DsbISH()
	kmem.IsbSY()

	return 0
}
