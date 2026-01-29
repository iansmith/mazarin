//go:build !test_stubs


#include "textflag.h"

// Exit terminates the system via ARM64 semihosting.
// Before exiting, disables the timer, waits for UART TX FIFO to drain,
// and uses WFI to yield to QEMU's event loop so it can flush its serial
// file output buffer. If semihosting is not available, deadloops.
//
TEXT ·Exit(SB), NOSPLIT, $16-0
	// Disable all IRQs so WFI actually halts the CPU
	MSR	$3, DAIFSet

	// Disable the ARM timer hardware (CNTV_CTL_EL0 = 0)
	// This prevents timer interrupts from waking WFI immediately
	MOVD	ZR, R0
	MSR	R0, CNTV_CTL_EL0

	// Wait for UART TX FIFO to empty.
	// PL011 UART Flag Register (FR) is at UART_BASE + 0x18
	// Bit 7 (TXFE) = 1 means TX FIFO is empty
	MOVD	$0xFFFFFFFF09000000, R2		// UART base (high-memory mapped)
	ADD	$0x18, R2, R3			// R3 = UART_FR address

exit_txfe_wait:
	MOVW	(R3), R4			// Read Flag Register
	AND	$0x80, R4, R4			// Isolate TXFE bit (bit 7)
	CBZ	R4, exit_txfe_wait		// Loop while TXFE=0 (FIFO not empty)

	// Wait for UART not busy
exit_busy_wait:
	MOVW	(R3), R4			// Read Flag Register
	AND	$8, R4, R4			// Isolate BUSY bit (bit 3)
	CBNZ	R4, exit_busy_wait		// Loop while BUSY=1

	// Write a few newlines to flush any line-buffered output
	MOVW	$'\n', R4
	MOVW	R4, (R2)
	MOVW	R4, (R2)
	MOVW	R4, (R2)

	// Wait for TX FIFO empty again after newlines
exit_txfe_wait2:
	MOVW	(R3), R4
	AND	$0x80, R4, R4
	CBZ	R4, exit_txfe_wait2

exit_busy_wait2:
	MOVW	(R3), R4
	AND	$8, R4, R4
	CBNZ	R4, exit_busy_wait2

	// Memory barrier to ensure all writes are visible
	DSB	$15

	// Re-enable IRQs briefly so WFI can be woken by spurious events,
	// then use WFI to yield to QEMU's event loop which handles file I/O.
	// With timer disabled and IRQs masked, WFI halts the vCPU,
	// giving QEMU's main event loop time to flush serial output to disk.
	MSR	$2, DAIFClr			// Enable IRQs
	MOVD	$50, R5
exit_wfi_loop:
	WFI
	SUB	$1, R5, R5
	CBNZ	R5, exit_wfi_loop

	// Mask IRQs again before semihosting exit
	MSR	$3, DAIFSet

	// ARM64 semihosting SYS_EXIT (0x18)
	// Parameter block at SP: [0]=reason, [8]=status
	MOVD	$0x20026, R1		// ADP_Stopped_ApplicationExit
	MOVD	R1, (RSP)
	MOVD	ZR, 8(RSP)		// status = 0 (success)
	MOVD	RSP, R1			// R1 = pointer to parameter block
	MOVW	$0x18, R0		// R0 = SYS_EXIT
	WORD	$0xD45E0000		// HLT #0xF000 (semihosting trap)
	// If semihosting not present, fall through to deadloop
halt:
	B	halt
