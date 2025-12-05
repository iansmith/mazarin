# Go Write Barrier Internals in Bare-Metal

## Overview

The Go compiler **automatically emits** write barrier checks and calls when assigning pointers to global variables. This is compiler instrumentation, not optional runtime code.

## Compiler Instrumentation

When you write:
```go
var globalPtr *MyType
globalPtr = somePointer
```

The Go compiler emits assembly that:
1. Loads the write barrier flag from `runtime.zerobase + 704` (0x3582C0)
2. If flag is non-zero, calls `runtime.gcWriteBarrier2` (or `gcWriteBarrier3`, `gcWriteBarrier4`, etc.)
3. If flag is zero, performs a direct store

## Write Barrier Functions

The Go runtime provides multiple write barrier functions for different write sizes:
- `runtime.gcWriteBarrier2` - 16 bytes (2 pointers)
- `runtime.gcWriteBarrier3` - 24 bytes (3 pointers)
- `runtime.gcWriteBarrier4` - 32 bytes (4 pointers)
- etc.

All of these call the common `gcWriteBarrier` function.

## `gcWriteBarrier` Implementation

The `gcWriteBarrier` function expects a fully initialized Go runtime:

1. **Goroutine pointer (`g`)** in register `x28`
   - This is set by the Go runtime scheduler
   - In bare-metal, `x28` is not initialized

2. **`g.m` structure** at `[x28, #48]`
   - Points to the machine/M structure for the current goroutine
   - Contains write barrier buffer pointers

3. **Write barrier buffers** at `[g.m, #5272]` and `[g.m, #5280]`
   - These are allocated by the Go runtime
   - Used to batch write barrier operations

4. **Buffer flush function** `runtime.wbBufFlush.abi0`
   - Called when the write barrier buffer is full
   - Requires initialized GC runtime

## Why It Fails in Bare-Metal

In bare-metal Go:
- ❌ No goroutine (`g`) is initialized → `x28` is invalid
- ❌ No `g.m` structure exists → accessing `[x28, #48]` crashes
- ❌ No write barrier buffers allocated → accessing them corrupts memory
- ❌ No GC runtime initialized → `wbBufFlush` doesn't work

Even if you disable the write barrier flag, the compiler still emits the check. If the flag is somehow enabled later (or if the check is wrong), the write barrier will crash.

## Symbol Locations

From `target-nm` output:
- `runtime.zerobase` at `0x358000` (`.bss` section)
- `runtime.writeBarrier` at `0x3582C0` (zerobase + 704)
- `runtime.gcWriteBarrier2` at `0x26ecf0` (Go runtime's version, calls `gcWriteBarrier`)
- `gcWriteBarrier` at `0x26c130` (Go runtime's main implementation)
- Our custom `runtime.gcWriteBarrier2` at `0x27cbb4` (performs direct assignment)

## Solutions

### Solution 1: Avoid Global Pointer Variables (Simple)
- Use local variables for pointers
- Pass pointers as function parameters
- Works reliably in bare-metal
- **Recommended for most cases**

### Solution 2: Binary Patching (Current Implementation)
- Create custom write barrier implementation in `src/asm/writebarrier.s`
- Weaken Go runtime's write barrier symbols using `objcopy --weaken-symbol`
- Patch the binary after linking to redirect calls to our implementation
- Our implementation performs the assignment directly: `str x2, [x27]`
- **Status**: Patching works, call redirects successfully, but assignment may still be failing (under investigation)

### Solution 3: Initialize Minimal Runtime (Complex)
- Initialize a fake goroutine structure
- Set `x28` to point to it
- Allocate write barrier buffers
- Very complex, may not be worth it
- **Not recommended** - the write barrier still requires GC runtime to process buffers

## Current Implementation: Binary Patching

We've implemented a binary patching approach that:

1. **Custom Write Barrier** (`src/asm/writebarrier.s`):
   - Provides `runtime.gcWriteBarrier2` that performs direct assignment
   - Uses `str x2, [x27]` where `x27` = destination, `x2` = new value
   - No buffer, no GC tracking - just the assignment

2. **Symbol Weakening** (Makefile):
   - Uses `objcopy --weaken-symbol` to weaken Go runtime's write barrier symbols
   - Allows our global symbols to potentially override them

3. **Binary Patching** (`src/patch_writebarrier.py`):
   - Patches the ELF binary after linking
   - Redirects the `bl` instruction at `0x27c2a4` from Go runtime's `gcWriteBarrier2` (`0x26ecf0`) to our implementation (`0x27cbb4`)
   - Calculates correct file offset from `.text` section header
   - Encodes new `bl` instruction with correct relative offset

**Result**: The call successfully redirects to our function. However, the assignment test still shows failure (`T2 N`), suggesting there may be additional issues to investigate (register values, memory layout, etc.).

## Conclusion

The write barrier is **compiler-emitted code**, not optional runtime. It's deeply integrated into Go's garbage collector design. 

For bare-metal kernels, the practical solutions are:
1. **Avoid global pointer variables** (simplest and most reliable)
2. **Binary patching** (works but requires maintenance and may have edge cases)
