// exc_vectors.s - ARM64 Exception Vector Table
//
// This file defines the exception vector table for ARM64 EL1.
// The table must be 2KB aligned and contains 16 entries of 128 bytes each.
//
// Layout (offsets from VBAR_EL1):
//   Group 0 (Current EL, SP_EL0):    0x000-0x1FF
//   Group 1 (Current EL, SP_EL1):    0x200-0x3FF
//   Group 2 (Lower EL, AArch64):     0x400-0x5FF
//   Group 3 (Lower EL, AArch32):     0x600-0x7FF
//
// Each group has 4 entries (Sync, IRQ, FIQ, SError) at 128-byte intervals.
//
// Build with: goasm2gnu -elf -section .vectors -align 11 -pad-to 128 -check-size 128
//
// The -pad-to 128 ensures each TEXT block is exactly 128 bytes with NOP padding.
// The -align 11 ensures the section has 2KB (2^11) alignment.
// The -check-size 128 errors if any handler exceeds 128 bytes before padding.

#include "textflag.h"

// ============================================================================
// Group 0: Current EL, using SP_EL0 (0x000-0x1FF)
// Used when running code at EL1 in EL1t mode (SPSel=0)
// This is used for kmazarin execution
// ============================================================================

// 0x000: Synchronous exception (SP_EL0)
// CRITICAL: Vector table must be 2KB aligned for VBAR_EL1
// PCALIGN $2048 ensures this function (and thus the vector table base) is 2KB aligned
TEXT vec_sync_sp_el0(SB), NOSPLIT|NOFRAME, $0
	PCALIGN	$2048
	B	sync_exception_handler_el0(SB)

// 0x080: IRQ (SP_EL0)
TEXT vec_irq_sp_el0(SB), NOSPLIT, $0
	B	irq_exception_handler_el0(SB)

// 0x100: FIQ (SP_EL0) - not used
TEXT vec_fiq_sp_el0(SB), NOSPLIT, $0
	B	exc_hang(SB)

// 0x180: SError (SP_EL0) - not used
TEXT vec_serror_sp_el0(SB), NOSPLIT, $0
	B	exc_hang(SB)

// ============================================================================
// Group 1: Current EL, using SP_EL1 (0x200-0x3FF)
// Used when running code at EL1 in EL1h mode (SPSel=1)
// This is the kernel's primary exception handling mode
// ============================================================================

// 0x200: Synchronous exception (SP_EL1)
TEXT vec_sync_sp_el1(SB), NOSPLIT, $0
	B	sync_exception_handler(SB)

// 0x280: IRQ (SP_EL1)
// Note: irq_exception_el1 is the existing inlined IRQ handler in exceptions.s
// It will be refactored to a proper function later
TEXT vec_irq_sp_el1(SB), NOSPLIT, $0
	B	irq_exception_el1(SB)

// 0x300: FIQ (SP_EL1) - not used
TEXT vec_fiq_sp_el1(SB), NOSPLIT, $0
	B	exc_hang(SB)

// 0x380: SError (SP_EL1) - not used
TEXT vec_serror_sp_el1(SB), NOSPLIT, $0
	B	exc_hang(SB)

// ============================================================================
// Group 2: Lower EL, AArch64 (0x400-0x5FF)
// Used for exceptions from EL0 running AArch64 code
// Not yet implemented - will be needed for user-space processes
// ============================================================================

// 0x400: Synchronous exception (Lower EL, AArch64)
TEXT vec_sync_lower_a64(SB), NOSPLIT, $0
	B	exc_hang(SB)

// 0x480: IRQ (Lower EL, AArch64)
TEXT vec_irq_lower_a64(SB), NOSPLIT, $0
	B	exc_hang(SB)

// 0x500: FIQ (Lower EL, AArch64)
TEXT vec_fiq_lower_a64(SB), NOSPLIT, $0
	B	exc_hang(SB)

// 0x580: SError (Lower EL, AArch64)
TEXT vec_serror_lower_a64(SB), NOSPLIT, $0
	B	exc_hang(SB)

// ============================================================================
// Group 3: Lower EL, AArch32 (0x600-0x7FF)
// Used for exceptions from EL0 running AArch32 code
// Not supported - mazzy only supports AArch64
// ============================================================================

// 0x600: Synchronous exception (Lower EL, AArch32)
TEXT vec_sync_lower_a32(SB), NOSPLIT, $0
	B	exc_hang(SB)

// 0x680: IRQ (Lower EL, AArch32)
TEXT vec_irq_lower_a32(SB), NOSPLIT, $0
	B	exc_hang(SB)

// 0x700: FIQ (Lower EL, AArch32)
TEXT vec_fiq_lower_a32(SB), NOSPLIT, $0
	B	exc_hang(SB)

// 0x780: SError (Lower EL, AArch32)
TEXT vec_serror_lower_a32(SB), NOSPLIT, $0
	B	exc_hang(SB)

// NOTE: exc_hang is defined in exc_handlers.s (not in this file)
// This file ONLY contains the 16 vector table entries
