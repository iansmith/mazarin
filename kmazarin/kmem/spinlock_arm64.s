// spinlock_arm64.s - ARM64 assembly for spinlock support

#include "textflag.h"

// yieldProcessor executes WFE (Wait For Event) instruction.
// This puts the core in a low-power state until an event occurs.
// On ARM64, store-exclusive instructions (used by atomic.CompareAndSwap)
// automatically generate events, so this works well for spinlocks.
//
// func yieldProcessor()
TEXT ·yieldProcessor(SB), NOSPLIT, $0
	WFE
	RET

// saveAndDisableIRQsLocal reads DAIF, masks IRQs, and returns the prior DAIF.
// kmem-local copy of the kernel's SaveAndDisableIRQs (kmem cannot import the
// main package — import cycle). Used to make kmem.Spinlock IRQ-atomic: a holder
// must not be preemptible, because the same lock is also acquired from the
// IRQ-masked demand-fault allocator. Encodings match exceptions_arm64.s.
//
// func saveAndDisableIRQsLocal() uint64
TEXT ·saveAndDisableIRQsLocal(SB), NOSPLIT|NOFRAME, $0-8
	WORD	$0xD53B4220   // MRS X0, DAIF (op2=1; 0xD53B4200 was MRS NZCV) — read current DAIF into R0
	WORD	$0xD50342DF   // MSR DAIFSET, #2 — set I bit (disable IRQs)
	ISB	$15
	MOVD	R0, ret+0(FP)
	RET

// restoreIRQsLocal writes saved back to DAIF (restoring the prior IRQ state).
//
// func restoreIRQsLocal(saved uint64)
TEXT ·restoreIRQsLocal(SB), NOSPLIT|NOFRAME, $0-8
	MOVD	saved+0(FP), R0
	WORD	$0xD51B4220   // MSR DAIF, X0 (op2=1; 0xD51B4200 is NZCV) (MAZ-128/MAZ-166)
	ISB	$15
	RET
