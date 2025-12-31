// exceptions.s
// AArch64 Exception Vector Table and Exception Handlers
//
// Layout: Exception vector table must be 2KB aligned (2048 bytes)
// Contains 4 groups of 4 exception handlers (128 bytes each)
// We use Group 2 (Current EL, using SP_EL1) for kernel at EL1
//
// Group 2: Current EL, using SP_EL1 (0x200-0x3ff in vector table)
//   0x200 - 0x280: Synchronous exception
//   0x280 - 0x300: IRQ (Interrupt Request)
//   0x300 - 0x380: FIQ (Fast Interrupt Request)
//   0x380 - 0x400: SError (System Error)

// Include kmazarin symbol addresses (auto-generated from kmazarin.elf)
.include "build/mazboot/kmazarin_symbols.s"

// ========== DATA SECTION ==========
.data
.align 3

// Declare Go function for exception handling
.extern ExceptionHandler

.text  // CRITICAL: Switch to text section for code!

// ============================================================================
// EXCEPTION FRAME LAYOUT AND MACROS
// ============================================================================

// Exception Frame Offsets (320-byte frame on SP_EL1 exception stack)
// --------------------------------------------------------------------
// Standard saved state:
.equ EXC_FRAME_X0_X1,        0      // x0, x1 saved here
.equ EXC_FRAME_X28,          224    // x28 (g pointer)
.equ EXC_FRAME_X29_X30,      232    // x29, x30 (FP, LR)
.equ EXC_FRAME_ORIG_SP,      248    // Original SP before exception
.equ EXC_FRAME_ELR_SPSR,     256    // ELR_EL1, SPSR_EL1
.equ EXC_FRAME_FAR_ESR,      272    // FAR_EL1, ESR_EL1

// Extended state (uses previously unused space 288-319):
.equ EXC_FRAME_SP_EL0,       288    // Saved SP_EL0 (kmazarin stack)
.equ EXC_FRAME_SAVED_G,      296    // Saved g for syscall (original x28)
.equ EXC_FRAME_SAVED_X0,     304    // Saved x0 for syscall return value
.equ EXC_FRAME_SAVED_LR,     312    // Saved LR for syscall

// Total frame size
.equ EXC_FRAME_SIZE,         320

// ============================================================================
// MACROS FOR STACK-BASED STATE MANAGEMENT
// ============================================================================

