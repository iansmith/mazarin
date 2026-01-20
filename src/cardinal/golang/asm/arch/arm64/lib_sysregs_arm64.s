// lib_sysregs.s - System register access functions in Go/Plan9 assembly
//
// ============================================================================
// OVERVIEW
// ============================================================================
// This file contains atomic system register primitives that cannot be decomposed.
// Each function is a single-purpose operation accessing ARM64 system registers
// for MMU configuration, exception handling, and system control.
//
// Functions are organized by category:
//   - SCTLR_EL1: System control (MMU enable, alignment check)
//   - CurrentEL and Feature Registers: Exception level and processor features
//   - TPIDR Registers: Thread-local storage pointers
//   - Exception Syndrome/Link Registers: Exception handling state
//   - VBAR_EL1: Vector base address register
//   - Runtime Stack Configuration: Go runtime stack limits
//   - Exception Vector and IRQ Control: Interrupt management
//   - SPSR_EL1: Saved program status register
//
// ABI NOTES:
// - These are abi0 functions. Go generates wrappers to call them from internal ABI.
// - Arguments: The wrapper passes args in registers AND on stack. Use either.
// - Return values: Most store to ret+0(FP). The wrapper reads from stack, not R0.
// - Some functions return in R0 directly when used in same-package assembly.
//
// Migrated from asm/aarch64/lib.s

#include "textflag.h"

// ============================================================================
// SCTLR_EL1 - System Control Register
// ============================================================================

// ============================================================================
// read_sctlr_el1() - Read System Control Register
// ============================================================================
// Read SCTLR_EL1 (System Control Register for EL1)
// Key bits:
//   Bit 0 (M): MMU enable (1=enabled, 0=disabled)
//   Bit 1 (A): Alignment check enable
//   Bit 2 (C): Data cache enable
//   Bit 12 (I): Instruction cache enable
// Returns value in R0 (Go register ABI)
//
// Segments:
//   1. Read SCTLR_EL1 to R0
//   2. Return to caller
//
TEXT read_sctlr_el1(SB), NOSPLIT|NOFRAME, $0-8
	// Segment 1: Read SCTLR_EL1
	MRS	SCTLR_EL1, R0

	// Segment 2: Store return value and return
	// CRITICAL: Must store to ret+0(FP) for ABI0 compatibility
	MOVD	R0, ret+0(FP)
	RET

// ============================================================================
// write_sctlr_el1(value uint64) - Write System Control Register with MMU Enable
// ============================================================================
// Write SCTLR_EL1 with full MMU enable sequence
// Includes TLB and cache invalidation before/after MMU enable
// Argument arrives in R0 (Go register ABI)
//
// Segments:
//   1. Save link register
//   2. Pre-MMU barriers and TLB invalidation
//   3. Write SCTLR_EL1 to enable MMU
//   4. Post-MMU cache and TLB invalidation
//   5. Restore link register
//   6. Return to caller
//
TEXT write_sctlr_el1(SB), NOSPLIT, $16-8
	// Segment 1: Save LR
	// R0 already contains value from Go register ABI
	MOVD	LR, R9
	MOVD	R9, 8(RSP)

	// Segment 2: Pre-MMU barriers and TLB invalidation
	// Ensure all prior operations complete
	DSB	$15		// DSB SY
	ISB	$15
	// Invalidate TLB before enabling MMU
	// TLBI VMALLE1 - encoded as: 0xD508871F
	WORD	$0xD508871F
	DSB	$15
	ISB	$15

	// DEBUG: Write '<' to UART before MMU enable
	MOVD	$0x09000000, R1
	MOVD	$'<', R2
	MOVB	R2, (R1)

	// Segment 3: Write SCTLR_EL1
	MSR	R0, SCTLR_EL1

	// DEBUG: Write '=' after MSR, before ISB
	MOVD	$0x09000000, R1
	MOVD	$'=', R2
	MOVB	R2, (R1)

	ISB	$15

	// DEBUG: Write '>' to UART after ISB
	MOVD	$0x09000000, R1
	MOVD	$'>', R2
	MOVB	R2, (R1)

	// Segment 4: Post-MMU invalidation
	// Invalidate caches after MMU enable
	WORD	$0xD508751F	// IC IALLU
	// TLBI VMALLE1
	WORD	$0xD508871F
	DSB	$15
	ISB	$15

	// Segment 5: Restore LR
	MOVD	8(RSP), R9
	MOVD	R9, LR

	// Segment 6: Return
	RET

