package main

import (
	"cardinal/asm"
	"sync/atomic"
	"unsafe"
	_ "unsafe" // for go:linkname
)

// External symbols for pending thread (set by clone syscall in assembly)
//
//go:linkname pending_thread_valid pending_thread_valid
var pending_thread_valid uint32

//go:linkname pending_thread_stack pending_thread_stack
var pending_thread_stack uintptr

//go:linkname pending_thread_mp pending_thread_mp
var pending_thread_mp uintptr

//go:linkname pending_thread_gp pending_thread_gp
var pending_thread_gp uintptr

//go:linkname pending_thread_fn pending_thread_fn
var pending_thread_fn uintptr

// Debug counter for timer state prints
var timerDebugCounter uint64

// runPendingThread is DEFERRED - we don't run mstart immediately.
// The user's insight: "if we can just get the program to start (finish runtime
// initialization) we will be fine because we can cause the sysmon thread to run
// whenever we want" via timer/channel mechanism.
//
// The pending thread data is captured by clone syscall and stored here.
// It will be used later once the scheduler is running to spawn the actual thread.
//
//go:nosplit
func runPendingThread() {
	// DEFERRED: Don't run mstart yet - let runtime initialization complete first.
	// The timer interrupt handler will run pending threads via goroutine/channel.
}

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
	// Print clear error message
	uartPutsDirect("Unknown System Call ")
	uartPutHex64Direct(syscallNum)
	uartPutsDirect("\r\n")

	// Call exit to halt the system - unknown syscalls are fatal
	SyscallExit()

	// Never returns - SyscallExit halts the system
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

// verifyMmapSpansMapped checks that the mmapSpans array is properly mapped
// Called by VerifyCriticalGlobalsMapped() in mmu.go
func verifyMmapSpansMapped() bool {
	mmapSpansAddr := uintptr(unsafe.Pointer(&mmapSpans[0]))
	pa := getPhysicalAddress(mmapSpansAddr)
	print("  mmapSpans array at VA=0x")
	printHex64ForVerify(uint64(mmapSpansAddr))
	if pa != 0 {
		print(" -> PA=0x")
		printHex64ForVerify(uint64(pa))
		print(" OK\r\n")
		return true
	} else {
		print(" -> NOT MAPPED! FATAL\r\n")
		return false
	}
}

// printHex64ForVerify is a local hex printer for verification
// (avoids dependency on mmu.go's printHex64)
func printHex64ForVerify(v uint64) {
	const digits = "0123456789abcdef"
	var buf [16]byte
	for i := 15; i >= 0; i-- {
		buf[i] = digits[v&0xF]
		v >>= 4
	}
	print(string(buf[:]))
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

	// Compact debug: A=sysAlloc, R=sysReserve, C=sysMap(commit)
	const MAP_FIXED_LOCAL = 0x10
	if addr == 0 && (flags & MAP_FIXED_LOCAL) == 0 {
		uartPutcDirect('A')  // Alloc from anywhere
	} else if (flags & MAP_FIXED_LOCAL) != 0 {
		uartPutcDirect('C')  // Commit at fixed addr
	} else {
		uartPutcDirect('R')  // Reserve with hint
	}
	uartPutHex64Direct(uint64(addr))
	uartPutcDirect('/')
	uartPutHex64Direct(length)
	uartPutcDirect(' ')

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
			uartPutsDirect("FIX_SPAN_FULL!")
			return -12 // -ENOMEM (syscall returns negative errno)
		}

		// Return the exact address
		uartPutcDirect('F')
		uartPutHex64Direct(uint64(addr))
		uartPutcDirect('\n')
		return int64(addr)
	}

	// No MAP_FIXED - addr is just a hint, but Go runtime RELIES on hints being honored
	// Linux doesn't guarantee honoring hints, but Go's arena allocator expects it

	// If hint provided and reasonable, try to honor it
	if addr != 0 {
		// Validate hint is reasonable
		// CRITICAL INSIGHT: L0 entry 0 covers the first 512GB (2^39 bytes).
		// Any address < 512GB can be mapped on demand because:
		//   - L0 index 0 is valid (set in initMMU)
		//   - L1/L2/L3 entries are allocated on demand in mapPage()
		// Go's arena allocator uses addresses like 0x4000000000 (256GB), which
		// is well within the 512GB L0 coverage.
		const MAX_MAPPABLE_ADDR = uintptr(0x8000000000) // 512GB (L0 entry 0 coverage)
		if (addr&0xFFF) == 0 && // Page aligned
			addr+uintptr(roundedLength) >= addr && // No overflow
			addr+uintptr(roundedLength) <= MAX_MAPPABLE_ADDR { // Range OK
			// Hint is within mappable range - honor it

			// Register this span for demand paging
			if !registerMmapSpan(addr, addr+uintptr(roundedLength)) {
				uartPutsDirect("SPAN_FULL!")
				return -12 // -ENOMEM (syscall returns negative errno)
			}

			// Return the requested address
			uartPutcDirect('=')
			uartPutHex64Direct(uint64(addr))
			uartPutcDirect('\n')
			return int64(addr)
		}
		// Hint is outside mappable range (> 512GB) - fall through to bump allocator
	}

