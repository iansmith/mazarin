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
	MOVD	main·LinkerUartBase(SB), R1	// UART base
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
TEXT irq_exception_handler_el0(SB), NOSPLIT, $0
	B	irq_exception_el1(SB)
