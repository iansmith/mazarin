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

	MAILBOX_READ    = MAILBOX_BASE + 0x00
	MAILBOX_STATUS  = MAILBOX_BASE + 0x18
	MAILBOX_WRITE   = MAILBOX_BASE + 0x20
)

//go:nosplit
func uartInit() {
	mmio_write(UART0_CR, 0x00000000)

	mmio_write(GPPUD, 0x00000000)
	delay(150)

	mmio_write(GPPUDCLK0, (1<<14)|(1<<15))
	delay(150)

	mmio_write(GPPUDCLK0, 0x00000000)

	mmio_write(UART0_ICR, 0x7FF)

	mmio_write(UART0_IBRD, 1)
	mmio_write(UART0_FBRD, 40)

	mmio_write(UART0_LCRH, (1<<4)|(1<<5)|(1<<6))

	mmio_write(UART0_IMSC, (1<<1)|(1<<4)|(1<<5)|(1<<6)|
		(1<<7)|(1<<8)|(1<<9)|(1<<10))

	mmio_write(UART0_CR, (1<<0)|(1<<8)|(1<<9))
}

//go:nosplit
func uartPutc(c byte) {
	for mmio_read(UART0_FR)&(1<<5) != 0 {
		// Wait for transmit FIFO to have space
	}
	mmio_write(UART0_DR, uint32(c))
}

//go:nosplit
func uartGetc() byte {
	for mmio_read(UART0_FR)&(1<<4) != 0 {
		// Wait for receive FIFO to have data
	}
	return byte(mmio_read(UART0_DR))
}

//go:nosplit
func uartPuts(str string) {
	for i := 0; i < len(str); i++ {
		uartPutc(str[i])
	}
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
	uartPuts("Hello, Mazarin!\r\n")
	uartPuts("Initializing memory...\r\n")

	// Initialize memory management (pages + heap)
	// This must be done before using kmalloc for mailbox messages
	// Note: memInit calls pageInit which can take a while for large memory
	// For now, do minimal initialization - just initialize heap at a fixed location
	// TODO: Fix full pageInit() which seems to hang
	// memInit(uintptr(atags))
	
	// Minimal heap initialization - set up heap at a safe location
	// Stack is at 0x400000, so we need heap well above that
	// Use 0x500000 (5MB) to be safe - well above stack at 0x400000
	heapStart := uintptr(0x500000)
	heapStart = (heapStart + 15) &^ 15 // Align to 16 bytes
	heapInit(heapStart)
	
	// Verify heap is initialized by checking if we can allocate
	testAlloc := kmalloc(100)
	if testAlloc == nil {
		uartPuts("ERROR: Heap initialization failed - cannot allocate!\r\n")
	} else {
		uartPuts("Test allocation (100 bytes) succeeded\r\n")
		// Don't free it - keep it allocated to test if that's the issue
		// kfree(testAlloc)
		uartPuts("Memory initialized (minimal, verified)\r\n")
	}
	
	// Initialize GPU/framebuffer
	uartPuts("Initializing framebuffer...\r\n")
	
	// Test heap with the approximate mailbox buffer size (should be ~80-100 bytes)
	// Mailbox buffer: 3 tags * ~20 bytes each + 12 bytes header = ~72 bytes, rounded to 80
	// Don't free the previous test allocation - test if multiple allocations work
	testMailboxSize := kmalloc(100)
	if testMailboxSize == nil {
		uartPuts("ERROR: Cannot allocate mailbox-sized buffer (100 bytes)!\r\n")
	} else {
		uartPuts("Mailbox-sized allocation (100 bytes) works\r\n")
		// Don't free it either - test if keeping allocations affects things
		// kfree(testMailboxSize)
	}
	
	fbResult := gpuInit()
	if fbResult != 0 {
		uartPuts("Error: Failed to initialize framebuffer\r\n")
		if fbResult == -1 {
			uartPuts("  Reason: Memory allocation failed (mailbox buffer)\r\n")
		} else if fbResult == -4 {
			uartPuts("  Reason: Buffer size too large\r\n")
		} else if fbResult == -2 {
			uartPuts("  Reason: Mailbox read failed (no GPU response)\r\n")
		} else if fbResult == -3 {
			uartPuts("  Reason: Unexpected response code\r\n")
		} else if fbResult == 1 {
			uartPuts("  Reason: GPU did not process request\r\n")
		} else if fbResult == 2 {
			uartPuts("  Reason: GPU returned error\r\n")
		} else {
			uartPuts("  Reason: Unknown error\r\n")
		}
		// Continue anyway - UART still works
	} else {
		uartPuts("Framebuffer initialized successfully\r\n")
		uartPuts("FB Size: ")
		uartPutUint32(fbinfo.BufSize)
		uartPuts(" bytes, Width: ")
		uartPutUint32(fbinfo.Width)
		uartPuts(", Height: ")
		uartPutUint32(fbinfo.Height)
		uartPuts("\r\n")
	}

	// Get and display memory size
	// Note: QEMU does not provide ATAGs for Raspberry Pi 4 - it uses Device Tree (DTB) instead
	// ATAGs are only available on real hardware with bootloaders that support them
	// See: https://www.qemu.org/docs/master/system/arm/raspi.html
	memSize := getMemSize(uintptr(atags))
	
	// Display on both UART and GPU
	uartPuts("Memory: ")
	gpuPuts("Memory: ")
	
	if memSize == 0 {
		// No ATAGs available (e.g., running in QEMU which uses Device Tree, not ATAGs)
		uartPuts("unknown (ATAGs not available)\r\n")
		gpuPuts("unknown (ATAGs not available)\n")
	} else {
		uartPutMemSize(memSize)
		uartPuts("\r\n")
		// For GPU, we'll just show a simple message
		gpuPuts("detected\n")
	}
	
	// Display welcome message on framebuffer
	gpuPuts("\nHello, Mazarin!\n")
	gpuPuts("Framebuffer is working!\n")

	// Echo loop - output to both UART and GPU
	for {
		c := uartGetc()
		uartPutc(c)
		uartPutc('\n')
		gpuPutc(c)
		gpuPutc('\n')
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
