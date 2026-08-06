package ksyscall

import (
	"mazzy/kmazarin/klog"
	"mazzy/kmazarin/kmem"
	"mazzy/kmazarin/proc"
	"mazzy/kmazarin/serial"
	"mazzy/shared/constants"
	"sync/atomic"
	"unsafe"
)

// Userspace mmap allocates from low-memory range accessible via TTBR0 (or equivalent).
// This is used by shepherd and other userspace programs.
//
// Per-process bump pointers and span groups live in proc.Shepherd (proc package),
// accessed via proc.CurrentShepherd(). Physical memory is only used when pages
// are actually faulted in.
const (
	userMmapEnd = 0x0000700000000000 // 112TB - plenty of VA space
)

// Userspace framebuffer mapping constants.
// The framebuffer is mapped at a fixed VA for all shepherds to allow UI rendering.
// Located below the stack region to avoid conflicts.
const (
	UserFramebufferVA = 0x00007FFE00000000 // Fixed VA for framebuffer in shepherd space
)

// Constraint shared page mapping constants.
// Constraint pages are mapped read-only into every shepherd's address space.
// The kernel writes via its own VA mapping (PA + KernelVAOffset).
const (
	UserConstraintPagesVA   = 0x00007FFD00000000       // Fixed VA for constraint pages
	UserConstraintPagesSize = kmem.ConstraintTotalSize // Derived from region layout
)

// userspaceActive is set to true when we jump to userspace.
// Mmap calls after this point (with addr=0) should use userspace allocator.
var userspaceActive uint32 // 0 = kernel only, 1 = userspace active

// SetUserspaceActive is called when we launch userspace to enable userspace mmap.
//
//go:nosplit
func SetUserspaceActive() {
	atomic.StoreUint32(&userspaceActive, 1)
}

// Linux mmap flags
const (
	_MAP_SHARED  = 0x01
	_MAP_PRIVATE = 0x02
	_MAP_FIXED   = 0x10
)

