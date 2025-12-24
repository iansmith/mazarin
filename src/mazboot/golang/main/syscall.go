package main

import (
	"mazboot/asm"
	"sync/atomic"
	"unsafe"
	_ "unsafe" // for go:linkname
)

// Simple FD tracking for /dev/random
// Instead of a full FD table, just track if FD 3 is allocated for /dev/random
var devRandomFDAllocated uint32 // 0 = not allocated, 1 = FD 3 is /dev/random

// Runtime scheduler functions accessed via go:linkname
//
//go:linkname runtimeGopark runtime.gopark
//go:nosplit
func runtimeGopark(unlockf unsafe.Pointer, lock unsafe.Pointer, reason uint8, traceEv uint8, traceskip int)

//go:linkname runtimeGoready runtime.goready
//go:nosplit
func runtimeGoready(gp unsafe.Pointer, traceskip int)

// Futex operation constants
const (
	_FUTEX_WAIT         = 0
	_FUTEX_WAKE         = 1
	_FUTEX_PRIVATE_FLAG = 128
	_FUTEX_WAIT_PRIVATE = _FUTEX_WAIT | _FUTEX_PRIVATE_FLAG // 128
	_FUTEX_WAKE_PRIVATE = _FUTEX_WAKE | _FUTEX_PRIVATE_FLAG // 129
)

// Futex wait queue
const MAX_FUTEX_WAITERS = 64

type futexWaiter struct {
	addr uintptr // Address being waited on (0 = free slot)
	gp   uintptr // Goroutine pointer (g*), 0 = free
}

var futexWaiters [MAX_FUTEX_WAITERS]futexWaiter

// Track if futex is being used before scheduler is ready
var futexEarlyUseDetected uint32
var schedulerReady uint32  // Set to 1 after schedinit completes

// MarkSchedulerReady is called after schedinit completes to enable real gopark/goready
//
//go:nosplit
func MarkSchedulerReady() {
	atomic.StoreUint32(&schedulerReady, 1)
}

// SyscallSchedGetaffinity implements the sched_getaffinity syscall
// Returns CPU affinity mask for single-CPU bare-metal system
//
// Parameters:
//   - pid: Process ID (0 = current process, ignored on bare-metal)
//   - cpusetsize: Size of the mask buffer in bytes
//   - mask: Pointer to buffer where CPU mask is written
//
// Returns: Number of bytes written (8), or -errno on error
//
//go:nosplit
func SyscallSchedGetaffinity(pid int32, cpusetsize uint64, mask unsafe.Pointer) int64 {
	// Validate mask pointer
	if mask == nil {
		return -22 // -EINVAL
	}

	// We need at least 1 byte to write the CPU mask
	if cpusetsize < 1 {
		return -22 // -EINVAL (buffer too small)
	}

	// For single-CPU bare-metal system:
	// Set bit 0 to indicate CPU 0 is available
	// The runtime's getCPUCount() will count the bits to determine ncpu = 1
	//
	// CPU mask format (little-endian):
	//   byte 0: bit 0 = CPU 0, bit 1 = CPU 1, ..., bit 7 = CPU 7
	//   byte 1: bit 0 = CPU 8, bit 1 = CPU 9, ..., bit 7 = CPU 15
	//   etc.

	// Set bit 0 (CPU 0 available)
	*(*byte)(mask) = 0x01

	// Return 8 bytes as the size of the CPU mask
	// This is the standard size for up to 64 CPUs (8 bytes * 8 bits/byte = 64 CPUs)
	// The runtime reads this many bytes and counts the set bits
	return 8
}

// SyscallUnknown prints unknown syscall number for debugging
//
//go:nosplit
func SyscallUnknown(syscallNum uint64) {
	print("?(")
	printHex32(uint32(syscallNum))
	print(")")
}

// SyscallClose implements the close syscall
// Handles closing file descriptors (just returns success for now)
//
// Parameters:
//   - fd: File descriptor to close
//
// Returns: 0 on success, or -errno on error
//
var closeCallCount uint32

