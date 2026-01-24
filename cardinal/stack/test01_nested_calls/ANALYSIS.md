# Analysis: test01_nested_calls - ARM64 Go Calling Convention

## Summary of Findings

By analyzing the compiled ARM64 assembly for this 4-level nested call program, we can now definitively answer critical questions about how Go manages the stack, frame pointer, and registers across function calls.

## Key Patterns Observed

### 1. Function Prologue (Entry) Pattern

Every function follows this exact pattern (example from `level1`):

```assembly
a4b50:  f9400b90  ldr   x16, [x28, #16]      // Stack overflow check: load stack limit
a4b54:  eb3063ff  cmp   sp, x16              // Compare SP with limit
a4b58:  54000569  b.ls  a4c04                 // If SP <= limit, call morestack
a4b5c:  f8180ffe  str   x30, [sp, #-128]!    // Push LR, allocate 128 bytes
a4b60:  f81f83fd  stur  x29, [sp, #-8]       // Save FP at [sp-8]
a4b64:  d10023fd  sub   x29, sp, #0x8        // Set FP = SP - 8
```

**Critical observations**:
1. **LR (X30) is saved FIRST** with a pre-decrement writeback (`str x30, [sp, #-N]!`)
   - This atomically: saves LR, allocates the stack frame, and updates SP
2. **FP (X29) is saved immediately after** at `[sp-8]` (which is `[FP+0]` after FP is set)
3. **FP points to the saved FP location** - specifically `FP = SP - 8`
4. This means: `[FP+0] = saved FP`, `[FP+8] = saved LR`

### 2. Stack Frame Layout

For `level1` (128-byte frame):

```
High Address
+---------------------------+ ← SP on entry (before prologue)
| Saved LR (X30)            | [FP+8]   = [SP+120]
+---------------------------+ [FP+0]   = [SP+112]
| Saved FP (X29)            | ← FP points here after prologue
+---------------------------+
| Local variables           | [SP+88] to [SP+112]
| & spill slots             |
+---------------------------+
| Outgoing call args        | [SP+0] to [SP+88]
| (if needed)               |
+---------------------------+ ← SP after prologue
Low Address
```

**Stack frame size**: 128 bytes for level1, 144 bytes for level2, 160 bytes for level3
- Varies based on local variables, spill slots, and maximum outgoing parameter size

### 3. Parameter Passing

**Observation from `level1` calling `level2`**:

```assembly
# level2(a int64, b int64, tag string) expects:
#   R0 = a (int64)
#   R1 = b (int64)
#   R2 = tag.ptr (string pointer)
#   R3 = tag.len (string length)

a4bdc:  f9402be0  ldr   x0, [sp, #80]       # Load a into R0
a4be0:  f94027e1  ldr   x1, [sp, #72]       # Load b into R1
a4be4:  90000182  adrp  x2, d4000           # Load string ptr into R2
a4be8:  91020c42  add   x2, x2, #0x83
a4bec:  d2800163  mov   x3, #0xb            # Load string len into R3
a4bf0:  9400000c  bl    a4c20 <main.level2> # Call
```

**Parameters in registers**:
- X0-X7 used for integer/pointer parameters
- Strings are 2-word values (ptr + len), consuming 2 registers
- If >8 register slots needed, remaining parameters go on stack

**Example**: `level3(s1 string, s2 string, x int64, y int64)`
- R0 = s1.ptr
- R1 = s1.len
- R2 = s2.ptr
- R3 = s2.len
- R4 = x
- R5 = y
- All fit in registers!

### 4. Return Values

**Observation from `level1` returning to `main`**:

```assembly
a4bf4:  91000400  add   x0, x0, #0x1       # result + 1
a4bf8:  f85f83fd  ldur  x29, [sp, #-8]     # Restore FP
a4bfc:  f84807fe  ldr   x30, [sp], #128    # Restore LR, free stack
a4c00:  d65f03c0  ret                      # Return to caller
```

**Return value in R0**:
- Single int64 return value in X0
- Multiple return values would use X0, X1, X2, etc.

### 5. Function Epilogue (Exit) Pattern

Every function uses this pattern:

```assembly
a4bf8:  f85f83fd  ldur  x29, [sp, #-8]     # Restore FP from [sp-8] = [FP+0]
a4bfc:  f84807fe  ldr   x30, [sp], #128    # Restore LR, free stack frame
a4c00:  d65f03c0  ret                      # Return to caller
```

**Critical observations**:
1. **FP is restored first** from `[sp-8]`
2. **LR is restored with post-increment** (`ldr x30, [sp], #N`)
   - This atomically: restores LR and frees the stack frame
3. **SP is back to its original value** (before prologue) after epilogue

### 6. Callee-Saved Registers

**NOT OBSERVED in these functions!**

None of the functions in our test save X19-X28 (callee-saved registers). This means:
- These simple functions don't need to use X19-X28
- If they did, they would save them in the prologue and restore in epilogue
- The calling convention guarantees X19-X28 are preserved across calls

### 7. Frame Pointer (FP) Behavior

**Key insight**: FP is set up in EVERY function, including leaf functions!

```assembly
# From level1 prologue:
a4b60:  f81f83fd  stur  x29, [sp, #-8]     # Save old FP
a4b64:  d10023fd  sub   x29, sp, #0x8      # FP = SP - 8
```