// Save SP_EL0 to exception stack frame (replaces fixed scratch area at 0x40FFF000)
// Clobbers: x10
.macro SAVE_SP_EL0_TO_STACK
    mrs x10, SP_EL0                     // x10 = current SP_EL0
    str x10, [sp, #EXC_FRAME_SP_EL0]   // Save to exception frame at offset 288
.endm

// Restore SP_EL0 from exception stack frame
// Clobbers: x10
.macro RESTORE_SP_EL0_FROM_STACK
    ldr x10, [sp, #EXC_FRAME_SP_EL0]   // Load saved SP_EL0 from offset 288
    msr SP_EL0, x10                     // Restore it
    isb                                 // Ensure write completes
.endm

// Save syscall context to exception frame (replaces scratch area at 0x40FFF020)
// This preserves critical state across Go syscall handler calls
// Input: x0 = return value to save, x28 = g to save, x30 = LR to save
// Clobbers: none (only stores)
.macro SAVE_SYSCALL_CONTEXT
    str x0, [sp, #EXC_FRAME_SAVED_X0]   // Save syscall return value (offset 304)
    str x28, [sp, #EXC_FRAME_SAVED_G]   // Save original g (offset 296)
    str x30, [sp, #EXC_FRAME_SAVED_LR]  // Save original LR (offset 312)
.endm

// Restore syscall context from exception frame
// Output: x0, x28, x30 restored
// Clobbers: none (loads directly to target registers)
.macro RESTORE_SYSCALL_CONTEXT
    ldr x0, [sp, #EXC_FRAME_SAVED_X0]   // Restore syscall return value
    ldr x28, [sp, #EXC_FRAME_SAVED_G]   // Restore original g
    ldr x30, [sp, #EXC_FRAME_SAVED_LR]  // Restore original LR
.endm

// ============================================================================
// INTERRUPT FULL SAVE/RESTORE MACROS
// ============================================================================
// These macros save and restore ALL registers across an interrupt.
// This ensures the interrupted code is completely unaware of the interrupt.
//
// Frame layout (272 bytes, 16-byte aligned):
//   Offset   Contents
//   ------   --------
//   0-7      x0
//   8-15     x1
//   16-23    x2
//   24-31    x3
//   ...      ... (pairs of registers)
//   224-231  x28
//   232-239  x29
//   240-247  x30
//   248-255  SP_EL0 (interrupted code's stack pointer)
//   256-263  ELR_EL1 (return address)
//   264-271  SPSR_EL1 (saved processor state)
//
// After INTERRUPT_FULL_SAVE:
//   - All registers saved to exception stack
//   - x0 = interrupt ID (from GIC IAR, masked to 10 bits)
//   - SP = exception stack with frame allocated
//
// INTERRUPT_FULL_RESTORE:
//   - Signals EOI to GIC (uses interrupt ID from x0 parameter)
//   - Restores all registers exactly as they were
//   - Executes eret to return to interrupted code

.equ IRQ_FRAME_SIZE, 272
.equ IRQ_FRAME_X0,      0
.equ IRQ_FRAME_X1,      8
.equ IRQ_FRAME_X2,      16
.equ IRQ_FRAME_X3,      24
.equ IRQ_FRAME_X4,      32
.equ IRQ_FRAME_X5,      40
.equ IRQ_FRAME_X6,      48
.equ IRQ_FRAME_X7,      56
.equ IRQ_FRAME_X8,      64
.equ IRQ_FRAME_X9,      72
.equ IRQ_FRAME_X10,     80
.equ IRQ_FRAME_X11,     88
.equ IRQ_FRAME_X12,     96
.equ IRQ_FRAME_X13,     104
.equ IRQ_FRAME_X14,     112
.equ IRQ_FRAME_X15,     120
.equ IRQ_FRAME_X16,     128
.equ IRQ_FRAME_X17,     136
.equ IRQ_FRAME_X18,     144
.equ IRQ_FRAME_X19,     152
.equ IRQ_FRAME_X20,     160
.equ IRQ_FRAME_X21,     168
.equ IRQ_FRAME_X22,     176
.equ IRQ_FRAME_X23,     184
.equ IRQ_FRAME_X24,     192
.equ IRQ_FRAME_X25,     200
.equ IRQ_FRAME_X26,     208
.equ IRQ_FRAME_X27,     216
.equ IRQ_FRAME_X28,     224
.equ IRQ_FRAME_X29,     232
.equ IRQ_FRAME_X30,     240
.equ IRQ_FRAME_SP_EL0,  248
.equ IRQ_FRAME_ELR,     256
.equ IRQ_FRAME_SPSR,    264

// INTERRUPT_FULL_SAVE
// Entry: In EL1h mode (exception taken), SP = SP_EL1, SP_EL0 = interrupted stack
// Exit:  SP = exception stack with 272-byte frame, x0 = interrupt ID
// Clobbers: None (all original values preserved in frame)
.macro INTERRUPT_FULL_SAVE
    // Allocate full 272-byte frame FIRST (avoids push/overlap issues)
    sub sp, sp, #IRQ_FRAME_SIZE

    // Step 1: Save x0, x1 IMMEDIATELY before clobbering
    stp x0, x1, [sp, #IRQ_FRAME_X0]

    // Step 2: Read GIC IAR to acknowledge interrupt (MUST be done quickly)
    // Now safe to use x0, x1
    movz x0, #0x0801, lsl #16       // GICC_IAR at 0x0801000C
    movk x0, #0x000C
    ldr w0, [x0]                    // w0 = IAR value
    and w0, w0, #0x3FF              // w0 = interrupt ID (keep in x0)

    // Step 3: Save x2, x3 so we can use them
    stp x2, x3, [sp, #IRQ_FRAME_X2]

    // Step 4: Read SP_EL0, ELR_EL1, SPSR_EL1
    mrs x1, SP_EL0                  // x1 = interrupted stack
    mrs x2, ELR_EL1                 // x2 = return address
    mrs x3, SPSR_EL1                // x3 = saved processor state

    // Step 5: Store SP_EL0, ELR, SPSR
    str x1, [sp, #IRQ_FRAME_SP_EL0]
    str x2, [sp, #IRQ_FRAME_ELR]
    str x3, [sp, #IRQ_FRAME_SPSR]

    // Step 6: Save remaining registers x4-x30 (these haven't been modified)
    stp x4, x5, [sp, #IRQ_FRAME_X4]
    stp x6, x7, [sp, #IRQ_FRAME_X6]
    stp x8, x9, [sp, #IRQ_FRAME_X8]
    stp x10, x11, [sp, #IRQ_FRAME_X10]
    stp x12, x13, [sp, #IRQ_FRAME_X12]
    stp x14, x15, [sp, #IRQ_FRAME_X14]
    stp x16, x17, [sp, #IRQ_FRAME_X16]
    stp x18, x19, [sp, #IRQ_FRAME_X18]
    stp x20, x21, [sp, #IRQ_FRAME_X20]
    stp x22, x23, [sp, #IRQ_FRAME_X22]
    stp x24, x25, [sp, #IRQ_FRAME_X24]
    stp x26, x27, [sp, #IRQ_FRAME_X26]
    stp x28, x29, [sp, #IRQ_FRAME_X28]
    str x30, [sp, #IRQ_FRAME_X30]

    // x0 = interrupt ID, all registers saved in frame
.endm

// INTERRUPT_FULL_RESTORE
// Entry: SP = exception stack with frame
// Exit:  Returns via eret with all registers restored
// Note:  This macro does NOT do EOI - caller must handle EOI if needed
//        (Go handlers typically do EOI themselves via gicEndOfInterrupt)
.macro INTERRUPT_FULL_RESTORE
    // Step 1: Restore ELR_EL1 and SPSR_EL1
    // These tell ERET where to return and what mode to return to
    ldr x0, [sp, #IRQ_FRAME_ELR]
    ldr x1, [sp, #IRQ_FRAME_SPSR]
    msr ELR_EL1, x0
    msr SPSR_EL1, x1
    isb

    // Step 2: Restore SP_EL0 (interrupted code's stack pointer)
    // ERET does NOT automatically restore this - we must do it manually
    ldr x0, [sp, #IRQ_FRAME_SP_EL0]
    msr SP_EL0, x0
    isb

    // Step 3: Restore all GPRs (x0-x30)
    // Restore x2-x30 first (we use x0, x1 as temporaries above)
    ldp x2, x3, [sp, #IRQ_FRAME_X2]
    ldp x4, x5, [sp, #IRQ_FRAME_X4]
    ldp x6, x7, [sp, #IRQ_FRAME_X6]
    ldp x8, x9, [sp, #IRQ_FRAME_X8]
    ldp x10, x11, [sp, #IRQ_FRAME_X10]
    ldp x12, x13, [sp, #IRQ_FRAME_X12]
    ldp x14, x15, [sp, #IRQ_FRAME_X14]
    ldp x16, x17, [sp, #IRQ_FRAME_X16]
    ldp x18, x19, [sp, #IRQ_FRAME_X18]
    ldp x20, x21, [sp, #IRQ_FRAME_X20]
    ldp x22, x23, [sp, #IRQ_FRAME_X22]
    ldp x24, x25, [sp, #IRQ_FRAME_X24]
    ldp x26, x27, [sp, #IRQ_FRAME_X26]
    ldp x28, x29, [sp, #IRQ_FRAME_X28]
    ldr x30, [sp, #IRQ_FRAME_X30]

    // Step 4: Restore x0, x1 LAST (after we're done using them as temps)
    ldp x0, x1, [sp, #IRQ_FRAME_X0]

    // Step 5: Deallocate frame
    add sp, sp, #IRQ_FRAME_SIZE

    // Step 6: Return from exception
    // ERET restores: PC ← ELR_EL1, PSTATE ← SPSR_EL1
    // SPSR contains SPSel bit which determines SP_EL0 vs SP_EL1 usage
    eret
.endm

// ============================================================================
// DEBUG MACROS - SAFE REGISTER PRESERVATION
// ============================================================================
//
// DEBUG_SAVE_REGS: Save all scratch registers (x0-x15) to stack
// Use this at the start of debug code to protect register state
.macro DEBUG_SAVE_REGS
    stp x0, x1, [sp, #-128]!
    stp x2, x3, [sp, #16]
    stp x4, x5, [sp, #32]
    stp x6, x7, [sp, #48]
    stp x8, x9, [sp, #64]
    stp x10, x11, [sp, #80]
    stp x12, x13, [sp, #96]
    stp x14, x15, [sp, #112]
.endm

// DEBUG_RESTORE_REGS: Restore all scratch registers from stack
// Use this at the end of debug code to restore register state
.macro DEBUG_RESTORE_REGS
    ldp x14, x15, [sp, #112]
    ldp x12, x13, [sp, #96]
    ldp x10, x11, [sp, #80]
    ldp x8, x9, [sp, #64]
    ldp x6, x7, [sp, #48]
    ldp x4, x5, [sp, #32]
    ldp x2, x3, [sp, #16]
    ldp x0, x1, [sp], #128
.endm

// ============================================================================
// SAFE DEBUG OUTPUT MACROS
// ============================================================================
// These macros provide safe debug output that preserves ALL registers,
// especially X0 which is critical for syscall return values.
//
// IMPORTANT: These macros save/restore X0-X15 around ALL debug operations.
// Use these instead of direct UART writes or function calls.

// DEBUG_PUTC_SAFE: Print a single character, preserving ALL registers
// Parameter: character (immediate or register)
// Example: DEBUG_PUTC_SAFE 'A'
//          DEBUG_PUTC_SAFE w10
.macro DEBUG_PUTC_SAFE char
    DEBUG_SAVE_REGS
    .ifnc \char,w0
        mov w0, \char                // Move character to w0 if not already there
    .endif
    movz x1, #0x0900, lsl #16        // x1 = UART base
    strb w0, [x1]                    // Write character
    DEBUG_RESTORE_REGS
.endm

// DEBUG_PRINT_HEX64_SAFE: Print X0 as 64-bit hex, preserving ALL registers
// Expects value to print in X0 on entry
.macro DEBUG_PRINT_HEX64_SAFE
    DEBUG_SAVE_REGS
    // Value to print is now in saved X0 on stack at [sp, #0]
    ldr x0, [sp, #0]                 // Reload value to print
    bl print_hex64                   // Call print function
    DEBUG_RESTORE_REGS
.endm

// DEBUG_PRINT_HEX32_SAFE: Print X0 as 32-bit hex, preserving ALL registers
// Expects value to print in W0 on entry
.macro DEBUG_PRINT_HEX32_SAFE
    DEBUG_SAVE_REGS
    ldr x0, [sp, #0]                 // Reload value to print
    bl print_hex32                   // Call print function (need to add this)
    DEBUG_RESTORE_REGS
.endm

// ============================================================================
// SAFE GO FUNCTION CALL MACROS
// ============================================================================
// These macros safely call Go UART functions while preserving ALL registers,
// especially X0 which may contain critical return values.
//
// CRITICAL: Go functions use X0 for parameters AND return values, so any call
// to a Go function will clobber X0. These macros save/restore X0 around calls.

// DEBUG_CALL_GO_PUTC: Call Go uartPutcDirect(c) safely
// Parameter: character to print (immediate or register)
// Example: DEBUG_CALL_GO_PUTC #'A'
.macro DEBUG_CALL_GO_PUTC char
    DEBUG_SAVE_REGS                  // Save all registers including X0
    .ifnc \char,w0
        mov w0, \char                // Move character parameter to w0
    .else
        ldr x0, [sp, #0]             // Reload saved x0 as parameter
    .endif
    bl main.uartPutcDirect           // Call Go function
    DEBUG_RESTORE_REGS               // Restore all registers including X0
.endm

// DEBUG_CALL_GO_PUTS: Call Go uartPutsDirect(s) safely
// Parameter: register containing string pointer (must be x0 on entry)
// Example:
//   ldr x0, =my_string
//   DEBUG_CALL_GO_PUTS
.macro DEBUG_CALL_GO_PUTS
    DEBUG_SAVE_REGS                  // Save all registers
    ldr x0, [sp, #0]                 // Reload string pointer from saved x0
    bl main.uartPutsDirect           // Call Go function
    DEBUG_RESTORE_REGS               // Restore all registers including X0
.endm

// DEBUG_CALL_GO_PUTHEX64: Call Go uartPutHex64Direct(v) safely
// Parameter: value to print (must be in x0 on entry)
// Example:
//   mov x0, x8
//   DEBUG_CALL_GO_PUTHEX64
.macro DEBUG_CALL_GO_PUTHEX64
    DEBUG_SAVE_REGS                  // Save all registers
    ldr x0, [sp, #0]                 // Reload value from saved x0
    bl main.uartPutHex64Direct       // Call Go function
    DEBUG_RESTORE_REGS               // Restore all registers including X0
.endm

// DEBUG_CALL_GO_PUTHEX32: Call Go uartPutHex32Direct(v) safely
// Parameter: value to print (must be in w0 on entry)
.macro DEBUG_CALL_GO_PUTHEX32
    DEBUG_SAVE_REGS                  // Save all registers
    ldr x0, [sp, #0]                 // Reload value from saved x0
    bl main.uartPutHex32Direct       // Call Go function
    DEBUG_RESTORE_REGS               // Restore all registers including X0
.endm

// ============================================================================
// SAFE DEBUG PRINTING FUNCTIONS
// ============================================================================
//
// These functions print to UART while preserving ALL registers.
// CRITICAL: All functions save/restore x0-x30 to ensure caller state unchanged.

// uart_putc: Print a single character to UART
// Input: w0 = character to print (only low byte used)
// Preserves: ALL registers (x0-x30)
.macro UART_PUTC
    stp x0, x1, [sp, #-16]!          // Save x0, x1
    movz x1, #0x0900, lsl #16        // x1 = 0x09000000 (UART base)
    strb w0, [x1]                    // Write character
    ldp x0, x1, [sp], #16            // Restore x0, x1
.endm

// UART_PUTC_REG: Print a character from any register (uses x14, x15 as temps)
// Input: any w register containing character
// Preserves: x0-x13, x16-x30 (clobbers x14, x15)
.macro UART_PUTC_REG reg
    stp x14, x15, [sp, #-16]!
    movz x15, #0x0900, lsl #16       // x15 = UART base
    strb \reg, [x15]                 // Write character
    ldp x14, x15, [sp], #16
.endm

// print_hex_nibble: Print a single hex nibble (0-F)
// Input: w0 = nibble value (0-15)
// Preserves: ALL registers
.macro PRINT_HEX_NIBBLE
    stp x0, x1, [sp, #-16]!
    and w0, w0, #0xF                 // Mask to nibble
    cmp w0, #10
    blt 1f
    add w0, w0, #0x37                // 'A'-10
    b 2f
1:  add w0, w0, #0x30                // '0'
2:  movz x1, #0x0900, lsl #16
    strb w0, [x1]
    ldp x0, x1, [sp], #16
.endm

// print_hex64 and print_string have been migrated to asm/goasm/exc_utils.s
// These functions are now provided by exc_utils.o

// ============================================================================
// GO CALLING CONVENTION SUPPORT
// ============================================================================
//
// Go's calling convention requires the caller to provide stack space for
// argument spills. Arguments are passed in registers x0-x5, but Go immediately
// spills them to the stack at POSITIVE offsets from SP.
//
// For a Go function with frame size F that accesses [SP + M]:
//   - After allocating frame: Go_SP = Caller_SP - F
//   - Go accesses: [Go_SP + M] = [Caller_SP - F + M] = [Caller_SP + (M - F)]
//   - If M > F, this is ABOVE the caller's original SP
//
// SPILL_SPACE = (max_offset - frame_size + param_size + 15) & ~15
//
// Spill space constants (derived from disassembly analysis):
.equ SPILL_SPACE_1PARAM,  16    // 1 parameter functions
.equ SPILL_SPACE_2PARAM,  32    // 2 parameter functions
.equ SPILL_SPACE_3PARAM,  32    // 3 parameter functions
.equ SPILL_SPACE_4PARAM,  48    // 4 parameter functions
.equ SPILL_SPACE_6PARAM,  320   // 6 parameter functions (Go uses sp-128+144=272, round up)
.equ SPILL_SPACE_8PARAM,  64    // 8 parameter functions

// Register save space (7 registers: x19-x22, x28-x30)
.equ REG_SAVE_SPACE,      64    // 64 bytes (4 pairs + alignment)

// CALL_GO_PROLOGUE: Prepare to call a Go function from assembly
//   Arguments: \spill_space - bytes of spill space needed (use SPILL_SPACE_*PARAM)
//   Clobbers: none (saves all callee-saved registers)
//   Stack effect: Allocates REG_SAVE_SPACE + spill_space bytes
//
//   Stack layout after prologue:
//     [original_SP]                           <- entry SP
//     [original_SP - REG_SAVE_SPACE]          <- saved regs (x19-x22, x28-x30)
//     [original_SP - REG_SAVE_SPACE - spill] <- current SP (Go's entry point)
//
.macro CALL_GO_PROLOGUE spill_space
    // Go ABIInternal expects caller to provide spill space ABOVE sp
    // Allocate total space: REG_SAVE + SPILL_SPACE
    sub sp, sp, #(REG_SAVE_SPACE + \spill_space)

    // Save callee-saved registers at BOTTOM of allocated space
    //   x19-x28: callee-saved general purpose registers
    //   x29: frame pointer
    //   x30: link register
    stp x19, x20, [sp, #0]
    stp x21, x22, [sp, #16]
    stp x28, x29, [sp, #32]
    str x30, [sp, #48]

    // Spill space is at [sp, #64] to [sp, #64+spill_space]
    // Go function can use [sp, #N] where N >= 64
.endm

// CALL_GO_EPILOGUE: Clean up after calling a Go function
//   Arguments: \spill_space - bytes of spill space (must match prologue)
//   Effects: Restores registers and stack, preserves x0 (return value)
//   Returns: x0 contains the Go function's return value
//
.macro CALL_GO_EPILOGUE spill_space
    // Go function has returned with result in x0
    // Saved registers are at [sp, #0] through [sp, #48]
    // Spill space is at [sp, #64] onwards

    // Restore callee-saved registers from bottom of stack frame
    ldp x19, x20, [sp, #0]
    ldp x21, x22, [sp, #16]
    ldp x28, x29, [sp, #32]
    ldr x30, [sp, #48]

    // Deallocate entire frame (REG_SAVE + SPILL_SPACE)
    add sp, sp, #(REG_SAVE_SPACE + \spill_space)

    // x0 already contains return value from Go function
.endm

// ============================================================================
// IRQ Exception Handler (placed in .text section)
// ============================================================================
// Called from vector table entry vec_irq_sp_el1 (via branch)
// This is the main IRQ handler with timer preemption support.
.global irq_exception_el1
irq_exception_el1:
    // ========================================================================
    // IRQ HANDLER WITH TIMER PREEMPTION
    // ========================================================================
    //
    // This handler:
    // 1. Saves ALL registers (preserves interrupted code's state completely)
    // 2. Checks if this is a timer interrupt (IRQ 27)
    //    - If timer: Do "call injection" to redirect to asyncPreemptBM
    //    - If other: Call irqHandlerGo(irqID) for Go dispatch
    // 3. Restores ALL registers and returns via ERET
    //
    // Timer preemption uses "call injection" (like signal-based preemption):
    // We modify the exception frame so ERET jumps to asyncPreemptBM instead
    // of the interrupted code. asyncPreemptBM saves all state, calls
    // runtime.Gosched(), then returns to the interrupted PC.
    // ========================================================================

    // DEBUG: Print '!' immediately on IRQ entry (before any state is saved)
    // This helps verify if ANY IRQ is firing at all
    stp x0, x1, [sp, #-16]!          // Save x0, x1 temporarily
    movz x1, #0x0900, lsl #16        // UART base
    mov w0, #'!'
    strb w0, [x1]
    ldp x0, x1, [sp], #16            // Restore x0, x1

    INTERRUPT_FULL_SAVE              // Save all regs, x0 = interrupt ID

    // Check if this is a timer interrupt (IRQ 27 = virtual timer)
    cmp w0, #27
    beq timer_preempt_handler

    // Not a timer interrupt - use Go dispatch
    //
    // CRITICAL: Go ABI requires caller to reserve spill space for callee's
    // register arguments. irqHandlerGo takes 1 arg (irqID in R0), so we need
    // at least 8 bytes of spill space. We reserve 16 for alignment.
    //
    // Without this, irqHandlerGo would spill R0 to [SP+0], corrupting the
    // saved x0 in our exception frame.
    //
    sub sp, sp, #16                  // Reserve 16 bytes spill space
    mov x29, sp
    bl main.irqHandlerGo
    add sp, sp, #16                  // Deallocate spill space
    INTERRUPT_FULL_RESTORE           // Restores all regs + SP_EL0, does ERET

timer_preempt_handler:
    // ========================================================================
    // TIMER PREEMPTION VIA CALL INJECTION
    // ========================================================================
    // We need to:
    // 1. Increment tick counter and check sleeping threads (ALWAYS)
    // 2. Check if we're on g0 (system goroutine) - if so, skip preemption
    // 3. Re-arm the timer
    // 4. Do call injection: modify ELR to jump to asyncPreemptBM
    // 5. Signal EOI to GIC
    // 6. ERET will jump to asyncPreemptBM which calls scheduler
    // ========================================================================

    // 0. DEBUG: DISABLED

    // Handle timer tick (increment counter, wake sleeping threads)
    mov x29, sp
    bl main.TimerTickHandler

    // 1. CHECK IF WE'RE ON g0 - if so, skip preemption
    // Cannot call Gosched() from g0's stack - it's the system goroutine
    // Check: if g == g.m.g0, we're on g0
    // Go struct offsets:
    //   g.m   is at offset 48 in g struct
    //   m.g0  is at offset 0 in m struct
    // NOTE: Go on arm64 without cgo uses x28 directly as the g pointer.
    // With cgo, TPIDR_EL0 + offset 16 contains g.
    // Kmazarin is compiled without cgo (runtime.iscgo = 0), so x28 = g.
    // We saved x28 in the exception frame, so read it from there.
    ldr x3, [sp, #IRQ_FRAME_X28]    // x3 = interrupted x28 = g
    cbz x3, timer_skip_preempt      // If g is nil, skip preemption

    // Check if g is in kmazarin's virtual address ranges:
    // Go runtime uses HIGH virtual addresses: 0x4000000000 (256GB base) for arenas
    // Kmazarin ELF is at LOW addresses (see kmazarin_symbols.s for exact range)
    //
    // Range 1: HIGH VA (Go arenas) = 0x4000000000 - 0x8000000000
    // Range 2: LOW VA (kmazarin ELF) = KMAZARIN_TEXT_START - KMAZARIN_TEXT_END
    // Anything else = mazboot's address space

    // Check HIGH VA range first (Go's arena/heap addresses)
    // 0x4000000000 = 64 << 32 = 0x40 in bits 39-32
    movz x6, #0x40, lsl #32         // 0x4000000000 (256GB)
    cmp x3, x6
    blt check_kmazarin_elf_range    // Less than 256GB, check ELF range
    movz x6, #0x80, lsl #32         // 0x8000000000 (512GB upper bound)
    cmp x3, x6
    blt found_kmazarin_g            // In Go's high VA range!

check_kmazarin_elf_range:
    // Check LOW VA range (kmazarin ELF binary text section)
    movz x6, #(KMAZARIN_TEXT_START >> 16), lsl #16
    movk x6, #(KMAZARIN_TEXT_START & 0xFFFF)
    cmp x3, x6
    blt timer_skip_mazboot          // g pointer < kmazarin start, it's mazboot's
    movz x6, #(KMAZARIN_TEXT_END >> 16), lsl #16
    movk x6, #(KMAZARIN_TEXT_END & 0xFFFF)
    cmp x3, x6
    bge timer_skip_mazboot          // g pointer >= kmazarin end, it's mazboot's

found_kmazarin_g:
    // g is likely a kmazarin pointer!
    // Save g for comparison
    mov x6, x3                      // x6 = g

    ldr x4, [x3, #48]               // x4 = g.m (m pointer)
    cbz x4, timer_skip_preempt      // If m is nil, skip preemption
    ldr x5, [x4, #0]                // x5 = m.g0 (the m's system goroutine)
    cmp x6, x5                      // Compare current g with m.g0
    bne not_g0_preempt              // If g != m.g0, we can preempt!

    // g == m.g0, we're on scheduler. Print first digit of g address for debug
    // Print 'S' + hex digit of high nibble to show we're seeing different g0s
    // Actually, for first pass, just print 'S' once every ~100 times
    // Skip printing - too noisy
    b timer_skip_preempt

not_g0_preempt:
    // g != m.g0! We can preempt this goroutine

    // DEBUG: Print 'P' to show we're taking preemption path
    mov w0, #'P'
    UART_PUTC

    // 1. RE-ARM TIMER - Set timer to fire again in 20ms
    // (1,250,000 ticks @ 62.5 MHz)
    mrs x3, CNTVCT_EL0              // Read current counter
    movz x4, #0x0013, lsl #16       // 1,250,000 = 0x1312D0
    movk x4, #0x12D0
    add x3, x3, x4
    msr CNTV_CVAL_EL0, x3           // Set compare value

    // 2. CALL INJECTION
    // Read interrupted PC and SP from our exception frame
    ldr x19, [sp, #IRQ_FRAME_ELR]    // x19 = interrupted PC
    ldr x20, [sp, #IRQ_FRAME_SP_EL0] // x20 = interrupted SP (goroutine stack)

    // Allocate 16-byte frame on interrupted goroutine's stack
    sub x20, x20, #16               // SP -= 16
    and x20, x20, #0xFFFFFFFFFFFFFFF0  // Ensure 16-byte alignment

    // Save interrupted PC and FP to that frame (for proper ARM64 stack unwinding)
    // This creates a valid stack frame where [FP-8] = return address, [FP] = previous FP
    str x19, [x20, #0]              // *SP = interrupted PC (return address for unwinding)
    ldr x22, [sp, #IRQ_FRAME_X29]   // Get FP (x29) from frame
    str x22, [x20, #8]              // *(SP+8) = old FP

    // Set LR (x30) = interrupted PC (asyncPreempt will return here)
    str x19, [sp, #IRQ_FRAME_X30]   // Update LR in frame

    // Set SP_EL0 in frame to new adjusted value
    str x20, [sp, #IRQ_FRAME_SP_EL0]

    // Determine which asyncPreempt to call:
    // - If interrupted PC is in kmazarin text section, use kmazarin's runtime.asyncPreempt
    // - Otherwise use mazboot's asyncPreemptBM
    // Uses auto-generated constants from kmazarin.elf (see kmazarin_symbols.s)
    movz x21, #(KMAZARIN_TEXT_START >> 16), lsl #16
    movk x21, #(KMAZARIN_TEXT_START & 0xFFFF)
    cmp x19, x21                    // Compare interrupted PC with kmazarin .text start
    blt use_mazboot_preempt         // If PC < text start, use mazboot
    movz x21, #(KMAZARIN_TEXT_END >> 16), lsl #16
    movk x21, #(KMAZARIN_TEXT_END & 0xFFFF)
    cmp x19, x21                    // Compare interrupted PC with kmazarin .text end
    bge use_mazboot_preempt         // If PC >= text end, use mazboot

    // Use kmazarin's runtime.asyncPreempt.abi0 (address auto-extracted from kmazarin.elf)
    LOAD_KMAZARIN_ASYNCPREEMPT x21

    // DEBUG: Print 'K' to show using kmazarin asyncPreempt
    mov w0, #'K'
    UART_PUTC

    b set_elr_and_continue

use_mazboot_preempt:
    // Use mazboot's asyncPreemptBM
    adrp x21, asyncPreemptBM
    add x21, x21, :lo12:asyncPreemptBM

    // DEBUG: Print 'M' to show using mazboot asyncPreempt
    mov w0, #'M'
    UART_PUTC

set_elr_and_continue:
    str x21, [sp, #IRQ_FRAME_ELR]   // ELR will be restored by INTERRUPT_FULL_RESTORE

    // NOTE: x28 (g pointer) should already be correct in the saved frame
    // since kmazarin uses x28 directly (no cgo TLS).
    // Go's asyncPreempt2 uses x28 to access g struct.

    // 3. SIGNAL EOI TO GIC (before ERET!)
    // GICC_EOIR at 0x08010010
    movz x1, #0x0801, lsl #16
    movk x1, #0x0010
    mov w2, #27                     // Timer IRQ ID
    str w2, [x1]                    // Write to EOIR

    // 4. RESTORE AND ERET
    // INTERRUPT_FULL_RESTORE will restore all regs from frame and ERET
    // ELR now points to asyncPreempt, SP_EL0 to adjusted goroutine stack
    // x30 (LR) contains interrupted PC, x28 has correct g pointer
    INTERRUPT_FULL_RESTORE

    // NEVER REACHED - INTERRUPT_FULL_RESTORE includes eret

timer_skip_mazboot:
    // Mazboot's g0 - skip preemption
    b timer_common_skip

timer_skip_preempt:
    // Kmazarin's g0 (scheduler) - skip preemption

timer_common_skip:
    // Re-arm timer - Set timer to fire again in 20ms
    mrs x3, CNTVCT_EL0
    movz x4, #0x0013, lsl #16
    movk x4, #0x12D0
    add x3, x3, x4
    msr CNTV_CVAL_EL0, x3

    // Send EOI to GIC
    movz x1, #0x0801, lsl #16
    movk x1, #0x0010
    mov w2, #27
    str w2, [x1]

    // Return normally without preemption
    INTERRUPT_FULL_RESTORE


// ============================================================================
// Exception Handler Functions
// ============================================================================
// Go functions called from assembly (e.g., UartTransmitHandler) are defined
// in their respective Go files and exported via //go:linkname. The assembly
// code calls these Go functions directly using 'bl main.FunctionName'.
// No stubs needed - Go compiler will handle the linkage.


// ============================================================================
// NOTE: The following functions have been migrated to Go/Plan9 assembly in lib_sysregs.s:
//   enable_irqs, enable_irqs_asm, disable_irqs,
//   read_spsr_el1, write_spsr_el1,
//   read_elr_el1, write_elr_el1, read_esr_el1, read_far_el1, read_daif,
//   set_vbar_el1, read_vbar_el1, get_exception_vectors_addr
// ============================================================================


// ============================================================================
// Synchronous Exception Handler (placed outside vector table)
// ============================================================================
// This handler is called from the vector table entry at 0x200
// It handles SVC syscalls by faking responses, and forwards other exceptions
// to the Go exception handler.

.global sync_exception_handler
// NOTE: syscall_return and load_context_and_eret are now in Go/Plan9 assembly (exc_syscall.s)
sync_exception_handler:
    // CRITICAL FOR DEMAND PAGING: Save ALL registers IMMEDIATELY before ANY operations
    // This ensures we can restore exact state for retry after handling page faults.
    //
    // Approach: First save x29, x30 to current stack, then use them to set up exception stack.

    // Step 1: Save x29, x30 to current stack (we'll recover them later)
    stp x29, x30, [sp, #-16]!       // Push x29, x30, decrement SP by 16

    // CRITICAL: Save SP_EL0 IMMEDIATELY to exception stack frame
    // When exception occurs in EL1t mode (using SP_EL0), the CPU switches to EL1h
    // mode (using SP_EL1) for the handler. SP_EL0 is NOT automatically saved!
    // If we don't save/restore SP_EL0, it will have the wrong value after eret,
    // causing stack corruption and NOFRAME functions to write to wrong addresses.
    // NOTE: We can't use the macro yet because we haven't switched to exception stack
    mrs x29, SP_EL0                 // x29 = current SP_EL0 (kmazarin's stack)
    // x29, x30 will be saved shortly when we switch to exception stack

    // Step 2: Save original SP (before we pushed) to x30
    add x30, sp, #16               // x30 = original SP (current SP + 16 for the push)

    // Step 3: Switch to exception stack
    // CRITICAL FIX: Check if we're already on exception stack (nested exception)
    // Exception stack is at 0x5F000000-0x5F010000 (64KB)
    // If SP is in this range, we're in a nested exception
    movz x29, #0x5F00, lsl #16     // x29 = 0x5F000000 (lower bound)
    cmp x30, x29                    // Compare original SP with lower bound
    b.lo use_primary_stack          // If below, use primary exception stack
    movz x29, #0x5F01, lsl #16     // x29 = 0x5F010000 (upper bound)
    cmp x30, x29                    // Compare original SP with upper bound
    b.hs use_primary_stack          // If above or equal, use primary stack

    // We're in nested exception - use alternate stack within exception stack
    // Use bottom half of exception stack for nested exceptions
    movz x29, #0x5F00, lsl #16     // Nested exception stack at 0x5F008000
    movk x29, #0x8000, lsl #0      // (middle of 64KB exception stack)
    b stack_selected

use_primary_stack:
    movz x29, #0x5F01, lsl #16     // Primary exception stack at 0x5F010000 (top)
    movk x29, #0x0000, lsl #0

stack_selected:
    mov sp, x29

    // Allocate stack frame (320 bytes for all registers + exception state + alignment)
    sub sp, sp, #320

    // Step 4: Save original SP (in x30) and original x29, x30 location
    str x30, [sp, #248]             // Save original SP

    // Save ALL registers x0-x28
    stp x0, x1, [sp, #0]
    stp x2, x3, [sp, #16]
    stp x4, x5, [sp, #32]
    stp x6, x7, [sp, #48]
    stp x8, x9, [sp, #64]
    stp x10, x11, [sp, #80]
    stp x12, x13, [sp, #96]
    stp x14, x15, [sp, #112]
    stp x16, x17, [sp, #128]
    stp x18, x19, [sp, #144]
    stp x20, x21, [sp, #160]
    stp x22, x23, [sp, #176]
    stp x24, x25, [sp, #192]
    stp x26, x27, [sp, #208]
    str x28, [sp, #224]

    // Recover original x29, x30 from the kernel stack where we pushed them
    // x30 currently holds original SP, so original x29/x30 are at [x30-16]
    ldr x0, [sp, #248]              // x0 = original SP
    sub x0, x0, #16                 // x0 = address where we pushed x29, x30
    ldp x1, x2, [x0]                // x1 = original x29, x2 = original x30
    stp x1, x2, [sp, #232]          // Save original x29, x30

    // Save exception system registers
    mrs x0, ELR_EL1                 // Return address
    mrs x1, SPSR_EL1                // Saved PSTATE
    mrs x2, FAR_EL1                 // Fault address
    mrs x3, ESR_EL1                 // Exception syndrome
    stp x0, x1, [sp, #256]          // ELR, SPSR
    stp x2, x3, [sp, #272]          // FAR, ESR

    // Save SP_EL0 to exception frame (was saved in x29 earlier, before stack switch)
    // NOTE: x29 was read from SP_EL0 at the very beginning of the exception handler
    // and has been preserved through the stack switch
    ldr x0, [sp, #232]              // x0 = original x29 (NOT SP_EL0!)
    // Actually, we need to re-read SP_EL0 now that we're on exception stack
    SAVE_SP_EL0_TO_STACK            // Save SP_EL0 to frame offset 288

    // ========================================================================
    // UNIFIED EXCEPTION DISPATCH
    // ========================================================================
    // All synchronous exceptions now go through sync_exception_entry (Go/Plan9 asm)
    // which dispatches based on EC to the appropriate handler:
    //   - EC=0x15 (SVC): syscall handling
    //   - EC=0x25 (data abort): page fault / demand paging
    //   - Other: print error and exit
    //
    // Entry state for sync_exception_entry:
    //   - SP: Points to 320-byte exception frame
    //   - Exception frame contains all saved registers + system state
    //
    b sync_exception_entry

    // All synchronous exceptions now go through sync_exception_entry (Go/Plan9 asm)
    // which handles all exception types and performs ERET.
    // The legacy handlers (sync_unknown_exception, sync_other_exception,
    // sync_restore_and_svc) have been removed as they were unreachable dead code.

// ============================================================================
// EL1t MODE (SP_EL0) EXCEPTION HANDLERS
// ============================================================================
// NOTE: These handlers have been migrated to src/mazboot/asm/goasm/exc_handlers.s
// They are now in Go/Plan9 assembly format:
//   - sync_exception_handler_el0
//   - irq_exception_handler_el0

// print_decimal_uart and print_hex_byte_uart have been migrated to asm/goasm/exc_utils.s