// ============================================================================
// enable_mmu_minimal(value uint64) - Minimal MMU Enable
// ============================================================================
// Minimal MMU enable function - just enable MMU and return
// No TLB/cache invalidation - caller must handle if needed
// Argument arrives in R0 (Go register ABI)
//
// Segments:
//   1. Write SCTLR_EL1
//   2. Issue instruction barrier
//   3. Return to caller
//
TEXT enable_mmu_minimal(SB), NOSPLIT|NOFRAME, $0-8
	// Segment 1: Write SCTLR_EL1
	// R0 already contains value from Go register ABI
	MSR	R0, SCTLR_EL1

	// Segment 2: Barrier
	ISB	$15

	// Segment 3: Return
	RET

// ============================================================================
// disable_alignment_check() - Disable Alignment Fault Checking
// ============================================================================
// Clear the A bit in SCTLR_EL1 to disable alignment faults
//
// Segments:
//   1. Read current SCTLR_EL1 value
//   2. Clear A bit (bit 1)
//   3. Write modified value back
//   4. Issue instruction barrier
//   5. Return to caller
//
TEXT disable_alignment_check(SB), NOSPLIT|NOFRAME, $0-0
	// Segment 1: Read SCTLR_EL1
	MRS	SCTLR_EL1, R0

	// Segment 2: Clear A bit
	AND	$~2, R0		// Clear bit 1 (A bit)

	// Segment 3: Write back
	MSR	R0, SCTLR_EL1

	// Segment 4: Barrier
	ISB	$15

	// Segment 5: Return
	RET

// ============================================================================
// CurrentEL and Feature Registers
// ============================================================================

// ============================================================================
// read_current_el() - Read Current Exception Level
// ============================================================================
// Read CurrentEL (Current Exception Level)
// Returns bits [3:2] containing the exception level
// Returns value in R0 (Go register ABI)
//
// Segments:
//   1. Read CurrentEL to R0
//   2. Return to caller
//
TEXT read_current_el(SB), NOSPLIT|NOFRAME, $0-4
	// Segment 1: Read CurrentEL
	MRS	CurrentEL, R0

	// Segment 2: Store return value and return
	MOVD	R0, ret+0(FP)
	RET

// ============================================================================
// read_id_aa64pfr0_el1() - Read Processor Feature Register
// ============================================================================
// Read ID_AA64PFR0_EL1 (Processor Feature Register)
// Bits [15:12] = EL3 support, Bits [7:4] = EL1 support, Bits [3:0] = EL0 support
// Returns value in R0 (Go register ABI)
//
// Segments:
//   1. Read ID_AA64PFR0_EL1 to R0
//   2. Return to caller
//
TEXT read_id_aa64pfr0_el1(SB), NOSPLIT|NOFRAME, $0-8
	// Segment 1: Read ID_AA64PFR0_EL1
	// MRS ID_AA64PFR0_EL1, X0 - encoded as: 0xD5380400
	WORD	$0xD5380400

	// Segment 2: Store return value and return
	MOVD	R0, ret+0(FP)
	RET

// ============================================================================
// read_scr_el3() - Read Secure Configuration Register
// ============================================================================
// Attempt to read SCR_EL3 (Secure Configuration Register)
// NOTE: This will cause a sync exception at EL1 - only callable from EL3
// Returns value in R0 (Go register ABI)
//
// Segments:
//   1. Read SCR_EL3 to R0 (will fault at EL1)
//   2. Return to caller (unreachable at EL1)
//
TEXT read_scr_el3(SB), NOSPLIT|NOFRAME, $0-8
	// Segment 1: Read SCR_EL3
	// MRS SCR_EL3, X0 - encoded as: 0xD53E1100
	WORD	$0xD53E1100

	// Segment 2: Store return value and return
	MOVD	R0, ret+0(FP)
	RET