//go:nosplit
func SyscallClose(fd int32) int64 {
	closeCallCount++
	// Print breadcrumb for first few calls
	if closeCallCount <= 5 {
		const uartBase = uintptr(0x09000000)
		*(*uint32)(unsafe.Pointer(uartBase)) = 0x63  // 'c'
	}
	if closeCallCount % 100 == 0 {
		// Print progress every 100 calls
		const uartBase = uintptr(0x09000000)
		*(*uint32)(unsafe.Pointer(uartBase)) = 0x43  // 'C' (uppercase)
	}
	if closeCallCount > 10000 {
		print("\r\nCLOSE LOOP! (>10000 calls)\r\n")
		for {
		}
	}

	// Special case: FD 3 is /dev/random
	if fd == 3 {
		if atomic.LoadUint32(&devRandomFDAllocated) == 1 {
			atomic.StoreUint32(&devRandomFDAllocated, 0)
			return 0 // Success
		}
	}

	// For all other FDs, just return success (no-op)
	return 0
}

// SyscallRead implements the read syscall
// Handles special file descriptors like /dev/urandom (FD 3)
//
// Parameters:
//   - fd: File descriptor to read from
//   - buf: Buffer to read into
//   - count: Number of bytes to read
//
// Returns: Number of bytes read (>=0) on success, or -errno on error
//
var readCallCount uint32

//go:nosplit
func SyscallRead(fd int32, buf unsafe.Pointer, count uint64) int64 {
	readCallCount++
	// Print breadcrumb for first few calls
	if readCallCount <= 5 {
		const uartBase = uintptr(0x09000000)
		*(*uint32)(unsafe.Pointer(uartBase)) = 0x72  // 'r'
	}
	if readCallCount % 100 == 0 {
		// Print progress every 100 calls
		const uartBase = uintptr(0x09000000)
		*(*uint32)(unsafe.Pointer(uartBase)) = 0x52  // 'R' (uppercase)
	}
	if readCallCount > 10000 {
		print("\r\nREAD LOOP! (>10000 calls)\r\n")
		for {
		}
	}

	// Special case: FD 3 is /dev/random
	if fd == 3 && atomic.LoadUint32(&devRandomFDAllocated) == 1 {
		// Validate buffer pointer
		if buf == nil {
			return -14 // -EFAULT (bad address)
		}

		// Limit count to uint32 max
		if count > 0xFFFFFFFF {
			count = 0xFFFFFFFF
		}

		// Call VirtIO RNG to get random bytes
		bytesRead := getRandomBytes(buf, uint32(count))
		return int64(bytesRead)
	}

	// NO FD SUPPORT - We rely entirely on AT_RANDOM for random numbers
	// If the runtime tries to read /dev/urandom, it means AT_RANDOM failed
	print("read: unsupported fd=")
	printHex64(uint64(fd))
	print("\r\n")
	return -9 // -EBADF (bad file descriptor)
}

// SyscallOpenat implements the openat syscall
// Currently returns -ENOENT for all files (no filesystem support)
//
// This allows runtime functions like getHugePageSize() to gracefully fail
// when trying to read /sys/kernel/mm/transparent_hugepage/hpage_pmd_size
//
// Parameters:
//   - dirfd: Directory file descriptor (AT_FDCWD = -100 for absolute paths)
//   - pathname: Pointer to null-terminated path string
//   - flags: Open flags (O_RDONLY, O_WRONLY, etc.)
//   - mode: File creation mode (ignored if not creating)
//
// Returns: File descriptor (>=0) on success, or -errno on error
//
var openatCallCount uint32

