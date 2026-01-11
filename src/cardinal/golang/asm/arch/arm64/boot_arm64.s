// boot_arm64.s - ARM64 Boot Code in Go/Plan9 Assembly
//
// This is the entry point for bare-metal execution on ARM64.
// QEMU loads this code and jumps here at reset.
//
// This code runs BEFORE the Go runtime is initialized, so:
// - Cannot use any Go runtime features
// - Cannot allocate memory
// - Cannot call Go functions (except via raw BL)
// - Must use NOSPLIT|NOFRAME to prevent stack frame manipulation
//
// Responsibilities:
// 1. Initialize UART for early debugging
// 2. Preserve DTB pointer from QEMU
// 3. Check CPU ID (only run on CPU 0)
// 4. Drop from EL2 to EL1
// 5. Initialize both stacks (SP_EL0 and SP_EL1)
// 6. Enable SIMD/FPU
// 7. Disable alignment checking
// 8. Clear BSS section
// 9. Initialize runtime structures (g0, m0, write barrier)
// 10. Set up exception vectors and GIC
// 11. Jump to KernelMain

#include "textflag.h"

// ============================================================================
// Instruction Encodings for Go Assembler
// ============================================================================
// Go's assembler doesn't support all ARM64 instructions directly.
// We encode unsupported instructions as WORD directives.

// System register encodings (MSR Xn, <reg> format - writes Xn to sysreg)
// Note: For MSR, the register number is in bits [4:0]
#define MSR_HCR_EL2_X0      WORD $0xD51C1100   // msr hcr_el2, x0
#define MSR_CNTHCTL_EL2_X0  WORD $0xD51CE100   // msr cnthctl_el2, x0
#define MSR_CNTVOFF_EL2_X0  WORD $0xD51CE060   // msr cntvoff_el2, x0
#define MSR_SPSR_EL2_X0     WORD $0xD51C4000   // msr spsr_el2, x0
#define MSR_ELR_EL2_X0      WORD $0xD51C4020   // msr elr_el2, x0

// MRS encodings (MRS Xn, <reg> format - reads sysreg to Xn)
#define MRS_MPIDR_EL1_X1    WORD $0xD53800A1   // mrs x1, mpidr_el1
#define MRS_CURRENTEL_X0    WORD $0xD5384240   // mrs x0, CurrentEL
#define MRS_ESR_EL2_X1      WORD $0xD53C5201   // mrs x1, esr_el2

// MSR encodings for setting system registers
#define MSR_VBAR_EL2_X0     WORD $0xD51CC000   // msr vbar_el2, x0
#define MSR_SP_EL1_X0       WORD $0xD51C4100   // msr sp_el1, x0

// Other instruction encodings
#define ERET_INSN           WORD $0xD69F03E0   // eret
#define HVC_1               WORD $0xD4000022   // hvc #1

// ============================================================================
// Boot Entry Point
// ============================================================================

// _cardinal_boot is the bare-metal boot entry point.
// After building, use objcopy or a post-processor to set the ELF entry point
// to this symbol's address.
//
// For bare-metal, QEMU passes DTB pointer in R0 at reset.
//
// On entry:
//   R0 = DTB pointer (from QEMU)
//   All other registers undefined
//   Exception level: EL2 (with virtualization=off) or EL1
//
TEXT _cardinal_boot(SB), NOSPLIT|NOFRAME, $0
	// DEBUG: Earliest possible breadcrumb - hardcoded UART address
	// QEMU virt PL011 UART is at 0x09000000
	// Wait for TX FIFO not full (bit 5 = TXFF) before writing
	MOVD	$0x09000000, R1
	ADD	$0x18, R1, R3		// R3 = Flag Register address
