package main

import (
	"unsafe"

	"cardinal/asm"
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
	uartBase := uintptr(0x09000000)
	*(*uint32)(unsafe.Pointer(uartBase)) = '1' // 1 = start preRegister

	// Span 0: DTB Region (identity-mapped)
	// Use fixed 1MB size to avoid memory access issues early in boot
	*(*uint32)(unsafe.Pointer(uartBase)) = 'a' // a = about to call GetDtbBootAddr
	dtbStart := asm.GetDtbBootAddr()
	*(*uint32)(unsafe.Pointer(uartBase)) = '2' // 2 = got dtbStart
	*(*uint32)(unsafe.Pointer(uartBase)) = 'b' // b = about to call GetDtbSize
	dtbSize := asm.GetDtbSize() // Use fixed 1MB from linker symbol
	*(*uint32)(unsafe.Pointer(uartBase)) = '3' // 3 = got dtbSize
	dtbEnd := dtbStart + dtbSize

	if !registerMmapSpan(dtbStart, dtbEnd) {
		for {} // Hang
	}
	*(*uint32)(unsafe.Pointer(uartBase)) = '4' // 4 = registered DTB

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

//go:nosplit
func uartPutHex64(val uint64) {
	// Write hex digits using uartPutc (which checks if UART is enabled)
	writeHexDigit := func(digit uint32) {
		if digit < 10 {
			uartPutc(byte('0' + digit))
		} else {
			uartPutc(byte('A' + digit - 10))
		}
	}

	writeHexDigit(uint32((val >> 60) & 0xF))
	writeHexDigit(uint32((val >> 56) & 0xF))
	writeHexDigit(uint32((val >> 52) & 0xF))
	writeHexDigit(uint32((val >> 48) & 0xF))
	writeHexDigit(uint32((val >> 44) & 0xF))
	writeHexDigit(uint32((val >> 40) & 0xF))
	writeHexDigit(uint32((val >> 36) & 0xF))
	writeHexDigit(uint32((val >> 32) & 0xF))
	writeHexDigit(uint32((val >> 28) & 0xF))
	writeHexDigit(uint32((val >> 24) & 0xF))
	writeHexDigit(uint32((val >> 20) & 0xF))
	writeHexDigit(uint32((val >> 16) & 0xF))
	writeHexDigit(uint32((val >> 12) & 0xF))
	writeHexDigit(uint32((val >> 8) & 0xF))
	writeHexDigit(uint32((val >> 4) & 0xF))
	writeHexDigit(uint32(val & 0xF))
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

// printHex8 outputs a uint8 as a 2-digit hex string via print()
//
//go:nosplit
func printHex8(val uint8) {
	nibbleHi := (val >> 4) & 0xF
	nibbleLo := val & 0xF
	if nibbleHi < 10 {
		printChar(byte('0' + nibbleHi))
	} else {
		printChar(byte('A' + nibbleHi - 10))
	}
	if nibbleLo < 10 {
		printChar(byte('0' + nibbleLo))
	} else {
		printChar(byte('A' + nibbleLo - 10))
	}
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
func uartPutHex8(val uint8) {
	// Write 2 hex digits for a byte
	writeHexDigit := func(digit uint32) {
		if digit < 10 {
			uartPutc(byte('0' + digit))
		} else {
			uartPutc(byte('A' + digit - 10))
		}
	}

	writeHexDigit(uint32((val >> 4) & 0xF))
	writeHexDigit(uint32(val & 0xF))
}

// checkSPAlignment checks if SP is 16-byte aligned and prints diagnostic info
// Returns true if aligned, false if misaligned
// This function must be nosplit and use minimal stack
//
//go:nosplit
func checkSPAlignment(context string) bool {
	sp := asm.GetCallerStackPointer()
	aligned := (sp & 0xF) == 0
	return aligned
}

// checkSPAlignmentSilent checks if SP is 16-byte aligned without printing
// Returns true if aligned, false if misaligned
//
//go:nosplit
func checkSPAlignmentSilent() bool {
	sp := asm.GetCallerStackPointer()
	return (sp & 0xF) == 0
}

// printSPBreadcrumb prints a breadcrumb with label and SP value
// Format: "[label] SP=0xXXXXXXXX\r\n"
// Uses printChar for all characters via the ring buffer path
//
//go:nosplit
func printSPBreadcrumb(label byte) {
	// Get SP BEFORE any function calls to avoid corruption
	sp := asm.GetCallerStackPointer()

	printChar('[')
	printChar(label)
	printChar(']')
	printChar(' ')
	printChar('S')
	printChar('P')
	printChar('=')
	printChar('0')
	printChar('x')
	printHex64(uint64(sp))
	printChar('\r')
	printChar('\n')
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

// printMemSize formats and displays memory size in a human-readable format
// Displays as MB or GB depending on size
//
//go:nosplit
func printMemSize(sizeBytes uint32) {
	// Convert to MB (dividing by 1024*1024)
	sizeMB := sizeBytes / (1024 * 1024)

	if sizeMB >= 1024 {
		// Display as GB
		sizeGB := sizeMB / 1024
		printUint32(sizeGB)
		print(" GB")
	} else {
		// Display as MB
		printUint32(sizeMB)
		print(" MB")
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

// SimpleTestKernel is a minimal test kernel for fast UART debugging
// Just initializes UART, writes a string, and exits via semihosting
//
//go:nosplit
//go:noinline
func SimpleTestKernel() {
	// Initialize UART
	uartInit()

	// Write test string
	print("UART Test: Hello from simplified kernel!\r\n")

	// Exit via semihosting
	print("Exiting via semihosting\r\n")
	asm.QemuExit()
}

// kernelMainInternal is the entry point called from boot.s via ABI stub KernelMain
// For bare metal, we ensure it's not optimized away
//
//go:noinline
func kernelMainInternal(r0, r1, atags uint32) {
	// VERY EARLY breadcrumb - before any complex operations
	uartBase := uintptr(0x09000000)
	*(*uint32)(unsafe.Pointer(uartBase)) = 0x4B // 'K' = Entered KernelMain

	_ = r0
	_ = r1

	// On QEMU virt, the DTB pointer is passed in as the "atags" parameter (low 32 bits).
	// boot.s captures QEMU's reset-time x0 and passes it through to kernel_main in x2.
	setDTBPtr(uintptr(atags))
	*(*uint32)(unsafe.Pointer(uartBase)) = 0x44 // 'D' = setDTBPtr done

	// Initialize UART first for early debugging
	uartInit()
	*(*uint32)(unsafe.Pointer(uartBase)) = 0x55 // 'U' = uartInit done

	// Check SCTLR_EL1 for alignment check bit
	sctlr := asm.ReadSctlrEl1()
	alignCheck := (sctlr & 2) != 0 // Bit 1: A - Alignment Check Enable

	// Disable alignment check if enabled (required for Go runtime)
	if alignCheck {
		asm.DisableAlignmentCheck()
	}
	*(*uint32)(unsafe.Pointer(uartBase)) = 0x41 // 'A' = Alignment check done

	// Initialize minimal runtime structures for write barrier
	initRuntimeStubs()
	*(*uint32)(unsafe.Pointer(uartBase)) = 0x52 // 'R' = Runtime stubs done

	// CRITICAL: Set up exception vectors BEFORE enabling MMU
	// If MMU enable causes an exception, we need a valid handler
	// Use assembly helper to get exception vector address without accessing .rodata
	exceptionVectorAddr := asm.GetExceptionVectorsAddr()
	asm.SetVbarEl1ToAddr(exceptionVectorAddr)
	*(*uint32)(unsafe.Pointer(uartBase)) = 0x56 // 'V' = VBAR set

	// Initialize MMU (required before heap - enables Normal memory for unaligned access)
	*(*uint32)(unsafe.Pointer(uartBase)) = 0x4D // 'M' = Starting MMU init
	if !initMMU() {
		for {
		}
	}
	*(*uint32)(unsafe.Pointer(uartBase)) = 0x6D // 'm' = initMMU done

	*(*uint32)(unsafe.Pointer(uartBase)) = 0x45 // 'E' = Starting enableMMU
	if !enableMMU() {
		for {
		}
	}
	*(*uint32)(unsafe.Pointer(uartBase)) = 0x65 // 'e' = enableMMU done
	*(*uint32)(unsafe.Pointer(uartBase)) = 0x56 // 'V' = about to verify globals

	// ==========================================================================
	// CRITICAL: Verify all globals used by page fault handler are pre-mapped
	// ==========================================================================
	// This MUST be done immediately after MMU is enabled, before any code path
	// that could trigger demand paging. If any critical global is not mapped,
	// the page fault handler will itself trigger a nested page fault, causing
	// an infinite loop or crash.
	//
	// The verification function walks page tables to check that:
	// - pageTableL0 pointer
	// - physFrameAllocatorState_global
	// - totalKernelPages_global
	// - inPageFaultHandler_global (re-entrancy guard)
	// - mmapSpans array
	// are all in identity-mapped BSS/data sections.
	//
	*(*uint32)(unsafe.Pointer(uartBase)) = 'v' // 'v' = calling VerifyCriticalGlobalsMapped
	if !VerifyCriticalGlobalsMapped() {
		// Verification failed - halt immediately
		uartPutsDirect("FATAL: Critical globals not mapped. Cannot proceed.\r\n")
		for {
		}
	}
	*(*uint32)(unsafe.Pointer(uartBase)) = 'P' // 'P' = globals verified, proceeding

	// Set physPageSize before schedinit (needed by mallocinit which schedinit calls)
	// Normally this would be set by sysauxv from AT_PAGESZ auxiliary vector
	physPageSizeAddr := asm.GetPhysPageSizeAddr()
	*(*uint32)(unsafe.Pointer(uartBase)) = 'Q' // Q = got physPageSizeAddr
	uartPutHex64Direct(uint64(physPageSizeAddr))
	*(*uint32)(unsafe.Pointer(uartBase)) = ':'
	*(*uint32)(unsafe.Pointer(uartBase)) = 'a' // a = about to check mapping
	// Check if the address is mapped
	pa := getPhysicalAddress(physPageSizeAddr)
	*(*uint32)(unsafe.Pointer(uartBase)) = 'b' // b = got PA
	uartPutHex64Direct(uint64(pa))
	*(*uint32)(unsafe.Pointer(uartBase)) = 'c' // c = printed PA
	if pa == 0 {
		uartPutsDirect("UNMAPPED!\r\n")
		// physPageSize is in runtime BSS which should be in our .bss
		// It's not being mapped properly - let's skip for now
	} else {
		*(*uint32)(unsafe.Pointer(uartBase)) = 'd' // d = about to write
		// Try reading first to see if reads work
		readVal := *(*uint64)(unsafe.Pointer(physPageSizeAddr))
		*(*uint32)(unsafe.Pointer(uartBase)) = 'r' // r = read worked
		uartPutHex64Direct(readVal)
		*(*uint32)(unsafe.Pointer(uartBase)) = 'w' // w = about to write
		// Try a simple store to a local variable first (on stack)
		var testVar uint64
		testVar = 4096
		*(*uint32)(unsafe.Pointer(uartBase)) = 't' // t = stack write worked
		_ = testVar
		// SKIP memory write for now
		*(*uint32)(unsafe.Pointer(uartBase)) = 'e' // e = skipped write
	}
	*(*uint32)(unsafe.Pointer(uartBase)) = 'R' // R = wrote physPageSize

	// NOTE: VirtIO RNG initialization moved to after schedinit() to allow print() usage
	// schedinit() will use fake random data via getFakeRandomBytes() until RNG is initialized

	// Map PL031 RTC MMIO region before accessing it
	// NOTE: RTC is already mapped during initMMU, skip redundant mapping
	*(*uint32)(unsafe.Pointer(uartBase)) = 'S' // S = RTC region skipped

	// NOTE: DTB region, stacks, and cardinal sections are already mapped during initMMU()
	// No need to re-map them here
	*(*uint32)(unsafe.Pointer(uartBase)) = 'T' // T = regions ready

	// =========================================
	// PRE-REGISTER FIXED MEMORY SPANS
	// TEMP: Skip preRegisterFixedSpans - BSS writes fail
	*(*uint32)(unsafe.Pointer(uartBase)) = 'U' // U = skipping preRegister
	// preRegisterFixedSpans()
	*(*uint32)(unsafe.Pointer(uartBase)) = 'u' // u = skipped preRegister

	// =========================================
	// POST-MMU DEVICE INITIALIZATION
	// 0. Parse device tree
	*(*uint32)(unsafe.Pointer(uartBase)) = 'D' // D = device tree
	initDeviceTree()
	*(*uint32)(unsafe.Pointer(uartBase)) = 'd' // d = device tree done

	// 1. Initialize GIC
	*(*uint32)(unsafe.Pointer(uartBase)) = 'G' // G = GIC
	gicInit()
	*(*uint32)(unsafe.Pointer(uartBase)) = 'g' // g = GIC done

	// 2. Initialize UART ring buffer
	*(*uint32)(unsafe.Pointer(uartBase)) = 'B' // B = buffer
	uartInitRingBufferAfterMemInit()
	*(*uint32)(unsafe.Pointer(uartBase)) = 'b' // b = buffer done

	// 3. Initialize VirtIO RNG - SKIPPED (BSS writes hang)
	*(*uint32)(unsafe.Pointer(uartBase)) = 'N' // N = RNG
	// initVirtIORNG() // SKIP: BSS writes cause hang after MMU enable
	*(*uint32)(unsafe.Pointer(uartBase)) = 'n' // n = RNG skipped

	// 4. PL031 RTC already mapped, skip

	// 5. Enable interrupts
	*(*uint32)(unsafe.Pointer(uartBase)) = 'I' // I = IRQ
	asm.EnableIrqs()
	*(*uint32)(unsafe.Pointer(uartBase)) = 'i' // i = IRQ enabled

	// 6. Test print() with ring buffer
	// Put a test character in the ring buffer
	uartPutc('T')
	uartPutc('E')
	uartPutc('S')
	uartPutc('T')
	uartPutc('\r')
	uartPutc('\n')
	// Drain the ring buffer manually (interrupts may not fire immediately)
	for i := 0; i < 10; i++ {
		uartDrainRingBuffer()
	}

	uartPutsDirect("A1\r\n") // Before print() test

	// TEST: Go Assembly Transpilation (goasm2gnu) - REMOVED
	// The goasm_test.s file was removed as part of assembly reorganization.
	// Go assembly transpilation is verified through normal kernel operation.

	// =========================================
	// DEBUG: Dump page table walk for key addresses
	// Verify that .rodata is RO and BSS is RW
	// =========================================
	// .rodata sample - should be RO (AP=3)
	dumpPageTableWalk(".rodata sample (0x401C0000)", 0x401C0000)
	// runtime.argv is at 0x402BB918 (in BSS) - should be RW (AP=1)
	dumpPageTableWalk("runtime.argv (0x402BB918)", 0x402BB918)
	// runtime.argc is at 0x402E4B68 (in BSS) - should be RW (AP=1)
	dumpPageTableWalk("runtime.argc (0x402E4B68)", 0x402E4B68)

	// =========================================
	// TEST: Item 3 - runtime.args()
	// Test that we can call runtime.args with a minimal argv/auxv structure
	// This verifies the args() → sysargs() → sysauxv() path works.
	// =========================================
	uartPutsDirect("A2\r\n") // Before runtime.args
	result := asm.CallRuntimeArgs()
	uartPutsDirect("A3\r\n") // After runtime.args
	if result != 0 {
		for {
		}
	}
	uartPutsDirect("A4\r\n") // After result check

	// =========================================
	// Initialize timer for preemptive scheduling
	// Timer fires every 20ms and calls asyncPreemptBM to preempt goroutines
	// =========================================
	timerInit()

	// DEBUG: Verify timer is enabled after timerInit
	uartPutsDirect("After timerInit: CTL=0x")
	uartPutHex32Direct(asm.ReadCntvCtlEl0())
	uartPutsDirect("\r\n")

	// =========================================
	// Initialize thread table before loading kmazarin
	// This sets up M0 as thread 0 for clone/futex/nanosleep syscall handling
	// =========================================
	InitThreads()

	// =========================================
	// Load and run kmazarin
	// All devices initialized, runtime.args() passed, ready to load kmazarin
	// =========================================
	loadAndRunKmazarin()

	// Should never return
	for {
	}

	/*
	// =========================================
	// TEST: Item 4a - Direct syscall test (SKIPPED)
	// Before calling runtime.osinit, test our syscalls directly
	// =========================================
	print("Testing Item 4a: sched_getaffinity syscall... ")
	var cpuMask [128]byte
	result2 := SyscallSchedGetaffinity(0, uint64(len(cpuMask)), unsafe.Pointer(&cpuMask[0]))
	if result2 == 8 && cpuMask[0] == 0x01 {
		print("PASS\r\n")
	} else {
		print("FAIL\r\n")
	}

	print("Testing Item 4b: openat syscall (expected path)... ")
	expectedPathBytes := []byte("/sys/kernel/mm/transparent_hugepage/hpage_pmd_size\x00")
	result3 := SyscallOpenat(-100, unsafe.Pointer(&expectedPathBytes[0]), 0, 0)
	if result3 == -2 { // -ENOENT for the expected path
		print("PASS\r\n")
	} else {
		print("FAIL (got ")
		print(int(result3))
		print(")\r\n")
	}

	print("Testing Item 4b2: openat syscall (unexpected path - should show warning)... ")
	unexpectedPathBytes := []byte("/etc/passwd\x00") // Truly unexpected path
	result4 := SyscallOpenat(-100, unsafe.Pointer(&unexpectedPathBytes[0]), 0, 0)
	// Should print warning about unexpected path and return an error
	if result4 < 0 { // Any error is acceptable
		print("PASS (returned error as expected)\r\n")
	} else {
		print("FAIL (should return error for unexpected path, got fd=")
		print(int(result4))
		print(")\r\n")
	}

	// =========================================
	// TEST: Item 4c - runtime.osinit()
	// Now that we have a 64KB g0 stack (matching real runtime),
	// this should work without hitting stack guard
	// =========================================
	print("Testing Item 4c: runtime.osinit()... ")
	asm.CallRuntimeOsinit()
	print("PASS\r\n")

	// =========================================
	// TEST: Item 5 - runtime.schedinit()
	// Initialize Go scheduler
	//
	// NOTE: g0 and m0 are initialized in boot.s (assembly) before kernel_main runs,
	// just like the Go runtime's rt0_go does. This ensures x28 points to runtime.g0
	// and the scheduler infrastructure exists before schedinit is called.
	//
	// During schedinit, locks use futex which uses STUB behavior (no real blocking)
	// because there's only g0 and no other runnable goroutines yet.
	//
	// schedinit will:
	// - Call lockInit() for all runtime locks (uses futex with stub gopark)
	// - Initialize scheduler structures
	// - Set up processor (P) structures
	// - Initialize system monitor
	// =========================================

	// DEBUG: Pre-map the 64KB boundary page to prevent hang at fault #17
	// This is a workaround to test if the issue is related to demand paging at 64KB boundaries
	// DISABLED: Testing cache coherency fix instead
	//print("Pre-mapping 64KB boundary page (0x4000010000)... ")
	//preMapPages()
	//print("DONE\r\n")

	// CHANGED: Skip full runtime.schedinit() in cardinal
	// Cardinal is a bootloader, not a full Go program - it doesn't need GC or full heap
	// Instead, use simple kmalloc/kfree for minimal allocation needs

	// CRITICAL: Must call initKmallocHeap() BEFORE heapInit() to calculate KMALLOC_HEAP_BASE
	// initKmallocHeap() reads linker symbols to determine heap boundaries
	initKmallocHeap()

	// Use existing heapInit (already implemented in heap.go)
	heapInit(KMALLOC_HEAP_BASE)

	// NOTE: Runtime structures (MMU allocators, TLS, P, mcache, exception vectors)
	// are now BSS globals to avoid circular dependency with heap initialization

	// Just call osinit to set ncpu (doesn't allocate memory)
	asm.CallRuntimeOsinit()

	// NOTE: We skip runtime.schedinit() and mallocinit() entirely
	// kmazarin (the actual kernel) will do full Go runtime initialization

	// Initialize VirtIO RNG device for random number generation
	// NOTE: Moved here (after schedinit) to allow print() usage in initialization code
	// schedinit() used fake random data via getFakeRandomBytes() during initialization
	// DISABLED: Crashes during PCI config write return - see commit afc16d2
	// print("\r\nInitializing VirtIO RNG...\r\n")
	// initVirtIORNG()

	// Initialize max stack size (normally done in runtime.main, but we don't run that)
	// Max stack size is 1 GB on 64-bit, 250 MB on 32-bit
	// Using decimal instead of binary GB and MB because they look nicer in stack overflow messages
	const ptrSize = 8 // ARM64 is 64-bit
	var stackSize uintptr
	if ptrSize == 8 {
		stackSize = 1000000000 // 1 GB
	} else {
		stackSize = 250000000 // 250 MB
	}
	setMaxstacksize(stackSize)
	setMaxstackceiling(2 * stackSize)

	// Mark scheduler as ready - futex can now use real gopark/goready
	MarkSchedulerReady()

	// NOTE: parseEmbeddedKmazarin() will be called from simpleMain() (user goroutine)
	// because debug/elf uses defer, which isn't allowed on the system stack (g0)

	// =========================================
	// Initialize Timer-Based Preemption System
	// =========================================
	// SKIP TIMER/GOROUTINE TESTS IN CARDINAL
	// =========================================
	// Cardinal is a bootloader, not a full Go program
	// It doesn't need timers, GC, scheduler, or goroutines
	// All of that will be initialized in kmazarin (the actual kernel)
	//
	// Skipping:
	// - Timer initialization (requires interrupts and runtime heap)
	// - GC/scheduler monitors (require full runtime)
	// - Goroutine tests (require scheduler)
	//
	// Instead, go straight to loading and starting kmazarin
	print("\r\n═══════════════════════════════════════════════\r\n")
	print("cardinal: Skipping runtime tests (cardinal is bootloader only)\r\n")
	print("cardinal: Ready to load kmazarin\r\n")
	print("═══════════════════════════════════════════════\r\n\r\n")

	// TODO: Add kmazarin loading code here
	// For now, just print success and halt
	print("cardinal initialization COMPLETE!\r\n")
	print("TODO: Load kmazarin from storage and transfer control\r\n\r\n")

	// SKIP goroutine/scheduler code (requires full runtime)
	//print("Creating goroutine for simpleMain...\r\n")
	//asm.CallNewprocSimpleMain()
	//print("Goroutine created, starting scheduler...\r\n")
	//print("Calling runtime.mstart()...\r\n")
	//asm.CallRuntimeMstart()
	*/

	// Cardinal init complete - load and run kmazarin
	loadAndRunKmazarin()

	// Should never return
	for {
	}

	// Should never reach here
	print("ERROR: mstart returned - should never happen!\r\n")
	for {
	}

	// Initialize kernel stack info for Go runtime stack checks
	initKernelStack()

	// Initialize memory management
	memInit(0) // No ATAGs in QEMU, pass 0

	// Verify mcache.alloc[] is still valid after memInit
	mcacheStructAddr := uintptr(0x41020000)
	allocArrayStart := mcacheStructAddr + 0x30
	expectedEmptymspan := uint64(asm.GetEmptymspanAddr()) // Get address dynamically
	if readMemory64(allocArrayStart+47*8) != expectedEmptymspan {
		// Reinitialize if corrupted
		for i := uintptr(0); i < 136; i++ {
			writeMemory64(allocArrayStart+i*8, expectedEmptymspan)
		}
	}

	// Create main kernel goroutine
	mainG := createKernelGoroutine(nil, KERNEL_GOROUTINE_STACK_SIZE)
	if mainG == nil {
		print("FATAL: Failed to create main goroutine\r\n")
		for {
		}
	}

	// Store mainG in global before switching stacks
	mainKernelGoroutine = mainG
	mainG.startpc = 0
	mainG.sched.pc = 0

	// Switch to main goroutine stack
	asm.SwitchToGoroutine(unsafe.Pointer(mainG))

	// Update m0.curg to point to mainG
	m0Addr := asm.GetM0Addr()
	mainGFromGlobal := mainKernelGoroutine
	curgOffset := unsafe.Offsetof(runtimeM{}.curg)
	writeMemory64(m0Addr+curgOffset, uint64(uintptr(unsafe.Pointer(mainGFromGlobal))))

	// Call the main kernel body
	kernelMainBodyWrapper()

	// Should never return
	print("FATAL: Unexpected return from kernel\r\n")
	for {
	}
}

// kernelMainBodyWrapper is called from assembly after switching to the new goroutine's stack
//
//go:noinline
func kernelMainBodyWrapper() {
	kernelMainBody()
}

// kernelMainBody performs the full initialization sequence on a regular stack.
//
// KernelMainBody is the exported entry point for the main kernel goroutine
// This is called from assembly after switching to the main goroutine's stack
// Note: Go exports this as main.KernelMainBody (package.function)
//
//go:linkname KernelMainBody main.KernelMainBody
//go:noinline
func KernelMainBody() {
	kernelMainBody()
}

//go:noinline
func kernelMainBody() {
	// Staged kernel bring-up
	// Early stages use UART for breadcrumbs (before framebuffer)
	// Later stages use framebuffer for user-facing status

	// Stage 0: UART initialization (required for early debugging)
	uartInit()

	// Stage 1: write barrier flag check (critical for Go runtime)
	wbFlagAddr := getLinkerSymbol("runtime.writeBarrier")
	wbFlag := readMemory32(wbFlagAddr)
	if wbFlag == 0 {
		print("ERROR: Write barrier flag not set!\r\n")
	}

	// Memory barrier for write barrier operations
	asm.Dsb()

	// Stage 3: exception handler init - now done early in KernelMain()

	// Stage 4: MMU already initialized in KernelMain
	asm.DisableIrqs()

	// NOTE: Device tree parsing moved to earlier in KernelMain() (before PCI access)
	// initDeviceTree() is now called at line 714, before any PCI scanning

	// Stage 5: Framebuffer initialization
	fbResult := framebufferInit()
	if fbResult != 0 {
		print("WARNING: Framebuffer init failed\r\n")
	} else {
		// Initialize framebuffer text rendering
		if err := InitFramebufferText(fbinfo.Buf, fbinfo.Width, fbinfo.Height, fbinfo.Pitch); err != nil {
			print("WARNING: Framebuffer text init failed\r\n")
		} else {
			// Render boot splash screen
			testFramebufferText()

			// Verify heap works with make()
			testSlice := make([]byte, 100)
			if testSlice == nil {
				print("ERROR: heap allocation failed\r\n")
			}

			// Render gg startup circle (temporarily disabled for channel testing)
			// drawGGStartupCircle()
		}
	}
	// Framebuffer is now ready - use it for boot status messages
	// UART is now reserved for debug breadcrumbs only (via print())

	// Stage 6: UART ring buffer initialization
	FramebufferPuts("Initializing UART...\r\n")
	uartInitRingBufferAfterMemInit()

	// Stage 8: GIC init (interrupt controller)
	FramebufferPuts("Initializing interrupts...\r\n")
	gicInit()

	// Set up UART TX interrupts for interrupt-driven output
	uartSetupInterrupts()

	// Stage 9: Timer init
	FramebufferPuts("Initializing timer...\r\n")
	timerInit()

	// Stage 9b: Thread table init (for clone/futex/nanosleep syscall handling)
	FramebufferPuts("Initializing thread table...\r\n")
	InitThreads()

	// Stage 10: SDHCI init (SD card controller)
	FramebufferPuts("Initializing SD card...\r\n")
	if !sdhciInit() {
		FramebufferPuts("FATAL: SD card init failed!\r\n")
		abortBoot("sdhciInit failed - cannot load kernel from SD card!")
	}

	// Stage 11a: Test Go heap allocation
	FramebufferPuts("Testing Go heap allocation...\r\n")
	testSlice := make([]byte, 100) // Simple heap allocation test
	if testSlice == nil {
		FramebufferPuts("FATAL: Go heap allocation failed!\r\n")
		for {
		}
	}
	testSlice[0] = 42 // Write to allocated memory
	if testSlice[0] != 42 {
		FramebufferPuts("FATAL: Go heap read/write failed!\r\n")
		for {
		}
	}
	FramebufferPuts("Go heap allocation OK!\r\n")

	// Stage 11b: Create Go channel (testing real Go channel allocation)
	FramebufferPuts("Creating Go channel...\r\n")
	goSignalChan = make(chan struct{}, 10) // Real Go channel with buffer
	if goSignalChan == nil {
		FramebufferPuts("FATAL: Failed to create Go channel\r\n")
		for {
		}
	}

	// Stage 11c: Create SimpleChannel for interrupt handler (still needed)
	ch := createSimpleChannel()
	if ch == nil {
		FramebufferPuts("FATAL: Failed to create SimpleChannel\r\n")
		for {
		}
	}
	simpleSignalChan = ch // Store globally for interrupt handler

	// NOTE: The code below is unreachable because mstart() never returns.
	// Interrupts are actually enabled in initTime() before the scheduler starts.
	// This is left here as documentation of the original intent.
	//
	// FramebufferPuts("Boot complete.\r\n")
	// asm.EnableIrqsAsm()  // DEAD CODE - interrupts already enabled in initTime()
	// FramebufferPuts("Interrupts enabled.\r\n")
}

// timerListenerLoop runs an endless loop waiting for timer signals on the global channel.
// Tests both SimpleChannel (from interrupt) and Go channel (from goroutine context).
//
//go:noinline
func timerListenerLoop() {
	print("goroutine: testing Go channel...\n")
	// Drain output
	for i := 0; i < 100; i++ {
		uartDrainRingBuffer()
	}

	// Test Go channel: send and receive from same goroutine
	goSignalChan <- struct{}{} // Send to Go channel
	<-goSignalChan             // Receive from Go channel
	print("Go channel send/receive works!\n")
	for i := 0; i < 100; i++ {
		uartDrainRingBuffer()
	}

	// Now wait for timer signals using SimpleChannel (from interrupt handler)
	for {
		simpleSignalChan.receive() // Block until timer sends a signal
		print("bong\n")
		// Drain the bong output
		for i := 0; i < 100; i++ {
			uartDrainRingBuffer()
		}
	}
}

// testFramebufferText tests the framebuffer text rendering system
//
//go:nosplit
func testFramebufferText() {
	// Render the boot image along right edge
	imageData := GetBootMazarinImageData()
	if imageData != nil {
		RenderImageData(imageData, 128, 0, false)
	}

	FramebufferPuts("===== Mazarin Kernel =====\r\n")
	FramebufferPuts("Framebuffer Text Output Ready\r\n")
	FramebufferPuts("\r\n")
	FramebufferPuts("Display: 1024x768 pixels\r\n")
	FramebufferPuts("Format: XRGB8888 (32-bit)\r\n")
}

// drawTestPattern draws a simple test pattern to the framebuffer
// This helps verify that VNC display is working correctly
// Uses XRGB8888 format (32-bit pixels: 0x00RRGGBB)
//
//go:nosplit
func drawTestPattern() {
	if fbinfo.Buf == nil {
		return
	}

	// Get framebuffer as 32-bit pixel array (XRGB8888 format)
	// XRGB8888 format: [X:8][R:8][G:8][B:8] = 0x00RRGGBB
	testPixels32 := (*[1 << 28]uint32)(fbinfo.Buf)

	// Draw colored rectangles across the screen
	// Each rectangle is 256 pixels wide (1024/4 = 256)

	// Red rectangle (left quarter) - XRGB8888: 0x00FF0000
	for y := uint32(0); y < fbinfo.Height; y++ {
		for x := uint32(0); x < fbinfo.Width/4; x++ {
			offset := y*fbinfo.Width + x
			testPixels32[offset] = 0x00FF0000 // Red (R=FF, G=00, B=00)
		}
	}

	// Green rectangle (second quarter) - XRGB8888: 0x0000FF00
	for y := uint32(0); y < fbinfo.Height; y++ {
		for x := uint32(fbinfo.Width / 4); x < fbinfo.Width/2; x++ {
			offset := y*fbinfo.Width + x
			testPixels32[offset] = 0x0000FF00 // Green (R=00, G=FF, B=00)
		}
	}

	// Blue rectangle (third quarter) - XRGB8888: 0x000000FF
	for y := uint32(0); y < fbinfo.Height; y++ {
		for x := uint32(fbinfo.Width / 2); x < (fbinfo.Width*3)/4; x++ {
			offset := y*fbinfo.Width + x
			testPixels32[offset] = 0x000000FF // Blue (R=00, G=00, B=FF)
		}
	}

	// White rectangle (right quarter) - XRGB8888: 0x00FFFFFF
	for y := uint32(0); y < fbinfo.Height; y++ {
		for x := uint32((fbinfo.Width * 3) / 4); x < fbinfo.Width; x++ {
			offset := y*fbinfo.Width + x
			testPixels32[offset] = 0x00FFFFFF // White (R=FF, G=FF, B=FF)
		}
	}

	// Draw a yellow cross in the center - XRGB8888: 0x00FFFF00 (Yellow = Red + Green)
	centerX := fbinfo.Width / 2
	centerY := fbinfo.Height / 2

	// Horizontal line (20 pixels thick)
	for y := centerY - 10; y < centerY+10 && y < fbinfo.Height; y++ {
		for x := uint32(0); x < fbinfo.Width; x++ {
			offset := y*fbinfo.Width + x
			testPixels32[offset] = 0x00FFFF00 // Yellow (R=FF, G=FF, B=00)
		}
	}

	// Vertical line (20 pixels thick)
	for y := uint32(0); y < fbinfo.Height; y++ {
		for x := centerX - 10; x < centerX+10 && x < fbinfo.Width; x++ {
			offset := y*fbinfo.Width + x
			testPixels32[offset] = 0x00FFFF00 // Yellow (R=FF, G=FF, B=00)
		}
	}
}

// =================================================================
// Simple goroutine/channel test - runs as main goroutine
// =================================================================

// simpleMain is the entry point for our simple goroutine/channel test
// This will be run by the scheduler as the main goroutine
//
// Modified to test preemption: both g1 and g2 busy-wait concurrently
// We should see '1' and '2' characters interleaved as scheduler switches between them
//
// NOTE: Removed //go:nosplit because this function now calls parseEmbeddedKmazarin()
// which uses debug/elf and needs stack growth capability
func simpleMain() {
	print("\r\n[g1] Simple main started!\r\n")

	// Parse and display embedded kmazarin ELF information
	// NOTE: Must be done from user goroutine because debug/elf uses defer
	parseEmbeddedKmazarin()

	print("\r\n[g1] ELF parsing complete\r\n")

	// Load and run the kmazarin kernel
	loadAndRunKmazarin()

	// Should never reach here
	print("\r\n[g1] ERROR: returned from kmazarin - should never happen!\r\n")
	asm.QemuExit()
}

// simpleGoroutine2 is the second goroutine for the preemption test
// Pure busy-wait with NO cooperative yielding
//
//go:nosplit
func simpleGoroutine2(ch chan string) {
	print("[g2] Started, entering busy-wait loop (NO yielding)...\r\n")

	uartBase := getLinkerSymbol("__uart_base")

	// Infinite busy-wait loop to test timer-based preemption
	// NO calls to Gosched() - the timer interrupt must forcibly preempt us
	counter := uint64(0)

	for {
		counter++
		// Every million iterations, print our marker
		if counter%1000000 == 0 {
			// Print '2' to show g2 is running
			asm.MmioWrite(uartBase, uint32('2'))
			// NO checkPreemption() call - pure busy-wait!
		}
	}
}

// =================================================================

// testTraceback tests the exception handler traceback by deliberately causing a crash
// This jumps to an invalid address to trigger a prefetch abort exception
//
//go:noinline
func testTraceback() {
	print("\r\n=== Testing Exception Traceback ===\r\n")
	print("About to trigger a prefetch abort by jumping to NULL...\r\n")

	// Call a helper function to create a deeper call stack for the traceback
	testTracebackHelper1()

	// Should never reach here
	print("ERROR: Should not reach here!\r\n")
}

//go:noinline
func testTracebackHelper1() {
	print("In testTracebackHelper1\r\n")
	testTracebackHelper2()
}

//go:noinline
func testTracebackHelper2() {
	print("In testTracebackHelper2\r\n")
	testTracebackHelper3()
}

//go:noinline
func testTracebackHelper3() {
	print("In testTracebackHelper3 - about to crash!\r\n")

	// Jump to NULL address - this will cause a prefetch abort exception
	// We do this via assembly to avoid compiler optimizations
	jumpToNull()
}

// jumpToNull is implemented in assembly to jump to address 0
// This will trigger a prefetch abort exception
//
//go:linkname jumpToNull jump_to_null
//go:nosplit
func jumpToNull()

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

// dumpAllGs uses runtime internals to dump the allgs slice
//
//go:nosplit
func dumpAllGs() {
	// Runtime symbol addresses (use 'make update-runtime-addrs' to refresh)
	// To manually update: nm flash/cardinal.elf | grep 'runtime.allg'
	const allglenAddr = uintptr(0x406a32f0) // runtime.allglen
	const allgsAddr = uintptr(0x40679db0)   // runtime.allgs

	// Read allglen (number of goroutines)
	allglen := uintptr(readMemory64(allglenAddr))
	print("  allglen = ")
	printHex64(uint64(allglen))
	print("\r\n")

	if allglen == 0 {
		print("  allgs is empty!\r\n")
		return
	}

	// Read allgs slice header
	// A slice is: {ptr *elem, len int, cap int}
	allgsPtr := readMemory64(allgsAddr)      // pointer to array
	allgsLen := readMemory64(allgsAddr + 8)  // length
	allgsCap := readMemory64(allgsAddr + 16) // capacity

	print("  allgs ptr=")
	printHex64(allgsPtr)
	print(" len=")
	printHex64(allgsLen)
	print(" cap=")
	printHex64(allgsCap)
	print("\r\n")

	if allgsPtr == 0 {
		print("  allgs backing array is NULL!\r\n")
		return
	}

	// Iterate through allgs array
	print("  Goroutines in allgs:\r\n")
	for i := uint64(0); i < 20 && i < allgsLen; i++ { // Limit to first 20
		gpAddr := readMemory64(uintptr(allgsPtr + i*8)) // Each entry is a *g (8 bytes)
		print("    [")
		printHex64(i)
		print("] g=0x")
		printHex64(gpAddr)

		if gpAddr == 0 {
			print(" <NULL>\r\n")
			continue
		}

		// For now, just note it's valid
		print(" (valid)\r\n")
	}

	print("  End of allgs dump\r\n")
}

// parseEmbeddedKmazarin reads the embedded kmazarin ELF binary and displays information
// NOTE: This function requires heap allocation (for debug/elf), so must be called
// after scheduler initialization
func parseEmbeddedKmazarin() {
	print("\r\n=== Embedded Kmazarin ELF Information ===\r\n")

	// Get the embedded kmazarin binary location from linker symbols
	kmazarinStart := getLinkerSymbol("__kmazarin_start")
	kmazarinSize := getLinkerSymbol("__kmazarin_size")

	print("Embedded binary location: 0x")
	printHex64(uint64(kmazarinStart))
	print("\r\n")
	print("Embedded binary size: ")
	printUint32(uint32(kmazarinSize))
	print(" bytes\r\n")

	// Create a byte slice from the embedded binary
	// We need to use unsafe to create a slice from raw memory
	var elfData []byte
	sliceHeader := (*struct {
		Data uintptr
		Len  int
		Cap  int
	})(unsafe.Pointer(&elfData))
	sliceHeader.Data = kmazarinStart
	sliceHeader.Len = int(kmazarinSize)
	sliceHeader.Cap = int(kmazarinSize)

	// Verify we can read the ELF magic bytes
	if len(elfData) < 64 {
		print("ERROR: ELF data too small\r\n")
		return
	}

	// Check ELF magic
	if elfData[0] != 0x7F || elfData[1] != 'E' || elfData[2] != 'L' || elfData[3] != 'F' {
		print("ERROR: Invalid ELF magic bytes\r\n")
		return
	}

	print("Valid ELF magic bytes detected\r\n")

	// Manually parse ELF header (avoiding debug/elf package to prevent defer issues)
	// ELF64 header structure:
	// 0x00-0x03: Magic (0x7F 'E' 'L' 'F')
	// 0x04: Class (1=32-bit, 2=64-bit)
	// 0x05: Data (1=little-endian, 2=big-endian)
	// 0x10-0x11: Type
	// 0x12-0x13: Machine
	// 0x18-0x1F: Entry point (8 bytes for ELF64)

	print("\r\nELF Header:\r\n")
	print("  Class: ")
	if elfData[4] == 1 {
		print("ELF32")
	} else if elfData[4] == 2 {
		print("ELF64")
	} else {
		print("Unknown")
	}
	print("\r\n")

	print("  Data: ")
	if elfData[5] == 1 {
		print("Little-endian")
	} else if elfData[5] == 2 {
		print("Big-endian")
	} else {
		print("Unknown")
	}
	print("\r\n")

	print("  Machine: ")
	machine := uint16(elfData[0x12]) | (uint16(elfData[0x13]) << 8)
	if machine == 0xB7 { // EM_AARCH64
		print("AArch64 (0x00B7)")
	} else {
		print("0x")
		printHex64(uint64(machine))
	}
	print("\r\n")

	print("  Entry point: 0x")
	// Read 8-byte little-endian entry point
	entry := uint64(elfData[0x18]) |
		(uint64(elfData[0x19]) << 8) |
		(uint64(elfData[0x1A]) << 16) |
		(uint64(elfData[0x1B]) << 24) |
		(uint64(elfData[0x1C]) << 32) |
		(uint64(elfData[0x1D]) << 40) |
		(uint64(elfData[0x1E]) << 48) |
		(uint64(elfData[0x1F]) << 56)
	printHex64(entry)
	print("\r\n")

	// Parse program headers (segments) - these tell us what to load into memory
	// ELF64 header offsets:
	// 0x20-0x27: Program header offset (8 bytes)
	// 0x36-0x37: Program header entry size (2 bytes)
	// 0x38-0x39: Program header entry count (2 bytes)
	phoff := uint64(elfData[0x20]) |
		(uint64(elfData[0x21]) << 8) |
		(uint64(elfData[0x22]) << 16) |
		(uint64(elfData[0x23]) << 24) |
		(uint64(elfData[0x24]) << 32) |
		(uint64(elfData[0x25]) << 40) |
		(uint64(elfData[0x26]) << 48) |
		(uint64(elfData[0x27]) << 56)

	phentsize := uint16(elfData[0x36]) | (uint16(elfData[0x37]) << 8)
	phnum := uint16(elfData[0x38]) | (uint16(elfData[0x39]) << 8)

	print("\r\nProgram Headers (segments to load):\r\n")
	print("  Count: ")
	printUint32(uint32(phnum))
	print("\r\n")

	// Parse each program header
	for i := uint16(0); i < phnum && i < 10; i++ {
		offset := phoff + uint64(i)*uint64(phentsize)
		if offset+56 > uint64(len(elfData)) {
			print("  ERROR: Program header offset out of bounds\r\n")
			break
		}

		// Program header structure (ELF64):
		// 0x00-0x03: Type (4 bytes)
		// 0x04-0x07: Flags (4 bytes)
		// 0x08-0x0F: Offset in file (8 bytes)
		// 0x10-0x17: Virtual address (8 bytes)
		// 0x18-0x1F: Physical address (8 bytes)
		// 0x20-0x27: File size (8 bytes)
		// 0x28-0x2F: Memory size (8 bytes)
		// 0x30-0x37: Alignment (8 bytes)

		ptype := uint32(elfData[offset]) |
			(uint32(elfData[offset+1]) << 8) |
			(uint32(elfData[offset+2]) << 16) |
			(uint32(elfData[offset+3]) << 24)

		flags := uint32(elfData[offset+4]) |
			(uint32(elfData[offset+5]) << 8) |
			(uint32(elfData[offset+6]) << 16) |
			(uint32(elfData[offset+7]) << 24)

		poffset := uint64(elfData[offset+8]) |
			(uint64(elfData[offset+9]) << 8) |
			(uint64(elfData[offset+10]) << 16) |
			(uint64(elfData[offset+11]) << 24) |
			(uint64(elfData[offset+12]) << 32) |
			(uint64(elfData[offset+13]) << 40) |
			(uint64(elfData[offset+14]) << 48) |
			(uint64(elfData[offset+15]) << 56)

		vaddr := uint64(elfData[offset+16]) |
			(uint64(elfData[offset+17]) << 8) |
			(uint64(elfData[offset+18]) << 16) |
			(uint64(elfData[offset+19]) << 24) |
			(uint64(elfData[offset+20]) << 32) |
			(uint64(elfData[offset+21]) << 40) |
			(uint64(elfData[offset+22]) << 48) |
			(uint64(elfData[offset+23]) << 56)

		filesz := uint64(elfData[offset+32]) |
			(uint64(elfData[offset+33]) << 8) |
			(uint64(elfData[offset+34]) << 16) |
			(uint64(elfData[offset+35]) << 24) |
			(uint64(elfData[offset+36]) << 32) |
			(uint64(elfData[offset+37]) << 40) |
			(uint64(elfData[offset+38]) << 48) |
			(uint64(elfData[offset+39]) << 56)

		memsz := uint64(elfData[offset+40]) |
			(uint64(elfData[offset+41]) << 8) |
			(uint64(elfData[offset+42]) << 16) |
			(uint64(elfData[offset+43]) << 24) |
			(uint64(elfData[offset+44]) << 32) |
			(uint64(elfData[offset+45]) << 40) |
			(uint64(elfData[offset+46]) << 48) |
			(uint64(elfData[offset+47]) << 56)

		print("\r\n  [")
		printUint32(uint32(i))
		print("] Type: ")

		// PT_LOAD = 1
		if ptype == 1 {
			print("LOAD")
		} else if ptype == 2 {
			print("DYNAMIC")
		} else if ptype == 3 {
			print("INTERP")
		} else if ptype == 4 {
			print("NOTE")
		} else {
			print("0x")
			printHex64(uint64(ptype))
		}

		print("  Flags: ")
		if (flags & 0x1) != 0 {
			print("X")
		} else {
			print("-")
		}
		if (flags & 0x2) != 0 {
			print("W")
		} else {
			print("-")
		}
		if (flags & 0x4) != 0 {
			print("R")
		} else {
			print("-")
		}

		print("\r\n      File offset: 0x")
		printHex64(poffset)
		print("  Vaddr: 0x")
		printHex64(vaddr)
		print("\r\n      Filesz: ")
		printUint32(uint32(filesz))
		print("  Memsz: ")
		printUint32(uint32(memsz))
		print("\r\n")
	}

	print("\r\n=== End of ELF Information ===\r\n\r\n")
}

// setupKmazarinStartupEnv sets up the argc/argv/envp/auxv structure that the Go runtime expects
// Returns: (stackPointer, argc, argv)
//   - stackPointer: pointer to the start of the structure (for SP register)
//   - argc: argument count (for R0 register)
//   - argv: pointer to argv array (for R1 register)
//
// The structure layout in memory (byte offsets):
//   [0]   = argc (int64, value: 1)
//   [8]   = argv[0] (pointer to program name string at offset 120)
//   [16]  = NULL (end of argv)
//   [24]  = NULL (end of envp, empty environment)
//   [32]  = auxv[0].tag (AT_PAGESZ = 6)
//   [40]  = auxv[0].value (4096)
//   [48]  = auxv[1].tag (AT_RANDOM = 25)
//   [56]  = auxv[1].value (pointer to random bytes at offset 136)
//   [64]  = auxv[2].tag (AT_SECURE = 23)
//   [72]  = auxv[2].value (0)
//   [80]  = auxv[3].tag (AT_HWCAP = 16)
//   [88]  = auxv[3].value (HWCAP_ASIMD = 0x2)
//   [96]  = AT_NULL (0)
//   [104] = AT_NULL value (0)
//   [112] = (padding for alignment)
//   [120] = program name string "kmazarin\0"
//   [136] = random bytes (16 bytes, 128 bits)
//
// This matches the Linux kernel's ELF loader behavior
//
//go:nosplit
func setupKmazarinStartupEnv() (stackPointer uintptr, argc uint64, argv uintptr) {
	// Allocate a 1MB stack for kmazarin (256 pages)
	// In Linux, argc/argv/envp/auxv are placed at the TOP of the initial stack
	// IMPORTANT: Go runtime needs substantial stack space for initialization
	const stackPages = 256
	const stackSize = stackPages * 0x1000
	const stackBase = uintptr(0xD0000000)  // Stack base address

	// Allocate and map stack pages
	for i := uintptr(0); i < stackPages; i++ {
		physFrame := allocPhysFrame()
		if physFrame == 0 {
			return 0, 0, 0
		}
		mapPage(stackBase+i*0x1000, physFrame, PTE_ATTR_NORMAL, PTE_AP_RW_EL1, PTE_EXEC_NEVER)
	}

	// IMPORTANT: Skip TLB invalidation! Since this is a NEW mapping (not modifying
	// an existing one), we don't need to invalidate the TLB. Calling InvalidateTlbAll()
	// causes kmazarin to execute prematurely (likely because it invalidates TLB entries
	// for kmazarin's memory space).
	// Just use barriers to ensure the page table update is visible:
	asm.Dsb()  // Ensure page table write completes
	asm.Isb()  // Ensure subsequent instructions see the new mapping

	// Place argc/argv/envp/auxv at the TOP of the stack
	// SP will point to argc, and the structure grows downward
	stackTop := stackBase + stackSize
	structStart := stackTop - 0x200  // Reserve 512 bytes for the structure

	// Zero the structure area
	bzero4K(unsafe.Pointer(structStart), 0x200)

	// Set up the structure as an array of uint64 values
	data := (*[256]uint64)(unsafe.Pointer(structStart))

	// Auxv entries use indices 4-13 (80 bytes for auxv, starting at byte 32)
	// After auxv (112 bytes total for indices 0-13), we place strings and random data:
	// - Program name string at byte offset 120 (after 15 uint64s worth of space for alignment)
	// - Random bytes at byte offset 136

	// Program name string at offset 120 (byte offset, 8-byte aligned)
	progName := (*[16]byte)(unsafe.Pointer(structStart + 120))
	progName[0] = 'k'
	progName[1] = 'm'
	progName[2] = 'a'
	progName[3] = 'z'
	progName[4] = 'a'
	progName[5] = 'r'
	progName[6] = 'i'
	progName[7] = 'n'
	progName[8] = 0

	// Get random bytes for AT_RANDOM at offset 136
	randomBytesAddr := structStart + 136
	getRandomBytes(unsafe.Pointer(randomBytesAddr), 16)

	// argc = 1
	data[0] = 1
	// argv[0] = pointer to program name
	data[1] = uint64(structStart + 120)
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
	data[12] = 0
	data[13] = 0

	// Return values for register setup
	// SP should point to argc (start of structure at top of stack)
	// R0 = argc
	// R1 = pointer to argv (which is at offset 8)
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
	uartPutsDirect("=== Loading Kmazarin ===\r\n")
	uartPutcDirect('a') // breadcrumb: start

	// Get the embedded kmazarin binary location from linker symbols
	uartPutcDirect('b') // breadcrumb: before kmazarin_start
	kmazarinStart := getLinkerSymbol("__kmazarin_start")
	uartPutcDirect('c') // breadcrumb: after kmazarin_start
	uartPutsDirect(" kmazarinStart=0x")
	uartPutHex64Direct(uint64(kmazarinStart))
	uartPutcDirect('d') // breadcrumb: before kmazarin_size
	kmazarinSize := getLinkerSymbol("__kmazarin_size")
	uartPutcDirect('e') // breadcrumb: after kmazarin_size
	uartPutsDirect(" kmazarinSize=0x")
	uartPutHex64Direct(uint64(kmazarinSize))
	uartPutsDirect("\r\n")

	// Validate symbols
	uartPutcDirect('f') // breadcrumb: before validate
	if kmazarinStart == 0 || kmazarinSize == 0 {
		uartPutsDirect("ERROR: Invalid kmazarin symbols\r\n")
		return
	}
	uartPutcDirect('g') // breadcrumb: after validate

	// Create a byte slice from the embedded binary
	uartPutcDirect('h') // breadcrumb: before slice creation
	var elfData []byte
	sliceHeader := (*struct {
		Data uintptr
		Len  int
		Cap  int
	})(unsafe.Pointer(&elfData))
	sliceHeader.Data = kmazarinStart
	sliceHeader.Len = int(kmazarinSize)
	sliceHeader.Cap = int(kmazarinSize)
	uartPutcDirect('i') // breadcrumb: after slice creation

	// Verify ELF magic
	uartPutcDirect('j') // breadcrumb: before ELF magic check
	if len(elfData) < 64 ||
		elfData[0] != 0x7F || elfData[1] != 'E' || elfData[2] != 'L' || elfData[3] != 'F' {
		uartPutsDirect("ERROR: Invalid ELF file\r\n")
		return
	}
	uartPutcDirect('k') // breadcrumb: after ELF magic check

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

	// Process each program header
	for i := uint16(0); i < phnum; i++ {
		uartPutcDirect('[') // Segment start marker
		uartPutcDirect('0' + byte(i)) // Show segment index: 0, 1, 2, etc.
		uartPutcDirect(']')

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

		// Calculate page-aligned range
		vaStart := uintptr(pVaddr) &^ 0xFFF
		vaEnd := (uintptr(pVaddr) + uintptr(pMemsz) + 0xFFF) &^ 0xFFF
		numPages := (vaEnd - vaStart) >> 12

		// Update min/max for span registration
		if minVA == 0 || vaStart < minVA {
			minVA = vaStart
		}
		if vaEnd > maxVA {
			maxVA = vaEnd
		}

		// Determine execute permission based on segment flags
		var execPerm uint64 = PTE_EXEC_NEVER
		if (pFlags & PF_X) != 0 {
			execPerm = PTE_EXEC_ALLOW
		}

		// DEBUG: Print segment info
		uartPutsDirect("  Segment ")
		uartPutHex64Direct(uint64(i))
		uartPutsDirect(": VA 0x")
		uartPutHex64Direct(uint64(vaStart))
		uartPutsDirect("-0x")
		uartPutHex64Direct(uint64(vaEnd))
		uartPutsDirect(" flags=")
		if (pFlags & PF_R) != 0 {
			uartPutsDirect("R")
		}
		if (pFlags & PF_W) != 0 {
			uartPutsDirect("W")
		}
		if (pFlags & PF_X) != 0 {
			uartPutsDirect("X")
		}
		uartPutsDirect(" FileSz=0x")
		uartPutHex64Direct(pFilesz)
		uartPutsDirect(" MemSz=0x")
		uartPutHex64Direct(pMemsz)
		uartPutsDirect("\r\n")

		// Map all pages for this segment
		uartPutcDirect('L') // breadcrumb: start page loop
		for pageIdx := uintptr(0); pageIdx < numPages; pageIdx++ {
			if pageIdx == 0 {
				uartPutcDirect('M') // breadcrumb: first iteration
			}
			// Print progress dot every 10 pages
			if (pageIdx % 10) == 0 {
				uartPutcDirect('.')
			}
			va := vaStart + (pageIdx << 12)
			physFrame := allocPhysFrame()
			if physFrame == 0 {
				uartPutsDirect("ERROR: Out of memory mapping segment\r\n")
				return
			}
			if pageIdx == 0 {
				uartPutcDirect('N') // breadcrumb: after first allocPhysFrame
			}

			// Map with permissions based on segment flags
			// For now, map as RW regardless (we need to write during loading)
			// Execute permission is set based on PF_X flag
			mapPage(va, physFrame, PTE_ATTR_NORMAL, PTE_AP_RW_EL1, execPerm)
			if pageIdx == 0 {
				uartPutcDirect('O') // breadcrumb: after first mapPage
			}
		}
		uartPutcDirect('P') // breadcrumb: page loop completed

		// Use barriers after mapping (no TLB invalidation needed for new mappings)
		asm.Dsb()
		asm.Isb()
		uartPutcDirect('Q') // breadcrumb: after barriers

		// Copy file data to mapped memory
		uartPutcDirect('R') // breadcrumb: before file copy
		if pFilesz > 0 {
			var srcOffset uintptr
			var dstAddr uintptr
			var copySize uint64

			if pOffset > 0x8000000000000000 { // Negative offset (e.g., first LOAD segment)
				uartPutcDirect('S') // breadcrumb: negative offset path
				// Go's linker with -T flag creates segments with negative offsets
				// The segment includes a 64KB header region before .text
				// Relationship: segment_offset = text_offset - (text_va - segment_va)
				//              = 0x1000 - 0x10000 = -0xF000

				// Zero-fill the header region (first 64KB of segment)
				headerSize := uintptr(0x10000)  // 64KB for ELF headers, PHDR, notes
				headerStart := unsafe.Pointer(uintptr(pVaddr))
				bzero4K(headerStart, uint32(headerSize))

				// Copy .text section from file offset 0x1000 to VA (segment_va + 0x10000)
				srcOffset = kmazarinStart + 0x1000
				dstAddr = uintptr(pVaddr) + headerSize  // Skip past header region
				copySize = pFilesz - uint64(headerSize)  // Remaining bytes after header

				// Load segment with negative offset: zero-fill headers, copy code
			} else {
				uartPutcDirect('T') // breadcrumb: positive offset path
				// Normal positive offset - load segment data directly
				srcOffset = kmazarinStart + uintptr(pOffset)
				dstAddr = uintptr(pVaddr)
				copySize = pFilesz
			}

			if copySize > 0 {
				uartPutcDirect('U') // breadcrumb: before copy
				// DEBUG: Print copy parameters
				uartPutsDirect(" src=0x")
				uartPutHex64Direct(uint64(srcOffset))
				uartPutsDirect(" dst=0x")
				uartPutHex64Direct(uint64(dstAddr))
				uartPutsDirect(" size=0x")
				uartPutHex64Direct(copySize)
				uartPutsDirect("\r\n")

				// Use simplified assembly memmove (8-byte MOVD, no LDP/STP)
				uartPutcDirect('Z') // breadcrumb: before copy
				dst := unsafe.Pointer(dstAddr)
				src := unsafe.Pointer(srcOffset)
				asm.MemmoveBytes(dst, src, uint32(copySize))
				uartPutcDirect('V') // breadcrumb: after copy
			}
		}
		uartPutcDirect('W') // breadcrumb: after file copy section

		// Zero BSS section (MemSz > FileSz)
		if pMemsz > pFilesz {
			bssStart := unsafe.Pointer(uintptr(pVaddr) + uintptr(pFilesz))
			bssSize := pMemsz - pFilesz

			// DEBUG: Print BSS zeroing info
			uartPutsDirect("  BSS: zeroing 0x")
			uartPutHex64Direct(uint64(uintptr(bssStart)))
			uartPutsDirect(" - 0x")
			uartPutHex64Direct(uint64(uintptr(bssStart) + uintptr(bssSize)))
			uartPutsDirect(" (")
			uartPutHex64Direct(uint64(bssSize))
			uartPutsDirect(" bytes)\r\n")

			bzeroSimple(bssStart, uint32(bssSize))

			// DEBUG: Verify critical BSS addresses are zeroed
			if uintptr(bssStart) <= 0x419A1060 && 0x419A1060 < uintptr(bssStart)+uintptr(bssSize) {
				uartPutsDirect("  Verifying globalAlloc at 0x419A1060: ")
				globalAllocPtr := (*uint64)(unsafe.Pointer(uintptr(0x419A1060)))
				uartPutHex64Direct(*globalAllocPtr)
				uartPutsDirect("\r\n")

				// Also check the address that loads 0xDEAD000E (x4 = 0x419A39A8)
				uartPutsDirect("  Verifying memstats field at 0x419A39A8: ")
				memstatsFieldPtr := (*uint64)(unsafe.Pointer(uintptr(0x419A39A8)))
				uartPutHex64Direct(*memstatsFieldPtr)
				uartPutsDirect("\r\n")
			}
		}

		// CRITICAL: Remap executable pages as Read-Only
		// ARM architecture requires code pages to be RO+X, not RW+X
		if execPerm == PTE_EXEC_ALLOW {
			for pageIdx := uintptr(0); pageIdx < numPages; pageIdx++ {
				va := vaStart + (pageIdx << 12)
				// Get the physical address from the existing PTE
				l0Index := (va >> 39) & 0x1FF
				l1Index := (va >> 30) & 0x1FF
				l2Index := (va >> 21) & 0x1FF
				l3Index := (va >> 12) & 0x1FF

				ttbr0 := asm.ReadTtbr0El1()
				l0Table := uintptr(ttbr0 & ^uint64(0xFFF))
				l0Entry := (*uint64)(unsafe.Pointer(l0Table + l0Index*8))
				l1Table := uintptr(*l0Entry & ^uint64(0xFFF))
				l1Entry := (*uint64)(unsafe.Pointer(l1Table + l1Index*8))
				l2Table := uintptr(*l1Entry & ^uint64(0xFFF))
				l2Entry := (*uint64)(unsafe.Pointer(l2Table + l2Index*8))
				l3Table := uintptr(*l2Entry & ^uint64(0xFFF))
				l3Entry := (*uint64)(unsafe.Pointer(l3Table + l3Index*8))

				// Extract physical address from existing PTE
				physAddr := uintptr(*l3Entry & ^uint64(0xFFF))

				// Remap with Read-Only permissions
				mapPage(va, physAddr, PTE_ATTR_NORMAL, PTE_AP_RO_EL1, execPerm)
			}
			// Invalidate TLB for this region after remapping
			asm.Dsb()
			asm.InvalidateTlbAll()
			asm.Isb()
		}
	}

	// Segment loop completed - all kmazarin segments loaded

	// CRITICAL: Cache maintenance for executable code
	// After loading code into memory, we must:
	// 1. Clean D-cache (DC CVAU) - ensure data is visible to I-cache
	// 2. Invalidate I-cache (IC IVAU) - discard stale instructions
	// 3. Use barriers to ensure ordering
	for va := minVA; va < maxVA; va += 64 {
		// DC CVAU - Data Cache Clean by VA to Point of Unification
		asm.DcCvau(va)
	}
	asm.Dsb() // Ensure all DC operations complete

	for va := minVA; va < maxVA; va += 64 {
		// IC IVAU - Instruction Cache Invalidate by VA to Point of Unification
		asm.IcIvau(va)
	}
	asm.Dsb() // Ensure all IC operations complete
	asm.Isb() // Synchronize context

	// CRITICAL: Invalidate TLB after mapping new code pages
	// The TLB might have cached "not present" entries from earlier failed translations
	// or speculative fetches. We must invalidate before attempting to execute the code.
	asm.InvalidateTlbAll()
	asm.Dsb()
	asm.Isb()

	uartPutcDirect('2') // DEBUG: After cache maintenance

	if !registerMmapSpan(minVA, maxVA) {
		// Failed to register - hang
		for {}
	}

	uartPutcDirect('3') // DEBUG: After registerMmapSpan

	// Set up argc/argv/envp/auxv structure
	stackPointer, argc, argv := setupKmazarinStartupEnv()
	uartPutcDirect('4') // DEBUG: After setupKmazarinStartupEnv
	if stackPointer == 0 || argv == 0 {
		uartPutsDirect("ERROR: Environment setup failed!\r\n")
		return
	}

	// Debug: Verify stack structure before jumping
	uartPutsDirect("=== Stack Structure Verification ===\r\n")

	// Read the stack structure (do this BEFORE any printHex calls to verify access)
	data := (*[20]uint64)(unsafe.Pointer(stackPointer))

	// Verify we can read the data
	uartPutsDirect("Stack allocated, reading data...\r\n")

	// Check basic structure (just report success/failure, no hex printing yet)
	if data[0] != 1 {
		uartPutsDirect("ERROR: argc is not 1!\r\n")
		return
	}
	uartPutsDirect("argc = 1 OK\r\n")

	if data[2] != 0 {
		uartPutsDirect("ERROR: argv[1] is not NULL!\r\n")
		return
	}
	uartPutsDirect("argv[1] = NULL OK\r\n")

	if data[3] != 0 {
		uartPutsDirect("ERROR: envp[0] is not NULL!\r\n")
		return
	}
	uartPutsDirect("envp[0] = NULL OK\r\n")

	if data[4] != 6 {
		uartPutsDirect("ERROR: AT_PAGESZ tag is not 6!\r\n")
		return
	}
	uartPutsDirect("AT_PAGESZ tag OK\r\n")

	if data[5] != 4096 {
		uartPutsDirect("ERROR: AT_PAGESZ value is not 4096!\r\n")
		return
	}
	uartPutsDirect("AT_PAGESZ value OK\r\n")

	// Verify AT_HWCAP (tag=16) at data[10], value at data[11]
	if data[10] != 16 {
		uartPutsDirect("ERROR: AT_HWCAP tag is not 16!\r\n")
		return
	}
	uartPutsDirect("AT_HWCAP tag OK\r\n")

	// AT_NULL is now at data[12] (after adding AT_HWCAP)
	if data[12] != 0 {
		uartPutsDirect("ERROR: AT_NULL tag is not 0!\r\n")
		return
	}
	uartPutsDirect("AT_NULL tag OK\r\n")

	uartPutsDirect("Stack structure verified!\r\n")

	// Use entry point directly (it's already the correct virtual address)
	entryAddr := uintptr(entry)

	// DEBUG: Print the actual entry address we're about to jump to
	uartPutsDirect("Entry addr = ")
	// Print bytes of entryAddr
	entryBytes := (*[8]byte)(unsafe.Pointer(&entryAddr))
	for i := 0; i < 8; i++ {
		b := entryBytes[i]
		nibbleHigh := (b >> 4) & 0xF
		nibbleLow := b & 0xF
		if nibbleHigh < 10 {
			uartPutsDirect(string([]byte{'0' + nibbleHigh}))
		} else {
			uartPutsDirect(string([]byte{'a' + (nibbleHigh - 10)}))
		}
		if nibbleLow < 10 {
			uartPutsDirect(string([]byte{'0' + nibbleLow}))
		} else {
			uartPutsDirect(string([]byte{'a' + (nibbleLow - 10)}))
		}
		uartPutsDirect(" ")
	}
	uartPutsDirect("\r\n")

	// DEBUG: Check PTE for entry point page
	entryPage := entryAddr &^ 0xFFF
	uartPutsDirect("Entry page: 0x")
	uartPutHex64Direct(uint64(entryPage))
	uartPutsDirect("\r\n")

	// Walk page tables to find PTE
	ttbr0 := asm.ReadTtbr0El1()
	uartPutsDirect("TTBR0_EL1=0x")
	uartPutHex64Direct(ttbr0)
	uartPutsDirect(" Expected=0x")
	uartPutHex64Direct(uint64(asm.GetPageTablesStartAddr()))
	uartPutsDirect("\r\n")
	l0Table := uintptr(ttbr0 & ^uint64(0xFFF))

	l0Index := (entryPage >> 39) & 0x1FF
	l0Entry := (*uint64)(unsafe.Pointer(l0Table + l0Index*8))
	uartPutsDirect("L0[")
	uartPutHex64Direct(uint64(l0Index))
	uartPutsDirect("]=0x")
	uartPutHex64Direct(*l0Entry)
	uartPutsDirect("\r\n")

	if (*l0Entry & PTE_VALID) == 0 {
		uartPutsDirect("ERROR: L0 entry not valid!\r\n")
		return
	}

	l1Table := uintptr(*l0Entry & ^uint64(0xFFF))
	l1Index := (entryPage >> 30) & 0x1FF
	l1Entry := (*uint64)(unsafe.Pointer(l1Table + l1Index*8))
	uartPutsDirect("L1[")
	uartPutHex64Direct(uint64(l1Index))
	uartPutsDirect("]=0x")
	uartPutHex64Direct(*l1Entry)
	uartPutsDirect("\r\n")

	if (*l1Entry & PTE_VALID) == 0 {
		uartPutsDirect("ERROR: L1 entry not valid!\r\n")
		return
	}

	l2Table := uintptr(*l1Entry & ^uint64(0xFFF))
	l2Index := (entryPage >> 21) & 0x1FF
	l2Entry := (*uint64)(unsafe.Pointer(l2Table + l2Index*8))
	uartPutsDirect("L2[")
	uartPutHex64Direct(uint64(l2Index))
	uartPutsDirect("]=0x")
	uartPutHex64Direct(*l2Entry)
	uartPutsDirect("\r\n")

	if (*l2Entry & PTE_VALID) == 0 {
		uartPutsDirect("ERROR: L2 entry not valid!\r\n")
		return
	}

	l3Table := uintptr(*l2Entry & ^uint64(0xFFF))
	l3Index := (entryPage >> 12) & 0x1FF
	l3Entry := (*uint64)(unsafe.Pointer(l3Table + l3Index*8))
	uartPutsDirect("L3[")
	uartPutHex64Direct(uint64(l3Index))
	uartPutsDirect("]=0x")
	uartPutHex64Direct(*l3Entry)
	if (*l3Entry & PTE_PXN) != 0 {
		uartPutsDirect(" PXN")
	}
	if (*l3Entry & PTE_UXN) != 0 {
		uartPutsDirect(" UXN")
	}
	if (*l3Entry & PTE_PXN) == 0 && (*l3Entry & PTE_UXN) == 0 {
		uartPutsDirect(" EXEC_OK")
	}
	uartPutsDirect("\r\n")

	// DEBUG: Read and verify the bytes at the entry point
	uartPutsDirect("Bytes at entry point: ")
	entryInstructions := (*[16]byte)(unsafe.Pointer(entryAddr))
	for i := 0; i < 16; i++ {
		b := entryInstructions[i]
		nibbleHigh := (b >> 4) & 0xF
		nibbleLow := b & 0xF
		if nibbleHigh < 10 {
			uartPutsDirect(string([]byte{'0' + nibbleHigh}))
		} else {
			uartPutsDirect(string([]byte{'a' + (nibbleHigh - 10)}))
		}
		if nibbleLow < 10 {
			uartPutsDirect(string([]byte{'0' + nibbleLow}))
		} else {
			uartPutsDirect(string([]byte{'a' + (nibbleLow - 10)}))
		}
		uartPutsDirect(" ")
	}
	uartPutsDirect("\r\n")

	// Jump to kmazarin with proper argc/argv/auxv
	uartPutsDirect("Jumping to kmazarin...\r\n")

	// DEBUG: Check timer status before jumping
	uartPutsDirect("Timer CTL=0x")
	ctl := asm.ReadCntvCtlEl0()
	uartPutHex32Direct(ctl)
	uartPutsDirect(" TVAL=0x")
	tval := asm.ReadCntvTvalEl0()
	uartPutHex32Direct(tval)
	uartPutsDirect("\r\n")

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
