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
.section ".vectors"
.global exception_vectors

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
    // Save context
    // We need to save registers that we use in the handler
    stp x0, x1, [sp, #-16]!
    stp x2, x3, [sp, #-16]!
    stp x4, x5, [sp, #-16]!
    
    // Save exception info for debugging
    mrs x0, ESR_EL1      // Exception Syndrome Register
    mrs x1, ELR_EL1      // Exception Link Register (return address)
    mrs x2, SPSR_EL1     // Saved Program Status Register
    mrs x3, FAR_EL1      // Fault Address Register
    
    // Call Go exception handler with exception info
    // Parameters: x0=ESR, x1=ELR, x2=SPSR, x3=FAR, x4=exception_type
    mov x4, #0           // SYNC_EXCEPTION type
    bl main.ExceptionHandler
    
    // Restore context
    ldp x4, x5, [sp], #16
    ldp x2, x3, [sp], #16
    ldp x0, x1, [sp], #16
    
    // Return from exception
    eret
    
    
    // 0x280 - 0x300: IRQ (SP_EL1) - 128 bytes
    .align 7
irq_exception_el1:
    // Save context - minimal for IRQ
    stp x0, x1, [sp, #-16]!
    stp x2, x3, [sp, #-16]!
    stp x4, x5, [sp, #-16]!
    stp x30, xzr, [sp, #-16]!  // Save LR
    
    // Call Go IRQ handler
    // The handler will:
    // 1. Read GIC to acknowledge interrupt
    // 2. Dispatch to specific IRQ handler
    // 3. Signal end of interrupt to GIC
    bl main.IRQHandler
    
    // Restore context
    ldp x30, xzr, [sp], #16
    ldp x4, x5, [sp], #16
    ldp x2, x3, [sp], #16
    ldp x0, x1, [sp], #16
    
    eret
    
    
    // 0x300 - 0x380: FIQ (SP_EL1) - 128 bytes
    .align 7
fiq_exception_el1:
    // Save minimal context
    stp x0, x1, [sp, #-16]!
    stp x2, x3, [sp, #-16]!
    
    // FIQ handler (not commonly used, just log for now)
    bl main.FIQHandler
    
    ldp x2, x3, [sp], #16
    ldp x0, x1, [sp], #16
    
    eret
    
    
    // 0x380 - 0x400: SError (SP_EL1) - 128 bytes
    .align 7
serror_exception_el1:
    // Save minimal context
    stp x0, x1, [sp, #-16]!
    stp x2, x3, [sp, #-16]!
    
    // SError handler
    bl main.SErrorHandler
    
    ldp x2, x3, [sp], #16
    ldp x0, x1, [sp], #16
    
    eret


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
// Exception Handler Stubs
// ============================================================================
// These are minimal stubs that ensure the symbols exist and can be linked.
// They are never called - the assembly exception handlers above call the
// functions directly through Go's calling convention.

.global main.ExceptionHandler
main.ExceptionHandler:
    b .  // Never reached - for linking purposes only

.global main.IRQHandler
main.IRQHandler:
    b .  // Never reached - for linking purposes only

.global main.FIQHandler
main.FIQHandler:
    b .  // Never reached - for linking purposes only

.global main.SErrorHandler
main.SErrorHandler:
    b .  // Never reached - for linking purposes only


// ============================================================================
// Set VBAR_EL1 (Vector Base Address Register)
// ============================================================================
// This function is called from Go to set up the exception vector table
.global set_vbar_el1
set_vbar_el1:
    // x0 = address of exception vector table
    msr VBAR_EL1, x0
    isb  // Ensure instruction synchronization
    ret


// ============================================================================
// Enable/Disable IRQs
// ============================================================================

// void enable_irqs(void)
// Clears the I bit in PSTATE (bit 7) to enable IRQ interrupts
.global enable_irqs
enable_irqs:
    msr DAIFCLR, #2  // Clear I bit (bit 1 in DAIF set) = enable IRQs
    ret


// void disable_irqs(void)
// Sets the I bit in PSTATE (bit 7) to disable IRQ interrupts
.global disable_irqs
disable_irqs:
    msr DAIFSET, #2  // Set I bit = disable IRQs
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
