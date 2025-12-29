.section ".text"

// =================================================================
// Go Calling Convention Support
// =================================================================

// Constants for Go function call setup
.equ SPILL_SPACE_2PARAM,  32    // 2 parameter functions (16 bytes per param)
.equ REG_SAVE_SPACE,      64    // 64 bytes for saving callee-saved regs

// CALL_GO_PROLOGUE: Prepare stack for calling a Go function
//   Arguments: \spill_space - bytes of spill space to allocate
.macro CALL_GO_PROLOGUE spill_space
    // Allocate space for callee-saved registers per AAPCS64
    sub sp, sp, #REG_SAVE_SPACE

    // Save callee-saved registers (x19-x22, x28-x30)
    stp x19, x20, [sp, #0]
    stp x21, x22, [sp, #16]
    stp x28, x29, [sp, #32]
    str x30, [sp, #48]

    // Allocate spill space for Go's argument spills
    sub sp, sp, #\spill_space
.endm

// CALL_GO_EPILOGUE: Clean up after calling a Go function
//   Arguments: \spill_space - bytes of spill space (must match prologue)
.macro CALL_GO_EPILOGUE spill_space
    // Save return value (x0) below spill space
    str x0, [sp, #0]

    // Restore SP to point at saved registers
    add sp, sp, #\spill_space

    // Restore callee-saved registers
    ldp x19, x20, [sp, #0]
    ldp x21, x22, [sp, #16]
    ldp x28, x29, [sp, #32]
    ldr x30, [sp, #48]

    // Restore return value
    ldr x0, [sp, #-\spill_space]

    // Deallocate register save space
    add sp, sp, #REG_SAVE_SPACE
.endm

// =================================================================
// Simple getter functions moved to lib_getters.s (Go/Plan9 assembly)
// MMIO functions moved to lib_mmio.s (Go/Plan9 assembly)
// Barrier functions moved to lib_barriers.s (Go/Plan9 assembly)
// Memory functions (bzero, memmove) moved to lib_misc.s (Go/Plan9 assembly)
// System register functions moved to lib_sysregs.s (Go/Plan9 assembly)
// QEMU exit functions moved to lib_misc.s (Go/Plan9 assembly)

// =================================================================
// Test function: call_runtime_args
// Sets up minimal Linux-style argv/envp/auxv structure and calls runtime.args
// This tests Item 3 of the runtime master plan.
// Returns 0 on success (args completed without crash)
//
// NOTE: Only provides AT_PAGESZ for now (AT_RANDOM requires VirtIO RNG init)
// =================================================================
.global call_runtime_args
.extern runtime.args
call_runtime_args:
    // Save callee-saved registers and create stack frame
    stp x29, x30, [sp, #-96]!
    mov x29, sp
    stp x19, x20, [sp, #16]
    stp x21, x22, [sp, #32]

    // Build the argv/envp/auxv structure on stack
    // Layout (each entry 8 bytes):
    //   sp+48: argv[0] = NULL (end of argv, argc=0)
    //   sp+56: envp[0] = NULL (end of envp)
    //   sp+64: AT_PAGESZ (6)
    //   sp+72: 4096
    //   sp+80: AT_NULL (0)
    //   sp+88: 0

    // argv[0] = NULL (end of argv)
    str xzr, [sp, #48]
    // envp[0] = NULL (end of envp)
    str xzr, [sp, #56]
    // auxv[0] = AT_PAGESZ (6), auxv[1] = 4096
    mov x0, #6
    str x0, [sp, #64]
    mov x0, #4096
    str x0, [sp, #72]
    // auxv[2] = AT_NULL (0), auxv[3] = 0
    str xzr, [sp, #80]
    str xzr, [sp, #88]

    // Call runtime.args(argc=0, argv=&sp[48])
    mov w0, #0              // argc = 0 (int32)
    add x1, sp, #48         // argv = pointer to our structure

    bl runtime.args

    // If we get here, args() completed without crash
    mov x0, #0              // Return 0 = success

    // Restore and return
    ldp x19, x20, [sp, #16]
    ldp x21, x22, [sp, #32]
    ldp x29, x30, [sp], #96
    ret

// =================================================================
// Test function: call_runtime_osinit
// Calls runtime.osinit() to test syscalls:
//   - sched_getaffinity (for getCPUCount)
//   - openat (for getHugePageSize - should fail gracefully)
// This tests Item 4 of the runtime master plan.
// Returns 0 on success (osinit completed without crash)
// =================================================================
.global call_runtime_osinit
.extern runtime.osinit
call_runtime_osinit:
    // Save callee-saved registers and create stack frame
    stp x29, x30, [sp, #-32]!
    mov x29, sp
    stp x19, x20, [sp, #16]

    // Call runtime.osinit()
    // This will call getCPUCount() which uses sched_getaffinity syscall
    // and getHugePageSize() which tries to open /sys/... (will fail gracefully)
    bl runtime.osinit

    // If we get here, osinit() completed without crash
    mov x0, #0              // Return 0 = success

    // Restore and return
    ldp x19, x20, [sp, #16]
    ldp x29, x30, [sp], #32
    ret

// call_runtime_schedinit()
// Call runtime.schedinit() to initialize scheduler and locks
// This will call lockInit() for all runtime locks (Item 5a)
// Returns 0 on success
.global call_runtime_schedinit
.extern runtime.schedinit
call_runtime_schedinit:
    // Save callee-saved registers and create stack frame
    stp x29, x30, [sp, #-32]!
    mov x29, sp
    stp x19, x20, [sp, #16]

    // Call runtime.schedinit()
    // This will:
    // - Call lockInit() for all runtime locks (uses futex syscall)
    // - Initialize scheduler structures
    // - Set up processors (P)
    // - Initialize system monitor
    bl runtime.schedinit

    // If we get here, schedinit() completed without crash
    mov x0, #0              // Return 0 = success

    // Restore and return
    ldp x19, x20, [sp, #16]
    ldp x29, x30, [sp], #32
    ret

// =================================================================
// Test function: call_runtime_newproc
// Calls runtime.newproc(runtime.mainPC) to create the main goroutine
// This tests Item 6 of the runtime master plan.
//
// runtime.newproc takes a *funcval as parameter.
// funcval is a struct with a single field: fn uintptr (function pointer)
// runtime.mainPC is a global variable (funcval) containing the address of runtime.main
//
// ARM64 calling convention for newproc:
//   SP+0: dummy LR (0)
//   SP+8: pointer to funcval (address of runtime.mainPC)
//
// Returns 0 on success (newproc completed without crash)
// =================================================================
.global call_runtime_newproc
.extern runtime.newproc
.extern runtime.mainPC
call_runtime_newproc:
    // Save callee-saved registers and create stack frame
    // We need extra space for newproc's calling convention
    stp x29, x30, [sp, #-48]!
    mov x29, sp
    stp x19, x20, [sp, #16]

    // Prepare to call runtime.newproc(runtime.mainPC)
    // Following the same pattern as rt0_go in asm_arm64.s lines 124-129

    // Load address of runtime.mainPC (this is a *funcval)
    ldr x0, =runtime.mainPC

    // Set up stack for newproc call:
    //   SP+0: dummy LR (0)
    //   SP+8: funcval pointer (runtime.mainPC)
    sub sp, sp, #16
    str xzr, [sp, #0]       // Store 0 at SP+0 (dummy LR)
    str x0, [sp, #8]        // Store funcval pointer at SP+8

    // Call runtime.newproc
    bl runtime.newproc

    // Clean up newproc's stack frame
    add sp, sp, #16

    // If we get here, newproc() completed without crash
    mov x0, #0              // Return 0 = success

    // Restore and return
    ldp x19, x20, [sp, #16]
    ldp x29, x30, [sp], #48
    ret

// =================================================================
// Data section: funcval for simpleMain
// This is similar to runtime.mainPC - a funcval pointing to our main function
// =================================================================
.section ".data"
.global simpleMainPC
.align 3
simpleMainPC:
    .quad main.simpleMain

.section ".text"

// =================================================================
// call_newproc_simple_main()
// Call runtime.newproc(simpleMainPC) to create goroutine for our simple test
// simpleMainPC is a funcval (pointer to main.simpleMain)
//
// Returns 0 on success (newproc completed without crash)
// =================================================================
.global call_newproc_simple_main
.extern main.simpleMain
call_newproc_simple_main:
    // Save callee-saved registers and create stack frame
    stp x29, x30, [sp, #-48]!
    mov x29, sp
    stp x19, x20, [sp, #16]

    // Load address of simpleMainPC (the funcval structure)
    // This is like runtime.mainPC - it contains a pointer to the function
    ldr x0, =simpleMainPC

    // Set up stack for newproc call:
    //   SP+0: dummy LR (0)
    //   SP+8: funcval pointer (simpleMainPC)
    sub sp, sp, #16
    str xzr, [sp, #0]       // Store 0 at SP+0 (dummy LR)
    str x0, [sp, #8]        // Store funcval pointer at SP+8

    // Call runtime.newproc
    bl runtime.newproc

    // Clean up newproc's stack frame
    add sp, sp, #16

    // If we get here, newproc() completed without crash
    mov x0, #0              // Return 0 = success

    // Restore and return
    ldp x19, x20, [sp, #16]
    ldp x29, x30, [sp], #48
    ret

// =================================================================
// call_runtime_mstart()
// Call runtime.mstart() to start the scheduler
// This function should never return
// =================================================================
.global call_runtime_mstart
.extern runtime.mstart.abi0
call_runtime_mstart:
    // Save callee-saved registers and create stack frame
    stp x29, x30, [sp, #-32]!
    mov x29, sp
    stp x19, x20, [sp, #16]

    // Call runtime.mstart.abi0()
    // This should never return - it starts the scheduler
    bl runtime.mstart.abi0

    // If we somehow get here, restore and return
    // (This should never happen)
    ldp x19, x20, [sp, #16]
    ldp x29, x30, [sp], #32
    ret

// Bridge function: kernel_main -> main.KernelMain (Go function)
// This allows boot.s to call kernel_main, which then calls the Go KernelMain function
// Go exports it as main.KernelMain (package.function)
.global kernel_main
.extern main.KernelMain
.extern main.GrowStackForCurrent
kernel_main:
    // Set up proper ARM64 stack frame for Go compatibility
    // Save frame pointer and link register
    stp x29, x30, [sp, #-16]!      // Push FP and LR, adjust SP
    mov x29, sp                    // Set FP to current SP

    // UART will be initialized by uartInit() called from kernel_main
    // No early debug writes

    // Function signature: KernelMain(r0, r1, atags uint32)
    // AArch64 calling convention: first 8 parameters in x0-x7
    //
    // NOTE: In QEMU virt, the DTB pointer is provided by QEMU in x0 at reset.
    // boot.s preserves that pointer and passes it to kernel_main in x2, so DO NOT clobber x2 here.
    mov x0, #0                    // r0 = 0
    mov x1, #0                    // r1 = 0

    // Set x28 (goroutine pointer) to point to runtime.g0
    // This is required for write barrier to work
    // Use linker symbol (not hardcoded address) so BSS can be relocated
    ldr x28, =runtime.g0

    // Note: Write barrier flag is set in boot.s AFTER BSS clear
    // (Setting it here would be overwritten by BSS clear)

    // Call Go function - this will initialize everything
    bl main.KernelMain

    // KernelMain returns after initialization is complete
    // Restore frame pointer and link register
    ldp x29, x30, [sp], #16       // Pop FP and LR, adjust SP
    ret                            // Return to boot.s

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
    
    // Print 'S' to show we're about to halt
    stp x0, x1, [sp, #-16]!  // Save x0, x1 again
    mov x0, #0x53  // 'S'
    bl uart_putc_pl011
    ldp x0, x1, [sp], #16  // Restore x0, x1
    
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
// Timer functions moved to lib_timer.s (Go/Plan9 assembly)
// MMU functions moved to lib_mmu.s (Go/Plan9 assembly)
// System register functions moved to lib_sysregs.s (Go/Plan9 assembly)
// ============================================================================

// =================================================================
// Kmazarin Kernel Loading
// =================================================================

// jump_to_kmazarin(entryAddr uintptr, argc uint64, argv uintptr, stackPointer uintptr)
// This function sets up the Go runtime environment and jumps to kmazarin
// Parameters (ARM64 calling convention):
//   x0 = entryAddr - address to jump to
//   x1 = argc - argument count
//   x2 = argv - pointer to argv array
//   x3 = stackPointer - pointer to argc/argv/envp/auxv structure
// Sets up registers as expected by Go runtime _rt0_arm64_linux:
//   R0 = argc
//   R1 = argv
//   SP = stackPointer (pointing to the full structure)
// NOTE: This function never returns
.global jump_to_kmazarin
jump_to_kmazarin:
    // Save entry point address to x4 (we need x0 for argc)
    mov x4, x0

    // Set up Go runtime registers:
    // R0 = argc (from x1)
    mov x0, x1

    // R1 = argv (from x2)
    mov x1, x2

    // SP = stackPointer (from x3)
    // CRITICAL: The Go runtime expects SP to point to the start of the structure
    // which contains argc at [SP+0], argv at [SP+8], envp at [SP+16], auxv at [SP+32]
    mov sp, x3

    // Jump to kmazarin entry point (_rt0_arm64_linux)
    // At this point:
    //   R0 = argc = 1
    //   R1 = argv = pointer to argv array
    //   SP = pointer to full argc/argv/envp/auxv structure
    br x4                     // Branch to entry point - never returns

// Cache maintenance and semihosting functions moved to lib_misc.s and lib_mmu.s (Go/Plan9 assembly)
// NOTE: Nop moved to asm/goasm/lib_barriers.s
