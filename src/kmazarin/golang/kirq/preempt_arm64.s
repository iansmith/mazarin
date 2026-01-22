
//go:build !test_stubs

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
	// DEBUG: Increment and check timer IRQ counter
	// ========================================================================
	// TEST: Put counter back at kirq.TimerIRQCount to see if cycling returns
	// Load current count
	MOVD	·TimerIRQCount(SB), R0
	ADD	$1, R0
	MOVD	R0, ·TimerIRQCount(SB)
	// Memory barrier to ensure store completes
	DSB	$15  // DSB SY - full system barrier

	// ========================================================================
	// DEBUG: Check AsyncPreemptWrapperAddr for corruption
	// ========================================================================
	// This address (0x439d7bc0) was where TimerIRQCount used to be.
	// Check if something is writing to it unexpectedly.
	MOVD	·AsyncPreemptWrapperAddr(SB), R6
	// Expected value should be non-zero (set during init) and stable
	// If count > 100 and value is suspicious, output warning
	CMP	$100, R0
	BLT	skip_wrapper_check
	// Check if wrapper addr looks corrupted (e.g., small value like counter would be)
	// A valid function address should be > 0x43800000
	MOVD	$0x43800000, R7
	CMP	R7, R6
	BHS	skip_wrapper_check  // Value >= 0x43800000, looks valid
	// Wrapper addr is suspiciously low! Output '!' and the value
	MOVD	$0x09000000, R2  // UART base
	MOVD	$'!', R3
	MOVB	R3, (R2)
	MOVD	$'W', R3
	MOVB	R3, (R2)
	MOVD	$'=', R3
	MOVB	R3, (R2)
	// Output low 12 bits of R6 as 3 hex digits
	MOVD	R6, R4
	LSR	$8, R4, R3
	AND	$0xF, R3
	CMP	$10, R3
	BLT	wrapper_digit1
	ADD	$('A'-10), R3
	B	wrapper_out1
wrapper_digit1:
	ADD	$'0', R3
wrapper_out1:
	MOVB	R3, (R2)
	MOVD	R6, R4
	LSR	$4, R4, R3
	AND	$0xF, R3
	CMP	$10, R3
	BLT	wrapper_digit2
	ADD	$('A'-10), R3
	B	wrapper_out2
wrapper_digit2:
	ADD	$'0', R3
wrapper_out2:
	MOVB	R3, (R2)
	MOVD	R6, R3
	AND	$0xF, R3
	CMP	$10, R3
	BLT	wrapper_digit3
	ADD	$('A'-10), R3
	B	wrapper_out3
wrapper_digit3:
	ADD	$'0', R3
wrapper_out3:
	MOVB	R3, (R2)
	MOVD	$' ', R3
	MOVB	R3, (R2)
skip_wrapper_check:

	// Check for milestone counts and output breadcrumbs
	// Count 640: Output '#'
	MOVD	$640, R1
	CMP	R0, R1
	BNE	check_644
	MOVD	$0x09000000, R2  // UART base
	MOVD	$'#', R3
	MOVB	R3, (R2)
	MOVD	$'6', R3
	MOVB	R3, (R2)
	MOVD	$'4', R3
	MOVB	R3, (R2)
	MOVD	$'0', R3
	MOVB	R3, (R2)
	MOVD	$' ', R3
	MOVB	R3, (R2)

check_644:
	// Count 644: Output '@' and RE-READ counter to verify
	MOVD	$644, R1
	CMP	R0, R1
	BNE	check_650
	MOVD	$0x09000000, R2  // UART base
	MOVD	$'@', R3
	MOVB	R3, (R2)
	// DEBUG: Re-read the counter from memory to see if it's really 644
	MOVD	·TimerIRQCount(SB), R5  // Re-load counter
	MOVD	$'[', R3
	MOVB	R3, (R2)
	// Output R0 (what we stored) low nibble
	MOVD	R0, R4
	AND	$0xF, R4
	ADD	$'0', R4
	MOVB	R4, (R2)
	MOVD	$':', R3
	MOVB	R3, (R2)
	// Output R5 (what we read back) low nibble
	MOVD	R5, R4
	AND	$0xF, R4
	ADD	$'0', R4
	MOVB	R4, (R2)
	MOVD	$']', R3
	MOVB	R3, (R2)

