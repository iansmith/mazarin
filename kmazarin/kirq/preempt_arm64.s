
//go:build !test_stubs

#include "textflag.h"

// UART base for debug output (high-memory mapped)
// Must match the kernel virtual address mapping for PL011 UART
#define UART_BASE 0xFFFFFFFF09000000

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
	MOVD	$UART_BASE, R2
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
	MOVD	$UART_BASE, R2
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
	MOVD	$UART_BASE, R2
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
	MOVD	$UART_BASE, R2
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
	MOVD	$UART_BASE, R2
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
	// Steps 6-7 REMOVED: No longer manipulate g.preempt or g.stackguard0
	// ========================================================================
	// We now use asyncPreempt injection (modifying ELR) instead of the
	// cooperative preemption path. This provides a unified approach for
	// both kmazarin and priest goroutines:
	// 1. Timer tracks elapsed time and sets NeedsAsyncPreempt flag
	// 2. Exception return path modifies ELR to inject asyncPreempt
	// 3. asyncPreempt saves state and calls the Go scheduler
	//
	// Benefits:
	// - Kernel doesn't need to touch Go runtime g struct
	// - Same mechanism works for kmazarin and priests
	// - Cleaner separation between kernel and Go runtime

	// ========================================================================
	// Step 6: Check for g change and track preemption time
	// ========================================================================
	// R4 = current g pointer (validated above)

	// Load currentThread pointer directly (no index calculation needed)
	MOVD	main·CurrentThread(SB), R7  // *Thread
	CBNZ	R7, thread_not_nil
	B	timer_return  // currentThread is nil
thread_not_nil:

	// ========================================================================
	// DEADLINE-BASED PREEMPTION (no division in hot path)
	// Thread struct offsets (must match Go code - verified by checkThreadOffsets):
	//   LastSeenG: 320
	//   StartTick: 328
	//   GoroutineStart: 336
	//   ThreadPreemptDeadline: 344
	//   GoroutinePreemptDeadline: 352
	// ========================================================================

	// Read current counter FIRST before any debug output
	// MRS X9, CNTVCT_EL0
	WORD	$0xD53BE049

	// DEBUG: Mark that we reached deadline check
	MOVD	$UART_BASE, R8  // Use high-memory mapped UART
	MOVW	$'@', R10
	MOVB	R10, (R8)

	// Load LastSeenG: offset 320
	MOVD	320(R7), R8  // R8 = currentThread.LastSeenG

	// Compare with current g (R4)
	CMP	R4, R8
	BEQ	same_goroutine

	// ========================================================================
	// G changed! Go runtime switched goroutines internally.
	// Reset GoroutinePreemptDeadline for the new goroutine.
	// But DON'T reset ThreadPreemptDeadline - we still want to track total
	// thread time for OS-level preemption of priests waiting in ready queue.
	// ========================================================================
	MOVD	R4, 320(R7)  // currentThread.LastSeenG = current g

	// Reset GoroutineStart and deadline for the new goroutine
	MOVD	R9, 336(R7)  // currentThread.GoroutineStart = current tick
	MOVD	·GoroutinePreemptTicks(SB), R8
	ADD	R9, R8, R8  // R8 = current + threshold = new deadline
	MOVD	R8, 352(R7)  // currentThread.GoroutinePreemptDeadline = new deadline

	// Fall through to check thread preemption only
	B	check_thread_deadline

same_goroutine:
	// Same g - check both goroutine and thread deadlines
	// R9 = current counter

	// Check if deadlines are initialized (StartTick != 0)
	MOVD	328(R7), R8  // R8 = currentThread.StartTick
	CBZ	R8, init_deadlines  // Not initialized yet

	// Check goroutine deadline: if current >= deadline, signal preemption
	// NOTE: Go ARM64 CMP is swapped: CMP Rn, Rm computes Rm - Rn
	MOVD	352(R7), R8  // R8 = GoroutinePreemptDeadline
	CMP	R8, R9  // Computes R9 - R8 = current - deadline
	BLT	check_thread_deadline  // if current < deadline (negative result), skip

	// Current >= goroutine deadline: signal async preemption needed
	MOVW	$1, R8
	MOVW	R8, ·NeedsAsyncPreempt(SB)

