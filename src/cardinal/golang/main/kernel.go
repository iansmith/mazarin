package main

import (
	"unsafe"

	"cardinal/asm"
	"cardinal/constants"
)

// KernelMain is the entry point called from boot_arm64.s
// It's implemented in abi_stubs_arm64.s as an ABI0 wrapper
// that calls kernelMainInternal with proper register setup.
func KernelMain(r0, r1, atags uint32)

// Assembly functions to set runtime stack limits
//
//go:linkname setMaxstacksize set_maxstacksize
func setMaxstacksize(size uintptr)

//go:linkname setMaxstackceiling set_maxstackceiling
func setMaxstackceiling(size uintptr)

// readActualDTBSize reads the actual DTB size from the DTB header
// instead of using the hard-coded 1MB constant.
//
// DTB Header Format (at DTB base address):
//   Offset 0x00: magic (0xd00dfeed, big-endian)
//   Offset 0x04: totalsize (uint32, big-endian) <- what we read
//   Offset 0x08: off_dt_struct
//   ...
//
// Returns the DTB size rounded up to the nearest page boundary (4KB)
//
//go:nosplit
func readActualDTBSize() uintptr {
	dtbBase := asm.GetDtbBootAddr()

	// Read totalsize field at offset 0x04 (big-endian uint32)
	sizePtr := (*uint32)(unsafe.Pointer(dtbBase + 4))
	sizeBE := *sizePtr

	// Convert from big-endian to little-endian
	actualSize := ((sizeBE & 0xFF) << 24) |
		((sizeBE & 0xFF00) << 8) |
		((sizeBE & 0xFF0000) >> 8) |
		((sizeBE & 0xFF000000) >> 24)

	// Validate the size is reasonable
	// DTB size should be between 4KB and 1MB typically
	// If it's 0 or too large, fall back to reading from linker symbol
	if actualSize == 0 || actualSize > 0x100000 {
		// Fall back to linker-defined size (1MB default)
		return asm.GetDtbSize()
	}

	// Round up to page boundary (4KB = 0x1000)
	pageSizeRounded := (uintptr(actualSize) + 0xFFF) &^ 0xFFF

	return pageSizeRounded
}

// preRegisterFixedSpans pre-registers the fixed memory regions that are
// identity-mapped or pre-mapped before the MMU is enabled.
//
// This MUST be called before any mmap() syscalls to ensure:
// - Span 0: DTB region (actual size from header, not 1MB)
// - Span 1: Cardinal region (code/data/bss/stacks)
//
// Span 2 (Kmazarin) is registered later by loadAndRunKmazarin() after ELF loading.
//
//go:nosplit
func preRegisterFixedSpans() {
	// Span 0: DTB Region (identity-mapped)
	// Use fixed 1MB size to avoid memory access issues early in boot
	dtbStart := asm.GetDtbBootAddr()
	dtbSize := asm.GetDtbSize() // Use fixed 1MB from linker symbol
	dtbEnd := dtbStart + dtbSize

	if !registerMmapSpan(dtbStart, dtbEnd) {
		for {} // Hang
	}

	// Span 1: Cardinal Region (identity-mapped)
	// Includes: .text, .rodata, .data, .bss, stacks
	// Start: 0x40100000 (after DTB)
	// End: bss_end (last section of cardinal)
	cardinalStart := asm.GetTextStartAddr() // 0x40100000
	cardinalEnd := asm.GetBssEndAddr()      // End of bss section

	if !registerMmapSpan(cardinalStart, cardinalEnd) {
		for {} // Hang
	}

	// Span 3: Bump Allocator Region (pre-register fixed 2GB region)
	// This is used as a fallback when Go provides no hint (addr=0)
	// Pre-registering avoids wasting span slots on individual small allocations
	const BUMP_REGION_START = uintptr(0x48000000)
	const BUMP_REGION_SIZE = uintptr(0x80000000) // 2GB
	bumpEnd := BUMP_REGION_START + BUMP_REGION_SIZE

	if !registerMmapSpan(BUMP_REGION_START, bumpEnd) {
		for {} // Hang
	}
}

// Peripheral base address for Raspberry Pi 4
const (
	// Peripheral base address for Raspberry Pi 4
	PERIPHERAL_BASE uintptr = 0xFE000000 // Raspberry Pi 4 (was 0x3F000000 for Pi 2/3, 0x20000000 for Pi 1)

	// The GPIO registers base address
	GPIO_BASE = PERIPHERAL_BASE + 0x200000 // 0xFE200000 for Pi 4

	GPPUD     = GPIO_BASE + 0x94
	GPPUDCLK0 = GPIO_BASE + 0x98

	// The base address for UART0 (PL011 UART)
	UART0_BASE = PERIPHERAL_BASE + 0x201000 // 0xFE201000 for Pi 4

	UART0_DR     = UART0_BASE + 0x00
	UART0_RSRECR = UART0_BASE + 0x04
	UART0_FR     = UART0_BASE + 0x18
	UART0_ILPR   = UART0_BASE + 0x20
	UART0_IBRD   = UART0_BASE + 0x24
	UART0_FBRD   = UART0_BASE + 0x28
	UART0_LCRH   = UART0_BASE + 0x2C
	UART0_CR     = UART0_BASE + 0x30
	UART0_IFLS   = UART0_BASE + 0x34
	UART0_IMSC   = UART0_BASE + 0x38
	UART0_RIS    = UART0_BASE + 0x3C
	UART0_MIS    = UART0_BASE + 0x40
	UART0_ICR    = UART0_BASE + 0x44
	UART0_DMACR  = UART0_BASE + 0x48
	UART0_ITCR   = UART0_BASE + 0x80
	UART0_ITIP   = UART0_BASE + 0x84
	UART0_ITOP   = UART0_BASE + 0x88
	UART0_TDR    = UART0_BASE + 0x8C

	// Mailbox base address (BCM2835 Mailbox)
	// Raspberry Pi 4 uses the same mailbox interface as Pi 2/3
	MAILBOX_BASE = PERIPHERAL_BASE + 0xB880 // 0xFE00B880 for Pi 4

	MAILBOX_READ   = MAILBOX_BASE + 0x00
	MAILBOX_STATUS = MAILBOX_BASE + 0x18
	MAILBOX_WRITE  = MAILBOX_BASE + 0x20
)

