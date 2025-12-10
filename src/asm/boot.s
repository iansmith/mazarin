.section ".text.boot"

.global _start

_start:
    // Get CPU ID - only run on CPU 0
    mrs x1, mpidr_el1
    ubfx x1, x1, #0, #8          // Extract Aff0 (bits 0-7)
    cmp x1, #0
    bne cpu_halt_loop

    // CPU 0 continues here
    // Breadcrumb: CPU 0 selected
    movz x14, #0x0900, lsl #16     // UART base = 0x09000000
    movz w15, #0x30                // '0' = CPU 0 selected
    str w15, [x14]
    
    // ========================================
    // Drop from EL2 to EL1 if necessary
    // QEMU virt with virtualization=on starts at EL2
    // We need to be at EL1 for proper OS operation
    // ========================================
    movz w15, #0x45                // 'E' = Checking EL
    str w15, [x14]
    mrs x0, CurrentEL
    lsr x0, x0, #2               // Extract EL bits [3:2]
    cmp x0, #2                   // Are we at EL2?
    bne at_el1                   // If not, skip EL2->EL1 transition
    movz w15, #0x32              // '2' = At EL2, dropping to EL1
    str w15, [x14]
    
    // We're at EL2, need to drop to EL1
    // Configure HCR_EL2 (Hypervisor Configuration Register)
    // RW (bit 31) = 1: EL1 uses AArch64
    // All other bits = 0: No trapping, no virtualization features
    mov x0, #(1 << 31)           // RW bit for AArch64 at EL1
    msr HCR_EL2, x0
    
    // Configure CNTHCTL_EL2 to allow EL1/EL0 access to timers
    // EL1PCTEN (bit 0) = 1: Don't trap CNTPCT_EL0 reads from EL1
    // EL1PCEN (bit 1) = 1: Don't trap CNTP_* accesses from EL1
    // For virtual timer (CNTV_*), these aren't needed but good to set anyway
    mov x0, #3                   // EL1PCTEN | EL1PCEN
    msr CNTHCTL_EL2, x0
    
    // Set virtual timer offset to 0 (CNTVOFF_EL2)
    // This ensures virtual timer counter matches physical counter
    mov x0, #0
    msr CNTVOFF_EL2, x0
    
    // Configure SPSR_EL2 for return to EL1h (EL1 using SP_EL1)
    // M[3:0] = 0b0101 = EL1h (EL1 with SP_EL1)
    // M[4] = 0: AArch64
    // DAIF = 0b1111: All exceptions masked initially
    // D (bit 9) = 1: Debug masked
    // A (bit 8) = 1: SError masked
    // I (bit 7) = 1: IRQ masked
    // F (bit 6) = 1: FIQ masked
    mov x0, #0x3C5               // 0b1111000101 = DAIF masked + EL1h
    msr SPSR_EL2, x0
    
    // Set ELR_EL2 to the address we want to return to (at_el1 label)
    adr x0, at_el1
    msr ELR_EL2, x0
    
    // Return to EL1 (this is an exception return from EL2 to EL1)
    eret

at_el1:
    // Now we're at EL1
    // Breadcrumb: At EL1
    movz x14, #0x0900, lsl #16     // UART base = 0x09000000
    movz w15, #0x31                // '1' = At EL1
    str w15, [x14]
    
    // QEMU virt machine memory layout (1GB RAM):
    // - 0x00000000-0x08000000: Flash/ROM (kernel loaded at 0x200000)
    // - 0x09000000-0x09010000: UART (PL011)
    // - 0x40000000-0x40100000: DTB (QEMU device tree blob, 1MB)
    // - 0x40100000-0x48100000: Kernel RAM (128MB allocated for kernel)
    //   - 0x40100000-0x401xxxxx: BSS section
    //   - After BSS: Heap (grows upward, extends to 0x5FFFFE000)
    //   - 0x5FFFFE000-0x5F000000: g0 stack (8KB, grows downward from 0x5F000000)
    //
    // Set stack pointer to 0x5F000000 (g0 stack top, 8KB stack)
    // g0 stack bottom is at 0x5FFFFE000, heap should end before this
    movz w15, #0x53                // 'S' = Setting stack
    str w15, [x14]
    movz x0, #0x5F00, lsl #16    // 0x5F000000 (g0 stack top, 8KB)
    mov sp, x0
    movz w15, #0x73                // 's' = Stack set
    str w15, [x14]

    // Clear BSS section (now in RAM region at 0x40100000, after DTB)
    movz w15, #0x42                // 'B' = Clearing BSS
    str w15, [x14]
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

    movz w15, #0x62              // 'b' = BSS cleared
    str w15, [x14]

    // Enable write barrier flag AFTER clearing BSS
    // runtime.writeBarrier is in BSS at 0x40026b40 (RAM region)
    // The Go compiler checks this flag before pointer assignments
    // Note: Address is determined by `target-nm kernel.elf | grep runtime.writeBarrier`
    movz x10, #0x4002, lsl #16     // 0x40020000
    movk x10, #0x6b40, lsl #0      // 0x40026b40
    mov w11, #1                    // Enable write barrier
    strb w11, [x10]                // Store byte (bool field)
    dsb sy                         // Memory barrier
    movz w15, #0x57                // 'W' = Write barrier enabled
    str w15, [x14]
    
    // Set exception vector base to our table (required before enabling IRQs)
    movz w15, #0x56                // 'V' = Setting VBAR
    str w15, [x14]
    ldr x0, =exception_vectors
    dsb sy
    msr VBAR_EL1, x0
    isb
    movz w15, #0x76                // 'v' = VBAR set
    str w15, [x14]
    
    // Breadcrumb: About to call kernel_main
    // Write 'B' (0x42) to UART to show we reached this point
    movz x14, #0x0900, lsl #16     // UART base = 0x09000000
    movz w15, #0x42                // 'B' = Boot complete, about to call kernel_main
    str w15, [x14]

    // Jump to kernel_main
    ldr x0, =kernel_main
    blr x0

    // After kernel_main returns:
    // 1. Enable interrupts (IRQs) - kernel has set up handlers
    // 2. Enter idle loop that prints dots and waits for interrupts
    
    // Enable IRQs by clearing I bit (bit 1) in DAIF
    msr DAIFCLR, #2              // Clear I bit - enable IRQs
    
idle_loop:
    // Wait for interrupt (low power mode)
    wfi                          // Wait for timer or other interrupt
    // Loop forever - interrupts are handled in exception handlers
    b idle_loop

// Exit via semihosting
exit_via_semihosting:
    // Print 'X' to indicate we're about to exit
    movz x10, #0x0900, lsl #16   // UART base = 0x09000000
    movz w11, #0x58              // 'X' = eXiting
    str w11, [x10]               // Write to UART
    movz w11, #0x0A              // '\n'
    str w11, [x10]               // Write to UART
    
    // Use semihosting to exit
    // SYS_EXIT = 0x18
    movz x0, #0x18               // x0 = 0x18 (SYS_EXIT opcode)
    movz x1, #0                  // x1 = 0 (exit code)
    hlt #0xF000                  // Semihosting call (AArch64)

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



