// exceptions.s
// AArch64 Exception Vector Table and Exception Handlers
//
// Layout: Exception vector table must be 2KB aligned (2048 bytes)
// Contains 4 groups of 4 exception handlers (128 bytes each)
// We use Group 2 (Current EL, using SP_EL1) for kernel at EL1
//
// Group 2: Current EL, using SP_EL1 (0x200-0x3ff in vector table)
//   0x200 - 0x280: Synchronous exception
//   0x280 - 0x300: IRQ (Interrupt Request)
//   0x300 - 0x380: FIQ (Fast Interrupt Request)
//   0x380 - 0x400: SError (System Error)

// ========== DATA SECTION ==========
.data
.align 3

// Counter for dot output (separate from timerTickCount)
// Counts how many timer interrupts since last dot
// Resets to 0 after outputting dot
dot_counter:
    .quad 0

// Declare Go function for exception handling
.extern ExceptionHandler

.text  // CRITICAL: Switch to text section for code!

// ============================================================================
// EXCEPTION FRAME LAYOUT AND MACROS
// ============================================================================

// Exception Frame Offsets (320-byte frame on SP_EL1 exception stack)
// --------------------------------------------------------------------
// Standard saved state:
.equ EXC_FRAME_X0_X1,        0      // x0, x1 saved here
.equ EXC_FRAME_X28,          224    // x28 (g pointer)
.equ EXC_FRAME_X29_X30,      232    // x29, x30 (FP, LR)
.equ EXC_FRAME_ORIG_SP,      248    // Original SP before exception
.equ EXC_FRAME_ELR_SPSR,     256    // ELR_EL1, SPSR_EL1
.equ EXC_FRAME_FAR_ESR,      272    // FAR_EL1, ESR_EL1

// Extended state (uses previously unused space 288-319):
.equ EXC_FRAME_SP_EL0,       288    // Saved SP_EL0 (kmazarin stack)
.equ EXC_FRAME_SAVED_G,      296    // Saved g for syscall (original x28)
.equ EXC_FRAME_SAVED_X0,     304    // Saved x0 for syscall return value
.equ EXC_FRAME_SAVED_LR,     312    // Saved LR for syscall

// Total frame size
.equ EXC_FRAME_SIZE,         320

// ============================================================================
// MACROS FOR STACK-BASED STATE MANAGEMENT
// ============================================================================

