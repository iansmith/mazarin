//go:build qemuvirt && aarch64

package main

import (
	"cardinal/asm"
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
}

// uartPutc outputs a character via UART (QEMU virt machine)
// Writes directly to UART hardware (polling mode)
// Auto-converts LF to CRLF for proper terminal display
//
//go:nosplit
func uartPutc(c byte) {
	// Auto-convert LF to CRLF for consistent terminal display
	if c == '\n' {
		asm.UartPutcPl011('\r')
	}
	asm.UartPutcPl011(c)
}

// SyscallWriteBuffer writes a buffer directly to UART hardware (polling mode)
// Called from assembly syscall handler for stdout/stderr writes
// Returns the number of bytes written (count on success)
//
//go:nosplit
//go:noinline
func SyscallWriteBuffer(buf unsafe.Pointer, count uint32) uint32 {
	if buf == nil || count == 0 {
		return 0
	}

	// Write each byte directly via uartPutc (polling mode)
	for i := uint32(0); i < count; i++ {
		c := *(*byte)(unsafe.Pointer(uintptr(buf) + uintptr(i)))
		uartPutc(c)
	}

	return count
}

// SyscallWriteDirect writes a buffer directly to UART hardware (no ring buffer)
// Used for writes from kmazarin where ring buffer/interrupt mechanism isn't reliable
// Returns the number of bytes written (count on success)
//
//go:nosplit
//go:noinline
func SyscallWriteDirect(buf unsafe.Pointer, count uint32) uint32 {
	if buf == nil || count == 0 {
		return 0
	}

	// Write each byte directly to UART hardware
	for i := uint32(0); i < count; i++ {
		c := *(*byte)(unsafe.Pointer(uintptr(buf) + uintptr(i)))

		// Auto-convert LF to CRLF for proper terminal display
		if c == '\n' {
			asm.UartPutcPl011('\r')
		}

		asm.UartPutcPl011(c)
	}

	return count
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


// Let me reconsider the requirement: "when the buffer reaches 3 slots before the ring buffer is full"
// This means when there are 3 or fewer slots remaining, we should add "***" and drop new characters.

// So the logic should be: if spaceBefore <= 3, then overflow.
