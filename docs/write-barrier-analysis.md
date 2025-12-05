# Write Barrier Analysis and Recovery Plan

## Review of Chat History

### What Worked Before
According to the documentation:
- Write barrier patching tool successfully redirected calls
- 334 call sites were patched
- Kernel executed through all tests
- T2 test showed "N" (failure), but kernel didn't crash

### What Changed
During debugging, we added extensive debug code:
1. Complex debug output in `writebarrier.s` (WDV=! pattern)
2. Test code in `kernel.go` to check flag values
3. Modified `writeMemory32` to use assembly version

### Current State
- Kernel crashes early (only shows "SB K")
- Write barrier flag is 0 (disabled) during T2 assignment
- When flag=0, compiler does direct store instead of calling write barrier
- Direct store fails (hence "N")

## Write Barrier Flag Location

### Global Flag Structure

From disassembly analysis:
- `runtime.zerobase`: `0x358000` (in `.noptrbss` section)
- `runtime.writeBarrier` flag: `0x3582C0` (zerobase + 704 = 0x2C0)

The write barrier flag is a **global flag**, not per-object. The compiler checks this single flag before every pointer assignment to decide whether to call the write barrier or do a direct store.

### Compiler-Generated Code Pattern

```assembly
// Load write barrier flag
adrp x1, runtime.zerobase     // Load page address
add x1, x1, #0x2C0             // Add offset 704
ldr w0, [x1]                   // Load flag value
cbz w0, direct_store           // If flag=0, skip write barrier
bl runtime.gcWriteBarrier2     // If flag=1, call write barrier
direct_store:
str x2, [x27]                  // Direct store (happens regardless)
```

**Key insight**: The write barrier is only called if the flag is non-zero at compile time check. If flag=0, the `bl` instruction is skipped entirely.

## Why T2 Shows "N"

### The Problem

1. **Flag is 0**: When T2 assignment happens, the flag is 0
2. **Direct store is used**: Compiler skips `bl runtime.gcWriteBarrier2` and does `str x2, [x27]`
3. **Our patched function isn't called**: Since the `bl` is skipped, our patched function never runs
4. **Direct store fails**: The `str x2, [x27]` fails (destination address invalid or protected?)

### Why Flag is 0

`initRuntimeStubs()` tries to set flag to 1 using `writeMemory32()`, but:
- Test shows `TN0` — `writeMemory32()` fails even for `.bss` variables
- This suggests the write itself may be triggering a write barrier check (recursion!)
- Or the compiler is optimizing away the write
- Or there's a memory protection issue

## Recovery Plan

### Option 1: Revert Debug Code (Immediate)

1. **Revert `writebarrier.s` to simple version**:
   ```assembly
   runtime.gcWriteBarrier2:
       str x2, [x27]
       ret
   ```

2. **Revert kernel.go test code**: Remove all the debug code that's causing crashes

3. **Test to see if kernel runs again**: Get back to "T2 N" state (not crashing)

### Option 2: Fix the Write Barrier Flag Issue (Root Cause)

The core issue is that the write barrier flag is never enabled because `writeMemory32()` doesn't work.

**Potential causes**:
1. **Recursive write barrier**: `writeMemory32()` may trigger a write barrier check itself
2. **`.noptrbss` section protection**: The flag is in `.noptrbss` (no-pointer .bss), may have special handling
3. **Compiler optimization**: Write may be optimized away

**Solutions**:
1. **Use assembly to set flag**: Set flag in `lib.s` after setting x28, before calling `KernelMain`
2. **Disable flag checks**: Use a compiler flag or build option to disable write barrier checks entirely
3. **Alternative approach**: Accept that write barriers don't work and use local variables instead of globals

### Option 3: Understanding the Original Behavior

According to `docs/writebarrier-patch-approach.md`:
- The patching approach was documented as working (calls redirected successfully)
- But the status was: "⚠️ **Assignment may still fail**: Test shows `T2 N`"
- So T2 showing "N" was the original behavior before we started debugging

**This means**: The write barrier patching never fully worked — it redirected calls, but the assignment still failed.

## Recommended Approach

### Step 1: Revert to Clean State

Remove all debug code and get back to the simple version that at least runs:

```assembly
runtime.gcWriteBarrier2:
    str x2, [x27]
    ret
```

### Step 2: Fix the Root Cause

The real issue is that **the write barrier flag is never enabled**. Two approaches:

**A. Set flag in assembly** (before calling KernelMain):
```assembly
// In lib.s, after setting x28:
movz x10, #0x3582, lsl #16    // 0x3582 << 16
movk x10, #0x00C0              // + 0xC0 = 0x3582C0
mov w11, #1
str w11, [x10]                 // Set flag to 1
dsb sy                         // Memory barrier
```

**B. Accept that write barriers don't work**: Use local variables and pass pointers as parameters instead of using global pointer variables.

### Step 3: Verify Flag Address

According to `target-nm` output:
- `.noptrbss` section starts at `0x358000`
- Write barrier flag should be at `0x3582C0` (offset 704 = 0x2C0)
- But we didn't see `runtime.writeBarrier` in the symbol table

We should verify:
```bash
target-nm kernel-qemu.elf | grep "runtime.writeBarrier"
target-nm kernel-qemu.elf | grep "runtime.zerobase"
```

## Write Barrier Flag vs Object Pointer

**Question**: "Where are the write barrier flags relative to the pointer to the object?"

**Answer**: There is only ONE global write barrier flag for the entire runtime:
- Location: `runtime.zerobase + 704` (typically `0x3582C0`)
- Type: `uint32` (or similar)
- Purpose: Global on/off switch for all write barriers

This is NOT per-object. All pointer assignments check the same global flag.

**The `runtime.writeBarrier` structure** contains:
- `enabled` field at offset 0 (the flag we're talking about)
- Other fields for GC coordination

The flag is checked by the compiler before EVERY pointer assignment to a global variable.







