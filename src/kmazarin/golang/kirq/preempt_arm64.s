//go:build qemuvirt && aarch64

#include "textflag.h"

// TimerIRQHandlerAsm is the pure assembly timer IRQ handler.
// Called from exceptions_arm64.s when IRQ 27 (timer) is detected.
//
// This handler does NOT call any Go functions, making it safe to execute
// from IRQ context where the Go runtime may be in an inconsistent state.
//
// The handler:
//   1. Re-arms the timer for the next tick (~10ms)
//   2. Validates that preemption offsets are initialized
//   3. Gets g pointer from R28 (saved by caller in exception frame)
//   4. Validates g pointer (non-nil, in kernel memory, running state)
//   5. Sets g.preempt = true and g.stackguard0 = stackPreempt
//   6. Increments tick counter for async preemption tracking
//
// If any validation fails, the handler aborts and waits for the next tick.
// This is safe because cooperative preemption will eventually trigger
// at the next function call.
//
// Input:
//   R28 contains the g pointer (saved to exception frame before this is called)
//   Exception frame is on stack with saved registers
//
// Output:
//   None (modifies g struct directly)
//
// Clobbers: R0-R9 (caller must save if needed)
//
TEXT ·TimerIRQHandlerAsm(SB), NOSPLIT|NOFRAME, $0
	// ========================================================================
	// Step 1: Re-arm timer immediately
	// ========================================================================
	// This ensures we get the next tick even if we abort early.
	// Use relative timer (CNTV_TVAL_EL0) for simplicity.
	// 10ms at 62.5MHz = 625000 ticks (default, actual computed at runtime)

	// Load tick count from SystemTimerFrequency
	// ticks = (freq * 10) / 1000 = freq / 100
	MOVD	·SystemTimerFrequency(SB), R0
	MOVD	$100, R1
	UDIV	R1, R0, R0  // R0 = freq / 100 = ticks for 10ms

	// If frequency not set, use default
	CBZ	R0, use_default_ticks
	B	rearm_timer

use_default_ticks:
	MOVD	$625000, R0  // Default: 62.5MHz / 100 = 625000

rearm_timer:
	// Read current counter: MRS X1, CNTVCT_EL0
	WORD	$0xD53BE041

	// Calculate new compare value
	ADD	R0, R1, R1

	// Write to CNTV_CVAL_EL0: MSR CNTV_CVAL_EL0, X1
	WORD	$0xD51BE341

	// Ensure timer enabled: MSR CNTV_CTL_EL0, X2 (with value 1)
	MOVD	$1, R2
	WORD	$0xD51BE322

	// ========================================================================
	// Step 2: Check if preemption offsets are initialized
	// ========================================================================
	MOVW	·PreemptOffsetsValid(SB), R0  // uint32 - use MOVW not MOVD
	CBZ	R0, timer_return  // Not initialized, skip preemption

	// ========================================================================
	// Step 3: Get g pointer
	// ========================================================================
	// R28 contains g, but Go assembler doesn't allow direct access to x28.
	// Use raw instruction: MOV X4, X28
	WORD	$0xAA1C03E4  // mov x4, x28

	// R4 = g pointer
	CBZ	R4, timer_return  // g is nil, skip

	// ========================================================================
	// Step 4: Validate g pointer
	// ========================================================================
	// Check that g is in kernel memory range (high 16 bits == 0xFFFF)
	LSR	$48, R4, R5
	MOVD	$0xFFFF, R6
	CMP	R5, R6
	BNE	timer_return  // Not in kernel memory, skip

	// ========================================================================
	// Step 5: Check g.atomicstatus == _Grunning
	// ========================================================================
	// Load g.atomicstatus offset
	MOVD	·PreemptGStatusOffset(SB), R5
	ADD	R4, R5  // R5 = &g.atomicstatus

	// Load status (32-bit atomic, but single load is atomic on ARM64)
	MOVW	(R5), R6  // R6 = g.atomicstatus

	// Mask off _Gscan bit (0x1000) when comparing
	MOVW	·PreemptGScan(SB), R7  // uint32 - use MOVW not MOVD
	BIC	R7, R6, R8  // R8 = status & ~_Gscan

	// Compare with _Grunning
	MOVW	·PreemptGRunning(SB), R7  // uint32 - use MOVW not MOVD
	CMP	R8, R7
	BNE	timer_return  // Not running, skip preemption

	// Also check if _Gscan bit is set (GC is scanning this g)
	MOVW	·PreemptGScan(SB), R7  // uint32 - use MOVW not MOVD
	TST	R6, R7
	BNE	timer_return  // GC scanning, skip preemption

	// ========================================================================
	// Step 6: Set g.preempt = true
	// ========================================================================
	MOVD	·PreemptPreemptOffset(SB), R5
	ADD	R4, R5  // R5 = &g.preempt
	MOVD	$1, R6
	MOVB	R6, (R5)  // g.preempt = true

	// ========================================================================
	// Step 7: Set g.stackguard0 = stackPreempt
	// ========================================================================
	// This is the key operation - it poisons the stack guard so the next
	// function call triggers the preemption path in morestack.
	MOVD	·PreemptStackGuard0Offset(SB), R5
	ADD	R4, R5  // R5 = &g.stackguard0
	MOVD	·PreemptStackPreemptValue(SB), R6  // R6 = stackPreempt poison value
	MOVD	R6, (R5)  // g.stackguard0 = stackPreempt

	// ========================================================================
	// Step 8: Increment tick counter for async fallback tracking
	// ========================================================================
	// Hash g pointer to get index: (g >> 8) & 0x3FF
	LSR	$8, R4, R5
	AND	$0x3FF, R5  // R5 = hash index (0-1023)

	// Calculate address: &preemptTickCounts[index]
	MOVD	·preemptTickCounts(SB), R6
	LSL	$2, R5  // R5 = index * 4 (sizeof uint32)
	ADD	R5, R6  // R6 = &preemptTickCounts[index]

	// Atomic increment
	MOVW	$1, R7
atomic_inc:
	LDAXRW	(R6), R8      // Load exclusive
	ADD	R7, R8        // Increment
	STLXRW	R8, (R6), R9  // Store exclusive
	CBNZ	R9, atomic_inc  // Retry if failed

	// R8 now contains new tick count
	// TODO: If R8 >= 10, consider async preemption injection
	// For Phase 1, we just rely on cooperative preemption

timer_return:
	RET