//go:nosplit
func SyscallOpenat(dirfd int32, pathname unsafe.Pointer, flags int32, mode int32) int64 {
	openatCallCount++
	if openatCallCount % 100 == 0 {
		// Print progress every 100 calls
		const uartBase = uintptr(0x09000000)
		*(*uint32)(unsafe.Pointer(uartBase)) = 0x4F  // 'O'
	}
	if openatCallCount > 10000 {
		print("\r\nOPENAT LOOP! (>10000 calls)\r\n")
		for {
		}
	}

	if pathname == nil {
		print("openat: pathname is nil\r\n")
		return -14 // -EFAULT
	}

	// Check for /dev/random - allocate FD 3
	if cstringEqual(pathname, "/dev/random") {
		// Check if already allocated
		if atomic.CompareAndSwapUint32(&devRandomFDAllocated, 0, 1) {
			return 3 // Return FD 3
		}
		return -23 // -ENFILE (already open)
	}

	// Check for /dev/urandom
	// With AT_RANDOM support, the runtime should never try to open this
	// If it does, return ENOENT to indicate the file doesn't exist
	if cstringEqual(pathname, "/dev/urandom") {
		return -2 // -ENOENT (file not found)
	}

	// Expected path from getHugePageSize()
	if cstringEqual(pathname, "/sys/kernel/mm/transparent_hugepage/hpage_pmd_size") {
		// This is the expected path - return -ENOENT (file doesn't exist)
		// This causes getHugePageSize() to return 0 (no huge pages)
		return -2 // -ENOENT
	}

	// Unexpected path - print error and abort
	print("openat: UNEXPECTED PATH: ")
	printCString(pathname)
	print("\r\n")

	// Return error for unexpected path
	return -2 // -ENOENT for now, could panic if we want to be strict
}

// Helper function to compare null-terminated C string with Go string
//go:nosplit
func cstringEqual(cstr unsafe.Pointer, gostr string) bool {
	if cstr == nil {
		return false
	}
	p := (*byte)(cstr)
	for i := 0; i < len(gostr); i++ {
		if *p != gostr[i] {
			return false
		}
		p = (*byte)(unsafe.Pointer(uintptr(unsafe.Pointer(p)) + 1))
	}
	// Check for null terminator
	return *p == 0
}

// Helper function to print a null-terminated C string
//go:nosplit
func printCString(cstr unsafe.Pointer) {
	if cstr == nil {
		print("<nil>")
		return
	}
	p := (*byte)(cstr)
	// Limit to 256 chars to prevent runaway
	for i := 0; i < 256; i++ {
		if *p == 0 {
			break
		}
		print(string([]byte{*p}))
		p = (*byte)(unsafe.Pointer(uintptr(unsafe.Pointer(p)) + 1))
	}
}

// SyscallFutex implements the futex (fast userspace mutex) syscall
//
// This is the foundation for all synchronization in Go (locks, semaphores, etc.)
// The Go runtime calls this via runtime.futex() wrapper.
//
// Parameters:
//   - addr: Address of the futex word (uint32)
//   - op: Operation (_FUTEX_WAIT_PRIVATE or _FUTEX_WAKE_PRIVATE)
//   - val: Value for operation (expected value for WAIT, wake count for WAKE)
//   - ts: Timeout (not implemented yet, will be *timespec)
//   - addr2: Second address (for FUTEX_REQUEUE, not implemented)
//   - val3: Additional value (for FUTEX_CMP_REQUEUE, not implemented)
//
// Returns: 0 on success, -errno on error
//
// Bump allocator region for mmap with no hint
// This is a fixed 2GB region that is pre-registered as Span 3
// during boot (see preRegisterFixedSpans in kernel.go)
const (
	BUMP_REGION_START = uintptr(0x48000000)    // 1.125GB
	BUMP_REGION_SIZE  = uintptr(0x80000000)    // 2GB
	BUMP_REGION_END   = BUMP_REGION_START + BUMP_REGION_SIZE // 0xC8000000
)

var mmapBumpNext uintptr = BUMP_REGION_START

// Mmap span tracking - records which virtual address ranges have been mmap'd
// Used by page fault handler to validate that faulting addresses are legitimate
// (not ROM/Flash/device regions which should trigger errors)
//
// Span 0 is reserved for the kmazarin kernel's loaded region (code/data/bss)
// Spans 1-31 are available for Go runtime mmap() allocations
const MAX_MMAP_SPANS = 32

type mmapSpan struct {
	startVA uintptr // Start of virtual address range
	endVA   uintptr // End of virtual address range (exclusive)
	inUse   bool    // Whether this span slot is occupied
}

