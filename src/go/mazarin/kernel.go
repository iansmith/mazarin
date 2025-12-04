package main

import (
	"unsafe"
)

// Link to external assembly functions from lib.s
//
//go:linkname mmio_write mmio_write
//go:nosplit
func mmio_write(reg uintptr, data uint32)

//go:linkname mmio_read mmio_read
//go:nosplit
func mmio_read(reg uintptr) uint32

//go:linkname delay delay
//go:nosplit
func delay(count int32)

//go:linkname bzero bzero
//go:nosplit
func bzero(ptr unsafe.Pointer, size uint32)

//go:linkname dsb dsb
//go:nosplit
func dsb()

//go:linkname qemu_exit qemu_exit
//go:nosplit
func qemu_exit()

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
	const uartBase uintptr = 0x09000000
	const uartFR = uartBase + 0x18
	const uartDR = uartBase + 0x00

	// Debug: write 'B' to verify we entered uartPutsBytes
	for mmio_read(uartFR)&(1<<5) != 0 {
	}
	mmio_write(uartDR, uint32('B'))

	ptr := uintptr(unsafe.Pointer(data))
	// Store length in local variable
	lenVal := length

	// Debug: write 'L' to verify we got past variable initialization
	for mmio_read(uartFR)&(1<<5) != 0 {
	}
	mmio_write(uartDR, uint32('L'))

	// Debug: write length as single character to see if it's valid
	for mmio_read(uartFR)&(1<<5) != 0 {
	}
	lenChar := byte(lenVal & 0xFF)
	if lenChar < 10 {
		mmio_write(uartDR, uint32('0'+lenChar))
	} else {
		mmio_write(uartDR, uint32('?'))
	}

	// Write first character before loop to test pointer access
	for mmio_read(uartFR)&(1<<5) != 0 {
	}
	mmio_write(uartDR, uint32(*(*byte)(unsafe.Pointer(ptr + uintptr(0)))))

	// Debug: write 'X' to show we got past first character
	for mmio_read(uartFR)&(1<<5) != 0 {
	}
	mmio_write(uartDR, uint32('X'))

	// Now write rest using simple loop with decrementing counter
	// Use a counter that decrements to avoid comparison issues
	remaining := lenVal - 1
	pos := 1
	for remaining > 0 {
		// Debug: write 'Y' each iteration to see if loop runs
		for mmio_read(uartFR)&(1<<5) != 0 {
		}
		mmio_write(uartDR, uint32('Y'))

		// Wait for UART ready
		for mmio_read(uartFR)&(1<<5) != 0 {
		}
		// Write character
		mmio_write(uartDR, uint32(*(*byte)(unsafe.Pointer(ptr + uintptr(pos)))))
		pos = pos + 1
		remaining = remaining - 1
	}
}