// SyscallMmap implements the mmap(2) syscall
// For userspace callers: returns virtual addresses in low memory (TTBR0).
// For kernel: uses kernel heap range (high memory, TTBR1).
// Pages are NOT mapped immediately - they will be demand-paged on first access.
//
// Hint handling (addr != 0 without MAP_FIXED):
// - If the hint is valid and doesn't overlap existing spans, honor it
// - Otherwise fall back to bump allocator
//
//go:nosplit
func SyscallMmap(addr, length, prot, flags, fd, offset uint64) int64 {
	// Linux PROT_* flags
	const protWrite = 2

	// Zero-length mmap is invalid (EINVAL) - standard Linux behavior
	if length == 0 {
		return -22 // EINVAL
	}

	// Align length to page size (4KB)
	pageSize := uint64(4096)
	alignedLength := (length + pageSize - 1) & ^(pageSize - 1)

	// MAZARIN_CONTIGUOUS: physically contiguous DMA pages, pre-mapped
	if (flags & constants.MAZARIN_CONTIGUOUS) != 0 {
		return syscallMmapContiguous(alignedLength)
	}

	var result uint64

	// Check if userspace is active and determine which allocator to use
	isUserspace := atomic.LoadUint32(&userspaceActive) != 0
	isMapFixed := (flags & _MAP_FIXED) != 0

	// Handle MAP_FIXED: must return the exact address or fail
	// MAP_FIXED can overwrite existing mappings (dangerous but that's the semantics)
	if isMapFixed && addr != 0 {
		if isUserspace && addr >= userMmapStart && addr+alignedLength <= userMmapEnd {
			// [MAZ-179 tier-13b probe] MAP_FIXED census (overlap is intended here)
			atomic.AddUint32(&proc.GrantFixedMaps, 1)
			// Check if this MAP_FIXED overlaps any file mapping
			checkMapFixedFileOverlap(addr, alignedLength)
			// Real Linux MAP_FIXED semantics: atomically replace any existing mapping.
			// Unmap existing PTEs so demand-faulting the range yields fresh zeroed pages.
			// Without this, stale physical pages (e.g. from a previous GC arena) can
			// corrupt new allocations at the same VA.
			unmapFixedRange(addr, alignedLength)
			// Remove any existing spans that overlap, then add new span
			removeSpan(addr, alignedLength)
			if !addSpan(addr, alignedLength) {
				mmapENOMEM('A', addr, length)
				return -12 // ENOMEM
			}
			result = addr
		} else if !isUserspace || addr >= 0x0000800000000000 {
			// Kernel fixed mapping
			cfg := getRuntimeConfigTyped()
			heapStart := uint64(cfg.KernelHeapStart)
			heapEnd := uint64(cfg.KernelHeapEnd)
			if addr >= heapStart && addr+alignedLength <= heapEnd {
				result = addr
			} else {
				mmapENOMEM('B', addr, length)
				return -12 // ENOMEM - can't honor MAP_FIXED at this address
			}
		} else {
			mmapENOMEM('C', addr, length)
			return -12 // ENOMEM - can't honor MAP_FIXED at this address
		}
	} else if isUserspace && addr < 0x0000800000000000 {
		// Userspace request without MAP_FIXED
		if addr != 0 && addr >= userMmapStart && addr+alignedLength <= userMmapEnd {
			// Try to honor the hint if it doesn't overlap existing spans
			result = tryReserveHint(addr, alignedLength)
			if result == 0 {
				// [MAZ-179 tier-13b probe] arena-hint pressure gauge
				atomic.AddUint32(&proc.GrantHintFallbacks, 1)
				// Hint couldn't be honored, fall back to bump allocator
				result = userBumpAlloc(alignedLength)
				if result != 0 {
					addSpan(result, alignedLength)
				}
			}
		} else {
			// No hint or invalid hint - use bump allocator
			result = userBumpAlloc(alignedLength)
			if result != 0 {
				addSpan(result, alignedLength)
			}
		}
	} else if addr != 0 && addr >= 0x0000800000000000 {
		// Kernel hint in high memory - try to honor it
		cfg := getRuntimeConfigTyped()
		heapStart := uint64(cfg.KernelHeapStart)
		heapEnd := uint64(cfg.KernelHeapEnd)
		if addr >= heapStart && addr+alignedLength <= heapEnd {
			result = addr
		} else {
			result = kernelBumpAlloc(alignedLength)
		}
	} else {
		// Kernel request (before userspace or high memory hint)
		result = kernelBumpAlloc(alignedLength)
	}

	// Return result (or error)
	if result == 0 {
		mmapENOMEM('D', addr, length)
		return -12 // ENOMEM
	}

	// DEBUG: Check for pre-existing PTEs in the allocated VA range.
	// This detects overlap with Go runtime heap pages.
	if (flags&0x20) == 0 && fd != ^uint64(0) && int64(fd) >= 0 {
		scanForStalePTEs(result, alignedLength, fd)
	}

	// Record file-backed mapping if fd is valid and not MAP_ANONYMOUS (0x20).
	// Both read-only and writable mappings are recorded. Writable MAP_SHARED
	// mappings are written back to the file on munmap or shepherd death.
	if (flags&0x20) == 0 && fd != ^uint64(0) && int64(fd) >= 0 {
		isWritable := (prot & protWrite) != 0
		isShared := (flags & _MAP_SHARED) != 0
		p := proc.CurrentShepherd()
		if p != nil {
			p.AddFileMapping(result, alignedLength, int16(p.PID), int32(fd), offset, isWritable, isShared)
		}
	}

	return int64(result)
}

// scanForStalePTEs walks the page table for a file-backed mmap VA range and
// reports any pre-existing PTEs. This detects cases where the bump allocator
// returns VAs that overlap with Go runtime heap pages.
//
//go:noinline
func scanForStalePTEs(va, length, fd uint64) {
	p := proc.CurrentShepherd()
	if p == nil || p.PageTableL0PA == 0 {
		return
	}
	l0PA := p.PageTableL0PA
	pageCount := length / 4096
	staleCount := uint64(0)
	for i := uint64(0); i < pageCount; i++ {
		pageVA := uintptr(va + i*4096)
		pa := kmem.WalkUserPageTableWithL0(pageVA, l0PA)
		if pa != 0 {
			staleCount++
		}
	}
	if staleCount > 0 {
		klog.Errf("[mmap:STALE] total=%x in fd=%x range %x-%x\n", staleCount, fd, va, va+length)
	}
}

