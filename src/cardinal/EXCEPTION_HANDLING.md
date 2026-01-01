# ARM64 Exception Handling in Mazboot

## Overview

This document captures critical lessons learned about ARM64 exception handling, specifically for implementing syscall support in a bare-metal environment where Go code runs at EL1.

## Architecture Setup

### Exception Levels and Stack Registers

Mazboot runs in **EL1t mode** (Exception Level 1, Thread mode):
- **Privilege Level**: EL1 (full kernel privileges)
- **Stack Register**: SP_EL0 (SPSel=0)
- **Exception Stack**: SP_EL1 (used when exceptions occur)

**CRITICAL DISTINCTION**: SP_EL0 is just a register name - it does NOT mean we're running at EL0 (user mode)! We're running at EL1 privilege, just using the SP_EL0 register as our stack pointer.

### Exception Flow

When a synchronous exception (SVC syscall) occurs in EL1t mode:

1. **Before Exception** (EL1t mode):
   - Executing at EL1 privilege
   - Using SP_EL0 as stack pointer (e.g., SP_EL0 = 0xC8001000)
   - CPU uses SP_EL1 for exception vectors (set in boot.s to 0x5F010000)

2. **Exception Occurs** (CPU automatically):
   - Saves current PSTATE to SPSR_EL1
   - Saves return address to ELR_EL1 (points to the SVC instruction!)
   - Switches from EL1t → EL1h mode
   - Now uses SP_EL1 as stack pointer (0x5F010000)
   - Jumps to exception vector

3. **Exception Handler Runs** (EL1h mode):
   - Executes on SP_EL1 (exception stack)
   - SP_EL0 remains unchanged (still 0xC8001000)
   - Handles the exception

4. **Return with ERET** (CPU automatically):
   - Restores PSTATE from SPSR_EL1
   - Jumps to address in ELR_EL1
   - Switches from EL1h → EL1t mode
   - Now uses SP_EL0 as stack pointer again

## Critical Bug #1: ELR_EL1 Must Be Advanced

### The Problem

When an SVC exception occurs, **ELR_EL1 points to the SVC instruction itself**, not the next instruction. If the exception handler returns without advancing ELR_EL1, the CPU will execute the same SVC instruction again → **infinite loop**!

### The Solution

Before returning from a syscall handler, **advance ELR_EL1 by 4 bytes**:

```assembly
syscall_return:
    // Load saved ELR_EL1
    ldr x12, [scratch_area, #ELR_offset]

    // CRITICAL: Advance past SVC instruction
    add x12, x12, #4                // ELR += 4 (ARM64 instructions are 4 bytes)

    // Restore to ELR_EL1
    msr ELR_EL1, x12
    msr SPSR_EL1, x13
    isb

    // Return
    eret
```

**Status**: ✅ **FIXED** (2025-12-25) - This was causing kmazarin to hang after the first mmap syscall.

## Critical Bug #2: SP_EL0 Corruption and NOFRAME Return Values

### The Problem

When running in **EL1t mode** (using SP_EL0 as the stack pointer), the ARM64 CPU behavior during exceptions is:

1. **Exception occurs** (e.g., SVC syscall):
   - CPU automatically switches from EL1t → EL1h mode
   - Now uses SP_EL1 (exception stack) instead of SP_EL0
   - **SP_EL0 is NOT automatically saved by the CPU!**

2. **Exception handler runs** on SP_EL1

3. **ERET returns**:
   - CPU switches from EL1h → EL1t mode
   - Now uses SP_EL0 again
   - **If SP_EL0 was modified during the exception, the stack pointer is corrupted!**

This causes:
- Stack pointer pointing to wrong location after `eret`
- Function epilogues reading from wrong addresses
- **NOFRAME functions writing return values to wrong memory!**

### NOFRAME Functions and Return Values

Go's `runtime.sysMmap` (and similar syscall wrappers) are **NOFRAME** functions - they don't set up their own stack frame. Instead, they use the **caller's FP** to access the stack:

```assembly
TEXT runtime·sysMmap(SB),NOSPLIT|NOFRAME,$0
    // Read parameters from caller's frame
    MOVD	addr+0(FP), R0
    MOVD	n+8(FP), R1
    ...

    // Make syscall
    SVC

    // Write return values to caller's frame
    MOVD	R0, p+32(FP)      # Write pointer to [FP+32]
    MOVD	$0, err+40(FP)    # Write error to [FP+40]
    RET
```

**Critical Issue**: If FP points to the wrong location (because SP_EL0 was corrupted), the return value writes go to the wrong memory! The caller then reads garbage from the correct `[FP+32]/[FP+40]` locations.

### Current Status

