//go:build qemuvirt && aarch64

package main

import (
	"mazboot/asm"
	"unsafe"
)

// QEMU virt machine UART constants
// The virt machine uses PL011 UART at 0x9000000 (different from Raspberry Pi)
const (
	// PL011 UART base address for QEMU virt machine
	// Try both formats - sometimes written as 0x09000000
	QEMU_UART_BASE = 0x09000000 // PL011 UART base for virt machine (0x09000000)

	QEMU_UART_DR   = QEMU_UART_BASE + 0x00
	QEMU_UART_FR   = QEMU_UART_BASE + 0x18
	QEMU_UART_IBRD = QEMU_UART_BASE + 0x24
	QEMU_UART_FBRD = QEMU_UART_BASE + 0x28
	QEMU_UART_LCRH = QEMU_UART_BASE + 0x2C
	QEMU_UART_CR   = QEMU_UART_BASE + 0x30
	QEMU_UART_ICR  = QEMU_UART_BASE + 0x44
)

// uartInit initializes the UART for QEMU virt machine
// Uses PL011 UART at 0x09000000
// Follows proper PL011 initialization sequence
//
//go:nosplit
func uartInit() {
	// Initialize UART using proper PL011 sequence
	asm.UartInitPl011()

	// Note: Ring buffer initialization deferred until after memInit()
	// Call uartInitRingBuffer() after memory management is set up
}

// uartInitRingBufferAfterMemInit initializes the ring buffer after memory is available
// Call this after memInit() has been called
//
//go:nosplit
func uartInitRingBufferAfterMemInit() {
	// Initialize ring buffer for interrupt-driven transmission
	uartInitRingBuffer()

	uartPuts("UART: Ring buffer ready (interrupt setup deferred until after GIC init)\r\n")
}

// uartSetupInterrupts configures UART interrupts after GIC initialization
// Call this after gicInit() has been called
//
//go:nosplit
func uartSetupInterrupts() {
	// Enable TX interrupt in UART (bit 5 = TXIM)
	// This triggers when transmit FIFO has space available
	// Note: We enable it in GIC but don't set IMSC yet - it's set when data is enqueued

	// Enable UART interrupt (ID 33) in GIC
	gicEnableInterrupt(IRQ_ID_UART_SPI)

	uartPuts("UART: TX interrupt enabled in GIC (ID 33)\r\n")
}

// uartDrainRingBuffer drains one character from ring buffer to UART
// Call this periodically to transmit queued characters when interrupts are disabled
//
//go:nosplit
func uartDrainRingBuffer() {
	if uartRingBuf == nil {
		return
	}

	// Check if UART TX FIFO has space
	fr := asm.MmioRead(QEMU_UART_BASE + 0x18) // UART_FR
	if fr&(1<<5) == 0 {                       // TXFF bit clear = FIFO not full
		// Try to dequeue a character
		if c, ok := uartDequeue(); ok {
			// Write character to UART
			asm.MmioWrite(QEMU_UART_DR, uint32(c))
		}
	}
}

// UartTransmitHandler handles UART transmit interrupt
// Called from assembly IRQ handler when UART TX FIFO has space
// This function MUST be nosplit and minimal - it's called from interrupt context
//
//go:nosplit
//go:noinline
func UartTransmitHandler() {
	// Breadcrumb: Go UART handler called
	asm.UartPutcPl011('G')

	// Check if ring buffer is initialized
	if uartRingBuf == nil {
		// No ring buffer - just disable TX interrupt
		asm.UartPutcPl011('N')                // No ring buffer
		asm.MmioWrite(QEMU_UART_BASE+0x38, 0) // UART_IMSC - disable all interrupts
		return
	}

	// Try to dequeue a character from ring buffer
	if c, ok := uartDequeue(); ok {
		// Write character to UART data register
		asm.MmioWrite(QEMU_UART_DR, uint32(c))

		// Check if buffer is empty now
		isEmpty := (uartRingBuf.head == uartRingBuf.tail)
		if isEmpty {
			asm.MmioWrite(QEMU_UART_BASE+0x38, 0) // UART_IMSC - disable TX interrupt
		}
		// Otherwise, TX interrupt will fire again when FIFO has space
	} else {
		// Ring buffer empty - disable TX interrupt
		asm.MmioWrite(QEMU_UART_BASE+0x38, 0) // UART_IMSC - disable TX interrupt
	}

	// Clear UART TX interrupt
	asm.MmioWrite(QEMU_UART_ICR, 1<<5) // Clear TXIC bit
}

