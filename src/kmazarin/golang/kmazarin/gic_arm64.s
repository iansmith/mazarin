//go:build qemuvirt && aarch64

#include "textflag.h"

// GIC-400 (GICv2) register offsets
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

// EnableTimerIRQ enables IRQ 27 (virtual timer) in the GIC
TEXT ·EnableTimerIRQ(SB), NOSPLIT, $0
	// Enable IRQ 27 in GICD_ISENABLER0
	// IRQ 27 is bit 27 in ISENABLER0
	MOVD	$(GICD_BASE + GICD_ISENABLER0), R0

	// ISENABLER is write-1-to-set, so we can just write the bit
	MOVD	$(1 << 27), R1		// R1 = (1 << 27)
	MOVW	R1, (R0)

	ISB	$15
	RET

// RearmTimerNow re-arms the virtual timer to fire after ~10ms
TEXT ·RearmTimerNow(SB), NOSPLIT, $0
	// Set CNTV_TVAL_EL0 to ~10ms worth of ticks
	// Assuming 62.5MHz: 10ms * 62.5MHz = 625000 = 0x98968
	MOVD	$0x98968, R0
	// MSR CNTV_TVAL_EL0, X0 = 0xD51BE000
	WORD	$0xD51BE000

	// Enable timer: CNTV_CTL_EL0 = 1 (enable bit)
	MOVD	$1, R0
	// MSR CNTV_CTL_EL0, X0 = 0xD51BE020
	WORD	$0xD51BE020

	ISB	$15
	RET
