package main

import (
	"unsafe"
)

// Link to external assembly functions from lib.s
//
//go:linkname mmio_write mmio_write
//go:nosplit
func mmio_write(reg uintptr, data uint32)

//go:linkname uart_init_pl011 uart_init_pl011
//go:nosplit
func uart_init_pl011()

//go:linkname uart_putc_pl011 uart_putc_pl011
//go:nosplit
func uart_putc_pl011(c byte)

//go:linkname mmio_read mmio_read
//go:nosplit
func mmio_read(reg uintptr) uint32

//go:linkname mmio_write16 mmio_write16
//go:nosplit
func mmio_write16(reg uintptr, data uint16)

//go:linkname mmio_read16 mmio_read16
//go:nosplit
func mmio_read16(reg uintptr) uint16

//go:linkname mmio_write64 mmio_write64
//go:nosplit
func mmio_write64(reg uintptr, data uint64)

//go:linkname delay delay
//go:nosplit
func delay(count int32)

//go:linkname busy_wait busy_wait
//go:nosplit
func busy_wait(count uint32)

//go:linkname systemstack runtime.systemstack
//go:nosplit
func systemstack(fn func())

//go:linkname bzero bzero
//go:nosplit
func bzero(ptr unsafe.Pointer, size uint32)

//go:linkname storePointerNoBarrier store_pointer_nobarrier
//go:nosplit
func storePointerNoBarrier(dest unsafe.Pointer, value unsafe.Pointer)

//go:linkname dsb dsb
//go:nosplit
func dsb()

//go:linkname qemu_exit qemu_exit
//go:nosplit
func qemu_exit()

//go:linkname get_stack_pointer get_stack_pointer
//go:nosplit
func get_stack_pointer() uintptr

//go:linkname get_caller_stack_pointer get_caller_stack_pointer
//go:nosplit
func get_caller_stack_pointer() uintptr

//go:linkname verifyStackPointerReading verify_stack_pointer_reading
//go:nosplit
func verifyStackPointerReading() int

//go:linkname set_g_pointer set_g_pointer
//go:nosplit
func set_g_pointer(g uintptr)

// System Control Register access functions for alignment diagnostics
//
//go:linkname read_sctlr_el1 read_sctlr_el1
//go:nosplit
func read_sctlr_el1() uint64

//go:linkname write_sctlr_el1 write_sctlr_el1
//go:nosplit
func write_sctlr_el1(value uint64)

//go:linkname disable_alignment_check disable_alignment_check
//go:nosplit
func disable_alignment_check()

//go:linkname switchToGoroutine switchToGoroutine
//go:nosplit
func switchToGoroutine(g *runtimeG, fn uintptr)

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
	sp := get_caller_stack_pointer()
	aligned := (sp & 0xF) == 0

	if !aligned {
		uartPuts("SP-MISALIGN: ")
		uartPuts(context)
		uartPuts(" SP=0x")
		uartPutHex64(uint64(sp))
		uartPuts(" (misaligned, last nibble=0x")
		uartPutHex8(uint8(sp & 0xF))
		uartPuts(")\r\n")
	}

	return aligned
}

// checkSPAlignmentSilent checks if SP is 16-byte aligned without printing
// Returns true if aligned, false if misaligned
//
//go:nosplit
func checkSPAlignmentSilent() bool {
	sp := get_caller_stack_pointer()
	return (sp & 0xF) == 0
}

// printSPBreadcrumb prints a breadcrumb with label and SP value
// Format: "[label] SP=0xXXXXXXXX\r\n"
// Uses uartPutc for all characters to avoid string literal issues
//
//go:nosplit
func printSPBreadcrumb(label byte) {
	// Get SP BEFORE any function calls to avoid corruption
	sp := get_caller_stack_pointer()

	uartPutc('[')
	uartPutc(label)
	uartPutc(']')
	uartPutc(' ')
	uartPutc('S')
	uartPutc('P')
	uartPutc('=')
	uartPutc('0')
	uartPutc('x')
	uartPutHex64(uint64(sp))
	uartPutc('\r')
	uartPutc('\n')

	// Check SP alignment and print warning if misaligned
	spAfter := get_stack_pointer()
	if (spAfter & 0xF) != 0 {
		uartPutc('!')
		uartPutc('M')
		uartPutc('I')
		uartPutc('S')
		uartPutc('A')
		uartPutc('L')
		uartPutc('I')
		uartPutc('G')
		uartPutc('N')
		uartPutc('E')
		uartPutc('D')
		uartPutc('!')
		uartPutc('\r')
		uartPutc('\n')
	}
}

