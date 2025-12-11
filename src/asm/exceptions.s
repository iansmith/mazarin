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
.extern ExceptionHandler

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
    // Switch to dedicated exception stack immediately
    // Exception stack: 1KB region below main stack (0x5EFFFC00)
    // All exception handler paths are nosplit, so stack usage is bounded
    
    // Save current SP
    mov x0, sp                     // Save current SP to x0
    movz x1, #0x5EFF, lsl #16
    movk x1, #0xFC00, lsl #0       // 0x5EFFFC00 (1KB below main stack at 0x5F000000)
    mov sp, x1                     // Switch to exception stack
    
    // Save registers on exception stack
    // CRITICAL: Must save ALL callee-saved registers (x19-x28) per AArch64 PCS!
    sub sp, sp, #160              // Space for 20 registers (160 bytes)
    str x0, [sp, #0]              // Save original SP_EL1
    stp x1, x2, [sp, #8]          // Save x1, x2
    stp x3, x4, [sp, #24]         // Save x3, x4
    stp x5, x6, [sp, #40]         // Save x5, x6
    stp x19, x20, [sp, #56]       // Save callee-saved x19, x20
    stp x21, x22, [sp, #72]       // Save callee-saved x21, x22
    stp x23, x24, [sp, #88]       // Save callee-saved x23, x24
    stp x25, x26, [sp, #104]      // Save callee-saved x25, x26
    stp x27, x28, [sp, #120]      // Save callee-saved x27 and x28 (g pointer)
    stp x29, x30, [sp, #136]      // Save frame pointer and link register
    
    // Read exception registers
    mrs x0, ESR_EL1               // Exception Syndrome Register -> x0
    mrs x1, ELR_EL1                // Exception Link Register (return address) -> x1
    mrs x2, SPSR_EL1               // Saved Program Status Register -> x2
    mrs x3, FAR_EL1                // Fault Address Register -> x3
    
    // Prepare arguments for ExceptionHandler(esr, elr, spsr, far, excType)
    // x0 = ESR (already set)
    // x1 = ELR (already set)
    // x2 = SPSR (already set)
    // x3 = FAR (already set)
    // x4 = excType (SYNC_EXCEPTION = 0)
    mov x4, #0                     // excType = SYNC_EXCEPTION
    
    // Ensure SP is 16-byte aligned before calling Go function
    mov x5, sp                     // Copy SP to check alignment
    and x5, x5, #0xF               // Check alignment (lower 4 bits)
    cbnz x5, sync_sp_misaligned    // If not zero, SP is misaligned!
    
    // Call Go exception handler
    bl ExceptionHandler
    
    // ExceptionHandler should not return (it hangs), but if it does, hang here
    b .
    
sync_sp_misaligned:
    // SP was misaligned - align it and continue
    mov x5, sp                      // Copy SP to x5
    mov x6, #0xF                    // Load mask
    mvn x6, x6                      // Complement: x6 = ~0xF = 0xFFFFFFFFFFFFFFF0
    and x5, x5, x6                  // Clear lower 4 bits
    mov sp, x5                      // Restore aligned SP
    
    // Call Go exception handler with aligned SP
    bl ExceptionHandler
    
    // ExceptionHandler should not return (it hangs), but if it does, hang here
    b .
    
    
    // 0x280 - 0x300: IRQ (SP_EL1) - 128 bytes
    .align 7
irq_exception_el1:
    // PROOF: Print marker to prove we reached the interrupt handler
    // This happens IMMEDIATELY when interrupt occurs, before any stack operations
    movz x14, #0x0900, lsl #16     // UART base = 0x09000000
    movk x14, #0x0000, lsl #0
    movz w15, #0x5B                 // '[' - IRQ entry marker
    str w15, [x14]
    movz w15, #0x49                 // 'I'
    str w15, [x14]
    movz w15, #0x52                 // 'R'
    str w15, [x14]
    movz w15, #0x51                 // 'Q'
    str w15, [x14]
    movz w15, #0x5D                 // ']' - IRQ entry marker end
    str w15, [x14]
    
    // Switch to dedicated exception stack immediately
    // Exception stack: 1KB region below main stack (0x5EFFFC00)
    // All interrupt handler paths are nosplit, so stack usage is bounded
    
    // SP ALIGNMENT CHECK: Check SP alignment before saving
    // If SP is misaligned when interrupt occurs, we need to detect it
    mov x0, sp                     // Save current SP to x0 for check
    and x1, x0, #0xF               // Check alignment (lower 4 bits)
    cbnz x1, sp_misaligned_entry   // If not zero, SP is misaligned!
    
    // SP is aligned, continue normally
    mov x0, sp                     // Save current SP (restore for normal path)
    movz x1, #0x5EFF, lsl #16
    movk x1, #0xFC00, lsl #0       // 0x5EFFFC00 (1KB below main stack at 0x5F000000)
    mov sp, x1                     // Switch to exception stack
    b irq_save_registers
    
sp_misaligned_entry:
    // SP was misaligned when interrupt occurred!
    // CRITICAL: x0 contains the original misaligned SP - DON'T OVERWRITE IT!
    // We need to preserve x0 so we can restore the original SP later
    
    // Save x0 (original SP) to x2 temporarily before using registers
    mov x2, x0                     // Save original misaligned SP to x2
    
    // Print diagnostic via UART (minimal, no stack)
    movz x3, #0x0900, lsl #16      // UART base = 0x09000000
    movk x3, #0x0000, lsl #0
    
    // Print "SP-MISALIGN: IRQ entry SP=0x"
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
    movz w4, #0x49                 // 'I'
    str w4, [x3]
    movz w4, #0x52                 // 'R'
    str w4, [x3]
    movz w4, #0x51                 // 'Q'
    str w4, [x3]
    movz w4, #0x20                 // ' '
    str w4, [x3]
    movz w4, #0x65                 // 'e'
    str w4, [x3]
    movz w4, #0x6E                 // 'n'
    str w4, [x3]
    movz w4, #0x74                 // 't'
    str w4, [x3]
    movz w4, #0x72                 // 'r'
    str w4, [x3]
    movz w4, #0x79                 // 'y'
    str w4, [x3]
    
    // Switch to exception stack (we need a stack to continue)
    movz x1, #0x5EFF, lsl #16
    movk x1, #0xFC00, lsl #0       // 0x5EFFFC00
    mov sp, x1                     // Switch to exception stack
    
    // Restore x0 with original misaligned SP (from x2)
    mov x0, x2                     // Restore x0 with original SP (misaligned)
    
irq_save_registers:
    
    // Save registers on exception stack
    // CRITICAL: Must save ALL callee-saved registers (x19-x28) per AArch64 PCS!
    // x19-x27: Callee-saved registers (must be preserved)
    // x28: g pointer for Go runtime (must be preserved)
    // x29: Frame pointer (FP)
    // x30: Link register (LR)
    // Also save x0-x6 which we use in the handler
    sub sp, sp, #160              // Space for 20 registers (160 bytes)
    str x0, [sp, #0]              // Save original SP_EL1
    stp x1, x2, [sp, #8]          // Save x1, x2
    stp x3, x4, [sp, #24]         // Save x3, x4
    stp x5, x6, [sp, #40]         // Save x5, x6
    stp x19, x20, [sp, #56]       // Save callee-saved x19, x20
    stp x21, x22, [sp, #72]       // Save callee-saved x21, x22
    stp x23, x24, [sp, #88]       // Save callee-saved x23, x24
    stp x25, x26, [sp, #104]      // Save callee-saved x25, x26
    stp x27, x28, [sp, #120]      // Save callee-saved x27 and x28 (g pointer)
    stp x29, x30, [sp, #136]      // Save frame pointer and link register
    
    // Acknowledge interrupt from GIC (GICC_IAR at 0x0801000C)
    movz x1, #0x0801, lsl #16
    movk x1, #0x000C, lsl #0
    ldr w2, [x1]                  // Read IAR (acknowledges interrupt)
    and w2, w2, #0x3FF            // Mask to get interrupt ID
    
    // PROOF: Print interrupt ID to show which interrupt occurred
    movz x3, #0x0900, lsl #16     // UART base
    movk x3, #0x0000, lsl #0
    movz w4, #0x49                 // 'I'
    str w4, [x3]
    movz w4, #0x52                 // 'R'
    str w4, [x3]
    movz w4, #0x51                 // 'Q'
    str w4, [x3]
    movz w4, #0x3A                 // ':'
    str w4, [x3]
    // Print interrupt ID (print both nibbles for better debugging)
    // Print high nibble first
    lsr w5, w2, #4                // Shift right 4 bits to get high nibble
    and w5, w5, #0xF              // Mask to 4 bits
    cmp w5, #9
    b.le irq_print_high
    add w5, w5, #7                // Add 7 for A-F
irq_print_high:
    add w5, w5, #0x30             // Convert to ASCII
    str w5, [x3]                  // Print high hex digit
    // Print low nibble
    and w4, w2, #0xF              // Get low 4 bits of interrupt ID
    cmp w4, #9
    b.le irq_print_digit
    add w4, w4, #7                // Add 7 for A-F
irq_print_digit:
    add w4, w4, #0x30             // Convert to ASCII
    str w4, [x3]                  // Print low hex digit
    movz w4, #0x20                // ' '
    str w4, [x3]
    
    // Check interrupt type and handle accordingly
    cmp w2, #27                   // Timer interrupt (ID 27)?
    beq handle_timer_irq
    cmp w2, #33                   // UART interrupt (ID 33)?
    beq handle_uart_irq
    b irq_eoi                     // Unknown interrupt - just EOI
    
handle_timer_irq:
    // CRITICAL: Ensure SP is aligned before calling Go function
    // Go function prologue requires 16-byte aligned SP
    mov x3, sp                     // Copy SP to check alignment
    and x3, x3, #0xF               // Check alignment (lower 4 bits)
    cbnz x3, sp_misaligned_before_go_call  // If not zero, SP is misaligned!
    
    // Call Go timer interrupt handler (minimal nosplit function)
    bl handleTimerIRQ
    
    // CRITICAL: Check SP alignment after Go function call
    mov x3, sp                     // Copy SP to check alignment
    and x3, x3, #0xF               // Check alignment (lower 4 bits)
    cbnz x3, sp_misaligned_after_go_call  // If not zero, SP became misaligned!
    
    b irq_eoi
    
handle_uart_irq:
    // CRITICAL: Ensure SP is aligned before calling Go function
    mov x3, sp                     // Copy SP to check alignment
    and x3, x3, #0xF               // Check alignment (lower 4 bits)
    cbnz x3, sp_misaligned_before_go_call  // If not zero, SP is misaligned!
    
    // Call Go UART interrupt handler (minimal nosplit function)
    bl handleUARTIRQ
    
    // CRITICAL: Check SP alignment after Go function call
    mov x3, sp                     // Copy SP to check alignment
    and x3, x3, #0xF               // Check alignment (lower 4 bits)
    cbnz x3, sp_misaligned_after_go_call  // If not zero, SP became misaligned!
    
    b irq_eoi
    
sp_misaligned_before_go_call:
    // SP was misaligned before calling Go function!
    // Print diagnostic and align SP
    movz x2, #0x0900, lsl #16      // UART base
    movk x2, #0x0000, lsl #0
    movz w3, #0x21                 // '!'
    str w3, [x2]
    movz w3, #0x53                 // 'S'
    str w3, [x2]
    movz w3, #0x50                 // 'P'
    str w3, [x2]
    movz w3, #0x2D                 // '-'
    str w3, [x2]
    movz w3, #0x4D                 // 'M'
    str w3, [x2]
    movz w3, #0x49                 // 'I'
    str w3, [x2]
    movz w3, #0x53                 // 'S'
    str w3, [x2]
    movz w3, #0x41                 // 'A'
    str w3, [x2]
    movz w3, #0x4C                 // 'L'
    str w3, [x2]
    movz w3, #0x49                 // 'I'
    str w3, [x2]
    movz w3, #0x47                 // 'G'
    str w3, [x2]
    movz w3, #0x3A                 // ':'
    str w3, [x2]
    movz w3, #0x20                 // ' '
    str w3, [x2]
    movz w3, #0x62                 // 'b'
    str w3, [x2]
    movz w3, #0x65                 // 'e'
    str w3, [x2]
    movz w3, #0x66                 // 'f'
    str w3, [x2]
    movz w3, #0x6F                 // 'o'
    str w3, [x2]
    movz w3, #0x72                 // 'r'
    str w3, [x2]
    movz w3, #0x65                 // 'e'
    str w3, [x2]
    movz w3, #0x20                 // ' '
    str w3, [x2]
    movz w3, #0x47                 // 'G'
    str w3, [x2]
    movz w3, #0x6F                 // 'o'
    str w3, [x2]
    
    // Align SP and continue (round down to 16-byte boundary)
    mov x3, sp                      // Copy SP to x3
    mov x4, #0xF                    // Load mask
    mvn x4, x4                      // Complement: x4 = ~0xF = 0xFFFFFFFFFFFFFFF0
    and x3, x3, x4                  // Clear lower 4 bits
    mov sp, x3                      // Restore aligned SP
    b irq_eoi                       // Skip Go call, just do EOI
    
sp_misaligned_after_go_call:
    // SP became misaligned after Go function call!
    // Print diagnostic
    movz x2, #0x0900, lsl #16      // UART base
    movk x2, #0x0000, lsl #0
    movz w3, #0x21                 // '!'
    str w3, [x2]
    movz w3, #0x53                 // 'S'
    str w3, [x2]
    movz w3, #0x50                 // 'P'
    str w3, [x2]
    movz w3, #0x2D                 // '-'
    str w3, [x2]
    movz w3, #0x4D                 // 'M'
    str w3, [x2]
    movz w3, #0x49                 // 'I'
    str w3, [x2]
    movz w3, #0x53                 // 'S'
    str w3, [x2]
    movz w3, #0x41                 // 'A'
    str w3, [x2]
    movz w3, #0x4C                 // 'L'
    str w3, [x2]
    movz w3, #0x49                 // 'I'
    str w3, [x2]
    movz w3, #0x47                 // 'G'
    str w3, [x2]
    movz w3, #0x3A                 // ':'
    str w3, [x2]
    movz w3, #0x20                 // ' '
    str w3, [x2]
    movz w3, #0x61                 // 'a'
    str w3, [x2]
    movz w3, #0x66                 // 'f'
    str w3, [x2]
    movz w3, #0x74                 // 't'
    str w3, [x2]
    movz w3, #0x65                 // 'e'
    str w3, [x2]
    movz w3, #0x72                 // 'r'
    str w3, [x2]
    movz w3, #0x20                 // ' '
    str w3, [x2]
    movz w3, #0x47                 // 'G'
    str w3, [x2]
    movz w3, #0x6F                 // 'o'
    str w3, [x2]
    
    // Align SP and continue
    mov x3, sp                      // Copy SP to x3
    mov x4, #0xF                    // Load mask
    mvn x4, x4                      // Complement: x4 = ~0xF = 0xFFFFFFFFFFFFFFF0
    and x3, x3, x4                  // Clear lower 4 bits
    mov sp, x3                      // Restore aligned SP
    b irq_eoi
    
irq_eoi:
    // PROOF: Print marker to show we're about to return from interrupt
    movz x3, #0x0900, lsl #16     // UART base
    movk x3, #0x0000, lsl #0
    movz w4, #0x5B                 // '[' - IRQ exit marker
    str w4, [x3]
    movz w4, #0x2F                 // '/'
    str w4, [x3]
    movz w4, #0x49                 // 'I'
    str w4, [x3]
    movz w4, #0x52                 // 'R'
    str w4, [x3]
    movz w4, #0x51                 // 'Q'
    str w4, [x3]
    movz w4, #0x5D                 // ']' - IRQ exit marker end
    str w4, [x3]
    
    // Signal end of interrupt to GIC (GICC_EOIR at 0x08010010)
    movz x1, #0x0801, lsl #16
    movk x1, #0x0010, lsl #0
    str w2, [x1]                  // Write interrupt ID to EOIR
    
    // Restore registers and return to normal stack
    // CRITICAL: Must restore ALL callee-saved registers (x19-x28) before restoring SP!
    // Restore in reverse order of save
    ldp x29, x30, [sp, #136]      // Restore frame pointer and link register
    ldp x27, x28, [sp, #120]      // Restore callee-saved x27 and x28 (g pointer)
    ldp x25, x26, [sp, #104]      // Restore callee-saved x25, x26
    ldp x23, x24, [sp, #88]       // Restore callee-saved x23, x24
    ldp x21, x22, [sp, #72]       // Restore callee-saved x21, x22
    ldp x19, x20, [sp, #56]       // Restore callee-saved x19, x20
    ldp x5, x6, [sp, #40]         // Restore x5, x6
    ldp x3, x4, [sp, #24]         // Restore x3, x4
    ldp x1, x2, [sp, #8]          // Restore x1, x2
    ldr x0, [sp, #0]              // Load original SP_EL1
    
    // SP ALIGNMENT CHECK: Check SP alignment before restoring
    mov x3, x0                     // Copy SP to check alignment
    and x3, x3, #0xF               // Check alignment (lower 4 bits)
    cbnz x3, sp_misaligned_exit    // If not zero, SP is misaligned!
    
    // SP is aligned, restore normally
    // DEFENSIVE: Even though SP is aligned, ensure it stays aligned
    // (This shouldn't be necessary, but provides extra safety)
    add sp, sp, #160               // Restore stack (160 bytes for all registers)
    mov sp, x0                    // Restore original SP (verified aligned)
    eret
    
sp_misaligned_exit:
    // SP was misaligned when restoring!
    // Print diagnostic via UART
    movz x2, #0x0900, lsl #16      // UART base = 0x09000000
    movk x2, #0x0000, lsl #0
    
    // Print "SP-MISALIGN: IRQ exit SP=0x"
    movz w3, #0x53                 // 'S'
    str w3, [x2]
    movz w3, #0x50                 // 'P'
    str w3, [x2]
    movz w3, #0x2D                 // '-'
    str w3, [x2]
    movz w3, #0x4D                 // 'M'
    str w3, [x2]
    movz w3, #0x49                 // 'I'
    str w3, [x2]
    movz w3, #0x53                 // 'S'
    str w3, [x2]
    movz w3, #0x41                 // 'A'
    str w3, [x2]
    movz w3, #0x4C                 // 'L'
    str w3, [x2]
    movz w3, #0x49                 // 'I'
    str w3, [x2]
    movz w3, #0x47                 // 'G'
    str w3, [x2]
    movz w3, #0x3A                 // ':'
    str w3, [x2]
    movz w3, #0x20                 // ' '
    str w3, [x2]
    movz w3, #0x49                 // 'I'
    str w3, [x2]
    movz w3, #0x52                 // 'R'
    str w3, [x2]
    movz w3, #0x51                 // 'Q'
    str w3, [x2]
    movz w3, #0x20                 // ' '
    str w3, [x2]
    movz w3, #0x65                 // 'e'
    str w3, [x2]
    movz w3, #0x78                 // 'x'
    str w3, [x2]
    movz w3, #0x69                 // 'i'
    str w3, [x2]
    movz w3, #0x74                 // 't'
    str w3, [x2]
    
    // Restore registers before restoring SP (even if misaligned)
    // CRITICAL: Must restore ALL callee-saved registers even in error path!
    ldp x29, x30, [sp, #136]      // Restore frame pointer and link register
    ldp x27, x28, [sp, #120]      // Restore callee-saved x27 and x28 (g pointer)
    ldp x25, x26, [sp, #104]      // Restore callee-saved x25, x26
    ldp x23, x24, [sp, #88]       // Restore callee-saved x23, x24
    ldp x21, x22, [sp, #72]       // Restore callee-saved x21, x22
    ldp x19, x20, [sp, #56]       // Restore callee-saved x19, x20
    ldp x5, x6, [sp, #40]         // Restore x5, x6
    ldp x3, x4, [sp, #24]         // Restore x3, x4
    ldp x1, x2, [sp, #8]          // Restore x1, x2
    // x0 already contains original SP (from earlier)
    
    // CRITICAL FIX: Align SP before restoring!
    // Round down to 16-byte boundary to ensure SP is aligned
    mov x3, x0                     // Copy SP to x3
    mov x4, #0xF                   // Load mask (0xF)
    mvn x4, x4                     // Complement: x4 = ~0xF = 0xFFFFFFFFFFFFFFF0
    and x3, x3, x4                 // Clear lower 4 bits (round down to 16-byte boundary)
    mov x0, x3                     // Update x0 with aligned SP
    
    // Restore stack and SP (now guaranteed aligned)
    add sp, sp, #160               // Restore stack (160 bytes for all registers)
    mov sp, x0                    // Restore aligned SP
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