// UART functions are in:
// - uart_rpi.go (for real hardware, build tag: !qemu)
// - uart_qemu.go (for QEMU, build tag: qemu)
// Both implementations have the same signatures:
//   func uartInit()
//   func uartPutc(c byte)
//   func uartGetc() byte

//go:nosplit
func uartPutsBytes(data *byte, length int) {
	ptr := uintptr(unsafe.Pointer(data))
	lenVal := length

	// Write all characters in the string using uartPutc (which checks if UART is enabled)
	for i := 0; i < lenVal; i++ {
		uartPutc(*(*byte)(unsafe.Pointer(ptr + uintptr(i))))
	}
}

// printHex64 outputs a uint64 as a 16-digit hex string via print()
//
//go:nosplit
//go:nosplit
func uint64String(n uint64) string {
	if n == 0 {
		return "0"
	}

	// Use fixed-size buffer (max uint64 is 20 digits)
	var buf [20]byte
	i := 19
	for n > 0 {
		buf[i] = byte('0' + (n % 10))
		n /= 10
		i--
	}

	// Use unsafe.String to avoid bounds checking overhead
	return unsafe.String(&buf[i+1], 19-i)
}

//go:nosplit
func hex32(val uint32) string {
	// Format: 0x0000_0000 (11 chars total)
	const hexChars = "0123456789ABCDEF"
	var buf [11]byte // "0x" + 8 hex digits + 1 underscore

	buf[0] = '0'
	buf[1] = 'x'

	pos := 2
	for i := 0; i < 8; i++ {
		nibble := (val >> uint(28-i*4)) & 0xF
		buf[pos] = hexChars[nibble]
		pos++
		// Add underscore after 4 hex digits
		if i == 3 {
			buf[pos] = '_'
			pos++
		}
	}

	// Use unsafe.String to avoid bounds checking overhead
	return unsafe.String(&buf[0], 11)
}

//go:nosplit
func hex64(val uint64) string {
	// Build string with underscores every 4 digits for readability
	// Format: 0x0000_0000_0000_0000 (21 chars total)
	const hexChars = "0123456789ABCDEF"
	var buf [21]byte // "0x" + 16 hex digits + 3 underscores

	buf[0] = '0'
	buf[1] = 'x'

	pos := 2
	for i := 0; i < 16; i++ {
		nibble := (val >> uint(60-i*4)) & 0xF
		buf[pos] = hexChars[nibble]
		pos++
		// Add underscore after every 4 hex digits (except at the end)
		if (i+1)%4 == 0 && i != 15 {
			buf[pos] = '_'
			pos++
		}
	}

	// Use unsafe.String to avoid bounds checking overhead
	return unsafe.String(&buf[0], 21)
}

// printHex outputs a uint64 as a 16-digit hex string
func printHex(val uint64) {
	print(hex64(val))
}

// printHex64 outputs a uint64 as a 16-digit hex string
// DEPRECATED: Use print(hex64(val)) instead for consistent formatting
func printHex64(val uint64) {
	print(hex64(val))
}

// printHex32 outputs a uint32 as an 8-digit hex string
// DEPRECATED: Use print(hex32(val)) instead for consistent formatting
func printHex32(val uint32) {
	print(hex32(val))
}

// printChar outputs a single character via the syscall mechanism
// This uses the same path as print() but for a single byte
//
//go:nosplit
func printChar(c byte) {
	// Use uartPutc which goes through the ring buffer/direct UART path
	uartPutc(c)
}

// uartPutHex32 outputs a uint32 as an 8-digit hex string via UART
//
//go:nosplit
func uartPutHex32(val uint32) {
	// Output 8 hex digits (32 bits / 4 bits per digit)
	for i := 7; i >= 0; i-- {
		nibble := (val >> uint(i*4)) & 0xF
		if nibble < 10 {
			uartPutc(byte('0' + nibble))
		} else {
			uartPutc(byte('A' + (nibble - 10)))
		}
	}
}

//go:nosplit
func uartPuts(str string) {
	// NOTE: String literals are not accessible in bare-metal Go
	// The .rodata section may not be loaded, or Go places string literals
	// in a way that's not accessible. For now, we'll use a workaround:
	// Instead of using string literals, we'll write strings character-by-character
	// directly in the calling code.
	//
	// This function is kept for API compatibility, but string literals won't work.
	// Use uartPutsBytes with explicit byte arrays instead.
	//
	// Use uartPutc instead of direct MMIO to ensure UART is enabled

	// Use unsafe.StringData() if available (Go 1.20+), otherwise fall back to manual access
	// For bare-metal, we use the manual string header access pattern
	// String layout: [data *uintptr, len int] = [2]uintptr on 64-bit
	strHeader := (*[2]uintptr)(unsafe.Pointer(&str))

	// Extract data pointer and length
	dataPtrVal := strHeader[0]
	strLenVal := strHeader[1]

	// If string is null/empty, just return (don't try to access)
	if dataPtrVal == 0 || strLenVal == 0 {
		return
	}

	// Convert to proper types
	dataPtr := (*byte)(unsafe.Pointer(dataPtrVal))
	strLen := int(strLenVal)

	// Call uartPutsBytes with the extracted pointer and length
	uartPutsBytes(dataPtr, strLen)
}

