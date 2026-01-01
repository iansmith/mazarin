// exc_handlers.s - Exception Handler Entry Points
//
// This file provides the exception handler entry points that the vector
// table branches to. These handlers are small stubs that redirect to the
// main exception handling code.
//
// Note: The actual exception handling logic (sync_exception_handler,
// irq_handler_entry) are more complex and will be migrated separately.

#include "textflag.h"

// ============================================================================
// Hang handler for unimplemented exceptions
// ============================================================================

// exc_hang - Infinite loop for unhandled exceptions
// This uses WFE to reduce power consumption while waiting
TEXT exc_hang(SB), NOSPLIT, $0
	MOVD	$0x09000000, R1		// UART base
	MOVD	$'H', R0
	MOVW	R0, (R1)		// Print 'H' for hang
	MOVD	$'A', R0
	MOVW	R0, (R1)		// Print 'A'
	MOVD	$'N', R0
	MOVW	R0, (R1)		// Print 'N'
	MOVD	$'G', R0
	MOVW	R0, (R1)		// Print 'G'
	MOVD	$'\n', R0
	MOVW	R0, (R1)		// Newline
hang_loop:
	WFE				// Wait for event (low power)
	B	hang_loop

// ============================================================================
// EL0 mode handler redirects
// When running in EL1t mode (using SP_EL0), exceptions automatically switch
// to EL1h mode (using SP_EL1) for the handler. This means the handlers can
// be identical to the EL1h handlers.
// ============================================================================

// sync_exception_handler_el0 - Redirect to main sync handler
TEXT sync_exception_handler_el0(SB), NOSPLIT, $0
	B	sync_exception_handler(SB)

// irq_exception_handler_el0 - Redirect to main IRQ handler
// Print '@' to show EL0 IRQ path taken (for debugging)
TEXT irq_exception_handler_el0(SB), NOSPLIT, $0
	// Save R0, R1 for debug print
	SUB	$16, RSP
	MOVD	R0, (RSP)
	MOVD	R1, 8(RSP)

	// Print '@' to show EL0 IRQ path
	MOVD	$0x09000000, R1
	MOVD	$'@', R0
	MOVW	R0, (R1)

	// Restore R0, R1
	MOVD	(RSP), R0
	MOVD	8(RSP), R1
	ADD	$16, RSP

	// Jump to main IRQ handler (irq_exception_el1 in exceptions.s)
	B	irq_exception_el1(SB)
