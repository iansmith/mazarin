.section ".text"

// mmio_write(uintptr_t reg, uint32_t data)
// x0 = register address, w1 = data (32-bit)
.global mmio_write
mmio_write:
    str w1, [x0]        // Store 32-bit value from w1 to address in x0
    ret                 // Return

// mmio_read(uintptr_t reg)
// x0 = register address, returns uint32_t in w0
.global mmio_read
mmio_read:
    ldr w0, [x0]        // Load 32-bit value from address in x0 to w0
    ret                 // Return (value already in w0)

// mmio_write16(uintptr_t reg, uint16_t data)
// x0 = register address, w1 = data (16-bit, zero-extended to 32-bit)
.global mmio_write16
mmio_write16:
    strh w1, [x0]       // Store 16-bit value from w1 to address in x0
    ret                 // Return

// mmio_read16(uintptr_t reg)
// x0 = register address, returns uint16_t in w0 (zero-extended to 32-bit)
.global mmio_read16
mmio_read16:
    ldrh w0, [x0]       // Load 16-bit value from address in x0 to w0
    ret                 // Return (value already in w0)

// delay(int32_t count)
// w0 = count (32-bit signed integer)
.global delay
delay:
    cbz w0, delay_done  // If count is zero, skip loop
delay_loop:
    subs w0, w0, #1     // Decrement count
    bne delay_loop      // Branch if not zero
delay_done:
    ret                 // Return

// busy_wait(uint32_t count)
// w0 = count (32-bit unsigned integer)
// Simple busy wait loop - cannot be optimized away
.global busy_wait
busy_wait:
    cbz w0, busy_wait_done  // If count is zero, skip loop
busy_wait_loop:
    subs w0, w0, #1         // Decrement count
    bne busy_wait_loop      // Branch if not zero
busy_wait_done:
    ret                     // Return

// mmio_write64(uintptr_t reg, uint64_t data)
// x0 = register address, x1 = data (64-bit)
.global mmio_write64
mmio_write64:
    str x1, [x0]        // Store 64-bit value from x1 to address in x0
    ret                 // Return

// bzero(void *ptr, uint32_t size)
// x0 = pointer to memory (64-bit), w1 = size in bytes (32-bit unsigned)
// Zeroes size bytes starting at ptr
.global bzero
bzero:
    cbz w1, bzero_done  // If size is zero, skip loop
    mov w2, #0          // Zero value to write
bzero_loop:
    strb w2, [x0], #1   // Store byte (zero) and post-increment pointer
    subs w1, w1, #1     // Decrement size counter
    bne bzero_loop      // Branch if not zero
bzero_done:
    ret                 // Return

// dsb() - Data Synchronization Barrier
// Ensures all memory accesses before this instruction complete before continuing
.global dsb
dsb:
    dsb sy              // Data Synchronization Barrier - system-wide
    ret                  // Return

// get_stack_pointer() - Returns current stack pointer value
// Returns uintptr_t (64-bit) in x0
.global get_stack_pointer
get_stack_pointer:
    mov x0, sp           // Move stack pointer to x0 (return value)
    ret                  // Return

// set_stack_pointer(sp uintptr) - Sets stack pointer register
// x0 = new stack pointer value
.global set_stack_pointer
set_stack_pointer:
    mov sp, x0           // Set stack pointer from x0
    dsb sy               // Memory barrier to ensure SP update is visible
    ret                  // Return

