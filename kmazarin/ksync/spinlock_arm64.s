// spinlock_arm64.s - the kernel's ONE ARM64 IRQ save/restore + spin-yield
// implementation (MAZ-167). DAIF is touched only via assembler mnemonics, never
// hand-encoded WORDs: op2=0 selects NZCV and op2=1 selects DAIF — one hex digit
// apart — and hand-encoding that digit wrong (MAZ-128) in two duplicated copies
// of this primitive caused MAZ-166's machine-wide IRQ blackouts. The mnemonic
// form makes the typo class unwritable; ksync/irq_encoding_test.go is the gate.

#include "textflag.h"

// SaveAndDisableIRQs reads DAIF, masks IRQs, and returns the prior DAIF.
//
// Mask-width decision (MAZ-167, Ian, 2026-08-03): I-only. Masking I is
// sufficient for the locks' atomicity contract (IRQ is the only exception
// source that re-enters scheduler/spinlock machinery); D stays live so
// hardware watchpoints work inside critical sections, and A stays live so
// SErrors are attributed at the faulting context. RestoreIRQs writes all four
// bits back regardless, so the round-trip preserves the caller's full state.
//
// func SaveAndDisableIRQs() uint64
TEXT ·SaveAndDisableIRQs(SB), NOSPLIT|NOFRAME, $0-8
	MRS	DAIF, R0
	MSR	$2, DAIFSet	// set I bit (disable IRQs)
	ISB	$15
	MOVD	R0, ret+0(FP)
	RET

// RestoreIRQs writes saved back to DAIF (restoring the prior IRQ state).
//
// func RestoreIRQs(saved uint64)
TEXT ·RestoreIRQs(SB), NOSPLIT|NOFRAME, $0-8
	MOVD	saved+0(FP), R0
	MSR	R0, DAIF
	ISB	$15
	RET

// yieldProcessor executes WFE (Wait For Event) to relax a spin-wait.
// Store-exclusive instructions (used by atomic.CompareAndSwap) automatically
// generate events, so this works well for spinlocks.
//
// func yieldProcessor()
TEXT ·yieldProcessor(SB), NOSPLIT, $0
	WFE
	RET
