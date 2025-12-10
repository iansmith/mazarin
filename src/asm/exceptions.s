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
// CRITICAL: Use "ax" flags to make section Allocatable and Executable
// Without these flags, the section won't be loaded into memory!
.section ".vectors", "ax", @progbits
.global exception_vectors
.global exception_vectors_start_addr

// External Go function declarations (called from interrupt handlers)
.extern fb_putc_irq
.extern handleTimerIRQ
.extern handleUARTIRQ

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
    
    // Try to read ESR_EL1 (Exception Syndrome Register) to get exception info
    // ESR bits 31:26 = EC (Exception Class), bits 25:0 = ISS (Instruction Specific Syndrome)
    mrs x15, ESR_EL1
    // Print ESR high byte (EC field)
    lsr x15, x15, #24              // Shift right 24 bits to get top 8 bits
    and x15, x15, #0xFF            // Mask to 8 bits
    // Convert to hex and print (simplified - just print low 4 bits)
    and w16, w15, #0xF             // Get low 4 bits
    cmp w16, #9
    b.le print_digit
    add w16, w16, #7               // Add 7 for A-F
print_digit:
    add w16, w16, #0x30            // Convert to ASCII
    str w16, [x14]                 // Print hex digit
    
    // Hang forever - sync exception occurred
    b .
    
    
    // 0x280 - 0x300: IRQ (SP_EL1) - 128 bytes
    .align 7
irq_exception_el1:
    // Switch to dedicated exception stack immediately
    // Exception stack: 1KB region below main stack (0x5EFFFC00)
    // All interrupt handler paths are nosplit, so stack usage is bounded
    mov x0, sp                     // Save current SP
    movz x1, #0x5EFF, lsl #16
    movk x1, #0xFC00, lsl #0       // 0x5EFFFC00 (1KB below main stack at 0x5F000000)
    mov sp, x1                     // Switch to exception stack
    
    // Save registers on exception stack
    sub sp, sp, #80               // Space for 10 registers
    str x0, [sp, #0]              // Save original SP_EL1
    stp x1, x2, [sp, #8]
    stp x3, x4, [sp, #24]
    stp x5, x6, [sp, #40]
    stp x29, x30, [sp, #56]       // Save frame pointer and link register
    
    // Acknowledge interrupt from GIC (GICC_IAR at 0x0801000C)
    movz x1, #0x0801, lsl #16
    movk x1, #0x000C, lsl #0
    ldr w2, [x1]                  // Read IAR (acknowledges interrupt)
    and w2, w2, #0x3FF            // Mask to get interrupt ID
    
    // Check interrupt type and handle accordingly
    cmp w2, #27                   // Timer interrupt (ID 27)?
    beq handle_timer_irq
    cmp w2, #33                   // UART interrupt (ID 33)?
    beq handle_uart_irq
    b irq_eoi                     // Unknown interrupt - just EOI
    
handle_timer_irq:
    // Call Go timer interrupt handler (minimal nosplit function)
    bl handleTimerIRQ
    b irq_eoi
    
handle_uart_irq:
    // Call Go UART interrupt handler (minimal nosplit function)
    bl handleUARTIRQ
    b irq_eoi
    
irq_eoi:
    // Signal end of interrupt to GIC (GICC_EOIR at 0x08010010)
    movz x1, #0x0801, lsl #16
    movk x1, #0x0010, lsl #0
    str w2, [x1]                  // Write interrupt ID to EOIR
    
    // Restore registers and return to normal stack
    ldp x29, x30, [sp, #56]
    ldp x5, x6, [sp, #40]
    ldp x3, x4, [sp, #24]
    ldp x1, x2, [sp, #8]
    ldr x0, [sp, #0]              // Load original SP_EL1
    add sp, sp, #80
    mov sp, x0                    // Restore original SP
    
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


// ============================================================================
// Exception Handler Functions
// ============================================================================
// The Go functions (IRQHandler, FIQHandler, SErrorHandler) are defined in
// exceptions.go and exported via //go:linkname. The assembly code above
// calls these Go functions directly using 'bl main.IRQHandler', etc.
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

