.section ".text.boot"

.global _start

_start:
    // Get CPU ID - only run on CPU 0
    mrs x1, mpidr_el1
    ubfx x1, x1, #0, #8          // Extract Aff0 (bits 0-7)
    cmp x1, #0
    bne cpu_halt_loop

    // CPU 0 continues here
    // Write 'S' immediately to verify kernel is starting
    movz x10, #0x900, lsl #16    // UART base 0x09000000
    movk x10, #0x0000, lsl #0
    add x11, x10, #0x18          // FR register
s_wait:
    ldr w12, [x11]
    tst w12, #(1 << 5)
    bne s_wait
    movz w13, #'S'
    str w13, [x10]
    
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
    
    // Write 'B' before jumping to kernel_main
    movz x10, #0x900, lsl #16    // UART base
    movk x10, #0x0000, lsl #0
    add x11, x10, #0x18          // FR register
b_wait:
    ldr w12, [x11]
    tst w12, #(1 << 5)
    bne b_wait
    movz w13, #'B'
    str w13, [x10]
b_wait2:
    ldr w12, [x11]
    tst w12, #(1 << 5)
    bne b_wait2
    movz w13, #'\n'
    str w13, [x10]

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



