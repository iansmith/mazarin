# Write Barrier Recovery Plan

## Key Findings

### Write Barrier Flag Location

From `target-nm` output:
- `runtime.zerobase`: `0x358000` (type `b` - in `.noptrbss` section)
- `runtime.writeBarrier`: `0x3582C0` (type `b` - in `.noptrbss` section)
- **Offset**: `0x3582C0 - 0x358000 = 0x2C0 = 704 bytes`

**Answer to "where are write barrier flags relative to the pointer to the object":**

The write barrier flag is **NOT per-object**. There is ONE global flag for the entire runtime:
- **Location**: `runtime.writeBarrier` at `0x3582C0`
- **Type**: Structure (first field is `enabled`, a boolean/uint32)
- **Purpose**: Global on/off switch checked before every pointer assignment

The compiler checks this single flag before every pointer assignment to decide whether to call the write barrier or do a direct store.

### Section Information

`.noptrbss` section (no-pointer BSS):
- Start: `0x358000`
- Size: `0x14ec0` bytes (~84KB)
- Flags: `WA` (writable, allocatable)
- Contains: Runtime variables that don't contain pointers (like the write barrier flag)

## Review: What Changed and Broke

### Original State (Before Debug Code)
- Kernel ran successfully through all tests
- T2 showed "N" (assignment failed)
- Write barrier patching tool worked (334 sites patched)
- No crashes

### Current State (After Debug Code)
- Kernel crashes early (only shows "SB K")
- Extensive debug code in `writebarrier.s` and `kernel.go`
- Attempts to call `writeMemory32` may be triggering issues

### Root Cause of "T2 N"

The T2 test failure is due to:

1. **Write barrier flag is 0** when T2 assignment happens
2. **Compiler skips write barrier call**: When flag=0, compiler does direct `str x2, [x27]`
3. **Our patched function never runs**: The `bl` instruction is skipped entirely
4. **Direct store fails**: The `str x2, [x27]` fails (why? unknown)

## Recovery Plan

### Phase 1: Revert Debug Code (Get Back to Working State)

**Goal**: Get kernel running again, back to "T2 N" state (not crashing)

1. **Revert `writebarrier.s` to simple version**:
   ```assembly
   .global runtime.gcWriteBarrier2
   runtime.gcWriteBarrier2:
       str x2, [x27]
       ret
   ```

2. **Remove debug code from `kernel.go`**:
   - Remove `writeMemory32` tests
   - Remove flag checking code
   - Keep only the simple "B\nA\n" markers around the assignment

3. **Verify kernel runs**: Should see full output ending with "T2 N"

### Phase 2: Set Write Barrier Flag Correctly

**Goal**: Enable the write barrier flag so compiler calls our patched function

**Problem**: `writeMemory32()` doesn't work (recursive write barrier issue?)

**Solution A: Set Flag in Assembly** (Recommended)

Add to `lib.s` in `kernel_main`, after setting x28 and before calling `main.KernelMain`:

```assembly
kernel_main:
    // ... existing UART 'K' output ...
    
    // Set x28 to point to runtime.g0
    movz x28, #0x331a, lsl #16
    movk x28, #0x0000, lsl #0
    
    // Enable write barrier flag BEFORE calling KernelMain
    // Address: 0x3582C0 = runtime.writeBarrier
    movz x10, #0x358, lsl #16     // Upper bits: 0x358
    movk x10, #0x2C0, lsl #0      // Lower bits: 0x2C0
    mov w11, #1                    // Value to write: 1
    str w11, [x10]                 // Store: runtime.writeBarrier.enabled = 1
    dsb sy                         // Memory barrier
    
    // Call Go function
    bl main.KernelMain
```

This bypasses Go's `writeMemory32()` and sets the flag directly in assembly.

**Solution B: Check Flag Address at Runtime**

Before setting the flag, verify the address is correct by reading it:

```assembly
// Read current flag value
movz x10, #0x358, lsl #16
movk x10, #0x2C0, lsl #0
ldr w11, [x10]
// If it's already non-zero, don't change it
// If it's zero, set it to 1
```

### Phase 3: Debug Why Direct Store Fails

If the write barrier is still bypassed (flag=0), we need to understand why the direct store fails:

**Possible causes**:
1. **Invalid destination address**: `x27` doesn't contain `heapSegmentListHead` address
2. **Memory protection**: `.bss` section may not be writable (unlikely, but check ELF flags)
3. **Alignment issue**: Destination may not be properly aligned
4. **MMU/Cache issue**: Memory management unit not configured for writes

**Debug approach**:
1. Add minimal debug output in assembly to check `x27` value
2. Verify destination address is in writable memory
3. Check if direct store triggers an exception

### Phase 4: Alternative Approach

If write barriers remain problematic:

**Use local variables only** (as recommended in `write-barrier-internals.md`):
```go
// Don't use global pointers
// var heapSegmentListHead *heapSegment  // DON'T USE

// Use local variables and pass them as parameters
func heapInit(heapStart uintptr) *heapSegment {
    head := castToPointer[heapSegment](heapStart)
    head.next = nil
    head.prev = nil
    return head // Return local, let caller decide what to do
}
```

## Implementation Steps

### Immediate Action

1. Revert `src/asm/writebarrier.s` to simple 3-line version
2. Revert `src/go/mazarin/kernel.go` to remove debug code (keep simple B/A markers)
3. Revert `src/go/mazarin/runtime_stub.go` to simple version
4. Revert `src/go/mazarin/memory.go` to original `writeMemory32`
5. Rebuild and verify kernel runs (should show "T2 N" without crashing)

### Next Action

Add flag-setting in `lib.s` assembly:
1. Set flag to 1 in assembly after setting x28
2. Verify compiler now calls write barrier
3. Check if T2 shows "P" (success)

### Verification

```bash
cd src && make clean && make kernel-qemu.elf && make push-qemu
cd .. && source enable-mazzy
timeout 40 runqemu-fb < /dev/null 2>&1 | tail -30
```

Look for:
- Full output (no early crash)
- "T2 N" or "T2 P"
- "W" markers if write barrier is called

## Summary

- **Write barrier flag**: Single global flag at `0x3582C0` (not per-object)
- **Current issue**: Flag is 0, so compiler skips write barrier call entirely
- **Recovery**: Revert debug code, then set flag in assembly
- **Test result**: Should see "T2 P" if write barrier works

The generalized patching tool works correctly — the issue is that the flag needs to be enabled so the compiler actually calls the functions we patched.