// ============================================================================
// Cache Type Register
// ============================================================================

// ============================================================================
// read_ctr_el0() - Read Cache Type Register
// ============================================================================
// Read Cache Type Register
// Bit 28 (IDC): Data cache clean NOT required for I/D coherency if 1
// Bit 29 (DIC): Instruction cache invalidation NOT required for I/D coherency if 1
// Returns value in R0 (Go register ABI)
//
// Segments:
//   1. Read CTR_EL0 to R0
//   2. Return to caller
//
TEXT read_ctr_el0(SB), NOSPLIT|NOFRAME, $0-8
	// Segment 1: Read CTR_EL0
	MRS	CTR_EL0, R0

	// Segment 2: Store return value and return
	MOVD	R0, ret+0(FP)
	RET

// ============================================================================
// TPIDR Registers (Thread Pointer ID)
// ============================================================================

// ============================================================================
// read_tpidr_el0() - Read Thread Pointer ID Register (User)
// ============================================================================
// Read TPIDR_EL0 (Thread Pointer ID Register, User)
// Used by Go runtime for thread-local storage (TLS)
// Returns value in R0 (Go register ABI)
//
// Segments:
//   1. Read TPIDR_EL0 to R0
//   2. Return to caller
//
TEXT read_tpidr_el0(SB), NOSPLIT|NOFRAME, $0-8
	// Segment 1: Read TPIDR_EL0
	MRS	TPIDR_EL0, R0

	// Segment 2: Store return value and return
	MOVD	R0, ret+0(FP)
	RET

// ============================================================================
// write_tpidr_el0(value uint64) - Write Thread Pointer ID Register (User)
// ============================================================================
// Write TPIDR_EL0 (Thread Pointer ID Register, User)
// Argument arrives in R0 (Go register ABI)
//
// Segments:
//   1. Write R0 to TPIDR_EL0
//   2. Issue instruction barrier
//   3. Return to caller
//
TEXT write_tpidr_el0(SB), NOSPLIT|NOFRAME, $0-8
	// Segment 1: Write to TPIDR_EL0
	// R0 already contains value from Go register ABI
	MSR	R0, TPIDR_EL0

	// Segment 2: Barrier
	ISB	$15

	// Segment 3: Return
	RET

// ============================================================================
// read_tpidr_el1() - Read Thread Pointer ID Register (Kernel)
// ============================================================================
// Read TPIDR_EL1 (Thread Pointer ID Register, Kernel)
// Returns value in R0 (Go register ABI)
//
// Segments:
//   1. Read TPIDR_EL1 to R0
//   2. Return to caller
//
TEXT read_tpidr_el1(SB), NOSPLIT|NOFRAME, $0-8
	// Segment 1: Read TPIDR_EL1
	MRS	TPIDR_EL1, R0

	// Segment 2: Store return value and return
	MOVD	R0, ret+0(FP)
	RET

// ============================================================================
// write_tpidr_el1(value uint64) - Write Thread Pointer ID Register (Kernel)
// ============================================================================
// Write TPIDR_EL1 (Thread Pointer ID Register, Kernel)
// Argument arrives in R0 (Go register ABI)
//
// Segments:
//   1. Write R0 to TPIDR_EL1
//   2. Issue instruction barrier
//   3. Return to caller
//
TEXT write_tpidr_el1(SB), NOSPLIT|NOFRAME, $0-8
	// Segment 1: Write to TPIDR_EL1
	// R0 already contains value from Go register ABI
	MSR	R0, TPIDR_EL1

	// Segment 2: Barrier
	ISB	$15

	// Segment 3: Return
	RET