var mmapSpans [MAX_MMAP_SPANS]mmapSpan

// registerMmapSpan records a new mmap'd region
// Returns true on success, false if all spans are exhausted
//
//go:nosplit
func registerMmapSpan(startVA, endVA uintptr) bool {
	// Find first free span
	for i := 0; i < MAX_MMAP_SPANS; i++ {
		if !mmapSpans[i].inUse {
			mmapSpans[i].startVA = startVA
			mmapSpans[i].endVA = endVA
			mmapSpans[i].inUse = true

			uartPutsDirect("  -> registered span ")
			uartPutHex64Direct(uint64(i))
			uartPutsDirect(": VA 0x")
			uartPutHex64Direct(uint64(startVA))
			uartPutsDirect("-0x")
			uartPutHex64Direct(uint64(endVA))
			uartPutsDirect("\r\n")
			return true
		}
	}
	return false // All spans exhausted
}

// isInMmapSpan checks if a virtual address is within any registered mmap span
// Returns true if the address is valid (in a span), false otherwise
//
//go:nosplit
func isInMmapSpan(va uintptr) bool {
	for i := 0; i < MAX_MMAP_SPANS; i++ {
		if mmapSpans[i].inUse && va >= mmapSpans[i].startVA && va < mmapSpans[i].endVA {
			return true
		}
	}
	return false
}

