.section ".text.boot"

.global _start

_start:
    // Get CPU ID - only run on CPU 0
    mrs x1, mpidr_el1
    and x1, x1, #0xFF
    cmp x1, #0
    bne cpu_halt_loop

    // CPU 0 continues here
    // Set stack pointer to a higher address (above kernel at 0x200000)
    // Go runtime needs significant stack space
    // Stack at 0x400000 (4MB) - well above kernel and provides 1MB+ stack
    movz x0, #0x40, lsl #16    // Load 0x400000 (0x40 << 16)
    mov sp, x0

    // Clear BSS section
    ldr x4, =__bss_start
    ldr x9, =__bss_end
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

    // Jump to kernel_main
    ldr x0, =kernel_main
    blr x0

    // If kernel_main returns, halt CPU 0
    b halt

// Halt loop for CPUs 1-3
// These CPUs will loop here indefinitely
cpu_halt_loop:
    wfe                    // Wait for event (low power)
    b cpu_halt_loop        // Loop forever

// Halt loop for CPU 0 (if kernel_main returns)
halt:
    wfe
    b halt

    