// ============================================================================
// ESR_EL1 and ELR_EL1 - Exception Syndrome and Link Registers
// ============================================================================

// ============================================================================
// read_esr_el1() - Read Exception Syndrome Register
// ============================================================================
// Read ESR_EL1 (Exception Syndrome Register for EL1)
// Contains information about the exception (type, fault status, etc.)
// Returns value in R0 (Go register ABI)
//
// Segments:
//   1. Read ESR_EL1 to R0
//   2. Return to caller
//
TEXT read_esr_el1(SB), NOSPLIT|NOFRAME, $0-8
	// Segment 1: Read ESR_EL1
	MRS	ESR_EL1, R0

	// Segment 2: Store return value and return
	MOVD	R0, ret+0(FP)
	RET

// ============================================================================
// read_elr_el1() - Read Exception Link Register
// ============================================================================
// Read ELR_EL1 (Exception Link Register for EL1)
// Contains the address to return to after exception handling
// Returns value in R0 (Go register ABI)
//
// Segments:
//   1. Read ELR_EL1 to R0
//   2. Return to caller
//
TEXT read_elr_el1(SB), NOSPLIT|NOFRAME, $0-8
	// Segment 1: Read ELR_EL1
	MRS	ELR_EL1, R0

	// Segment 2: Store return value and return
	MOVD	R0, ret+0(FP)
	RET

// ============================================================================
// write_elr_el1(value uint64) - Write Exception Link Register
// ============================================================================
// Write to ELR_EL1 (Exception Link Register for EL1)
// Sets the address to return to after exception handling
// Argument arrives in R0 (Go register ABI)
//
// Segments:
//   1. Write R0 to ELR_EL1
//   2. Return to caller
//
TEXT write_elr_el1(SB), NOSPLIT|NOFRAME, $0-8
	// Segment 1: Write to ELR_EL1
	// R0 already contains value from Go register ABI
	MSR	R0, ELR_EL1

	// Segment 2: Return
	RET

// ============================================================================
// read_far_el1() - Read Fault Address Register
// ============================================================================
// Read FAR_EL1 (Fault Address Register for EL1)
// Contains the virtual address that caused a memory access fault
// Returns value in R0 (Go register ABI)
//
// Segments:
//   1. Read FAR_EL1 to R0
//   2. Return to caller
//
TEXT read_far_el1(SB), NOSPLIT|NOFRAME, $0-8
	// Segment 1: Read FAR_EL1
	MRS	FAR_EL1, R0

	// Segment 2: Store return value and return
	MOVD	R0, ret+0(FP)
	RET

// ============================================================================
// read_daif() - Read Interrupt Mask Register
// ============================================================================
// Read DAIF register (interrupt mask bits)
// Bits: D=Debug, A=SError, I=IRQ, F=FIQ mask flags
// Returns value in R0 (Go register ABI)
//
// Segments:
//   1. Read DAIF to R0
//   2. Return to caller
//
TEXT read_daif(SB), NOSPLIT|NOFRAME, $0-8
	// Segment 1: Read DAIF
	MRS	DAIF, R0

	// Segment 2: Store return value and return
	MOVD	R0, ret+0(FP)
	RET

// ============================================================================
// VBAR_EL1 - Vector Base Address Register
// ============================================================================

// ============================================================================
// set_vbar_el1_to_addr(addr uintptr) - Set VBAR_EL1 without Barriers
// ============================================================================
// Set VBAR_EL1 to specific address
// NOTE: Caller must execute DSB + ISB after this returns
// Argument arrives in R0 (Go register ABI)
//
// Segments:
//   1. Write R0 to VBAR_EL1
//   2. Return to caller
//
TEXT set_vbar_el1_to_addr(SB), NOSPLIT|NOFRAME, $0-8
	// Segment 1: Write to VBAR_EL1
	// R0 already contains addr from Go register ABI
	MSR	R0, VBAR_EL1

	// Segment 2: Return
	RET

// ============================================================================
// Runtime Stack Size Configuration
// ============================================================================