check_650:
	// Count 650: Output '$'
	MOVD	$650, R1
	CMP	R0, R1
	BNE	check_655
	MOVD	$0x09000000, R2  // UART base
	MOVD	$'$', R3
	MOVB	R3, (R2)
	MOVD	$'6', R3
	MOVB	R3, (R2)
	MOVD	$'5', R3
	MOVB	R3, (R2)
	MOVD	$'0', R3
	MOVB	R3, (R2)
	MOVD	$' ', R3
	MOVB	R3, (R2)

check_655:
	// Count 655: Output '!' (should never wrap past 654)
	MOVD	$655, R1
	CMP	R0, R1
	BNE	continue_normal
	MOVD	$0x09000000, R2  // UART base
	MOVD	$'!', R3
	MOVB	R3, (R2)
	MOVD	$'6', R3
	MOVB	R3, (R2)
	MOVD	$'5', R3
	MOVB	R3, (R2)
	MOVD	$'5', R3
	MOVB	R3, (R2)
	MOVD	$' ', R3
	MOVB	R3, (R2)

continue_normal:
	// ========================================================================
	// Step 2: Check if preemption offsets are initialized
	// ========================================================================
	MOVW	·PreemptOffsetsValid(SB), R0  // uint32 - use MOVW not MOVD
	CBNZ	R0, offsets_valid
	B	timer_return  // Offsets not valid yet
offsets_valid:

	// ========================================================================
	// Step 3: Get g pointer
	// ========================================================================
	// R28 contains g, but Go assembler doesn't allow direct access to x28.
	// Use raw instruction: MOV X4, X28
	WORD	$0xAA1C03E4  // mov x4, x28

	// R4 = g pointer
	CBNZ	R4, g_not_nil
	B	timer_return  // g is nil
g_not_nil:

	// ========================================================================
	// Step 4: Validate g pointer
	// ========================================================================
	// Check that g is in kernel memory range (high 16 bits == 0xFFFF)
	// For userspace threads, g points to userspace memory - we skip the
	// Go runtime preemption (g.preempt, g.stackguard0) but still track
	// OS-level thread preemption time.
	LSR	$48, R4, R5
	MOVD	$0xFFFF, R6
	CMP	R5, R6
	BEQ	g_in_kernel
	B	g_in_userspace  // g in userspace - skip Go runtime preemption, check thread preemption
g_in_kernel:

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
	// Step 8: Check for g change and track preemption time
	// ========================================================================
	// R4 = current g pointer (validated above)

	// Load currentThread pointer directly (no index calculation needed)
	MOVD	main·CurrentThread(SB), R7  // *Thread
	CBNZ	R7, thread_not_nil
	B	timer_return  // currentThread is nil
thread_not_nil:

	// Load LastSeenG: offset 312
	MOVD	312(R7), R8  // R8 = currentThread.LastSeenG

	// Compare with current g (R4)
	CMP	R4, R8
	BEQ	same_goroutine

	// ========================================================================
	// G changed! Go runtime switched goroutines internally.
	// Reset: store current g as LastSeenG
	// But DON'T reset StartTick - we still want to track total thread time
	// for OS-level preemption of priests waiting in ready queue.
	// ========================================================================
	MOVD	R4, 312(R7)  // currentThread.LastSeenG = current g

	// Don't reset StartTick - continue tracking total thread elapsed time
	// Fall through to same_goroutine to check thread preemption threshold
	B	same_goroutine