// unmapFixedRange releases any physical pages mapped in [addr, addr+length) for
// the current shepherd. Called before re-registering the span under MAP_FIXED so
// that the range demand-faults fresh zeroed pages instead of reusing stale frames.
// The SVC handler runs on a kernel worker goroutine, so we use the shepherd's
// stored L0PA rather than the hardware TTBR0 register.
//
//go:noinline
func unmapFixedRange(addr, length uint64) {
	shepherd := proc.CurrentShepherd()
	if shepherd == nil {
		return
	}
	l0PA := shepherd.PageTableL0PA
	if l0PA == 0 {
		return
	}
	pageSize := uint64(4096)
	alignedAddr := addr & ^(pageSize - 1)
	alignedEnd := (addr + length + pageSize - 1) & ^(pageSize - 1)
	savedDAIF := saveAndDisableIRQs()
	for va := alignedAddr; va < alignedEnd; va += pageSize {
		pa := kmem.UnmapUserPageWithL0(uintptr(va), l0PA)
		if pa != 0 {
			kmem.ReleasePageByPA(pa)
		}
	}
	restoreIRQs(savedDAIF)
}

// checkMapFixedFileOverlap checks if a MAP_FIXED call would overlap with any
// existing file-backed mapping. This detects Go's MAP_FIXED overwriting
// file-backed mmap PTEs. Separated to avoid nosplit growth.
//
//go:noinline
func checkMapFixedFileOverlap(addr, length uint64) {
	p := proc.CurrentShepherd()
	if p == nil {
		return
	}
	fm := p.FindFileMappingByVA(addr)
	if fm != nil {
		klog.Errf("[MAP_FIXED:OVERLAP] sid=%d addr=%x len=%x HITS file mapping fd=%d [%x, %x)\n",
			p.PID, addr, length, fm.FD, fm.StartVA, fm.StartVA+fm.Length)
	}
}

// mmapENOMEM logs an mmap ENOMEM via direct UART. path identifies which
// branch failed (A=addSpan, B=kernel fixed, C=user fixed, D=bump alloc).
//
//go:nosplit
func mmapENOMEM(path byte, addr, length uint64) {
	serial.PollWrite('[')
	serial.PollWrite('m')
	serial.PollWrite('m')
	serial.PollWrite('a')
	serial.PollWrite('p')
	serial.PollWrite(':')
	serial.PollWrite(path)
	serial.PollWrite(']')
	serial.PollWrite(' ')
	serial.PollWrite('E')
	serial.PollWrite('N')
	serial.PollWrite('O')
	serial.PollWrite('M')
	serial.PollWrite('E')
	serial.PollWrite('M')
	serial.PollWrite(' ')
	serial.PollWrite('a')
	serial.PollWrite('=')
	kmem.SerialHex16(addr)
	serial.PollWrite(' ')
	serial.PollWrite('l')
	serial.PollWrite('=')
	kmem.SerialHex16(length)
	serial.PollWrite('\r')
	serial.PollWrite('\n')
	kmem.LogPageBreakdown()
}

// GetUserMmapAllocEnd returns the current end of userspace mmap allocations
// for the calling process. Any address >= this value has NOT been allocated.
// This is used by the page fault handler to validate fault addresses.
//
//go:nosplit
func GetUserMmapAllocEnd() uint64 {
	p := proc.CurrentShepherd()
	if p == nil {
		return userMmapStart
	}
	v := atomic.LoadUint64(&p.BumpPointer)
	if v == 0 {
		return userMmapStart
	}
	return v
}

//go:nosplit
func userBumpAlloc(size uint64) uint64 {
	p := proc.CurrentShepherd()
	if p == nil {
		return 0 // No shepherd — kernel context, don't bump-alloc userspace VA
	}

	// Align to page boundary
	pageSize := uint64(4096)
	aligned := (size + pageSize - 1) & ^(pageSize - 1)

	// Atomically allocate from this shepherd's bump pointer
	for {
		currentPtr := atomic.LoadUint64(&p.BumpPointer)

		// Lazy initialization: first allocation starts at userMmapStart
		if currentPtr == 0 {
			atomic.CompareAndSwapUint64(&p.BumpPointer, 0, userMmapStart)
			continue
		}

		nextPtr := currentPtr + aligned

		// Check if this allocation would overlap any existing span
		if findSpanOverlapEnd(currentPtr, aligned) != 0 {
			return 0 // ENOMEM - allocation conflicts with reserved span
		}

		// Check for wrap-around AND exceeding end
		if nextPtr < currentPtr || nextPtr > userMmapEnd {
			return 0 // Out of VA space
		}

		if atomic.CompareAndSwapUint64(&p.BumpPointer, currentPtr, nextPtr) {
			return currentPtr
		}
	}
}

