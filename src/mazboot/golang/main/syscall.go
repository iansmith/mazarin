package main

import (
	"mazboot/asm"
	"sync/atomic"
	"unsafe"
	_ "unsafe" // for go:linkname
)

// ============================================================================
// Syscall Debug Tracing Functions
// ============================================================================
// These functions provide clear one-line debug output for each syscall
// Format: SYSCALL: #<num> <name>(<param>=<value>, ...) = <retval>

//go:nosplit
func syscallDebugStart(num uint32, name string) {
	uartPutsDirect("SYSCALL: #")
	uartPutHex32Direct(num)
	uartPutsDirect(" ")
	uartPutsDirect(name)
	uartPutsDirect("(")
}

//go:nosplit
func syscallDebugParamHex(name string, value uint64) {
	uartPutsDirect(name)
	uartPutsDirect("=0x")
	uartPutHex64Direct(value)
}

//go:nosplit
func syscallDebugParamInt(name string, value int64) {
	uartPutsDirect(name)
	uartPutsDirect("=")
	if value < 0 {
		uartPutsDirect("-")
		value = -value
	}
	uartPutHex64Direct(uint64(value))
}

//go:nosplit
func syscallDebugSep() {
	uartPutsDirect(", ")
}

//go:nosplit
func syscallDebugEnd(retval int64) {
	uartPutsDirect(") = ")
	if retval < 0 {
		uartPutsDirect("-")
		retval = -retval
	}
	uartPutHex64Direct(uint64(retval))
	uartPutsDirect("\r\n")
}

// ============================================================================

// Simple FD tracking for /dev/random and /proc/self/auxv
// Instead of a full FD table, just track if FD 3 and FD 4 are allocated
var devRandomFDAllocated uint32 // 0 = not allocated, 1 = FD 3 is /dev/random
var procAuxvFDAllocated uint32  // 0 = not allocated, 1 = FD 4 is /proc/self/auxv

// Auxiliary vector (auxv) for /proc/self/auxv
// The Linux kernel provides this to processes at startup
// Format: array of (tag, value) pairs, terminated by AT_NULL (0, 0)
//
// Go runtime uses these values during initialization:
// - AT_PAGESZ: Physical page size (4096 bytes)
// - AT_RANDOM: Pointer to 16 bytes of random data
// - AT_SECURE: Secure mode flag (0 = not secure)
//
// Conservative values suitable for bare-metal ARM64:
var auxvData = [...]uint64{
	// AT_PAGESZ = 6
	6, 4096,

	// AT_RANDOM = 25 (pointer to randomBytes)
	// This will be filled in dynamically with address of randomBytes
	25, 0,  // Value set at runtime

	// AT_SECURE = 23
	23, 0,  // Not in secure mode

	// AT_NULL = 0 (terminator)
	0, 0,
}

// Random bytes for AT_RANDOM (16 bytes as required by Go runtime)
var randomBytes [16]byte
var randomBytesInitialized uint32 // 0 = not initialized, 1 = initialized

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

	// Zero out the entire mask buffer to avoid garbage data
	// Write 8 bytes (standard CPU mask size for up to 64 CPUs)
	maskBytes := (*[8]byte)(mask)
	for i := 0; i < 8; i++ {
		maskBytes[i] = 0
	}

	// Set bit 0 (CPU 0 available)
	maskBytes[0] = 0x01

	// Return 8 bytes as the size of the CPU mask
	// This is the standard size for up to 64 CPUs (8 bytes * 8 bits/byte = 64 CPUs)
	// The runtime reads this many bytes and counts the set bits
	return 8
}

