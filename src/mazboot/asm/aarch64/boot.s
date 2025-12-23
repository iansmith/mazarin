.section ".text.boot"

.global _start

.equ ENABLE_MMU_IN_BOOT, 0

_start:
    // Preserve QEMU-provided DTB pointer.
    // On QEMU virt, x0 contains the DTB physical address at reset.
    // We'll carry it through early init and pass it to kernel_main in x2.
    mov x22, x0

    // =====================================================
    // Early DTB pointer diagnostic
    // Print a single character to UART indicating whether
    // QEMU provided a non-zero DTB pointer in x0 at reset.
    //   'D' => DTB pointer non-zero (something was passed)
    //   'd' => DTB pointer is zero (nothing passed)
    // This runs before any EL transitions or BSS clearing.
    // =====================================================
    str  w15, [x14]
    b    2f
1:
    movz w15, #0x64                // 'd' = DTB pointer zero
    str  w15, [x14]
2:

    // Get CPU ID - only run on CPU 0
    mrs x1, mpidr_el1
    ubfx x1, x1, #0, #8          // Extract Aff0 (bits 0-7)
    cmp x1, #0
    bne cpu_halt_loop

    // CPU 0 continues here
    // Breadcrumb: CPU 0 selected
    
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
    // TTLB (bit 25) = 0: Don't trap TLB maintenance instructions to EL2
    // All other bits = 0: No trapping, no virtualization features
    mov x0, #(1 << 31)           // RW bit for AArch64 at EL1
    // TTLB is already 0 (cleared) in this value, but explicitly document it
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

    // ========================================
    // Enable SIMD/floating-point (required for gg library)
    // CPACR_EL1.FPEN (bits 21:20) = 0b11: No trapping from EL0 or EL1
    // Without this, any FPU/SIMD instruction traps with EC=0x07
    // ========================================
    movz w15, #0x46                // 'F' = Enabling FPU
    str w15, [x14]
    mov x0, #(3 << 20)             // FPEN = 0b11
    msr CPACR_EL1, x0
    isb                            // Ensure FPU is enabled before continuing
    movz w15, #0x66                // 'f' = FPU enabled
    str w15, [x14]

    // ========================================
    // Disable strict alignment checking to allow unaligned accesses
    // SCTLR_EL1.A (bit 1) = 0: Allow unaligned access for normal memory
    // This is required because Go compiler places strings in .rodata without
    // guaranteed 8-byte alignment, and runtime.memequal uses ldp which requires alignment
    // ========================================
    mrs x0, SCTLR_EL1
    bic x0, x0, #(1 << 1)          // Clear bit 1 (A = alignment check)
    msr SCTLR_EL1, x0
    isb

    // QEMU virt machine memory layout (1GB RAM):
    // - 0x00000000-0x08000000: Flash/ROM (kernel loaded at 0x200000)
    // - 0x09000000-0x09010000: UART (PL011)
    // - 0x40000000-0x40100000: DTB (QEMU device tree blob, 1MB)
    // - 0x40100000-0x48100000: Kernel RAM (128MB allocated for kernel)
    //   - 0x40100000-0x401xxxxx: BSS section
    //   - After BSS: Heap (grows upward, extends to 0x5EFF0000)
    //   - 0x5EFF0000-0x5F000000: g0 stack (64KB, grows downward from 0x5F000000)
    //
    // Set stack pointer to 0x5F000000 (g0 stack top, 64KB stack - matches real Go runtime)
    // g0 stack bottom is at 0x5EFF0000, heap should end before this
    movz w15, #0x53                // 'S' = Setting stack
    str w15, [x14]
    movz x0, #0x5F00, lsl #16    // 0x5F000000 (g0 stack top, 64KB)
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

    // NOTE: .data section is loaded directly to RAM by QEMU (no copy needed)
    // The linker places .data at 0x40100000+ and QEMU loads it there

    // Initialize mmap bump pointer at 0x40FFF000 to 0x48000000
    // IMPORTANT: Must be within mapped heap region (0x40000000-0x50000000)
    // Start at 0x48000000 to leave room for initial heap allocations
    // FIX: Was 0x60000000 which is UNMAPPED, causing page faults in schedinit
    movz x4, #0x40FF, lsl #16     // 0x40FF0000
    movk x4, #0xF000, lsl #0      // 0x40FFF000
    movz x5, #0x4800, lsl #16     // 0x48000000 - start of mmap region (within heap!)
    movk x5, #0x0000, lsl #0
    str x5, [x4]                  // Store initial mmap pointer
    movz w15, #0x4D              // 'M' = mmap pointer initialized
    str w15, [x14]

    // Enable write barrier flag AFTER clearing BSS
    // runtime.writeBarrier is in BSS - use linker symbol (not hardcoded address)
    // The Go compiler checks this flag before pointer assignments
    ldr x10, =runtime.writeBarrier
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
    
    // CRITICAL: Verify VBAR_EL1 was set correctly
    mrs x1, VBAR_EL1
    cmp x0, x1
    beq vbar_ok
    // VBAR mismatch - print 'X' and hang
    movz w15, #0x58                // 'X' = VBAR mismatch error
    str w15, [x14]
    b .
