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

// Bridge function: kernel_main -> main.KernelMain (Go function)
// This allows boot.s to call kernel_main, which then calls the Go KernelMain function
// Go exports it as main.KernelMain (package.function)
.global kernel_main
.extern main.KernelMain
kernel_main:
    // Function signature: KernelMain(r0, r1, atags uint32)
    // Parameters are already in x0, x1, x2 (AArch64 calling convention)
    // Call Go function directly
    b main.KernelMain