//go:nosplit
func SyscallMmap(addr uintptr, length uint64, prot int32, flags int32, fd int32, offset int64) int64 {
	// Log mmap call using direct UART (safe in exception context)
	uartPutsDirect("mmap(addr=0x")
	uartPutHex64Direct(uint64(addr))
	uartPutsDirect(", len=0x")
	uartPutHex64Direct(length)
	uartPutsDirect(", prot=0x")
	uartPutHex64Direct(uint64(prot))
	uartPutsDirect(", flags=0x")
	uartPutHex64Direct(uint64(flags))
	uartPutsDirect(")\r\n")

	// Handle zero-length mmap
	// Linux allows this and returns a page-aligned address without actually allocating
	// The Go runtime expects a valid (non-NULL) address even for 0-length maps
	if length == 0 {
		// Return a valid dummy address that won't be dereferenced
		// Use a high address that's unlikely to conflict
		const ZERO_LENGTH_DUMMY = uintptr(0x1000) // Page-aligned dummy
		uartPutsDirect("  -> zero-length mmap, returning dummy address 0x1000\r\n")
		return int64(ZERO_LENGTH_DUMMY)
	}

	// Linux mmap semantics:
	// - Without MAP_FIXED: addr is just a hint, kernel can choose different address
	// - With MAP_FIXED: Must use exact addr or return ENOMEM

	// Check for MAP_FIXED (0x10) - must return exact address or fail
	const MAP_FIXED = 0x10
	if (flags & MAP_FIXED) != 0 {
		// MAP_FIXED validation
		if addr == 0 {
			uartPutsDirect("  -> MAP_FIXED with addr=0, returning -EINVAL\r\n")
			return -22 // -EINVAL
		}

		// Check page alignment (4KB)
		if (addr & 0xFFF) != 0 {
			uartPutsDirect("  -> MAP_FIXED unaligned addr, returning -EINVAL\r\n")
			return -22 // -EINVAL
		}

		// CRITICAL: Validate address is within reasonable virtual address space
		// ARM64 with 4KB pages supports 48-bit VA = 256TB max
		// Go runtime uses formula: uintptr(i)<<40 | 0x4000000000 for arenas
		// Kmazarin runtime may use very high stack addresses (seen 279TB)
		// Accept up to 1PB to handle all reasonable Go runtime addresses
		const MAX_VIRT_ADDR = uintptr(0x4000000000000) // 1PB (1024TB)
		if addr >= MAX_VIRT_ADDR {
			uartPutsDirect("  -> MAP_FIXED addr too high (>1PB), returning -ENOMEM\r\n")
			return -12 // -ENOMEM
		}

		// Check if would overflow when adding length
		if addr+uintptr(length) < addr {
			uartPutsDirect("  -> MAP_FIXED overflow, returning -ENOMEM\r\n")
			return -12 // -ENOMEM
		}

		if addr+uintptr(length) > MAX_VIRT_ADDR {
			uartPutsDirect("  -> MAP_FIXED range exceeds 1PB, returning -ENOMEM\r\n")
			return -12 // -ENOMEM
		}

		// Round length up to page boundary
		pageSize := uint64(4096)
		roundedLength := (length + pageSize - 1) &^ (pageSize - 1)

		// Register this span
		if !registerMmapSpan(addr, addr+uintptr(roundedLength)) {
			uartPutsDirect("  -> MAP_FIXED: all mmap spans exhausted, returning -ENOMEM\r\n")
			return -12 // -ENOMEM
		}

		uartPutsDirect("  -> MAP_FIXED, returning 0x")
		uartPutHex64Direct(uint64(addr))
		uartPutsDirect("\r\n")
		return int64(addr)
	}

	// No MAP_FIXED - addr is just a hint, but Go runtime RELIES on hints being honored
	// Linux doesn't guarantee honoring hints, but Go's arena allocator expects it
	pageSize := uint64(4096)
	roundedLength := (length + pageSize - 1) &^ (pageSize - 1)

	// If hint provided and reasonable, try to honor it
	if addr != 0 {
		// Validate hint is reasonable (same checks as MAP_FIXED but non-fatal)
		const MAX_VIRT_ADDR = uintptr(0x4000000000000) // 1PB (1024TB)
		if (addr&0xFFF) == 0 && // Page aligned
			addr < MAX_VIRT_ADDR && // Not too high
			addr+uintptr(roundedLength) >= addr && // No overflow
			addr+uintptr(roundedLength) <= MAX_VIRT_ADDR { // Range OK
			// Hint is reasonable - honor it to keep Go runtime happy

			// Register this span
			if !registerMmapSpan(addr, addr+uintptr(roundedLength)) {
				uartPutsDirect("  -> hint: all mmap spans exhausted, returning -ENOMEM\r\n")
				return -12 // -ENOMEM
			}

			uartPutsDirect("  -> honoring hint 0x")
			uartPutHex64Direct(uint64(addr))
			uartPutsDirect(" (len=0x")
			uartPutHex64Direct(roundedLength)
			uartPutsDirect(")\r\n")
			return int64(addr)
		}
		// Hint is unreasonable - fall through to bump allocator
		uartPutsDirect("  -> hint 0x")
		uartPutHex64Direct(uint64(addr))
		uartPutsDirect(" too high/invalid, using bump allocator\r\n")
	}

	// No hint or hint was unreasonable - use bump allocator
	// NOTE: Bump region (Span 3) is pre-registered during boot
	// All allocations must fit within BUMP_REGION_START to BUMP_REGION_END
	allocAddr := mmapBumpNext
	endAddr := allocAddr + uintptr(roundedLength)

	// Check if allocation would overflow the pre-registered bump region
	if endAddr > BUMP_REGION_END {
		uartPutsDirect("  -> bump allocator exhausted (would exceed 2GB region), returning -ENOMEM\r\n")
		return -12 // -ENOMEM
	}

	// Update bump pointer for next allocation
	mmapBumpNext = endAddr

	uartPutsDirect("  -> bump allocator, returning 0x")
	uartPutHex64Direct(uint64(allocAddr))
	uartPutsDirect(" (len=0x")
	uartPutHex64Direct(roundedLength)
	uartPutsDirect(", within pre-registered Span 3)\r\n")

	return int64(allocAddr)
}

//go:nosplit
func SyscallBrk(addr uintptr) int64 {
	uartPutsDirect("brk(0x")
	uartPutHex64Direct(uint64(addr))
	uartPutsDirect(") -> 0x50000000\r\n")
	return 0x50000000 // Fixed break address
}