same_goroutine:
	// Same g - check elapsed time
	// Load StartTick: offset 320
	MOVD	320(R7), R8  // R8 = currentThread.StartTick
	CBZ	R8, init_start_tick  // Not initialized yet

	// Read current counter: MRS X9, CNTVCT_EL0
	WORD	$0xD53BE049

	// Calculate elapsed ticks
	SUB	R8, R9, R9  // R9 = current - start = elapsed

	// Convert to timer intervals (divide by ticks per 10ms)
	// ticks_per_10ms = freq / 100
	MOVD	·SystemTimerFrequency(SB), R8
	MOVD	$100, R10
	UDIV	R10, R8, R8  // R8 = ticks per 10ms
	CBZ	R8, timer_return  // Avoid divide by zero

	UDIV	R8, R9, R9  // R9 = elapsed intervals (10ms units)

	// Check against goroutine preemption threshold (5 intervals = 50ms)
	CMP	$5, R9
	BLT	check_thread_preempt  // Under goroutine threshold, check thread threshold

	// Elapsed >= goroutine threshold: signal async preemption needed
	MOVW	$1, R8
	MOVW	R8, ·NeedsAsyncPreempt(SB)

check_thread_preempt:
	// Check against thread preemption threshold (1 interval = 10ms)
	// Reduced from 10 intervals (100ms) to allow priests to be scheduled sooner
	// R9 still contains elapsed intervals
	CMP	$1, R9
	BLT	timer_return  // Under thread threshold, done

	// Elapsed >= thread threshold: signal thread preemption needed
	MOVW	$1, R8
	MOVW	R8, ·NeedsThreadPreempt(SB)
	B	timer_return

init_start_tick:
	// Initialize StartTick and LastSeenG for this thread
	// Read current counter: MRS X8, CNTVCT_EL0
	WORD	$0xD53BE048
	MOVD	R8, 320(R7)  // currentThread.StartTick = current tick
	MOVD	R4, 312(R7)  // currentThread.LastSeenG = current g
	// Fall through to timer_return
	B	timer_return

// ========================================================================
// Userspace thread preemption path
// ========================================================================
// When a userspace thread is running, its g pointer is in low memory.
// We skip the Go runtime preemption (g.preempt, g.stackguard0) because:
// 1. We can't safely access userspace memory from IRQ context
// 2. Userspace threads have their own Go runtime that handles cooperative preemption
// But we still need OS-level thread preemption so other priests get scheduled.
g_in_userspace:
	// Load currentThread pointer directly
	MOVD	main·CurrentThread(SB), R7  // *Thread
	CBZ	R7, timer_return  // currentThread is nil

	// Load StartTick: offset 320
	MOVD	320(R7), R8  // R8 = currentThread.StartTick
	CBZ	R8, userspace_init_tick  // Not initialized yet

	// Read current counter: MRS X9, CNTVCT_EL0
	WORD	$0xD53BE049

	// Calculate elapsed ticks
	SUB	R8, R9, R9  // R9 = current - start = elapsed

	// Convert to timer intervals (divide by ticks per 10ms)
	MOVD	·SystemTimerFrequency(SB), R8
	MOVD	$100, R10
	UDIV	R10, R8, R8  // R8 = ticks per 10ms
	CBZ	R8, timer_return  // Avoid divide by zero

	UDIV	R8, R9, R9  // R9 = elapsed intervals (10ms units)

	// Check against thread preemption threshold (1 interval = 10ms)
	// For userspace threads, we only care about OS-level thread preemption
	CMP	$1, R9
	BLT	timer_return  // Under threshold, done

	// Elapsed >= threshold: signal thread preemption needed
	MOVW	$1, R8
	MOVW	R8, ·NeedsThreadPreempt(SB)
	B	timer_return

userspace_init_tick:
	// Initialize StartTick for userspace thread
	// Read current counter: MRS X8, CNTVCT_EL0
	WORD	$0xD53BE048
	MOVD	R8, 320(R7)  // currentThread.StartTick = current tick
	B	timer_return

timer_return:
	// Set deadline flag to trigger bottom half processing
	// This allows deadline queue processing to happen in safe Go context
	// instead of from IRQ context
	MOVW	$1, R8
	MOVW	R8, main·DeadlinePending(SB)
	RET