// qemu_exit() - Exit QEMU using semihosting
// This function uses the QEMU semihosting interface to cleanly exit
// Requires QEMU to be run with -semihosting flag
//
// AArch64 Semihosting convention:
// - w0: Semihosting operation number (0x18 = SYS_EXIT)
// - x1: Pointer to parameter block
// - HLT #0xf000: Trigger semihosting call
//
// Parameter block for SYS_EXIT:
//   [0]: Exit reason code (0x20026 = ADP_Stopped_ApplicationExit)
//   [8]: Status code (0 = success)
.global qemu_exit
qemu_exit:
    // Set up parameter block on stack
    // Reserve 16 bytes for parameter block (8 bytes for reason, 8 bytes for status)
    sub sp, sp, #16
    
    // Store exit reason code: ADP_Stopped_ApplicationExit (0x20026)
    mov x1, #0x26          // Lower 16 bits: 0x26
    movk x1, #2, lsl #16   // Upper 16 bits: 0x2 -> 0x20026
    str x1, [sp, #0]       // Store reason code at [sp+0]
    
    // Store status code: 0 (success)
    mov x0, #0             // Exit status 0 = success
    str x0, [sp, #8]       // Store status code at [sp+8]
    
    // Set up semihosting call
    mov x1, sp             // x1 = pointer to parameter block
    mov w0, #0x18          // w0 = SYS_EXIT (0x18)
    
    // Trigger semihosting call
    hlt #0xf000            // HLT with immediate 0xf000 triggers semihosting
    
    // If semihosting is not enabled, we'll reach here
    // Restore stack and return
    add sp, sp, #16
    ret

// Bridge function: kernel_main -> main.KernelMain (Go function)
// This allows boot.s to call kernel_main, which then calls the Go KernelMain function
// Go exports it as main.KernelMain (package.function)
.global kernel_main
.extern main.KernelMain
.extern main.GrowStackForCurrent
kernel_main:
    // UART will be initialized by uartInit() called from kernel_main
    // No early debug writes
    
    // Function signature: KernelMain(r0, r1, atags uint32)
    // AArch64 calling convention: first 8 parameters in x0-x7
    // Set parameters to 0 (no ATAGs in QEMU virt machine)
    mov x0, #0                    // r0 = 0
    mov x1, #0                    // r1 = 0  
    mov x2, #0                    // atags = 0
    
    // Ensure stack is 16-byte aligned (required by Go)
    mov x3, sp
    and x3, x3, #0xF              // Check alignment
    cbz x3, stack_ok              // If aligned, continue
    sub sp, sp, #8                // Adjust if not aligned
stack_ok:
    
    // Set x28 (goroutine pointer) to point to runtime.g0
    // This is required for write barrier to work
    // runtime.g0 is at address 0x331a00
    movz x28, #0x331a, lsl #16    // Load upper 16 bits: 0x331a00
    movk x28, #0x0000, lsl #0     // Load lower 16 bits
    
    // Note: Write barrier flag is set in boot.s AFTER BSS clear
    // (Setting it here would be overwritten by BSS clear)
    
    // Call Go function
    bl main.KernelMain
    
    // If we get here, KernelMain returned (shouldn't happen)
    // Just loop forever
    b .

// =================================================================
// Stack Growth Functions (Bare-Metal Implementation)
// =================================================================
// These functions are called by the Go compiler when a function
// needs more stack space. For our large pre-allocated stack (508MB),
// these should never be called. If they are, it indicates a stack overflow.

// runtime.morestack is called by Go compiler when stack check fails
// This implements simplified stack growth for bare-metal
// Saves registers, calls growStack(), restores registers, continues
.global runtime.morestack
runtime.morestack:
    // Save all callee-saved registers to current stack
    // AArch64 calling convention: x19-x28, x29 (FP), x30 (LR) are callee-saved
    // We also need to save x0-x7 (arguments) and x8 (indirect result)
    // But morestack is called from function prologue, so we need to be careful
    
    // Save link register and frame pointer
    stp x29, x30, [sp, #-16]!
    mov x29, sp  // Set frame pointer
    
    // Save callee-saved registers (x19-x28)
    sub sp, sp, #80  // 10 registers * 8 bytes
    stp x19, x20, [sp, #0]
    stp x21, x22, [sp, #16]
    stp x23, x24, [sp, #32]
    stp x25, x26, [sp, #48]
    stp x27, x28, [sp, #64]
    
    // TODO: Implement stack growth
    // For now, just halt if morestack is called (shouldn't happen with large pre-allocated stack)
    // bl main.GrowStackForCurrent
    // Infinite loop - stack overflow
halt_morestack:
    b halt_morestack
    
    // Restore callee-saved registers
    ldp x27, x28, [sp, #64]
    ldp x25, x26, [sp, #48]
    ldp x23, x24, [sp, #32]
    ldp x21, x22, [sp, #16]
    ldp x19, x20, [sp, #0]
    add sp, sp, #80
    
    // Restore frame pointer and link register
    ldp x29, x30, [sp], #16
    
    // Return to continue execution on new stack
    ret

// runtime.morestack_noctxt is called for functions without context
.global runtime.morestack_noctxt
runtime.morestack_noctxt:
    b runtime.morestack  // Same as morestack

// runtime.morestackc is called for C functions
.global runtime.morestackc
runtime.morestackc:
    b runtime.morestack  // Same as morestack

// =================================================================
// PL011 UART Functions for QEMU virt machine
// =================================================================

// PL011 UART base address and register offsets
.equ QEMU_UART_BASE, 0x09000000
.equ UART_DR_OFFSET, 0x00   // Data Register
.equ UART_FR_OFFSET, 0x18   // Flag Register
.equ UART_IBRD_OFFSET, 0x24 // Integer Baud Rate Divisor Register
.equ UART_FBRD_OFFSET, 0x28 // Fractional Baud Rate Divisor Register
.equ UART_LCRH_OFFSET, 0x2C // Line Control Register High
.equ UART_CR_OFFSET, 0x30   // Control Register
.equ UART_IMSC_OFFSET, 0x38 // Interrupt Mask Set/Clear Register
.equ UART_DMACR_OFFSET, 0x48 // DMA Control Register

// Bit definitions
.equ CR_UARTEN, (1 << 0)    // UART Enable bit
.equ CR_TXEN, (1 << 8)      // Transmit Enable bit
.equ CR_RXEN, (1 << 9)      // Receive Enable bit
.equ FR_BUSY, (1 << 3)      // BUSY bit in Flag Register
.equ FR_TXFF, (1 << 5)      // Transmit FIFO Full bit
.equ LCR_FEN, (1 << 4)      // FIFO Enable bit

// uart_init_pl011 initializes the PL011 UART for QEMU virt machine
// Follows proper PL011 initialization sequence from specification
// No parameters needed
.global uart_init_pl011
uart_init_pl011:
    ldr x1, =QEMU_UART_BASE

    // Step 1: Disable UART (clear UARTEN bit)
    ldr w2, [x1, #UART_CR_OFFSET]
    bic w2, w2, #CR_UARTEN      // Clear UARTEN bit
    str w2, [x1, #UART_CR_OFFSET]
    dsb sy                       // Memory barrier

    // Step 2: Wait for any ongoing transmission to complete
    // Check BUSY bit (bit 3) in UARTFR
wait_tx_complete:
    ldr w2, [x1, #UART_FR_OFFSET]
    tst w2, #FR_BUSY             // Test BUSY bit
    bne wait_tx_complete         // If busy, keep waiting

    // Step 3: Flush FIFOs (clear FEN bit in UARTLCR_H)
    ldr w2, [x1, #UART_LCRH_OFFSET]
    bic w2, w2, #LCR_FEN         // Clear FEN bit to flush FIFOs
    str w2, [x1, #UART_LCRH_OFFSET]
    dsb sy

    // Step 4: Configure Baud Rate divisors
    // For QEMU, use simple divisors (115200 baud with 24MHz clock)
    // IBRD = 1, FBRD = 0 (or calculate properly if needed)
    mov w2, #1                   // IBRD = 1
    str w2, [x1, #UART_IBRD_OFFSET]
    mov w2, #0                   // FBRD = 0
    str w2, [x1, #UART_FBRD_OFFSET]
    dsb sy

    // Step 5: Configure Line Control (UARTLCR_H)
    // 8 data bits: WLEN = 3 (bits 5-6 = 0b11)
    // FIFO enabled: FEN = 1 (bit 4)
    // 1 stop bit: STP2 = 0 (bit 3)
    // No parity: PEN = 0 (bit 1)
    // Value: 0x70 (0b01110000)
    mov w2, #0x70
    str w2, [x1, #UART_LCRH_OFFSET]
    dsb sy

    // Step 6: Mask all interrupts (UARTIMSC)
    // Set all bits to 1 to mask all interrupts
    mov w2, #0x7FF               // Mask all 11 interrupt sources
    str w2, [x1, #UART_IMSC_OFFSET]
    dsb sy

    // Step 7: Disable DMA (UARTDMACR)
    // Set all bits to 0 to disable DMA
    mov w2, #0x0
    str w2, [x1, #UART_DMACR_OFFSET]
    dsb sy

    // Step 8: Enable Transmitter (TXE bit)
    mov w2, #CR_TXEN             // Enable TXE only
    str w2, [x1, #UART_CR_OFFSET]
    dsb sy

    // Step 9: Enable UART (UARTEN bit) - must be last step
    mov w2, #(CR_TXEN | CR_UARTEN) // Enable both TXE and UARTEN
    str w2, [x1, #UART_CR_OFFSET]
    dsb sy                       // Memory barrier to ensure enable is visible
    
    // Wait for UART to be ready by checking that it's not busy
    // This uses proper status register checking instead of arbitrary delays
wait_uart_ready:
    ldr w2, [x1, #UART_FR_OFFSET]
    tst w2, #FR_BUSY             // Check BUSY bit
    bne wait_uart_ready          // If busy, keep waiting
    
    // Verify UART is enabled by reading control register
    ldr w2, [x1, #UART_CR_OFFSET]
    tst w2, #CR_UARTEN           // Check UARTEN bit
    beq uart_init_failed         // If not enabled, something went wrong

    ret

uart_init_failed:
    // UART initialization failed - loop forever
    wfe
    b uart_init_failed

// uart_putc_pl011 sends a single character via PL011 UART
// Parameters: w0 = character to send (byte)
.global uart_putc_pl011
uart_putc_pl011:
    ldr x1, =QEMU_UART_BASE

    // Verify UART is enabled before writing
    // Check UARTEN bit (bit 0) and TXE bit (bit 8) in UART_CR
    ldr w2, [x1, #UART_CR_OFFSET]
    // Test UARTEN (bit 0) - use movz/movk for large immediate
    movz w3, #0x1              // Bit 0 (UARTEN)
    tst w2, w3
    beq uart_not_enabled       // If UARTEN not set, skip write
    movz w3, #0x100            // Bit 8 (TXE)
    tst w2, w3
    beq uart_not_enabled       // If TXE not set, skip write
    
check_tx_full:
    ldr w2, [x1, #UART_FR_OFFSET]
    ands w2, w2, #0x20         // Test if the TXFF bit (bit 5) is set
    bne check_tx_full          // If set, branch back and wait

    strb w0, [x1, #UART_DR_OFFSET] // Store the character
    ret

uart_not_enabled:
    // UART not enabled - just return (don't write)
    ret


// ============================================================================
// Memory Functions
// ============================================================================

// memmove(void *dest, void *src, size_t n)
// Copy n bytes from src to dest
// Optimized for speed using 16-byte (128-bit) chunks
// x0 = dest, x1 = src, x2 = size
.global memmove
memmove:
    cmp x2, #0              // Compare size with 0
    beq memmove_done        // If size == 0, done

    // Check if we can do 16-byte copies
    cmp x2, #16
    blt memmove_bytes_loop  // If < 16 bytes, use byte copy

memmove_16_loop:
    ldp x3, x4, [x1], #16   // Load 16 bytes (two 64-bit regs)
    stp x3, x4, [x0], #16   // Store 16 bytes
    sub x2, x2, #16         // Decrement size by 16
    cmp x2, #16             // Check if we have 16+ bytes left
    bge memmove_16_loop     // Loop if yes

memmove_bytes_loop:
    cbz x2, memmove_done    // If size == 0, done
    ldrb w3, [x1], #1       // Load byte
    strb w3, [x0], #1       // Store byte
    sub x2, x2, #1          // Decrement size
    bne memmove_bytes_loop  // Loop if not zero

memmove_done:
    ret

// MemmoveBytes is the Go-callable alias for memmove
.global MemmoveBytes
MemmoveBytes:
    b memmove
