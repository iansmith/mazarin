# Investigation Summary: 0xDEAD000E Bug

## Problem Statement
The system crashes with a page fault at address 0xDEAD000E when kmazarin tries to use the result of an mmap syscall. The error occurs in `runtime.persistentalloc1` when it attempts to dereference a pointer stored in `globalAlloc.persistent.base`.

## Root Cause Analysis

### Location of Corruption
- **Faulting instruction**: `0x41813f90: str x2, [x3]`
- **X3 value**: 0xDEAD000E (loaded from [X4])
- **X4 value**: 0x419a1068 (address of `globalAlloc.persistent.base`)
- **Conclusion**: `globalAlloc.persistent.base` contains the poison value 0xDEAD000E instead of a valid pointer

### What Should Happen
1. `runtime.persistentalloc1` calls `sysAlloc()`
2. `sysAlloc()` calls mmap syscall (222)
3. Mmap returns a valid address (e.g., 0x48000000)
4. Return value should be stored in `persistent.base`
5. Code should use `persistent.base` as a valid pointer

### What Actually Happens
1. Mmap executes and returns correctly (confirmed by seeing "M" debug output)
2. Debug code in `SyscallMmap` confirms `persistent.base` is NOT corrupted during the mmap call itself
3. BSS zeroing confirms the memory location starts at 0 (verified during boot)
4. **Something** writes 0xDEAD000E to `globalAlloc.persistent.base` (0x419a1068)
5. Later code tries to dereference 0xDEAD000E and crashes

## Bugs Found and Fixed

### Bug #1: Unsafe Debug Code Modifying Stack Pointer ✅ FIXED
**Location**: `syscall_return` in `exceptions.s`

**Problem**:
```asm
stp x0, x1, [sp, #-16]!  // Pre-decrement modifies SP!
```

This is exactly the kind of bug warned about in CLAUDE.md:
> "DO NOT modify SP with pre-decrement when the current SP value is critical"

**Impact**: When SP is modified, all subsequent accesses to the exception frame are offset by 16 bytes, causing register save/restore to access wrong memory locations and corrupt data.

**Fix**: Removed the unsafe debug code entirely.

### Bug #2: Syscall Return Value Saved to Wrong Offset ✅ FIXED
**Location**: `syscall_return` in `exceptions.s`

**Problem**:
```asm
str x0, [sp, #0]  // Saves to EXC_FRAME_X0_X1, overwrites saved X0 from exception entry
```

**Correct**:
```asm
str x0, [sp, #EXC_FRAME_SAVED_X0]  // Saves to dedicated slot at offset 304
```

**Root Cause**: The exception frame has TWO slots for X0:
- Offset 0 (`EXC_FRAME_X0_X1`): Saves X0 value from when exception occurred
- Offset 304 (`EXC_FRAME_SAVED_X0`): Dedicated slot for syscall return value

The code was incorrectly using offset 0 for both, causing the syscall return value to overwrite the original X0, and then restoring the wrong value.

**Fix**: Changed both save and restore to use offset 304 (EXC_FRAME_SAVED_X0).

## Remaining Mystery

Despite fixing both bugs, the system still crashes at 0xDEAD000E. This suggests:

1. **There's another bug** we haven't found yet that corrupts `globalAlloc.persistent.base`
2. **The value 0xDEAD000E** is being written by some code path we haven't traced
3. **Possible causes**:
   - Another register corruption issue in exception handling
   - Buffer overflow from adjacent memory writes
   - Uninitialized memory being used as a pointer
   - A third bug in our syscall return path

## Evidence Gathered

### What We Know Works
- ✅ Stack frame allocation is correct (320 bytes allocated, all offsets within range)
- ✅ Stack frame deallocation is correct (`add sp, sp, #320` before eret)
- ✅ BSS is properly zeroed during boot
- ✅ Mmap syscall executes and returns correct values
- ✅ SyscallMmap does NOT corrupt persistent.base (no corruption message printed)

### What We Know Fails
- ❌ `globalAlloc.persistent.base` (0x419a1068) contains 0xDEAD000E at crash time
- ❌ This value is being used as a pointer, causing page fault
- ❌ Corruption happens AFTER BSS zeroing and AFTER mmap returns

## Next Steps

### Immediate: Use GDB Hardware Watchpoint
The fastest way to find the bug is to watch the exact memory location:

```bash
# Start QEMU with GDB
GDB=1 NOGRAPHIC=1 /Users/iansmith/mazzy/bin/run-mazboot &

# In another terminal
~/mazzy/bin/target-gdb flash/mazboot.elf
(gdb) target remote :1234
(gdb) add-symbol-file src/kmazarin/build/kmazarin.elf 0x41800000
(gdb) watch *(uint64_t*)0x419a1068
(gdb) continue
```

This will stop execution the INSTANT anything writes to `globalAlloc.persistent.base`, showing us:
- What instruction wrote the value
- What registers/values were involved
- The call stack at that point

### Alternative: Add Strategic Debug Output
If GDB proves difficult, add breadcrumb prints in key locations:

1. **After every mmap return**: Print the return value
2. **Before/after register restore**: Print X0 value
3. **In persistentalloc1**: Print persistent.base before use

### Hypothesis to Test

Given that we fixed the offset bug but the crash persists, the most likely remaining causes are:

1. **X1 register corruption**: We restore X1 separately at the end - is this correct?
2. **Memory alignment issue**: Does the 16-byte SP modification from removed debug code have lingering effects?
3. **Race condition**: Is timer interrupt corrupting registers despite being disabled?
4. **Go runtime expectation mismatch**: Does Go expect certain registers to be preserved that we're not preserving?

## Files Modified

- `src/mazboot/asm/aarch64/exceptions.s`:
  - Line 1566: Changed `str x0, [sp, #0]` to `str x0, [sp, #EXC_FRAME_SAVED_X0]`
  - Line 1606: Changed `ldr x0, [sp, #0]` to `ldr x0, [sp, #EXC_FRAME_SAVED_X0]`
  - Removed unsafe debug code (lines 1564-1571)

## Tools Created

- `break-and-watch.gdb`: GDB script for setting breakpoint + watchpoint combos
- `watch-corruption.gdb`: GDB script to watch `globalAlloc.persistent.base`
- `debug-crash.gdb`: GDB script to examine state at crash point

## References

- Crash location: `runtime.persistentalloc1` at `malloc.go:1975`
- Faulting instruction: `0x41813f90`
- Corrupted variable: `globalAlloc.persistent.base` at `0x419a1068`
- CLAUDE.md warnings about unsafe debug code (lines on SP modification)