// uartPutc outputs a character via UART (QEMU virt machine)
// Uses interrupt-driven transmission via ring buffer when available
//
//go:nosplit
func uartPutc(c byte) {
	// Try to enqueue character to ring buffer
	if uartEnqueueOrOverflow(c) {
		// Character was enqueued successfully
		// Enable TX interrupt to start transmission
		asm.MmioWrite(QEMU_UART_BASE+0x38, 1<<5) // UART_IMSC - set TXIM bit (5)
	}
	// If enqueue failed (overflow), character was dropped and "***" was added
}

// uartGetc reads a character from UART (QEMU virt machine)
//
//go:nosplit
func uartGetc() byte {
	for asm.MmioRead(QEMU_UART_FR)&(1<<4) != 0 {
		// Wait for receive FIFO to have data
	}
	return byte(asm.MmioRead(QEMU_UART_DR))
}

// ============================================================================
// UART Ring Buffer Implementation for Interrupt-Driven Transmission
// ============================================================================

const UART_RING_BUFFER_SIZE = 4096 // 4KB buffer

type uartRingBuffer struct {
	buf  *[UART_RING_BUFFER_SIZE]byte // Fixed-size buffer
	head uint32                       // Write position (producer)
	tail uint32                       // Read position (consumer)
}

// Global ring buffer instance
var uartRingBuf *uartRingBuffer

// uartInitRingBuffer initializes the UART ring buffer
//
//go:nosplit
func uartInitRingBuffer() {
	uartPutc('Q') // Breadcrumb: uartInitRingBuffer entry

	// Allocate ring buffer structure via kmalloc
	uartPutc('k') // Breadcrumb: about to kmalloc struct
	buf := kmalloc(uint32(unsafe.Sizeof(uartRingBuffer{})))
	if buf == nil {
		uartPutc('!') // Breadcrumb: kmalloc struct failed
		uartPuts("UART: ERROR - Failed to allocate ring buffer struct\r\n")
		return
	}
	uartPutc('K') // Breadcrumb: kmalloc struct succeeded

	// Allocate the buffer array
	uartPutc('b') // Breadcrumb: about to kmalloc buffer
	buffer := kmalloc(UART_RING_BUFFER_SIZE)
	if buffer == nil {
		uartPutc('!') // Breadcrumb: kmalloc buffer failed
		uartPuts("UART: ERROR - Failed to allocate ring buffer data\r\n")
		return
	}
	uartPutc('B') // Breadcrumb: kmalloc buffer succeeded

	// Initialize the ring buffer
	uartPutc('i') // Breadcrumb: about to initialize ring buffer
	ringBuf := (*uartRingBuffer)(buf)

	// Zero the struct first
	uartPutc('z') // Breadcrumb: about to bzero struct
	asm.Bzero(unsafe.Pointer(ringBuf), uint32(unsafe.Sizeof(uartRingBuffer{})))
	uartPutc('Z') // Breadcrumb: bzero struct done

	// Set individual fields carefully
	uartPutc('1') // Breadcrumb: setting buf pointer
	ringBuf.buf = (*[UART_RING_BUFFER_SIZE]byte)(buffer)
	uartPutc('2') // Breadcrumb: buf pointer set
	ringBuf.head = 0
	uartPutc('3') // Breadcrumb: head set
	ringBuf.tail = 0
	uartPutc('4') // Breadcrumb: tail set

	uartPutc('a') // Breadcrumb: about to assign uartRingBuf

	// Use assembly function to store pointer without write barrier
	uartPutc('x') // Debug: before assignment
	asm.StorePointerNoBarrier((*unsafe.Pointer)(unsafe.Pointer(&uartRingBuf)), unsafe.Pointer(ringBuf))
	uartPutc('X') // Debug: after assignment

	uartPutc('A') // Breadcrumb: uartRingBuf assigned

	// Debug: print addresses
	uartPuts("UART Ring buffer debug:\r\n")
	uartPuts("  struct at: ")
	uartPutHex64(uint64(pointerToUintptr(buf)))
	uartPuts("\r\n  buffer at: ")
	uartPutHex64(uint64(pointerToUintptr(buffer)))
	uartPuts("\r\n  buf field: ")
	uartPutHex64(uint64(pointerToUintptr(unsafe.Pointer(ringBuf.buf))))
	uartPuts("\r\n")

	uartPuts("UART: Ring buffer initialized (4KB)\r\n")
	uartPutc('q') // Breadcrumb: uartInitRingBuffer done
}

// uartEnqueue adds a character to the ring buffer
// Returns true if successful, false if buffer is full
//
//go:nosplit
func uartEnqueue(c byte) bool {
	if uartRingBuf == nil {
		return false // Not initialized
	}

	nextHead := (uartRingBuf.head + 1) % UART_RING_BUFFER_SIZE

	// Check if buffer is full
	if nextHead == uartRingBuf.tail {
		return false // Buffer full
	}

	// Add character to buffer
	uartRingBuf.buf[uartRingBuf.head] = c
	uartRingBuf.head = nextHead

	return true
}