boot_txff_wait:
	MOVW	(R3), R2
	AND	$0x20, R2, R2		// Isolate TXFF bit (bit 5)
	CBNZ	R2, boot_txff_wait	// Loop while FIFO full

	MOVD	$'!', R0
	MOVW	R0, (R1)

	// Initialize UART base address for early breadcrumb debugging
	// R14 = UART PL011 base (from LinkerUartBase)
	// NOTE: R14 is used for UART breadcrumbs throughout boot. It remains valid
	// until the BL to KernelMain, since there are no other BL instructions
	// that would clobber it (R14/X14 is caller-saved in AAPCS64).
	MOVD	main·LinkerUartBase(SB), R14

	// Preserve QEMU-provided DTB pointer.
	// On QEMU virt, R0 contains the DTB physical address at reset.
	// We'll carry it through early init and pass it to KernelMain.
	MOVD	R0, R22

	// Get CPU ID - only run on CPU 0
	// mrs x1, mpidr_el1
	MRS_MPIDR_EL1_X1
	// Extract Aff0 (bits 0-7) - equivalent to ubfx x1, x1, #0, #8
	AND	$0xFF, R1, R1
	CBNZ	R1, cpu_halt

	// CPU 0 continues here

	// ========================================
	// Drop from EL2 to EL1 if necessary
	// QEMU virt with virtualization=off starts at EL2
	// We need to be at EL1 for proper OS operation
	// ========================================

	// mrs x0, CurrentEL
	MRS_CURRENTEL_X0
	// Extract EL bits [3:2]
	LSR	$2, R0, R0

	// DEBUG: Print 'E' followed by the EL value (should be '2' for EL2)
	MOVD	$0x09000000, R10
	MOVD	$'E', R5
	MOVB	R5, (R10)
	// Print EL number as digit '0', '1', '2', or '3'
	ADD	$'0', R0, R5
	MOVB	R5, (R10)

	CMP	$2, R0
	BNE	at_el1

	// We're at EL2, need to drop to EL1
	// Print '2' to show we're at EL2
	MOVD	$0x09000000, R10
	MOVD	$'2', R5
	MOVB	R5, (R10)

	// DEBUG: Print 'a' before HCR_EL2
	MOVD	$'a', R5
	MOVB	R5, (R10)

	// Configure HCR_EL2 (Hypervisor Configuration Register)
	// RW (bit 31) = 1: EL1 uses AArch64
	MOVD	$(1<<31), R0
	MSR_HCR_EL2_X0

	// DEBUG: Print 'b' after HCR_EL2
	MOVD	$'b', R5
	MOVB	R5, (R10)

	// Configure CNTHCTL_EL2 to allow EL1/EL0 access to timers
	// EL1PCTEN (bit 0) = 1: Don't trap CNTPCT_EL0 reads from EL1
	// EL1PCEN (bit 1) = 1: Don't trap CNTP_* accesses from EL1
	MOVD	$3, R0
	MSR_CNTHCTL_EL2_X0

	// DEBUG: Print 'c' after CNTHCTL_EL2
	MOVD	$'c', R5
	MOVB	R5, (R10)

	// Set virtual timer offset to 0 (CNTVOFF_EL2)
	MOVD	$0, R0
	MSR_CNTVOFF_EL2_X0

	// DEBUG: Print 'd' after CNTVOFF_EL2
	MOVD	$'d', R5
	MOVB	R5, (R10)

	// Set up EL2 exception vectors BEFORE dropping to EL1
	// This allows us to call back to EL2 later to modify SP_EL1

	// DEBUG: Print # BEFORE VBAR_EL2
	MOVD	$0x09000000, R10
	MOVD	$'#', R5
	MOVB	R5, (R10)

	MOVD	$el2_exception_vectors(SB), R0
	MSR_VBAR_EL2_X0

	// DEBUG: Print @ AFTER VBAR_EL2
	MOVD	$0x09000000, R10
	MOVD	$'@', R5
	MOVB	R5, (R10)

	// ========================================
	// CRITICAL: Set SP_EL1 to high-memory exception stack NOW (while at EL2)
	// After ERET to EL1, we can't use HVC to set it (virtualization=off)
	// Load address from LinkerKernelExcStackTop (high memory for kmazarin)
	// ========================================
	MOVD	main·LinkerKernelExcStackTop(SB), R0

	// DEBUG: Print SP_EL1 value before MSR using distinctive markers
	// Format: <SP1=XXXXXXXX> where X is top 8 hex digits (bits 63-32)
	MOVD	$0x09000000, R10
	MOVD	$'<', R5
	MOVB	R5, (R10)
	MOVD	$'S', R5
	MOVB	R5, (R10)
	MOVD	$'P', R5
	MOVB	R5, (R10)
	MOVD	$'1', R5
	MOVB	R5, (R10)
	MOVD	$'=', R5
	MOVB	R5, (R10)

	// Print 8 hex digits (bits 63-32 of R0)
	// Save R0 to R11 since we'll modify it
	MOVD	R0, R11

	// Digit 1 (bits 63-60)
	LSR	$60, R11, R5
	AND	$0xF, R5
	CMP	$10, R5
	BLT	3(PC)
	ADD	$('A'-10), R5
	B	2(PC)
	ADD	$'0', R5
	MOVB	R5, (R10)

	// Digit 2 (bits 59-56)
	LSR	$56, R11, R5
	AND	$0xF, R5
	CMP	$10, R5
	BLT	3(PC)
	ADD	$('A'-10), R5
	B	2(PC)
	ADD	$'0', R5
	MOVB	R5, (R10)

	// Digit 3 (bits 55-52)
	LSR	$52, R11, R5
	AND	$0xF, R5
	CMP	$10, R5
	BLT	3(PC)
	ADD	$('A'-10), R5
	B	2(PC)
	ADD	$'0', R5
	MOVB	R5, (R10)

	// Digit 4 (bits 51-48)
	LSR	$48, R11, R5
	AND	$0xF, R5
	CMP	$10, R5
	BLT	3(PC)
	ADD	$('A'-10), R5
	B	2(PC)
	ADD	$'0', R5
	MOVB	R5, (R10)

	// Digit 5 (bits 47-44)
	LSR	$44, R11, R5
	AND	$0xF, R5
	CMP	$10, R5
	BLT	3(PC)
	ADD	$('A'-10), R5
	B	2(PC)
	ADD	$'0', R5
	MOVB	R5, (R10)

	// Digit 6 (bits 43-40)
	LSR	$40, R11, R5
	AND	$0xF, R5
	CMP	$10, R5
	BLT	3(PC)
	ADD	$('A'-10), R5
	B	2(PC)
	ADD	$'0', R5
	MOVB	R5, (R10)

	// Digit 7 (bits 39-36)
	LSR	$36, R11, R5
	AND	$0xF, R5
	CMP	$10, R5
	BLT	3(PC)
	ADD	$('A'-10), R5
	B	2(PC)
	ADD	$'0', R5
	MOVB	R5, (R10)

	// Digit 8 (bits 35-32)
	LSR	$32, R11, R5
	AND	$0xF, R5
	CMP	$10, R5
	BLT	3(PC)
	ADD	$('A'-10), R5
	B	2(PC)
	ADD	$'0', R5
	MOVB	R5, (R10)

	// Restore R0 from R11
	MOVD	R11, R0

	MOVD	$'>', R5
	MOVB	R5, (R10)

	MSR_SP_EL1_X0

	// Print 'Q' to show SP_EL1 was set at EL2
	MOVD	$'Q', R5
	MOVB	R5, (R10)

	// Configure SPSR_EL2 for return to EL1h (EL1 using SP_EL1)
	// M[3:0] = 0b0101 = EL1h (EL1 with SP_EL1)
	// DAIF = 0b1111: All exceptions masked initially
	MOVD	$0x3C5, R0
	MSR_SPSR_EL2_X0

	// Set ELR_EL2 to the address we want to return to (at_el1)
	// Layout:
	//   ADR x0, at_el1   (this instruction, at PC)
	//   MSR elr_el2, x0  (PC + 4)
	//   ERET             (PC + 8)
	//   at_el1:          (PC + 12) <- this is where we want to return
	// So we need: ADR X0, .+12
	// ADR encoding: imm[20:5] = offset/4, but it's actually encoded as:
	//   immlo (bits 30:29) = offset[1:0]
	//   immhi (bits 23:5) = offset[20:2]
	// For offset = 12 = 0xC:
	//   ADR X0, .+12 = 0x10000060
	WORD	$0x10000060		// adr x0, .+12 (at_el1 is 3 instructions ahead)
	MSR_ELR_EL2_X0

	// Return to EL1 (exception return from EL2 to EL1)
	ERET_INSN

