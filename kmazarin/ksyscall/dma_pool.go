package ksyscall

// dma_pool.go — SysRegisterDMAPool / SysUnregisterDMAPool syscall implementations.
//
// Allows userspace to register a contiguous range of pages for direct DMA I/O.
// The kernel caches the PA for each page at registration time, enabling O(1)
// PA lookup during block I/O submission (no page table walk per request).
//
// Registered pages are pinned (PD_PINNED) and marked PageUserDMA so they
// won't be evicted or repurposed. The pool is per-shepherd and cleaned up
// automatically on shepherd exit.

import (
	"mazzy/kmazarin/kmem"
	"mazzy/kmazarin/proc"
	"mazzy/kmazarin/serial"
)

// SyscallRegisterDMAPool registers a contiguous range of userspace pages
// for direct DMA I/O (zero-copy block reads/writes).
//
// arg0 = baseVA   (start of contiguous buffer, must be page-aligned)
// arg1 = numPages (number of 4KB pages to register, max 256)
//
// Returns: 0 on success, negative errno on error.
//
//go:noinline
func SyscallRegisterDMAPool(arg0, arg1, _, _, _, _ uint64) int64 {
	baseVA := uintptr(arg0)
	numPages := int(arg1)

	// Validate caller
	shepherd := proc.CurrentShepherd()
	if shepherd == nil {
		return -1 // EPERM
	}

	// Only the block device owner may register a DMA pool
	ownerSID := getBlockDeviceOwnerPID()
	if ownerSID < 0 || int16(shepherd.PID) != ownerSID {
		serial.RawUARTPuts("[DMAPool] EPERM: not block device owner\r\n")
		return -1 // EPERM
	}

	// Validate args
	if baseVA&0xFFF != 0 {
		serial.RawUARTPuts("[DMAPool] EINVAL: baseVA not page-aligned\r\n")
		return -22 // EINVAL
	}
	if numPages <= 0 || numPages > proc.DMAPoolMaxPages {
		serial.RawUARTPuts("[DMAPool] EINVAL: numPages out of range\r\n")
		return -22 // EINVAL
	}

	// Cannot re-register without unregistering first
	if shepherd.DMAPool != nil {
		serial.RawUARTPuts("[DMAPool] EEXIST: pool already registered\r\n")
		return -17 // EEXIST
	}

	l0PA := shepherd.PageTableL0PA

	// Build pool: demand-map each page, walk page table to cache PA
	pool := new(proc.DMAPool)
	pool.BaseVA = baseVA
	pool.NumPages = numPages

	for i := 0; i < numPages; i++ {
		va := baseVA + uintptr(i)*kmem.PageSize

		// Ensure the page is mapped (demand-fault if needed)
		pa := kmem.DemandMapUserPage(va, l0PA)
		if pa == 0 {
			serial.RawUARTPuts("[DMAPool] ENOMEM: failed to map page\r\n")
			// Undo already-registered pages
			unregisterDMAPoolPages(pool, i)
			return -12 // ENOMEM
		}

		// Get the page-aligned PA
		pagePA := pa &^ (kmem.PageSize - 1)

		pool.Entries[i] = proc.DMAPoolEntry{
			UserVA: va,
			PA:     pagePA,
		}

		// Update page descriptor: mark as DMA-pinned
		desc := kmem.GetPageDescriptor(pagePA)
		if desc != nil {
			desc.Type = kmem.PageUserDMA
			desc.Flags |= kmem.PD_PINNED
			desc.RefCount++ // Kernel holds a ref for the pool registration
		}
	}

	shepherd.DMAPool = pool
	serial.RawUARTPuts("[DMAPool] registered ")
	serial.RawUARTDecimal(uint64(numPages))
	serial.RawUARTPuts(" pages\r\n")

	return 0
}

// SyscallUnregisterDMAPool unregisters the caller's DMA page pool.
// Unpins pages and decrements their ref counts.
//
// Returns: 0 on success, negative errno on error.
//
//go:noinline
func SyscallUnregisterDMAPool(_, _, _, _, _, _ uint64) int64 {
	shepherd := proc.CurrentShepherd()
	if shepherd == nil {
		return -1 // EPERM
	}

	pool := shepherd.DMAPool
	if pool == nil {
		return -2 // ENOENT
	}

	unregisterDMAPoolPages(pool, pool.NumPages)
	shepherd.DMAPool = nil

	serial.RawUARTPuts("[DMAPool] unregistered\r\n")
	return 0
}

// unregisterDMAPoolPages unpins and decrements ref counts for pool pages.
// Called during explicit unregistration and shepherd cleanup.
func unregisterDMAPoolPages(pool *proc.DMAPool, count int) {
	for i := 0; i < count; i++ {
		pagePA := pool.Entries[i].PA
		if pagePA == 0 {
			continue
		}
		desc := kmem.GetPageDescriptor(pagePA)
		if desc != nil {
			desc.Flags &^= kmem.PD_PINNED
			if desc.Type == kmem.PageUserDMA {
				desc.Type = kmem.PageUserHeap // Restore to normal userspace type
			}
			if desc.RefCount > 1 {
				desc.RefCount--
			}
		}
	}
}

// CleanupShepherdDMAPool releases the DMA pool for a dying shepherd.
// Called from shepherd cleanup before pages are freed.
func CleanupShepherdDMAPool(shepherd *proc.Shepherd) {
	pool := shepherd.DMAPool
	if pool == nil {
		return
	}
	unregisterDMAPoolPages(pool, pool.NumPages)
	shepherd.DMAPool = nil
}
