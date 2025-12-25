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

// get_g0_addr() - returns address of runtime.g0
// This allows Go code to get the g0 address without hardcoding
.global get_g0_addr
get_g0_addr:
    ldr x0, =runtime.g0
    ret

// get_m0_addr() - returns address of runtime.m0
// This allows Go code to get the m0 address without hardcoding
.global get_m0_addr
get_m0_addr:
    ldr x0, =runtime.m0
    ret

// getGRegister() - returns current value of g register (x28)
// This is for debugging to see what g points to
.global getGRegister
getGRegister:
    mov x0, x28
    ret

// call_mallocinit()
// Call runtime.mallocinit from assembly.
// Minimal assembly wrapper that just calls mallocinit (not full schedinit).
// physPageSize should be set from Go before calling this.
.global call_mallocinit
.extern runtime.mallocinit
call_mallocinit:
    // Save frame pointer and link register
    stp x29, x30, [sp, #-16]!
    mov x29, sp

    // Call runtime.mallocinit()
    // This initializes just the heap allocator, not full scheduler.
    bl runtime.mallocinit

    // Restore frame pointer and link register
    ldp x29, x30, [sp], #16
    ret

// get_phys_page_size_addr()
// Returns uintptr
// This allows Go code to set physPageSize before calling schedinit.
.global get_phys_page_size_addr
.extern runtime.physPageSize
get_phys_page_size_addr:
    ldr x0, =runtime.physPageSize
    ret

// get_mcache0_addr() - returns address of runtime.mcache0
// This allows Go code to get the mcache0 address without hardcoding
.global get_mcache0_addr
.extern runtime.mcache0
get_mcache0_addr:
    ldr x0, =runtime.mcache0
    ret

// get_emptymspan_addr() - returns address of runtime.emptymspan
// This allows Go code to get the emptymspan address without hardcoding
.global get_emptymspan_addr
.extern runtime.emptymspan
get_emptymspan_addr:
    ldr x0, =runtime.emptymspan
    ret

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

// store_pointer_nobarrier(dest *unsafe.Pointer, value unsafe.Pointer)
// x0 = destination address, x1 = pointer value to store
// Stores a pointer without triggering Go's write barrier
// Minimal implementation - no stack frame needed
.global store_pointer_nobarrier
store_pointer_nobarrier:
    str x1, [x0]            // Store pointer directly
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
// OPTIMIZED: Uses 128-bit stores (STP) for 16x speedup
.global bzero
bzero:
    cbz w1, bzero_done      // If size is zero, skip
    mov x2, #0              // Zero value (64-bit)
    mov x3, #0              // Zero value (64-bit)

    // Fast path: 16-byte chunks using STP (store pair)
bzero_loop_16:
    cmp w1, #16             // Check if at least 16 bytes remain
    blt bzero_loop_8        // If less than 16, do 8-byte stores
    stp x2, x3, [x0], #16   // Store 16 bytes (two 64-bit zeros) and increment
    sub w1, w1, #16         // Decrement by 16
    b bzero_loop_16         // Repeat

    // Medium path: 8-byte chunks
bzero_loop_8:
    cmp w1, #8              // Check if at least 8 bytes remain
    blt bzero_loop_1        // If less than 8, do byte stores
    str x2, [x0], #8        // Store 8 bytes and increment
    sub w1, w1, #8          // Decrement by 8
    b bzero_loop_8          // Repeat

    // Slow path: remaining bytes
bzero_loop_1:
    cbz w1, bzero_done      // If size is zero, done
    strb w2, [x0], #1       // Store byte and increment
    sub w1, w1, #1          // Decrement by 1
    b bzero_loop_1          // Repeat

bzero_done:
    ret                     // Return

// dsb() - Data Synchronization Barrier
// Ensures all memory accesses before this instruction complete before continuing
.global dsb
dsb:
    dsb sy              // Data Synchronization Barrier - system-wide
    ret                  // Return

// isb() - Instruction Synchronization Barrier
// Ensures all instructions before this barrier complete before continuing
.global isb
isb:
    isb                 // Instruction Synchronization Barrier
    ret                 // Return

// get_stack_pointer() - Returns current stack pointer value
// Returns uintptr_t (64-bit) in x0
.global get_stack_pointer
get_stack_pointer:
    mov x0, sp           // Move stack pointer to x0 (return value)
    ret                  // Return

// ============================================================================
// System Control Register (SCTLR) Access Functions
// These are used to diagnose alignment fault issues
// ============================================================================

// read_sctlr_el1() - Read SCTLR_EL1 (System Control Register for EL1)
// Returns uint64 in x0
// Key bits:
//   Bit 0 (M): MMU enable (1=enabled, 0=disabled)
//   Bit 1 (A): Alignment check enable (1=faults on unaligned, 0=allows unaligned)
//   Bit 2 (C): Data cache enable
//   Bit 12 (I): Instruction cache enable
.global read_sctlr_el1
read_sctlr_el1:
    mrs x0, SCTLR_EL1    // Read SCTLR_EL1 into x0
    ret                   // Return

// write_sctlr_el1(value uint64) - Write SCTLR_EL1
// x0 = value to write
.global write_sctlr_el1
// Alternative minimal MMU enable function - test if location matters
.global enable_mmu_minimal
enable_mmu_minimal:
    // x0 = SCTLR value
    // Minimal function - just enable MMU and return
    msr SCTLR_EL1, x0
    isb
    ret

write_sctlr_el1:
    // Clean implementation - no debug breadcrumbs
    // Now that stack is mapped, this should work

    // Save x30 (link register) on stack - stack is now mapped!
    str x30, [sp, #-16]!

    // Ensure all prior operations complete
    dsb sy
    isb

    // Invalidate TLB before enabling MMU
    tlbi vmalle1
    dsb sy
    isb

    // Write SCTLR_EL1 to enable MMU
    msr SCTLR_EL1, x0
    isb

    // Invalidate caches after MMU enable
    ic iallu
    tlbi vmalle1
    dsb sy
    isb

    // Restore x30 and return
    ldr x30, [sp], #16
    ret

// disable_alignment_check() - Clear the A bit in SCTLR_EL1 to disable alignment faults
// This allows unaligned memory accesses to Normal memory regions
.global disable_alignment_check
disable_alignment_check:
    mrs x0, SCTLR_EL1    // Read current SCTLR_EL1
    bic x0, x0, #2       // Clear bit 1 (A bit) - disable alignment check
    msr SCTLR_EL1, x0    // Write modified value back
    isb                   // Instruction synchronization barrier
    ret

// set_stack_pointer(sp uintptr) - Sets stack pointer register
// x0 = new stack pointer value
.global set_stack_pointer
set_stack_pointer:
    // SP ALIGNMENT CHECK: Verify SP is 16-byte aligned before setting
    and x1, x0, #0xF               // Check alignment (lower 4 bits)
    cbnz x1, sp_misaligned_set      // If not zero, SP is misaligned!
    
    // SP is aligned, set it normally
    mov sp, x0           // Set stack pointer from x0
    dsb sy               // Memory barrier to ensure SP update is visible
    ret                  // Return
    
sp_misaligned_set:
    // SP was misaligned!
    // Print diagnostic via UART (minimal, no stack)
    // Save x0 (SP value) to x2 before using registers
    mov x2, x0                       // Save SP value
    
    movz x1, #0x0900, lsl #16      // UART base = 0x09000000
    movk x1, #0x0000, lsl #0
    
    // Print "SP-MISALIGN: set_stack_pointer SP=0x"
    //     movz w3, #0x53                 // 'S' - BREADCRUMB DISABLED
    //     str w3, [x1] - BREADCRUMB DISABLED
    //     movz w3, #0x50                 // 'P' - BREADCRUMB DISABLED
    //     str w3, [x1] - BREADCRUMB DISABLED
    //     movz w3, #0x2D                 // '-' - BREADCRUMB DISABLED
    //     str w3, [x1] - BREADCRUMB DISABLED
    //     movz w3, #0x4D                 // 'M' - BREADCRUMB DISABLED
    //     str w3, [x1] - BREADCRUMB DISABLED
    //     movz w3, #0x49                 // 'I' - BREADCRUMB DISABLED
    //     str w3, [x1] - BREADCRUMB DISABLED
    //     movz w3, #0x53                 // 'S' - BREADCRUMB DISABLED
    //     str w3, [x1] - BREADCRUMB DISABLED
    //     movz w3, #0x41                 // 'A' - BREADCRUMB DISABLED
    //     str w3, [x1] - BREADCRUMB DISABLED
    //     movz w3, #0x4C                 // 'L' - BREADCRUMB DISABLED
    //     str w3, [x1] - BREADCRUMB DISABLED
    //     movz w3, #0x49                 // 'I' - BREADCRUMB DISABLED
    //     str w3, [x1] - BREADCRUMB DISABLED
    //     movz w3, #0x47                 // 'G' - BREADCRUMB DISABLED
    //     str w3, [x1] - BREADCRUMB DISABLED
    //     movz w3, #0x3A                 // ':' - BREADCRUMB DISABLED
    str w3, [x1]
    movz w3, #0x20                 // ' '
    str w3, [x1]
    movz w3, #0x73                 // 's'
    str w3, [x1]
    movz w3, #0x65                 // 'e'
    str w3, [x1]
    movz w3, #0x74                 // 't'
    str w3, [x1]
    movz w3, #0x5F                 // '_'
    str w3, [x1]
    movz w3, #0x73                 // 's'
    str w3, [x1]
    movz w3, #0x70                 // 'p'
    str w3, [x1]
    
    // Round down to 16-byte boundary and set SP anyway
    bic x0, x2, #0xF                // Clear lower 4 bits to align (use saved value)
    mov sp, x0                       // Set aligned SP
    dsb sy                           // Memory barrier
    ret                              // Return

// set_g_pointer(g uintptr) - Sets x28 (g pointer register)
// x0 = new goroutine pointer
.global set_g_pointer
set_g_pointer:
    mov x28, x0          // Set x28 (g pointer) to new goroutine
    dsb sy               // Memory barrier
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

    // DEBUG: Print 'H' before calling runtime.args
    mov x20, #0x09000000    // UART base
    mov w21, #'H'
    str w21, [x20]

    bl runtime.args

    // DEBUG: Print 'I' after runtime.args returns
    mov x20, #0x09000000    // UART base
    mov w21, #'I'
    str w21, [x20]

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

    // DEBUG: Print '<' before calling schedinit (marker 1)
    movz x0, #0x0900, lsl #16
    movz w1, #0x3C              // '<'
    str w1, [x0]

    // Call runtime.schedinit()
    // This will:
    // - Call lockInit() for all runtime locks (uses futex syscall)
    // - Initialize scheduler structures
    // - Set up processors (P)
    // - Initialize system monitor
    bl runtime.schedinit

    // DEBUG: Print '>' after schedinit returns (marker 2)
    movz x0, #0x0900, lsl #16
    movz w1, #0x3E              // '>'
    str w1, [x0]

    // DEBUG: Print '!' to confirm we got here (marker 3)
    movz x0, #0x0900, lsl #16
    movz w1, #0x21              // '!'
    str w1, [x0]

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
    // DEBUG: Print 'G' after Go KernelMain returns
    movz x10, #0x0900, lsl #16   // UART base = 0x09000000
    movz w11, #0x47              // 'G' = Go KernelMain returned
    str w11, [x10]               // Write to UART
    
    // DEBUG: Print 'Z' before returning to boot.s
    movz x10, #0x0900, lsl #16   // UART base = 0x09000000
    movz w11, #0x5A              // 'Z' = aboZt to return to boot.s
    str w11, [x10]               // Write to UART
    
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
    
    // BREADCRUMB: Print 'M' to show morestack was called
    // Save x0 before using it
    stp x0, x1, [sp, #-16]!  // Save x0, x1
    mov x0, #0x4D  // 'M'
    bl uart_putc_pl011
    ldp x0, x1, [sp], #16  // Restore x0, x1
    
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
// ARM Generic Timer System Register Access
// IMPORTANT: Using VIRTUAL timer (CNTV_*) at EL1 - matches reference repo!
// Virtual timer is the standard choice for EL1 OS/kernel code
// ============================================================================

// read_cntv_ctl_el0() - Read CNTV_CTL_EL0 (Virtual Timer Control Register)
// Returns uint32 in w0
.global read_cntv_ctl_el0
read_cntv_ctl_el0:
    mrs x0, CNTV_CTL_EL0    // Read CNTV_CTL_EL0 into x0
    ret                      // Return (value in w0)

// write_cntv_ctl_el0(value uint32) - Write CNTV_CTL_EL0
// w0 = value to write
.global write_cntv_ctl_el0
write_cntv_ctl_el0:
    msr CNTV_CTL_EL0, x0    // Write x0 to CNTV_CTL_EL0
    isb                      // Instruction synchronization barrier
    ret

// read_cntv_tval_el0() - Read CNTV_TVAL_EL0 (Virtual Timer Value Register)
// Returns uint32 in w0
.global read_cntv_tval_el0
read_cntv_tval_el0:
    mrs x0, CNTV_TVAL_EL0   // Read CNTV_TVAL_EL0 into x0
    ret                      // Return (value in w0)

// write_cntv_tval_el0(value uint32) - Write CNTV_TVAL_EL0
// w0 = value to write
.global write_cntv_tval_el0
write_cntv_tval_el0:
    msr CNTV_TVAL_EL0, x0   // Write x0 to CNTV_TVAL_EL0
    isb                      // Instruction synchronization barrier
    ret

// read_cntv_cval_el0() - Read CNTV_CVAL_EL0 (Virtual Timer Compare Value Register)
// Returns uint64 in x0
.global read_cntv_cval_el0
read_cntv_cval_el0:
    mrs x0, CNTV_CVAL_EL0   // Read CNTV_CVAL_EL0 into x0
    ret                      // Return (value in x0)

// write_cntv_cval_el0(value uint64) - Write CNTV_CVAL_EL0
// x0 = value to write
.global write_cntv_cval_el0
write_cntv_cval_el0:
    msr CNTV_CVAL_EL0, x0   // Write x0 to CNTV_CVAL_EL0
    isb                      // Instruction synchronization barrier
    ret

// read_cntvct_el0() - Read CNTVCT_EL0 (Virtual Counter Register)
// Returns uint64 in x0
.global read_cntvct_el0
read_cntvct_el0:
    mrs x0, CNTVCT_EL0      // Read CNTVCT_EL0 into x0
    ret                      // Return (value in x0)

// read_cntfrq_el0() - Read CNTFRQ_EL0 (Counter Frequency Register)
// Returns uint32 in w0
.global read_cntfrq_el0
read_cntfrq_el0:
    mrs x0, CNTFRQ_EL0      // Read CNTFRQ_EL0 into x0
    ret                      // Return (value in w0)

// read_current_el() - Read CurrentEL (Current Exception Level)
// Returns uint32 in w0 (bits [3:2] contain EL)
.global read_current_el
read_current_el:
    mrs x0, CurrentEL       // Read CurrentEL into x0
    ret                      // Return (value in w0)

// read_id_aa64pfr0_el1() - Read ID_AA64PFR0_EL1 (Processor Feature Register)
// Returns uint64 in x0
// Bits [15:12] = EL3 support (0000 = not implemented, 0001 = AArch64, 0010 = AArch64+AArch32)
// Bits [7:4] = EL1 support
// Bits [3:0] = EL0 support
.global read_id_aa64pfr0_el1
read_id_aa64pfr0_el1:
    mrs x0, ID_AA64PFR0_EL1
    ret

// read_scr_el3() - Attempt to read SCR_EL3 (Secure Configuration Register)
// This will trap if we're at EL1, but we can catch the exception
// Returns uint64 in x0 (or 0 if trapped)
.global read_scr_el3
read_scr_el3:
    // NOTE: This will cause a sync exception at EL1
    // Only callable from EL3
    mrs x0, SCR_EL3
    ret

// PHYSICAL TIMER FUNCTIONS (CNTP_*) - for comparison with virtual timer
// Physical timer uses PPI 30 (virtual timer uses PPI 27)

// read_cntp_ctl_el0() - Read CNTP_CTL_EL0 (Physical Timer Control Register)
// Returns uint32 in w0
.global read_cntp_ctl_el0
read_cntp_ctl_el0:
    mrs x0, CNTP_CTL_EL0    // Read CNTP_CTL_EL0 into x0
    ret                      // Return (value in w0)

// write_cntp_ctl_el0(value uint32) - Write CNTP_CTL_EL0
// w0 = value to write
.global write_cntp_ctl_el0
write_cntp_ctl_el0:
    msr CNTP_CTL_EL0, x0    // Write x0 to CNTP_CTL_EL0
    isb                      // Instruction synchronization barrier
    ret

// read_cntp_tval_el0() - Read CNTP_TVAL_EL0 (Physical Timer Value Register)
// Returns uint32 in w0
.global read_cntp_tval_el0
read_cntp_tval_el0:
    mrs x0, CNTP_TVAL_EL0   // Read CNTP_TVAL_EL0 into x0
    ret                      // Return (value in w0)

// write_cntp_tval_el0(value uint32) - Write CNTP_TVAL_EL0
// w0 = value to write
.global write_cntp_tval_el0
write_cntp_tval_el0:
    msr CNTP_TVAL_EL0, x0   // Write x0 to CNTP_TVAL_EL0
    isb                      // Instruction synchronization barrier
    ret

// read_cntp_cval_el0() - Read CNTP_CVAL_EL0 (Physical Timer Compare Value Register)
// Returns uint64 in x0
.global read_cntp_cval_el0
read_cntp_cval_el0:
    mrs x0, CNTP_CVAL_EL0   // Read CNTP_CVAL_EL0 into x0
    ret                      // Return (value in x0)

// write_cntp_cval_el0(value uint64) - Write CNTP_CVAL_EL0
// x0 = value to write
.global write_cntp_cval_el0
write_cntp_cval_el0:
    msr CNTP_CVAL_EL0, x0   // Write x0 to CNTP_CVAL_EL0
    isb                      // Instruction synchronization barrier
    ret

// read_cntpct_el0() - Read CNTPCT_EL0 (Physical Counter Register)
// Returns uint64 in x0
.global read_cntpct_el0
read_cntpct_el0:
    mrs x0, CNTPCT_EL0      // Read CNTPCT_EL0 into x0
    ret                      // Return (value in x0)

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

// ============================================================================
// MMU System Register Access Functions
// ============================================================================

// read_ttbr0_el1() - Read TTBR0_EL1 (Translation Table Base Register 0)
// Returns uint64 in x0 (Go ABI: x0 is the return value register for uint64)
.global read_ttbr0_el1
read_ttbr0_el1:
    mrs x0, TTBR0_EL1    // Read TTBR0_EL1 into x0 (return value register for uint64)
    ret                   // Return (x0 contains the 64-bit value)

// write_ttbr0_el1(value uint64) - Write TTBR0_EL1
// x0 = value to write (Go ABI: first parameter in x0 for uint64)
// Must be 4KB aligned, lower 12 bits ignored by hardware
.global write_ttbr0_el1
write_ttbr0_el1:
    // x0 already contains the parameter (Go ABI: first uint64 param in x0)
    msr TTBR0_EL1, x0    // Write x0 (parameter) to TTBR0_EL1
    isb                   // Instruction synchronization barrier
    ret                   // Return (no return value, void function)

// write_ttbr1_el1(value uint64) - Write TTBR1_EL1
// x0 = value to write (Go ABI: first parameter in x0 for uint64)
// Must be 4KB aligned, lower 12 bits ignored by hardware
// Even when EPD1=1 (TTBR1 disabled), should be initialized to a safe value
.global write_ttbr1_el1
write_ttbr1_el1:
    // x0 already contains the parameter (Go ABI: first uint64 param in x0)
    msr TTBR1_EL1, x0    // Write x0 (parameter) to TTBR1_EL1
    isb                   // Instruction synchronization barrier
    ret                   // Return (no return value, void function)

// read_mair_el1() - Read MAIR_EL1 (Memory Attribute Indirection Register)
// Returns uint64 in x0 (Go ABI: x0 is the return value register for uint64)
.global read_mair_el1
read_mair_el1:
    mrs x0, MAIR_EL1      // Read MAIR_EL1 into x0 (return value register for uint64)
    ret                    // Return (x0 contains the 64-bit value)

// write_mair_el1(value uint64) - Write MAIR_EL1
// x0 = value to write (Go ABI: first parameter in x0 for uint64)
// MAIR_EL1 format: 8 attributes, 8 bits each
//   Attr0 (bits 7:0)   = Normal, Inner/Outer Write-Back Cacheable (0xFF)
//   Attr1 (bits 15:8)  = Device-nGnRnE (0x00)
.global write_mair_el1
write_mair_el1:
    // x0 already contains the parameter (Go ABI: first uint64 param in x0)
    msr MAIR_EL1, x0      // Write x0 (parameter) to MAIR_EL1
    isb                    // Instruction synchronization barrier
    ret                    // Return (no return value, void function)

// read_tcr_el1() - Read TCR_EL1 (Translation Control Register)
// Returns uint64 in x0 (Go ABI: x0 is the return value register for uint64)
.global read_tcr_el1
read_tcr_el1:
    mrs x0, TCR_EL1       // Read TCR_EL1 into x0 (return value register for uint64)
    ret                    // Return (x0 contains the 64-bit value)

// write_tcr_el1(value uint64) - Write TCR_EL1
// x0 = value to write (Go ABI: first parameter in x0 for uint64)
.global write_tcr_el1
write_tcr_el1:
    // x0 already contains the parameter (Go ABI: first uint64 param in x0)
    msr TCR_EL1, x0      // Write x0 (parameter) to TCR_EL1
    isb                   // Instruction synchronization barrier
    ret                   // Return (no return value, void function)

// read_tpidr_el0() uint64 - Read TPIDR_EL0 (Thread Pointer ID Register, User)
// Returns: x0 = current value of TPIDR_EL0
// TPIDR_EL0 is used by Go runtime for thread-local storage (TLS)
// Can be accessed from EL1 (kernel mode) without restriction
.global read_tpidr_el0
read_tpidr_el0:
    mrs x0, TPIDR_EL0    // Read TPIDR_EL0 into x0 (return value)
    ret                   // Return with value in x0

// write_tpidr_el0(value uint64) - Write TPIDR_EL0 (Thread Pointer ID Register, User)
// x0 = value to write (Go ABI: first parameter in x0 for uint64)
// TPIDR_EL0 is used by Go runtime for thread-local storage (TLS)
// Can be written from EL1 (kernel mode) without restriction
.global write_tpidr_el0
write_tpidr_el0:
    msr TPIDR_EL0, x0    // Write x0 (parameter) to TPIDR_EL0
    isb                   // Instruction synchronization barrier
    ret                   // Return (no return value, void function)

// read_tpidr_el1() uint64 - Read TPIDR_EL1 (Thread Pointer ID Register, Kernel)
// Returns: x0 = current value of TPIDR_EL1
// TPIDR_EL1 is used for kernel thread-local storage at EL1
.global read_tpidr_el1
read_tpidr_el1:
    mrs x0, TPIDR_EL1    // Read TPIDR_EL1 into x0 (return value)
    ret                   // Return with value in x0

// write_tpidr_el1(value uint64) - Write TPIDR_EL1 (Thread Pointer ID Register, Kernel)
// x0 = value to write (Go ABI: first parameter in x0 for uint64)
// TPIDR_EL1 is used for kernel thread-local storage at EL1
// This allows kernel and user space to have separate TLS without conflicts
.global write_tpidr_el1
write_tpidr_el1:
    msr TPIDR_EL1, x0    // Write x0 (parameter) to TPIDR_EL1
    isb                   // Instruction synchronization barrier
    ret                   // Return (no return value, void function)

// invalidate_tlb_all() - Invalidate entire TLB
// Clears all translation lookaside buffer entries
// NOTE: Uses tlbi vmalle1 (not alle1) because this can be executed
// with MMU disabled during bootloader initialization.
// tlbi alle1 requires MMU to be enabled and would cause UNDEFINED behavior.
.global invalidate_tlb_all
invalidate_tlb_all:
    dsb sy                   // Ensure all memory accesses complete
    tlbi vmalle1             // Invalidate all EL1 TLB entries for current VMID
    dsb sy                   // Ensure TLB invalidation completes
    isb                      // Instruction synchronization barrier
    ret

// invalidate_tlb_va(addr uintptr) - Invalidate TLB entry for specific virtual address
// This is much faster than invalidating the entire TLB when mapping a single page
// Parameters:
//   x0 = Virtual address to invalidate (page-aligned)
.global invalidate_tlb_va
invalidate_tlb_va:
    lsr x0, x0, #12          // Convert to page number (VA >> 12)
    dsb ishst                // Ensure prior writes complete before TLB invalidation
    tlbi vae1, x0            // Invalidate TLB entry for this VA at EL1
    dsb ish                  // Ensure TLB invalidation completes
    isb                      // Instruction synchronization barrier
    ret

// clean_dcache_va(addr uintptr) - Clean data cache for specific virtual address
// This ensures modified page table entries are visible to hardware page table walker
// Parameters:
//   x0 = Virtual address to clean
.global clean_dcache_va
clean_dcache_va:
    dc cvac, x0              // Clean data cache by VA to Point of Coherency
    dsb ish                  // Ensure cache clean completes
    ret

// get_current_g() uintptr - Returns pointer to current goroutine from x28 register
// The Go runtime stores the current goroutine pointer in x28 (g register)
// Returns: uintptr - Pointer to current G structure
.global get_current_g
get_current_g:
    mov x0, x28              // Return current g pointer from x28 register
    ret

// set_current_g(gptr uintptr) - Sets the current goroutine pointer in x28 register
// The Go runtime expects the current goroutine pointer to be in x28
// Parameters:
//   x0 = gptr (uintptr - pointer to goroutine structure)
.global set_current_g
set_current_g:
    mov x28, x0              // Set g register (x28) to the provided G pointer
    ret

// CleanDataCacheVA(addr uintptr) - Clean data cache by virtual address
// This ensures writes to page tables are visible to the MMU's page table walker
// Parameters:
//   x0 = addr (virtual address to clean)
// Uses DC CVAC (Data Cache Clean by VA to point of Coherency)
.global CleanDataCacheVA
CleanDataCacheVA:
    dc cvac, x0              // Clean data cache line containing address in x0
    dsb sy                   // Ensure clean completes before continuing
    ret

// InvalidateInstructionCacheAll() - Invalidate all instruction caches
// This ensures the CPU fetches fresh instructions from memory after code is modified
// Used after relocating exception vectors or self-modifying code
// No parameters
.global InvalidateInstructionCacheAll
InvalidateInstructionCacheAll:
    ic iallu                 // Invalidate all instruction caches to Point of Unification
    dsb sy                   // Ensure invalidation completes
    isb                      // Synchronize context
    ret

// read_ctr_el0() - Read Cache Type Register
// Returns uint64 in x0: CTR_EL0 which describes cache characteristics
// Bit 28 (IDC): Data cache clean NOT required for I/D coherency if 1
// Bit 29 (DIC): Instruction cache invalidation NOT required for I/D coherency if 1
.global read_ctr_el0
read_ctr_el0:
    mrs x0, ctr_el0          // Read Cache Type Register
    ret

// getCurrentSP() uintptr - Returns the current stack pointer
// This is used for stack tracing / debugging
.global getCurrentSP
getCurrentSP:
    mov x0, sp               // Copy stack pointer to x0 (return value)
    ret


// set_vbar_el1_to_addr(addr uintptr) - Set VBAR_EL1 to specific address
// Used to relocate exception vectors to safe RAM location
// NOTE: Caller must execute DSB + ISB after this returns
.global set_vbar_el1_to_addr
set_vbar_el1_to_addr:
    msr vbar_el1, x0         // Set VBAR_EL1 to address in x0
    ret                       // Return (barriers done by caller)

// Wfi() - Wait For Interrupt
// Puts the CPU in low-power mode until an interrupt arrives
// This is safe to call in a loop for halting
.global Wfi
Wfi:
    wfi                       // Wait for interrupt
    ret

// jump_to_null - Jumps to address 0 to trigger a prefetch abort
// Used for testing exception handler traceback functionality
.global jump_to_null
jump_to_null:
    mov x0, #0                // Load address 0
    br x0                     // Branch to NULL - will cause prefetch abort

// =================================================================
// Runtime Stack Size Configuration
// =================================================================

// Declare runtime symbols we need to access
.extern runtime.maxstacksize
.extern runtime.maxstackceiling

// set_maxstacksize(size uintptr) - Set runtime.maxstacksize
// Parameter: x0 = new max stack size in bytes
.global set_maxstacksize
set_maxstacksize:
    adrp x1, runtime.maxstacksize
    add x1, x1, :lo12:runtime.maxstacksize
    str x0, [x1]              // Store new value
    ret

// set_maxstackceiling(size uintptr) - Set runtime.maxstackceiling
// Parameter: x0 = new max stack ceiling in bytes
.global set_maxstackceiling
set_maxstackceiling:
    adrp x1, runtime.maxstackceiling
    add x1, x1, :lo12:runtime.maxstackceiling
    str x0, [x1]              // Store new value
    ret

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
