// Goroutine switching assembly functions
// Simplified versions of Go runtime's gogo() function

// switchToGoroutine switches execution to a new goroutine
// This is called from Go code on g0's stack (the system goroutine)
// This is the proper pattern: g0 switches to user goroutines
// Parameters:
//   x0: Pointer to new goroutine (runtimeG*)
//   x1: Function address to call (uintptr, currently unused - we call kernelMainBodyWrapper directly)
//
// This function:
//   1. Sets x28 (g pointer) to new goroutine
//   2. Sets SP to new goroutine's stack
//   3. Sets up call frame and calls kernelMainBodyWrapper
.global switchToGoroutine
.extern main.kernelMainBodyWrapper

switchToGoroutine:
    // x0 = new goroutine pointer (runtimeG*)
    // x1 = function address (currently unused)
    
    // Set x28 to new goroutine (CRITICAL for Go runtime)
    // The Go runtime reads the current goroutine via x28
    mov x28, x0
    
    // Get new goroutine's stack pointer from g.sched.sp
    // runtimeG layout: stack(16) + stackguard0(8) + stackguard1(8) + _panic(8) + _defer(8) + m(8) = 56
    // sched starts at offset 56
    // sched.sp is at offset 56 + 0 = 56
    ldr x2, [x0, #56]  // Load g.sched.sp
    
    // SP ALIGNMENT CHECK: Verify g.sched.sp is 16-byte aligned before setting SP
    and x3, x2, #0xF               // Check alignment (lower 4 bits)
    cbnz x3, sp_misaligned_switch   // If not zero, SP is misaligned!
    
    // SP is aligned, set it normally
    mov sp, x2
    
    // Return to caller - they will call the Go function on the new stack
    // The return address is in lr (link register), not on the stack
    // So we can safely return even though we've switched stacks
    // The Go function will allocate its own frame when called
    ret
    
sp_misaligned_switch:
    // g.sched.sp was misaligned!
    // Print diagnostic via UART (minimal, no stack)
    movz x3, #0x0900, lsl #16      // UART base = 0x09000000
    movk x3, #0x0000, lsl #0
    
    // Print "SP-MISALIGN: switchToGoroutine g.sched.sp=0x"
    movz w4, #0x53                 // 'S'
    str w4, [x3]
    movz w4, #0x50                 // 'P'
    str w4, [x3]
    movz w4, #0x2D                 // '-'
    str w4, [x3]
    movz w4, #0x4D                 // 'M'
    str w4, [x3]
    movz w4, #0x49                 // 'I'
    str w4, [x3]
    movz w4, #0x53                 // 'S'
    str w4, [x3]
    movz w4, #0x41                 // 'A'
    str w4, [x3]
    movz w4, #0x4C                 // 'L'
    str w4, [x3]
    movz w4, #0x49                 // 'I'
    str w4, [x3]
    movz w4, #0x47                 // 'G'
    str w4, [x3]
    movz w4, #0x3A                 // ':'
    str w4, [x3]
    movz w4, #0x20                 // ' '
    str w4, [x3]
    movz w4, #0x73                 // 's'
    str w4, [x3]
    movz w4, #0x77                 // 'w'
    str w4, [x3]
    movz w4, #0x69                 // 'i'
    str w4, [x3]
    movz w4, #0x74                 // 't'
    str w4, [x3]
    movz w4, #0x63                 // 'c'
    str w4, [x3]
    movz w4, #0x68                 // 'h'
    str w4, [x3]

    // Round down to 16-byte boundary and set SP anyway
    bic x2, x2, #0xF                // Clear lower 4 bits to align

    mov sp, x2                       // Set aligned SP
    
    // Continue execution (SP is now aligned)
    ret
    
    // If kernelMainBodyWrapper returns (shouldn't happen), halt
halt_loop:
    wfe
    b halt_loop

// runOnGoroutine switches to a new goroutine's stack, runs a function, then returns
// This is used for cooperative goroutine spawning.
//
// Parameters:
//   x0: Pointer to new goroutine (runtimeG*)
//   x1: Function pointer to call (func())
//
// This function:
//   1. Saves caller's state (SP, LR, callee-saved registers)
//   2. Sets x28 (g pointer) to new goroutine
//   3. Switches SP to new goroutine's stack
//   4. Calls the function
//   5. Restores original state and returns
//
.global runOnGoroutine
runOnGoroutine:
    // Save callee-saved registers and return address on current stack
    // AArch64 calling convention: x19-x28 are callee-saved
    // We also save x29 (frame pointer) and x30 (link register)
    stp x29, x30, [sp, #-16]!
    stp x27, x28, [sp, #-16]!
    stp x25, x26, [sp, #-16]!
    stp x23, x24, [sp, #-16]!
    stp x21, x22, [sp, #-16]!
    stp x19, x20, [sp, #-16]!

    // Save current SP in a callee-saved register so we can restore it
    mov x19, sp

    // Save the old g pointer (x28) so we can restore it
    mov x20, x28

    // Save function pointer
    mov x21, x1

    // Set x28 to new goroutine pointer
    mov x28, x0

    // Get new goroutine's stack pointer from g.sched.sp (offset 56)
    ldr x2, [x0, #56]

    // Verify SP is 16-byte aligned
    and x3, x2, #0xF
    cbnz x3, run_sp_misaligned

    // Switch to new goroutine's stack
    mov sp, x2

    // Call the function
    // In Go, func() is a pointer to a funcval struct where first word is the code pointer
    ldr x3, [x21]           // Load code pointer from funcval
    blr x3                   // Call the function

    // Function returned - restore original state
run_restore:
    // Restore original SP
    mov sp, x19

    // Restore original g pointer
    mov x28, x20

    // Restore callee-saved registers
    ldp x19, x20, [sp], #16
    ldp x21, x22, [sp], #16
    ldp x23, x24, [sp], #16
    ldp x25, x26, [sp], #16
    ldp x27, x28, [sp], #16
    ldp x29, x30, [sp], #16

    ret

run_sp_misaligned:
    // SP was misaligned - print error via UART and halt
    movz x3, #0x0900, lsl #16
    movk x3, #0x0000
    movz w4, #0x47    // 'G'
    str w4, [x3]
    movz w4, #0x4F    // 'O'
    str w4, [x3]
    movz w4, #0x2D    // '-'
    str w4, [x3]
    movz w4, #0x53    // 'S'
    str w4, [x3]
    movz w4, #0x50    // 'P'
    str w4, [x3]
    movz w4, #0x21    // '!'
    str w4, [x3]
    b run_restore     // Try to recover anyway
