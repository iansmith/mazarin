// async_preempt.s - Bare-metal async preemption for ARM64
// Based on Go runtime's asyncPreempt mechanism
//
// This function is "injected" by the timer interrupt handler.
// The interrupt handler modifies ELR_EL1 to point here, with LR set to interrupted PC.
// This saves all registers, calls the scheduler, then returns to interrupted location.

.text
.align 2

// asyncPreemptBM is the bare-metal equivalent of runtime.asyncPreempt
// Entry state (set by timer interrupt handler):
//   - PC = asyncPreemptBM (via modified ELR_EL1)
//   - LR (x30) = interrupted PC (where to resume after preemption)
//   - SP = interrupted SP - 16 (timer handler allocated 16-byte frame)
//   - [SP+0] = old LR value (saved by timer handler, unused)
//   - [SP+8] = old FP value (saved by timer handler)
//   - All other registers (x0-x29, SIMD, flags) have interrupted values
//
// This function:
//   1. Saves ALL registers including current LR (= interrupted PC)
//   2. Calls timerPreempt() which calls runtime.Gosched()
//   3. When Gosched() returns (we're rescheduled), restores ALL registers
//   4. Returns to LR (interrupted PC)
//
.global asyncPreemptBM
asyncPreemptBM:
    // Save current LR (= interrupted PC, set by timer handler) BEFORE moving SP
    // We allocate 504 bytes (not 496) to include space for x27
    // ARM64 pre-indexed str has limited range (-256 to +255), so we do this in two steps
    sub sp, sp, #504            // SP -= 504
    str x30, [sp]               // Store LR at [SP+0]

    // Set up frame pointer for stack walking
    // Save current FP at [SP-8] (relative to entry SP, now [SP+504-8])
    str x29, [sp, #496]
    add x29, sp, #496            // FP = SP + 496 (points to saved FP location)

    // Save all general-purpose registers (R0-R27, skip R28=g, R29=FP, R30=LR already saved)
    // Use offsets from SP (which now points to saved LR)
    stp x0, x1, [sp, #8]         // Save x0, x1 at SP+8
    stp x2, x3, [sp, #24]        // Save x2, x3
    stp x4, x5, [sp, #40]        // Save x4, x5
    stp x6, x7, [sp, #56]        // Save x6, x7
    stp x8, x9, [sp, #72]        // Save x8, x9
    stp x10, x11, [sp, #88]      // Save x10, x11
    stp x12, x13, [sp, #104]     // Save x12, x13
    stp x14, x15, [sp, #120]     // Save x14, x15
    stp x16, x17, [sp, #136]     // Save x16, x17
    stp x19, x20, [sp, #152]     // Save x19, x20
    stp x21, x22, [sp, #168]     // Save x21, x22
    stp x23, x24, [sp, #184]     // Save x23, x24
    stp x25, x26, [sp, #200]     // Save x25, x26
    str x27, [sp, #216]          // Save x27 (CRITICAL: we interrupt at arbitrary points, not safe points!)
    // Note: R28 (g pointer) managed by runtime, updated by Gosched()
    // Note: R29 (FP) already saved above
    // Note: R30 (LR) already saved at SP+0

    // Save condition flags (NZCV)
    mrs x0, NZCV
    str x0, [sp, #224]           // Save NZCV at SP+224

    // Save floating-point status register
    mrs x0, FPSR
    str x0, [sp, #232]           // Save FPSR at SP+232

    // Save all floating-point/SIMD registers (V0-V31)
    // Each is 128 bits (16 bytes), save in pairs
    stp q0, q1, [sp, #240]       // V0, V1 at SP+240
    stp q2, q3, [sp, #272]       // V2, V3
    stp q4, q5, [sp, #304]       // V4, V5
    stp q6, q7, [sp, #336]       // V6, V7
    stp q8, q9, [sp, #368]       // V8, V9
    stp q10, q11, [sp, #400]     // V10, V11
    stp q12, q13, [sp, #432]     // V12, V13
    stp q14, q15, [sp, #464]     // V14, V15

    // Allocate more space for remaining SIMD registers
    // We've used up to offset 496, need 256 more bytes for V16-V31
    sub sp, sp, #256
    stp q16, q17, [sp, #0]       // V16, V17
    stp q18, q19, [sp, #32]      // V18, V19
    stp q20, q21, [sp, #64]      // V20, V21
    stp q22, q23, [sp, #96]      // V22, V23
    stp q24, q25, [sp, #128]     // V24, V25
    stp q26, q27, [sp, #160]     // V26, V27
    stp q28, q29, [sp, #192]     // V28, V29
    stp q30, q31, [sp, #224]     // V30, V31

    // Now call the Go preemption handler
    // This will call runtime.Gosched() which may switch goroutines
    // When it returns, we're back on this goroutine's stack
    bl main.timerPreempt

    // Restore SIMD registers (V16-V31 from temporary stack space)
    ldp q30, q31, [sp, #224]
    ldp q28, q29, [sp, #192]
    ldp q26, q27, [sp, #160]
    ldp q24, q25, [sp, #128]
    ldp q22, q23, [sp, #96]
    ldp q20, q21, [sp, #64]
    ldp q18, q19, [sp, #32]
    ldp q16, q17, [sp, #0]
    add sp, sp, #256             // Deallocate temporary SIMD space

    // Restore SIMD registers V0-V15 (SP now points to saved LR again)
    ldp q14, q15, [sp, #464]     // Restore with correct offsets
    ldp q12, q13, [sp, #432]
    ldp q10, q11, [sp, #400]
    ldp q8, q9, [sp, #368]
    ldp q6, q7, [sp, #336]
    ldp q4, q5, [sp, #304]
    ldp q2, q3, [sp, #272]
    ldp q0, q1, [sp, #240]

    // Restore floating-point status
    ldr x0, [sp, #232]
    msr FPSR, x0

    // Restore condition flags
    ldr x0, [sp, #224]
    msr NZCV, x0

    // Restore general-purpose registers (R0-R26, then R27 separately)
    ldp x25, x26, [sp, #200]
    ldp x23, x24, [sp, #184]
    ldp x21, x22, [sp, #168]
    ldp x19, x20, [sp, #152]
    ldp x16, x17, [sp, #136]
    ldp x14, x15, [sp, #120]
    ldp x12, x13, [sp, #104]
    ldp x10, x11, [sp, #88]
    ldp x8, x9, [sp, #72]
    ldp x6, x7, [sp, #56]
    ldp x4, x5, [sp, #40]
    ldp x2, x3, [sp, #24]
    ldp x0, x1, [sp, #8]

    // Restore frame pointer
    ldr x29, [sp, #496]

    // Restore x27 (needed because we interrupt at arbitrary points)
    ldr x27, [sp, #216]

    // CRITICAL: Load saved LR (interrupted PC) into x30
    // We do this BEFORE deallocating the frame
    ldr x30, [sp]                // x30 = interrupted PC

    // Deallocate our frame (504 bytes) + the 16 bytes pushCall allocated
    add sp, sp, #520

    // Jump to interrupted PC via ret (which jumps to x30/LR)
    // This restores execution at the interrupted location
    ret                          // Jump to LR (interrupted PC)
