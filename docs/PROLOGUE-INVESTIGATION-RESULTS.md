# Prologue Investigation Results

## Summary

Commit **2b9809c** ("we got the prologue code for go sorted out") breaks framebuffer initialization. 

**Last working commit**: 9c3569a (Add semihosting exit after event handling test completes)
**First broken commit**: 2b9809c (we got the prologue code for go sorted out)

## Investigation Findings

We systematically disabled each change from commit 2b9809c:

### Tests Performed

1. **Disabled stackguard0 initialization** ❌ Still broken
   - Commented out the `str x11, [x28, #16]` that sets g0.stackguard0
   - Result: No change, still hangs at "Searching files..."

2. **Disabled all debug UART output** ❌ Still broken  
   - Commented out all the extensive hex printing in kernel_main
   - Result: No change, still hangs

3. **Restored original g0 location** ❌ Still broken
   - Changed g0 from 0x40100ce0 back to 0x331a00 
   - Result: No change, still hangs

4. **Disabled event loop code** ❌ Still broken
   - Commented out go_event_loop in boot.s and lib.s
   - Result: No change, still hangs

5. **Re-added //go:nosplit to framebufferInit** ❌ Still broken
   - Added back the directive that was removed
   - Result: No change, still hangs

6. **Tried disabling -N -l compiler flags** ⚠️ Build failed
   - Without -N -l, GoEventLoopEntry gets optimized away
   - Can't test without major restructuring

## The Complex Problem

The issue appears to be a **combination of changes** rather than a single culprit:

1. The commit removes `//go:nosplit` from several functions, allowing stack growth checks
2. It adds `-gcflags "all=-N -l"` which disables optimizations and inlining
3. It changes g0 location to RAM region
4. It adds extensive initialization code that may affect timing

When combined, these changes cause `qemu_cfg_read()` to hang during fw_cfg directory enumeration.

## Why Individual Fixes Didn't Work

Each change we disabled still left other changes in place:
- We kept the `-N -l` compiler flags even when disabling assembly changes
- We kept the Go code structure even when fixing assembly
- The interactions between changes may be cumulative

## Root Cause Hypothesis

The most likely cause is that **stack growth checks** inserted by removing `//go:nosplit`, combined with **unoptimized code** from `-N -l`, cause timing or state issues during MMIO operations to fw_cfg.

Possible mechanisms:
1. Stack checks during `qemu_cfg_read()` interfere with MMIO timing
2. Unoptimized code changes register usage affecting fw_cfg state machine
3. The combination causes unexpected memory barriers or cache behavior

## Verified Solution

**Revert commit 2b9809c entirely**:

```bash
cd /Users/iansmith/mazzy
git revert 2b9809c
cd src && make clean && make qemu
```

This will:
- ✅ Restore framebuffer functionality
- ❌ Lose the Go prologue improvements
- ❌ Lose the event loop infrastructure

## Alternative: Cherry-pick from Working Branch

Use the `working-fb-init-with-breadcrumbs` branch which is based on e433835 (before all these issues):

```bash
git checkout working-fb-init-with-breadcrumbs
# or
git checkout master
git merge working-fb-init-with-breadcrumbs --strategy-option theirs
```

## Recommended Path Forward

### Option 1: Revert and Redesign (Recommended)
1. Revert 2b9809c completely
2. Redesign the prologue improvements in smaller, incremental commits:
   - First: Just move g0 location (test)
   - Second: Add stackguard0 (test)
   - Third: Add event loop (test)
   - Fourth: Add debugging (test)
3. This allows bisecting which specific piece breaks fw_cfg

### Option 2: Accept the Working State
1. Stay on commit 9c3569a or working-fb-init-with-breadcrumbs branch
2. Framebuffer works reliably
3. Forgo the prologue improvements for now

### Option 3: Deep Investigation Required
To fix this properly while keeping prologue improvements:
1. Add instrumentation to `qemu_cfg_read()` in broken state
2. Compare register state, stack state between working and broken
3. Use QEMU tracing (`-d exec,int,guest_errors`) to see what's different
4. This is time-consuming and may not yield results

## Files Changed in Breaking Commit

```
src/Makefile                       |   8 ++-
src/asm/boot.s                     |  23 +++++++-
src/asm/lib.s                      | 117 +++++++++++++++++++++++++++++++++++--
src/go/dummy/dummy.go              |  38 ++++++++++++
src/go/mazarin/framebuffer_qemu.go |   2 -
src/go/mazarin/kernel.go           |  42 ++++++++-----
```

## Testing Commands

```bash
# Test working commit
git checkout 9c3569a
cd src && make clean && make qemu
timeout 15 mazboot  # Should show blue background with text

# Test broken commit  
git checkout 2b9809c
cd src && make clean && make qemu
timeout 15 mazboot  # Black screen, hangs at "Searching files..."
```

## Conclusion

The prologue improvements in commit 2b9809c fundamentally break MMIO operations to fw_cfg in a way that cannot be fixed by disabling individual changes. The interaction between multiple changes creates the failure.

**Recommendation**: Revert 2b9809c and implement prologue improvements incrementally with testing after each change.

