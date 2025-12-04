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
kernel_main:
    // Write 'K' to show we're in kernel_main
    movz x10, #0x900, lsl #16    // UART base
    movk x10, #0x0000, lsl #0
    add x11, x10, #0x18          // FR register
k_wait:
    ldr w12, [x11]
    tst w12, #(1 << 5)
    bne k_wait
    movz w13, #'K'
    str w13, [x10]
k_wait2:
    ldr w12, [x11]
    tst w12, #(1 << 5)
    bne k_wait2
    movz w13, #'\n'
    str w13, [x10]
    
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