// uitoa converts a uint32 to its decimal string representation
// Returns the number of digits written
// This is a bare-metal implementation (no fmt package)
//
//go:nosplit
func uitoa(n uint32, buf []byte) int {
	if n == 0 {
		buf[0] = '0'
		return 1
	}

	// Count digits
	digits := 0
	temp := n
	for temp > 0 {
		digits++
		temp /= 10
	}

	// Write digits from right to left
	idx := digits - 1
	for n > 0 {
		buf[idx] = byte('0' + (n % 10))
		n /= 10
		idx--
	}

	return digits
}

// uartPutUint32 outputs a uint32 as a decimal string via UART
// CRITICAL FIX: Avoids local array to prevent unaligned stores when MMU is disabled
// With MMU disabled, memory is Device-nGnRnE type which requires strict alignment.
// The Go compiler would generate `stur xzr, [sp, #53]` for local array initialization,
// which stores 8 bytes to an unaligned address (SP + 53 = address ending in 5).
//
//go:nosplit
func uartPutUint32(n uint32) {
	// Workaround: Compute and output digits directly without local array
	// This avoids the problematic `stur xzr, [sp, #53]` instruction

	if n == 0 {
		uartPutc('0')
		return
	}

	// Count digits first (needed to output in correct order)
	digits := 0
	temp := n
	for temp > 0 {
		digits++
		temp /= 10
	}

	// Extract and output digits from left to right
	// We need to extract the most significant digit first
	divisor := uint32(1)
	for i := 1; i < digits; i++ {
		divisor *= 10
	}

	// Output each digit
	for i := 0; i < digits; i++ {
		digit := (n / divisor) % 10
		uartPutc(byte('0' + digit))
		divisor /= 10
	}
}

// printUint32 outputs a uint32 as a decimal string via print()
//
//go:nosplit
func printUint32(n uint32) {
	if n == 0 {
		printChar('0')
		return
	}

	// Count digits first
	digits := 0
	temp := n
	for temp > 0 {
		digits++
		temp /= 10
	}

	// Extract and output digits from left to right
	divisor := uint32(1)
	for i := 1; i < digits; i++ {
		divisor *= 10
	}

	for i := 0; i < digits; i++ {
		digit := (n / divisor) % 10
		printChar(byte('0' + digit))
		divisor /= 10
	}
}

// kernelMainInternal is the entry point called from boot.s via ABI stub KernelMain
// For bare metal, we ensure it's not optimized away
//
//go:noinline
func kernelMainInternal(r0, r1, atags uint32) {
	_ = r0
	_ = r1

	// On QEMU virt, the DTB pointer is passed in as the "atags" parameter
	setDTBPtr(uintptr(atags))

	// Initialize UART first for early debugging
	uartInit()

	// Check SCTLR_EL1 for alignment check bit
	sctlr := asm.ReadSctlrEl1()
	alignCheck := (sctlr & 2) != 0 // Bit 1: A - Alignment Check Enable

	// Disable alignment check if enabled (required for Go runtime)
	if alignCheck {
		asm.DisableAlignmentCheck()
	}

	// Initialize minimal runtime structures for write barrier
	initRuntimeStubs()

	// Set up exception vectors BEFORE enabling MMU
	exceptionVectorAddr := asm.GetExceptionVectorsAddr()
	asm.SetVbarEl1ToAddr(exceptionVectorAddr)

	// Initialize MMU (required before heap - enables Normal memory for unaligned access)
	if !initMMU() {
		uartPutsDirect("FATAL: MMU init failed\r\n")
		for {
		}
	}

	if !enableMMU() {
		uartPutsDirect("FATAL: MMU enable failed\r\n")
		for {
		}
	}

	// Verify all globals used by page fault handler are pre-mapped
	if !VerifyCriticalGlobalsMapped() {
		uartPutsDirect("FATAL: Critical globals not mapped\r\n")
		for {
		}
	}

	// Pre-register fixed memory spans
	preRegisterFixedSpans()

	// Post-MMU device initialization
	initDeviceTree()
	gicInit()
	asm.EnableIrqs()

	// Call runtime.args with minimal argv/auxv structure
	result := asm.CallRuntimeArgs()
	if result != 0 {
		uartPutsDirect("ERROR: runtime.args failed\r\n")
		for {
		}
	}

	// Initialize timer for preemptive scheduling
	timerInit()

	// Initialize thread table (M0 as thread 0 for syscall handling)
	InitThreads()

	// Load and run kmazarin (never returns)
	loadAndRunKmazarin()

	// Should never return - if we get here, something went very wrong
	print("FATAL: loadAndRunKmazarin returned unexpectedly\r\n")
	asm.SemihostingExit()
}

// abortBoot aborts the boot process with a fatal error message
// This function prints the error message, exits QEMU, and hangs forever
// Used by critical initialization failures (MMU, SDHCI, etc.)
//
//go:nosplit
func abortBoot(message string) {
	print("FATAL: ")
	print(message)
	print("\r\n")
	print("Aborting boot process...\r\n")
	asm.QemuExit()
	for {
		// Hang forever
	}
}

// NOTE: parseEmbeddedKmazarin() was removed - it was dead code that was only
// called from the old goroutine-based startup path (simpleMain)