at_el1:
	// Now we're at EL1
	// NOTE: We may have arrived here from EL2 (via ERET) or started directly at EL1

	// ========================================
	// CRITICAL: Set up BOTH stacks
	// If we came from EL2, SP_EL1 was set there.
	// If we started at EL1, we need to set SP_EL1 NOW!
	//
	// Stack Architecture:
	// - SP_EL1: Exception handler stack, HIGH MEMORY (LinkerKernelExcStackTop)
	// - SP_EL0: g0/kernel stack, used in EL1t mode (LinkerStackTop)
	// ========================================

	// First, set SP_EL1 (exception stack) by switching to EL1h mode
	// From EL1, we can only write to SP_EL1 when SPSel=1 (EL1h mode)
	MSR	$1, SPSel		// Switch to EL1h mode (SP = SP_EL1)
	ISB	$15

	// Load the exception stack top address
	MOVD	main·LinkerKernelExcStackTop(SB), R0

	// DEBUG: Print 'X' and the SP_EL1 value we're about to set
	MOVD	$0x09000000, R10
	MOVD	$'X', R5
	MOVB	R5, (R10)
	// Print first 4 hex digits of R0 (should be FFFF)
	MOVD	R0, R11
	LSR	$60, R11, R5
	AND	$0xF, R5
	CMP	$10, R5
	BLT	sp_el1_digit1
	ADD	$('A'-10), R5
	B	sp_el1_print1
