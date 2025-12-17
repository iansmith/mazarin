package main

import (
	// "runtime/debug" // Temporarily disabled for exit debugging
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

	// Check SCTLR_EL1 for alignment check bit
	sctlr := asm.ReadSctlrEl1()
	alignCheck := (sctlr & 2) != 0 // Bit 1: A - Alignment Check Enable

	// Disable alignment check if enabled (required for Go runtime)
	if alignCheck {
		asm.DisableAlignmentCheck()
	}

	// Initialize minimal runtime structures for write barrier
	initRuntimeStubs()

	// Initialize MMU (required before heap - enables Normal memory for unaligned access)
	if !initMMU() {
		print("FATAL: MMU initialization failed\r\n")
		for {
		}
	}
	if !enableMMU() {
		print("FATAL: MMU enablement failed\r\n")
		for {
		}
	}

	// Initialize Go runtime heap allocator
	initGoHeap()

	// Initialize kernel stack info for Go runtime stack checks
	initKernelStack()

	// Initialize memory management
	memInit(0) // No ATAGs in QEMU, pass 0

	// Verify mcache.alloc[] is still valid after memInit
	mcacheStructAddr := uintptr(0x41020000)
	allocArrayStart := mcacheStructAddr + 0x30
	if readMemory64(allocArrayStart+47*8) != 0x40108500 {
		// Reinitialize if corrupted
		emptymspanAddr := uint64(0x40108500)
		for i := uintptr(0); i < 136; i++ {
			writeMemory64(allocArrayStart+i*8, emptymspanAddr)
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

	// Stage 3: exception handler init
	if err := InitializeExceptions(); err != nil {
		print("ERROR: Exception init failed\r\n")
		abortBoot("Exception handler initialization failed")
		return
	}

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

			// Render gg startup circle
			drawGGStartupCircle()
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

	// Stage 11: Goroutine test
	FramebufferPuts("Testing goroutine spawn...\r\n")
	spawnGoroutine(testGoroutineFunc)
	FramebufferPuts("Goroutine test complete!\r\n")

	// Drain ring buffer manually since interrupts aren't enabled yet
	// This allows print() calls from goroutine test to be visible
	for i := 0; i < 1000; i++ {
		uartDrainRingBuffer()
	}

	FramebufferPuts("Boot complete.\r\n")

	// Enable CPU interrupts now that everything is initialized
	// This unmasks the I bit in PSTATE to allow IRQs to fire
	asm.EnableIrqsAsm()

	// Enter idle loop - wait for timer interrupts
	// Timer interrupts will fire every 5 seconds and print dots to framebuffer
	for {
		// Busy-wait loop - interrupts will fire and be handled
		// The timer interrupt handler will print dots to the framebuffer
	}
}

// testGoroutineFunc is a simple function that runs in a spawned goroutine
//
//go:noinline
func testGoroutineFunc() {
	print("go go goroutine\n")
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
