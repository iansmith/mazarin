package main

import (
	"unsafe"

	"mazboot/asm"
)

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
	sp := asm.GetCallerStackPointer()
	return (sp & 0xF) == 0
}

// printSPBreadcrumb prints a breadcrumb with label and SP value
// Format: "[label] SP=0xXXXXXXXX\r\n"
// Uses uartPutc for all characters to avoid string literal issues
//
//go:nosplit
func printSPBreadcrumb(label byte) {
	// Get SP BEFORE any function calls to avoid corruption
	sp := asm.GetCallerStackPointer()

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
	spAfter := asm.GetStackPointer()
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
	asm.QemuExit()
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

	// On QEMU virt, the DTB pointer is passed in as the "atags" parameter (low 32 bits).
	// boot.s captures QEMU's reset-time x0 and passes it through to kernel_main in x2.
	setDTBPtr(uintptr(atags))

	// Raw UART poke before init to prove we reached KernelMain
	asm.MmioWrite(0x09000000, uint32('K'))
	asm.MmioWrite(0x09000000, uint32('M')) // 'M' = KernelMain entry

	asm.MmioWrite(0x09000000, uint32('U')) // 'U' = UART init starting

	// Initialize UART first for early debugging
	uartInit()
	asm.MmioWrite(0x09000000, uint32('u')) // 'u' = UART init done
	uartPuts("DEBUG: KernelMain after uartInit\r\n")

	// DTB pointer diagnostics (QEMU virt)
	uartPuts("DEBUG: atags/DTB ptr = 0x")
	uartPutHex64(uint64(uintptr(atags)))
	uartPuts("\r\n")
	if atags != 0 {
		magic := *(*uint32)(unsafe.Pointer(uintptr(atags)))
		uartPuts("DEBUG: DTB magic @atags = 0x")
		uartPutHex64(uint64(magic))
		uartPuts("\r\n")
	}
	magic400 := *(*uint32)(unsafe.Pointer(uintptr(0x40000000)))
	uartPuts("DEBUG: DTB magic @0x40000000 = 0x")
	uartPutHex64(uint64(magic400))
	uartPuts("\r\n")

	// CRITICAL DIAGNOSTIC: Check SCTLR_EL1 to understand alignment fault behavior
	// Read current SCTLR_EL1 and print its value
	sctlr := asm.ReadSctlrEl1()
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
		asm.DisableAlignmentCheck()
		sctlr2 := asm.ReadSctlrEl1()
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

	uartPutc('R') // 'R' = Runtime stubs init starting

	// Initialize minimal runtime structures for write barrier
	// This sets up g0, m0, and write barrier buffers so that gcWriteBarrier can work
	// Note: x28 (goroutine pointer) is set in lib.s before calling KernelMain
	initRuntimeStubs()
	uartPutc('r') // 'r' = Runtime stubs init done
	uartPuts("DEBUG: KernelMain after initRuntimeStubs\r\n")

	uartPutc('S') // 'S' = Stack init starting

	// Initialize kernel stack info for Go runtime stack checks
	// The actual stack pointer is set in boot.s, but we need to tell
	// the Go runtime where the stack bounds are
	initKernelStack()
	uartPutc('s') // 's' = Stack init done

	// Initialize memory management (pages and heap) early so we can allocate the main goroutine
	// This must happen before createKernelGoroutine since it needs kmalloc
	uartPuts("DEBUG: Initializing memory management (early)...\r\n")
	memInit(0) // No ATAGs in QEMU, pass 0

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
	asm.SwitchToGoroutine(unsafe.Pointer(mainG))

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

	// Stage 0: UART initialization (required for all debugging output)
	uartPuts("DEBUG: Initializing UART...\r\n")
	uartInit()
	uartPuts("DEBUG: UART initialized\r\n")
	uartPuts("Hello, Mazarin!\r\n")
	uartPuts("\r\n")

	// Stage 1: write barrier flag check
	uartPuts("DEBUG: stage1 write barrier check start\r\n")
	wbFlagAddr := uintptr(0x40026b40) // runtime.writeBarrier in RAM
	wbFlag := readMemory32(wbFlagAddr)
	if wbFlag == 0 {
		uartPuts("ERROR: Write barrier flag not set!\r\n")
	}
	uartPuts("DEBUG: stage2 complete, skipping stage3 (memInit already done)\r\n")

	// Stage 2: memory init
	// NOTE: memInit is now called early in KernelMain (before goroutine creation)
	// so we skip it here to avoid double initialization
	uartPuts("DEBUG: stage2 memInit skipped (already initialized)\r\n")
	uartPuts("DEBUG: Memory management already initialized (done early for goroutine allocation)\r\n")
	// memInit(0)    // Already called early in KernelMain
	// Use direct uartPuts instead of closure to avoid potential write barrier issues
	uartPuts("DEBUG: Memory management initialized\r\n")
	// Add memory barrier to ensure any pending write barrier operations complete
	asm.Dsb()

	uartPuts("DEBUG: stage2 complete, proceeding to stage3 (exceptions)\r\n")

	// Stage 3: exception handler init (CRITICAL: Must be before MMU)
	uartPuts("DEBUG: stage3 InitializeExceptions start\r\n")
	if err := InitializeExceptions(); err != nil {
		uartPuts("ERROR: Failed to initialize exception handling\r\n")
		abortBoot("Exception handler initialization failed")
		return
	}
	uartPuts("DEBUG: InitializeExceptions completed\r\n")

	uartPuts("DEBUG: stage3 complete, proceeding to stage4 (MMU)\r\n")

	// Stage 4: MMU initialization (interrupts disabled)
	uartPuts("DEBUG: stage4 MMU initialization start\r\n")

	// CRITICAL: Disable interrupts during MMU initialization for atomicity
	uartPuts("DEBUG: Disabling interrupts for MMU initialization...\r\n")
	asm.DisableIrqs()

	uartPutc('M') // 'M' = MMU init starting
	uartPuts("DEBUG: Initializing MMU...\r\n")
	if !initMMU() {
		abortBoot("MMU initialization failed - cannot continue without MMU!")
	}
	uartPutc('m') // 'm' = MMU init done
	if !enableMMU() {
		abortBoot("MMU enablement failed - cannot continue without MMU!")
	}
	uartPutc('E') // 'E' = MMU enable done
	uartPuts("DEBUG: MMU enabled successfully\r\n")

	// Re-enable interrupts after MMU is fully enabled
	uartPuts("DEBUG: Re-enabling interrupts after MMU initialization...\r\n")
	asm.EnableIrqsAsm()

	uartPuts("DEBUG: stage4 complete, proceeding to stage5 (memory management)\r\n")

	// Stage 5: Memory management (if not done earlier)
	// NOTE: memInit may already be done in KernelMain() for goroutine allocation
	// If not, initialize it here after MMU

	// Stage 6: UART ring buffer (uses kmallocReserved, requires MMU for virtual addresses)
	uartPuts("DEBUG: stage6 Initializing UART ring buffer...\r\n")
	uartInitRingBufferAfterMemInit()
	uartPuts("DEBUG: UART ring buffer initialized\r\n")

	// Stage 7: Framebuffer (uses kmallocReserved, requires MMU for virtual addresses)
	uartPuts("DEBUG: stage7 Initializing framebuffer...\r\n")
	fbResult := framebufferInit()
	if fbResult != 0 {
		uartPuts("ERROR: Framebuffer initialization failed!\r\n")
		abortBoot("Framebuffer init failed after MMU enablement")
	}
	uartPuts("DEBUG: Framebuffer initialized successfully\r\n")

	// Initialize framebuffer text rendering
	if err := InitFramebufferText(fbinfo.Buf, fbinfo.Width, fbinfo.Height, fbinfo.Pitch); err != nil {
		uartPuts("ERROR: Framebuffer text initialization failed\r\n")
		abortBoot("Framebuffer text init failed after MMU enablement")
	}
	uartPuts("DEBUG: Framebuffer text initialized successfully\r\n")

	uartPuts("DEBUG: stage7 complete, proceeding to stage8 (GIC)\r\n")

	// Stage 8: GIC init (not needed for MMU, can be done after)
	uartPuts("DEBUG: stage8 gicInit start\r\n")
	gicInit()
	uartPuts("DEBUG: gicInit completed\r\n")

	// Check security state before setting up interrupts
	checkSecurityState()

	// Set up UART interrupts now that GIC is initialized
	uartPuts("DEBUG: Setting up UART interrupts (ID 33)...\r\n")
	uartSetupInterrupts()
	uartPuts("DEBUG: UART interrupts configured\r\n")

	uartPuts("DEBUG: stage8 complete, proceeding to stage9 (timer)\r\n")

	// Stage 9: Timer init (not needed for MMU, can be done after)
	uartPuts("DEBUG: stage9 timerInit start\r\n")
	timerInit()
	uartPuts("DEBUG: timerInit completed\r\n")

	uartPuts("DEBUG: stage9 complete, proceeding to stage10 (SDHCI)\r\n")

	// Stage 10: SDHCI init (not needed for MMU, can be done after)
	uartPuts("DEBUG: stage10 sdhciInit start\r\n")
	if !sdhciInit() {
		abortBoot("sdhciInit failed - cannot load kernel from SD card!")
	}
	uartPuts("DEBUG: sdhciInit completed successfully\r\n")
	uartPutc('S') // Breadcrumb: SDHCI done

	uartPuts("DEBUG: All initialization complete\r\n")

	// Interrupts were already re-enabled after MMU initialization above
	// No need to enable again here

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

// abortBoot aborts the boot process with a fatal error message
// This function prints the error message, exits QEMU, and hangs forever
// Used by critical initialization failures (MMU, SDHCI, etc.)
//
//go:nosplit
func abortBoot(message string) {
	uartPuts("FATAL: ")
	uartPuts(message)
	uartPuts("\r\n")
	uartPuts("Aborting boot process...\r\n")
	asm.QemuExit()
	for {
		// Hang forever
	}
}
