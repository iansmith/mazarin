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

// Other instruction encodings
#define ERET_INSN           WORD $0xD69F03E0   // eret

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
	CMP	$2, R0
	BNE	at_el1

	// We're at EL2, need to drop to EL1
	// Configure HCR_EL2 (Hypervisor Configuration Register)
	// RW (bit 31) = 1: EL1 uses AArch64
	MOVD	$(1<<31), R0
	MSR_HCR_EL2_X0

	// Configure CNTHCTL_EL2 to allow EL1/EL0 access to timers
	// EL1PCTEN (bit 0) = 1: Don't trap CNTPCT_EL0 reads from EL1
	// EL1PCEN (bit 1) = 1: Don't trap CNTP_* accesses from EL1
	MOVD	$3, R0
	MSR_CNTHCTL_EL2_X0

	// Set virtual timer offset to 0 (CNTVOFF_EL2)
	MOVD	$0, R0
	MSR_CNTVOFF_EL2_X0

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

	// ========================================
	// CRITICAL: Set up BOTH stacks FIRST
	// We just entered EL1h mode (using SP_EL1), but SP_EL1 is uninitialized!
	//
	// Stack Architecture:
	// - SP_EL1: Exception handler stack, used in EL1h mode (from LinkerExceptionStackTop)
	// - SP_EL0: g0/kernel stack, used in EL1t mode (from LinkerStackTop)
	// ========================================

	// Set SP_EL1 (exception stack) from LinkerExceptionStackTop
	// We're in EL1h mode, so 'MOVD Rn, RSP' sets SP_EL1
	MOVD	main·LinkerExceptionStackTop(SB), R0
	MOVD	R0, RSP

	// Set SP_EL0 (g0 stack) from LinkerStackTop
	// Use MSR to set SP_EL0 since we're currently using SP_EL1
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