//go:nosplit
func SyscallMunmap(addr uintptr, length uint64) int64 {
	uartPutsDirect("munmap(0x")
	uartPutHex64Direct(uint64(addr))
	uartPutsDirect(", 0x")
	uartPutHex64Direct(length)
	uartPutsDirect(") -> 0\r\n")
	return 0 // Success (we don't actually reclaim)
}

//go:nosplit
func SyscallFutex(addr unsafe.Pointer, op int32, val uint32, ts unsafe.Pointer, addr2 unsafe.Pointer, val3 uint32) int64 {
	uaddr := (*uint32)(addr)
	addrVal := uintptr(addr)

	// Early-use detection: Log if futex is used before scheduler is ready
	if atomic.LoadUint32(&schedulerReady) == 0 {
		if atomic.CompareAndSwapUint32(&futexEarlyUseDetected, 0, 1) {
			print("FUTEX: Early use detected (before scheduler ready) - op=")
			printHex32(uint32(op))
			print(" addr=")
			printHex64(uint64(addrVal))
			print("\r\n")
		}
	}

	switch op {
	case _FUTEX_WAIT_PRIVATE:
		// FUTEX_WAIT: Atomically check if *addr == val, and if so, sleep until woken
		//
		// The atomic check is CRITICAL to prevent lost wakeup:
		//   1. Thread A checks lock, sees it's taken
		//   2. Thread B releases lock, calls FUTEX_WAKE (but A not waiting yet)
		//   3. Thread A calls FUTEX_WAIT (missed the wakeup - deadlock!)
		//
		// With atomic check:
		//   1. Thread A calls FUTEX_WAIT(val=1)
		//   2. Thread B changes *addr to 0 and calls FUTEX_WAKE
		//   3. Thread A's FUTEX_WAIT sees *addr != val, returns -EAGAIN (no sleep)

		// DEBUG: Log futex WAIT calls during schedinit
		print("F")

		// Step 1: Atomic check (CRITICAL)
		if atomic.LoadUint32(uaddr) != val {
			print("E") // E = EAGAIN (value mismatch)
			return -11 // -EAGAIN: value changed, don't sleep
		}

		// Step 2: Get current goroutine and allocate wait slot
		gp := asm.GetCurrentG()
		if gp == 0 {
			print("FUTEX: GetCurrentG() returned 0\r\n")
			return -11 // -EAGAIN
		}

		mySlot := allocateFutexWaitSlotWithG(addrVal, gp)
		if mySlot < 0 {
			print("FUTEX: No free wait slots (max ")
			printHex32(MAX_FUTEX_WAITERS)
			print(" waiters)\r\n")
			return -11 // -EAGAIN: no free slots
		}

		// Step 3: Park goroutine (suspend until FUTEX_WAKE)
		//
		// IMPORTANT: During early bootstrap (before schedinit completes), we can't
		// actually block because there's only g0 and no other runnable goroutines.
		// Use stub behavior (return immediately) until scheduler is fully initialized.
		if atomic.LoadUint32(&schedulerReady) == 0 {
			// Scheduler not ready - use stub behavior (don't actually park)
			print("R") // R = Returned immediately (stub)
			freeFutexWaitSlot(mySlot)
			return 0
		}

		// Scheduler is ready - use real gopark
		print("P") // P = Parking
		runtimeGopark(nil, unsafe.Pointer(&futexWaiters[mySlot]), 0, 0, 0)

		// Step 4: Woken up - clean up wait slot
		print("W") // W = Woken
		freeFutexWaitSlot(mySlot)
		return 0

	case _FUTEX_WAKE_PRIVATE:
		// FUTEX_WAKE: Wake up to 'val' goroutines waiting on this address
		print("w") // w = FUTEX_WAKE (lowercase to distinguish from W=woken)

		woken := 0
		for i := 0; i < MAX_FUTEX_WAITERS && woken < int(val); i++ {
			if atomic.LoadUintptr(&futexWaiters[i].addr) == addrVal {
				// Found a waiter on this address - wake it up
				gp := atomic.LoadUintptr(&futexWaiters[i].gp)
				if gp != 0 {
					// Clear the slot before waking (the goroutine will clean up when it resumes)
					atomic.StoreUintptr(&futexWaiters[i].gp, 0)
					atomic.StoreUintptr(&futexWaiters[i].addr, 0)

					// Wake the goroutine (mark as runnable and add to run queue)
					runtimeGoready(unsafe.Pointer(gp), 0)
					woken++
				}
			}
		}

		return int64(woken)

	default:
		print("FUTEX: Unsupported operation: ")
		printHex32(uint32(op))
		print("\r\n")
		return -22 // -EINVAL
	}
}

