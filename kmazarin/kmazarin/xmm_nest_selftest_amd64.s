// MAZ-139 DoD#1 — XMM nested-exception RED self-test probe (amd64).
//go:build amd64

#include "textflag.h"
#include "xmm_frame_amd64.h"

// xmmNestProbe(sentA, gotOuter *byte)
//
// Drives the PRODUCTION exception XMM save/restore through a REAL two-level
// nesting — NOT a standalone reimplementation:
//
//  1. Load sentinel A into X0..X15.
//  2. INT $48 — a REAL outer entry through the production timer vector
//     (isr48 → common_exception_entry saves X0..X15 to the single global
//     ·xmmSaveArea → handle_timer_irq).
//  3. The one-shot hook at handle_timer_irq (exceptions_amd64.s), armed by
//     runXMMNestSelfTest, loads sentinel B and issues a REAL nested INT $48.
//     The nested common_exception_entry overwrites the global with B.
//  4. The nested level returns (restores B); the outer exception_return then
//     restores X0..X15 from the (clobbered) global → B.
//  5. Store X0..X15 to gotOuter.
//
// gotOuter == B (RED) while XMM lives in the single global; gotOuter == A
// (GREEN) once each nesting level saves XMM into its own exception frame
// (item 2). ist1Floor is published (SetupSyscallMSRs, main.go) before the test,
// so the nested entry rotates onto its own IST stack — it clobbers ONLY the
// global XMM, not the outer's stack frame (no MAZ-136 trample).
TEXT ·xmmNestProbe(SB), NOSPLIT, $0-16
	MOVQ	sentA+0(FP), AX
	RESTORE_XMM_AT(AX)		// X0..X15 = sentinel A
	INT	$48			// REAL outer entry through the production exception path
	MOVQ	gotOuter+8(FP), AX
	SAVE_XMM_AT(AX)			// gotOuter = X0..X15 after the outer exception_return
	RET
