
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

	// Increment timer IRQ counter
	MOVD	·TimerIRQCount(SB), R0
	ADD	$1, R0
	MOVD	R0, ·TimerIRQCount(SB)
	DSB	$15  // DSB SY - ensure store completes
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

	// Read current counter
	// MRS X9, CNTVCT_EL0
	WORD	$0xD53BE049

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

	CMP	R8, R9  // Computes R9 - R8 = current - deadline
	BLT	timer_return  // if current < deadline (negative), no preemption
	// Current >= thread deadline - fall through to signal preemption

thread_deadline_exceeded:
	// Current >= thread deadline: signal preemption
	MOVW	$1, R8
	MOVW	R8, ·NeedsThreadPreempt(SB)
	B	timer_return

init_deadlines:
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
	// Increment userspace path counter
	MOVD	·UserspacePathCount(SB), R0
	ADD	$1, R0
	MOVD	R0, ·UserspacePathCount(SB)

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
	// Increment g-changed counter
	MOVD	·UserspaceGChangedCount(SB), R8
	ADD	$1, R8
	MOVD	R8, ·UserspaceGChangedCount(SB)

	MOVD	R4, 320(R7)  // currentThread.LastSeenG = current g
	MOVD	R9, 336(R7)  // currentThread.GoroutineStart = current tick
	MOVD	·GoroutinePreemptTicks(SB), R8
	ADD	R9, R8, R8  // R8 = current + threshold = new deadline
	MOVD	R8, 352(R7)  // currentThread.GoroutinePreemptDeadline = new deadline
	B	userspace_check_thread_deadline  // Skip goroutine preemption check for new g

userspace_same_goroutine:
	// Same g - check goroutine deadline
	// NOTE: Go ARM64 CMP is swapped: CMP Rn, Rm computes Rm - Rn
	MOVD	352(R7), R8  // R8 = GoroutinePreemptDeadline
	CMP	R8, R9  // Computes R9 - R8 = current - deadline
	BLT	userspace_check_thread_deadline  // if current < deadline (negative), skip

	// Current >= goroutine deadline: signal async preemption needed
	// This will inject the priest's registered asyncPreempt (if registered)
	MOVW	$1, R8
	MOVW	R8, ·NeedsAsyncPreempt(SB)

	// Increment cooperative preemption counter
	MOVD	·UserspaceCoopPreemptCount(SB), R8
	ADD	$1, R8
	MOVD	R8, ·UserspaceCoopPreemptCount(SB)

	// ========================================================================
	// COOPERATIVE PREEMPTION: Set g.preempt = true and g.stackguard0 = stackPreempt
	// ========================================================================
	// Since the timer often fires during syscalls (SPSR shows EL1), async preemption
	// injection is skipped. To ensure preemption, we also enable cooperative
	// preemption by setting g.preempt and g.stackguard0 in the userspace g struct.
	//
	// The priest's Go runtime will check g.stackguard0 on function entry. If it
	// equals stackPreempt, the runtime yields to let other goroutines run.
	//
	// R4 = userspace g pointer (from earlier in this function)
	//
	// Use STTR (unprivileged store) to write to userspace memory.
	// STTR performs the store with EL0 permissions.

	// Set g.preempt = true (1)
	MOVD	·PreemptPreemptOffset(SB), R5
	ADD	R4, R5, R5  // R5 = &g.preempt
	MOVD	$1, R6
	// STTR X6, [X5] - unprivileged store of 1 to g.preempt
	// Encoding: size=11 (8-byte), opc=00, imm9=0, Rn=5, Rt=6
	WORD	$0xF80008A6  // sttr x6, [x5]

	// Set g.stackguard0 = stackPreempt
	MOVD	·PreemptStackGuard0Offset(SB), R5
	ADD	R4, R5, R5  // R5 = &g.stackguard0
	MOVD	·PreemptStackPreemptValue(SB), R6  // R6 = stackPreempt poison value
	// STTR X6, [X5] - unprivileged store of stackPreempt to g.stackguard0
	WORD	$0xF80008A6  // sttr x6, [x5]

userspace_check_thread_deadline:
	// Check thread deadline: if current >= deadline, signal preemption
	// NOTE: Go ARM64 CMP is swapped: CMP Rn, Rm computes Rm - Rn
	MOVD	344(R7), R8  // R8 = ThreadPreemptDeadline
	CMP	R8, R9  // Computes R9 - R8 = current - deadline
	BLT	timer_return  // if current < deadline (negative), no preemption

	// Current >= thread deadline: signal preemption needed
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