// populateRuntimeConfig populates the RuntimeConfig structure at the beginning
// of the StartupParams buffer. This provides kmazarin with immediate access to
// all runtime configuration before the Go runtime initializes.
//
//go:nosplit
func populateRuntimeConfig(kmazarinStartupParamsVA uintptr, ttbr1L0PA uintptr) {
	// Get frame pool info
	physAlloc := getPhysFrameAllocator()

	// Compute derived values
	const KernelVAOffset = uint64(0xFFFFFFFF00000000)
	const TTBR1RegionVA = uint64(0x419C0000)  // Where TTBR1 tables are mapped (low-mem)
	const TTBR1RegionSize = uint64(0x10000)   // 64KB for TTBR1 tables
	const PTPoolSize = uint64(0x30000)        // 192KB (48 pages)

	ptPoolStart := TTBR1RegionVA + TTBR1RegionSize + KernelVAOffset
	ptPoolEnd := ptPoolStart + PTPoolSize

	// Use TTBR1 (kernel-space) addresses for heap - high memory
	// This tests whether we can make Go work with high-memory addresses
	// by patching the arena index checks in the Go runtime.
	heapStart := uint64(constants.KernelHeapStart) // High memory: 0xFFFFFFFF41A00000
	heapEnd := uint64(constants.KernelHeapEnd)     // High memory: 0xFFFFFFFF45800000

	// Cast StartupParams buffer to RuntimeConfig struct
	// Note: We're using shared/constants.RuntimeConfig structure
	type RuntimeConfig struct {
		Magic                uint32
		Version              uint32
		DtbPhysAddr          uint64
		DtbSize              uint64
		DtbVirtAddr          uint64
		KmazarinPhysAddr     uint64
		KmazarinSize         uint64
		FramePoolStart       uint64
		FramePoolEnd         uint64
		KernelUartBase       uint64
		KernelGicBase        uint64
		TTBR1L0Phys          uint64
		TTBR0L0Phys          uint64
		StartupParamsAddr    uint64
		KernelVAOffset       uint64
		KernelPTPoolStart    uint64
		KernelPTPoolEnd      uint64
		KernelHeapStart      uint64
		KernelHeapEnd        uint64
		PageSize             uint64
		HWCap                uint64
		G0StackBottom        uint64
		G0StackTop           uint64
		G0StackSize          uint64
		ExceptionStackBottom uint64
		ExceptionStackTop    uint64
		ExceptionStackSize   uint64
		G0StructAddr         uint64 // High-memory address for g0 struct copy
		AsyncPreemptAddr     uint64 // High-memory address of runtime.asyncPreempt
		ReadyForAsyncPreempt uint64 // Address of readyForAsyncPreempt flag
	}

	config := (*RuntimeConfig)(unsafe.Pointer(kmazarinStartupParamsVA))

	// Populate the struct
	config.Magic = 0x4B4D5A52 // "KMZR"
	config.Version = 1

	// DTB information
	dtbPhys := uint64(asm.GetDtbBootAddr())
	config.DtbPhysAddr = dtbPhys
	config.DtbSize = uint64(asm.GetDtbSize())
	config.DtbVirtAddr = dtbPhys + KernelVAOffset // Compute DTB VA from physical

	// Kmazarin binary information
	config.KmazarinPhysAddr = uint64(constants.KmazarinLoadAddr)
	config.KmazarinSize = uint64(kmazarinAllocatedBytes)

	// Frame pool
	config.FramePoolStart = uint64(physAlloc.next)
	config.FramePoolEnd = uint64(physAlloc.end)

	// MMIO devices (already in high memory)
	config.KernelUartBase = uint64(constants.KernelUartBase)
	config.KernelGicBase = uint64(constants.KernelGicBase)

	// Page tables
	config.TTBR1L0Phys = uint64(ttbr1L0PA)
	config.TTBR0L0Phys = uint64(pageTableL0)
	config.StartupParamsAddr = uint64(kmazarinStartupParamsVA)

	// Memory layout
	config.KernelVAOffset = KernelVAOffset
	config.KernelPTPoolStart = ptPoolStart
	config.KernelPTPoolEnd = ptPoolEnd
	config.KernelHeapStart = heapStart
	config.KernelHeapEnd = heapEnd

	// Standard values
	config.PageSize = 4096
	config.HWCap = 0x2 // HWCAP_ASIMD

	// CRITICAL: Pass KERNEL high-memory stack addresses (NOT Cardinal bootstrap stacks)
	// All values computed from constants, not hardcoded
	config.G0StackBottom = uint64(constants.KernelG0StackBottom)
	config.G0StackTop = uint64(constants.KernelG0StackTop)
	config.G0StackSize = uint64(constants.KernelG0StackSize)
	config.ExceptionStackBottom = uint64(constants.KernelExcStackBottom)
	config.ExceptionStackTop = uint64(constants.KernelExcStackTop)
	config.ExceptionStackSize = uint64(constants.KernelExcStackSize)

	// G0 struct address (for copying g struct to kmazarin high memory)
	config.G0StructAddr = uint64(LinkerKmazarinG0Struct)

	// AsyncPreempt address (for timer-based preemption in kmazarin)
	config.AsyncPreemptAddr = uint64(LinkerKmazarinAsyncPreempt)

	// ReadyForAsyncPreempt flag address (kmazarin checks this before preempting)
	config.ReadyForAsyncPreempt = uint64(LinkerKmazarinReadyForPreempt)

	// Ensure writes complete
	asm.Dsb()
}