// ============================================================================
// set_maxstacksize(size uintptr) - Set Runtime Maximum Stack Size
// ============================================================================
// Set runtime.maxstacksize
// Argument arrives in R0 (Go register ABI)
//
// Segments:
//   1. Load address of runtime.maxstacksize
//   2. Store size to runtime.maxstacksize
//   3. Return to caller
//
TEXT set_maxstacksize(SB), NOSPLIT|NOFRAME, $0-8
	// Segment 1: Load address
	// R0 already contains size from Go register ABI
	MOVD	$runtime·maxstacksize(SB), R1

	// Segment 2: Store size
	MOVD	R0, (R1)

	// Segment 3: Return
	RET

// ============================================================================
// set_maxstackceiling(size uintptr) - Set Runtime Maximum Stack Ceiling
// ============================================================================
// Set runtime.maxstackceiling
// Argument arrives in R0 (Go register ABI)
//
// Segments:
//   1. Load address of runtime.maxstackceiling
//   2. Store size to runtime.maxstackceiling
//   3. Return to caller
//
TEXT set_maxstackceiling(SB), NOSPLIT|NOFRAME, $0-8
	// Segment 1: Load address
	// R0 already contains size from Go register ABI
	MOVD	$runtime·maxstackceiling(SB), R1

	// Segment 2: Store size
	MOVD	R0, (R1)

	// Segment 3: Return
	RET

// ============================================================================
// Exception Vector and IRQ Control (migrated from exceptions.s)
// ============================================================================

// ============================================================================
// set_vbar_el1(addr uintptr) - Set VBAR_EL1 with Barriers
// ============================================================================
// Set VBAR_EL1 with proper barriers
// This is the full version with DSB/ISB barriers
// Argument arrives in R0 (Go register ABI)
//
// Segments:
//   1. Issue data barrier before write
//   2. Write R0 to VBAR_EL1
//   3. Issue instruction barrier after write
//   4. Return to caller
//
TEXT set_vbar_el1(SB), NOSPLIT|NOFRAME, $0-8
	// Segment 1: Pre-write barrier
	// R0 already contains addr from Go register ABI
	// Data synchronization barrier to ensure all previous memory accesses complete
	DSB	$15		// DSB SY

	// Segment 2: Write to VBAR_EL1
	MSR	R0, VBAR_EL1

	// Segment 3: Post-write barrier
	// Instruction synchronization barrier to ensure VBAR_EL1 is set
	// before any subsequent instructions execute
	ISB	$15

	// Segment 4: Return
	RET

// ============================================================================
// read_vbar_el1() - Read Vector Base Address Register
// ============================================================================
// Read VBAR_EL1 to verify it was set correctly
// Returns value in R0 (Go register ABI)
//
// Segments:
//   1. Read VBAR_EL1 to R0
//   2. Store to stack return location
//   3. Return to caller
//
TEXT read_vbar_el1(SB), NOSPLIT|NOFRAME, $0-8
	// Segment 1: Read VBAR_EL1
	MRS	VBAR_EL1, R0

	// Segment 2: Store to stack
	MOVD	R0, ret+0(FP)

	// Segment 3: Return
	RET

// ============================================================================
// get_exception_vectors_addr() - Get Exception Vector Table Address
// ============================================================================
// Returns the address of the new Go/Plan9 vector table
// Points to vec_sync_sp_el0 which is the first entry
// CRITICAL: Must store return value at ret+0(FP) for ABI0 compatibility
// The Go compiler generates an ABI wrapper that loads from [SP+8]
//
// Segments:
//   1. Load address of vector table into R0
//   2. Store to stack return location
//   3. Return to caller
//
TEXT get_exception_vectors_addr(SB), NOSPLIT|NOFRAME, $0-8
	// Segment 1: Load vector table address
	MOVD	$vec_sync_sp_el0(SB), R0

	// Segment 2: Store to stack
	// Store to return value slot (required for ABI0)
	MOVD	R0, ret+0(FP)

	// Segment 3: Return
	RET

