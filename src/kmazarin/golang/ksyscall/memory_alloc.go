package ksyscall

import "kmazarin/kthread"

// PageSize is the standard page size (4KB).
const PageSize = 4096

// SyscallAllocPages allocates page-aligned memory.
// count: number of 4KB pages
// Returns: virtual address or negative errno.
func SyscallAllocPages(count, arg1, arg2, arg3, arg4, arg5 uint64) int64 {
	if count == 0 || count > 1024 {
		return -22 // EINVAL
	}

	pcb := kthread.GetCurrentPCB()
	if pcb == nil {
		return -22 // EINVAL
	}

	// TODO: Implement
	// 1. Allocate physical pages via kmem.AllocFrame()
	// 2. Find free virtual address range in pcb's address space
	// 3. Map pages into pcb.PageTableRoot
	// 4. Record in pcb.MappedPages for cleanup
	// 5. Return virtual address

	_ = count
	return -12 // ENOMEM (stub - not implemented yet)
}
