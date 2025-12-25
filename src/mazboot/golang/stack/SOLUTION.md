# Solution: Fix SP_EL0 Corruption in Exception Handler

## Problem Visualization

### Normal Function Call (No Exception)

```
Kmazarin's Stack (SP_EL0 = 0xD00FFA00)
+---------------------------+ 0xD00FFA00 ← SP before call
| Saved LR                  | [FP+8]
+---------------------------+ [FP+0] ← FP = 0xD00FF9F8
| Saved FP                  |
+---------------------------+
| Locals                    |
+---------------------------+
| Return value slots        | [FP+32] ← sysMmap writes here
|                           | [FP+40] ← sysMmap writes here
+---------------------------+
| ...                       |
+---------------------------+ 0xD00FF900 ← SP after frame setup
```

### With Syscall - BROKEN (Current State)

```
Step 1: Before SVC
------------------
SP_EL0 = 0xD00FF900 (kmazarin's stack)
FP     = 0xD00FF9F8 (points to kmazarin's stack)

Step 2: SVC Exception Occurs
-----------------------------
CPU automatically:
- Switches from EL1t to EL1h
- Starts using SP_EL1 (exception stack)
- SP_EL1 = 0x5F010000

Step 3: Our Exception Handler
------------------------------
SP = 0x5F00FFF0 (exception stack)
FP = 0xD00FF9F8 (STILL points to kmazarin's stack - unchanged)

Step 4: Syscall Executes
-------------------------
mmap allocates memory successfully
Returns 0x48000000 in X0

Step 5: Exception Handler Returns (eret)
-----------------------------------------
CPU automatically:
- Switches from EL1h back to EL1t
- Starts using SP_EL0 again
- **BUT SP_EL0 was never updated!**

SP = ??? (SP_EL0 contains garbage or old value)
FP = 0xD00FF9F8 (unchanged)

Step 6: Function Epilogue Executes
-----------------------------------
ldur  x29, [sp, #-8]    # Read from WRONG ADDRESS!
ldr   x30, [sp], #128   # Read from WRONG ADDRESS!

FP now contains garbage!

Step 7: sysMmap Tries to Write Return Values
---------------------------------------------
MOVD  R0, p+32(FP)      # Write to [garbage+32]!
MOVD  $0, err+40(FP)    # Write to [garbage+40]!

Return values written to wrong memory!

Step 8: Caller Reads Return Values
-----------------------------------
p = [FP+32]             # Read garbage
err = [FP+40]           # Read garbage

sysAllocOS sees err != 0, returns nil
```

## The Fix: Save and Restore SP_EL0

### Fixed Memory Location

We'll use address `0x40FFF000` as a dedicated save area for SP_EL0.