// ============================================================================
// enable_irqs() - Enable IRQ Interrupts with Barriers
// ============================================================================
// Clears the I bit in PSTATE to enable IRQ interrupts
// DAIF bits: Bit 1 = I (IRQ), so #2 clears IRQ mask
//
// CRITICAL: This uses DAIFCLR to CLEAR the I bit, which ENABLES interrupts
// MSR DAIFCLR, #2 = 0xD50342FF
//
// Segments:
//   1. Issue data barrier before enabling
//   2. Clear I bit to enable IRQs (DAIFCLR)
//   3. Issue instruction barrier after enabling
//   4. Return to caller
//
TEXT enable_irqs(SB), NOSPLIT|NOFRAME, $0-0
	// Segment 1: Pre-enable barrier
	// Data barrier to ensure all previous operations complete
	DSB	$15		// DSB SY

	// Segment 2: Enable IRQs
	// Clear I bit (bit 1) to enable IRQ interrupts
	// MSR DAIFCLR, #2 - encoded as: 0xD50342FF
	WORD	$0xD50342FF

	// Segment 3: Post-enable barrier
	// Instruction barrier to ensure interrupt enable is visible
	ISB	$15

	// Segment 4: Return
	RET

// ============================================================================
// enable_irqs_asm() - Enable IRQ Interrupts without Barriers
// ============================================================================
// Minimal version to enable interrupts - just the MSR instruction
// No barriers, for use when barriers aren't needed
//
// Segments:
//   1. Clear I bit to enable IRQs (DAIFCLR)
//   2. Return to caller
//
TEXT enable_irqs_asm(SB), NOSPLIT|NOFRAME, $0-0
	// Segment 1: Enable IRQs
	// MSR DAIFCLR, #2 - Clear I bit (bit 1) = enable IRQs
	WORD	$0xD50342FF

	// Segment 2: Return
	RET

// ============================================================================
// disable_irqs() - Disable IRQ Interrupts
// ============================================================================
// Sets the I bit in PSTATE to disable IRQ interrupts
// DAIF bits: Bit 1 = I (IRQ), so #2 sets IRQ mask
//
// CRITICAL: This uses DAIFSET to SET the I bit, which DISABLES interrupts
// MSR DAIFSET, #2 = 0xD50342DF
//
// Segments:
//   1. Set I bit to disable IRQs (DAIFSET)
//   2. Issue instruction barrier
//   3. Return to caller
//
TEXT disable_irqs(SB), NOSPLIT|NOFRAME, $0-0
	// Segment 1: Disable IRQs
	// MSR DAIFSET, #2 - Set I bit (bit 1) = disable IRQs
	WORD	$0xD50342DF

	// Segment 2: Barrier
	ISB	$15

	// Segment 3: Return
	RET

// ============================================================================
// SPSR_EL1 - Saved Program Status Register
// ============================================================================

// ============================================================================
// read_spsr_el1() - Read Saved Program Status Register
// ============================================================================
// Read the Saved Program Status Register
// Returns value in R0 (Go register ABI)
//
// Segments:
//   1. Read SPSR_EL1 to R0
//   2. Return to caller
//
TEXT read_spsr_el1(SB), NOSPLIT|NOFRAME, $0-8
	// Segment 1: Read SPSR_EL1
	MRS	SPSR_EL1, R0

	// Segment 2: Store return value and return
	MOVD	R0, ret+0(FP)
	RET

// ============================================================================
// write_spsr_el1(value uint64) - Write Saved Program Status Register
// ============================================================================
// Write to SPSR_EL1
// Argument arrives in R0 (Go register ABI)
//
// Segments:
//   1. Write R0 to SPSR_EL1
//   2. Return to caller
//
TEXT write_spsr_el1(SB), NOSPLIT|NOFRAME, $0-8
	// Segment 1: Write to SPSR_EL1
	// R0 already contains value from Go register ABI
	MSR	R0, SPSR_EL1

	// Segment 2: Return
	RET