**✅ IMPLEMENTED** - SP_EL0 save/restore added to exception handler
**⏳ DEBUGGING** - Still seeing "runtime: cannot allocate memory" errors, investigating if return values are being corrupted

### The Solution (Implemented)

Save and restore SP_EL0 explicitly:

```assembly
sync_exception_handler:
    // CRITICAL: Save SP_EL0 IMMEDIATELY
    mrs x29, SP_EL0                   // Read current SP_EL0
    movz x30, #0x40FF, lsl #16        // Fixed memory location
    movk x30, #0xF000, lsl #0         // 0x40FFF000
    str x29, [x30]                    // Save SP_EL0

    // ... rest of exception handler ...

syscall_return:
    // ... handle syscall ...

    // CRITICAL: Restore SP_EL0 before eret
    movz x11, #0x40FF, lsl #16
    movk x11, #0xF000, lsl #0
    ldr x11, [x11]                    // Load saved SP_EL0
    msr SP_EL0, x11                   // Restore SP_EL0
    isb

    // Return
    eret
```

**Status**: ⏳ **Pending** - Need to confirm if this is the actual issue.

## Register Save/Restore Requirements

### Registers That MUST Be Preserved Across Syscalls

According to ARM64 AAPCS64 and Go calling convention:

1. **X19-X28**: Callee-saved general purpose registers
2. **X29 (FP)**: Frame pointer - **CRITICAL** for stack unwinding
3. **X28 (g)**: Current goroutine pointer - **NEVER modify!**
4. **X30 (LR)**: Link register (caller saves this)
5. **SP**: Stack pointer (must be same before/after, but which SP?)

### Registers That Can Be Clobbered

1. **X0-X18**: Caller-saved (except X0 for return value)
2. **X30 (LR)**: Caller responsibility

### Special Considerations for SP