// setupKmazarinStartupEnv sets up the argc/argv/envp/auxv structure that the Go runtime expects
// Returns: (stackPointer, argc, argv)
//   - stackPointer: pointer to the start of the structure (for SP register)
//   - argc: argument count (for R0 register)
//   - argv: pointer to argv array (for R1 register)
//
// The structure layout in memory (byte offsets):
//   [0]   = argc (int64, value: 1)
//   [8]   = argv[0] (pointer to program name string at offset 256)
//   [16]  = NULL (end of argv)
//   [24]  = NULL (end of envp, empty environment)
//   [32]  = auxv[0].tag (AT_PAGESZ = 6)
//   [40]  = auxv[0].value (4096)
//   [48]  = auxv[1].tag (AT_RANDOM = 25)
//   [56]  = auxv[1].value (pointer to random bytes at offset 272)
//   [64]  = auxv[2].tag (AT_SECURE = 23)
//   [72]  = auxv[2].value (0)
//   [80]  = auxv[3].tag (AT_HWCAP = 16)
//   [88]  = auxv[3].value (HWCAP_ASIMD = 0x2)
//   [96]  = auxv[4].tag (AT_NULL = 0)
//   [104] = auxv[4].value (0)
//   [256] = program name string "kmazarin\0"
//   [272] = random bytes (16 bytes, 128 bits)
//
// This matches the Linux kernel's ELF loader behavior.
//
// NOTE: All Cardinal boot information (DTB, frame pools, UART, GIC, TTBR1, etc.)
// is passed via RuntimeConfig structure at the beginning of StartupParams buffer,
// eliminating the need for custom AT_* auxv entries. RuntimeConfig is the single
// source of truth.
//
//go:nosplit
func setupKmazarinStartupEnv(kmazarinStartupParamsVA uintptr) (stackPointer uintptr, argc uint64, argv uintptr) {
	// CRITICAL: Place argc/argv/envp/auxv at the TOP of the kernel g0 stack (high memory)
	// The kernel g0 stack is already mapped by setupKernelStacks() using computed addresses
	// from constants. We place the parameters near the top, leaving room for stack growth.
	kernelG0StackTop := uintptr(constants.KernelG0StackTop)
	const paramSize = 0x200  // 512 bytes for argc/argv/envp/auxv structure

	// Place structure 512 bytes below stack top
	structStart := kernelG0StackTop - paramSize

	// Zero the structure area
	bzeroSimple(unsafe.Pointer(structStart), paramSize)

	// Set up the structure as an array of uint64 values
	data := (*[256]uint64)(unsafe.Pointer(structStart))

	// Auxv entries use indices 4-13 (starting at byte 32, ending at byte 104)
	// After auxv, we place strings and random data:
	// - Program name string at byte offset 256 (index 32)
	// - Random bytes at byte offset 272 (index 34)

	// Program name string at offset 256 (byte offset, 8-byte aligned)
	progName := (*[16]byte)(unsafe.Pointer(structStart + 256))
	progName[0] = 'k'
	progName[1] = 'm'
	progName[2] = 'a'
	progName[3] = 'z'
	progName[4] = 'a'
	progName[5] = 'r'
	progName[6] = 'i'
	progName[7] = 'n'
	progName[8] = 0

	// Get random bytes for AT_RANDOM at offset 272
	randomBytesAddr := structStart + 272
	getRandomBytes(unsafe.Pointer(randomBytesAddr), 16)

	// argc = 1
	data[0] = 1
	// argv[0] = pointer to program name (at offset 256)
	data[1] = uint64(structStart + 256)
	// argv[1] = NULL (end of argv)
	data[2] = 0
	// envp[0] = NULL (empty environment)
	data[3] = 0

	// Auxiliary vector starts at offset 32 (index 4)
	// AT_PAGESZ = 6: Physical page size
	data[4] = 6
	data[5] = 4096
	// AT_RANDOM = 25: pointer to 16 bytes of random data
	data[6] = 25
	data[7] = uint64(randomBytesAddr)
	// AT_SECURE = 23: secure mode flag (0 = not secure)
	data[8] = 23
	data[9] = 0
	// AT_HWCAP = 16: ARM64 hardware capability bits
	// Go runtime uses this via archauxv() -> cpu.HWCap for feature detection
	// Provide basic ASIMD/NEON capability (bit 1) which is mandatory on AArch64
	data[10] = 16
	data[11] = 0x2 // HWCAP_ASIMD - Advanced SIMD (NEON)

	// AT_NULL = 0 (terminator)
	// NOTE: All Cardinal boot information is now passed via RuntimeConfig in StartupParams,
	// eliminating the need for custom AT_* auxv entries. RuntimeConfig is the single source
	// of truth for DTB, frame pools, UART, GIC, TTBR1, etc.
	data[12] = 0
	data[13] = 0

	// ========================================================================
	// Return stack pointer - structure is already on kernel g0 stack
	// ========================================================================
	// SP should point to argc (start of structure on kernel g0 stack)
	// R0 = argc
	// R1 = pointer to argv (which is at offset 8 from structStart)
	//
	// The Go runtime will use the ~15.5KB of stack space below structStart
	// for initialization and early execution.
	asm.Dsb()  // Ensure all writes complete

	return structStart, 1, structStart + 8
}