sp_el1_digit1:
	ADD	$'0', R5
sp_el1_print1:
	MOVB	R5, (R10)

	// Set SP (which is SP_EL1 in EL1h mode) to exception stack top
	MOVD	R0, RSP			// This sets SP_EL1!

	// DEBUG: Print 'Y' to confirm SP_EL1 was set
	MOVD	$0x09000000, R10
	MOVD	$'Y', R5
	MOVB	R5, (R10)

	// Now set SP_EL0 (g0 stack)
	MOVD	main·LinkerStackTop(SB), R0
	MSR	R0, SP_EL0

	// Switch to EL1t mode to use SP_EL0 for normal execution
	// SPSel=0 means use SP_EL0 (still at EL1 privilege!)
	MSR	$0, SPSel

	// Breadcrumb to confirm early boot progress
	MOVW	$'!', R15
	MOVW	R15, (R14)

	// ========================================
	// Enable SIMD/floating-point
	// CPACR_EL1.FPEN (bits 21:20) = 0b11: No trapping
	// ========================================
	MOVD	$(3<<20), R0
	MSR	R0, CPACR_EL1
	ISB	$15

	// ========================================
	// Disable strict alignment checking
	// SCTLR_EL1.A (bit 1) = 0: Allow unaligned access
	// ========================================
	MRS	SCTLR_EL1, R0
	AND	$~2, R0, R0		// Clear bit 1 (A = alignment check)
	MSR	R0, SCTLR_EL1
	ISB	$15

	// ========================================
	// Clear BSS section
	// ========================================

	// Load BSS start and end from Go variables in layout.go
	// Values may be injected at build time or set by ELF post-processor
	MOVD	main·LinkerBssStart(SB), R4	// R4 = BSS start address
	MOVD	main·LinkerBssEnd(SB), R9	// R9 = BSS end address

	// Zero registers for clearing
	MOVD	$0, R5
	MOVD	$0, R6
	MOVD	$0, R7
	MOVD	$0, R8
	B	bss_clear_check

bss_clear_loop:
	// Store 64 bytes at a time (4 pairs of 16 bytes)
	STP	(R5, R6), (R4)
	ADD	$16, R4
	STP	(R7, R8), (R4)
	ADD	$16, R4
	STP	(R5, R6), (R4)
	ADD	$16, R4
	STP	(R7, R8), (R4)
	ADD	$16, R4

bss_clear_check:
	CMP	R9, R4
	BLO	bss_clear_loop

	// Breadcrumb: BSS clear complete
	// R14 still holds UART base (no BL instructions since it was set)
	MOVW	$'B', R15
	MOVW	R15, (R14)

	// NOTE: mmap bump pointer (main.mmapBumpNext) is initialized by Go's
	// static variable initialization: var mmapBumpNext uintptr = BUMP_REGION_START
	// No need to set it here in assembly.

	// ========================================
	// Enable write barrier flag AFTER clearing BSS
	// ========================================
	MOVD	$runtime·writeBarrier(SB), R10
	MOVW	$1, R11
	MOVB	R11, (R10)
	DSB	$15

	// Breadcrumb: Write barrier enabled
	MOVW	$'W', R15
	MOVW	R15, (R14)

	// ========================================
	// Set exception vector base
	// ========================================
	MOVD	$vec_sync_sp_el0(SB), R0

	// Breadcrumb: About to set VBAR_EL1
	MOVW	$'V', R15
	MOVW	R15, (R14)

	DSB	$15
	MSR	R0, VBAR_EL1
	ISB	$15

	// Breadcrumb: VBAR_EL1 written
	MOVW	$'v', R15
	MOVW	R15, (R14)

	// Verify VBAR_EL1 was set correctly
	MRS	VBAR_EL1, R1

	// Breadcrumb: VBAR_EL1 read back
	MOVW	$'X', R15
	MOVW	R15, (R14)

	CMP	R0, R1
	BEQ	vbar_ok
	// VBAR mismatch - hang
	B	boot_halt