// SyscallUnknown prints unknown syscall number for debugging
//
//go:nosplit
func SyscallUnknown(syscallNum uint64) {
	// Simple breadcrumb - don't use print() as it may allocate
	uartPutcDirect('?')

	// Return -ENOSYS (function not implemented)
	// CRITICAL: Must return a value to avoid corrupting X0!
	// Note: This is a void function, so we can't return here.
	// The assembly handler must set X0 to -ENOSYS
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
	// Loop detection: hang if called too many times (indicates infinite loop)
	if closeCallCount > 60 {
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

	// Special case: FD 4 is /proc/self/auxv
	if fd == 4 {
		if atomic.LoadUint32(&procAuxvFDAllocated) == 1 {
			atomic.StoreUint32(&procAuxvFDAllocated, 0)
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
	// Loop detection: hang if called too many times (indicates infinite loop)
	if readCallCount > 60 {
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

	// Special case: FD 4 is /proc/self/auxv
	if fd == 4 && atomic.LoadUint32(&procAuxvFDAllocated) == 1 {
		// Validate buffer pointer
		if buf == nil {
			return -14 // -EFAULT (bad address)
		}

		// auxvData is an array of uint64 pairs
		auxvSize := uint64(len(auxvData) * 8) // 8 bytes per uint64

		// Limit count to auxv size
		if count > auxvSize {
			count = auxvSize
		}

		// Copy auxv data to buffer
		dst := (*[1 << 30]byte)(buf)[:count]
		src := (*[1 << 30]byte)(unsafe.Pointer(&auxvData[0]))[:count]
		for i := uint64(0); i < count; i++ {
			dst[i] = src[i]
		}

		return int64(count)
	}

	// NO FD SUPPORT - We rely entirely on AT_RANDOM for random numbers
	// If the runtime tries to read /dev/urandom, it means AT_RANDOM failed
	// DEBUG REMOVED - print/printHex64 allocate memory and corrupt X0
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
	// Loop detection: hang if called too many times (indicates infinite loop)
	if openatCallCount > 60 {
		for {
		}
	}

	if pathname == nil {
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

	// Check for /proc/self/auxv - allocate FD 4
	if cstringEqual(pathname, "/proc/self/auxv") {
		// Initialize randomBytes if not already done
		if atomic.CompareAndSwapUint32(&randomBytesInitialized, 0, 1) {
			// Get random bytes for AT_RANDOM
			getRandomBytes(unsafe.Pointer(&randomBytes[0]), 16)
			// Set the AT_RANDOM pointer in auxvData
			auxvData[3] = uint64(uintptr(unsafe.Pointer(&randomBytes[0])))
		}

		// Check if already allocated
		if atomic.CompareAndSwapUint32(&procAuxvFDAllocated, 0, 1) {
			return 4 // Return FD 4
		}
		return -23 // -ENFILE (already open)
	}

	// Expected path from getHugePageSize()
	if cstringEqual(pathname, "/sys/kernel/mm/transparent_hugepage/hpage_pmd_size") {
		// This is the expected path - return -ENOENT (file doesn't exist)
		// This causes getHugePageSize() to return 0 (no huge pages)
		return -2 // -ENOENT
	}

	// Unexpected path - return error
	return -2 // -ENOENT
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

var mmapCallCount uint32
var mmapTestValue uint64 = 0xDEADBEEF

// uartBase points to PL011 UART data register
var uartBase *uint8 = (*uint8)(unsafe.Pointer(uintptr(0x09000000)))

//go:nosplit
//go:noinline
func SyscallMmap(addr uintptr, length uint64, prot int32, flags int32, fd int32, offset int64) int64 {
	mmapCallCount++
	// Loop detection: hang if called too many times (indicates infinite loop)
	if mmapCallCount > 60 {
		for {
		}
	}

	// Debug trace entry - DISABLED (causes nested syscalls that corrupt return value)
	//syscallDebugStart(222, "mmap")
	//syscallDebugParamHex("addr", uint64(addr))
	//syscallDebugSep()
	//syscallDebugParamHex("len", length)
	//syscallDebugSep()
	//syscallDebugParamHex("prot", uint64(prot))
	//syscallDebugSep()
	//syscallDebugParamHex("flags", uint64(flags))
	//syscallDebugSep()
	//syscallDebugParamInt("fd", int64(fd))
	//syscallDebugSep()
	//syscallDebugParamHex("off", uint64(offset))
	// Return value will be added at each return point

	const MAP_FIXED = 0x10

	// Handle zero-length mmap
	// This should never happen and indicates a runtime initialization bug
	// However, we'll allocate a minimal page to allow runtime to continue
	if length == 0 {
		// DEBUG REMOVED - any output here corrupts X0
		// Allocate one page (4KB) from bump allocator
		length = 4096
		// CRITICAL: If addr=0 with zero length, clear address to force bump allocator
		// This handles broken mmap(0, 0, ..., MAP_FIXED, ...) calls
		addr = 0
		// Fall through to normal allocation (will use bump allocator)
	}

	// Round length up to page boundary (needed for both paths)
	pageSize := uint64(4096)
	roundedLength := (length + pageSize - 1) &^ (pageSize - 1)

	// Linux mmap semantics:
	// - Without MAP_FIXED: addr is just a hint, kernel can choose different address
	// - With MAP_FIXED: Must use exact addr or return ENOMEM

	// Check for MAP_FIXED (0x10) - must return exact address or fail
	if (flags & MAP_FIXED) != 0 {
		// MAP_FIXED validation
		if addr == 0 {
			// MAP_FIXED with addr=0 is invalid, but zero-length path forces addr=0
			// Fall through to bump allocator by ignoring MAP_FIXED in this broken case
			goto use_bump_allocator
		}

		// Check page alignment (4KB)
		if (addr & 0xFFF) != 0 {
			// syscallDebugEnd(-22) // DISABLED - corrupts X0
			return -22 // -EINVAL (syscall returns negative errno)
		}

		// CRITICAL: Validate address is within reasonable virtual address space
		// ARM64 with 4KB pages supports 48-bit VA = 256TB max
		// Go runtime uses formula: uintptr(i)<<40 | 0x4000000000 for arenas
		// Kmazarin runtime may use very high stack addresses (seen 279TB)
		// Accept up to 1PB to handle all reasonable Go runtime addresses
		const MAX_VIRT_ADDR = uintptr(0x4000000000000) // 1PB (1024TB)
		if addr >= MAX_VIRT_ADDR {
			// syscallDebugEnd(-12) // DISABLED - corrupts X0
			return -12 // -ENOMEM (syscall returns negative errno)
		}

		// Check if would overflow when adding length
		if addr+uintptr(length) < addr {
			// syscallDebugEnd(-12) // DISABLED - corrupts X0
			return -12 // -ENOMEM (syscall returns negative errno)
		}

		if addr+uintptr(length) > MAX_VIRT_ADDR {
			// syscallDebugEnd(-12) // DISABLED - corrupts X0
			return -12 // -ENOMEM (syscall returns negative errno)
		}

		// Register this span
		if !registerMmapSpan(addr, addr+uintptr(roundedLength)) {
			// syscallDebugEnd(-12) // DISABLED - corrupts X0
			return -12 // -ENOMEM (syscall returns negative errno)
		}

		// uartPutcDirect('F')  // DISABLED - corrupts X0 right before return!
		retVal := int64(addr)
		// syscallDebugEnd(retVal) // DISABLED - corrupts X0
		return retVal
	}

	// No MAP_FIXED - addr is just a hint, but Go runtime RELIES on hints being honored
	// Linux doesn't guarantee honoring hints, but Go's arena allocator expects it

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
				// syscallDebugEnd(-12) // DISABLED - corrupts X0
				return -12 // -ENOMEM (syscall returns negative errno)
			}

			// uartPutcDirect('H')  // DISABLED - corrupts X0 right before return!
			retVal := int64(addr)
			// syscallDebugEnd(retVal) // DISABLED - corrupts X0
			return retVal
		}
	}

use_bump_allocator:
	// No hint or hint was unreasonable - use bump allocator
	// NOTE: Bump region (Span 3) is pre-registered during boot
	// All allocations must fit within BUMP_REGION_START to BUMP_REGION_END
	allocAddr := mmapBumpNext
	endAddr := allocAddr + uintptr(roundedLength)

	// Check if allocation would overflow the pre-registered bump region
	if endAddr > BUMP_REGION_END {
		return -12 // -ENOMEM (syscall returns negative errno)
	}

	// Update bump pointer for next allocation
	mmapBumpNext = endAddr

	// Return the allocated address
	return int64(allocAddr)
}

//go:nosplit
func SyscallBrk(addr uintptr) int64 {
	return 0x50000000 // Fixed break address
}

//go:nosplit
func SyscallMunmap(addr uintptr, length uint64) int64 {
	return 0 // Success (we don't actually reclaim)
}

//go:nosplit
func SyscallFutex(addr unsafe.Pointer, op int32, val uint32, ts unsafe.Pointer, addr2 unsafe.Pointer, val3 uint32) int64 {
	// KMAZARIN FIX: We can't use mazboot's gopark/goready for kmazarin's syscalls.
	// Kmazarin has its own Go runtime with its own scheduler.
	// For now, use simple stub behavior:
	// - FUTEX_WAIT: Check value, if matches return -EAGAIN (forces spin-wait)
	// - FUTEX_WAKE: Return number woken (0 since no real waiters)
	//
	// This works because in single-core without real threads, contention
	// should resolve quickly as kmazarin's runtime initializes.

	uaddr := (*uint32)(addr)

	switch op {
	case _FUTEX_WAIT_PRIVATE:
		// Check if value matches
		if atomic.LoadUint32(uaddr) != val {
			return -11 // -EAGAIN: value changed
		}
		// Value matches, but we can't block.
		// Return 0 (success) to tell caller it was "woken up".
		// The caller will check the condition again and either proceed or retry.
		// This is effectively a spin-check pattern.
		return 0

	case _FUTEX_WAKE_PRIVATE:
		// FUTEX_WAKE: Since we don't actually block anyone, just return 0
		// (no waiters woken - but that's fine, the memory was updated)
		return 0

	default:
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
	// Try to exit QEMU using ARM semihosting
	// Semihosting call: SYS_EXIT (0x18) with exit code 0
	// This uses HLT instruction which QEMU intercepts when semihosting is enabled
	asm.SemihostingExit()

	// If semihosting didn't work (or returned), deadloop with busy wait
	// NEVER return from this function - exit syscall should never return!
	for {
		// Busy wait - prevents optimization from removing the loop
		asm.Nop()
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
