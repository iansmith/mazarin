# Bare-Metal Go Limitations and Root Causes

## Summary

After extensive testing, we've identified the root causes of two issues in bare-metal Go:

1. **Pointer assignment to .bss globals fails** (but doesn't hang)
2. **Complex control flow works fine** (not actually a problem)

## Issue 1: Pointer Assignment to .bss Globals

### Root Cause: Go's Write Barrier

When assigning a pointer to a global variable in `.bss`, Go's compiler inserts a call to `runtime.gcWriteBarrier2`. This is part of Go's garbage collector write barrier mechanism.

**Evidence from disassembly:**
```assembly
27c284:  cbz w3, 27c298          # Check if write barrier enabled
27c288:  adrp x27, heapSegmentListHead
27c28c:  ldr x1, [x27]            # Load old value
27c290:  bl runtime.gcWriteBarrier2 # Call write barrier
27c294:  stp x2, x1, [x25]        # Store via write barrier
27c298:  str x2, [x27]             # Direct store (if barrier disabled)
```

**What happens:**
1. Go checks if write barrier is enabled (flag at `runtime.zerobase + 704`)
2. If enabled, calls `runtime.gcWriteBarrier2` before the actual assignment
3. The write barrier may not be properly initialized in bare-metal
4. The assignment appears to complete, but reading back returns `nil`

**Test Results:**
- ✅ Simple `uint32` assignment to `.bss` works
- ❌ Pointer assignment to `.bss` fails (returns `nil` when read back)
- ✅ Pointer assignment to local variables works

### Workaround

Use **local variables** instead of global variables for pointers:

```go
// ❌ Doesn't work:
var heapSegmentListHead *heapSegment
heapSegmentListHead = castToPointer[heapSegment](heapStart)

// ✅ Works:
localHeapHead := castToPointer[heapSegment](heapStart)
```

## Issue 2: Complex Control Flow

### Finding: NOT A PROBLEM

Complex control flow (nested ifs, for loops, while loops, switch statements) **works fine** in bare-metal Go.

**Test Results:**
- ✅ Nested if statements work
- ✅ For loops work
- ✅ While-style loops (for with condition) work
- ✅ Switch statements work
- ✅ Function calls within control flow work

**Conclusion:** The earlier assumption that "complex control flow" was problematic was incorrect. The real issue was pointer assignment to globals.

## Why Write Barriers Fail in Bare-Metal

Go's write barrier (`runtime.gcWriteBarrier2`) is part of the garbage collector runtime. In bare-metal:

1. **No GC Runtime:** The full Go runtime (including GC) is not initialized
2. **Write Barrier State:** The write barrier flag may be uninitialized or point to invalid memory
3. **Barrier Implementation:** `runtime.gcWriteBarrier2` may not be implemented for bare-metal or may crash

### Technical Details: What the Compiler Emits

When the Go compiler encounters a pointer assignment to a global variable, it emits code that:

1. **Checks the write barrier flag** at `runtime.zerobase + 704` (address `0x3582C0`)
2. **If enabled**, calls `runtime.gcWriteBarrier2` (or `gcWriteBarrier3`, `gcWriteBarrier4`, etc. depending on write size)
3. **If disabled**, performs a direct store

**Disassembly evidence:**
```assembly
27c27c:  adrp x27, 358000 <runtime.zerobase>  # Load zerobase address
27c280:  ldr w3, [x27, #704]                  # Load write barrier flag (0x3582C0)
27c284:  cbz w3, 27c298                        # If 0, skip write barrier
27c288:  adrp x27, heapSegmentListHead        # Load global variable address
27c28c:  ldr x1, [x27]                        # Load old value
27c290:  bl runtime.gcWriteBarrier2            # Call write barrier
27c294:  stp x2, x1, [x25]                     # Store via write barrier buffer
27c298:  str x2, [x27]                         # Direct store (if barrier disabled)
```

### What `gcWriteBarrier` Does

The `gcWriteBarrier` function (called by `gcWriteBarrier2`) expects:

1. **Valid goroutine (`g`)** in register `x28` (Go's goroutine pointer)
2. **Valid `g.m` structure** at `[x28, #48]` (goroutine's machine/M structure)
3. **Initialized write barrier buffers** at `[x0, #5272]` and `[x0, #5280]`
4. **If buffer is full**, calls `runtime.wbBufFlush.abi0` to flush the buffer

**Disassembly of `gcWriteBarrier`:**
```assembly
26c120:  str x30, [sp, #-224]!                # Save return address
26c130:  ldr x0, [x28, #48]                   # Load g.m (expects valid goroutine!)
26c134:  ldr x0, [x0, #200]                    # Load m structure
26c138:  ldr x1, [x0, #5272]                   # Load write barrier buffer pointer
26c13c:  ldr x27, [x0, #5280]                  # Load write barrier buffer end
26c144:  cmp x1, x27                           # Check if buffer is full
26c148:  b.hi 26c164                            # If full, flush buffer
26c14c:  str x1, [x0, #5272]                   # Update buffer pointer
26c190:  bl runtime.wbBufFlush.abi0             # Flush buffer if needed
```

**In bare-metal:**
- `x28` (goroutine pointer) is **not initialized** → accessing `[x28, #48]` reads invalid memory
- `g.m` structure doesn't exist → accessing `[x0, #200]` crashes
- Write barrier buffers are **not allocated** → accessing them corrupts memory

This is why even disabling the write barrier flag doesn't help - the compiler still emits the check, and if the flag is somehow set later, the write barrier will crash.

## Solutions

### Solution 1: Use Local Variables (Current Approach)
- Store pointers in local variables
- Pass them as function parameters
- Avoid global pointer variables

### Solution 2: Disable Write Barrier (Future)
- Find where write barrier flag is set
- Ensure it's disabled at kernel startup
- May require runtime initialization

### Solution 3: Bypass Write Barrier (Advanced)
- Use `unsafe` to write directly to global variable memory
- Create helper function that writes via `unsafe.Pointer`
- Risk: May break if GC is ever enabled

## Current Status

- ✅ Simple function calls work (`getLinkerSymbol`, `castToPointer`, `uartInit`)
- ✅ Complex control flow works (loops, nested ifs, switches)
- ✅ Simple .bss assignments work (`uint32`, `int`, etc.)
- ❌ Pointer .bss assignments fail (due to write barrier)
- ✅ Local variable assignments work (pointers and values)

## Recommendations

1. **Avoid global pointer variables** - use local variables instead
2. **Use abstraction functions** - `getLinkerSymbol()`, `castToPointer()` hide `unsafe.Pointer`
3. **Function calls are safe** - as long as they don't assign to global pointers
4. **Complex control flow is safe** - no restrictions needed

