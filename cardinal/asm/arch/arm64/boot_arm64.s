// boot_arm64.s - ARM64 Bare-Metal Boot Sequence
//
// ============================================================================
// OVERVIEW
// ============================================================================
// This is the bare-metal boot entry point for ARM64. QEMU loads Cardinal's
// ELF and jumps to _cardinal_boot at reset.
//
// Functions:
//   - _cardinal_boot() - Main boot sequence (15 segments)
//
// This code runs BEFORE the Go runtime is initialized:
// - Cannot use any Go runtime features (no heap, no GC, no goroutines)
// - Cannot allocate memory (heap not initialized)
// - Cannot call Go functions normally (except via raw BL to known addresses)
// - Must use NOSPLIT|NOFRAME to prevent stack frame manipulation
//
// WHY NOT DECOMPOSE:
// Boot is a large orchestration function that coordinates hardware initialization.
// It cannot be decomposed because:
//   1. Runs before Go runtime - cannot call Go functions normally
//   2. Order-dependent - each step depends on previous (EL detection → EL config → stack setup)
//   3. State transitions - moves between exception levels (EL2 → EL1)
//   4. One-time execution - runs once at boot, not a reusable component
//
// POST-BOOT: After KernelMain starts, use decomposed primitives from:
//   - lib_sysregs_arm64.s for system register access
//   - lib_barriers_arm64.s for memory barriers
//   - mmio_arm64.s for device register access
//
// See asm/docs/boot_decomposition.md for detailed primitive mapping.
//
// ============================================================================
// BOOT SEQUENCE SEGMENTS (see asm/docs/boot_decomposition.md)
// ============================================================================
//
// Segment 1:  DTB Preservation & CPU Check - Preserve R0, halt non-boot CPUs
// Segment 2:  Exception Level Detection - Check if EL2 or EL1
// Segment 3:  EL2 Configuration - Configure HCR, timers, vectors (EL2 only)
// Segment 4:  SP_EL1 Setup at EL2 - Set exception stack while at EL2
// Segment 5:  ERET to EL1 - Configure SPSR/ELR and return to EL1
// Segment 6:  Stack Setup at EL1 - Configure SP_EL0 and SP_EL1
// Segment 7:  SIMD/FPU Enable - Set CPACR_EL1.FPEN
// Segment 8:  Alignment Check Disable - Clear SCTLR_EL1.A
// Segment 9:  BSS Clear - Zero .bss section
// Segment 10: Write Barrier Enable - Set runtime.writeBarrier flag
// Segment 11: Exception Vector Setup - Set VBAR_EL1
// Segment 12: GIC Initialization - Enable distributor and CPU interface
// Segment 13: g0/m0 Initialization - Set up Go runtime structures
// Segment 14: Jump to KernelMain - Call Go entry point
// Segment 15: Post-KernelMain Idle - Enable IRQs and WFI loop
//
// POST-BOOT NOTE: After boot completes, use decomposed primitives from:
//   - lib_sysregs_arm64.s for system register access
//   - lib_barriers_arm64.s for memory barriers
//   - mmio_arm64.s for device register access
// See boot_decomposition.md for detailed primitive mapping per segment.

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

	// ========================================
	// SEGMENT 1: DTB Preservation & CPU Check
	// ========================================
	// Preserve QEMU-provided DTB pointer and halt non-boot CPUs.
	// POST-BOOT: N/A (one-time boot operation)

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
	// SEGMENT 2: Exception Level Detection
	// ========================================
	// Detect current EL to determine if EL2→EL1 drop is needed.
	// QEMU virt machine can start at EL2 or EL1 depending on configuration.
	// POST-BOOT: Use readCurrentEL() from lib_sysregs for diagnostics

	// mrs x0, CurrentEL
	MRS_CURRENTEL_X0
	// Extract EL bits [3:2]
	LSR	$2, R0, R0

	CMP	$2, R0
	BNE	at_el1

	// ========================================
	// SEGMENT 3: EL2 Configuration
	// ========================================
	// Configure EL2 hypervisor registers before dropping to EL1.
	// These use WORD encodings because Go assembler doesn't support EL2 regs.
	// POST-BOOT: N/A (EL2 registers not accessible from EL1)

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

	// Set up EL2 exception vectors BEFORE dropping to EL1
	// This allows us to call back to EL2 later to modify SP_EL1
	MOVD	$el2_exception_vectors(SB), R0
	MSR_VBAR_EL2_X0

	// ========================================
	// SEGMENT 4: SP_EL1 Setup at EL2
	// ========================================
	// Set exception stack while still at EL2 (can't be done easily from EL1).
	// POST-BOOT: N/A (SP_EL1 set once, modified only by mode switching)

	// CRITICAL: Set SP_EL1 to LOW MEMORY exception stack NOW
	// Must use low memory because MMU is not enabled yet and high memory
	// addresses (0xFFFFFFFF...) don't exist in physical memory.
	// The stack will be switched to high memory after MMU is enabled.
	MOVD	main·LinkerExceptionStackTop(SB), R0
	MSR_SP_EL1_X0

	// ========================================
	// SEGMENT 5: ERET to EL1
	// ========================================
	// Configure return state and perform exception return to EL1.
	// POST-BOOT: N/A (one-time transition)

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
	// SEGMENT 6: Stack Setup at EL1
	// ========================================
	// Configure both stacks (SP_EL0 for kernel, SP_EL1 for exceptions).
	// If we came from EL2, SP_EL1 was already set there.
	// If we started at EL1, we need to set SP_EL1 NOW!
	//
	// Stack Architecture:
	// - SP_EL1: Exception handler stack, LOW MEMORY initially (LinkerExceptionStackTop)
	//           Switched to HIGH MEMORY (LinkerKernelExcStackTop) after MMU is enabled
	// - SP_EL0: g0/kernel stack, used in EL1t mode (LinkerStackTop)
	//
	// CRITICAL: Must use LOW memory for SP_EL1 during early boot because
	// exception vectors are active but MMU is not yet enabled. High memory
	// addresses (0xFFFFFFFF...) don't exist until MMU maps them.
	//
	// POST-BOOT: Use writeSPSel() and instructionBarrier() from lib_barriers

	// First, set SP_EL1 (exception stack) by switching to EL1h mode
	// From EL1, we can only write to SP_EL1 when SPSel=1 (EL1h mode)
	MSR	$1, SPSel		// Switch to EL1h mode (SP = SP_EL1)
	ISB	$15

	// Load the LOW MEMORY exception stack top address (for early boot before MMU)
	MOVD	main·LinkerExceptionStackTop(SB), R0

	// Set SP (which is SP_EL1 in EL1h mode) to exception stack top
	MOVD	R0, RSP			// This sets SP_EL1!

	// Now set SP_EL0 (g0 stack)
	MOVD	main·LinkerStackTop(SB), R0
	MSR	R0, SP_EL0

	// Switch to EL1t mode to use SP_EL0 for normal execution
	// SPSel=0 means use SP_EL0 (still at EL1 privilege!)
	MSR	$0, SPSel

	// ========================================
	// SEGMENT 7: SIMD/FPU Enable
	// ========================================
	// Enable floating-point and SIMD instructions.
	// CPACR_EL1.FPEN (bits 21:20) = 0b11: No trapping
	// POST-BOOT: Use instructionBarrier() from lib_barriers
	MOVD	$(3<<20), R0
	MSR	R0, CPACR_EL1
	ISB	$15

	// ========================================
	// SEGMENT 8: Alignment Check Disable
	// ========================================
	// Disable strict alignment checking for Go runtime compatibility.
	// SCTLR_EL1.A (bit 1) = 0: Allow unaligned access
	// POST-BOOT: Use readSystemControlRegister(), clearAlignmentCheckBit(),
	//            writeSystemControlRegister(), instructionBarrier() from lib_sysregs
	MRS	SCTLR_EL1, R0
	AND	$~2, R0, R0		// Clear bit 1 (A = alignment check)
	MSR	R0, SCTLR_EL1
	ISB	$15

	// ========================================
	// SEGMENT 9: BSS Clear
	// ========================================
	// Zero-initialize the BSS section before any Go code runs.
	// POST-BOOT: N/A (one-time initialization)

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

	// NOTE: mmap bump pointer (main.mmapBumpNext) is initialized by Go's
	// static variable initialization: var mmapBumpNext uintptr = BUMP_REGION_START
	// No need to set it here in assembly.

	// ========================================
	// SEGMENT 10: Write Barrier Enable
	// ========================================
	// Enable Go's write barrier after BSS is cleared.
	// POST-BOOT: Use dataBarrierFullSystem() from lib_barriers
	MOVD	$runtime·writeBarrier(SB), R10
	MOVW	$1, R11
	MOVB	R11, (R10)
	DSB	$15

	// ========================================
	// SEGMENT 11: Exception Vector Setup
	// ========================================
	// Install EL1 exception vector table.
	// POST-BOOT: Use dataBarrierFullSystem(), writeExceptionVectorBase(),
	//            instructionBarrier(), readExceptionVectorBase() from lib_sysregs
	MOVD	$vec_sync_sp_el0(SB), R0
	DSB	$15
	MSR	R0, VBAR_EL1
	ISB	$15

	// Verify VBAR_EL1 was set correctly
	MRS	VBAR_EL1, R1
	CMP	R0, R1
	BEQ	vbar_ok
	B	boot_halt