check_thread_deadline:
	// Check thread deadline: if current >= deadline, signal preemption
	// NOTE: Go ARM64 CMP is swapped: CMP Rn, Rm computes Rm - Rn
	MOVD	344(R7), R8  // R8 = ThreadPreemptDeadline

	// DEBUG: Every 20 ticks, print thread deadline status
	// Format: {C:xx D:xx} where C=current/1M, D=deadline/1M
	MOVD	·TimerIRQCount(SB), R10
	MOVD	$20, R11
	UDIV	R11, R10, R12  // R12 = count / 20
	MUL	R11, R12, R12   // R12 = (count/20)*20
	CMP	R10, R12
	BNE	skip_deadline_debug

	// Print deadline debug: {P<pid> C<current_M> D<deadline_M>}
	MOVD	$UART_BASE, R10
	MOVW	$'{', R11
	MOVB	R11, (R10)
	MOVW	$'P', R11
	MOVB	R11, (R10)

	// Print current thread PID (offset 8 in Thread struct)
	MOVW	8(R7), R11  // R11 = thread.PID (uint32 at offset 8)
	AND	$0xF, R11
	CMP	$10, R11
	BLT	pid_digit
	ADD	$('A'-10), R11
	B	pid_out
pid_digit:
	ADD	$'0', R11
pid_out:
	MOVB	R11, (R10)

	MOVW	$' ', R11
	MOVB	R11, (R10)
	MOVW	$'C', R11
	MOVB	R11, (R10)

	// Print current time / 1M (roughly ms at 1MHz or ticks/1000000)
	MOVD	$1000000, R11
	UDIV	R11, R9, R12  // R12 = current / 1M
	// Print 2 hex digits of R12
	LSR	$4, R12, R13
	AND	$0xF, R13
	CMP	$10, R13
	BLT	cur_digit1
	ADD	$('A'-10), R13
	B	cur_out1
cur_digit1:
	ADD	$'0', R13
cur_out1:
	MOVB	R13, (R10)
	AND	$0xF, R12
	CMP	$10, R12
	BLT	cur_digit2
	ADD	$('A'-10), R12
	B	cur_out2
cur_digit2:
	ADD	$'0', R12
cur_out2:
	MOVB	R12, (R10)

	MOVW	$' ', R11
	MOVB	R11, (R10)
	MOVW	$'D', R11
	MOVB	R11, (R10)

	// Print deadline / 1M
	MOVD	$1000000, R11
	UDIV	R11, R8, R12  // R12 = deadline / 1M
	LSR	$4, R12, R13
	AND	$0xF, R13
	CMP	$10, R13
	BLT	dl_digit1
	ADD	$('A'-10), R13
	B	dl_out1
dl_digit1:
	ADD	$'0', R13
dl_out1:
	MOVB	R13, (R10)
	AND	$0xF, R12
	CMP	$10, R12
	BLT	dl_digit2
	ADD	$('A'-10), R12
	B	dl_out2
dl_digit2:
	ADD	$'0', R12
dl_out2:
	MOVB	R12, (R10)

	MOVW	$'}', R11
	MOVB	R11, (R10)

skip_deadline_debug:
	// Re-load deadline since we clobbered R8
	MOVD	344(R7), R8  // R8 = ThreadPreemptDeadline

	CMP	R8, R9  // Computes R9 - R8 = current - deadline
	BLT	timer_return  // if current < deadline (negative), no preemption
	// Current >= thread deadline - fall through to signal preemption

thread_deadline_exceeded:
	// Current >= thread deadline: print '#' debug marker and signal preemption
	MOVD	$UART_BASE, R10
	MOVW	$'#', R11
	MOVB	R11, (R10)
	MOVW	$1, R8
	MOVW	R8, ·NeedsThreadPreempt(SB)
	B	timer_return

init_deadlines:
	// DEBUG: Mark that we're initializing deadlines
	MOVD	$UART_BASE, R10
	MOVW	$'I', R11
	MOVB	R11, (R10)
	// Deadlines not initialized - initialize them
	// R9 = current counter
	MOVD	R9, 328(R7)  // currentThread.StartTick = current tick
	MOVD	R9, 336(R7)  // currentThread.GoroutineStart = current tick
	MOVD	R4, 320(R7)  // currentThread.LastSeenG = current g

	// Set deadlines: current + threshold
	MOVD	·ThreadPreemptTicks(SB), R8
	ADD	R9, R8, R8
	MOVD	R8, 344(R7)  // ThreadPreemptDeadline = current + threshold

	MOVD	·GoroutinePreemptTicks(SB), R8
	ADD	R9, R8, R8
	MOVD	R8, 352(R7)  // GoroutinePreemptDeadline = current + threshold
	B	timer_return

