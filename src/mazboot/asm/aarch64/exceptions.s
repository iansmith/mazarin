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

// Declare Go function for exception handling
.extern ExceptionHandler

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
    // These are for kernel code using SP_EL0 (we don't use this)
    
    // 0x000 - 0x080: Synchronous exception (SP_EL0)
    .align 7  // 128 bytes per handler
    b .  // Hang - we don't use SP_EL0 at EL1
    
    // 0x080 - 0x100: IRQ (SP_EL0)
    .align 7
    b .  // Hang
    
    // 0x100 - 0x180: FIQ (SP_EL0)
    .align 7
    b .  // Hang
    
    // 0x180 - 0x200: SError (SP_EL0)
    .align 7
    b .  // Hang


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
    // Virtual timer interrupt - reset it for next tick using CVAL (absolute)
    // CRITICAL: Use CVAL approach like reference repo!
    // 1. Read current counter value (CNTVCT_EL0)
    mrs x3, CNTVCT_EL0
    // 2. Add 312500000 (5 seconds at 62.5MHz) to get target
    // 312500000 = 0x12A05F20
    movz x4, #0x12A0, lsl #16     // 0x12A00000
    movk x4, #0x5F20, lsl #0      // Complete to 312500000
    add x3, x3, x4                 // target = current + interval
    // 3. Write to CNTV_CVAL_EL0 (compare value)
    msr CNTV_CVAL_EL0, x3

    // Print '.' to framebuffer via Go function fb_putc_irq
    // Save additional registers needed for Go call
    sub sp, sp, #128              // Expand stack for more registers
    stp x5, x6, [sp, #64]         // Save x5, x6
    stp x7, x8, [sp, #80]         // Save x7, x8
    stp x28, x29, [sp, #96]       // Save x28 (g), x29 (FP)
    str x30, [sp, #112]           // Save x30 (LR)

    // Set up frame pointer for Go
    add x29, sp, #0

    // Call Go function fb_putc_irq('.') - pass '.' (0x2E) as first arg in x0
    movz x0, #0x2E                // '.' character
    bl main.fb_putc_irq

    // Call timerSignal() to send signal to channel for goroutine
    bl main.timerSignal

    // Restore preserved registers
    ldp x5, x6, [sp, #64]
    ldp x7, x8, [sp, #80]
    ldp x28, x29, [sp, #96]
    ldr x30, [sp, #112]
    add sp, sp, #128              // Restore stack

    // Reload UART base for final breadcrumb (optional)
    movz x0, #0x0900, lsl #16
    movk x0, #0x0000, lsl #0

    b irq_done
    
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
    
    // Reload UART base for final breadcrumb
    movz x0, #0x0900, lsl #16
    movk x0, #0x0000, lsl #0
    
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


// uint64_t read_daif(void)
// Read the DAIF register (interrupt mask bits)
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

    // Step 2: Save original SP (before we pushed) to x30
    add x30, sp, #16               // x30 = original SP (current SP + 16 for the push)

    // Step 3: Switch to exception stack
    movz x29, #0x5FFE, lsl #16     // Exception stack at 0x5FFE0000
    movk x29, #0x0000, lsl #0
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

    // For data aborts (EC=0x25), call Go handler
    cmp x4, #0x25
    bne sync_other_exception        // Not data abort - other exception

    // Data abort - this might be a demand paging request
    //
    // CRITICAL: Save x0-x7 to fixed memory BEFORE calling Go, because Go's
    // stack frame may overwrite our saved registers in the exception frame!
    // Go's prolog does `stp x29, x30, [sp, #-16]!; mov x29, sp` which sets
    // Go's frame pointer 16 bytes below our sp. Go then accesses [x29+16],
    // [x29+24], etc. for locals, which maps to our [sp+0], [sp+8], etc.!
    movz x8, #0x4000, lsl #16
    movk x8, #0x0FE0, lsl #0        // x8 = 0x40000FE0
    ldp x5, x6, [sp, #0]            // x5 = saved x0, x6 = saved x1
    stp x5, x6, [x8]                // Store to fixed memory at +0, +8
    ldp x5, x6, [sp, #16]           // x5 = saved x2, x6 = saved x3
    stp x5, x6, [x8, #16]           // Store at +16, +24
    ldp x5, x6, [sp, #32]           // x5 = saved x4, x6 = saved x5
    stp x5, x6, [x8, #32]           // Store at +32, +40
    ldp x5, x6, [sp, #48]           // x5 = saved x6, x6 = saved x7
    stp x5, x6, [x8, #48]           // Store at +48, +56
    dsb sy                          // Ensure stores complete before Go runs

    // Set up frame pointer for Go
    add x29, sp, #0

    // Prepare arguments for Go exception handler
    // x0 = ESR, x1 = ELR, x2 = SPSR, x3 = FAR, x4 = excType
    ldp x1, x2, [sp, #256]          // x1 = ELR, x2 = SPSR
    ldp x3, x0, [sp, #272]          // x3 = FAR, x0 = ESR (note: reversed order)
    movz x4, #0                     // excType = SYNC_EXCEPTION (0)

    // Call Go exception handler
    bl main.ExceptionHandler

    // Go handler returned - this means page fault was handled
    // Restore ALL registers and retry faulting instruction
    //
    // CRITICAL: Must restore ELR_EL1, SPSR_EL1, and SP before eret!
    //
    // Strategy: Use fixed memory (0x40000FE0) to save x0/x1 for final restoration

    // Step 1: Restore ELR_EL1 and SPSR_EL1 while still on exception stack
    ldp x0, x1, [sp, #256]          // x0 = saved ELR, x1 = saved SPSR
    msr ELR_EL1, x0                 // Restore return address
    msr SPSR_EL1, x1                // Restore saved PSTATE
    isb                             // Ensure ELR/SPSR writes complete

    // Step 2: x0-x7 were already saved to fixed memory (0x40000FE0) BEFORE calling Go!
    // We don't need to re-read from the exception frame (which Go may have corrupted).
    // Just restore x8-x28 from exception frame, then restore x0-x7 from fixed memory.

    // Step 3: Restore x8-x28 from exception frame (Go doesn't corrupt these)
    // Note: x0-x7 will be restored from fixed memory after stack switch
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

    // Step 4: Restore x29, x30 from exception frame
    ldp x29, x30, [sp, #232]        // x29 = original x29, x30 = original x30

    // Step 5: Switch to kernel stack
    ldr x0, [sp, #248]              // x0 = original kernel SP
    mov sp, x0                      // SP = original kernel SP

    // Step 6: Load original x0-x7 from fixed memory (stack is now kernel stack)
    // Use x7 as scratch to load the fixed memory address, then restore x7 last
    movz x7, #0x4000, lsl #16
    movk x7, #0x0FE0, lsl #0        // x7 = 0x40000FE0
    ldp x0, x1, [x7]                // x0 = original x0, x1 = original x1
    ldp x2, x3, [x7, #16]           // x2 = original x2, x3 = original x3
    ldp x4, x5, [x7, #32]           // x4 = original x4, x5 = original x5
    ldr x6, [x7, #48]               // x6 = original x6
    ldr x7, [x7, #56]               // x7 = original x7 (self-overwriting load)

    // Return from exception to retry the faulting instruction
    eret

sync_unknown_exception:
    // EC=0x00 - Unknown exception (often NULL pointer or undefined instruction)
    // Print diagnostic information directly to UART and hang
    // Don't try to return - this would create an infinite exception loop

    // Load UART base
    movz x0, #0x0900, lsl #16
    movk x0, #0x0000, lsl #0        // x0 = 0x09000000

    // Print "\r\n!UNKNOWN EXCEPTION (EC=0x00)!\r\n"
    movz w1, #0x000D                // '\r'
    str w1, [x0]
    movz w1, #0x000A                // '\n'
    str w1, [x0]
    movz w1, #0x0021                // '!'
    str w1, [x0]
    movz w1, #0x0055                // 'U'
    str w1, [x0]
    movz w1, #0x004E                // 'N'
    str w1, [x0]
    movz w1, #0x004B                // 'K'
    str w1, [x0]
    movz w1, #0x004E                // 'N'
    str w1, [x0]
    movz w1, #0x004F                // 'O'
    str w1, [x0]
    movz w1, #0x0057                // 'W'
    str w1, [x0]
    movz w1, #0x004E                // 'N'
    str w1, [x0]
    movz w1, #0x0020                // ' '
    str w1, [x0]
    movz w1, #0x0045                // 'E'
    str w1, [x0]
    movz w1, #0x0058                // 'X'
    str w1, [x0]
    movz w1, #0x0043                // 'C'
    str w1, [x0]
    movz w1, #0x0045                // 'E'
    str w1, [x0]
    movz w1, #0x0050                // 'P'
    str w1, [x0]
    movz w1, #0x0054                // 'T'
    str w1, [x0]
    movz w1, #0x0049                // 'I'
    str w1, [x0]
    movz w1, #0x004F                // 'O'
    str w1, [x0]
    movz w1, #0x004E                // 'N'
    str w1, [x0]
    movz w1, #0x0021                // '!'
    str w1, [x0]
    movz w1, #0x000D                // '\r'
    str w1, [x0]
    movz w1, #0x000A                // '\n'
    str w1, [x0]

    // Print "ELR=0x" and the ELR value (from [sp, #256])
    movz w1, #0x0045                // 'E'
    str w1, [x0]
    movz w1, #0x004C                // 'L'
    str w1, [x0]
    movz w1, #0x0052                // 'R'
    str w1, [x0]
    movz w1, #0x003D                // '='
    str w1, [x0]
    movz w1, #0x0030                // '0'
    str w1, [x0]
    movz w1, #0x0078                // 'x'
    str w1, [x0]

    // Print ELR as hex (16 hex digits)
    ldr x2, [sp, #256]              // x2 = ELR
    mov x3, #60                      // shift count (60, 56, ... 0)
1:  lsr x4, x2, x3                  // Extract nibble
    and w4, w4, #0xF
    cmp w4, #10
    blt 2f
    add w4, w4, #55                 // 'A'-10 = 55
    b 3f
2:  add w4, w4, #48                 // '0'
3:  str w4, [x0]
    subs x3, x3, #4
    bpl 1b

    // Print "\r\nFAR=0x"
    movz w1, #0x000D                // '\r'
    str w1, [x0]
    movz w1, #0x000A                // '\n'
    str w1, [x0]
    movz w1, #0x0046                // 'F'
    str w1, [x0]
    movz w1, #0x0041                // 'A'
    str w1, [x0]
    movz w1, #0x0052                // 'R'
    str w1, [x0]
    movz w1, #0x003D                // '='
    str w1, [x0]
    movz w1, #0x0030                // '0'
    str w1, [x0]
    movz w1, #0x0078                // 'x'
    str w1, [x0]

    // Print FAR as hex
    ldr x2, [sp, #272]              // x2 = FAR
    mov x3, #60
4:  lsr x4, x2, x3
    and w4, w4, #0xF
    cmp w4, #10
    blt 5f
    add w4, w4, #55
    b 6f
5:  add w4, w4, #48
6:  str w4, [x0]
    subs x3, x3, #4
    bpl 4b

    // Print final newline
    movz w1, #0x000D                // '\r'
    str w1, [x0]
    movz w1, #0x000A                // '\n'
    str w1, [x0]

    // Hang forever
    b .

sync_other_exception:
    // Other exception type - forward to Go handler but don't expect to return
    add x29, sp, #0
    ldp x1, x2, [sp, #256]
    ldp x3, x0, [sp, #272]
    movz x4, #0
    bl main.ExceptionHandler
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

    // Step 2: Save original x0, x29/x30 to scratch area (0x40FFF020) before we lose the frame
    // IMPORTANT: Use x10 as scratch pointer, NOT x0 (x0 contains syscall argument!)
    ldp x29, x30, [sp, #232]        // x29 = original x29, x30 = original x30
    movz x10, #0x40FF, lsl #16      // Scratch area at 0x40FFF020
    movk x10, #0xF020, lsl #0       // x10 = 0x40FFF020
    str x0, [x10, #16]              // Save original x0 at 0x40FFF030 (syscall argument!)
    stp x29, x30, [x10]             // Save original x29/x30 at 0x40FFF020

    // Step 3: Load original SP and switch to kernel stack
    // original_SP was saved at [sp, #248] - this is SP BEFORE we pushed x29/x30
    ldr x29, [sp, #248]             // x29 = original kernel SP
    mov sp, x29                     // Switch to kernel stack

    // Step 4: Restore x0, x29/x30 from scratch area
    movz x29, #0x40FF, lsl #16      // Scratch area at 0x40FFF020
    movk x29, #0xF020, lsl #0
    ldr x0, [x29, #16]              // Restore original x0 from 0x40FFF030
    ldp x29, x30, [x29]             // Restore original x29/x30

    // Now x0, x29, x30 are restored and SP = original SP (before SVC)
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

    // DEBUG: Syscall number printing disabled to see Go error message

    // Dispatch based on syscall number
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
    cmp x8, #131                   // tgkill syscall
    beq syscall_success
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
    cmp x8, #220                   // clone syscall
    beq syscall_clone_fail
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
    // x1 = buf pointer, x2 = count
    // Call Go function: SyscallWriteBuffer(buf unsafe.Pointer, count uint32) uint32

    // Save callee-saved registers for Go call
    sub sp, sp, #64
    stp x19, x20, [sp, #0]
    stp x21, x22, [sp, #16]
    stp x28, x29, [sp, #32]
    str x30, [sp, #48]

    // Set up frame pointer for Go
    add x29, sp, #0

    // Prepare arguments for SyscallWriteBuffer
    mov x0, x1                     // x0 = buf pointer
    mov w1, w2                     // x1 = count (32-bit)

    // Call Go function
    bl main.SyscallWriteBuffer

    // x0 now contains return value (count written)
    // Save return value to stack temporarily
    str x0, [sp, #56]

    // Restore callee-saved registers
    ldp x19, x20, [sp, #0]
    ldp x21, x22, [sp, #16]
    ldp x28, x29, [sp, #32]
    ldr x30, [sp, #48]
    ldr x0, [sp, #56]              // Restore return value to x0
    add sp, sp, #64

    b syscall_return

syscall_read:
    // read(fd, buf, count)
    // x0 = fd, x1 = buf, x2 = count
    // Call Go function to handle special FDs like /dev/urandom

    // Save callee-saved registers for Go call
    sub sp, sp, #64
    stp x19, x20, [sp, #0]
    stp x21, x22, [sp, #16]
    stp x28, x29, [sp, #32]
    str x30, [sp, #48]

    // Set up frame pointer for Go
    add x29, sp, #0

    // Arguments are already in x0, x1, x2 - perfect for Go
    // x0 = fd (int32)
    // x1 = buf (unsafe.Pointer)
    // x2 = count (uint64)

    // Call Go function
    bl main.SyscallRead

    // x0 now contains return value (int64: bytes read or -errno)
    // Save return value to stack temporarily
    str x0, [sp, #56]

    // Restore callee-saved registers
    ldp x19, x20, [sp, #0]
    ldp x21, x22, [sp, #16]
    ldp x28, x29, [sp, #32]
    ldr x30, [sp, #48]
    ldr x0, [sp, #56]              // Restore return value to x0
    add sp, sp, #64

    b syscall_return

syscall_openat:
    // openat(dirfd, pathname, flags, mode)
    // x0 = dirfd, x1 = pathname, x2 = flags, x3 = mode
    // Call Go function to check for special files

    // Save callee-saved registers for Go call
    sub sp, sp, #64
    stp x19, x20, [sp, #0]
    stp x21, x22, [sp, #16]
    stp x28, x29, [sp, #32]
    str x30, [sp, #48]

    // Set up frame pointer for Go
    add x29, sp, #0

    // Arguments are already in x0, x1, x2, x3 - perfect for Go
    // x0 = dirfd (int32)
    // x1 = pathname (unsafe.Pointer - *byte)
    // x2 = flags (int32)
    // x3 = mode (int32)

    // Call Go function
    bl main.SyscallOpenat

    // x0 now contains return value (int64: fd or -errno)
    // Save return value to stack temporarily
    str x0, [sp, #56]

    // Restore callee-saved registers
    ldp x19, x20, [sp, #0]
    ldp x21, x22, [sp, #16]
    ldp x28, x29, [sp, #32]
    ldr x30, [sp, #48]
    ldr x0, [sp, #56]              // Restore return value to x0
    add sp, sp, #64

    b syscall_return

syscall_futex:
    // futex(uaddr, futex_op, val, timeout, uaddr2, val3)
    // x0 = uaddr (uint32*), x1 = futex_op (int32), x2 = val (uint32)
    // x3 = timeout (*timespec), x4 = uaddr2 (uint32*), x5 = val3 (uint32)
    // Call Go function to implement futex wait/wake

    // Save callee-saved registers for Go call
    sub sp, sp, #80
    stp x19, x20, [sp, #0]
    stp x21, x22, [sp, #16]
    stp x28, x29, [sp, #32]
    str x30, [sp, #48]

    // Set up frame pointer for Go
    add x29, sp, #0

    // Arguments are already in x0-x5 - perfect for Go calling convention
    // x0 = addr (unsafe.Pointer)
    // x1 = op (int32)
    // x2 = val (uint32)
    // x3 = ts (unsafe.Pointer)
    // x4 = addr2 (unsafe.Pointer)
    // x5 = val3 (uint32)

    // Call Go function
    bl main.SyscallFutex

    // x0 now contains return value (int64: 0 on success, -errno on error)
    // Save return value to stack temporarily
    str x0, [sp, #56]

    // Restore callee-saved registers
    ldp x19, x20, [sp, #0]
    ldp x21, x22, [sp, #16]
    ldp x28, x29, [sp, #32]
    ldr x30, [sp, #48]
    ldr x0, [sp, #56]              // Restore return value to x0
    add sp, sp, #80

    b syscall_return

syscall_close:
    // close(fd)
    // x0 = fd
    // Call Go function for debug visibility

    // Save callee-saved registers for Go call
    sub sp, sp, #64
    stp x19, x20, [sp, #0]
    stp x21, x22, [sp, #16]
    stp x28, x29, [sp, #32]
    str x30, [sp, #48]

    // Set up frame pointer for Go
    add x29, sp, #0

    // Argument already in x0 (fd)
    bl main.SyscallClose

    // x0 contains return value
    str x0, [sp, #56]

    // Restore registers
    ldp x19, x20, [sp, #0]
    ldp x21, x22, [sp, #16]
    ldp x28, x29, [sp, #32]
    ldr x30, [sp, #48]
    ldr x0, [sp, #56]
    add sp, sp, #64

    b syscall_return

syscall_success:
    // Generic success return
    mov x0, #0
    b syscall_return

syscall_clone_fail:
    // clone - return -EAGAIN (can't create new thread)
    movn x0, #10                   // x0 = -11 (EAGAIN)
    b syscall_return

syscall_mmap:
    // mmap(addr, length, prot, flags, fd, offset)
    // x0 = addr (hint or 0), x1 = length, x2 = prot, x3 = flags, x4 = fd, x5 = offset
    //
    // Go's sysReserve passes a HINT address and expects to get it back exactly.
    // If we return a different address, Go frees it and tries next hint (forever).
    //
    // Strategy:
    //   - If hint (x0) is non-zero: return hint address (bare-metal has no competition)
    //   - If hint is zero: use bump allocator
    //   - MAP_FIXED (0x10): always return requested address
    //
    // Return value in x0:
    //   - On success: allocated address (positive)
    //   - On error: -errno (negative, e.g., -12 for ENOMEM)

    // DEBUG: mmap counter removed - found only ~20 calls before hang

    // Check for MAP_FIXED (0x10) first - always return requested address
    and x15, x3, #0x10
    cbnz x15, mmap_return_hint

    // Check if hint address (x0) is non-zero
    // Go's sysReserve passes hints like 0x4000000000 (256GB) for arm64
    // We must return EXACTLY this address, or Go will free and retry
    cbnz x0, mmap_return_hint

    // No hint - use bump allocator for anonymous mappings
    // Load current mmap pointer (stored at 0x40FFF000)
    // NOTE: This pointer is pre-initialized to 0x48000000 in boot.s (within heap!)
    movz x11, #0x40FF, lsl #16     // 0x40FF0000
    movk x11, #0xF000, lsl #0      // 0x40FFF000
    ldr x12, [x11]                 // x12 = current mmap pointer

    // Align length to page size (4KB)
    mov x17, x1                    // x17 = length
    add x17, x17, #0xFFF
    and x17, x17, #~0xFFF

    // Check if we have enough space (end at 0x100000000 - 4GB mapped heap limit)
    // We have 8GB QEMU RAM (0x40000000-0x240000000), so use generous limit
    // This allows plenty of room for GC metadata and other runtime allocations
    add x13, x12, x17              // x13 = new pointer after allocation
    movz x14, #0x0001, lsl #32     // 0x100000000 - end at 4GB
    cmp x13, x14
    bhi mmap_oom

    // Allocate: return current pointer, update stored pointer
    mov x0, x12                    // x0 = allocated address
    str x13, [x11]                 // Update bump pointer
    b syscall_return

mmap_return_hint:
    // Return the hint address (x0) as-is
    // This satisfies Go's sysReserve which expects exact address match
    // Demand paging will handle actual memory backing when accessed

    // DEBUG: 'H' marker disabled to see Go error message

    b syscall_return

mmap_oom:
    // Out of memory - return ENOMEM
    movn x0, #11                   // x0 = -12 (ENOMEM)
    b syscall_return

syscall_prctl:
    // prctl - return success (debug output disabled)
    mov x0, #0
    b syscall_return

syscall_getrandom:
    // getrandom(void *buf, size_t buflen, unsigned int flags)
    // x0 = buf, x1 = buflen, x2 = flags
    //
    // Call Go getRandomBytes() function to fill buffer with random data

    // Save callee-saved registers for Go call
    sub sp, sp, #64
    stp x19, x20, [sp, #0]
    stp x21, x22, [sp, #16]
    stp x28, x29, [sp, #32]
    str x30, [sp, #48]

    // Set up frame pointer for Go
    add x29, sp, #0

    // Arguments already in x0 (buf), x1 (length)
    // x0 = unsafe.Pointer (buf)
    // x1 = uint32 (length) - convert x1 (uint64) to w1 (uint32)
    mov w1, w1

    // Call Go getRandomBytes(buf, length) -> returns uint32
    bl main.getRandomBytes

    // x0 contains return value (bytes written)
    str x0, [sp, #56]

    // Restore registers
    ldp x19, x20, [sp, #0]
    ldp x21, x22, [sp, #16]
    ldp x28, x29, [sp, #32]
    ldr x30, [sp, #48]
    ldr x0, [sp, #56]
    add sp, sp, #64

    b syscall_return

syscall_sched_getaffinity:
    // sched_getaffinity(pid, cpusetsize, mask)
    // x0 = pid, x1 = cpusetsize, x2 = mask pointer
    // Call Go function to implement this

    // Save callee-saved registers for Go call
    sub sp, sp, #64
    stp x19, x20, [sp, #0]
    stp x21, x22, [sp, #16]
    stp x28, x29, [sp, #32]
    str x30, [sp, #48]

    // Set up frame pointer for Go
    add x29, sp, #0

    // Arguments are already in x0, x1, x2 - perfect for Go calling convention
    // x0 = pid (int32)
    // x1 = cpusetsize (uint64)
    // x2 = mask (unsafe.Pointer)

    // Call Go function
    bl main.SyscallSchedGetaffinity

    // x0 now contains return value (int64: bytes written or -errno)
    // Save return value to stack temporarily
    str x0, [sp, #56]

    // Restore callee-saved registers
    ldp x19, x20, [sp, #0]
    ldp x21, x22, [sp, #16]
    ldp x28, x29, [sp, #32]
    ldr x30, [sp, #48]
    ldr x0, [sp, #56]              // Restore return value to x0
    add sp, sp, #64

    b syscall_return

syscall_clock_gettime:
    // clock_gettime(clockid, timespec*)
    // x0 = clockid (CLOCK_REALTIME=0, CLOCK_MONOTONIC=1)
    // x1 = pointer to timespec {tv_sec, tv_nsec}
    // Return: 0 on success
    // Read ARM generic timer counter for monotonic time
    mrs x9, CNTVCT_EL0             // Virtual counter value
    mrs x10, CNTFRQ_EL0            // Counter frequency (ticks per second)

    // Convert ticks to seconds and nanoseconds
    // seconds = ticks / freq
    udiv x11, x9, x10              // x11 = seconds

    // nsec = (ticks % freq) * 1000000000 / freq
    msub x12, x11, x10, x9         // x12 = ticks % freq (remainder)
    movz x13, #0x3B9A, lsl #16     // 0x3B9A0000
    movk x13, #0xCA00, lsl #0      // 0x3B9ACA00 = 1000000000 (1 billion)
    mul x12, x12, x13              // x12 = remainder * 1e9
    udiv x12, x12, x10             // x12 = nanoseconds

    // Store to timespec if pointer is valid
    cbz x1, 1f
    str x11, [x1, #0]              // tv_sec
    str x12, [x1, #8]              // tv_nsec
1:
    mov x0, #0                     // success
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
    // brk(addr) - return current break (fake implementation)
    // x0 = requested break address (0 = query current)
    // For Go runtime's sbrk0(), just return a fixed address in heap region
    // Return: new break on success, -1 on failure
    movz x0, #0x5000, lsl #16      // Return 0x50000000 as current break
    movk x0, #0x0000, lsl #0
    b syscall_return

syscall_munmap:
    // munmap(addr, length) - free mapped memory
    // For our simple bump allocator, we don't actually reclaim memory
    // Just return success (0)
    mov x0, #0
    b syscall_return

syscall_madvise:
    // madvise(addr, length, advice) - give advice about memory usage
    // Just return success (0) - we don't actually do anything
    mov x0, #0
    b syscall_return

syscall_exit:
    // exit/exit_group - print debug info and exit via semihosting
    // At this point:
    //   x8 = syscall number (93 or 94)
    //   x0 = first argument (exit code)
    //   x1-x5 = other arguments (unused for exit)

    // Save registers we need for printing
    mov x19, x0                    // Save original x0 (exit code)
    mov x20, x8                    // Save syscall number

    movz x9, #0x0900, lsl #16      // UART base
    movk x9, #0x0000, lsl #0

    // Print "\r\n*** EXIT ***\r\n" marker
    movz w10, #0x0D                // '\r'
    str w10, [x9]
    movz w10, #0x0A                // '\n'
    str w10, [x9]
    movz w10, #0x2A                // '*'
    str w10, [x9]
    str w10, [x9]
    str w10, [x9]
    movz w10, #0x20                // ' '
    str w10, [x9]
    movz w10, #0x45                // 'E'
    str w10, [x9]
    movz w10, #0x58                // 'X'
    str w10, [x9]
    movz w10, #0x49                // 'I'
    str w10, [x9]
    movz w10, #0x54                // 'T'
    str w10, [x9]
    movz w10, #0x20                // ' '
    str w10, [x9]
    movz w10, #0x2A                // '*'
    str w10, [x9]
    str w10, [x9]
    str w10, [x9]
    movz w10, #0x0D                // '\r'
    str w10, [x9]
    movz w10, #0x0A                // '\n'
    str w10, [x9]

    // Print "syscall="
    movz w10, #0x73                // 's'
    str w10, [x9]
    movz w10, #0x79                // 'y'
    str w10, [x9]
    movz w10, #0x73                // 's'
    str w10, [x9]
    movz w10, #0x3D                // '='
    str w10, [x9]

    // Print syscall number (x20) as hex
    mov x14, x20
    mov x16, #28                   // 8 hex digits
print_syscall_num_loop:
    lsr x17, x14, x16
    and x17, x17, #0xF
    add x17, x17, #0x30
    cmp x17, #0x3A
    blo print_syscall_digit
    add x17, x17, #7
print_syscall_digit:
    str w17, [x9]
    subs x16, x16, #4
    bpl print_syscall_num_loop

    // Print " x0="
    movz w10, #0x20                // ' '
    str w10, [x9]
    movz w10, #0x78                // 'x'
    str w10, [x9]
    movz w10, #0x30                // '0'
    str w10, [x9]
    movz w10, #0x3D                // '='
    str w10, [x9]

    // Print x0 (exit code, x19) as 16-digit hex
    mov x14, x19
    mov x16, #60
print_exit_code_loop:
    lsr x17, x14, x16
    and x17, x17, #0xF
    add x17, x17, #0x30
    cmp x17, #0x3A
    blo print_exit_digit
    add x17, x17, #7
print_exit_digit:
    str w17, [x9]
    subs x16, x16, #4
    bpl print_exit_code_loop

    // Print newline
    movz w10, #0x0D                // '\r'
    str w10, [x9]
    movz w10, #0x0A                // '\n'
    str w10, [x9]

    // Exit via semihosting
    // Use exit code 0 since the x0 value looks corrupted
    mov x0, #2                     // Exit code 2 (to indicate abnormal exit)
    movz x1, #0x0002, lsl #16      // ADP_Stopped_ApplicationExit = 0x20026
    movk x1, #0x0026, lsl #0
    stp x1, x0, [sp, #-16]!        // Push exit reason and code
    mov x1, sp                     // x1 = pointer to parameter block
    movz w0, #0x18                 // SYS_EXIT operation
    hlt #0xF000                    // Semihosting call

    // If semihosting doesn't work, hang
1:  wfi
    b 1b

syscall_return:
    // Minimal syscall return - just advance ELR and eret
    // x0 contains the syscall result
    //
    // After eret, Go's code (e.g., sysMmap.abi0) will:
    //   1. Check x0 (cmn x0, #0xfff)
    //   2. Store results to stack
    //   3. Return to caller

    // Debug output disabled - was too noisy during panic

    // DEBUG: Removed x25 and 'R' markers to reduce noise

    // BUG FIX: ELR_EL1 is already pointing to the instruction AFTER the SVC!
    // (This is unusual - ARMv8 spec says ELR should point to the SVC itself)
    // So we do NOT add 4 - just return directly to where ELR points.
    // This lets sysMmap.abi0's cmn/branch/store logic execute correctly.

    // Return from exception (ELR now correct)
    eret

