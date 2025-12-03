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