// ========================================================================
// Userspace thread preemption path (DEADLINE-BASED)
// ========================================================================
// When a userspace thread is running, its g pointer is in low memory.
// We skip the Go runtime preemption (g.preempt, g.stackguard0) because:
// 1. We can't safely access userspace memory from IRQ context
// 2. Userspace threads have their own Go runtime that handles cooperative preemption
// But we still need OS-level thread preemption so other priests get scheduled.
g_in_userspace:
	// DEBUG: Print 'U' to show we're in userspace path
	MOVD	$UART_BASE, R10
	MOVW	$'U', R11
	MOVB	R11, (R10)

	// Load currentThread pointer directly
	MOVD	main·CurrentThread(SB), R7  // *Thread
	CBZ	R7, timer_return  // currentThread is nil

	// Read current counter: MRS X9, CNTVCT_EL0
	WORD	$0xD53BE049

	// Load ThreadPreemptDeadline: offset 344
	MOVD	344(R7), R8  // R8 = currentThread.ThreadPreemptDeadline
	CBZ	R8, userspace_init_deadlines  // Not initialized yet

	// ========================================================================
	// Track g changes for userspace threads too!
	// We CAN compare the g pointer value (R4) to detect goroutine switches.
	// We just can't DEREFERENCE it (to check g.atomicstatus) from IRQ context.
	// When g changes, reset the goroutine deadline to give the new g fair time.
	// ========================================================================
	MOVD	320(R7), R8  // R8 = currentThread.LastSeenG
	CMP	R4, R8
	BEQ	userspace_same_goroutine

	// G changed! Reset GoroutinePreemptDeadline for the new goroutine.
	MOVD	R4, 320(R7)  // currentThread.LastSeenG = current g
	MOVD	R9, 336(R7)  // currentThread.GoroutineStart = current tick
	MOVD	·GoroutinePreemptTicks(SB), R8
	ADD	R9, R8, R8  // R8 = current + threshold = new deadline
	MOVD	R8, 352(R7)  // currentThread.GoroutinePreemptDeadline = new deadline
	B	userspace_check_thread_deadline  // Skip goroutine preemption check for new g

userspace_same_goroutine:
	// Same g - check goroutine deadline
	// Check goroutine deadline: if current >= deadline, signal preemption
	// NOTE: Go ARM64 CMP is swapped: CMP Rn, Rm computes Rm - Rn
	MOVD	352(R7), R8  // R8 = GoroutinePreemptDeadline
	CMP	R8, R9  // Computes R9 - R8 = current - deadline
	BLT	userspace_check_thread_deadline  // if current < deadline (negative), skip

	// Current >= goroutine deadline: signal async preemption needed
	// This will inject the priest's registered asyncPreempt (if registered)
	MOVW	$1, R8
	MOVW	R8, ·NeedsAsyncPreempt(SB)

userspace_check_thread_deadline:
	// Check thread deadline: if current >= deadline, signal preemption
	// NOTE: Go ARM64 CMP is swapped: CMP Rn, Rm computes Rm - Rn
	MOVD	344(R7), R8  // R8 = ThreadPreemptDeadline
	CMP	R8, R9  // Computes R9 - R8 = current - deadline
	BLT	timer_return  // if current < deadline (negative), no preemption

	// Current >= thread deadline: print '#' and signal preemption needed
	MOVD	$UART_BASE, R10
	MOVW	$'#', R11
	MOVB	R11, (R10)
	MOVW	$1, R8
	MOVW	R8, ·NeedsThreadPreempt(SB)
	B	timer_return

userspace_init_deadlines:
	// Initialize deadlines for userspace thread
	// R9 = current counter
	MOVD	R9, 328(R7)  // currentThread.StartTick = current tick
	MOVD	R9, 336(R7)  // currentThread.GoroutineStart = current tick

	// Set deadlines: current + threshold
	MOVD	·ThreadPreemptTicks(SB), R8
	ADD	R9, R8, R8
	MOVD	R8, 344(R7)  // ThreadPreemptDeadline = current + threshold

	MOVD	·GoroutinePreemptTicks(SB), R8
	ADD	R9, R8, R8
	MOVD	R8, 352(R7)  // GoroutinePreemptDeadline = current + threshold
	B	timer_return

timer_return:
	// Set deadline flag to trigger bottom half processing
	// This allows deadline queue processing to happen in safe Go context
	// instead of from IRQ context
	MOVW	$1, R8
	MOVW	R8, main·DeadlinePending(SB)
	RET