// allocateFutexWaitSlot finds a free wait slot and claims it atomically
// Returns slot index, or -1 if no free slots
//
//go:nosplit
func allocateFutexWaitSlot(addr uintptr) int {
	for i := 0; i < MAX_FUTEX_WAITERS; i++ {
		// Try to claim free slot (addr == 0) atomically
		if atomic.CompareAndSwapUintptr(&futexWaiters[i].addr, 0, addr) {
			// Successfully claimed slot
			// Set gp to non-zero to indicate "waiting"
			// (actual gp pointer will be set when we integrate with scheduler)
			atomic.StoreUintptr(&futexWaiters[i].gp, 1)
			return i
		}
	}
	return -1 // No free slots
}

// allocateFutexWaitSlotWithG finds a free wait slot and claims it atomically,
// storing both the address and the goroutine pointer
// Returns slot index, or -1 if no free slots
//
//go:nosplit
func allocateFutexWaitSlotWithG(addr uintptr, gp uintptr) int {
	for i := 0; i < MAX_FUTEX_WAITERS; i++ {
		// Try to claim free slot (addr == 0) atomically
		if atomic.CompareAndSwapUintptr(&futexWaiters[i].addr, 0, addr) {
			// Successfully claimed slot - store the goroutine pointer
			atomic.StoreUintptr(&futexWaiters[i].gp, gp)
			return i
		}
	}
	return -1 // No free slots
}

// freeFutexWaitSlot releases a wait slot
//
//go:nosplit
func freeFutexWaitSlot(slot int) {
	atomic.StoreUintptr(&futexWaiters[slot].gp, 0)
	atomic.StoreUintptr(&futexWaiters[slot].addr, 0)
}

// =============================================================================
// Scheduler Integration: Real gopark/goready Implementation
// =============================================================================
//
// The futex syscall now uses the runtime's real gopark/goready functions to
// properly suspend and resume goroutines. This allows full lock synchronization.
//
// Prerequisites:
// - Scheduler bootstrap must complete before schedinit (see scheduler_bootstrap.go)
// - g0, m0, and P structures must be initialized
// - x28 register must point to g0
//
// How it works:
// - FUTEX_WAIT: Calls runtime.gopark to suspend current goroutine
// - FUTEX_WAKE: Calls runtime.goready to wake parked goroutines
//
// This enables proper lock behavior during runtime initialization:
// - schedinit → lockInit → lock acquisition → futex WAIT → gopark (real blocking) ✓
// - Other goroutine → lock release → futex WAKE → goready (real wakeup) ✓
// =============================================================================

// Stub syscall implementations for functions referenced by assembly but not yet implemented

//go:nosplit
func SyscallClockGettime() int64 {
	// TODO: Implement clock_gettime syscall
	return 0
}

//go:nosplit
func SyscallExit() {
	print("SyscallExit called\r\n")
	for {
	}
}

//go:nosplit
func SyscallKill() int64 {
	// TODO: Implement kill syscall
	return 0
}

//go:nosplit
func SyscallRtSigaction() int64 {
	// TODO: Implement rt_sigaction syscall
	return 0
}

//go:nosplit
func SyscallRtSigprocmask() int64 {
	// TODO: Implement rt_sigprocmask syscall
	return 0
}

//go:nosplit
func SyscallTgkill() int64 {
	// TODO: Implement tgkill syscall
	return 0
}

//go:nosplit
func SyscallTkill() int64 {
	// TODO: Implement tkill syscall
	return 0
}