use_bump_allocator:
	// No hint or hint was unreasonable - use bump allocator
	// NOTE: Bump region (Span 3) is pre-registered during boot
	// All allocations must fit within BUMP_REGION_START to BUMP_REGION_END
	allocAddr := mmapBumpNext
	endAddr := allocAddr + uintptr(roundedLength)

	// Check if allocation would overflow the pre-registered bump region
	if endAddr > BUMP_REGION_END {
		uartPutsDirect("BUMP_OOM!")
		return -12 // -ENOMEM (syscall returns negative errno)
	}

	// Update bump pointer for next allocation
	mmapBumpNext = endAddr

	// Return the allocated address
	uartPutcDirect('B')
	uartPutHex64Direct(uint64(allocAddr))
	uartPutcDirect('\n')
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
	// KMAZARIN FIX: Since we can't actually create separate threads, we need
	// to fake the "wake" that would come from mstart running.
	//
	// STRATEGY: When FUTEX_WAIT is called and we have a pending thread from
	// clone(), we simulate the notewakeup by setting the value to 1.
	// This allows the runtime to continue past notesleep.

	uaddr := (*uint32)(addr)

	switch op {
	case _FUTEX_WAIT_PRIVATE:
		// Check if value matches the expected value (what caller is waiting for)
		currentVal := atomic.LoadUint32(uaddr)
		if currentVal != val {
			return -11 // -EAGAIN: value already changed
		}

		// Check if there's a pending thread that should have woken us
		// If so, simulate notewakeup by setting the value to 1
		if atomic.LoadUint32(&pending_thread_valid) != 0 {
			// The pending thread would have called notewakeup which:
			// 1. Sets the note value to 1
			// 2. Calls futex WAKE
			// We simulate this by setting the value to 1
			atomic.StoreUint32(uaddr, 1)
			// Mark that we've "run" the pending thread for this wakeup
			// Keep pending_thread_valid set - we may need to track it later
		} else {
			// No pending thread, but still simulate wake to avoid spinning
			// This handles other futex waits that aren't related to clone
			atomic.StoreUint32(uaddr, 1)
		}

		// Return 0 (success) to tell caller it was "woken up".
		return 0

	case _FUTEX_WAKE_PRIVATE:
		// FUTEX_WAKE: Since we don't actually block anyone, just return 0
		// (This means "0 waiters woken" which is fine)
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

// ============================================================================
// Syscall handlers for functions previously inlined in assembly
// ============================================================================
// These functions ensure ALL syscalls go through Go for consistent handling.
// The assembly just sets up arguments and calls these functions.

// SyscallGetpid returns the process ID
// In bare-metal, we always return 1 (init process)
//
//go:nosplit
func SyscallGetpid() int64 {
	return 1
}

// SyscallGettid returns the thread ID
// In bare-metal, we always return 1 (main thread)
//
//go:nosplit
func SyscallGettid() int64 {
	return 1
}

// SyscallMadvise gives advice about memory usage
// We don't actually do anything with the advice, just return success
//
// Parameters:
//   - addr: Start address of memory region
//   - length: Length of memory region
//   - advice: Advice type (MADV_DONTNEED, etc.)
//
//go:nosplit
func SyscallMadvise(addr uintptr, length uint64, advice int32) int64 {
	return 0 // Success
}

// SyscallPrctl performs process control operations
// We return -EINVAL so setVMAName marks it unsupported and stops calling
//
// Parameters:
//   - option: Operation to perform
//   - arg2-arg5: Operation-specific arguments
//
//go:nosplit
func SyscallPrctl(option int32, arg2, arg3, arg4, arg5 uint64) int64 {
	return -22 // -EINVAL
}

// SyscallIoSetup sets up async I/O context
// We don't support async I/O, return -ENOSYS
//
// Parameters:
//   - nrEvents: Maximum number of events
//   - ctxp: Pointer to context ID
//
//go:nosplit
func SyscallIoSetup(nrEvents uint32, ctxp unsafe.Pointer) int64 {
	return -38 // -ENOSYS
}

// SyscallEventfd creates an event file descriptor
// Returns a fake eventfd (11) so the runtime thinks it succeeded
//
// Parameters:
//   - initval: Initial counter value
//   - flags: EFD_CLOEXEC, EFD_NONBLOCK, EFD_SEMAPHORE
//
//go:nosplit
func SyscallEventfd(initval uint32, flags int32) int64 {
	return 11 // Fake eventfd
}

// SyscallEpollCreate creates an epoll file descriptor
// Returns a fake epoll fd (10) so the runtime thinks it succeeded
//
// Parameters:
//   - flags: EPOLL_CLOEXEC
//
//go:nosplit
func SyscallEpollCreate(flags int32) int64 {
	return 10 // Fake epoll fd
}

// SyscallEpollCtl controls an epoll file descriptor
// Just returns success (0)
//
// Parameters:
//   - epfd: Epoll file descriptor
//   - op: Operation (EPOLL_CTL_ADD, EPOLL_CTL_MOD, EPOLL_CTL_DEL)
//   - fd: Target file descriptor
//   - event: Event structure pointer
//
//go:nosplit
func SyscallEpollCtl(epfd int32, op int32, fd int32, event unsafe.Pointer) int64 {
	return 0 // Success
}

// SyscallEpollPwait waits for events on an epoll file descriptor
// Returns 0 (no events) - this tells the runtime nothing is ready
//
// Parameters:
//   - epfd: Epoll file descriptor
//   - events: Buffer for returned events
//   - maxevents: Maximum number of events to return
//   - timeout: Timeout in milliseconds (-1 = infinite)
//   - sigmask: Signal mask
//   - sigsetsize: Size of signal mask
//
//go:nosplit
func SyscallEpollPwait(epfd int32, events unsafe.Pointer, maxevents int32, timeout int32, sigmask unsafe.Pointer, sigsetsize uint64) int64 {
	return 0 // No events
}

// SyscallFcntl performs file control operations
// Go runtime calls fcntl(fd, F_GETFD) to verify stdin/stdout/stderr are valid
//
// Parameters:
//   - fd: File descriptor
//   - cmd: Command (F_GETFD=1, F_SETFD=2, etc.)
//   - arg: Command-specific argument
//
//go:nosplit
func SyscallFcntl(fd int32, cmd int32, arg int64) int64 {
	// F_GETFD = 1: Get file descriptor flags
	if cmd == 1 && fd <= 2 {
		return 0 // No flags set, fd is valid
	}
	return -38 // -ENOSYS for anything else
}

// SyscallWrite writes data to a file descriptor
// Handles stdout/stderr by writing to UART, other fds return count
//
// Parameters:
//   - fd: File descriptor (1=stdout, 2=stderr)
//   - buf: Buffer to write
//   - count: Number of bytes to write
//
//go:nosplit
func SyscallWrite(fd int32, buf unsafe.Pointer, count uint64) int64 {
	if fd == 1 || fd == 2 {
		// Use direct UART output for writes from kmazarin
		// The ring buffer+interrupt mechanism isn't reliable across address spaces
		return int64(SyscallWriteDirect(buf, uint32(count)))
	}
	// For other fds, pretend we wrote all bytes
	return int64(count)
}

// SyscallSigaltstack sets up an alternate signal stack
// We don't support signals, just return success
//
// Parameters:
//   - ss: New signal stack (can be nil)
//   - oldSs: Previous signal stack buffer (can be nil)
//
//go:nosplit
func SyscallSigaltstack(ss unsafe.Pointer, oldSs unsafe.Pointer) int64 {
	return 0 // Success
}

// SyscallSchedSetaffinity sets CPU affinity for a process
// We only have one CPU, just return success
//
// Parameters:
//   - pid: Process ID (0 = current)
//   - cpusetsize: Size of mask buffer
//   - mask: CPU mask pointer
//
//go:nosplit
func SyscallSchedSetaffinity(pid int32, cpusetsize uint64, mask unsafe.Pointer) int64 {
	return 0 // Success
}

// SyscallSchedYield yields the processor
// In bare-metal with one CPU, this is a no-op
//
//go:nosplit
func SyscallSchedYield() int64 {
	return 0 // Success
}

// SyscallMprotect changes memory protection
// We don't enforce memory protection, just return success
//
// Parameters:
//   - addr: Start address (must be page-aligned)
//   - length: Length of region
//   - prot: Protection flags (PROT_READ, PROT_WRITE, PROT_EXEC)
//
//go:nosplit
func SyscallMprotect(addr uintptr, length uint64, prot int32) int64 {
	return 0 // Success
}

// SyscallPrlimit64 gets/sets resource limits
// We don't enforce resource limits, just return success
//
// Parameters:
//   - pid: Process ID (0 = current)
//   - resource: Resource type (RLIMIT_STACK, etc.)
//   - newLimit: New limit (can be nil)
//   - oldLimit: Buffer for old limit (can be nil)
//
//go:nosplit
func SyscallPrlimit64(pid int32, resource int32, newLimit unsafe.Pointer, oldLimit unsafe.Pointer) int64 {
	return 0 // Success
}

// ============================================================================
// Syscall Dispatch - Central entry point for all syscalls
// ============================================================================

// Linux ARM64 syscall numbers
const (
	SYS_io_setup          = 0
	SYS_eventfd2          = 19
	SYS_epoll_create1     = 20
	SYS_epoll_ctl         = 21
	SYS_epoll_pwait       = 22
	SYS_fcntl             = 25
	SYS_openat            = 56
	SYS_close             = 57
	SYS_read              = 63
	SYS_write             = 64
	SYS_exit              = 93
	SYS_exit_group        = 94
	SYS_futex             = 98
	SYS_nanosleep         = 101
	SYS_clock_gettime     = 113
	SYS_sched_getaffinity = 123
	SYS_sched_yield       = 124
	SYS_kill              = 129
	SYS_tkill             = 130
	SYS_tgkill            = 131
	SYS_sigaltstack       = 132
	SYS_rt_sigaction      = 134
	SYS_rt_sigprocmask    = 135
	SYS_prctl             = 167
	SYS_getpid            = 172
	SYS_gettid            = 178
	SYS_sched_setaffinity = 204
	SYS_brk               = 214
	SYS_munmap            = 215
	SYS_clone             = 220
	SYS_mmap              = 222
	SYS_mprotect          = 226
	SYS_madvise           = 233
	SYS_prlimit64         = 261
	SYS_getrandom         = 278
)

// SyscallDispatch is the central entry point for all syscalls.
// It takes the syscall number and arguments, dispatches to the appropriate
// handler, and returns the result.
//
// Parameters:
//   - num: Syscall number (from x8)
//   - arg0-arg5: Syscall arguments (from x0-x5)
//   - framePtr: Pointer to exception frame (for context switching)
//
// Returns:
//   - result: Syscall return value (goes in x0)
//   - switchTo: Thread index to switch to (-1 = no switch)
//
//go:nosplit
func SyscallDispatch(num int64, arg0, arg1, arg2, arg3, arg4, arg5 uint64, framePtr uintptr) (result int64, switchTo int32) {
	switchTo = -1 // Default: no context switch

	switch num {
	// Simple syscalls - just return a constant
	case SYS_getpid:
		return SyscallGetpid(), -1
	case SYS_gettid:
		return SyscallGettid(), -1
	case SYS_sched_yield:
		return SyscallSchedYield(), -1

	// Success stubs
	case SYS_sigaltstack:
		return SyscallSigaltstack(unsafe.Pointer(uintptr(arg0)), unsafe.Pointer(uintptr(arg1))), -1
	case SYS_sched_setaffinity:
		return SyscallSchedSetaffinity(int32(arg0), arg1, unsafe.Pointer(uintptr(arg2))), -1
	case SYS_prctl:
		return SyscallPrctl(int32(arg0), arg1, arg2, arg3, arg4), -1
	case SYS_mprotect:
		return SyscallMprotect(uintptr(arg0), arg1, int32(arg2)), -1
	case SYS_prlimit64:
		return SyscallPrlimit64(int32(arg0), int32(arg1), unsafe.Pointer(uintptr(arg2)), unsafe.Pointer(uintptr(arg3))), -1

	// I/O syscalls
	case SYS_io_setup:
		return SyscallIoSetup(uint32(arg0), unsafe.Pointer(uintptr(arg1))), -1
	case SYS_eventfd2:
		return SyscallEventfd(uint32(arg0), int32(arg1)), -1
	case SYS_epoll_create1:
		return SyscallEpollCreate(int32(arg0)), -1
	case SYS_epoll_ctl:
		return SyscallEpollCtl(int32(arg0), int32(arg1), int32(arg2), unsafe.Pointer(uintptr(arg3))), -1
	case SYS_epoll_pwait:
		return SyscallEpollPwait(int32(arg0), unsafe.Pointer(uintptr(arg1)), int32(arg2), int32(arg3), unsafe.Pointer(uintptr(arg4)), arg5), -1
	case SYS_fcntl:
		return SyscallFcntl(int32(arg0), int32(arg1), int64(arg2)), -1

	// File syscalls
	case SYS_write:
		return SyscallWrite(int32(arg0), unsafe.Pointer(uintptr(arg1)), arg2), -1
	case SYS_read:
		return SyscallRead(int32(arg0), unsafe.Pointer(uintptr(arg1)), arg2), -1
	case SYS_openat:
		return SyscallOpenat(int32(arg0), unsafe.Pointer(uintptr(arg1)), int32(arg2), int32(arg3)), -1
	case SYS_close:
		return SyscallClose(int32(arg0)), -1

	// Memory syscalls
	case SYS_mmap:
		return SyscallMmap(uintptr(arg0), arg1, int32(arg2), int32(arg3), int32(arg4), int64(arg5)), -1
	case SYS_munmap:
		return SyscallMunmap(uintptr(arg0), arg1), -1
	case SYS_brk:
		return SyscallBrk(uintptr(arg0)), -1
	case SYS_madvise:
		return SyscallMadvise(uintptr(arg0), arg1, int32(arg2)), -1

	// Signal syscalls - these are stubs with no parameters
	case SYS_rt_sigaction:
		return SyscallRtSigaction(), -1
	case SYS_rt_sigprocmask:
		return SyscallRtSigprocmask(), -1
	case SYS_kill:
		return SyscallKill(), -1
	case SYS_tkill:
		return SyscallTkill(), -1
	case SYS_tgkill:
		return SyscallTgkill(), -1

	// Time syscalls - stub with no parameters
	case SYS_clock_gettime:
		return SyscallClockGettime(), -1

	// Scheduling syscalls
	case SYS_sched_getaffinity:
		return SyscallSchedGetaffinity(int32(arg0), arg1, unsafe.Pointer(uintptr(arg2))), -1

	// Random syscalls
	case SYS_getrandom:
		getRandomBytes(unsafe.Pointer(uintptr(arg0)), uint32(arg1))
		return int64(arg1), -1

	// Exit syscalls - stub with no parameters
	case SYS_exit, SYS_exit_group:
		SyscallExit()
		// Never returns, but compiler needs a return
		return 0, -1

	// Complex syscalls that may need context switching
	case SYS_futex:
		result, switchTo = SyscallFutexHandler(arg0, int32(arg1), uint32(arg2))
		return result, switchTo

	case SYS_nanosleep:
		// Read timespec from pointer
		reqPtr := unsafe.Pointer(uintptr(arg0))
		tvSec := *(*uint64)(reqPtr)
		tvNsec := *(*uint64)(unsafe.Pointer(uintptr(reqPtr) + 8))
		result, switchTo = SyscallNanosleepHandler(tvSec, tvNsec)
		return result, switchTo

	case SYS_clone:
		// Extract stack, mp, gp, fn from clone args
		stack := arg1
		stackPtr := unsafe.Pointer(uintptr(stack))
		gp := *(*uint64)(unsafe.Pointer(uintptr(stackPtr) - 16))
		mp := *(*uint64)(unsafe.Pointer(uintptr(stackPtr) - 8))
		fn := *(*uint64)(unsafe.Pointer(uintptr(stackPtr) - 24))
		result, switchTo = SyscallCloneHandler(stack, fn, mp, gp)
		return result, switchTo

	default:
		// Unknown syscall
		SyscallUnknown(uint64(num))
		return -38, -1 // -ENOSYS
	}
}

// ============================================================================
// Unified Synchronous Exception Dispatcher
// ============================================================================
// syncExceptionDispatchInternal is the unified entry point for ALL synchronous exceptions.
// Called from SyncExceptionDispatch stub in abi_stubs_arm64.s, which is called from
// exc_syscall_arm64.s. The stub loads args from stack to registers and uses BL to call
// this function directly at its ABIInternal entry point (no wrapper).
// It dispatches based on exception class (EC) to the appropriate handler:
//   - EC=0x15 (SVC): syscall handling
//   - EC=0x25 (data abort): page fault / demand paging
//   - Other: general exception handling
//
// Parameters (all passed via registers from assembly):
//   ec        - Exception class (bits 31:26 of ESR, already extracted)
//   esr       - Full ESR_EL1 value
//   elr       - ELR_EL1 (return address)
//   far       - FAR_EL1 (fault address, valid for data aborts)
//   spsr      - SPSR_EL1 (saved processor state)
//   syscallNum - R8 (syscall number, valid for SVC)
//   arg0-arg5 - R0-R5 (syscall arguments, valid for SVC)
//   framePtr  - Pointer to exception frame on stack
//   savedFP   - Saved x29 (for traceback)
//   savedLR   - Saved x30 (for traceback)
//   savedG    - Saved x28/g (for traceback)
//
// Returns:
//   result   - Syscall return value (for SVC), or 0 for handled page faults
//   switchTo - Thread index for context switch (-1 = no switch, >=0 = switch to thread)
//   handled  - true if exception was handled (return via ERET), false to hang
//
// NOTE: nosplit removed - we're on g0 with 64KB stack after sync_exception_entry switches g.
// Stack growth isn't possible on g0, but we have enough space without the nosplit limit.
//
//go:noinline
func syncExceptionDispatchInternal(
	ec uint64,
	esr uint64,
	elr uint64,
	far uint64,
	spsr uint64,
	syscallNum uint64,
	arg0, arg1, arg2, arg3, arg4, arg5 uint64,
	framePtr uintptr,
	savedFP, savedLR, savedG uint64,
) (result int64, switchTo int32, handled bool) {

	// DEBUG: Print marker to verify this function is called
	uartPutcDirect('#')

	// DEBUG: Print SPSR I-bit to check if interrupts are enabled
	// SPSR bit 7 = I mask: 0=enabled, 1=disabled
	if (spsr & 0x80) != 0 {
		uartPutcDirect('I') // Interrupts DISABLED in saved state
	}

	// DEBUG: Periodically print timer state (every 100th syscall)
	timerDebugCounter++
	if timerDebugCounter%100 == 1 {
		vcnt := asm.ReadCntvctEl0()
		pcnt := asm.ReadCntpctEl0()
		cval := asm.ReadCntvCvalEl0()
		ctl := asm.ReadCntvCtlEl0()
		freq := asm.ReadCntfrqEl0()
		uartPutsDirect("\r\nTIMER: vcnt=")
		uartPutHex64Direct(vcnt)
		uartPutsDirect(" pcnt=")
		uartPutHex64Direct(pcnt)
		uartPutsDirect(" cval=")
		uartPutHex64Direct(cval)
		uartPutsDirect(" ctl=")
		uartPutHex32Direct(ctl)
		uartPutsDirect(" freq=")
		uartPutHex32Direct(freq)
		uartPutsDirect("\r\n")
	}

	// DEBUG: Print entry info for non-syscall exceptions
	if ec != 0x15 && ec != 0x25 {
		uartPutsDirect("SED:")
		uartPutHex64Direct(ec)
		uartPutcDirect(' ')
	}

	switch ec {
	case 0x15: // EC_SVC_EL1_A64 - Supervisor call from AArch64
		// DEBUG: Print syscall number for tracing
		uartPutcDirect('S')
		uartPutHex32Direct(uint32(syscallNum))
		uartPutcDirect(' ')
		// Dispatch to syscall handler
		result, switchTo = SyscallDispatch(
			int64(syscallNum),
			arg0, arg1, arg2, arg3, arg4, arg5,
			framePtr,
		)
		return result, switchTo, true

	case 0x25: // EC_DATA_ABORT_ELx - Data abort from current EL
		// Extract fault status from ESR (bits 5:0)
		faultStatus := esr & 0x3F

		// Try demand paging
		if HandlePageFault(uintptr(far), faultStatus) {
			// Page fault handled - return to retry faulting instruction
			return 0, -1, true
		}

		// Page fault failed - print error and indicate not handled
		uartPutsDirect("\r\n!FATAL DATA ABORT:\r\n")
		uartPutsDirect("  FAR=0x")
		uartPutHex64Direct(far)
		uartPutsDirect(" ELR=0x")
		uartPutHex64Direct(elr)
		uartPutsDirect(" ESR=0x")
		uartPutHex64Direct(esr)
		uartPutsDirect("\r\n")
		return 0, -1, false

	case 0x00: // EC_UNKNOWN - Unknown exception (often NULL pointer)
		uartPutsDirect("\r\n!UNKNOWN EXCEPTION (EC=0x00):\r\n")
		uartPutsDirect("  ELR=0x")
		uartPutHex64Direct(elr)
		uartPutsDirect(" FAR=0x")
		uartPutHex64Direct(far)
		uartPutsDirect("\r\n")
		// Fatal - call exit
		SyscallExit()
		// Never returns
		return 0, -1, false

	default:
		// Unknown EC in synchronous exception handler
		uartPutsDirect("\r\nUnknown EC in synchronous exception handler: EC=0x")
		uartPutHex64Direct(ec)
		uartPutsDirect("\r\n  ELR=0x")
		uartPutHex64Direct(elr)
		uartPutsDirect(" FAR=0x")
		uartPutHex64Direct(far)
		uartPutsDirect(" ESR=0x")
		uartPutHex64Direct(esr)
		uartPutsDirect("\r\n")
		// Return false to hang in assembly
		return 0, -1, false
	}
}
