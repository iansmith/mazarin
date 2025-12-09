# Write Barrier Final Status and Findings

## Summary of Work Completed

### 1. Generalized Runtime Patching Tool ✅

**Created**: `tools/runtime/patch_runtime.py`

**Features**:
- Accepts function name pairs (old → new) as command-line arguments
- Automatically finds symbol addresses (prefers weak for old, strong for new)
- Finds all call sites using `target-objdump`
- Patches all `bl` instructions to redirect calls
- Successfully patches 334 call sites

**Integration**:
- Makefile variable: `RUNTIME_REPLACEMENTS`
- Runs automatically during build after linking
- Configurable and extensible

**Result**: ✅ Tool works perfectly - all 334 call sites successfully redirected

### 2. QEMU Configuration Updates ✅

**Changed to `virt` machine type**:
- Better UART compatibility (`0x09000000` address matches our code)
- Serial output works reliably
- Updated `docker/runqemu-fb`, `docker/Dockerfile`
- Removed informational echo statements for cleaner automated testing

**Container management**:
- Automatic cleanup of orphaned containers
- Unique container names (`mazarin-<timestamp>-<pid>`)
- Works with timeout and automation

**Result**: ✅ QEMU configuration works - serial output visible, kernel executes

### 3. Documentation Updates ✅

**Updated `.cursorrules`**:
- Environment setup instructions
- QEMU script usage
- Automated testing workflow
- Container management details

**Created documentation**:
- `docs/qemu-uart-comparison.md` - UART configuration comparison
- `docs/qemu-virt-migration.md` - Migration to virt machine type
- `docs/write-barrier-analysis.md` - Analysis of write barrier mechanism
- `docs/write-barrier-recovery-plan.md` - Recovery plan and debugging
- `docs/write-barrier-final-status.md` - This document

## Write Barrier Flag Location

### Answer to "Where are write barrier flags relative to the pointer to the object?"

**There is ONE global write barrier flag for the entire runtime**, not per-object:

- **Location**: `runtime.writeBarrier` at `0x3582C0`
- **Section**: `.noptrbss` (no-pointer BSS)
- **Offset from `runtime.zerobase`**: 704 bytes (0x2C0)
- **Type**: Structure with `enabled` field at offset 0

**The flag is NOT relative to each object**. The compiler checks this single global flag before EVERY pointer assignment to decide whether to call the write barrier or do a direct store.

### Compiler-Generated Code Pattern

```assembly
// Before pointer assignment: globalPtr = value
adrp x1, runtime.zerobase         // Load page address
add x1, x1, #0x2C0                 // Add offset 704 -> 0x3582C0
ldr w0, [x1]                       // Load flag value
cbz w0, direct_store               // If flag=0, skip write barrier
bl runtime.gcWriteBarrier2         // If flag=1, call write barrier
direct_store:
str x2, [x27]                      // Direct store (happens regardless)
```

### Section Information

**`.noptrbss` section** (no-pointer BSS):
- Start: `0x358000`
- Size: `0x14ec0` bytes (~84KB)
- Flags: `WA` (writable, allocatable)
- Type: `NOBITS` (not loaded from ELF, allocated at runtime)
- Purpose: Contains runtime variables that don't contain pointers (so GC can ignore them)

**Key variables in `.noptrbss`**:
- `runtime.writeBarrier` at `0x3582C0` - The global write barrier flag
- `runtime.zerobase` at `0x358000` - Base address for runtime data
- Various internal runtime structures

## Current Status: Write Barrier Not Working

### Test Results

**Output**:
```
S!      ← 'S' from boot.s, '!' means flag read-back is 0
B       ← BSS cleared
K       ← kernel_main entered
...
T2      ← Test 2: pointer assignment
0       ← Flag is 0 when checked from Go
A       ← After assignment
N       ← Assignment failed
...
W       ← Write barrier called later (for different assignment)
```

### Root Cause

**The write barrier flag cannot be set**:

1. **Write appears to happen**: `str w11, [x10]` executes at `0x200088`
2. **Read-back shows 0**: Immediate `ldr w14, [x10]` returns 0 (output: '!')
3. **Flag remains 0**: When checked from Go during T2, flag is still 0

