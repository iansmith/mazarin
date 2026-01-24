// exc_handlers_arm64.s - Exception Handler Entry Points
//
// ============================================================================
// OVERVIEW
// ============================================================================
// Provides exception handler entry points that the vector table branches to.
// These are minimal stub functions that redirect to the main exception handlers.
//
// Functions:
//   - exc_hang() - Infinite loop for unimplemented exceptions (prints "HANG")
//   - sync_exception_handler_el0() - Redirect EL0 sync to main handler
//   - irq_exception_handler_el0() - Redirect EL0 IRQ to EL1 handler
//
// EXCEPTION MODE SWITCHING:
// When running in EL1t mode (using SP_EL0), exceptions automatically switch
// to EL1h mode (using SP_EL1) for the handler. This means EL0 handlers can
// simply redirect to the EL1 handlers.
//
// WHY NOT DECOMPOSE:
// These are already minimal stub functions (1-3 instructions each):
//   - exc_hang: Prints "HANG\n" and loops (debugging aid)
//   - EL0 redirects: Single branch instruction to actual handler
// They cannot be decomposed further as they perform atomic redirection.
//
// The actual exception handling logic is in:
//   - exc_sync_entry_arm64.s (sync_exception_handler)
//   - exc_irq_arm64.s (irq_exception_el1)

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