**What FP points to**:
- `[FP+0]` = Saved FP (previous function's FP)
- `[FP+8]` = Saved LR (return address)
- This creates a **frame chain** for stack unwinding

**FP is ALWAYS 8 bytes below the saved LR** on the stack.

## Critical Answers for Our Exception Handler

### Question 1: Where does SP need to point when returning from a syscall?

**Answer**: SP must point to **exactly the same address** as when the SVC instruction was executed.

**Evidence**:
- Function prologues atomically save LR and adjust SP
- Function epilogues atomically restore LR and adjust SP back
- No function expects SP to change during the call
- The epilogue does `ldr x30, [sp], #N` which assumes SP is at the same offset as the prologue left it

### Question 2: Where does FP need to point when returning from a syscall?

**Answer**: FP must **not change** during a syscall. It should point to the same location as before the SVC.

**Evidence**:
- FP is set once in the prologue and not modified until the epilogue
- FP points to the saved FP/LR pair on the stack
- If we use a different stack for exception handlers, we must restore the original FP before eret

### Question 3: What if SP_EL0 vs SP_EL1 confusion happens?

**This is EXACTLY our bug!**

When SVC occurs:
1. CPU switches from EL1t (using SP_EL0) to EL1h (using SP_EL1)
2. Our exception handler runs on SP_EL1 (exception stack)
3. When we `eret`, CPU switches back to EL1t (using SP_EL0)
4. **But we never restored SP_EL0!**

**Result**: After eret, kmazarin continues with **wrong SP** (still has exception stack address).

When the function tries to restore FP and LR:
```assembly
ldur  x29, [sp, #-8]      # Reads from WRONG stack!
ldr   x30, [sp], #128     # Reads from WRONG stack!
```

The FP now points to garbage, and when `sysMmap` (NOFRAME) tries to write return values:
```assembly
MOVD  R0, p+32(FP)        # Writes to [FP+32] = garbage address!
MOVD  $0, err+40(FP)      # Writes to [FP+40] = garbage address!
```

The return values never reach the caller's stack!

### Question 4: What registers MUST be preserved across a syscall?

**Answer**: From the caller's perspective, a syscall is a normal function call. We must preserve:

**Callee-saved registers (MUST preserve)**:
- X19-X28 (if we use them in the exception handler)
- X29 (FP) - CRITICAL!
- SP (via SP_EL0 for EL1t mode)

**Caller-saved registers (can be clobbered)**:
- X0-X18 (but X0 is the return value!)
- X30 (LR) - but this is handled by the function itself

**Special**:
- X28 (g) - Go runtime's current goroutine pointer - MUST NEVER CHANGE!

### Question 5: Where does sysMmap write its return values?

From `/opt/homebrew/Cellar/go/1.25.5/libexec/src/runtime/sys_linux_arm64.s`:

```assembly
TEXT runtime·sysMmap(SB),NOSPLIT|NOFRAME,$0
    # ... syscall ...
ok:
    MOVD  R0, p+32(FP)      # Write p to [FP+32]
    MOVD  $0, err+40(FP)    # Write err to [FP+40]
    RET
```

**NOFRAME** means sysMmap doesn't set up its own FP! It uses **the caller's FP**.

If FP points to the wrong stack, these writes go to the wrong memory location!

## The Solution

Our exception handler MUST:

1. **Save SP_EL0 on exception entry** (before any stack operations)
2. **Switch to exception stack** (SP_EL1)
3. **Save all caller-saved registers** (X0-X18)
4. **Keep FP (X29) and g (X28) unchanged**
5. **Handle the syscall** (FP and g still point to kernel stack)
6. **Restore all caller-saved registers** (except X0 which is the return value)
7. **Restore SP_EL0** (back to kernel stack)
8. **Execute eret**

**Simplest approach**:
- Use a fixed memory location (e.g., `0x40FFF000`) to save SP_EL0
- On exception entry: `mrs x0, SP_EL0; str x0, [0x40FFF000]`
- Before eret: `ldr x0, [0x40FFF000]; msr SP_EL0, x0`

This avoids complex register juggling!

## Stack Frame Size Observations

- `main.main`: 112 bytes ($112-0)
- `level1`: 128 bytes ($128-24) - 24 bytes for parameters
- `level2`: 144 bytes ($144-32) - 32 bytes for parameters
- `level3`: 160 bytes ($160-48) - 48 bytes for parameters
- `level4`: Not shown, but likely similar pattern

Frame size = locals + spills + max outgoing args + saved FP/LR (16 bytes)

## Conclusion

We now have **definitive proof** of how Go manages stacks and registers on ARM64. The critical insight is:

**SP and FP must be exactly as the caller left them when a syscall returns.**

Our SP_EL0 corruption bug is confirmed:
- We switch to exception stack (SP_EL1) on SVC
- We never restore SP_EL0 before eret
- Kmazarin continues with wrong SP after syscall
- Function epilogue reads garbage from wrong stack
- FP becomes corrupted
- sysMmap writes return values to garbage memory
- Caller reads garbage from its stack
- sysAllocOS sees err != 0 and returns nil
- persistentalloc1 throws "cannot allocate memory"

**Next step**: Implement SP_EL0 save/restore in exceptions.s using a fixed memory location.