vbar_ok:
    movz w15, #0x76                // 'v' = VBAR set and verified
    str w15, [x14]

    // ========================================
    // Initialize GIC (Generic Interrupt Controller)
    // Required to receive timer interrupts
    // ========================================
    movz w15, #0x47                // 'G' = Initializing GIC
    str w15, [x14]

    // QEMU virt machine GIC addresses:
    // GICD (Distributor): 0x08000000
    // GICC (CPU Interface): 0x08010000

    // 1. Enable the GIC Distributor (GICD_CTLR at offset 0x000)
    movz x0, #0x0800, lsl #16      // x0 = 0x08000000 (GICD base)
    mov w1, #1                     // Enable bit
    str w1, [x0, #0]               // GICD_CTLR = 1 (enable distributor)

    // 2. Enable interrupt 27 (virtual timer PPI) in GICD_ISENABLER0
    // Interrupt 27 is bit 27 in GICD_ISENABLER0 (offset 0x100)
    // Each bit enables one interrupt (0-31 for ISENABLER0)
    mov w1, #(1 << 27)             // Bit 27 for virtual timer
    str w1, [x0, #0x100]           // GICD_ISENABLER0 = enable interrupt 27

    // 3. Set priority for interrupt 27 (GICD_IPRIORITYR6 at offset 0x400 + 27)
    // Each interrupt gets 8 bits of priority (0 = highest, 0xFF = lowest)
    // Interrupt 27 is at byte offset 27 in the priority array
    mov w1, #0xA0                  // Priority 0xA0 (medium priority)
    strb w1, [x0, #0x400 + 27]     // GICD_IPRIORITYR[27] = 0xA0

    // 4. Enable the CPU Interface (GICC_CTLR at offset 0x000 from GICC base)
    movz x0, #0x0801, lsl #16      // x0 = 0x08010000 (GICC base)
    mov w1, #1                     // Enable bit
    str w1, [x0, #0]               // GICC_CTLR = 1 (enable CPU interface)

    // 5. Set priority mask to allow all interrupts (GICC_PMR at offset 0x004)
    // Priority mask: interrupts with priority < PMR are signaled
    // 0xFF = allow all priorities (0x00-0xFE)
    mov w1, #0xFF
    str w1, [x0, #4]               // GICC_PMR = 0xFF (allow all priorities)

    // Memory barrier to ensure GIC is configured before continuing
    dsb sy
    isb

    movz w15, #0x67                // 'g' = GIC initialized
    str w15, [x14]

    // ========================================
    // TEST: Enable MMU from boot.s (earliest possible location)
    // This tests if enabling MMU from pure assembly before Go code works
    // ========================================
    .if ENABLE_MMU_IN_BOOT
    movz w15, #0x4D                // 'M' = Starting MMU setup in boot.s
    str w15, [x14]
    
    // Set up minimal page tables for identity mapping
    // Page table base: 0x5F100000 (same as Go code uses)
    // We'll set up just enough to map the code region (0x00000000-0x08000000)
    
    // Step 1: Configure MAIR_EL1
    // Attr0 (bits 7:0) = 0xFF (Normal, Inner/Outer WB Cacheable)
    // Attr1 (bits 15:8) = 0x00 (Device-nGnRnE)
    // Attr2 (bits 23:16) = 0x44 (Normal, Inner/Outer Non-Cacheable)
    movz x0, #0x44FF               // MAIR bits 15:0 = Attr1:Attr0
    movk x0, #0x0044, lsl #16      // MAIR bits 31:16 = Attr3:Attr2
    msr MAIR_EL1, x0
    isb
    
    // Step 2: Configure TCR_EL1
    // T0SZ = 16, T1SZ = 16, IRGN0 = 1, ORGN0 = 1, SH0 = 3, TG0 = 0
    // EPD1 = 1, IPS = 2, AS = 0
    movz x0, #0x3510               // Low 16 bits: T0SZ=16, IRGN0=1, ORGN0=1, SH0=3
    movk x0, #0x0010, lsl #16      // T1SZ=16, EPD1=1
    movk x0, #0x0002, lsl #32      // IPS=2
    msr TCR_EL1, x0
    isb
    
    // Step 3: Set up minimal page tables
    // We need to create a minimal identity mapping for code execution
    // For simplicity, we'll use the same page table location as Go code: 0x5F100000
    // L0 table at 0x5F100000, L1 table at 0x5F101000
    
    // Zero out page tables (we'll use a simple approach - zero just what we need)
    movz x1, #0x5F10, lsl #16      // Page table base = 0x5F100000
    movk x1, #0x0000, lsl #0
    mov x2, x1
    add x2, x2, #0x2000             // Clear 8KB (L0 + L1 tables)
    mov x3, #0
    mov x4, #0
    
1:  // Clear loop
    stp x3, x4, [x1], #16
    cmp x1, x2
    blo 1b
    
    // Set up L0 entry 0 to point to L1 table
    // L0 entry format: bits 1:0 = 0b11 (table), bits 47:12 = L1 table address
    movz x1, #0x5F10, lsl #16      // L0 table base = 0x5F100000
    movk x1, #0x0000, lsl #0
    movz x2, #0x5F10, lsl #16      // L1 table base = 0x5F101000
    movk x2, #0x1000, lsl #0
    orr x2, x2, #0x3               // Set table bits (bits 1:0 = 0b11)
    str x2, [x1]                   // Store L0 entry 0
    
    // Set up L1 entry 0 to point to L2 table (we'll create one on the fly)
    // For simplicity, map first 2MB as a block (L1 block entry)
    // L1 block entry: bits 1:0 = 0b01 (block), AttrIndx=0 (Normal), AP=0b01 (RW_EL1)
    movz x1, #0x5F10, lsl #16      // L1 table base = 0x5F101000
    movk x1, #0x1000, lsl #0
    movz x2, #0x0000, lsl #16      // Physical address = 0x00000000
    movk x2, #0x0000, lsl #0
    orr x2, x2, #0x1               // Valid (bit 0)
    orr x2, x2, #0x40              // AP (bits 7:6 = 0b01 = RW_EL1)
    orr x2, x2, #0x300             // SH (bits 9:8 = 0b11 = Inner Shareable)
    orr x2, x2, #0x400             // AF (bit 10 = Access Flag)
    str x2, [x1]                   // Store L1 block entry 0 (maps 0x00000000-0x001FFFFF)
    
    // Set TTBR0_EL1
    movz x0, #0x5F10, lsl #16      // L0 table base = 0x5F100000
    movk x0, #0x0000, lsl #0
    msr TTBR0_EL1, x0
    isb
    
    // Set TTBR1_EL1 to safe value (0)
    mov x0, #0
    msr TTBR1_EL1, x0
    isb
    
    // Final barriers
    dsb sy
    isb
    
    // CRITICAL: Test if exceptions work at all before enabling MMU
    // We'll trigger a deliberate exception to verify the handler is working
    movz w15, #0x54                // 'T' = Testing exception handler
    str w15, [x14]
    
    // Trigger a deliberate undefined instruction exception
    // This should call our exception handler if it's working
    // We'll use an invalid instruction: 0x00000000 (all zeros is invalid)
    // But actually, let's use a simpler test - try to access an invalid system register
    // Actually, let's just verify VBAR is set and exception vectors are accessible
    ldr x0, =exception_vectors     // Load exception vectors address
    mrs x1, VBAR_EL1               // Read VBAR
    cmp x0, x1
    beq vbar_test_ok
    // VBAR mismatch - this shouldn't happen, but if it does, print error
    movz w15, #0x58                // 'X' = VBAR test failed
    str w15, [x14]
    b .
vbar_test_ok:
    movz w15, #0x74                // 't' = Exception handler test passed
    str w15, [x14]
    
    // Step 4: Enable MMU with minimal SCTLR (only M bit)
    movz w15, #0x6D                // 'm' = About to enable MMU
    str w15, [x14]
    
    // CRITICAL: Add breadcrumb right before msr to catch any exception
    movz w15, #0x3A                // ':' = About to write SCTLR_EL1
    str w15, [x14]
    
    mrs x0, SCTLR_EL1
    mov x0, #1                      // Set only M bit (MMU enable)
    msr SCTLR_EL1, x0
    
    // CRITICAL: Add breadcrumb immediately after msr (before ISB)
    // If we get here, msr completed without exception
    movz w15, #0x3B                // ';' = msr SCTLR_EL1 completed
    str w15, [x14]
    
    isb                             // Critical: ISB after MMU enable
    
    // CRITICAL: Add breadcrumb after ISB
    // If we get here, ISB completed and MMU should be enabled
    movz w15, #0x4D                // 'M' = MMU enabled (if this appears, it worked!)
    str w15, [x14]
    .endif
    
    // Breadcrumb: About to call kernel_main
    // Write 'B' (0x42) to UART to show we reached this point

    // =====================================================
    // Initialize g0 and m0 (like Go runtime's rt0_go does)
    // =====================================================
    // The Go runtime expects g0 and m0 to be initialized before any Go code runs.
    // Normally this is done in rt0_go, but since we jump directly to kernel_main,
    // we must do it here.

    // Step 1: Set g register (x28) to point to runtime.g0
    ldr x28, =runtime.g0           // x28 (g register) = &runtime.g0

    // Step 2: Set up stack guards for g0 (EXACTLY like rt0_go does)
    // g0 uses the current stack (already set up by boot.s)
    // Get current SP and set stack bounds
    mov x7, sp                      // x7 = current stack pointer

    // Set stackguard0 and stackguard1 (64KB below SP)
    sub x0, x7, #(64*1024)          // x0 = SP - 64KB (stack guard)
    str x0, [x28, #16]              // g.stackguard0 = x0 (offset 16 in runtimeG)
    str x0, [x28, #24]              // g.stackguard1 = x0 (offset 24 in runtimeG)

    // Set stack bounds in g.stack (NOTE: stack.lo is ALSO 64KB below, same as guard!)
    str x0, [x28, #0]               // g.stack.lo = stack guard (offset 0)
    str x7, [x28, #8]               // g.stack.hi = current SP (offset 8)

    // Step 3: Link g0 and m0
    ldr x0, =runtime.m0             // x0 = &runtime.m0
    str x0, [x28, #48]              // g0.m = &m0 (offset 48 in runtimeG for 'm *m' field)
    str x28, [x0, #0]               // m0.g0 = &g0 (offset 0 in runtimeM for 'g0 *g' field)

    // NOTE: runtime·save_g is not available in our bare-metal environment
    // (it's mainly for CGO/TLS). The g register (x28) is already set up,
    // which is sufficient for our purposes.

    // Breadcrumb: g0/m0 initialized
    movz w15, #0x47                 // 'G' = g0/m0 initialized
    str w15, [x14]

    // Jump to kernel_main
    ldr x0, =kernel_main
    mov x2, x22                   // atags param used as DTB pointer (low 32 bits consumed by Go)
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



