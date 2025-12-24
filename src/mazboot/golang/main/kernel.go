package main

import (
	"unsafe"

	"mazboot/asm"
)

// Assembly functions to set runtime stack limits
//
//go:linkname setMaxstacksize set_maxstacksize
func setMaxstacksize(size uintptr)

//go:linkname setMaxstackceiling set_maxstackceiling
func setMaxstackceiling(size uintptr)

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
func printHex64(val uint64) {
	// Use a small buffer to collect hex digits
	var buf [16]byte
	for i := 0; i < 16; i++ {
		nibble := (val >> uint(60-i*4)) & 0xF
		if nibble < 10 {
			buf[i] = byte('0' + nibble)
		} else {
			buf[i] = byte('A' + nibble - 10)
		}
	}
	// Print each character individually since print() doesn't take []byte
	for i := 0; i < 16; i++ {
		printChar(buf[i])
	}
}

// printHex32 outputs a uint32 as an 8-digit hex string via print()
//
//go:nosplit
func printHex32(val uint32) {
	for i := 7; i >= 0; i-- {
		nibble := (val >> uint(i*4)) & 0xF
		if nibble < 10 {
			printChar(byte('0' + nibble))
		} else {
			printChar(byte('A' + nibble - 10))
		}
	}
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

	if !aligned {
		print("SP-MISALIGN: ")
		print(context)
		print(" SP=0x")
		printHex64(uint64(sp))
		print(" (misaligned, last nibble=0x")
		printHex8(uint8(sp & 0xF))
		print(")\r\n")
	}

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

	// Check SP alignment and print warning if misaligned
	spAfter := asm.GetStackPointer()
	if (spAfter & 0xF) != 0 {
		print("!MISALIGNED!\r\n")
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

// KernelMain is the entry point called from boot.s
// For bare metal, we ensure it's not optimized away
//
//go:noinline
func KernelMain(r0, r1, atags uint32) {
	// VERY EARLY breadcrumb - before any complex operations
	uartBase := uintptr(0x09000000)
	*(*uint32)(unsafe.Pointer(uartBase)) = 0x4B // 'K' = Entered KernelMain

	// Uncomment the line below to use simplified test kernel
	// SimpleTestKernel()
	// return

	_ = r0
	_ = r1

	// Get MMIO device addresses from linker symbols
	_ = getLinkerSymbol("__uart_base")
	*(*uint32)(unsafe.Pointer(uartBase)) = 0x6B // 'k' = Past linker symbol

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

	// Initialize MMU (required before heap - enables Normal memory for unaligned access)
	if !initMMU() {
		print("FATAL: MMU initialization failed\r\n")
		for {
		}
	}
	*(*uint32)(unsafe.Pointer(uartBase)) = 0x4D // 'M' = initMMU done

	if !enableMMU() {
		print("FATAL: MMU enablement failed\r\n")
		for {
		}
	}
	*(*uint32)(unsafe.Pointer(uartBase)) = 0x6D // 'm' = enableMMU done

	// Set physPageSize before schedinit (needed by mallocinit which schedinit calls)
	// Normally this would be set by sysauxv from AT_PAGESZ auxiliary vector
	physPageSizeAddr := asm.GetPhysPageSizeAddr()
	writeMemory64(physPageSizeAddr, 4096)
	print("physPageSize set to 4096\r\n")

	// Initialize VirtIO RNG device for random number generation
	initVirtIORNG()

	// Map PL031 RTC MMIO region before accessing it
	// PL031 is a memory-mapped device, needs identity mapping with device attributes
	{
		pl031Base := getLinkerSymbol("__rtc_base")
		pl031Size := uintptr(0x1000) // 4KB page
		print("Mapping PL031 RTC at 0x")
		printHex32(uint32(pl031Base))
		print("...\r\n")
		for offset := uintptr(0); offset < pl031Size; offset += 0x1000 {
			va := pl031Base + offset
			pa := pl031Base + offset // Identity mapping
			mapPage(va, pa, PTE_ATTR_DEVICE, PTE_AP_RW_EL1, PTE_EXEC_NEVER)
		}
		print("PL031 RTC mapped\r\n")
	}

	// Initialize PL031 RTC for time services (needed by schedinit)
	// TODO: Implement initPL031RTC()
	// initPL031RTC()

	// Set up hardware watchpoint to catch corruption of text section
	// Watch address 0x312f38 which gets corrupted with pattern 0x0080
	// TODO: Implement asm.SetupWatchpoint()
	// print("Setting up watchpoint on text section at 0x00312f38...\r\n")
	// asm.SetupWatchpoint(0x00312f38, 3) // 3 = doubleword (8 bytes)

	// WORKAROUND: Pre-map critical memory regions to avoid page faults during demand paging
	// These regions must be mapped before demand paging is active:
	// 1. DTB region - QEMU device tree
	// 2. g0 stack - system goroutine stack
	// 3. Exception stacks - for handling page faults
	//
	// NOTE: We do NOT pre-map ROM/Flash or mazboot's own code/data because:
	//   - Memory layout varies by platform (QEMU vs Raspberry Pi vs others)
	//   - Instead, we ensure exception handlers don't access unmapped globals
	{
		// Map DTB region (QEMU device tree blob)
		dtbStart := getLinkerSymbol("__dtb_boot_addr")
		dtbEnd := dtbStart + getLinkerSymbol("__dtb_size")
		print("Pre-mapping DTB region (0x")
		printHex64(uint64(dtbStart))
		print("-0x")
		printHex64(uint64(dtbEnd))
		print(")...\r\n")
		for va := dtbStart; va < dtbEnd; va += 0x1000 {
			physFrame := allocPhysFrame()
			if physFrame == 0 {
				print("ERROR: Out of physical frames\r\n")
				break
			}
			bzero(unsafe.Pointer(physFrame), 0x1000)
			mapPage(va, physFrame, PTE_ATTR_NORMAL, PTE_AP_RW_EL1, PTE_EXEC_NEVER)
			if (va-dtbStart)%(64*0x1000) == 0 {
				print(".")
			}
		}
		print("\r\nPre-mapped DTB region\r\n")

		// Map g0 stack (system goroutine stack, 32KB)
		g0StackBottom := getLinkerSymbol("__g0_stack_bottom")
		g0StackTop := getLinkerSymbol("__stack_top")
		print("Pre-mapping g0 stack (0x")
		printHex64(uint64(g0StackBottom))
		print("-0x")
		printHex64(uint64(g0StackTop))
		print(")...\r\n")
		for va := g0StackBottom; va < g0StackTop; va += 0x1000 {
			physFrame := allocPhysFrame()
			if physFrame == 0 {
				print("ERROR: Out of physical frames\r\n")
				break
			}
			bzero(unsafe.Pointer(physFrame), 0x1000)
			mapPage(va, physFrame, PTE_ATTR_NORMAL, PTE_AP_RW_EL1, PTE_EXEC_NEVER)
			if (va-g0StackBottom)%(8*0x1000) == 0 {
				print(".")
			}
		}
		print("\r\nPre-mapped g0 stack\r\n")

		// Map exception stacks (primary and nested)
		// Primary exception stack: 0x5FFE0000 (8KB)
		// Nested exception stack: 0x5FFD0000 (4KB)
		const EXC_STACK_PRIMARY = uintptr(0x5FFE0000)
		const EXC_STACK_NESTED = uintptr(0x5FFD0000)
		const EXC_STACK_SIZE = uintptr(0x2000) // 8KB for primary

		print("Pre-mapping exception stacks (0x")
		printHex64(uint64(EXC_STACK_NESTED))
		print("-0x")
		printHex64(uint64(EXC_STACK_PRIMARY + EXC_STACK_SIZE))
		print(")...\r\n")

		// Map nested exception stack (4KB)
		for va := EXC_STACK_NESTED; va < EXC_STACK_PRIMARY; va += 0x1000 {
			physFrame := allocPhysFrame()
			if physFrame == 0 {
				print("ERROR: Out of physical frames for exception stack\r\n")
				break
			}
			bzero(unsafe.Pointer(physFrame), 0x1000)
			mapPage(va, physFrame, PTE_ATTR_NORMAL, PTE_AP_RW_EL1, PTE_EXEC_NEVER)
			if va == EXC_STACK_NESTED {
				print(".")
			}
		}

		// Map primary exception stack (8KB)
		for va := EXC_STACK_PRIMARY; va < EXC_STACK_PRIMARY+EXC_STACK_SIZE; va += 0x1000 {
			physFrame := allocPhysFrame()
			if physFrame == 0 {
				print("ERROR: Out of physical frames for exception stack\r\n")
				break
			}
			bzero(unsafe.Pointer(physFrame), 0x1000)
			mapPage(va, physFrame, PTE_ATTR_NORMAL, PTE_AP_RW_EL1, PTE_EXEC_NEVER)
			print(".")
		}
		print("\r\nPre-mapped exception stacks\r\n")

		// Pre-map all of mazboot's sections to avoid page faults in exception handlers
		// This ensures all code, data, and globals are accessible without demand paging
		// Everything is now in RAM (0x40100000+) for platform independence
		print("Pre-mapping mazboot sections (all in RAM)...\r\n")

		// Get linker symbols for section boundaries
		textStart := getLinkerSymbol("__text_start")
		textEnd := getLinkerSymbol("__text_end")
		rodataStart := getLinkerSymbol("__rodata_start")
		rodataEnd := getLinkerSymbol("__rodata_end")
		dataStart := getLinkerSymbol("__data_start")
		dataEnd := getLinkerSymbol("__data_end")
		bssStart := getLinkerSymbol("__bss_start")
		bssEnd := getLinkerSymbol("__bss_end")

		// Pre-map .text (code) - now in RAM starting at 0x40100000
		print("  .text:   0x")
		printHex64(uint64(textStart))
		print(" - 0x")
		printHex64(uint64(textEnd))
		print(" (")
		printUint32(uint32((textEnd - textStart) / 1024))
		print("KB)...")
		for va := textStart &^ 0xFFF; va < textEnd; va += 0x1000 {
			// Use identity mapping (VA == PA) with read-only permissions
			mapPage(va, va, PTE_ATTR_NORMAL, PTE_AP_RO_EL1, PTE_EXEC_NEVER)
		}
		print(" OK\r\n")

		// Pre-map .rodata (read-only data) - in RAM after .text
		print("  .rodata: 0x")
		printHex64(uint64(rodataStart))
		print(" - 0x")
		printHex64(uint64(rodataEnd))
		print(" (")
		printUint32(uint32((rodataEnd - rodataStart) / 1024))
		print("KB)...")
		for va := rodataStart &^ 0xFFF; va < rodataEnd; va += 0x1000 {
			mapPage(va, va, PTE_ATTR_NORMAL, PTE_AP_RO_EL1, PTE_EXEC_NEVER)
		}
		print(" OK\r\n")

		// Pre-map .data (initialized writable data) - in RAM after .rodata
		print("  .data:   0x")
		printHex64(uint64(dataStart))
		print(" - 0x")
		printHex64(uint64(dataEnd))
		print(" (")
		printUint32(uint32((dataEnd - dataStart) / 1024))
		print("KB)...")
		for va := dataStart &^ 0xFFF; va < dataEnd; va += 0x1000 {
			mapPage(va, va, PTE_ATTR_NORMAL, PTE_AP_RW_EL1, PTE_EXEC_NEVER)
		}
		print(" OK\r\n")

		// Pre-map .bss (zero-initialized data) - in RAM after .data
		print("  .bss:    0x")
		printHex64(uint64(bssStart))
		print(" - 0x")
		printHex64(uint64(bssEnd))
		print(" (")
		printUint32(uint32((bssEnd - bssStart) / 1024))
		print("KB)...")
		for va := bssStart &^ 0xFFF; va < bssEnd; va += 0x1000 {
			mapPage(va, va, PTE_ATTR_NORMAL, PTE_AP_RW_EL1, PTE_EXEC_NEVER)
		}
		print(" OK\r\n")

		print("All mazboot sections pre-mapped (")
		printUint32(uint32((bssEnd - textStart) / 1024))
		print("KB total)\r\n")
	}

	// =========================================
	// TEST: Item 3 - runtime.args()
	// Test that we can call runtime.args with a minimal argv/auxv structure
	// This verifies the args() → sysargs() → sysauxv() path works.
	// =========================================
	print("Testing Item 3: runtime.args()... ")
	result := asm.CallRuntimeArgs()
	if result == 0 {
		print("PASS\r\n")
	} else {
		print("FAIL\r\n")
	}

	// =========================================
	// TEST: Item 4a - Direct syscall test
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

	print("Testing Item 5: runtime.schedinit()... ")
	asm.CallRuntimeSchedinit()
	print("PASS\r\n")

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
	print("  Max stack size set to ", stackSize, " bytes\r\n")

	// Mark scheduler as ready - futex can now use real gopark/goready
	MarkSchedulerReady()
	print("Scheduler fully initialized (gopark/goready enabled)\r\n")

	// NOTE: parseEmbeddedKmazarin() will be called from simpleMain() (user goroutine)
	// because debug/elf uses defer, which isn't allowed on the system stack (g0)

	// =========================================
	// Initialize Timer-Based Preemption System
	// =========================================
	print("\r\n═══════════════════════════════════════════════\r\n")
	print("mazboot: Initializing Timer System\r\n")
	print("═══════════════════════════════════════════════\r\n")

	// Initialize time system (reads ARM Generic Timer frequency)
	print("mazboot: Initializing hardware timer...\r\n")
	initTime()

	// Start monitoring goroutines (now that maxstacksize is properly set)
	print("mazboot: Starting monitor goroutines...\r\n")
	startGCMonitor()
	startScavengerMonitor()
	startSchedtraceMonitor()
	print("mazboot: All monitors started\r\n")
	print("  (Monitors will run once they receive timer ticks)\r\n")

	// DEBUG: Dump allgs contents to understand the NULL entry issue
	print("\r\nDEBUG: Dumping allgs contents...\r\n")
	dumpAllGs()

	print("═══════════════════════════════════════════════\r\n\r\n")

	// =========================================
	// TEST: Simple goroutine/channel test
	// Create a goroutine to run simpleMain and start the scheduler
	// =========================================
	print("\r\n=== Starting Simple Goroutine/Channel Test ===\r\n")

	// Create goroutine for simpleMain
	print("Creating goroutine for simpleMain...\r\n")
	asm.CallNewprocSimpleMain()
	print("Goroutine created, starting scheduler...\r\n")

	// Start the scheduler - this should never return
	print("Calling runtime.mstart()...\r\n")
	asm.CallRuntimeMstart()

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

	// Parse device tree (needs MMU enabled for safe memory access)
	initDeviceTree()

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

	// Check security state before setting up interrupts
	checkSecurityState()

	// Set up UART TX interrupts for interrupt-driven output
	uartSetupInterrupts()

	// Stage 9: Timer init
	FramebufferPuts("Initializing timer...\r\n")
	timerInit()

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

	FramebufferPuts("Boot complete.\r\n")

	// Enable CPU interrupts now that everything is initialized
	// This unmasks the I bit in PSTATE to allow IRQs to fire
	asm.EnableIrqsAsm()
	FramebufferPuts("Interrupts enabled.\r\n")

	// Drain any pending output
	for i := 0; i < 1000; i++ {
		uartDrainRingBuffer()
	}

	// Stage 12: Spawn goroutine that waits for timer signals
	// NOTE: This goroutine has an infinite loop, so this call will never return.
	// Timer interrupts will fire (every 5 seconds) and send signals to the channel.
	// The goroutine will receive those signals and print "bong".
	FramebufferPuts("Spawning timer listener goroutine...\r\n")
	spawnGoroutine(timerListenerLoop)

	// Should never reach here since testGoroutineFunc has infinite loop
	FramebufferPuts("ERROR: goroutine returned unexpectedly!\r\n")
	for {
	}
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
	// Use direct memory access since we can't use go:linkname with slice types
	// runtime.allgs is at 0x401cd3b0, runtime.allglen is at 0x401f68a8 (from nm output)
	const allgsAddr = uintptr(0x401cd3b0)
	const allglenAddr = uintptr(0x401f68a8)

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

	print("  allgs ptr=0x")
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

// loadAndRunKmazarin loads the embedded kmazarin ELF binary into memory and jumps to it
// This function:
// 1. Parses the ELF program headers to find PT_LOAD segments
// 2. Computes a load offset to avoid DTB conflict at 0x40000000-0x40100000
//    - offset = DTB_END - first_segment_vaddr
// 3. Copies each segment from the embedded binary to memory at (vaddr + offset)
// 4. Handles BSS zeroing (memsz > filesz)
// 5. Jumps to the kmazarin entry point (entry + offset)
//
//go:nosplit
func loadAndRunKmazarin() {
	print("\r\n=== Loading Kmazarin Kernel ===\r\n")

	// DTB region ends at 0x40100000 on QEMU virt machine
	// We need to load kmazarin after this to avoid conflict
	const DTB_END = uintptr(0x40100000)

	// Get the embedded kmazarin binary location from linker symbols
	kmazarinStart := getLinkerSymbol("__kmazarin_start")
	kmazarinSize := getLinkerSymbol("__kmazarin_size")

	print("Loading from: 0x")
	printHex64(uint64(kmazarinStart))
	print(" (size: ")
	printUint32(uint32(kmazarinSize))
	print(" bytes)\r\n")

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
	if len(elfData) < 64 {
		print("ERROR: ELF data too small\r\n")
		return
	}
	if elfData[0] != 0x7F || elfData[1] != 'E' || elfData[2] != 'L' || elfData[3] != 'F' {
		print("ERROR: Invalid ELF magic bytes\r\n")
		print("  Got: 0x")
		printHex8(elfData[0])
		print(" 0x")
		printHex8(elfData[1])
		print(" 0x")
		printHex8(elfData[2])
		print(" 0x")
		printHex8(elfData[3])
		print("\r\n")
		return
	}

	// Parse entry point
	entry := uint64(elfData[0x18]) |
		(uint64(elfData[0x19]) << 8) |
		(uint64(elfData[0x1A]) << 16) |
		(uint64(elfData[0x1B]) << 24) |
		(uint64(elfData[0x1C]) << 32) |
		(uint64(elfData[0x1D]) << 40) |
		(uint64(elfData[0x1E]) << 48) |
		(uint64(elfData[0x1F]) << 56)

	print("Entry point: 0x")
	printHex64(entry)
	print("\r\n")

	// Parse program headers
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

	// First pass: find the first PT_LOAD segment to compute the load offset
	// Pack kmazarin right after mazboot's bss section for contiguous layout
	var loadOffset uintptr

	// Get end of mazboot's bss section - this is where kmazarin will be loaded
	bssEnd := getLinkerSymbol("__bss_end")
	// Round up to next page boundary for alignment
	kmazarinLoadAddr := (bssEnd + 0xFFF) &^ 0xFFF

	for i := uint16(0); i < phnum; i++ {
		offset := phoff + uint64(i)*uint64(phentsize)
		if offset+56 > uint64(len(elfData)) {
			break
		}

		ptype := uint32(elfData[offset]) |
			(uint32(elfData[offset+1]) << 8) |
			(uint32(elfData[offset+2]) << 16) |
			(uint32(elfData[offset+3]) << 24)

		// Find first PT_LOAD segment (type 1)
		if ptype == 1 {
			firstVaddr := uint64(elfData[offset+16]) |
				(uint64(elfData[offset+17]) << 8) |
				(uint64(elfData[offset+18]) << 16) |
				(uint64(elfData[offset+19]) << 24) |
				(uint64(elfData[offset+20]) << 32) |
				(uint64(elfData[offset+21]) << 40) |
				(uint64(elfData[offset+22]) << 48) |
				(uint64(elfData[offset+23]) << 56)

			// Compute offset: where we want it (after mazboot) minus where it wants to be (firstVaddr)
			loadOffset = kmazarinLoadAddr - uintptr(firstVaddr)
			print("Computed load offset: 0x")
			printHex64(uint64(loadOffset))
			print(" (mazboot bss end 0x")
			printHex64(uint64(bssEnd))
			print(" → kmazarin load 0x")
			printHex64(uint64(kmazarinLoadAddr))
			print(" - first segment vaddr 0x")
			printHex64(firstVaddr)
			print(")\r\n")
			break
		}
	}

	print("\r\nLoading segments:\r\n")

	// Load each PT_LOAD segment
	loadCount := 0
	for i := uint16(0); i < phnum; i++ {
		offset := phoff + uint64(i)*uint64(phentsize)
		if offset+56 > uint64(len(elfData)) {
			print("ERROR: Program header offset out of bounds\r\n")
			break
		}

		// Read program header fields
		ptype := uint32(elfData[offset]) |
			(uint32(elfData[offset+1]) << 8) |
			(uint32(elfData[offset+2]) << 16) |
			(uint32(elfData[offset+3]) << 24)

		// Only process PT_LOAD segments (type 1)
		if ptype != 1 {
			continue
		}

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

		// Calculate destination address with offset
		destAddr := uintptr(vaddr) + loadOffset

		print("  [")
		printUint32(uint32(loadCount))
		print("] Loading 0x")
		printHex64(vaddr)
		print(" → 0x")
		printHex64(uint64(destAddr))
		print(" (")
		printUint32(uint32(filesz))
		print(" bytes, flags: ")
		if (flags & 0x4) != 0 {
			print("R")
		} else {
			print("-")
		}
		if (flags & 0x2) != 0 {
			print("W")
		} else {
			print("-")
		}
		if (flags & 0x1) != 0 {
			print("X")
		} else {
			print("-")
		}
		print(")\r\n")

		// NOTE: We don't need to pre-map pages - the page fault handler
		// will automatically allocate and map pages on demand when we access them

		// Copy segment data from embedded binary to destination
		// Handle negative file offsets (ELF files can have segments that include headers)
		var srcAddr uintptr
		if poffset >= 0x8000000000000000 { // Negative offset (int64 < 0)
			// For negative offsets in an embedded ELF, use offset 0
			// (the segment wants to include the ELF headers from the start)
			srcAddr = kmazarinStart
		} else {
			srcAddr = kmazarinStart + uintptr(poffset)
		}

		if filesz > 0 {
			print("    Copying ")
			printUint32(uint32(filesz))
			print(" bytes from offset 0x")
			printHex64(poffset)
			print(" (src=0x")
			printHex64(uint64(srcAddr))
			print(" -> dest=0x")
			printHex64(uint64(destAddr))
			print(")\r\n")

			// Verify pointers are not NULL
			if destAddr == 0 {
				print("ERROR: destAddr is NULL!\r\n")
				return
			}
			if srcAddr == 0 {
				print("ERROR: srcAddr is NULL!\r\n")
				return
			}

			// Pre-map destination pages to avoid nested page faults during memmove
			// This prevents stack overflow from too many nested exceptions
			// We only need to pre-map destination - source is already mapped (mazboot's data section)
			print("    Pre-mapping ")
			destEnd := destAddr + uintptr(filesz)
			pageCount := uint32(0)
			for va := destAddr &^ 0xFFF; va < destEnd; va += 0x1000 {
				// Explicitly allocate and map each page instead of touching it
				// This avoids triggering Go runtime code that might access unmapped globals
				physFrame := allocPhysFrame()
				if physFrame == 0 {
					print("\r\nERROR: Out of physical frames during pre-mapping\r\n")
					return
				}
				// Zero the frame before mapping to avoid stale data
				bzero(unsafe.Pointer(physFrame), 0x1000)
				// Map with normal memory attributes, RW from EL1
				mapPage(va, physFrame, PTE_ATTR_NORMAL, PTE_AP_RW_EL1, PTE_EXEC_NEVER)
				pageCount++
			}
			printUint32(pageCount)
			print(" pages\r\n")

			print("    Calling memmove... ")
			// Use memmove for the copy
			asm.MemmoveBytes(
				unsafe.Pointer(destAddr),
				unsafe.Pointer(srcAddr),
				uint32(filesz))
			print("OK\r\n")
		}

		// Zero BSS area if memsz > filesz
		if memsz > filesz {
			bssSize := memsz - filesz
			print("    Zeroing BSS: ")
			printUint32(uint32(bssSize))
			print(" bytes... ")
			bzero(unsafe.Pointer(destAddr+uintptr(filesz)), uint32(bssSize))
			print("OK\r\n")
		}

		loadCount++
	}

	print("\r\nLoaded ")
	printUint32(uint32(loadCount))
	print(" segments\r\n")

	// Jump to kmazarin entry point
	entryAddr := uintptr(entry) + loadOffset
	print("\r\nJumping to kmazarin entry point at 0x")
	printHex64(uint64(entryAddr))
	print(" (entry 0x")
	printHex64(entry)
	print(" + offset 0x")
	printHex64(uint64(loadOffset))
	print(")...\r\n\r\n")

	// Jump to entry point
	// We need to do this via assembly to ensure proper register setup
	jumpToKmazarin(entryAddr)

	// Should never reach here
	print("ERROR: Returned from kmazarin!\r\n")
}

// jumpToKmazarin jumps to the kmazarin kernel entry point
// This is implemented in assembly (lib.s) to ensure clean transition
// Parameter: entryAddr = address to jump to
// NOTE: This function never returns
//
//go:linkname jumpToKmazarin jump_to_kmazarin
//go:nosplit
func jumpToKmazarin(entryAddr uintptr)