**Why this address?**
- In mazboot's scratch area region (0x40FFF000-0x40FFF100)
- Always mapped (part of mazboot's data region)
- Won't conflict with stack or other data
- Easy to access with ADRP+ADD

### Implementation

```assembly
# ============================================================================
# Exception Entry - SAVE SP_EL0 IMMEDIATELY
# ============================================================================

sync_exception_handler:
    # CRITICAL: Save SP_EL0 FIRST, before ANY stack operations
    # Use fixed memory location 0x40FFF000 to avoid register juggling

    mrs x29, SP_EL0                   # Read current SP_EL0 (kmazarin's stack)
    movz x30, #0x40FF, lsl #16        # x30 = 0x40FF0000
    movk x30, #0xF000, lsl #0         # x30 = 0x40FFF000 (save area)
    str x29, [x30]                    # Save SP_EL0 to fixed memory

    # Now we can use x29 and x30 freely
    # Save them to current stack (wherever it is)
    stp x29, x30, [sp, #-16]!         # Push x29, x30

    # Save original SP (before we pushed)
    add x30, sp, #16                  # x30 = original SP

    # ... rest of exception handler (stack selection, reg saves, etc.) ...
```

```assembly
# ============================================================================
# Syscall Return - RESTORE SP_EL0 BEFORE ERET
# ============================================================================

syscall_return:
    # Restore all registers except X0 (return value)
    # ... (existing code) ...

    # CRITICAL: Restore SP_EL0 before eret
    movz x11, #0x40FF, lsl #16        # x11 = 0x40FF0000
    movk x11, #0xF000, lsl #0         # x11 = 0x40FFF000
    ldr x11, [x11]                    # x11 = saved SP_EL0
    msr SP_EL0, x11                   # Restore SP_EL0
    isb                               # Ensure SP_EL0 write completes

    # Restore ELR and SPSR
    movz x11, #0x40FF, lsl #16
    movk x11, #0xF020, lsl #0
    ldp x12, x13, [x11, #24]
    msr ELR_EL1, x12
    msr SPSR_EL1, x13
    isb

    # Restore x0 (syscall result)
    mov x0, x10

    # Restore g (x28)
    movz x10, #0x40FF, lsl #16
    movk x10, #0xF020, lsl #0
    ldr x28, [x10, #40]

    # Return from exception - NOW SP_EL0 IS CORRECT!
    eret
```

### Memory Layout for Scratch Area

```
0x40FFF000: SP_EL0 save area         (NEW! 8 bytes)
0x40FFF008: (unused)                 (8 bytes)
0x40FFF010: (unused)                 (8 bytes)
0x40FFF018: (unused)                 (8 bytes)
0x40FFF020: x29 (original)           (8 bytes)
0x40FFF028: x30 (original)           (8 bytes)
0x40FFF030: x0 (original)            (8 bytes)
0x40FFF038: ELR_EL1 (saved)          (8 bytes)
0x40FFF040: SPSR_EL1 (saved)         (8 bytes)
0x40FFF048: x28 (g, original)        (8 bytes)
```

## Why This Works

### Advantages

1. **Minimal Register Juggling**:
   - We immediately save SP_EL0 using x29/x30
   - Then we save x29/x30 themselves
   - No complex interdependencies

2. **Fixed Memory Location**:
   - 0x40FFF000 is always accessible
   - No risk of stack overflow corrupting it
   - Easy to debug (always same address)

3. **Atomic Save**:
   - SP_EL0 is saved before any other operations
   - Even if exception handler crashes, we can examine the saved value

4. **Clean Restore**:
   - Restore SP_EL0 right before eret
   - CPU switches to EL1t and uses the correct SP_EL0
   - Function epilogue reads from correct stack
   - FP remains valid
   - Return values written to correct location

### After the Fix

```
Step 1: Before SVC
------------------
SP_EL0 = 0xD00FF900 (kmazarin's stack)
FP     = 0xD00FF9F8 (points to kmazarin's stack)

Step 2: SVC Exception Occurs
-----------------------------
CPU switches to SP_EL1 = 0x5F010000

Step 3: Exception Handler Entry
--------------------------------
mrs x29, SP_EL0                 # x29 = 0xD00FF900
str x29, [0x40FFF000]           # ✅ SAVED!

Step 4: Syscall Executes
-------------------------
Returns 0x48000000 in X0

Step 5: Before eret
--------------------
ldr x11, [0x40FFF000]           # x11 = 0xD00FF900
msr SP_EL0, x11                 # ✅ RESTORED!

Step 6: eret
------------
CPU switches to EL1t
SP = SP_EL0 = 0xD00FF900        # ✅ CORRECT!
FP = 0xD00FF9F8                 # ✅ UNCHANGED, STILL CORRECT!

Step 7: Function Epilogue
--------------------------
ldur  x29, [sp, #-8]            # ✅ Reads from correct stack!
ldr   x30, [sp], #128           # ✅ Reads from correct stack!

FP and LR are correct!

Step 8: sysMmap Writes Return Values
-------------------------------------
MOVD  R0, p+32(FP)              # ✅ Writes to correct location!
MOVD  $0, err+40(FP)            # ✅ Writes to correct location!

Step 9: Caller Reads Return Values
-----------------------------------
p = 0x48000000                  # ✅ CORRECT!
err = 0                         # ✅ CORRECT!

sysAllocOS returns p = 0x48000000
persistentalloc1 succeeds!
```

## Implementation Plan

1. **Modify exceptions.s**:
   - Add SP_EL0 save immediately after `sync_exception_handler` label
   - Add SP_EL0 restore before `eret` in `syscall_return`

2. **Test**:
   - Build and run kmazarin
   - Verify mmap returns correct value
   - Verify sysAllocOS succeeds
   - Verify persistentalloc1 succeeds
   - Verify mallocinit succeeds

3. **Verify with debug**:
   - Add breadcrumbs to confirm SP_EL0 is saved/restored
   - Print SP_EL0 value at each step
   - Confirm FP remains valid throughout

## Expected Result

After this fix:
- SP_EL0 preserved across syscalls ✅
- FP remains valid ✅
- Return values written to correct location ✅
- Caller receives correct return values ✅
- sysAllocOS succeeds ✅
- persistentalloc1 succeeds ✅
- mallocinit succeeds ✅
- **Kmazarin initialization completes!** ✅