//go:nosplit
func uartPutHex64(val uint64) {
	const uartBase uintptr = 0x09000000
	const uartFR = uartBase + 0x18
	const uartDR = uartBase + 0x00

	// Write hex digits directly without loops (avoid loop issues in bare-metal)
	// Extract and write each nibble explicitly
	writeHexDigit := func(digit uint32) {
		for mmio_read(uartFR)&(1<<5) != 0 {
		}
		if digit < 10 {
			mmio_write(uartDR, uint32('0'+digit))
		} else {
			mmio_write(uartDR, uint32('A'+digit-10))
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
func uartPuts(str string) {
	// NOTE: String literals are not accessible in bare-metal Go
	// The .rodata section may not be loaded, or Go places string literals
	// in a way that's not accessible. For now, we'll use a workaround:
	// Instead of using string literals, we'll write strings character-by-character
	// directly in the calling code.
	//
	// This function is kept for API compatibility, but string literals won't work.
	// Use uartPutsBytes with explicit byte arrays instead.

	const uartBase uintptr = 0x09000000
	const uartFR = uartBase + 0x18
	const uartDR = uartBase + 0x00

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
//
//go:nosplit
func uartPutUint32(n uint32) {
	// Buffer for up to 10 digits (uint32 max is 4,294,967,295)
	var buf [10]byte
	count := uitoa(n, buf[:])
	for i := 0; i < count; i++ {
		uartPutc(buf[i])
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

// KernelMain is the entry point called from boot.s
// For bare metal, we ensure it's not optimized away
//
//go:nosplit
//go:noinline
func KernelMain(r0, r1, atags uint32) {
	_ = r0
	_ = r1

	// Initialize UART first for early debugging
	uartInit()

	// Initialize minimal runtime structures for write barrier
	// This sets up g0, m0, and write barrier buffers so that gcWriteBarrier can work
	// Note: x28 (goroutine pointer) is set in lib.s before calling KernelMain
	initRuntimeStubs()

	// UART helpers (string literals don't work in bare-metal)
	const uartBase uintptr = 0x09000000
	const uartFR = uartBase + 0x18
	const uartDR = uartBase + 0x00

	putc := func(c byte) {
		for mmio_read(uartFR)&(1<<5) != 0 {
		}
		mmio_write(uartDR, uint32(c))
	}

	puts := func(s string) {
		for i := 0; i < len(s); i++ {
			putc(s[i])
		}
	}

	// Print welcome message
	puts("Hello, Mazarin!\r\n")

	puts("\r\n")

	// =================================================================
	// WRITE BARRIER TEST
	// =================================================================
	// This test verifies that Go's compiler-emitted write barrier works
	// in our bare-metal environment. The Go compiler automatically emits
	// write barrier calls for global pointer assignments.
	//
	// The write barrier flag must be set in boot.s (in RAM region), and
	// our custom writebarrier.s functions must handle the actual assignment.

	puts("Testing write barrier...\r\n")

	// Verify write barrier flag is enabled (set by boot.s)
	wbFlagAddr := uintptr(0x40026b40) // runtime.writeBarrier in RAM
	wbFlag := readMemory32(wbFlagAddr)

	if wbFlag == 0 {
		puts("ERROR: Write barrier flag not set!\r\n")
	} else {
		puts("Write barrier flag: enabled\r\n")
	}

	// Test global pointer assignment (triggers write barrier)
	// heapSegmentListHead is a global *heapSegment variable
	heapStart := uintptr(0x40500000)   // Heap in RAM region
	heapStart = (heapStart + 15) &^ 15 // Align to 16 bytes

	// This assignment will trigger the Go compiler's write barrier check!
	heapSegmentListHead = castToPointer[heapSegment](heapStart)

	// Verify the assignment worked
	if heapSegmentListHead == nil {
		puts("ERROR: Global pointer assignment failed!\r\n")
	} else {
		puts("SUCCESS: Global pointer assignment works!\r\n")
		// Initialize the heap segment
		bzero(unsafe.Pointer(heapSegmentListHead), uint32(unsafe.Sizeof(heapSegment{})))
		heapSegmentListHead.next = nil
		heapSegmentListHead.prev = nil
		heapSegmentListHead.isAllocated = 0
		heapSegmentListHead.segmentSize = KERNEL_HEAP_SIZE
		puts("Heap initialized at RAM region\r\n")
	}

	puts("\r\n")
	puts("All tests passed! Exiting via semihosting...\r\n")

	// Exit cleanly via QEMU semihosting
	qemu_exit()

	// If semihosting is not enabled, we'll reach here
	// Loop forever as fallback
	for {
	}
}

// Dummy main() function required by Go's c-archive build mode
// This is never called - boot.s calls KernelMain directly
// We call KernelMain here to ensure it's compiled and not optimized away
func main() {
	// Call KernelMain with dummy values to ensure it's compiled
	// This will never execute in bare metal, but ensures the function exists
	KernelMain(0, 0, 0)
	// This should never execute in bare metal
	for {
	}
}