vbar_ok:
	// Breadcrumb: VBAR verified OK
	MOVW	$'Y', R15
	MOVW	R15, (R14)
	// ========================================
	// Initialize GIC (Generic Interrupt Controller)
	// ========================================

	// 1. Enable the GIC Distributor (GICD_CTLR)
	MOVD	main·LinkerGicBase(SB), R0	// GICD base (from LinkerGicBase)
	MOVW	$1, R1
	MOVW	R1, (R0)		// GICD_CTLR = 1

	// 2. Enable interrupt 27 (virtual timer PPI)
	MOVW	$(1<<27), R1
	MOVW	R1, 0x100(R0)		// GICD_ISENABLER0

	// 3. Set priority for interrupt 27
	MOVW	$0xA0, R1
	MOVB	R1, (0x400+27)(R0)	// GICD_IPRIORITYR[27]

	// 4. Enable the CPU Interface
	// GICC base = GICD base + 0x10000 (GICv2 fixed offset)
	MOVD	main·LinkerGicBase(SB), R0
	ADD	$0x10000, R0		// GICC base
	MOVW	$1, R1
	MOVW	R1, (R0)		// GICC_CTLR = 1

	// 5. Set priority mask
	MOVW	$0xFF, R1
	MOVW	R1, 4(R0)		// GICC_PMR = 0xFF

	DSB	$15
	ISB	$15

	// ========================================
	// Initialize g0 and m0
	// ========================================

	// Set g register (R28) to point to runtime.g0
	MOVD	$runtime·g0(SB), g	// g is alias for R28

	// Set up stack guards for g0
	MOVD	RSP, R7			// Current stack pointer
	SUB	$(64*1024), R7, R0	// Stack guard = SP - 64KB

	// g.stackguard0 and g.stackguard1 (offsets 16, 24)
	MOVD	R0, 16(g)
	MOVD	R0, 24(g)

	// g.stack.lo and g.stack.hi (offsets 0, 8)
	MOVD	R0, 0(g)		// stack.lo
	MOVD	R7, 8(g)		// stack.hi

	// Link g0 and m0
	MOVD	$runtime·m0(SB), R0
	MOVD	R0, 48(g)		// g0.m = &m0
	MOVD	g, (R0)			// m0.g0 = &g0

	// Breadcrumb: About to call KernelMain
	// R14 still holds UART base (this is the last use before BL clobbers it)
	MOVW	$'G', R15
	MOVW	R15, (R14)

	// ========================================
	// Jump to KernelMain
	// ========================================
	MOVD	R22, R2			// DTB pointer as third argument
	BL	main·KernelMain(SB)

	// ========================================
	// After KernelMain returns: enable IRQs and idle
	// ========================================

	// Enable IRQs by clearing I bit (MSR DAIFCLR, #2)
	WORD	$0xD50342FF

idle_loop:
	HINT	$1			// WFI - Wait for interrupt
	B	idle_loop

cpu_halt:
	HINT	$2			// WFE - Wait for event
	B	cpu_halt

boot_halt:
	HINT	$2			// WFE
	B	boot_halt

// ============================================================================
// EL2 Exception Vector Table
// ============================================================================
// This table is installed at EL2 before dropping to EL1
// It allows EL1 code to call HVC #1 to set SP_EL1 to high memory
// Table must be 2KB (0x800) aligned

TEXT el2_exception_vectors(SB), NOSPLIT|NOFRAME, $0
	PCALIGN	$2048		// 2KB alignment
