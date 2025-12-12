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
    .align 7
sync_exception_el1:
    // Sync exception occurred - try to print debug message via UART
    // Load UART base address (PL011 at 0x09000000)
    movz x14, #0x0900, lsl #16     // 0x09000000
    movk x14, #0x0000, lsl #0
    
    // Print 'S' (0x53) to indicate sync exception
    movz w15, #0x53                // 'S'
    str w15, [x14]                 // Write to UART data register
    
    // Print 'Y' (0x59)
    movz w15, #0x59                // 'Y'
    str w15, [x14]
    
    // Print 'N' (0x4E)
    movz w15, #0x4E                // 'N'
    str w15, [x14]
    
    // Print 'C' (0x43)
    movz w15, #0x43                // 'C'
    str w15, [x14]
    
    // Hang forever - sync exception occurred
    b .
    
    
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
    
    // Print 'X' immediately (w11 now has interrupt ID, x10 free)
    movz x10, #0x0900, lsl #16    // UART base
    movk x10, #0x0000, lsl #0
    movz w12, #0x58               // 'X'
    str w12, [x10]
    
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
    
    // Print 'I' to show we're processing
    movz w1, #0x49                // 'I'
    str w1, [x0]
    
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
    // 2. Add 62500000 (1 second at 62.5MHz) to get target
    movz x4, #0x03B9, lsl #16     // 0x03B90000
    movk x4, #0xACA0, lsl #0      // Complete to 62500000
    add x3, x3, x4                 // target = current + interval
    // 3. Write to CNTV_CVAL_EL0 (compare value)
    msr CNTV_CVAL_EL0, x3
    
    // Print 'T' to show we reset timer
    movz w1, #0x54                // 'T'
    str w1, [x0]
    b irq_done
    
handle_uart_irq:
    // UART transmit ready interrupt
    // Print 'U' to show UART interrupt
    movz w1, #0x55                // 'U'
    str w1, [x0]
    
    // Call Go function main.uartTransmitHandler()
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
    bl main.uartTransmitHandler
    
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
    movz x1, #0x0801, lsl #16
    movk x1, #0x0010, lsl #0
    str w2, [x1]                  // Write interrupt ID to EOIR
    
    // Print '>' to show we're returning
    movz w1, #0x3E                // '>'
    str w1, [x0]
    
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
// Go functions called from assembly (e.g., uartTransmitHandler) are defined
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