vbar_ok:
	// ========================================
	// SEGMENT 12: GIC Initialization
	// ========================================
	// Initialize Generic Interrupt Controller for timer interrupts.
	// POST-BOOT: Use mmioWrite32(), mmioRead32() from mmio,
	//            dataBarrierFullSystem(), instructionBarrier() from lib_barriers

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
	// SEGMENT 13: g0/m0 Initialization
	// ========================================
	// Initialize Go runtime's g0 and m0 structures.
	// POST-BOOT: Use setGoroutinePointer() from lib_barriers,
	//            LinkerRuntimeG0(), LinkerRuntimeM0() from linker_symbols

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

	// ========================================
	// SEGMENT 14: Jump to KernelMain
	// ========================================
	// Call Go entry point with DTB pointer.
	// POST-BOOT: N/A (Go calling convention)
	MOVD	$0, R0			// First argument (unused)
	MOVD	$0, R1			// Second argument (unused)
	MOVD	R22, R2			// DTB pointer as third argument
	BL	main·KernelMain(SB)

	// ========================================
	// SEGMENT 15: Post-KernelMain Idle
	// ========================================
	// Enable IRQs and enter idle loop after KernelMain returns.
	// POST-BOOT: Use enableIRQs() if added to lib_sysregs

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
	// Fill rest of 128-byte vector with NOPs (31 WORDs = 124 bytes)
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
	MSR_SP_EL1_X0

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
