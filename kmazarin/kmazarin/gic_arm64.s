
#include "textflag.h"

// GIC-400 (GICv2) register offsets
// NOTE: Using PHYSICAL addresses because high-memory GIC mapping has issues
// The GIC device driver (gicv2.go) also uses physical addresses for the same reason
#define GICD_BASE	0x08000000
#define GICC_BASE	0x08010000

// GICD registers
#define GICD_CTLR	0x0000
#define GICD_ISENABLER0	0x0100

// GICC registers
#define GICC_CTLR	0x0000
#define GICC_PMR	0x0004

// EnableGIC enables the GIC distributor and CPU interface
// This allows interrupts to be delivered to kmazarin
TEXT ·EnableGIC(SB), NOSPLIT, $0
	// Enable GICD (distributor)
	MOVD	$(GICD_BASE + GICD_CTLR), R0
	MOVD	$1, R1
	MOVW	R1, (R0)

	// Enable GICC (CPU interface)
	MOVD	$(GICC_BASE + GICC_CTLR), R0
	MOVD	$1, R1
	MOVW	R1, (R0)

	// Set priority mask to allow all priorities
	MOVD	$(GICC_BASE + GICC_PMR), R0
	MOVD	$0xF0, R1
	MOVW	R1, (R0)

	ISB	$15
	RET

// DisableTimerHardware disables the ARM virtual timer hardware
// This stops the timer from generating interrupt requests
TEXT ·DisableTimerHardware(SB), NOSPLIT, $0
	// Disable timer: CNTV_CTL_EL0 = 0 (disable bit clear)
	MOVD	$0, R0
	// MSR CNTV_CTL_EL0, X0
	// CNTV_CTL_EL0 = S3_3_C14_C3_1 (op0=3, op1=3, CRn=14, CRm=3, op2=1)
	// Encoding: 0xD51BE320
	WORD	$0xD51BE320

	ISB	$15
	RET

// ReadCntvCtlEl0 reads the virtual timer control register
TEXT ·ReadCntvCtlEl0(SB), NOSPLIT, $0-8
	// MRS X0, CNTV_CTL_EL0
	WORD	$0xD53BE320
	MOVD	R0, ret+0(FP)
	RET

// ReadCntvTvalEl0 reads the virtual timer value register
TEXT ·ReadCntvTvalEl0(SB), NOSPLIT, $0-8
	// MRS X0, CNTV_TVAL_EL0
	WORD	$0xD53BE300
	MOVD	R0, ret+0(FP)
	RET

// ReadCntvctEl0 reads the virtual counter register
TEXT ·ReadCntvctEl0(SB), NOSPLIT, $0-8
	// MRS X0, CNTVCT_EL0
	WORD	$0xD53BE040
	MOVD	R0, ret+0(FP)
	RET

// ReadCntfrqEl0 reads the counter frequency register
TEXT ·ReadCntfrqEl0(SB), NOSPLIT, $0-8
	// MRS X0, CNTFRQ_EL0
	WORD	$0xD53BE000
	MOVD	R0, ret+0(FP)
	RET

// RearmTimerNow re-arms the virtual timer to fire after ~10ms
TEXT ·RearmTimerNow(SB), NOSPLIT, $0
	// CRITICAL: Disable timer FIRST to clear any pending state from Cardinal
	// This matches Cardinal's timerInit() pattern: disable, set TVAL, enable
	MOVD	$0, R0
	// MSR CNTV_CTL_EL0, X0
	// CNTV_CTL_EL0 = S3_3_C14_C3_1 (op0=3, op1=3, CRn=14, CRm=3, op2=1)
	WORD	$0xD51BE320
	ISB	$15

	// Set CNTV_TVAL_EL0 to ~10ms worth of ticks
	// Assuming 62.5MHz: 10ms * 62.5MHz = 625000 = 0x98968
	MOVD	$0x98968, R0
	// MSR CNTV_TVAL_EL0, X0
	// CNTV_TVAL_EL0 = S3_3_C14_C3_0 (op0=3, op1=3, CRn=14, CRm=3, op2=0)
	// Encoding: 0xD51BE300 (NOT 0xD51BE000 which has wrong CRm=0!)
	WORD	$0xD51BE300
	ISB	$15

	// Enable timer: CNTV_CTL_EL0 = 1 (enable bit, interrupt unmasked)
	MOVD	$1, R0
	// MSR CNTV_CTL_EL0, X0
	WORD	$0xD51BE320
	ISB	$15

	RET