// bumpAllocForShepherd allocates VA space in a target shepherd's address space
// for kernel-initiated mappings (IPC data pages, mmap page fill, mailbox,
// shared pages, writeback). Uses IPCBumpPointer starting at ipcDataVAStart
// (0x500000000000), separate from the shepherd's own BumpPointer/Go heap
// (0xC000000000) to prevent Go's MAP_FIXED arena management from creating
// span gaps that allow VA reuse.
//
//go:nosplit
func bumpAllocForShepherd(p *proc.Shepherd, size uint64) uint64 {
	if p == nil {
		return 0
	}

	pageSize := uint64(4096)
	aligned := (size + pageSize - 1) & ^(pageSize - 1)

	for {
		currentPtr := atomic.LoadUint64(&p.IPCBumpPointer)

		// Lazy initialization: first IPC allocation starts at ipcDataVAStart
		if currentPtr == 0 {
			atomic.CompareAndSwapUint64(&p.IPCBumpPointer, 0, ipcDataVAStart)
			continue
		}

		nextPtr := currentPtr + aligned

		// Check for wrap-around AND exceeding end
		if nextPtr < currentPtr || nextPtr > userMmapEnd {
			return 0 // Out of VA space
		}

		if atomic.CompareAndSwapUint64(&p.IPCBumpPointer, currentPtr, nextPtr) {
			// [MAZ-179 tier-13b probe] a kernel-initiated IPC-VA grant that
			// overlaps the target's existing spans is a double-grant into a
			// live range (callers Spans.Add right after, coalescing the
			// evidence). Same witness as addSpan, target-shepherd side.
			if ov := p.Spans.FindOverlapEnd(currentPtr, aligned); ov != 0 {
				if atomic.AddUint32(&proc.GrantOverlapTrips, 1) == 1 {
					atomic.StoreInt32(&proc.GrantOverlapFirstSID, int32(p.PID))
					atomic.StoreUint64(&proc.GrantOverlapFirstVA, currentPtr)
					atomic.StoreUint64(&proc.GrantOverlapFirstLen, aligned)
					atomic.StoreUint64(&proc.GrantOverlapFirstEnd, ov)
				}
			}
			return currentPtr
		}
	}
}

// ============================================================================
// MAZARIN_CONTIGUOUS: physically contiguous DMA pages
// ============================================================================

// syscallMmapContiguous allocates physically contiguous pages from the buddy
// allocator, pre-maps all PTEs (no demand paging), and registers the pages
// as a DMA clump for the calling shepherd. Returns the VA, or a negative
// errno on failure.
//
//go:nosplit
func syscallMmapContiguous(alignedLength uint64) int64 {
	p := proc.CurrentShepherd()
	if p == nil {
		return -1 // EPERM — only userspace can allocate DMA clumps
	}

	numPages := int(alignedLength / 4096)

	// Compute buddy order = ceil(log2(numPages))
	order := 0
	for (1 << uint(order)) < numPages {
		order++
	}
	if order >= kmem.MaxOrder {
		return -12 // ENOMEM — request too large
	}

	// The buddy allocator gives us 2^order pages, which may be more
	// than requested. We track the actual requested numPages but free
	// the full 2^order block when done.
	buddyPages := 1 << uint(order)

	// Allocate contiguous physical pages
	basePA := kmem.BuddyAllocTyped(order, kmem.PageUserDMA, int16(p.PID))
	if basePA == 0 {
		klog.Errf("[mmap] CONTIGUOUS: buddy alloc failed order=%x\n", uint64(order))
		return -12 // ENOMEM
	}

	// Allocate VA from the shepherd's bump allocator
	// We allocate the full buddy block size to keep VA alignment simple
	vaSize := uint64(buddyPages) * 4096
	baseVA := userBumpAlloc(vaSize)
	if baseVA == 0 {
		kmem.BuddyFreeTyped(basePA, order, kmem.PageUserDMA)
		return -12 // ENOMEM
	}
	addSpan(baseVA, vaSize)

	// Pre-map ALL pages (no demand paging for DMA pages)
	if !kmem.MapContiguousUserPages(int16(p.PID), p.PageTableL0PA,
		uintptr(baseVA), basePA, buddyPages) {
		kmem.BuddyFreeTyped(basePA, order, kmem.PageUserDMA)
		return -12 // ENOMEM — page table allocation failed
	}

	// Zero all pages before exposing to userspace.
	// The buddy allocator does not zero; pages contain stale data from
	// previous owners (kernel log strings, heap objects, etc.).
	kernelVA := basePA + constants.KernelVAOffset
	for i := 0; i < buddyPages; i++ {
		kmem.Bzero4K(kernelVA + uintptr(i)*4096)
	}

	// Register DMA clump for this shepherd
	clump := p.AddClump(basePA, uintptr(baseVA), numPages, order)
	if clump == nil {
		// Too many clumps — unmap and free
		kmem.BuddyFreeTyped(basePA, order, kmem.PageUserDMA)
		return -12 // ENOMEM
	}

	return int64(baseVA)
}