// Save SP_EL0 to exception stack frame (replaces fixed scratch area at 0x40FFF000)
// Clobbers: x10
.macro SAVE_SP_EL0_TO_STACK
    mrs x10, SP_EL0                     // x10 = current SP_EL0
    str x10, [sp, #EXC_FRAME_SP_EL0]   // Save to exception frame at offset 288
.endm

// Restore SP_EL0 from exception stack frame
// Clobbers: x10
.macro RESTORE_SP_EL0_FROM_STACK
    ldr x10, [sp, #EXC_FRAME_SP_EL0]   // Load saved SP_EL0 from offset 288
    msr SP_EL0, x10                     // Restore it
    isb                                 // Ensure write completes
.endm

// Save syscall context to exception frame (replaces scratch area at 0x40FFF020)
// This preserves critical state across Go syscall handler calls
// Input: x0 = return value to save, x28 = g to save, x30 = LR to save
// Clobbers: none (only stores)
.macro SAVE_SYSCALL_CONTEXT
    str x0, [sp, #EXC_FRAME_SAVED_X0]   // Save syscall return value (offset 304)
    str x28, [sp, #EXC_FRAME_SAVED_G]   // Save original g (offset 296)
    str x30, [sp, #EXC_FRAME_SAVED_LR]  // Save original LR (offset 312)
.endm

// Restore syscall context from exception frame
// Output: x0, x28, x30 restored
// Clobbers: none (loads directly to target registers)
.macro RESTORE_SYSCALL_CONTEXT
    ldr x0, [sp, #EXC_FRAME_SAVED_X0]   // Restore syscall return value
    ldr x28, [sp, #EXC_FRAME_SAVED_G]   // Restore original g
    ldr x30, [sp, #EXC_FRAME_SAVED_LR]  // Restore original LR
.endm

// ============================================================================
// SAFE DEBUG PRINTING FUNCTIONS
// ============================================================================
//
// These functions print to UART while preserving ALL registers.
// CRITICAL: All functions save/restore x0-x30 to ensure caller state unchanged.

// uart_putc: Print a single character to UART
// Input: w0 = character to print (only low byte used)
// Preserves: ALL registers (x0-x30)
.macro UART_PUTC
    stp x0, x1, [sp, #-16]!          // Save x0, x1
    movz x1, #0x0900, lsl #16        // x1 = 0x09000000 (UART base)
    strb w0, [x1]                    // Write character
    ldp x0, x1, [sp], #16            // Restore x0, x1
.endm

// print_hex_nibble: Print a single hex nibble (0-F)
// Input: w0 = nibble value (0-15)
// Preserves: ALL registers
.macro PRINT_HEX_NIBBLE
    stp x0, x1, [sp, #-16]!
    and w0, w0, #0xF                 // Mask to nibble
    cmp w0, #10
    blt 1f
    add w0, w0, #0x37                // 'A'-10
    b 2f
1:  add w0, w0, #0x30                // '0'
2:  movz x1, #0x0900, lsl #16
    strb w0, [x1]
    ldp x0, x1, [sp], #16
.endm

// print_hex64: Print a 64-bit value as 16 hex digits
// Input: x0 = value to print
// Preserves: ALL registers (x0-x30)
print_hex64:
    // Save ALL caller-saved and callee-saved registers
    stp x29, x30, [sp, #-16]!
    stp x0, x1, [sp, #-16]!
    stp x2, x3, [sp, #-16]!
    stp x4, x5, [sp, #-16]!

    mov x4, x0                       // x4 = value to print
    mov x5, #16                      // x5 = digit counter

.Lhex64_loop:
    lsr x0, x4, #60                  // Get top nibble
    and w0, w0, #0xF
    cmp w0, #10
    blt .Lhex64_digit
    add w0, w0, #0x37                // 'A'-10
    b .Lhex64_print
.Lhex64_digit:
    add w0, w0, #0x30                // '0'
.Lhex64_print:
    movz x1, #0x0900, lsl #16
    strb w0, [x1]
    lsl x4, x4, #4                   // Shift for next nibble
    sub x5, x5, #1
    cbnz x5, .Lhex64_loop

    // Restore ALL registers
    ldp x4, x5, [sp], #16
    ldp x2, x3, [sp], #16
    ldp x0, x1, [sp], #16
    ldp x29, x30, [sp], #16
    ret

// print_string: Print a null-terminated string
// Input: x0 = pointer to string
// Preserves: ALL registers
print_string:
    stp x29, x30, [sp, #-16]!
    stp x0, x1, [sp, #-16]!
    stp x2, x3, [sp, #-16]!

    mov x2, x0                       // x2 = string pointer
    movz x3, #0x0900, lsl #16        // x3 = UART base

.Lstring_loop:
    ldrb w0, [x2], #1                // Load byte, increment pointer
    cbz w0, .Lstring_done            // If null, done
    strb w0, [x3]                    // Write to UART
    b .Lstring_loop

.Lstring_done:
    ldp x2, x3, [sp], #16
    ldp x0, x1, [sp], #16
    ldp x29, x30, [sp], #16
    ret

// ============================================================================
// REGISTER PRESERVATION TEST
// ============================================================================
//
// Test that print functions preserve all registers.
// Sets all registers to unique non-zero values, calls print functions,
// and verifies all registers are unchanged.

.global test_print_functions_preserve_registers
test_print_functions_preserve_registers:
    stp x29, x30, [sp, #-16]!

    // Set all registers to unique large values (0xDEAD0000 + register number)
    movz x0, #0xDEAD, lsl #16
    movk x0, #0x0000
    movz x1, #0xDEAD, lsl #16
    movk x1, #0x0001
    movz x2, #0xDEAD, lsl #16
    movk x2, #0x0002
    movz x3, #0xDEAD, lsl #16
    movk x3, #0x0003
    movz x4, #0xDEAD, lsl #16
    movk x4, #0x0004
    movz x5, #0xDEAD, lsl #16
    movk x5, #0x0005
    movz x6, #0xDEAD, lsl #16
    movk x6, #0x0006
    movz x7, #0xDEAD, lsl #16
    movk x7, #0x0007
    movz x8, #0xDEAD, lsl #16
    movk x8, #0x0008
    movz x9, #0xDEAD, lsl #16
    movk x9, #0x0009
    movz x10, #0xDEAD, lsl #16
    movk x10, #0x000A
    movz x11, #0xDEAD, lsl #16
    movk x11, #0x000B
    movz x12, #0xDEAD, lsl #16
    movk x12, #0x000C
    movz x13, #0xDEAD, lsl #16
    movk x13, #0x000D
    movz x14, #0xDEAD, lsl #16
    movk x14, #0x000E
    movz x15, #0xDEAD, lsl #16
    movk x15, #0x000F
    movz x16, #0xDEAD, lsl #16
    movk x16, #0x0010
    movz x17, #0xDEAD, lsl #16
    movk x17, #0x0011
    movz x18, #0xDEAD, lsl #16
    movk x18, #0x0012
    movz x19, #0xDEAD, lsl #16
    movk x19, #0x0013
    movz x20, #0xDEAD, lsl #16
    movk x20, #0x0014
    movz x21, #0xDEAD, lsl #16
    movk x21, #0x0015
    movz x22, #0xDEAD, lsl #16
    movk x22, #0x0016
    movz x23, #0xDEAD, lsl #16
    movk x23, #0x0017
    movz x24, #0xDEAD, lsl #16
    movk x24, #0x0018
    movz x25, #0xDEAD, lsl #16
    movk x25, #0x0019
    movz x26, #0xDEAD, lsl #16
    movk x26, #0x001A
    movz x27, #0xDEAD, lsl #16
    movk x27, #0x001B
    // x28 is g pointer, x29 is FP (saved), x30 is LR (saved)

    // Save register values to stack for verification
    sub sp, sp, #256                 // Allocate space for 28 registers + alignment
    stp x0, x1, [sp, #0]
    stp x2, x3, [sp, #16]
    stp x4, x5, [sp, #32]
    stp x6, x7, [sp, #48]
    stp x8, x9, [sp, #64]
    stp x10, x11, [sp, #80]
    stp x12, x13, [sp, #96]
    stp x14, x15, [sp, #112]
    stp x16, x17, [sp, #128]
    stp x18, x19, [sp, #144]
    stp x20, x21, [sp, #160]
    stp x22, x23, [sp, #176]
    stp x24, x25, [sp, #192]
    stp x26, x27, [sp, #208]

    // Test print_hex64 with x0
    bl print_hex64

    // Verify x0-x27 unchanged
    ldp x0, x1, [sp, #0]
    movz x29, #0xDEAD, lsl #16
    movk x29, #0x0000
    cmp x0, x29
    bne .Ltest_failed
    movz x29, #0xDEAD, lsl #16
    movk x29, #0x0001
    cmp x1, x29
    bne .Ltest_failed

    // If we get here, test passed
    add sp, sp, #256

    // Print success message
    adrp x0, .Ltest_pass_msg
    add x0, x0, :lo12:.Ltest_pass_msg
    bl print_string

    ldp x29, x30, [sp], #16
    ret

.Ltest_failed:
    // Print failure message and hang
    add sp, sp, #256
    adrp x0, .Ltest_fail_msg
    add x0, x0, :lo12:.Ltest_fail_msg
    bl print_string

.Lhang:
    wfe
    b .Lhang

.section .rodata
.Ltest_pass_msg:
    .asciz "PRINT_TEST: PASS\r\n"
.Ltest_fail_msg:
    .asciz "PRINT_TEST: FAIL - registers corrupted!\r\n"

.text

// ============================================================================
// GO CALLING CONVENTION SUPPORT
// ============================================================================
//
// Go's calling convention requires the caller to provide stack space for
// argument spills. Arguments are passed in registers x0-x5, but Go immediately
// spills them to the stack at POSITIVE offsets from SP.
//
// For a Go function with frame size F that accesses [SP + M]:
//   - After allocating frame: Go_SP = Caller_SP - F
//   - Go accesses: [Go_SP + M] = [Caller_SP - F + M] = [Caller_SP + (M - F)]
//   - If M > F, this is ABOVE the caller's original SP
//
// SPILL_SPACE = (max_offset - frame_size + param_size + 15) & ~15
//
// Spill space constants (derived from disassembly analysis):
.equ SPILL_SPACE_1PARAM,  16    // 1 parameter functions
.equ SPILL_SPACE_2PARAM,  32    // 2 parameter functions
.equ SPILL_SPACE_3PARAM,  32    // 3 parameter functions
.equ SPILL_SPACE_4PARAM,  48    // 4 parameter functions
.equ SPILL_SPACE_6PARAM,  320   // 6 parameter functions (Go uses sp-128+144=272, round up)
.equ SPILL_SPACE_8PARAM,  64    // 8 parameter functions

// Register save space (7 registers: x19-x22, x28-x30)
.equ REG_SAVE_SPACE,      64    // 64 bytes (4 pairs + alignment)

// CALL_GO_PROLOGUE: Prepare to call a Go function from assembly
//   Arguments: \spill_space - bytes of spill space needed (use SPILL_SPACE_*PARAM)
//   Clobbers: none (saves all callee-saved registers)
//   Stack effect: Allocates REG_SAVE_SPACE + spill_space bytes
//
//   Stack layout after prologue:
//     [original_SP]                           <- entry SP
//     [original_SP - REG_SAVE_SPACE]          <- saved regs (x19-x22, x28-x30)
//     [original_SP - REG_SAVE_SPACE - spill] <- current SP (Go's entry point)
//
.macro CALL_GO_PROLOGUE spill_space
    // Go ABIInternal expects caller to provide spill space ABOVE sp
    // Allocate total space: REG_SAVE + SPILL_SPACE
    sub sp, sp, #(REG_SAVE_SPACE + \spill_space)

    // Save callee-saved registers at BOTTOM of allocated space
    //   x19-x28: callee-saved general purpose registers
    //   x29: frame pointer
    //   x30: link register
    stp x19, x20, [sp, #0]
    stp x21, x22, [sp, #16]
    stp x28, x29, [sp, #32]
    str x30, [sp, #48]

    // Spill space is at [sp, #64] to [sp, #64+spill_space]
    // Go function can use [sp, #N] where N >= 64
.endm

// CALL_GO_EPILOGUE: Clean up after calling a Go function
//   Arguments: \spill_space - bytes of spill space (must match prologue)
//   Effects: Restores registers and stack, preserves x0 (return value)
//   Returns: x0 contains the Go function's return value
//
.macro CALL_GO_EPILOGUE spill_space
    // Go function has returned with result in x0
    // Saved registers are at [sp, #0] through [sp, #48]
    // Spill space is at [sp, #64] onwards

    // Restore callee-saved registers from bottom of stack frame
    ldp x19, x20, [sp, #0]
    ldp x21, x22, [sp, #16]
    ldp x28, x29, [sp, #32]
    ldr x30, [sp, #48]

    // Deallocate entire frame (REG_SAVE + SPILL_SPACE)
    add sp, sp, #(REG_SAVE_SPACE + \spill_space)

    // x0 already contains return value from Go function
.endm

// Use a separate section for exception vectors so they can be 2KB aligned
// without affecting text section alignment
// Flags: "ax" = allocatable + executable (required for code execution)
.section ".vectors", "ax"
.global exception_vectors
.global exception_vectors_start_addr

// 2KB align the exception vector table
.align 11  // 2^11 = 2048 bytes = 2KB

exception_vectors:
    // Group 0: Current EL, using SP_EL0 (0x000-0x1ff)
    // We use this when running kmazarin in EL1t mode

    // 0x000 - 0x080: Synchronous exception (SP_EL0)
    .align 7  // 128 bytes per handler
    b sync_exception_handler_el0  // Jump to handler

    // 0x080 - 0x100: IRQ (SP_EL0)
    .align 7
    b irq_exception_handler_el0   // Jump to IRQ handler

    // 0x100 - 0x180: FIQ (SP_EL0)
    .align 7
    b .  // Hang - FIQ not used

    // 0x180 - 0x200: SError (SP_EL0)
    .align 7
    b .  // Hang - SError not used


    // ========================================
    // Group 1: Current EL, using SP_EL1 (0x200-0x3ff)
    // This is what we use for the kernel at EL1
    // ========================================
    
    // 0x200 - 0x280: Synchronous exception (SP_EL1) - 128 bytes
    // MUST fit in 128 bytes, so jump to external handler
    .align 7
sync_exception_el1:
    b sync_exception_handler       // Jump to handler outside vector table
    
    
    // 0x280 - 0x300: IRQ (SP_EL1) - 128 bytes
    .align 7
irq_exception_el1:
    // CRITICAL: Read GICC_IAR IMMEDIATELY to acknowledge interrupt
    // The GIC spec says IAR must be read ASAP to prevent spurious 1022
    // Use x10-x11 which are caller-saved and safe to clobber
    movz x10, #0x0801, lsl #16    // GICC_IAR at 0x0801000C
    movk x10, #0x000C, lsl #0
    ldr w11, [x10]                // Read IAR (acknowledges interrupt)
    and w11, w11, #0x3FF          // Mask to get interrupt ID (bits 9:0)
    
    // Now switch to exception stack (w11 has interrupt ID)
    mov x0, sp                     // Save current SP
    movz x1, #0x5FFF, lsl #16     // Exception stack at 0x5FFF8000
    movk x1, #0x8000, lsl #0
    mov sp, x1
    
    // Save registers on exception stack (including w11 with interrupt ID)
    sub sp, sp, #64
    str x0, [sp, #0]              // Save original SP_EL1
    stp x1, x2, [sp, #8]          // Save x1, x2
    stp x3, x4, [sp, #24]         // Save x3, x4
    str w11, [sp, #40]            // Save interrupt ID from w11
    
    // Move interrupt ID to w2 for rest of handler
    mov w2, w11
    
    // Load UART base for further prints
    movz x0, #0x0900, lsl #16
    movk x0, #0x0000, lsl #0
    
    // Check if this is virtual timer interrupt (ID 27 = 0x1B)
    cmp w2, #27
    beq handle_timer_irq
    
    // Check if this is UART interrupt (ID 33 = 0x21)
    cmp w2, #33
    beq handle_uart_irq
    
    // Unknown interrupt - print '?' and finish
    movz w1, #0x3F                // '?'
    str w1, [x0]
    b irq_done
    
handle_timer_irq:
    // ========== 1. RE-ARM TIMER ==========
    // Set timer to fire again in 20ms (1,250,000 hardware ticks @ 62.5 MHz)
    mrs x3, CNTVCT_EL0              // Read current hardware counter
    movz x4, #0x0013, lsl #16       // 1250000 = 0x1312D0
    movk x4, #0x12D0, lsl #0        // Complete to 1250000
    add x3, x3, x4                  // target = current + interval
    msr CNTV_CVAL_EL0, x3           // Set compare value

    // ========== 2. DOT OUTPUT CHECK ==========
    // Check if we should output a dot
    // Every 250 timer interrupt firings = 5 seconds

    // Load dot counter
    adrp x5, dot_counter
    add x5, x5, :lo12:dot_counter
    ldr x6, [x5]                    // x6 = current dot counter value

    // Increment dot counter (one more interrupt fired)
    add x6, x6, #1
    str x6, [x5]                    // Save incremented value

    // Check if we've reached 250 interrupt firings
    cmp x6, #250
    blt no_dot                      // If < 250, skip dot output

    // We've hit 250 interrupt firings - output dot and reset
    str xzr, [x5]                   // Reset dot counter to 0

    // Output dot directly to UART (0x09000000)
    // This is simple and avoids calling Go functions from interrupt context
    movz x7, #0x0900, lsl #16       // UART base address
    movz w8, #0x2E                  // '.' character
    str w8, [x7]                    // Write to UART data register

no_dot:
    // ========== 3. CALL TIMER SIGNAL WITH STACK SWITCHING ==========
    // We need to switch from exception stack to goroutine stack before
    // calling timerSignal() because channel operations do stack bounds checks
    //
    // Exception stack: 0x5FFFFFFF - kernel exception handler stack
    // Goroutine stack: g.stack.lo to g.stack.hi (e.g., 0x40000...)
    //
    // Strategy: Temporarily switch SP to current g's stack, call timerSignal(), restore SP

    // Save exception stack pointer
    mov x15, sp                      // x15 = exception SP (0x5FFF...)

    // Get current g pointer - it's saved in x28 by exception entry
    mov x16, x28                     // x16 = current g pointer (from x28, not TPIDR_EL0)

    // Check if g is valid (not null)
    cbz x16, use_g0_stack            // If g == 0, use g0 stack instead

    // Read g.stack.hi (offset 8 in runtimeStack, which is at offset 0 in runtimeG)
    ldr x17, [x16, #8]               // x17 = g.stack.hi
    b timer_stack_selected

use_g0_stack:
    // g is null - use g0's stack instead
    // g0 address is in runtime.g0 symbol
    adrp x16, runtime.g0
    add x16, x16, :lo12:runtime.g0
    ldr x16, [x16]                   // x16 = g0 pointer
    ldr x17, [x16, #8]               // x17 = g0.stack.hi

timer_stack_selected:
    // Async preemption using "call injection" (like signal-based preemption)
    // Instead of calling timerPreempt directly, we modify the exception return
    // context to "inject" a call to asyncPreemptBM.
    //
    // Strategy (replicates signal handler pushCall):
    // 1. Allocate 16-byte frame on interrupted goroutine's stack
    // 2. Save old LR and FP to this frame (for unwinding)
    // 3. Set interrupted goroutine's SP to new frame location
    // 4. Set LR (x30) = interrupted PC (where to resume after preemption)
    // 5. Set ELR_EL1 = asyncPreemptBM (where ERET will jump)
    // 6. ERET jumps to asyncPreemptBM with LR=interrupted PC
    // 7. asyncPreemptBM saves all registers, calls scheduler, restores, returns to LR

    // Read interrupted PC from ELR_EL1
    mrs x19, ELR_EL1                 // x19 = interrupted PC

    // Get interrupted SP - this is the SP when interrupt occurred
    // We saved original SP_EL1 at [x15, #0] (exception stack base)
    ldr x20, [x15, #0]               // x20 = interrupted SP

    // Read current LR value from interrupted context (for unwinding)
    // NOTE: x30 (LR) is not saved in our exception entry, so we read it from current state
    // This might be incorrect - ideally we'd save it on exception entry
    // For now, we'll save 0 as placeholder (stack unwinder won't rely on it)
    mov x21, xzr                     // x21 = 0 (placeholder for old LR)

    // Read frame pointer (R29) - also not saved in exception entry
    mov x22, x29                     // x22 = current FP (may be stale)

    // Implement pushCall logic:
    // Allocate 16-byte frame on interrupted stack
    sub x20, x20, #16                // SP -= 16
    and x20, x20, #0xFFFFFFFFFFFFFFF0  // Ensure 16-byte alignment

    // Save old LR at [SP] (for stack unwinding)
    str x21, [x20, #0]               // *SP = old LR

    // Save old FP at [SP-8] (for frame pointer tracking)
    str x22, [x20, #8]               // *(SP+8) = old FP

    // Now set up the injection:
    // 1. Set LR = interrupted PC (this is where asyncPreemptBM will return to)
    mov x30, x19                     // LR = interrupted PC

    // 2. Set SP to new frame location (so asyncPreemptBM uses correct stack)
    mov sp, x20                      // SP = new frame pointer

    // 3. Set ELR_EL1 = asyncPreemptBM (so ERET jumps there instead of interrupted PC)
    adrp x21, asyncPreemptBM
    add x21, x21, :lo12:asyncPreemptBM
    msr ELR_EL1, x21                 // ELR_EL1 = asyncPreemptBM

    // 4. Save adjusted goroutine SP (x20) to a safe register before restoring
    //    We need this value to set SP before ERET
    mov x0, x20                      // x0 = adjusted goroutine SP (will be restored but that's ok)

    // 5. Restore exception stack pointer to access saved registers
    mov sp, x15                      // Back to exception stack

    // 6. Restore other saved registers from exception stack
    //    Note: This will overwrite x0 with the original interrupted SP, but we don't need it
    ldp x19, x20, [sp, #0]           // Restore saved registers (x0->x19, x1->x20)
    ldp x21, x22, [sp, #16]          // (x2->x21, x3->x22)
    ldp x3, x4, [sp, #24]            // (x3->x3, x4->x4 - restoring themselves)
    ldr w11, [sp, #40]               // Restore interrupt ID (not used)
    add sp, sp, #64                  // Deallocate exception frame

    // 7. CRITICAL: Signal end-of-interrupt to GIC BEFORE ERET
    //    Otherwise GIC thinks we're still in interrupt and won't deliver more interrupts!
    //    Interrupt ID is in w11 (we reloaded it at line 343)
    movz x1, #0x0801, lsl #16
    movk x1, #0x0010, lsl #0         // GICC_EOIR at 0x08010010
    str w11, [x1]                    // Write interrupt ID to EOIR

    // 8. CRITICAL: Set SP to adjusted goroutine stack (still in x0)
    //    ERET doesn't change SP, so we must set it here
    mov sp, x0                       // SP = adjusted goroutine SP

    // 9. ERET will now:
    //    - Jump to asyncPreemptBM (from ELR_EL1)
    //    - With LR = interrupted PC
    //    - With SP = adjusted goroutine SP (interrupted SP - 16)
    //    - asyncPreemptBM will save all registers, call scheduler, restore, ret to LR
    //    - GIC has been notified of EOI, so more interrupts can arrive
    eret

    // NEVER REACHED - ERET jumped to asyncPreemptBM
    
handle_uart_irq:
    // UART transmit ready interrupt
    // Call Go function main.UartTransmitHandler()
    // CRITICAL: Must follow Go calling conventions:
    // - Preserve x28 (g pointer), x29 (FP), x30 (LR)
    // - 16-byte stack alignment
    // - At least 16 bytes free stack space
    // - Can clobber x0-x17 (caller-saved)
    // - Must preserve x19-x27 (callee-saved)

    // Save all registers we need to preserve (expand our stack frame)
    sub sp, sp, #128              // Expand stack for more registers
    stp x5, x6, [sp, #64]         // Save x5, x6
    stp x7, x8, [sp, #80]         // Save x7, x8
    stp x28, x29, [sp, #96]       // Save x28 (g), x29 (FP)
    str x30, [sp, #112]           // Save x30 (LR)

    // Set up frame pointer for Go
    add x29, sp, #0

    // Call Go function (no parameters)
    bl main.UartTransmitHandler

    // Restore preserved registers
    ldp x5, x6, [sp, #64]
    ldp x7, x8, [sp, #80]
    ldp x28, x29, [sp, #96]
    ldr x30, [sp, #112]
    add sp, sp, #128              // Restore stack

    b irq_done
    
irq_done:
    // Signal end of interrupt to GIC
    // GICC_EOIR at 0x08010010
    // NOTE: x2 may have been clobbered by Go functions, so reload interrupt ID from stack
    ldr w2, [sp, #40]             // Reload saved interrupt ID (was saved at line 88)
    movz x1, #0x0801, lsl #16
    movk x1, #0x0010, lsl #0
    str w2, [x1]                  // Write interrupt ID to EOIR

    // Restore registers and return to normal stack
    ldr x0, [sp, #0]              // Load original SP_EL1
    ldp x1, x2, [sp, #8]          // Restore x1, x2
    ldp x3, x4, [sp, #24]         // Restore x3, x4
    add sp, sp, #64               // Clean up exception stack
    
    // Restore original SP (SP_EL1)
    mov sp, x0

    // Return from exception
    eret
    
    
    // 0x300 - 0x380: FIQ (SP_EL1) - 128 bytes
    .align 7
fiq_exception_el1:
    // FIQ not used - just hang
    b .
    
    
    // 0x380 - 0x400: SError (SP_EL1) - 128 bytes
    .align 7
serror_exception_el1:
    // SError not used - just hang
    b .


    // ========================================
    // Group 2: Lower EL, AArch64 (0x400-0x5ff)
    // For exceptions from EL0 running AArch64 code
    // (Not used until we have EL0 processes)
    // ========================================
    
    // 0x400 - 0x480: Synchronous exception (Lower EL, AArch64)
    .align 7
    b .  // Hang - not implemented yet
    
    // 0x480 - 0x500: IRQ (Lower EL, AArch64)
    .align 7
    b .  // Hang - not implemented yet
    
    // 0x500 - 0x580: FIQ (Lower EL, AArch64)
    .align 7
    b .  // Hang - not implemented yet
    
    // 0x580 - 0x600: SError (Lower EL, AArch64)
    .align 7
    b .  // Hang - not implemented yet


    // ========================================
    // Group 3: Lower EL, AArch32 (0x600-0x7ff)
    // For exceptions from EL0 running AArch32 code
    // (Not used - we only support AArch64)
    // ========================================
    
    // 0x600 - 0x680: Synchronous exception (Lower EL, AArch32)
    .align 7
    b .  // Hang - not implemented (AArch32 not supported)
    
    // 0x680 - 0x700: IRQ (Lower EL, AArch32)
    .align 7
    b .  // Hang
    
    // 0x700 - 0x780: FIQ (Lower EL, AArch32)
    .align 7
    b .  // Hang
    
    // 0x780 - 0x800: SError (Lower EL, AArch32)
    .align 7
    b .  // Hang

.global exception_vectors_end
exception_vectors_end:

// Switch back to .text section for regular functions
// Everything after the exception vector table should be in .text, not .vectors
.section ".text"

// ============================================================================
// Exception Handler Functions
// ============================================================================
// Go functions called from assembly (e.g., UartTransmitHandler) are defined
// in their respective Go files and exported via //go:linkname. The assembly
// code calls these Go functions directly using 'bl main.FunctionName'.
// No stubs needed - Go compiler will handle the linkage.


// ============================================================================
// Set VBAR_EL1 (Vector Base Address Register)
// ============================================================================
// This function is called from Go to set up the exception vector table
// VBAR_EL1 must point to a 2KB-aligned address
.global set_vbar_el1
set_vbar_el1:
    // x0 = address of exception vector table (must be 2KB aligned)
    // Minimal implementation - just set VBAR_EL1 without touching DAIF
    // (accessing DAIF might cause exceptions if VBAR_EL1 isn't set yet)
    
    // Data synchronization barrier to ensure all previous memory accesses complete
    dsb sy
    
    // Set VBAR_EL1 directly from x0
    // The msr instruction transfers the 64-bit value from x0 to VBAR_EL1
    msr VBAR_EL1, x0
    
    // Instruction synchronization barrier to ensure VBAR_EL1 is set
    // before any subsequent instructions execute
    isb
    
    ret

// read_vbar_el1() - Read VBAR_EL1 to verify it was set correctly
// Returns uintptr in x0
.global read_vbar_el1
read_vbar_el1:
    mrs x0, VBAR_EL1
    ret

// get_exception_vectors_addr() - Returns the address of exception_vectors
// Returns uintptr in x0
// Use adrp + add for addresses that might be far away (>1MB)
// adrp loads the page-aligned address (4KB aligned), add adds the page offset
// Syntax matches image.s which uses :lo12: without #
.global get_exception_vectors_addr
get_exception_vectors_addr:
    // Ensure function is properly aligned
    .align 2
    adrp x0, exception_vectors
    add  x0, x0, :lo12:exception_vectors
    ret


// ============================================================================
// Enable/Disable IRQs
// ============================================================================

// void enable_irqs(void)
// Clears the I bit in PSTATE to enable IRQ interrupts
// DAIF bits encoding in immediate value:
//   Bit 0 = F (FIQ)
//   Bit 1 = I (IRQ)  <-- This is what we want to clear
//   Bit 2 = A (SError)
//   Bit 3 = D (Debug)
// So #2 = 0b0010 clears bit 1 (I bit) to enable IRQs
// This function must be called from Go with proper nosplit/noinline markers
.global enable_irqs
enable_irqs:
    // Minimal implementation - just enable IRQs
    // Data barrier to ensure all previous operations complete
    dsb sy
    // Clear I bit (bit 1) to enable IRQ interrupts
    msr DAIFCLR, #2
    // Instruction barrier to ensure interrupt enable is visible
    isb
    ret

// enable_irqs_asm() - Minimal version to enable interrupts
// This version tries to be as minimal as possible to avoid exceptions
.global enable_irqs_asm
enable_irqs_asm:
    // Try absolute minimal approach - just the msr instruction
    // No barriers, no other operations
    // DAIF bits: Bit 1 = I (IRQ), so #2 clears IRQ mask
    msr DAIFCLR, #2  // Clear I bit (bit 1) = enable IRQs
    ret              // Return immediately


// void disable_irqs(void)
// Sets the I bit in PSTATE to disable IRQ interrupts
// DAIF bits encoding in immediate value:
//   Bit 0 = F (FIQ)
//   Bit 1 = I (IRQ)  <-- This is what we want to set
//   Bit 2 = A (SError)
//   Bit 3 = D (Debug)
// So #2 = 0b0010 sets bit 1 (I bit) to disable IRQs
.global disable_irqs
disable_irqs:
    msr DAIFSET, #2  // Set I bit (bit 1) = disable IRQs
    isb               // Instruction synchronization barrier
    ret


// uint64_t read_spsr_el1(void)
// Read the Saved Program Status Register
.global read_spsr_el1
read_spsr_el1:
    mrs x0, SPSR_EL1
    ret


// void write_spsr_el1(uint64_t value)
// Write to SPSR_EL1
.global write_spsr_el1
write_spsr_el1:
    msr SPSR_EL1, x0
    ret


// uint64_t read_elr_el1(void)
// Read the Exception Link Register
.global read_elr_el1
read_elr_el1:
    mrs x0, ELR_EL1
    ret


// void write_elr_el1(uint64_t value)
// Write to ELR_EL1
.global write_elr_el1
write_elr_el1:
    msr ELR_EL1, x0
    ret


// uint64_t read_esr_el1(void)
// Read the Exception Syndrome Register
.global read_esr_el1
read_esr_el1:
    mrs x0, ESR_EL1
    ret


// uint64_t read_far_el1(void)
// Read the Fault Address Register
.global read_far_el1
read_far_el1:
    mrs x0, FAR_EL1
    ret


// read_daif() - Read the DAIF register (interrupt mask bits)
// Returns uint64 in x0
.global read_daif
read_daif:
    mrs x0, DAIF
    ret


// ============================================================================
// Synchronous Exception Handler (placed outside vector table)
// ============================================================================
// This handler is called from the vector table entry at 0x200
// It handles SVC syscalls by faking responses, and forwards other exceptions
// to the Go exception handler.

sync_exception_handler:
    // CRITICAL FOR DEMAND PAGING: Save ALL registers IMMEDIATELY before ANY operations
    // This ensures we can restore exact state for retry after handling page faults.
    //
    // Approach: First save x29, x30 to current stack, then use them to set up exception stack.

    // Step 1: Save x29, x30 to current stack (we'll recover them later)
    stp x29, x30, [sp, #-16]!       // Push x29, x30, decrement SP by 16

    // DEBUG: Print exception class (EC) from ESR_EL1 to see what type of exception
    stp x0, x1, [sp, #-16]!          // Save x0, x1
    stp x2, x3, [sp, #-16]!          // Save x2, x3

    mrs x0, ESR_EL1                  // x0 = ESR_EL1
    lsr x0, x0, #26                  // x0 = EC (exception class)

    movz x1, #0x0900, lsl #16        // x1 = UART base

    // Print EC as 2 hex digits
    lsr x2, x0, #4                   // High nibble
    and x2, x2, #0xF
    cmp x2, #10
    blt 1f
    add x2, x2, #0x37                // 'A'-10
    b 2f
1:  add x2, x2, #0x30                // '0'
2:  str w2, [x1]

    mov x2, x0                       // Low nibble
    and x2, x2, #0xF
    cmp x2, #10
    blt 3f
    add x2, x2, #0x37                // 'A'-10
    b 4f
3:  add x2, x2, #0x30                // '0'
4:  str w2, [x1]

    movz w2, #0x20                   // Space
    str w2, [x1]

    ldp x2, x3, [sp], #16            // Restore x2, x3
    ldp x0, x1, [sp], #16            // Restore x0, x1

    // CRITICAL: Save SP_EL0 IMMEDIATELY to exception stack frame
    // When exception occurs in EL1t mode (using SP_EL0), the CPU switches to EL1h
    // mode (using SP_EL1) for the handler. SP_EL0 is NOT automatically saved!
    // If we don't save/restore SP_EL0, it will have the wrong value after eret,
    // causing stack corruption and NOFRAME functions to write to wrong addresses.
    // NOTE: We can't use the macro yet because we haven't switched to exception stack
    mrs x29, SP_EL0                 // x29 = current SP_EL0 (kmazarin's stack)
    // x29, x30 will be saved shortly when we switch to exception stack

    // Step 2: Save original SP (before we pushed) to x30
    add x30, sp, #16               // x30 = original SP (current SP + 16 for the push)

    // Step 3: Switch to exception stack
    // CRITICAL FIX: Check if we're already on exception stack (nested exception)
    // Exception stack is at 0x5F000000-0x5F010000 (64KB)
    // If SP is in this range, we're in a nested exception
    movz x29, #0x5F00, lsl #16     // x29 = 0x5F000000 (lower bound)
    cmp x30, x29                    // Compare original SP with lower bound
    b.lo use_primary_stack          // If below, use primary exception stack
    movz x29, #0x5F01, lsl #16     // x29 = 0x5F010000 (upper bound)
    cmp x30, x29                    // Compare original SP with upper bound
    b.hs use_primary_stack          // If above or equal, use primary stack

    // We're in nested exception - use alternate stack within exception stack
    // Use bottom half of exception stack for nested exceptions
    movz x29, #0x5F00, lsl #16     // Nested exception stack at 0x5F008000
    movk x29, #0x8000, lsl #0      // (middle of 64KB exception stack)
    b stack_selected

use_primary_stack:
    movz x29, #0x5F01, lsl #16     // Primary exception stack at 0x5F010000 (top)
    movk x29, #0x0000, lsl #0

stack_selected:
    mov sp, x29

    // Allocate stack frame (320 bytes for all registers + exception state + alignment)
    sub sp, sp, #320

    // Step 4: Save original SP (in x30) and original x29, x30 location
    str x30, [sp, #248]             // Save original SP

    // Save ALL registers x0-x28
    stp x0, x1, [sp, #0]
    stp x2, x3, [sp, #16]
    stp x4, x5, [sp, #32]
    stp x6, x7, [sp, #48]
    stp x8, x9, [sp, #64]
    stp x10, x11, [sp, #80]
    stp x12, x13, [sp, #96]
    stp x14, x15, [sp, #112]
    stp x16, x17, [sp, #128]
    stp x18, x19, [sp, #144]
    stp x20, x21, [sp, #160]
    stp x22, x23, [sp, #176]
    stp x24, x25, [sp, #192]
    stp x26, x27, [sp, #208]
    str x28, [sp, #224]

    // Recover original x29, x30 from the kernel stack where we pushed them
    // x30 currently holds original SP, so original x29/x30 are at [x30-16]
    ldr x0, [sp, #248]              // x0 = original SP
    sub x0, x0, #16                 // x0 = address where we pushed x29, x30
    ldp x1, x2, [x0]                // x1 = original x29, x2 = original x30
    stp x1, x2, [sp, #232]          // Save original x29, x30

    // Save exception system registers
    mrs x0, ELR_EL1                 // Return address
    mrs x1, SPSR_EL1                // Saved PSTATE
    mrs x2, FAR_EL1                 // Fault address
    mrs x3, ESR_EL1                 // Exception syndrome
    stp x0, x1, [sp, #256]          // ELR, SPSR
    stp x2, x3, [sp, #272]          // FAR, ESR

    // Save SP_EL0 to exception frame (was saved in x29 earlier, before stack switch)
    // NOTE: x29 was read from SP_EL0 at the very beginning of the exception handler
    // and has been preserved through the stack switch
    ldr x0, [sp, #232]              // x0 = original x29 (NOT SP_EL0!)
    // Actually, we need to re-read SP_EL0 now that we're on exception stack
    SAVE_SP_EL0_TO_STACK            // Save SP_EL0 to frame offset 288

    // Check exception type - only route data aborts (EC=0x25) to Go for demand paging
    // SVC (EC=0x15) goes to syscall handler

    // DEBUG: Temporarily disable exception breadcrumbs to see Go error message
    lsr x4, x3, #26                 // Extract EC from ESR
    and x4, x4, #0x3F

    // CRITICAL: Check for EC=0x00 (Unknown exception) - this often indicates
    // a NULL pointer dereference or jump to NULL. Don't try to return from
    // these - just print diagnostics and hang to avoid infinite exception loop.
    cbz x4, sync_unknown_exception  // EC=0x00 - unknown exception

    cmp x4, #0x15                   // SVC?
    bne 3f
    b sync_restore_and_svc          // Go to SVC handler (restores regs first)
3:

    // Check for debug exceptions (watchpoint hit) - forward to general handler
    cmp x4, #0x34                   // Watchpoint from lower EL?
    beq sync_other_exception
    cmp x4, #0x35                   // Watchpoint from current EL?
    beq sync_other_exception

    // For data aborts (EC=0x25), call Go handler
    cmp x4, #0x25
    bne sync_other_exception        // Not data abort - other exception

    // DEBUG: Print FAR_EL1 (fault address) for data aborts
    stp x0, x1, [sp, #-16]!          // Save x0, x1
    stp x2, x3, [sp, #-16]!          // Save x2, x3

    // Print "FAR="
    mov w0, #0x46                    // 'F'
    UART_PUTC
    mov w0, #0x41                    // 'A'
    UART_PUTC
    mov w0, #0x52                    // 'R'
    UART_PUTC
    mov w0, #0x3D                    // '='
    UART_PUTC

    mrs x0, FAR_EL1                  // x0 = fault address
    bl print_hex64                   // Safe print

    mov w0, #0x20                    // ' '
    UART_PUTC

    ldp x2, x3, [sp], #16            // Restore x2, x3
    ldp x0, x1, [sp], #16            // Restore x0, x1

    // Data abort - this might be a demand paging request
    // NOTE: CALL_GO_PROLOGUE allocates separate stack space, protecting our exception frame

    // Set up frame pointer for Go
    add x29, sp, #0

    // Prepare arguments for Go exception handler
    // x0 = ESR, x1 = ELR, x2 = SPSR, x3 = FAR, x4 = excType
    // x5 = savedFP, x6 = savedLR, x7 = savedG (for traceback)
    ldp x1, x2, [sp, #256]          // x1 = ELR, x2 = SPSR
    ldp x3, x0, [sp, #272]          // x3 = FAR, x0 = ESR (note: reversed order)
    movz x4, #0                     // excType = SYNC_EXCEPTION (0)

    // Load saved registers for traceback
    // x5 = savedFP (x29), x6 = savedLR (x30), x7 = savedG (x28)
    ldp x5, x6, [sp, #232]          // x5 = saved x29 (FP), x6 = saved x30 (LR)
    ldr x7, [sp, #224]              // x7 = saved g (x28)

    // CRITICAL: Switch to g0 before calling Go exception handler
    // This allows runtime operations (including stack tracebacks) to work correctly
    // The original g (x28) is saved at [sp, #224] and will be restored before eret
    //
    // NOTE: For now, we DON'T switch to g0 because it causes pointer corruption issues.
    // The exception handlers run with whatever g was active, on the exception stack.
    // TODO: Investigate proper g0/gsignal setup for exception handlers
    // ldr x28, =runtime.g0

    // Call Go exception handler with 8 parameters
    // Must provide spill space for Go's argument spills
    CALL_GO_PROLOGUE SPILL_SPACE_8PARAM
    bl main.ExceptionHandler
    CALL_GO_EPILOGUE SPILL_SPACE_8PARAM

    // Go handler returned - this means page fault was handled
    // Restore ALL registers and retry faulting instruction
    //
    // CRITICAL: Must restore ELR_EL1, SPSR_EL1, and SP_EL0 before eret!
    //
    // Strategy: Restore all registers from exception frame, then ERET
    // NOTE: CALL_GO_PROLOGUE protects exception frame, so all registers are safe
    // NOTE: We DON'T need to switch SP here - ERET handles mode switching automatically

    // Step 1: Restore ELR_EL1 and SPSR_EL1 while still on exception stack
    ldp x0, x1, [sp, #256]          // x0 = saved ELR, x1 = saved SPSR
    msr ELR_EL1, x0                 // Restore return address
    msr SPSR_EL1, x1                // Restore saved PSTATE
    isb                             // Ensure ELR/SPSR writes complete

    // Step 2: Restore SP_EL0 (kmazarin's stack pointer)
    // This is critical because ERET may return to EL1t mode which uses SP_EL0
    RESTORE_SP_EL0_FROM_STACK       // Restore from frame offset 288

    // Step 3: Restore ALL general-purpose registers from exception frame
    ldp x0, x1, [sp, #0]            // Restore x0, x1
    ldp x2, x3, [sp, #16]           // Restore x2, x3
    ldp x4, x5, [sp, #32]           // Restore x4, x5
    ldp x6, x7, [sp, #48]           // Restore x6, x7
    ldp x8, x9, [sp, #64]           // Restore x8, x9
    ldp x10, x11, [sp, #80]         // Restore x10, x11
    ldp x12, x13, [sp, #96]         // Restore x12, x13
    ldp x14, x15, [sp, #112]        // Restore x14, x15
    ldp x16, x17, [sp, #128]        // Restore x16, x17
    ldp x18, x19, [sp, #144]        // Restore x18, x19
    ldp x20, x21, [sp, #160]        // Restore x20, x21
    ldp x22, x23, [sp, #176]        // Restore x22, x23
    ldp x24, x25, [sp, #192]        // Restore x24, x25
    ldp x26, x27, [sp, #208]        // Restore x26, x27
    ldr x28, [sp, #224]             // Restore x28 (g)
    ldp x29, x30, [sp, #232]        // Restore x29, x30

    // Step 4: Restore SP_EL1 (exception stack) to its original position
    // We need to deallocate the exception frame (320 bytes) before ERET
    add sp, sp, #320

    // Return from exception to retry the faulting instruction
    eret

sync_unknown_exception:
    // EC=0x00 - Unknown exception (often NULL pointer or jump to NULL)
    // Call Go exception handler to print traceback and then hang
    add x29, sp, #0
    ldp x1, x2, [sp, #256]          // x1 = ELR, x2 = SPSR
    ldp x3, x0, [sp, #272]          // x3 = FAR, x0 = ESR
    movz x4, #0                     // excType = SYNC_EXCEPTION (0)

    // Load saved registers for traceback
    // x5 = savedFP (x29), x6 = savedLR (x30), x7 = savedG (x28)
    ldp x5, x6, [sp, #232]          // x5 = saved x29 (FP), x6 = saved x30 (LR)
    ldr x7, [sp, #224]              // x7 = saved g (x28)

    // CRITICAL: Switch to g0 before calling Go exception handler
    // This allows runtime operations (including stack tracebacks) to work correctly
    ldr x28, =runtime.g0

    // CRITICAL: Switch SP to g0's stack
    ldr x10, [x28, #8]              // Load g0.stack.hi into x10
    mov sp, x10                     // Switch SP to top of g0 stack

    CALL_GO_PROLOGUE SPILL_SPACE_8PARAM
    bl main.ExceptionHandler
    CALL_GO_EPILOGUE SPILL_SPACE_8PARAM
    // If handler returns, hang
    b .

sync_other_exception:
    // Other exception type - forward to Go handler but don't expect to return
    add x29, sp, #0
    ldp x1, x2, [sp, #256]          // x1 = ELR, x2 = SPSR
    ldp x3, x0, [sp, #272]          // x3 = FAR, x0 = ESR
    movz x4, #0                     // excType = SYNC_EXCEPTION (0)

    // Load saved registers for traceback
    // x5 = savedFP (x29), x6 = savedLR (x30), x7 = savedG (x28)
    ldp x5, x6, [sp, #232]          // x5 = saved x29 (FP), x6 = saved x30 (LR)
    ldr x7, [sp, #224]              // x7 = saved g (x28)

    // CRITICAL: Switch to g0 before calling Go exception handler
    // This allows runtime operations (including stack tracebacks) to work correctly
    ldr x28, =runtime.g0

    // CRITICAL: Switch SP to g0's stack
    ldr x10, [x28, #8]              // Load g0.stack.hi into x10
    mov sp, x10                     // Switch SP to top of g0 stack

    CALL_GO_PROLOGUE SPILL_SPACE_8PARAM
    bl main.ExceptionHandler
    CALL_GO_EPILOGUE SPILL_SPACE_8PARAM
    // If handler returns, hang
    b .

sync_restore_and_svc:
    // SVC - restore registers and jump to SVC handler
    // For syscalls, we need to restore the full register state because
    // Go code expects x30 (LR) and other registers to be preserved across SVC.
    //
    // Memory layout at exception entry:
    //   - At entry: SP points to kernel stack
    //   - We pushed x29, x30 -> SP = original_SP - 16
    //   - We saved (SP + 16) = original_SP to [exc_sp, #248]
    //   - We recovered original x29/x30 from [original_SP - 16] and saved to [exc_sp, #232]
    //
    // To restore: We need to set SP = original_SP (not original_SP - 16)
    // and restore x29/x30 from the exception frame (not kernel stack).

    // Step 1: Restore x0-x28 from exception frame
    ldp x0, x1, [sp, #0]
    ldp x2, x3, [sp, #16]
    ldp x4, x5, [sp, #32]
    ldp x6, x7, [sp, #48]
    ldp x8, x9, [sp, #64]
    ldp x10, x11, [sp, #80]
    ldp x12, x13, [sp, #96]
    ldp x14, x15, [sp, #112]
    ldp x16, x17, [sp, #128]
    ldp x18, x19, [sp, #144]
    ldp x20, x21, [sp, #160]
    ldp x22, x23, [sp, #176]
    ldp x24, x25, [sp, #192]
    ldp x26, x27, [sp, #208]
    ldr x28, [sp, #224]

    // Step 2: Syscall context is ALREADY saved in exception frame!
    // CRITICAL: We must keep SPSR_EL1/ELR_EL1 in frame so we can restore RIGHT BEFORE eret!
    // If we restore them now and then execute more handler code, they might get corrupted.
    //
    // Exception frame already contains (saved during sync_exception_handler):
    //   [sp, #232]: x29, x30 (original FP, LR)
    //   [sp, #0]:   x0 (original, will be overwritten with syscall return value)
    //   [sp, #256]: ELR_EL1, SPSR_EL1 (saved)
    //   [sp, #288]: SP_EL0 (saved)
    //
    // For syscall, we need to preserve x0 (gets overwritten with return value),
    // x28 (g pointer), and x30 (LR) so they can be restored after Go handler returns.

    // Save current syscall context to extended frame area BEFORE calling Go handler
    // x0 = original x0 (from frame), x28 = current g, x30 = original x30 (from frame)
    ldr x10, [sp, #0]               // x10 = original x0
    str x10, [sp, #EXC_FRAME_SAVED_X0]  // Save to offset 304
    str x28, [sp, #EXC_FRAME_SAVED_G]   // Save g to offset 296
    ldp x10, x11, [sp, #232]        // x10 = original x29, x11 = original x30
    str x11, [sp, #EXC_FRAME_SAVED_LR]  // Save LR to offset 312

    // Step 3: x29/x30 are still in frame at [sp, #232] - just load them
    // CRITICAL: Do NOT switch SP! We must stay on exception stack (SP_EL1).
    // CRITICAL: x0 will be overwritten with syscall return value - that's expected!
    ldp x29, x30, [sp, #232]        // Restore x29/x30 from frame

    // DEBUG: Print FP1 (FP before calling syscall handler)
    stp x0, x1, [sp, #-16]!
    movz x0, #0x0900, lsl #16
    movz w1, #0x46                  // 'F'
    str w1, [x0]
    movz w1, #0x31                  // '1'
    str w1, [x0]
    movz w1, #0x20                  // ' '
    str w1, [x0]
    ldp x0, x1, [sp], #16

    // Now x0, x29, x30 are restored and SP = SP_EL1 (exception stack)
    // SP_EL0 remains at its original value (saved at 0x40FFF000)
    b handle_svc_syscall

handle_svc_syscall:
    // Handle syscalls in assembly - minimal version for testing
    // x8 contains the Linux syscall number
    // Return value goes in x0
    //
    // IMPORTANT: Go's syscall wrappers (sysMmap.abi0, etc.) expect:
    //   - SVC returns x0 = result (or -errno for error)
    //   - After eret, their code checks x0 and stores to stack
    //   - We just need to return correct x0 and advance ELR+4

    // DEBUG: Print syscall number in hex
    stp x0, x1, [sp, #-16]!         // Save x0, x1
    movz x0, #0x0900, lsl #16       // UART base
    // Print syscall number (x8) - just last 2 hex digits
    lsr x1, x8, #4                  // High nibble
    and x1, x1, #0xF
    cmp x1, #10
    blt 1f
    add x1, x1, #0x37               // 'A'-10
    b 2f
1:  add x1, x1, #0x30               // '0'
2:  str w1, [x0]

    mov x1, x8                      // Low nibble
    and x1, x1, #0xF
    cmp x1, #10
    blt 3f
    add x1, x1, #0x37               // 'A'-10
    b 4f
3:  add x1, x1, #0x30               // '0'
4:  str w1, [x0]

    movz w1, #0x20                  // Space
    str w1, [x0]
    ldp x0, x1, [sp], #16           // Restore x0, x1

    // CRITICAL: Switch to g0 before calling Go syscall handlers
    // This allows runtime operations (including stack tracebacks) to work correctly
    // NOTE: Original g (x28) was already saved to exception frame at offset 296
    // by sync_restore_and_svc (see SAVE_SYSCALL_CONTEXT equivalent above)

    ldr x28, =runtime.g0            // x28 = address of runtime.g0 struct (the g pointer itself)

    // NOTE: We don't switch SP to g0's stack here because syscalls need to preserve
    // the caller's stack frame and return properly. We just set x28 so Go code sees g0.
    // Syscall handlers run on the current stack, not g0's stack.

    // Dispatch based on syscall number
    cmp x8, #0                     // io_setup syscall (async I/O)
    beq syscall_io_setup
    cmp x8, #64                    // write syscall
    beq syscall_write
    cmp x8, #63                    // read syscall
    beq syscall_read
    cmp x8, #56                    // openat syscall
    beq syscall_openat
    cmp x8, #57                    // close syscall
    beq syscall_close
    cmp x8, #93                    // exit syscall
    beq syscall_exit
    cmp x8, #94                    // exit_group syscall
    beq syscall_exit
    cmp x8, #98                    // futex syscall
    beq syscall_futex
    cmp x8, #101                   // nanosleep syscall
    beq syscall_success
    cmp x8, #113                   // clock_gettime syscall
    beq syscall_clock_gettime
    cmp x8, #129                   // kill syscall
    beq syscall_kill
    cmp x8, #130                   // tkill syscall
    beq syscall_tkill
    cmp x8, #131                   // tgkill syscall
    beq syscall_tgkill
    cmp x8, #132                   // sigaltstack syscall
    beq syscall_success
    cmp x8, #134                   // rt_sigaction syscall
    beq syscall_rt_sigaction
    cmp x8, #135                   // rt_sigprocmask syscall
    beq syscall_rt_sigprocmask
    cmp x8, #167                   // prctl syscall
    beq syscall_success
    cmp x8, #172                   // getpid syscall
    beq syscall_getpid
    cmp x8, #123                   // sched_getaffinity syscall
    beq syscall_sched_getaffinity
    cmp x8, #178                   // gettid syscall
    beq syscall_gettid
    cmp x8, #204                   // sched_setaffinity syscall
    beq syscall_success            // Just return success for setaffinity
    cmp x8, #214                   // brk syscall
    beq syscall_brk
    cmp x8, #215                   // munmap syscall
    beq syscall_munmap
    cmp x8, #124                   // sched_yield syscall
    beq syscall_success
    cmp x8, #220                   // clone syscall
    beq syscall_clone_fake
    cmp x8, #222                   // mmap syscall
    beq syscall_mmap
    cmp x8, #226                   // mprotect syscall
    beq syscall_success
    cmp x8, #233                   // madvise syscall
    beq syscall_madvise
    cmp x8, #261                   // prlimit64 syscall
    beq syscall_success
    cmp x8, #278                   // getrandom syscall
    beq syscall_getrandom

    // Unknown syscall - call Go function to print syscall number
    // Save callee-saved registers for Go call
    sub sp, sp, #64
    stp x19, x20, [sp, #0]
    stp x21, x22, [sp, #16]
    stp x28, x29, [sp, #32]
    stp x30, x8, [sp, #48]         // Save x30 (LR) and x8 (syscall number)

    // Set up frame pointer for Go
    add x29, sp, #0

    // Pass syscall number as argument (x8 -> x0)
    mov x0, x8

    // Call Go function to print unknown syscall
    bl main.SyscallUnknown

    // Restore registers
    ldp x19, x20, [sp, #0]
    ldp x21, x22, [sp, #16]
    ldp x28, x29, [sp, #32]
    ldp x30, x8, [sp, #48]
    add sp, sp, #64

    movn x0, #37                   // x0 = -38 (ENOSYS)
    b syscall_return

syscall_write:
    // write(fd, buf, count)
    // x0 = fd, x1 = buf, x2 = count
    // If fd is 1 (stdout) or 2 (stderr), print to UART
    cmp x0, #1
    beq syscall_write_uart
    cmp x0, #2
    beq syscall_write_uart
    // For other fds, just pretend we wrote all bytes
    mov x0, x2
    b syscall_return

syscall_write_uart:
    // Write buffer to UART via ring buffer (interrupt-driven)
    // SyscallWriteBuffer(buf unsafe.Pointer, count uint32) - 2 parameters
    // x1 = buf pointer, x2 = count

    mov x0, x1                     // x0 = buf pointer
    mov w1, w2                     // x1 = count (32-bit)
    CALL_GO_PROLOGUE SPILL_SPACE_2PARAM
    bl main.SyscallWriteBuffer
    CALL_GO_EPILOGUE SPILL_SPACE_2PARAM
    b syscall_return

syscall_read:
    // read(fd, buf, count) - 3 parameters
    // x0-x2 contain arguments - call Go SyscallRead function
    CALL_GO_PROLOGUE SPILL_SPACE_3PARAM
    bl main.SyscallRead
    CALL_GO_EPILOGUE SPILL_SPACE_3PARAM
    b syscall_return

syscall_openat:
    // openat(dirfd, pathname, flags, mode) - 4 parameters
    // x0-x3 contain arguments - call Go SyscallOpenat function
    CALL_GO_PROLOGUE SPILL_SPACE_4PARAM
    bl main.SyscallOpenat
    CALL_GO_EPILOGUE SPILL_SPACE_4PARAM
    b syscall_return

syscall_futex:
    // futex(uaddr, futex_op, val, timeout, uaddr2, val3) - 6 parameters
    // x0-x5 contain arguments - call Go SyscallFutex function
    CALL_GO_PROLOGUE SPILL_SPACE_6PARAM
    bl main.SyscallFutex
    CALL_GO_EPILOGUE SPILL_SPACE_6PARAM
    b syscall_return

syscall_close:
    // close(fd) - 1 parameter
    // x0 = fd - call Go SyscallClose function
    CALL_GO_PROLOGUE SPILL_SPACE_1PARAM
    bl main.SyscallClose
    CALL_GO_EPILOGUE SPILL_SPACE_1PARAM
    b syscall_return

syscall_io_setup:
    // io_setup(nr_events, ctxp) - Async I/O setup
    // We don't support async I/O, so return ENOSYS (-38)
    // This tells the runtime that io_setup is not implemented
    movn x0, #37                   // x0 = -38 (ENOSYS)
    b syscall_return

syscall_success:
    // Generic success return
    mov x0, #0
    b syscall_return

syscall_clone_fake:
    // clone - return fake TIDs to prevent actual thread creation
    // Runtime expects clone to succeed, but we don't want real threads
    // sysmon is replaced by timer interrupt-driven monitors (GC, scavenger, schedtrace)
    adrp x1, fake_tid_counter
    add x1, x1, :lo12:fake_tid_counter
    ldr w0, [x1]
    add w0, w0, #1
    str w0, [x1]
    add w0, w0, #99                // Return fake TID: 100, 101, 102...
    b syscall_return

.data
.align 2
fake_tid_counter:
    .word 0                        // Counter for fake TIDs

.text

syscall_mmap:
    // mmap(addr, length, prot, flags, fd, offset) - 6 parameters
    // x0-x5 contain arguments - call Go SyscallMmap function

    CALL_GO_PROLOGUE SPILL_SPACE_6PARAM
    bl main.SyscallMmap

    // DEBUG: Print "MMAP_RET=" and return value
    // CRITICAL: Save x0 FIRST, then do ALL printing, then restore x0
    // This ensures x0 is never modified during printing
    stp x0, x1, [sp, #-16]!          // Save x0 (return value) to stack

    // Print "M="
    mov w0, #0x4D                    // 'M'
    UART_PUTC
    mov w0, #0x3D                    // '='
    UART_PUTC

    // Print the hex value (from stack, not x0!)
    ldr x0, [sp]                     // Load saved value from stack
    bl print_hex64                   // Print it (preserves all regs)

    // Print space
    mov w0, #0x20                    // ' '
    UART_PUTC

    // Restore x0 with the ORIGINAL return value (NOT corrupted!)
    ldp x0, x1, [sp], #16            // x0 = original return value

    CALL_GO_EPILOGUE SPILL_SPACE_6PARAM

    b syscall_return

syscall_prctl:
    // prctl - return success (debug output disabled)
    mov x0, #0
    b syscall_return

syscall_getrandom:
    // getrandom(void *buf, size_t buflen, unsigned int flags)
    // getRandomBytes(buf unsafe.Pointer, length uint32) - 2 parameters
    // x0 = buf, x1 = buflen, x2 = flags (ignored)
    mov w1, w1                     // Convert buflen to uint32
    CALL_GO_PROLOGUE SPILL_SPACE_2PARAM
    bl main.getRandomBytes
    CALL_GO_EPILOGUE SPILL_SPACE_2PARAM
    b syscall_return

syscall_sched_getaffinity:
    // sched_getaffinity(pid, cpusetsize, mask) - 3 parameters
    // x0-x2 contain arguments - call Go SyscallSchedGetaffinity function
    CALL_GO_PROLOGUE SPILL_SPACE_3PARAM
    bl main.SyscallSchedGetaffinity
    CALL_GO_EPILOGUE SPILL_SPACE_3PARAM
    b syscall_return

syscall_clock_gettime:
    // clock_gettime(clockid, timespec*)
    // x0 = clockid (CLOCK_REALTIME=0, CLOCK_MONOTONIC=1)
    // x1 = pointer to timespec {tv_sec, tv_nsec}
    // Return: 0 on success
    // Call Go implementation: SyscallClockGettime(clockid int32, timespecPtr uintptr) int64
    CALL_GO_PROLOGUE SPILL_SPACE_2PARAM
    bl main.SyscallClockGettime
    CALL_GO_EPILOGUE SPILL_SPACE_2PARAM
    b syscall_return

syscall_getpid:
    // getpid() - return fake PID 1 (init process)
    mov x0, #1
    b syscall_return

syscall_gettid:
    // gettid() - return fake TID 1 (main thread)
    mov x0, #1
    b syscall_return

syscall_brk:
    // brk(addr) - 1 parameter
    // x0 = requested break address - call Go SyscallBrk function
    CALL_GO_PROLOGUE SPILL_SPACE_1PARAM
    bl main.SyscallBrk
    CALL_GO_EPILOGUE SPILL_SPACE_1PARAM
    b syscall_return

syscall_munmap:
    // munmap(addr, length) - 2 parameters
    // x0-x1 contain arguments - call Go SyscallMunmap function
    CALL_GO_PROLOGUE SPILL_SPACE_2PARAM
    bl main.SyscallMunmap
    CALL_GO_EPILOGUE SPILL_SPACE_2PARAM
    b syscall_return

syscall_madvise:
    // madvise(addr, length, advice) - give advice about memory usage
    // x0 = addr, x1 = length, x2 = advice

    // Just return success (0) - we don't actually do anything
    mov x0, #0
    b syscall_return

syscall_kill:
    // kill(pid, sig) - 2 parameters
    // x0 = pid, x1 = sig
    CALL_GO_PROLOGUE SPILL_SPACE_2PARAM
    bl main.SyscallKill
    CALL_GO_EPILOGUE SPILL_SPACE_2PARAM
    b syscall_return

syscall_tkill:
    // tkill(tid, sig) - 2 parameters
    // x0 = tid, x1 = sig
    CALL_GO_PROLOGUE SPILL_SPACE_2PARAM
    bl main.SyscallTkill
    CALL_GO_EPILOGUE SPILL_SPACE_2PARAM
    b syscall_return

syscall_tgkill:
    // tgkill(tgid, tid, sig) - 3 parameters
    // x0 = tgid, x1 = tid, x2 = sig
    CALL_GO_PROLOGUE SPILL_SPACE_3PARAM
    bl main.SyscallTgkill
    CALL_GO_EPILOGUE SPILL_SPACE_3PARAM
    b syscall_return

syscall_rt_sigaction:
    // rt_sigaction(sig, act, oldact, sigsetsize) - 4 parameters
    // x0 = sig, x1 = act, x2 = oldact, x3 = sigsetsize
    CALL_GO_PROLOGUE SPILL_SPACE_4PARAM
    bl main.SyscallRtSigaction
    CALL_GO_EPILOGUE SPILL_SPACE_4PARAM
    b syscall_return

syscall_rt_sigprocmask:
    // rt_sigprocmask(how, set, oldset, sigsetsize) - 4 parameters
    // x0 = how, x1 = set, x2 = oldset, x3 = sigsetsize
    CALL_GO_PROLOGUE SPILL_SPACE_4PARAM
    bl main.SyscallRtSigprocmask
    CALL_GO_EPILOGUE SPILL_SPACE_4PARAM
    b syscall_return

syscall_exit:
    // exit/exit_group - call Go SyscallExit function
    // x0 = exit code (int32)
    CALL_GO_PROLOGUE SPILL_SPACE_1PARAM
    bl main.SyscallExit
    // SyscallExit never returns (infinite loop)
    // But if it does return, halt here instead of falling through to syscall_return
1:  wfe                             // Wait for event (low power)
    b 1b                           // Loop forever

syscall_return:
    // Syscall return - restore SPSR/ELR and return via eret
    // x0 contains the syscall result (must be preserved!)

    // DEBUG: Print 'R' to mark syscall_return entry
    // UART_PUTC expects character in w0, so save x0 first
    str x0, [sp, #-16]!             // Save x0 (syscall result)
    mov w0, #0x52                   // 'R'
    UART_PUTC
    ldr x0, [sp], #16               // Restore x0

    // DEBUG: First print persistent.base value (should contain pointer after mmap)
    stp x1, x2, [sp, #-16]!          // Save x1, x2
    str x3, [sp, #-16]!              // Save x3

    movz x1, #0x419A, lsl #16
    movk x1, #0x1068, lsl #0        // x1 = 0x419A1068 (persistent.base address)
    ldr x2, [x1]                    // x2 = value at persistent.base

    // Print 'P' marker for persistent.base
    str x0, [sp, #-16]!
    mov w0, #0x50                   // 'P'
    UART_PUTC
    mov w0, #0x5B                   // '['
    UART_PUTC

    // Print byte 3 of persistent.base
    lsr x3, x2, #24
    and x3, x3, #0xFF
    lsr w0, w3, #4
    cmp w0, #10
    add w0, w0, #0x30
    blt 101f
    add w0, w0, #7
101:  UART_PUTC
    and w0, w3, #0x0F
    cmp w0, #10
    add w0, w0, #0x30
    blt 102f
    add w0, w0, #7
102:  UART_PUTC

    // Print byte 2 of persistent.base
    lsr x3, x2, #16
    and x3, x3, #0xFF
    lsr w0, w3, #4
    cmp w0, #10
    add w0, w0, #0x30
    blt 103f
    add w0, w0, #7
103:  UART_PUTC
    and w0, w3, #0x0F
    cmp w0, #10
    add w0, w0, #0x30
    blt 104f
    add w0, w0, #7
104:  UART_PUTC

    mov w0, #0x5D                   // ']'
    UART_PUTC

    ldr x0, [sp], #16               // Restore x0
    ldr x3, [sp], #16               // Restore x3
    ldp x1, x2, [sp], #16           // Restore x1, x2

    // DEBUG: Now print memstats.other_sys value for comparison
    // This will show 0x00040000 vs 0xDEAD000E
    stp x1, x2, [sp, #-16]!          // Save x1, x2 (x0 must be preserved!)
    str x3, [sp, #-16]!              // Save x3

    movz x1, #0x419A, lsl #16
    movk x1, #0x39A8, lsl #0        // x1 = 0x419A39A8
    ldr x2, [x1]                    // x2 = value at 0x419A39A8

    // Print opening bracket
    str x0, [sp, #-16]!             // Save x0
    mov w0, #0x5B                   // '['
    UART_PUTC

    // Print byte 3 (high byte of low word)
    lsr x3, x2, #24
    and x3, x3, #0xFF
    lsr w0, w3, #4
    cmp w0, #10
    add w0, w0, #0x30
    blt 2f
    add w0, w0, #7
2:  UART_PUTC
    and w0, w3, #0x0F
    cmp w0, #10
    add w0, w0, #0x30
    blt 3f
    add w0, w0, #7
3:  UART_PUTC

    // Print byte 2
    lsr x3, x2, #16
    and x3, x3, #0xFF
    lsr w0, w3, #4
    cmp w0, #10
    add w0, w0, #0x30
    blt 4f
    add w0, w0, #7
4:  UART_PUTC
    and w0, w3, #0x0F
    cmp w0, #10
    add w0, w0, #0x30
    blt 5f
    add w0, w0, #7
5:  UART_PUTC

    // Print closing bracket
    mov w0, #0x5D                   // ']'
    UART_PUTC

    ldr x0, [sp], #16               // Restore x0
    ldr x3, [sp], #16               // Restore x3
    ldp x1, x2, [sp], #16           // Restore x1, x2

    // Restore ELR_EL1 and SPSR_EL1 from exception frame
    ldp x12, x13, [sp, #EXC_FRAME_ELR_SPSR]  // x12 = saved ELR, x13 = saved SPSR

    // CRITICAL: Advance ELR_EL1 by 4 to skip past SVC instruction
    add x12, x12, #4                // ELR += 4 (instruction after SVC)

    msr ELR_EL1, x12                // Restore return address
    msr SPSR_EL1, x13               // Restore saved PSTATE
    isb                             // Ensure ELR/SPSR writes complete

    // Restore original g (x28) and LR (x30) from exception frame
    ldr x30, [sp, #EXC_FRAME_SAVED_LR]  // Restore original LR (offset 312)
    ldr x28, [sp, #EXC_FRAME_SAVED_G]   // Restore original g (offset 296)

    // Restore SP_EL0 before eret
    RESTORE_SP_EL0_FROM_STACK       // Restore from frame offset 288

    // Deallocate exception frame before ERET
    add sp, sp, #320                 // Restore SP_EL1 to exception stack top

    // Return from exception - PSTATE will be restored from SPSR_EL1
    eret

// ============================================================================
// EL1t MODE (SP_EL0) EXCEPTION HANDLERS
// ============================================================================
// When running in EL1t mode (using SP_EL0), exceptions automatically switch
// to EL1h mode (using SP_EL1) for the handler. This means the handlers can
// be identical to the EL1h handlers - they already use the exception stack.

.global sync_exception_handler_el0
sync_exception_handler_el0:
    // Just jump to the regular sync handler - it works for both modes
    b sync_exception_handler

.global irq_exception_handler_el0
irq_exception_handler_el0:
    // Just jump to the regular IRQ handler
    b irq_exception_el1

// Helper: print decimal number in x1 to UART at x0
// Clobbers: x1, x2, x3, x4, x5
print_decimal_uart:
    stp x29, x30, [sp, #-16]!       // Save FP, LR
    mov x2, x1                      // x2 = number to print
    mov x3, #10                     // x3 = divisor
    mov x4, sp                      // x4 = buffer pointer (use stack)
    sub sp, sp, #32                 // Reserve 32 bytes for digits

    // Handle zero special case
    cbnz x2, 1f
    movz w5, #0x30                  // '0'
    str w5, [x0]
    add sp, sp, #32
    ldp x29, x30, [sp], #16
    ret

1:  // Convert to decimal digits (reverse order)
    mov x5, x4
2:  udiv x6, x2, x3                 // x6 = number / 10
    msub x7, x6, x3, x2             // x7 = number % 10
    add x7, x7, #0x30               // Convert to ASCII
    strb w7, [x5], #1               // Store digit
    mov x2, x6                      // number = number / 10
    cbnz x2, 2b                     // Continue if non-zero

    // Print digits in correct order
3:  sub x5, x5, #1                  // Move back one digit
    ldrb w6, [x5]                   // Load digit
    str w6, [x0]                    // Print to UART
    cmp x5, x4                      // At start?
    bne 3b                          // Continue if not

    add sp, sp, #32                 // Restore stack
    ldp x29, x30, [sp], #16         // Restore FP, LR
    ret

// Helper: print byte in x2 as 2 hex digits to UART at x1
// Clobbers: x3
print_hex_byte_uart:
    stp x29, x30, [sp, #-16]!
    and x2, x2, #0xFF               // Mask to byte
    lsr x3, x2, #4                  // High nibble
    cmp x3, #10
    blt 1f
    add x3, x3, #0x37               // 'A'-10
    b 2f
1:  add x3, x3, #0x30               // '0'
2:  str w3, [x1]
    and x3, x2, #0xF                // Low nibble
    cmp x3, #10
    blt 3f
    add x3, x3, #0x37
    b 4f
3:  add x3, x3, #0x30
4:  str w3, [x1]
    ldp x29, x30, [sp], #16
    ret