el2_vec_start:
	// 0x000-0x07F: Current EL with SP0 - Synchronous
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F

	// 0x080-0x0FF: Current EL with SP0 - IRQ
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F

	// 0x100-0x17F: Current EL with SP0 - FIQ
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F

	// 0x180-0x1FF: Current EL with SP0 - SError
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F

	// 0x200-0x3FF: Current EL with SPx (all NOPs - 512 bytes)
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F

	// 0x400: Lower EL using AArch64 - Synchronous (HVC lands here!)
el2_vec_sync_lower_64:
	B	el2_hvc_handler(SB)
	// Fill rest of 128-byte vector with NOPs
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F

	// 0x480-0x7FF: Rest of lower EL vectors (all NOPs - 896 bytes)
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F
	WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F; WORD $0xD503201F

// ============================================================================
// EL2 HVC Handler - Set SP_EL1 to High Memory
// ============================================================================
// Called via HVC #1 from EL1
// X0 = desired SP_EL1 value (high-memory address)
// This handler validates the request and sets SP_EL1
//
// ESR_EL2 format for HVC:
//   Bits [31:26] = EC (Exception Class) = 0x16 for HVC
//   Bits [15:0]  = immediate value from HVC instruction

TEXT el2_hvc_handler(SB), NOSPLIT|NOFRAME, $0
	// Read ESR_EL2 to validate this is HVC #1
	MRS_ESR_EL2_X1

	// Extract Exception Class (bits 31:26)
	LSR	$26, R1, R2		// R2 = EC
	MOVD	$0x16, R3		// Expected EC for HVC
	CMP	R2, R3
	BNE	el2_panic_bad_ec

	// Extract immediate value (bits 15:0)
	AND	$0xFFFF, R1, R2		// R2 = immediate
	MOVD	$1, R3			// Expected immediate = 1
	CMP	R2, R3
	BNE	el2_panic_bad_imm

	// Validation passed - set SP_EL1
	// Print 'M' before MSR
	MOVD	$0x09000000, R10
	MOVD	$'M', R5
	MOVB	R5, (R10)

	MSR_SP_EL1_X0

	// Print 'N' after MSR
	MOVD	$'N', R5
	MOVB	R5, (R10)

	// Return to EL1
	ERET_INSN

// EL2 Panic: Wrong Exception Class
el2_panic_bad_ec:
	// Print error to UART: "EL2: Bad EC"
	MOVD	$0x09000000, R10
	MOVD	$'E', R5
	MOVB	R5, (R10)
	MOVD	$'L', R5
	MOVB	R5, (R10)
	MOVD	$'2', R5
	MOVB	R5, (R10)
	MOVD	$':', R5
	MOVB	R5, (R10)
	MOVD	$' ', R5
	MOVB	R5, (R10)
	MOVD	$'B', R5
	MOVB	R5, (R10)
	MOVD	$'a', R5
	MOVB	R5, (R10)
	MOVD	$'d', R5
	MOVB	R5, (R10)
	MOVD	$' ', R5
	MOVB	R5, (R10)
	MOVD	$'E', R5
	MOVB	R5, (R10)
	MOVD	$'C', R5
	MOVB	R5, (R10)
	MOVD	$'\r', R5
	MOVB	R5, (R10)
	MOVD	$'\n', R5
	MOVB	R5, (R10)
	B	el2_halt

// EL2 Panic: Wrong HVC Immediate Value
el2_panic_bad_imm:
	// Print error to UART: "EL2: Bad HVC"
	MOVD	$0x09000000, R10
	MOVD	$'E', R5
	MOVB	R5, (R10)
	MOVD	$'L', R5
	MOVB	R5, (R10)
	MOVD	$'2', R5
	MOVB	R5, (R10)
	MOVD	$':', R5
	MOVB	R5, (R10)
	MOVD	$' ', R5
	MOVB	R5, (R10)
	MOVD	$'B', R5
	MOVB	R5, (R10)
	MOVD	$'a', R5
	MOVB	R5, (R10)
	MOVD	$'d', R5
	MOVB	R5, (R10)
	MOVD	$' ', R5
	MOVB	R5, (R10)
	MOVD	$'H', R5
	MOVB	R5, (R10)
	MOVD	$'V', R5
	MOVB	R5, (R10)
	MOVD	$'C', R5
	MOVB	R5, (R10)
	MOVD	$'\r', R5
	MOVB	R5, (R10)
	MOVD	$'\n', R5
	MOVB	R5, (R10)
	B	el2_halt

el2_halt:
	HINT	$2			// WFE
	B	el2_halt
