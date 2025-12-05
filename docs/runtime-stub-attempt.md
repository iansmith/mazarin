# Attempt to Simulate Go Runtime Write Barrier

## Goal

Create minimal runtime stubs to make `gcWriteBarrier` work in bare-metal, allowing pointer assignments to global variables.

## Analysis of `gcWriteBarrier`

From disassembly analysis, `gcWriteBarrier` expects:

1. **x28 register** pointing to a goroutine (`g`) structure
   - Address: `runtime.g0` at `0x331a00`

2. **g.m** (offset 48) pointing to a machine (`m`) structure
   - Address: `runtime.m0` at `0x332100`

3. **m.wbBufStruct** (offset 200) pointing to write barrier buffer structure
   - Contains buffer pointer and end pointer

4. **Write barrier buffer** - large enough to never fill
   - Buffer pointer at offset 5272 (0x1498)
   - Buffer end at offset 5280 (0x14A0)

## Implementation

Created `runtime_stub.go` with `initRuntimeStubs()` that:
- Sets `g0.m = m0` (offset 48)
- Allocates write barrier buffer structure at `0x600000`
- Allocates 64KB write barrier buffer at `0x601000`
- Sets up buffer pointers in the structure
- Sets `m0.wbBufStruct` to point to the buffer structure
- Enables write barrier flag

Modified `lib.s` to set `x28 = 0x331a00` (points to `g0`) before calling `KernelMain`.

## Current Status

**Still failing** - pointer assignments to `.bss` globals return `nil` even with runtime stubs initialized.

## Why It Might Not Work

1. **Write barrier buffer processing**: `gcWriteBarrier` writes to a buffer, not directly to the destination. The actual assignment might happen later via `wbBufFlush`, which requires a full GC runtime.

2. **Missing `wbBufFlush` implementation**: When the buffer fills (or on certain conditions), `gcWriteBarrier` calls `runtime.wbBufFlush.abi0`, which we haven't implemented. This will crash.

3. **GC runtime dependencies**: Even if the write barrier works, it's designed for a concurrent GC that needs to track writes. Without a GC, the writes might not be processed correctly.

4. **Compiler expectations**: The compiler might emit code that expects the write barrier to do more than just record the write - it might expect the GC to process it.

## Alternative Approach

Instead of trying to make the write barrier work, we could:

1. **Use `//go:nowritebarrier` directive**: This tells the compiler to error if write barriers are present. But it doesn't prevent them from being generated.

2. **Patch the binary**: After compilation, patch the write barrier flag check to always branch to the direct store path.

3. **Use local variables**: The current workaround - avoid global pointer variables entirely.

## Conclusion

While we can initialize the minimal structures that `gcWriteBarrier` expects, the write barrier is deeply integrated with Go's garbage collector. Making it work in bare-metal would require implementing a significant portion of the GC runtime, which defeats the purpose of bare-metal programming.

**Recommendation**: Continue using local variables for pointers. The runtime stub approach is too complex and may not work even if fully implemented.