There are **two stack pointers** in EL1t mode:
- **SP_EL0**: Used for normal execution (kmazarin's stack)
- **SP_EL1**: Used for exception handlers

When an exception occurs:
- **SP_EL0 is NOT automatically saved** by the CPU
- **SP_EL1 is set by the CPU** when entering exception handler
- The exception handler runs on SP_EL1
- On `eret`, CPU switches back to using SP_EL0
- **If SP_EL0 was modified**, the return will use the wrong stack!

## Calling Go Functions from Assembly

When calling Go functions from exception handlers, follow these rules:

### 1. Provide Spill Space

Go's compiler immediately spills register parameters to the stack at **positive offsets** from SP. The assembly caller must allocate this space:

```assembly
// For a Go function with N parameters:
.equ SPILL_SPACE_1PARAM,  16    // 1 parameter
.equ SPILL_SPACE_2PARAM,  32    // 2 parameters
.equ SPILL_SPACE_3PARAM,  32    // 3 parameters
.equ SPILL_SPACE_4PARAM,  48    // 4 parameters
.equ SPILL_SPACE_6PARAM,  48    // 6 parameters
.equ SPILL_SPACE_8PARAM,  64    // 8 parameters

// Allocate spill space before call
sub sp, sp, #SPILL_SPACE_6PARAM

// Call Go function (parameters in x0-x5)
bl GoFunction

// Deallocate spill space after return
add sp, sp, #SPILL_SPACE_6PARAM
```

### 2. Save Callee-Saved Registers

Use the `CALL_GO_PROLOGUE` and `CALL_GO_EPILOGUE` macros to properly save/restore registers:

```assembly
CALL_GO_PROLOGUE SPILL_SPACE_6PARAM
bl main.SyscallMmap
CALL_GO_EPILOGUE SPILL_SPACE_6PARAM
```

These macros save:
- X19-X22 (callee-saved general purpose)
- X28 (g pointer)
- X29 (frame pointer)
- X30 (link register)

### 3. Set Up Frame Pointer

Before calling Go, set FP to current SP:

```assembly
add x29, sp, #0
bl GoFunction
```

### 4. Preserve X28 (g pointer)

The g register (X28) points to the current goroutine. **NEVER modify it** unless you're intentionally switching goroutines.

For syscall handlers, we switch to g0 before calling Go:

```assembly
// Save original g
str x28, [scratch_area]

// Load g0
ldr x28, =runtime.g0

// ... call Go function ...

// Restore original g
ldr x28, [scratch_area]
```

## Exception Handler Stack Management

### Stack Selection Strategy

The exception handler uses a **dedicated exception stack** to avoid corrupting the interrupted goroutine's stack:

```assembly
sync_exception_handler:
    // Save x29, x30 to current stack first
    stp x29, x30, [sp, #-16]!

    // Save original SP
    add x30, sp, #16

    // Check if already on exception stack (nested exception)
    movz x29, #0x5F00, lsl #16     // Exception stack lower bound
    cmp x30, x29
    b.lo use_primary_stack
    movz x29, #0x5F01, lsl #16     // Exception stack upper bound
    cmp x30, x29
    b.hs use_primary_stack

    // Nested exception - use alternate stack
    movz x29, #0x5F00, lsl #16
    movk x29, #0x8000, lsl #0
    b stack_selected

use_primary_stack:
    movz x29, #0x5F01, lsl #16

stack_selected:
    mov sp, x29
    sub sp, sp, #320                // Allocate exception frame
```

### Exception Frame Layout

The exception handler saves all registers to a 320-byte frame:

```
[sp + 0]   : x0-x1
[sp + 16]  : x2-x3
[sp + 32]  : x4-x5
[sp + 48]  : x6-x7
[sp + 64]  : x8-x9
[sp + 80]  : x10-x11
[sp + 96]  : x12-x13
[sp + 112] : x14-x15
[sp + 128] : x16-x17
[sp + 144] : x18-x19
[sp + 160] : x20-x21
[sp + 176] : x22-x23
[sp + 192] : x24-x25
[sp + 208] : x26-x27
[sp + 224] : x28 (g)
[sp + 232] : x29 (FP), x30 (LR)
[sp + 248] : Original SP
[sp + 256] : ELR_EL1, SPSR_EL1
[sp + 272] : FAR_EL1, ESR_EL1
```

## Scratch Area for Exception State

To avoid stack corruption, critical exception state is saved to a **fixed memory location** at 0x40FFF020:

```
0x40FFF000: SP_EL0 (proposed)          8 bytes
0x40FFF008: (unused)                   8 bytes
0x40FFF010: (unused)                   8 bytes
0x40FFF018: (unused)                   8 bytes
0x40FFF020: x29 (original)             8 bytes
0x40FFF028: x30 (original)             8 bytes
0x40FFF030: x0 (original)              8 bytes
0x40FFF038: ELR_EL1 (saved)            8 bytes
0x40FFF040: SPSR_EL1 (saved)           8 bytes
0x40FFF048: x28 (g, original)          8 bytes
```

This fixed memory location is:
- Always mapped (part of mazboot's data region)
- Won't conflict with stack or heap
- Easy to access with ADRP+ADD
- Safe from corruption by Go runtime

## Syscall Return Value Handling

### How Go Expects Syscall Results

Go's syscall wrappers (like `sysMmap.abi0`) expect:
1. **X0** contains the result (or -errno for error)
2. After `eret`, execution continues at next instruction
3. The wrapper checks X0 and handles errors

### Critical Points

1. **X0 must be preserved** across exception return
2. **All other registers** must be restored exactly
3. **FP must remain valid** so NOFRAME functions can access caller's frame
4. **Stack must be unchanged** so epilogues work correctly

## Testing and Verification

### Debugging Checklist

When adding exception handlers:

1. ✅ **ELR_EL1 advanced by 4?** (for SVC only, not for page faults)
2. ⏳ **SP_EL0 saved and restored?**
3. ✅ **All registers saved before calling Go?**
4. ✅ **Spill space allocated for Go calls?**
5. ✅ **Frame pointer set up before Go calls?**
6. ✅ **g pointer preserved or intentionally switched?**
7. ✅ **Syscall result in X0 preserved?**
8. ✅ **SPSR_EL1 and ELR_EL1 restored before eret?**

### Common Symptoms

- **Infinite SVC loop**: ELR_EL1 not advanced → ✅ FIXED
- **Hang after syscall**: SP_EL0 corrupted → ⏳ Investigating
- **Pointer corruption**: Go spilling to wrong stack → Check spill space
- **Panic in Go code**: g pointer wrong → Check X28 handling
- **Stack unwinding fails**: FP corrupted → Check X29 preservation

## References

- [ARM Architecture Reference Manual](https://developer.arm.com/documentation/ddi0487/latest)
- [ARM AAPCS64 Procedure Call Standard](https://github.com/ARM-software/abi-aa/blob/main/aapcs64/aapcs64.rst)
- [SOLUTION.md](src/mazboot/golang/stack/SOLUTION.md) - SP_EL0 corruption analysis
- [Go ARM64 calling convention study](src/mazboot/golang/stack/) - Detailed assembly analysis
- [CLAUDE.md](CLAUDE.md) - Mazzy architecture documentation

## Change Log

- **2025-12-25**: Fixed ELR_EL1 advancement bug (infinite SVC loop)
- **2025-12-25**: Documented exception handling requirements
- **2025-12-25**: Investigating SP_EL0 corruption and memory allocation failures
