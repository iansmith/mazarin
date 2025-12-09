.section ".text.boot"

.global _start

_start:
    // Get CPU ID - only run on CPU 0
    mrs x1, mpidr_el1
    ubfx x1, x1, #0, #8          // Extract Aff0 (bits 0-7)
    cmp x1, #0
    bne cpu_halt_loop

    // CPU 0 continues here
    // UART initialization will happen in uartInit() called from kernel_main
    // No early debug writes - wait for proper initialization
    
    // QEMU virt machine memory layout (1GB RAM):
    // - 0x00000000-0x08000000: Flash/ROM (kernel loaded at 0x200000)
    // - 0x09000000-0x09010000: UART (PL011)
    // - 0x40000000-0x40100000: DTB (QEMU device tree blob, 1MB)
    // - 0x40100000-0x60000000: Kernel RAM (512MB allocated for kernel)
    //   - 0x40100000-0x401xxxxx: BSS section
    //   - 0x40400000-0x60000000: Stack (grows downward from 0x60000000)
    //   - 0x40500000-0x60000000: Heap (after stack region)
    //
    // Set stack pointer to top of kernel RAM: 0x60000000 (512MB boundary)
    // Stack grows downward, giving us ~508MB of stack space
    movz x0, #0x6000, lsl #16    // 0x60000000 (top of 512MB kernel region)
    mov sp, x0

    // Clear BSS section (now in RAM region at 0x40100000, after DTB)
    ldr x4, =__bss_start         // 0x40100000
    ldr x9, =__bss_end           // ~0x4003c000
    mov x5, #0
    mov x6, #0
    mov x7, #0
    mov x8, #0
    b       2f

1:
    // Store 64 bytes at a time (8 registers * 8 bytes)
    stp x5, x6, [x4], #16
    stp x7, x8, [x4], #16
    stp x5, x6, [x4], #16
    stp x7, x8, [x4], #16

2:
    cmp x4, x9
    blo 1b

    // Enable write barrier flag AFTER clearing BSS
    // runtime.writeBarrier is in BSS at 0x40026b40 (RAM region)
    // The Go compiler checks this flag before pointer assignments
    // Note: Address is determined by `target-nm kernel.elf | grep runtime.writeBarrier`
    movz x10, #0x4002, lsl #16     // 0x40020000
    movk x10, #0x6b40, lsl #0      // 0x40026b40
    mov w11, #1                    // Enable write barrier
    strb w11, [x10]                // Store byte (bool field)
    dsb sy                         // Memory barrier
    
    // Set exception vector base to our table (required before enabling IRQs)
    ldr x0, =exception_vectors
    dsb sy
    msr VBAR_EL1, x0
    isb
    
    // No early debug writes - UART will be initialized in kernel_main

    // Jump to kernel_main
    ldr x0, =kernel_main
    blr x0

    // After kernel_main returns:
    // 1. Enable interrupts (IRQs) - kernel has set up handlers
    // 2. Enter idle loop that prints dots and waits for interrupts
    
    // DEBUG: Reached after kernel_main returned - print 'X'
    movz x10, #0x0900, lsl #16   // UART base = 0x09000000
    movz w11, #0x58              // 'X' = eXecution reached idle setup
    str w11, [x10]               // Write to UART
    
    // Enable IRQs by clearing I bit (bit 1) in DAIF
    msr DAIFCLR, #2              // Clear I bit - enable IRQs
    
    // DEBUG: IRQs enabled - print 'Q'
    movz x10, #0x0900, lsl #16   // UART base = 0x09000000
    movz w11, #0x51              // 'Q' = iRQs enabled
    str w11, [x10]               // Write to UART
    
    // Print 'Y' to confirm IRQs are now enabled
    movz x10, #0x0900, lsl #16   // UART base = 0x09000000
    movz w11, #0x59              // 'Y' = IRQs enabled
    str w11, [x10]               // Write to UART
    
    // Print 'E' to confirm entering event loop
    movz w11, #0x45              // 'E' = Entering event loop
    str w11, [x10]               // Write to UART
    
    // Initialize dot counter (print dot every ~10000000 iterations)
    mov x12, #0                  // x12 = dot counter
    
idle_loop:
    // Increment counter
    add x12, x12, #1
    
    // Check if counter reached ~10000000
    // Approximate 10M: load 152 << 16 = 9961472 (99.6% of 10M)
    movz x13, #152, lsl #16      // x13 = 152 << 16 = 9961472
    cmp x12, x13
    bne skip_dot                 // If not reached, skip printing dot
    
    // Print '.' to show we're still running (every 10000 iterations)
    movz x10, #0x0900, lsl #16   // UART base = 0x09000000
    movz w11, #0x2E              // '.' character
    str w11, [x10]               // Write to UART
    
    // Reset counter
    mov x12, #0
    
skip_dot:
    // Print 'V' to show loop iteration before wfi
    movz x10, #0x0900, lsl #16   // UART base = 0x09000000
    movz w11, #0x56              // 'V' = loop iteration
    str w11, [x10]               // Write to UART
    
    // Wait for interrupt (low power mode)
    wfi                          // Wait for timer interrupt
    
    // After interrupt fires and handler returns, loop again
    b idle_loop

// UART initialization failed - loop forever
uart_init_failed:
    wfe
    b uart_init_failed

// Halt loop for CPUs 1-3
// These CPUs will loop here indefinitely
cpu_halt_loop:
    wfe                    // Wait for event (low power)
    b cpu_halt_loop        // Loop forever

// Halt loop for CPU 0 (if kernel_main returns)
halt:
    wfe
    b halt