// ============================================================================
// Kernel bump allocator (for kmazarin's own heap, high memory via TTBR1)
// ============================================================================
// This is separate from userspace allocation. The kernel has a single global
// bump allocator since kmazarin is a single "process".

var kernelBumpPointer uint64 = 0
var kernelBumpInitialized uint32 // uint32 for atomic operations

//go:nosplit
func kernelBumpAlloc(size uint64) uint64 {
	// Lazy initialization from runtime config (atomic check)
	if atomic.LoadUint32(&kernelBumpInitialized) == 0 {
		// Use compare-and-swap to ensure only one thread initializes
		if atomic.CompareAndSwapUint32(&kernelBumpInitialized, 0, 1) {
			cfg := getRuntimeConfigTyped()
			atomic.StoreUint64(&kernelBumpPointer, uint64(cfg.KernelHeapStart))
		} else {
			// Another thread won the race, wait for it to complete initialization
			for atomic.LoadUint32(&kernelBumpInitialized) == 0 {
				// Spin wait
			}
		}
	}

	// Align to page boundary
	pageSize := uint64(4096)
	aligned := (size + pageSize - 1) & ^(pageSize - 1)

	// Get heap end for bounds checking
	cfg := getRuntimeConfigTyped()
	heapEnd := uint64(cfg.KernelHeapEnd)

	// Atomically allocate from bump pointer
	for {
		currentPtr := atomic.LoadUint64(&kernelBumpPointer)
		nextPtr := currentPtr + aligned

		// Check for wrap-around AND exceeding heap end
		if nextPtr < currentPtr || nextPtr > heapEnd {
			return 0 // Out of heap VA space or wrap-around
		}

		// Try to atomically update the bump pointer
		if atomic.CompareAndSwapUint64(&kernelBumpPointer, currentPtr, nextPtr) {
			// Successfully allocated
			return currentPtr
		}
		// CAS failed, another thread allocated, retry
	}
}

// runtimeConfigStruct is the full config view returned by main package.
// Layout MUST match main.fullConfig exactly.
type runtimeConfigStruct struct {
	KernelVAOffset               uint64
	KmazarinSize                 uint64
	KmazarinPhysAddr             uint64
	FramePoolStart               uint64
	FramePoolEnd                 uint64
	KernelPTPoolStart            uint64
	KernelPTPoolEnd              uint64
	KernelHeapStart              uint64
	KernelHeapEnd                uint64
	G0StackBottom                uint64
	G0StackTop                   uint64
	G0StackSize                  uint64
	ExceptionStackBottom         uint64
	ExceptionStackTop            uint64
	ExceptionStackSize           uint64
	_reservedFramebufferPhysAddr uint64 // dead padding; see main.fullConfig
	_reservedFramebufferSize     uint64 // dead padding; see main.fullConfig
	BootImagePhysAddr            uint64
	BootImageSize                uint64
	TotalRAMSize                 uint64
	RAMBaseAddr                  uint64
	UserspaceFramePoolStart      uint64
	UserspaceFramePoolEnd        uint64
	UserspacePTPoolStart         uint64
	UserspacePTPoolEnd           uint64
	DtbPhysAddr                  uint64
}

// getRuntimeConfigTyped returns the runtime config with proper type.
// CRITICAL: Must extract data pointer from interface, not address of interface!
//
// Go interface layout: {tab *itab, data unsafe.Pointer}
// We need the data pointer (second field), not the interface itself.
//
//go:nosplit
func getRuntimeConfigTyped() *runtimeConfigStruct {
	cfgInterface := getRuntimeConfig()

	// Extract data pointer from interface (second field at offset 8)
	type iface struct {
		tab  *byte
		data unsafe.Pointer
	}
	ifacePtr := (*iface)(unsafe.Pointer(&cfgInterface))
	return (*runtimeConfigStruct)(ifacePtr.data)
}