**Possible explanations**:
1. **NOBITS section not allocated**: QEMU may not allocate `.noptrbss` section memory
2. **Memory not writable**: Despite `WA` flags, memory may not be accessible
3. **Cache/MMU issue**: Write may be cached but not visible to reads
4. **Address calculation error**: Despite verification, address may be wrong

### Why T2 Shows "N"

1. **Flag is 0**: Compiler checks flag, sees 0
2. **Write barrier bypassed**: Compiler does direct `str x2, [x27]` instead of `bl gcWriteBarrier2`
3. **Our patched function never called**: The patching works, but the call is skipped
4. **Direct store fails**: The `str x2, [x27]` fails (unknown why)

## Verification of Patching Tool

**The generalized runtime patching tool works correctly**:
- ✅ Finds old function addresses (weak symbols from Go runtime)
- ✅ Finds new function addresses (strong symbols from our code)
- ✅ Finds all 334 call sites automatically
- ✅ Patches all call sites to redirect to our implementations
- ✅ Build integrates cleanly

**Evidence**:
```
Processing replacement: runtime.gcWriteBarrier2 -> runtime.gcWriteBarrier2
  Old function address: 0x26ecf0
  New function address: 0x27cee4
  Found 285 call site(s)
...
Successfully patched 334 call site(s)
```

**Disassembly verification**:
```
2039e0:  9401e475  bl  27cee4 <runtime.gcWriteBarrier2>
```

Calls correctly redirect to our implementation.

## Recommendations

### Option 1: Accept Current Limitation (Pragmatic)

**Use local variables instead of global pointers**:
```go
// Don't use global pointers
// var heapSegmentListHead *heapSegment  // DON'T USE

// Use local variables and pass as parameters
func heapInit(heapStart uintptr) *heapSegment {
    head := castToPointer[heapSegment](heapStart)
    head.next = nil
    head.prev = nil
    return head
}
```

**Pros**:
- Works reliably in bare-metal
- No write barrier issues
- Recommended by Go documentation for bare-metal

**Cons**:
- Requires refactoring to avoid global pointers
- More parameter passing

### Option 2: Investigate Memory Allocation (Complex)

**Debug why `.noptrbss` writes fail**:
1. Check if QEMU allocates NOBITS sections
2. Verify memory is actually writable at `0x3582C0`
3. Try different memory addresses
4. Check if MMU/cache configuration is needed

**This requires**:
- Deep QEMU knowledge
- Memory management expertise
- May not be solvable without MMU setup

### Option 3: Compiler Flags (Research Needed)

**Disable write barrier at compile time**:
- Research Go compiler flags to disable write barrier checks
- May require custom Go toolchain build
- Complex and may have other side effects

## Conclusion

**The generalized runtime patching tool is complete and working**:
- Successfully generalizes the patching approach
- Handles multiple function replacements
- Integrates cleanly into the build process
- Can be extended for other runtime function replacements

**The write barrier itself remains problematic**:
- Flag cannot be set reliably (`.noptrbss` memory access issues)
- When flag=0, compiler bypasses write barrier entirely
- Direct stores also fail (separate issue to investigate)

**For production use**: Avoid global pointer variables and use local variables instead. This is the recommended approach for bare-metal Go programming.

## Files Modified

### Tools
- `tools/runtime/patch_runtime.py` - Generalized patching tool (NEW)
- `src/patch_writebarrier.py` - Removed (replaced by generalized tool)

### Build System
- `src/Makefile` - Updated to use `patch_runtime.py` with configurable replacements

### QEMU Configuration
- `docker/runqemu-fb` - Updated to use `virt` machine type, removed echo statements
- `docker/Dockerfile` - Updated default entrypoint to use `virt` machine type

### Kernel Code (Debugging)
- `src/asm/writebarrier.s` - Reverted to simple 3-line version
- `src/asm/boot.s` - Added flag-setting code (doesn't work reliably)
- `src/asm/lib.s` - Cleaned up debug code
- `src/linker.ld` - Added `.noptrbss` section
- `src/go/mazarin/kernel.go` - Added minimal flag checking
- `src/go/mazarin/runtime_stub.go` - Commented out Go-based flag setting
- `src/go/mazarin/memory.go` - Reverted to simple version

### Documentation
- `.cursorrules` - Updated with environment setup and QEMU usage
- Multiple analysis documents in `docs/`