// loadAndRunKmazarin loads the embedded kmazarin ELF binary into memory and jumps to it
// This function:
// 1. Parses the ELF program headers to find PT_LOAD segments
// 2. Computes a load offset to avoid DTB conflict at 0x40000000-0x40100000
//    - offset = DTB_END - first_segment_vaddr
// 3. Copies each segment from the embedded binary to memory at (vaddr + offset)
// 4. Handles BSS zeroing (memsz > filesz)
// 5. Jumps to the kmazarin entry point
//
// Note: This function is NOT marked nosplit because it runs after the Go runtime
// is initialized and contains many print() calls that require stack space.
func loadAndRunKmazarin() {
	// Get the embedded kmazarin binary location from linker symbols
	kmazarinStart := getLinkerSymbol("__kmazarin_start")
	kmazarinSize := getLinkerSymbol("__kmazarin_size")

	// Validate symbols
	if kmazarinStart == 0 || kmazarinSize == 0 {
		uartPutsDirect("ERROR: Invalid kmazarin symbols\r\n")
		return
	}

	// Create a byte slice from the embedded binary
	var elfData []byte
	sliceHeader := (*struct {
		Data uintptr
		Len  int
		Cap  int
	})(unsafe.Pointer(&elfData))
	sliceHeader.Data = kmazarinStart
	sliceHeader.Len = int(kmazarinSize)
	sliceHeader.Cap = int(kmazarinSize)

	// Verify ELF magic
	if len(elfData) < 64 ||
		elfData[0] != 0x7F || elfData[1] != 'E' || elfData[2] != 'L' || elfData[3] != 'F' {
		uartPutsDirect("ERROR: Invalid ELF file\r\n")
		return
	}

	// Parse entry point (offset 0x18-0x1F for 64-bit ELF)
	entry := uint64(elfData[0x18]) |
		(uint64(elfData[0x19]) << 8) |
		(uint64(elfData[0x1A]) << 16) |
		(uint64(elfData[0x1B]) << 24) |
		(uint64(elfData[0x1C]) << 32) |
		(uint64(elfData[0x1D]) << 40) |
		(uint64(elfData[0x1E]) << 48) |
		(uint64(elfData[0x1F]) << 56)

	// Parse program header offset (0x20-0x27)
	phoff := uint64(elfData[0x20]) |
		(uint64(elfData[0x21]) << 8) |
		(uint64(elfData[0x22]) << 16) |
		(uint64(elfData[0x23]) << 24) |
		(uint64(elfData[0x24]) << 32) |
		(uint64(elfData[0x25]) << 40) |
		(uint64(elfData[0x26]) << 48) |
		(uint64(elfData[0x27]) << 56)

	// Parse program header entry size and count
	phentsize := uint16(elfData[0x36]) | (uint16(elfData[0x37]) << 8)
	phnum := uint16(elfData[0x38]) | (uint16(elfData[0x39]) << 8)

	// PT_LOAD = 1
	const PT_LOAD = 1

	// Track min/max VAs for page fault handler registration
	var minVA, maxVA uintptr

	// Get kmazarin's StartupParams address from linker symbol
	// This is where Cardinal will copy argc/argv/envp/auxv before jumping
	// The address is extracted from kmazarin.elf symbol table at build time
	kmazarinStartupParamsVA := uintptr(LinkerKmazarinStartupParams)

	// Process each program header
	for i := uint16(0); i < phnum; i++ {

		phOffset := phoff + uint64(i)*uint64(phentsize)
		if phOffset+56 > uint64(len(elfData)) {
			uartPutsDirect("ERROR: Invalid program header\r\n")
			return
		}

		// Parse program header type
		pType := uint32(elfData[phOffset]) |
			(uint32(elfData[phOffset+1]) << 8) |
			(uint32(elfData[phOffset+2]) << 16) |
			(uint32(elfData[phOffset+3]) << 24)

		// Header type check

		if pType != PT_LOAD {
			continue
		}

		// Parse segment flags (offset +4)
		// Bits: PF_X=0x1 (execute), PF_W=0x2 (write), PF_R=0x4 (read)
		pFlags := uint32(elfData[phOffset+4]) |
			(uint32(elfData[phOffset+5]) << 8) |
			(uint32(elfData[phOffset+6]) << 16) |
			(uint32(elfData[phOffset+7]) << 24)

		const (
			PF_X = 0x1 // Execute
			PF_W = 0x2 // Write
			PF_R = 0x4 // Read
		)

		// Parse PT_LOAD segment fields
		pOffset := uint64(elfData[phOffset+8]) |
			(uint64(elfData[phOffset+9]) << 8) |
			(uint64(elfData[phOffset+10]) << 16) |
			(uint64(elfData[phOffset+11]) << 24) |
			(uint64(elfData[phOffset+12]) << 32) |
			(uint64(elfData[phOffset+13]) << 40) |
			(uint64(elfData[phOffset+14]) << 48) |
			(uint64(elfData[phOffset+15]) << 56)

		pVaddr := uint64(elfData[phOffset+16]) |
			(uint64(elfData[phOffset+17]) << 8) |
			(uint64(elfData[phOffset+18]) << 16) |
			(uint64(elfData[phOffset+19]) << 24) |
			(uint64(elfData[phOffset+20]) << 32) |
			(uint64(elfData[phOffset+21]) << 40) |
			(uint64(elfData[phOffset+22]) << 48) |
			(uint64(elfData[phOffset+23]) << 56)

		pFilesz := uint64(elfData[phOffset+32]) |
			(uint64(elfData[phOffset+33]) << 8) |
			(uint64(elfData[phOffset+34]) << 16) |
			(uint64(elfData[phOffset+35]) << 24) |
			(uint64(elfData[phOffset+36]) << 32) |
			(uint64(elfData[phOffset+37]) << 40) |
			(uint64(elfData[phOffset+38]) << 48) |
			(uint64(elfData[phOffset+39]) << 56)

		pMemsz := uint64(elfData[phOffset+40]) |
			(uint64(elfData[phOffset+41]) << 8) |
			(uint64(elfData[phOffset+42]) << 16) |
			(uint64(elfData[phOffset+43]) << 24) |
			(uint64(elfData[phOffset+44]) << 32) |
			(uint64(elfData[phOffset+45]) << 40) |
			(uint64(elfData[phOffset+46]) << 48) |
			(uint64(elfData[phOffset+47]) << 56)

		// Calculate page-aligned range from ELF virtual address
		// ELF is already relocated to high memory (0xFFFFFFFF41800000+)
		// Use the addresses directly without adding offset
		kernelVAStart := uintptr(pVaddr) &^ 0xFFF
		kernelVAEnd := (uintptr(pVaddr) + uintptr(pMemsz) + 0xFFF) &^ 0xFFF
		numPages := (kernelVAEnd - kernelVAStart) >> 12

		// Update min/max for span registration (use high-memory VAs)
		if minVA == 0 || kernelVAStart < minVA {
			minVA = kernelVAStart
		}
		if kernelVAEnd > maxVA {
			maxVA = kernelVAEnd
		}

		// Determine execute permission based on segment flags
		var execPerm uint64 = PTE_EXEC_NEVER
		if (pFlags & PF_X) != 0 {
			execPerm = PTE_EXEC_ALLOW
		}

		// Map all pages for this segment in TTBR1 (high-memory kernel space)
		for pageIdx := uintptr(0); pageIdx < numPages; pageIdx++ {
			kernelVA := kernelVAStart + (pageIdx << 12)

			// Enforce kmazarin size limit (64MB total)
			if kmazarinAllocatedBytes+0x1000 > uintptr(constants.KmazarinTotalLimit) {
				kernelPanic("Kmazarin size limit exceeded")
			}

			physFrame := allocPhysFrame()
			if physFrame == 0 {
				uartPutsDirect("ERROR: Out of memory mapping segment\r\n")
				return
			}

			// Track kmazarin allocation
			kmazarinAllocatedBytes += 0x1000

			// Map high-memory kernel VA to physical frame in TTBR1 page tables
			// For now, map as RW (we need to write during loading)
			// Execute permission is set based on PF_X flag
			mapKernelPage(kernelVA, physFrame, PTE_ATTR_NORMAL, PTE_AP_RW_EL1, execPerm)
		}

		// Use barriers after mapping (no TLB invalidation needed for new mappings)
		asm.Dsb()
		asm.Isb()

		// Copy file data to mapped memory (use kernel VAs - high memory)
		if pFilesz > 0 {
			var srcOffset uintptr
			var dstAddr uintptr
			var copySize uint64

			if pOffset > 0x8000000000000000 { // Negative offset (e.g., first LOAD segment)
				// Go's linker with -T flag creates segments with negative offsets
				// The segment includes a 64KB header region before .text

				// Zero-fill the header region (first 64KB of segment) - use kernel VA
				headerSize := uintptr(0x10000)  // 64KB for ELF headers, PHDR, notes
				headerStart := unsafe.Pointer(kernelVAStart)
				bzero4K(headerStart, uint32(headerSize))

				// Copy .text section from file offset 0x1000 to kernel VA (segment_va + 0x10000)
				srcOffset = kmazarinStart + 0x1000
				dstAddr = kernelVAStart + headerSize  // Skip past header region
				copySize = pFilesz - uint64(headerSize)  // Remaining bytes after header
			} else {
				// Normal positive offset - load segment data directly to kernel VA
				srcOffset = kmazarinStart + uintptr(pOffset)
				dstAddr = kernelVAStart
				copySize = pFilesz
			}

			if copySize > 0 {
				dst := unsafe.Pointer(dstAddr)
				src := unsafe.Pointer(srcOffset)
				asm.MemmoveBytes(dst, src, uint32(copySize))
			}
		}

		// Zero BSS section (MemSz > FileSz) - use kernel VA
		if pMemsz > pFilesz {
			bssStart := unsafe.Pointer(kernelVAStart + uintptr(pFilesz))
			bssSize := pMemsz - pFilesz
			bzeroSimple(bssStart, uint32(bssSize))
		}

		// NOTE: Remapping to RO is deferred until after cache maintenance
		// ARM requires: write code → clean D-cache → remap RO → invalidate I-cache
	}

	// CRITICAL: Cache maintenance and remapping for executable code
	// ARM Architecture mandates this sequence:
	// 1. DC CVAU (clean D-cache) - make code visible to hardware walker
	// 2. DSB - ensure cache clean completes
	// 3. Remap pages to RO (modify PTEs)
	// 4. DSB ISHST + TLBI + DSB ISH - ensure PTE changes visible
	// 5. IC IVAU (invalidate I-cache) - discard stale instructions
	// 6. DSB + ISB - final synchronization

	// Step 1-2: Clean D-cache for all code pages
	for va := minVA; va < maxVA; va += 64 {
		// DC CVAU - Data Cache Clean by VA to Point of Unification
		asm.DcCvau(va)
	}
	asm.Dsb() // Ensure all DC operations complete

	// Step 3-4: Remap executable segments to Read-Only
	// Iterate over all loaded segments and remap those with EXEC permission
	const PF_X_CONST = 0x1 // Execute permission flag
	for i := uint16(0); i < phnum; i++ {
		phOffset := phoff + uint64(i)*uint64(phentsize)
		if phOffset+56 > uint64(len(elfData)) {
			continue
		}

		// Parse program header
		pType := uint32(elfData[phOffset]) |
			(uint32(elfData[phOffset+1]) << 8) |
			(uint32(elfData[phOffset+2]) << 16) |
			(uint32(elfData[phOffset+3]) << 24)

		// Skip non-LOAD segments
		if pType != PT_LOAD {
			continue
		}

		// Parse segment flags
		pFlags := uint32(elfData[phOffset+4]) |
			(uint32(elfData[phOffset+5]) << 8) |
			(uint32(elfData[phOffset+6]) << 16) |
			(uint32(elfData[phOffset+7]) << 24)

		// Only remap executable segments
		if (pFlags & PF_X_CONST) == 0 {
			continue
		}

		// Parse pVaddr and pMemsz
		pVaddr := uint64(elfData[phOffset+16]) |
			(uint64(elfData[phOffset+17]) << 8) |
			(uint64(elfData[phOffset+18]) << 16) |
			(uint64(elfData[phOffset+19]) << 24) |
			(uint64(elfData[phOffset+20]) << 32) |
			(uint64(elfData[phOffset+21]) << 40) |
			(uint64(elfData[phOffset+22]) << 48) |
			(uint64(elfData[phOffset+23]) << 56)

		pMemsz := uint64(elfData[phOffset+40]) |
			(uint64(elfData[phOffset+41]) << 8) |
			(uint64(elfData[phOffset+42]) << 16) |
			(uint64(elfData[phOffset+43]) << 24) |
			(uint64(elfData[phOffset+44]) << 32) |
			(uint64(elfData[phOffset+45]) << 40) |
			(uint64(elfData[phOffset+46]) << 48) |
			(uint64(elfData[phOffset+47]) << 56)

		// Calculate VAs for this segment (pVaddr already has high-memory address)
		kernelVAStart := uintptr(pVaddr) &^ 0xFFF
		kernelVAEnd := (uintptr(pVaddr) + uintptr(pMemsz) + 0xFFF) &^ 0xFFF
		numPages := (kernelVAEnd - kernelVAStart) >> 12

		// Remap each page to RO
		asm.DsbIsh() // Ensure prior stores complete (use DsbIsh since DsbIshst may not exist yet)

		for pageIdx := uintptr(0); pageIdx < numPages; pageIdx++ {
			kernelVA := kernelVAStart + (pageIdx << 12)

			// Walk page tables to find PTE
			ttbr1 := asm.ReadTtbr1El1()
			l0Table := uintptr(ttbr1 & ^uint64(0xFFF))
			l0Index := (kernelVA >> 39) & 0x1FF
			l0Entry := (*uint64)(unsafe.Pointer(l0Table + l0Index*8))
			l1Table := uintptr(*l0Entry & PTE_ADDR_MASK)
			l1Index := (kernelVA >> 30) & 0x1FF
			l1Entry := (*uint64)(unsafe.Pointer(l1Table + l1Index*8))
			l2Table := uintptr(*l1Entry & PTE_ADDR_MASK)
			l2Index := (kernelVA >> 21) & 0x1FF
			l2Entry := (*uint64)(unsafe.Pointer(l2Table + l2Index*8))
			l3Table := uintptr(*l2Entry & PTE_ADDR_MASK)
			l3Index := (kernelVA >> 12) & 0x1FF
			l3Entry := (*uint64)(unsafe.Pointer(l3Table + l3Index*8))

			// Modify PTE: change AP from RW to RO
			oldPTE := *l3Entry
			newPTE := (oldPTE & ^uint64(0xC0)) | PTE_AP_RO_EL1
			*l3Entry = newPTE
		}

		asm.DsbIsh()           // Ensure PTE writes visible
		asm.InvalidateTlbAll() // Invalidate TLB (contains DSB+ISB)
	}

	// Step 5-6: Invalidate I-cache and synchronize
	for va := minVA; va < maxVA; va += 64 {
		// IC IVAU - Instruction Cache Invalidate by VA to Point of Unification
		asm.IcIvau(va)
	}
	asm.Dsb() // Ensure all IC operations complete
	asm.Isb() // Synchronize context

	if !registerMmapSpan(minVA, maxVA) {
		// Failed to register - hang
		for {}
	}

	// CRITICAL: Populate RuntimeConfig in StartupParams buffer FIRST
	// This must happen before jumping to kmazarin, so config is available immediately
	populateRuntimeConfig(kmazarinStartupParamsVA, kernelPageTableL0)

	// Set up argc/argv/envp/auxv structure on g0 stack
	stackPointer, argc, argv := setupKmazarinStartupEnv(kmazarinStartupParamsVA)
	if stackPointer == 0 || argv == 0 {
		uartPutsDirect("ERROR: Environment setup failed!\r\n")
		return
	}

	// Entry point from ELF is already relocated to high memory
	// Use it directly without adding offset
	entryAddr := uintptr(entry)

	// CRITICAL: Disable all interrupts before transferring control
	// Kmazarin will re-enable them after installing its handlers
	asm.DisableIrqs()

	jumpToKmazarin(entryAddr, argc, argv, stackPointer)

	// Should never reach here
	uartPutsDirect("ERROR: Returned from kmazarin!\r\n")
}

// jumpToKmazarin jumps to the kmazarin kernel entry point with proper environment setup
// This is implemented in assembly (lib.s) to ensure clean transition
// Parameters:
//   - entryAddr = address to jump to
//   - argc = argument count (loaded into R0)
//   - argv = pointer to argv array (loaded into R1)
//   - stackPointer = pointer to argc/argv/envp/auxv structure (loaded into SP)
// NOTE: This function never returns
//
//go:linkname jumpToKmazarin jump_to_kmazarin
//go:nosplit
func jumpToKmazarin(entryAddr uintptr, argc uint64, argv uintptr, stackPointer uintptr)

// OLD SEGMENT LOADING CODE REMOVED - replaced with simple embedded binary approach
//
// The original code loaded and copied ELF segments, but since kmazarin is already embedded
// in cardinal's binary at compile time, we can skip that and jump directly to it.
//
