# Framebuffer Initialization Bisect Results

## Summary

Git bisect identified **commit `2b9809c`** ("we got the prologue code for go sorted out") as the commit that broke framebuffer initialization.

## Bisect Details

- **Working commit**: `9c3569a` ("Add semihosting exit after event handling test completes")
- **Breaking commit**: `2b9809c` ("we got the prologue code for go sorted out")
- **Bad commit**: `96a3852` (master/HEAD - WIP verbose ramfb tracing)

### Bisect Steps
1. e433835 - **GOOD** (baseline DMA framebuffer working)
2. 1e9045d - **GOOD** (new boot image)
3. 2270830 - **GOOD** (Fix Go-assembly interop)
4. 9c3569a - **GOOD** (Add semihosting exit)
5. 2b9809c - **BAD** ← First bad commit
6. 96a3852 - **BAD** (master)

## The Breaking Change

Commit `2b9809c` made significant changes to Go function prologue and runtime initialization in `src/asm/lib.s`:

### Before (Working):
```assembly
// Set x28 (goroutine pointer) to point to runtime.g0
// runtime.g0 is at address 0x331a00
movz x28, #0x331a, lsl #16    // Load upper 16 bits: 0x331a00
movk x28, #0x0000, lsl #0     // Load lower 16 bits
```

### After (Broken):
```assembly
// Set x28 (goroutine pointer) to point to runtime.g0
// runtime.g0 is at address 0x40100ce0 (found via target-nm)
movz x28, #0x4010, lsl #16    // Load bits [31:16]: 0x4010
movk x28, #0x0ce0, lsl #0     // Load bits [15:0]:  0x0ce0

// Initialize g0.stackguard0 (offset +16 in g structure)
// Set to bottom of heap (0x40500000) so stack check always passes
movz x11, #0x4050, lsl #16    // Load 0x40500000
movk x11, #0x0000, lsl #0
str  x11, [x28, #16]          // Store to g0.stackguard0
```

### Key Changes:
1. **g0 location moved**: From `0x331a00` (ROM/code region) to `0x40100ce0` (RAM region)
2. **stackguard0 initialization added**: Sets `g0.stackguard0` to `0x40500000`
3. **Compiler flags changed**: Added `-gcflags "all=-N -l"` (disables optimizations/inlining)
4. **Additional debug output**: Extensive UART printing of stack values
5. **Event loop integration**: Added `go_event_loop` calls in boot.s

## Symptoms

When fw_cfg directory enumeration runs:
- Prints "RAMFB: File count=0000000A" (correctly reads 10 entries)
- Prints "RAMFB: Searching files..."
- **HANGS** on first `qemu_cfg_read()` call
- Never finds "etc/ramfb" selector
- Framebuffer remains uninitialized (black screen)

## Root Cause Analysis

The exact mechanism is unclear, but possibilities include:

1. **Stack guard interference**: Setting `stackguard0` to `0x40500000` may interfere with:
   - MMIO operations at `0x09000000` (fw_cfg)
   - Stack checks during fw_cfg reads
   - Memory barriers or cache coherency

2. **Excessive UART output**: The debug code prints extensive hex values during every `kernel_main` entry, potentially:
   - Affecting timing of fw_cfg operations
   - Interfering with MMIO read completion
   - Causing buffer/timing issues

3. **Compiler optimization changes**: `-gcflags "all=-N -l"` disables inlining and optimizations:
   - May change how `qemu_cfg_read()` executes
   - Could affect register usage or memory ordering
   - Might expose timing-dependent bugs

4. **g0 relocation**: Moving g0 from ROM to RAM region might affect:
   - Runtime behavior during MMIO operations
   - Stack management during fw_cfg reads
   - Memory access patterns

## Files Modified in Breaking Commit

- `src/Makefile`: Added `-gcflags "all=-N -l"`, `GoEventLoopEntry` symbol
- `src/asm/boot.s`: Added early UART init, `go_event_loop` calls, breadcrumbs
- `src/asm/lib.s`: Major changes to `kernel_main` stack/g0 initialization, extensive debug output
- `src/go/dummy/dummy.go`: Created (event loop stub)
- `src/go/mazarin/framebuffer_qemu.go`: Minor changes
- `src/go/mazarin/kernel.go`: Added event loop references

## Recommended Fix Options

### Option A: Revert the Breaking Commit
```bash
git revert 2b9809c
```
This will restore framebuffer functionality but lose the Go prologue improvements.

### Option B: Selective Revert
Revert only the problematic parts:
1. Keep g0 at original location (`0x331a00`)
2. Remove or modify `stackguard0` initialization
3. Remove excessive debug UART output from `kernel_main`
4. Test incrementally to find minimum change needed

### Option C: Fix Forward
Investigate why the new initialization breaks fw_cfg:
1. Add instrumentation to `qemu_cfg_read()` in the broken commit
2. Check if stack guard is triggering during MMIO operations
3. Test with reduced UART debug output
4. Experiment with different `stackguard0` values

### Option D: Use Working Branch
Stay on `working-fb-init-with-breadcrumbs` branch (based on e433835) which has:
- Confirmed working DMA framebuffer
- Minimal breadcrumb tracing
- No prologue issues

## Testing Commands

```bash
# Test current master (broken)
cd /Users/iansmith/mazzy/src && make clean && make qemu
timeout 15 mazboot  # Black screen, no etc/ramfb found

# Test commit before break (working)
git checkout 9c3569a
cd src && make clean && make qemu
timeout 15 mazboot  # Blue background, text rendering works

# Test breaking commit (first bad)
git checkout 2b9809c
cd src && make clean && make qemu
timeout 15 mazboot  # Black screen, hangs at "Searching files..."
```

## Next Steps

1. **Immediate**: Choose Option A or D to restore functionality
2. **Investigation**: If keeping prologue changes is important, use Option B or C
3. **Long-term**: Understand why `stackguard0` initialization interferes with MMIO

## Related Documentation

- Original working baseline: commit e433835
- Working branch: `working-fb-init-with-breadcrumbs`
- DMA investigation: `docs/DMA-INVESTIGATION-COMPLETE.md`
- Framebuffer investigation: `docs/FRAMEBUFFER-INVESTIGATION-SUMMARY.md` (if exists)