//go:nosplit
func uartPuts(str string) {
	// Breadcrumb: uartPuts entry (using assembly to avoid any Go overhead)
	uart_putc_pl011('P')

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
	uart_putc_pl011('h') // Breadcrumb: about to access string header
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
	uart_putc_pl011('b') // Breadcrumb: about to call uartPutsBytes
	uartPutsBytes(dataPtr, strLen)
	uart_putc_pl011('p') // Breadcrumb: uartPuts returning
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

// uartPutMemSize formats and displays memory size in a human-readable format
// Displays as MB or GB depending on size
//
//go:nosplit
func uartPutMemSize(sizeBytes uint32) {
	// Convert to MB (dividing by 1024*1024)
	sizeMB := sizeBytes / (1024 * 1024)

	if sizeMB >= 1024 {
		// Display as GB
		sizeGB := sizeMB / 1024
		uartPutUint32(sizeGB)
		uartPuts(" GB")
	} else {
		// Display as MB
		uartPutUint32(sizeMB)
		uartPuts(" MB")
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
	uartPuts("UART Test: Hello from simplified kernel!\r\n")

	// Exit via semihosting
	uartPuts("Exiting via semihosting\r\n")
	qemu_exit()
}

// KernelMain is the entry point called from boot.s
// For bare metal, we ensure it's not optimized away
//
//go:noinline
func KernelMain(r0, r1, atags uint32) {
	// Uncomment the line below to use simplified test kernel
	// SimpleTestKernel()
	// return

	_ = r0
	_ = r1

	// Breadcrumb: Entry to KernelMain
	// Raw UART poke before init to prove we reached KernelMain
	mmio_write(0x09000000, uint32('K'))
	mmio_write(0x09000000, uint32('M')) // 'M' = KernelMain entry

	// Breadcrumb: About to init UART
	mmio_write(0x09000000, uint32('U')) // 'U' = UART init starting

	// Initialize UART first for early debugging
	uartInit()
	mmio_write(0x09000000, uint32('u')) // 'u' = UART init done
	uartPuts("DEBUG: KernelMain after uartInit\r\n")

	// CRITICAL DIAGNOSTIC: Check SCTLR_EL1 to understand alignment fault behavior
	// Read current SCTLR_EL1 and print its value
	sctlr := read_sctlr_el1()
	uartPuts("SCTLR_EL1 = 0x")
	uartPutHex64(sctlr)
	uartPuts("\r\n")

	// Parse key bits for diagnosis
	mmuEnabled := (sctlr & 1) != 0            // Bit 0: M - MMU Enable
	alignCheck := (sctlr & 2) != 0            // Bit 1: A - Alignment Check Enable
	dcacheEnabled := (sctlr & 4) != 0         // Bit 2: C - Data Cache Enable
	icacheEnabled := (sctlr & (1 << 12)) != 0 // Bit 12: I - Instruction Cache Enable

	uartPuts("  M (MMU Enable):       ")
	if mmuEnabled {
		uartPuts("ENABLED\r\n")
	} else {
		uartPuts("DISABLED (memory=Device-nGnRnE by default!)\r\n")
	}

	uartPuts("  A (Alignment Check):  ")
	if alignCheck {
		uartPuts("ENABLED (unaligned access causes fault)\r\n")
	} else {
		uartPuts("DISABLED (unaligned allowed to Normal memory)\r\n")
	}

	uartPuts("  C (Data Cache):       ")
	if dcacheEnabled {
		uartPuts("ENABLED\r\n")
	} else {
		uartPuts("DISABLED\r\n")
	}

	uartPuts("  I (Instruction Cache): ")
	if icacheEnabled {
		uartPuts("ENABLED\r\n")
	} else {
		uartPuts("DISABLED\r\n")
	}

	// CRITICAL: If MMU is disabled, memory is Device-nGnRnE which requires strict alignment
	// Even STUR instruction cannot do unaligned access to Device memory!
	if !mmuEnabled {
		uartPuts("WARNING: MMU disabled - memory is Device type (strict alignment required)\r\n")
		uartPuts("This explains why STUR causes alignment fault!\r\n")
	}

	// TRY FIX: Disable alignment check bit (might not help if MMU is off)
	if alignCheck {
		uartPuts("Attempting to disable alignment check...\r\n")
		disable_alignment_check()
		sctlr2 := read_sctlr_el1()
		uartPuts("SCTLR_EL1 after disable = 0x")
		uartPutHex64(sctlr2)
		uartPuts("\r\n")
		if (sctlr2 & 2) == 0 {
			uartPuts("Alignment check disabled successfully\r\n")
		} else {
			uartPuts("WARNING: Failed to disable alignment check\r\n")
		}
	}

	// Verify stack pointer reading works correctly
	// TEMPORARILY DISABLED - testing offset fix
	// if verifyStackPointerReading() == 0 {
	// 	uartPuts("FATAL: Stack pointer reading verification failed!\r\n")
	// 	for {
	// 	}
	// }
	uartPuts("DEBUG: Stack pointer verification skipped (testing)\r\n")

	// Verify stack pointer reading works correctly
	// TEMPORARILY DISABLED - testing offset fix
	// if verifyStackPointerReading() == 0 {
	// 	uartPuts("FATAL: Stack pointer reading verification failed!\r\n")
	// 	for {
	// 	}
	// }
	uartPuts("DEBUG: SP reading verification skipped (testing #2)\r\n")

	// Breadcrumb: About to init runtime stubs
	uartPutc('R') // 'R' = Runtime stubs init starting

	// Initialize minimal runtime structures for write barrier
	// This sets up g0, m0, and write barrier buffers so that gcWriteBarrier can work
	// Note: x28 (goroutine pointer) is set in lib.s before calling KernelMain
	initRuntimeStubs()
	uartPutc('r') // 'r' = Runtime stubs init done
	uartPuts("DEBUG: KernelMain after initRuntimeStubs\r\n")

	// Breadcrumb: About to init kernel stack
	uartPutc('S') // 'S' = Stack init starting

	// Initialize kernel stack info for Go runtime stack checks
	// The actual stack pointer is set in boot.s, but we need to tell
	// the Go runtime where the stack bounds are
	initKernelStack()
	uartPutc('s') // 's' = Stack init done

	// Initialize memory management (pages and heap) early so we can allocate the main goroutine
	// This must happen before createKernelGoroutine since it needs kmalloc
	uartPuts("DEBUG: Initializing memory management (early)...\r\n")
	uartPutc('M') // 'M' = Memory init starting
	memInit(0)    // No ATAGs in QEMU, pass 0
	uartPutc('m') // 'm' = Memory init done
	uartPuts("DEBUG: Memory management initialized\r\n")

	// Create main kernel goroutine with 32KB stack
	uartPuts("DEBUG: Creating main kernel goroutine...\r\n")
	mainG := createKernelGoroutine(nil, KERNEL_GOROUTINE_STACK_SIZE)
	if mainG == nil {
		uartPuts("ERROR: Failed to create main goroutine!\r\n")
		for {
			// Hang
		}
	}

	// Set up the function to call (kernelMainBody)
	// We'll use a wrapper function that we can get the address of
	// For now, set PC to 0 and let the assembly function handle it
	mainG.startpc = 0  // Will be set by switchToGoroutine
	mainG.sched.pc = 0 // Will be set by switchToGoroutine

	uartPuts("DEBUG: Switching to main goroutine stack via g0...\r\n")

	// Use g0 to switch to the main goroutine
	// This is the proper pattern: g0 (system goroutine) switches to user goroutines
	// The assembly function will:
	//   1. Set x28 to the new goroutine
	//   2. Switch SP to the new goroutine's stack
	//   3. Return, allowing us to call the function on the new stack
	// Get function address using reflect (works in bare metal if we have reflect)
	// For now, pass 0 and the assembly will just set up the stack/x28 and return
	switchToGoroutine(mainG, 0)

	// After switchToGoroutine returns, we're on the new stack with x28 set
	// Now we can safely call the function
	uartPuts("DEBUG: Stack switched by g0, calling kernelMainBodyWrapper...\r\n")
	kernelMainBodyWrapper()

	// Should never return here - switchToGoroutine should jump to the new goroutine
	uartPuts("ERROR: switchToGoroutine returned (unexpected)\r\n")
	for {
	} // Hang

	// Should never return here (kernelMainBodyWrapper should not return)
	uartPuts("ERROR: Returned from kernelMainBodyWrapper (unexpected)\r\n")
	for {
		// Hang
	}

	uartPuts("DEBUG: KernelMain about to return\r\n")
}

// kernelMainBodyWrapper is called from assembly after switching to the new goroutine's stack
// This will be promoted to a global symbol via objcopy in the Makefile
//
//go:noinline
func kernelMainBodyWrapper() {
	uartPuts("DEBUG: Entered kernelMainBodyWrapper\r\n")
	uartPuts("DEBUG: wrapper about to call kernelMainBody\r\n")
	kernelMainBody()
	uartPuts("DEBUG: wrapper returned from kernelMainBody (unexpected)\r\n")
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
	// Minimal staged bring-up with early return after stage2

	// Stage 1: simple UART prints
	mmio_write(0x09000000, uint32('B'))
	uartPuts("DEBUG: Entered kernelMainBody (stage1)\r\n")

	putc := func(c byte) {
		uartPutc(c)
	}
	puts := func(s string) {
		uartPutc('p') // Breadcrumb: puts function entered
		for i := 0; i < len(s); i++ {
			putc(s[i])
		}
		uartPutc('P') // Breadcrumb: puts function exiting
	}
	puts("Hello, Mazarin!\r\n")
	puts("\r\n")
	uartPuts("DEBUG: stage1 complete, proceeding to write barrier check\r\n")

	// Stage 2: write barrier flag check
	uartPuts("DEBUG: stage2 write barrier check start\r\n")
	wbFlagAddr := uintptr(0x40026b40) // runtime.writeBarrier in RAM
	wbFlag := readMemory32(wbFlagAddr)
	if wbFlag == 0 {
		puts("ERROR: Write barrier flag not set!\r\n")
	} else {
		puts("Write barrier flag: enabled\r\n")
	}
	uartPuts("DEBUG: stage2 complete, skipping stage3 (memInit already done)\r\n")

	// Stage 3: memory init
	// NOTE: memInit is now called early in KernelMain (before goroutine creation)
	// so we skip it here to avoid double initialization
	uartPuts("DEBUG: stage3 memInit skipped (already initialized)\r\n")
	uartPutc('3') // Breadcrumb: memInit already done
	puts("Memory management already initialized (done early for goroutine allocation)\r\n")
	uartPutc('3') // Breadcrumb: message printed
	uartPutc('M') // Breadcrumb: memInit skipped
	// memInit(0)    // Already called early in KernelMain
	// CRITICAL: Use assembly-level UART write to avoid any Go function call overhead
	// This helps us see if the crash is in the return path or in a function call
	uart_putc_pl011('m') // Breadcrumb: memInit returned (assembly-level, bypasses Go)
	uart_putc_pl011('4') // Breadcrumb: about to call uartPuts (assembly-level)
	// Use direct uartPuts instead of closure to avoid potential write barrier issues
	uartPuts("Memory management initialized\r\n")
	// CRITICAL: Immediately after uartPuts returns, use assembly to print breadcrumb
	// This helps us see if the crash is during return or after
	uart_putc_pl011('X') // Breadcrumb: uartPuts returned (X = eXit from uartPuts)
	// CRITICAL: Add memory barrier and delay after uartPuts returns
	// This ensures any pending write barrier operations complete
	dsb()
	uart_putc_pl011('4') // Breadcrumb: after barrier (assembly-level)
	uart_putc_pl011('m') // Breadcrumb: memInit message printed (assembly-level)

	// Initialize UART ring buffer now that memory management is available
	uartPutc('Q') // Breadcrumb: about to call uartInitRingBufferAfterMemInit
	// TEMPORARY: Skip ring buffer to isolate crash
	//uartInitRingBufferAfterMemInit()
	uartPutc('S') // Breadcrumb: SKIPPED ring buffer init
	uartPutc('q') // Breadcrumb: continuing without ring buffer

	uartPuts("DEBUG: stage3 complete, proceeding to stage4 (framebuffer)\r\n")

	// Stage 4: framebuffer init + framebuffer text init
	uartPuts("DEBUG: stage4 framebuffer init start\r\n")
	uartPutc('4') // Breadcrumb: entering stage 4
	fbResult := framebufferInit()
	if fbResult != 0 {
		uartPuts("ERROR: Framebuffer initialization failed!\r\n")
		uartPutc('F') // Breadcrumb: framebuffer failed
		qemu_exit()
		return
	}
	uartPutc('f') // Breadcrumb: framebuffer succeeded
	uartPuts("DEBUG: framebufferInit succeeded\r\n")

	uartPutc('t') // Breadcrumb: about to init framebuffer text
	if err := InitFramebufferText(fbinfo.Buf, fbinfo.Width, fbinfo.Height, fbinfo.Pitch); err != nil {
		uartPuts("ERROR: Framebuffer text initialization failed\r\n")
		uartPutc('T') // Breadcrumb: text init failed
		qemu_exit()
		return
	}
	uartPutc('T') // Breadcrumb: text init succeeded (capital T for success)
	uartPuts("DEBUG: InitFramebufferText completed\r\n")

	// Test framebuffer text rendering
	testFramebufferText()
	uartPutc('p') // Breadcrumb: text written

	uartPuts("DEBUG: stage4 complete, proceeding to stage5 (exceptions)\r\n")

	// Stage 5: exception handler init
	uartPuts("DEBUG: stage5 InitializeExceptions start\r\n")
	if err := InitializeExceptions(); err != nil {
		uartPuts("ERROR: Failed to initialize exception handling\r\n")
		qemu_exit()
		return
	}
	uartPuts("DEBUG: InitializeExceptions completed\r\n")

	uartPuts("DEBUG: stage5 complete, proceeding to stage6 (GIC)\r\n")

	// Stage 6: GIC init
	uartPuts("DEBUG: stage6 gicInit start\r\n")
	gicInit()
	uartPuts("DEBUG: gicInit completed\r\n")

	// Set up UART interrupts now that GIC is initialized
	uartSetupInterrupts()

	uartPuts("DEBUG: stage6 complete, proceeding to stage7 (timer)\r\n")

	// Stage 7: timer init
	uartPuts("DEBUG: stage7 timerInit start\r\n")
	timerInit()
	uartPuts("DEBUG: timerInit completed\r\n")

	uartPuts("DEBUG: stage7 complete, entering idle loop\r\n")
	uartPuts("DEBUG: All initialization complete\r\n")

	// Try to enable interrupts using the assembly function
	// Note: This might cause issues, but we need interrupts enabled for timer
	uartPuts("DEBUG: Attempting to enable interrupts...\r\n")
	enable_irqs_asm() // Use the minimal version that just does msr DAIFCLR

	// CRITICAL: After enabling interrupts, avoid calling functions that might
	// create unaligned stores (like uartPuts with string literals) until we're
	// in the idle loop. The interrupt might fire immediately and cause issues.
	// Just enter the idle loop directly.

	// Enter idle loop - wait for timer interrupts
	// Timer interrupts will fire every second and print dots to framebuffer
	for {
		// Busy-wait loop - interrupts will fire and be handled
		// The timer interrupt handler will print dots to the framebuffer
	}
}

// testFramebufferText tests the framebuffer text rendering system
//
//go:nosplit
func testFramebufferText() {
	uartPuts("DEBUG: Framebuffer text system test starting\r\n")

	// Try to render the boot image first
	uartPuts("DEBUG: About to render boot image\r\n")
	imageData := GetBootMazarinImageData()
	if imageData != nil {
		uartPuts("DEBUG: Boot image data found, rendering along right edge\r\n")
		// Position 512x768 image along right edge of 1024x768 screen
		// Image will be pushed up as text is emitted
		// X position: 1024 - 512 = 512 (right edge, image will be fully visible)
		// Y position: 0 (top, will scroll up as text is added)
		RenderImageData(imageData, 128, 0, false)
		uartPuts("DEBUG: Boot image rendered\r\n")
	} else {
		uartPuts("DEBUG: Boot image data not available\r\n")
	}

	FramebufferPuts("===== Mazarin Kernel =====\r\n")
	FramebufferPuts("Framebuffer Text Output Ready\r\n")
	FramebufferPuts("\r\n")
	FramebufferPuts("Display: 1024x768 pixels\r\n")
	FramebufferPuts("Format: XRGB8888 (32-bit)\r\n")
	uartPuts("DEBUG: Framebuffer text system test complete\r\n")
}

// drawTestPattern draws a simple test pattern to the framebuffer
// This helps verify that VNC display is working correctly
// Uses XRGB8888 format (32-bit pixels: 0x00RRGGBB)
//
//go:nosplit
func drawTestPattern() {
	uartPuts("Drawing test pattern...\r\n")
	if fbinfo.Buf == nil {
		uartPuts("ERROR: Framebuffer not initialized\r\n")
		return
	}
	uartPuts("FB buf OK\r\n")

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
	uartPuts("Test pattern drawn\r\n")
}

// Dummy main() function required by Go's c-archive build mode
// This is never called - boot.s calls KernelMain directly
// We call KernelMain here to ensure it's compiled and not optimized away
func main() {
	// Call KernelMain with dummy values to ensure it's compiled
	// This will never execute in bare metal, but ensures the function exists
	KernelMain(0, 0, 0)

	// Reference interrupt handlers to prevent optimization
	// These are called from assembly interrupt handlers and must not be optimized away
	// This will never execute in bare metal, but ensures the functions exist
	handleTimerIRQ()
	handleUARTIRQ()
	ExceptionHandler(0, 0, 0, 0, 0)

	// This should never execute in bare metal
	for {
	}
}