// uartDequeue removes a character from the ring buffer
// Returns (character, true) if successful, (_, false) if empty
//
//go:nosplit
func uartDequeue() (byte, bool) {
	if uartRingBuf == nil {
		return 0, false // Not initialized
	}

	// Check if buffer is empty
	if uartRingBuf.head == uartRingBuf.tail {
		return 0, false // Buffer empty
	}

	// Get character from buffer
	c := uartRingBuf.buf[uartRingBuf.tail]
	uartRingBuf.tail = (uartRingBuf.tail + 1) % UART_RING_BUFFER_SIZE

	return c, true
}

// uartSpaceAvailable returns the number of free slots in the buffer
//
//go:nosplit
func uartSpaceAvailable() uint32 {
	if uartRingBuf == nil {
		return 0
	}

	if uartRingBuf.head >= uartRingBuf.tail {
		return UART_RING_BUFFER_SIZE - (uartRingBuf.head - uartRingBuf.tail) - 1
	} else {
		return uartRingBuf.tail - uartRingBuf.head - 1
	}
}

// uartIsNearFull returns true if buffer has exactly 3 or fewer slots remaining
//
//go:nosplit
func uartIsNearFull() bool {
	return uartSpaceAvailable() <= 3
}

// uartEnqueueOverflowMarker enqueues "***" marker and drops the triggering character
//
//go:nosplit
func uartEnqueueOverflowMarker(droppedChar byte) {
	// Enqueue "***" marker
	uartEnqueue('*')
	uartEnqueue('*')
	uartEnqueue('*')

	// droppedChar is already lost - no need to enqueue it
}

// uartEnqueueOrOverflow handles enqueue with overflow protection
// Returns true if character was enqueued, false if dropped due to overflow
//
//go:nosplit
func uartEnqueueOrOverflow(c byte) bool {
	if uartRingBuf == nil {
		// Fallback to direct UART output if ring buffer not initialized
		asm.UartPutcPl011(c)
		return true
	}

	// Check if we would be at or below 3 slots remaining after this enqueue
	spaceBefore := uartSpaceAvailable()
	if spaceBefore <= 3 {
		// This would put us at or below 3 slots remaining
		// Enqueue overflow marker and drop this character
		uartEnqueueOverflowMarker(c)
		return false // Character dropped
	}

	// Normal enqueue
	return uartEnqueue(c)
}

// ============================================================================
// UART Interrupt Handler for Interrupt-Driven Transmission
// ============================================================================

// handleUARTIRQ is called from assembly interrupt handler
// This is a minimal nosplit function that handles UART interrupts
//
//go:linkname handleUARTIRQ handleUARTIRQ
//go:nosplit
//go:noinline
func handleUARTIRQ() {
	// Check if UART TX FIFO has space (TXFF bit clear = FIFO not full)
	fr := asm.MmioRead(QEMU_UART_FR)
	if fr&(1<<5) != 0 {
		// TXFF bit set = FIFO is full, can't write
		// Clear interrupt and return
		asm.MmioWrite(QEMU_UART_ICR, 0x7FF)
		return
	}

	// UART is ready to write - try to dequeue a character from buffer
	c, ok := uartDequeue()
	if !ok {
		// Buffer is empty - disable TX interrupt to avoid unnecessary interrupts
		asm.MmioWrite(QEMU_UART_BASE+0x38, 0) // UART_IMSC - clear TXIM bit (5)
		// Clear interrupt and return
		asm.MmioWrite(QEMU_UART_ICR, 0x7FF)
		return
	}

	// Write character to UART
	asm.MmioWrite(QEMU_UART_DR, uint32(c))

	// Clear UART interrupt (write 0x7FF to ICR to clear all interrupts)
	asm.MmioWrite(QEMU_UART_ICR, 0x7FF)
}

// Note: The current logic is wrong. When spaceBefore == 3, we should allow the enqueue
// because it would leave 2 slots remaining, which is still > 3. Overflow should only
// trigger when spaceBefore <= 3. But let me check the test again...

// Actually, looking at the test, the buffer has exactly 3 slots remaining.
// When I call uartEnqueueOrOverflow('O'), spaceBefore = 3, so spaceBefore <= 3 is true,
// which triggers overflow. But that's wrong - we should allow enqueues that leave 3 or more slots.

// The correct logic should be: trigger overflow when spaceBefore <= 3.
// But that means when we have 3 slots, we can't enqueue anything else.
// That would mean the "near full" threshold is actually 4 slots remaining.

// Let me reconsider the requirement: "when the buffer reaches 3 slots before the ring buffer is full"
// This means when there are 3 or fewer slots remaining, we should add "***" and drop new characters.

// So the logic should be: if spaceBefore <= 3, then overflow.
