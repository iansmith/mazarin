# Go Runtime Stack Handling Analysis

## Current Situation

Your kernel uses `//go:nosplit` functions extensively, which have a hard limit of **792 bytes** of stack growth. This limit is enforced by the Go compiler/linker to prevent stack overflows.

## How Go Handles Stack Splitting (Non-nosplit Functions)

When a function is **NOT** marked with `//go:nosplit`:

1. **Stack Check Prologue**: The compiler inserts a stack check at function entry
2. **Stack Growth**: If stack space is insufficient, Go calls `runtime.morestack`
3. **Stack Allocation**: `runtime.morestack` allocates a new stack segment
4. **Stack Copying**: The function's frame is copied to the new stack
5. **Resume**: Execution continues on the new stack

### Key Runtime Functions

- `runtime.morestack`: Allocates new stack space
- `runtime.newstack`: Creates a new stack segment
- `runtime.copystack`: Copies stack frames to new stack
- `runtime.stackfree`: Frees old stack segments

### Stack Size Constants

Go runtime defines several stack-related constants:

```go
// From runtime/stack.go (typical values for AArch64)
const (
    _StackMin = 2048        // Minimum stack size (2KB)
    _StackSystem = 0        // OS-specific overhead
    _StackGuard = 928       // Guard space (platform-specific)
    _StackSmall = 128       // Small stack threshold
    _StackLimit = _StackGuard - _StackSystem - _StackSmall  // ~800 bytes
)
```

For `nosplit` functions, `_StackLimit` (typically ~792-800 bytes) is the maximum stack growth allowed.

## Modifying Runtime Constants for Kernel

Since your kernel:
- Has no goroutines (single-threaded)
- Has no GC running
- Has plenty of physical stack space (512MB allocated)
- Doesn't need stack splitting

You can potentially modify the runtime constants to allow larger `nosplit` stacks.

### Approach 1: Patch Runtime Constants at Link Time

You can override runtime constants using linker flags:

```bash
# In Makefile, add to linker flags:
-Wl,--defsym=runtime._StackLimit=0x2000  # 8KB instead of 792 bytes
```

However, this requires knowing the exact symbol names, which may vary by Go version.

### Approach 2: Create Runtime Override File

Create a file that redefines the constants:

```go
// runtime_override.go
package runtime

// Override stack limit for bare-metal kernel
// Since we have 512MB stack and no goroutines, we can use much larger limit
const _StackLimit = 8192  // 8KB instead of ~800 bytes
```

**Problem**: This won't work because:
- Runtime constants are defined in the Go runtime package
- You can't override them from your code
- They're compiled into the runtime library

### Approach 3: Remove `//go:nosplit` Selectively

Instead of modifying runtime, remove `nosplit` from functions that need more stack:

```go
// Remove nosplit from functions that allocate
func virtioGPUSetupFramebuffer(width, height uint32) bool {
    // No //go:nosplit - allows stack growth
    // ... code that might need more stack
}
```

**Pros**:
- Simple - just remove the directive
- Go runtime handles stack growth automatically
- No runtime modifications needed

**Cons**:
- Stack growth has overhead (function prologue checks)
- May not be suitable for interrupt handlers
- Slightly slower function calls

### Approach 4: Use Linker Script to Override Symbols

You can use the linker to override runtime symbols:

```ld
/* In linker.ld */
PROVIDE(runtime._StackLimit = 0x2000);  /* 8KB limit */
```

**Problem**: Symbol names are internal and may not be exported.

## Recommended Solution

**For your kernel development**: **Remove `//go:nosplit` from allocation-heavy functions**

Since you:
- Have 512MB of stack space
- Are single-threaded
- Don't need real-time guarantees
- Want simplicity

**Remove `nosplit` from**:
- `virtioGPUSetupFramebuffer` (already done - uses static allocation)
- Any other functions that call `kmalloc()` or have deep call chains

**Keep `nosplit` on**:
- Interrupt handlers (if you add them)
- Critical path functions that need guaranteed performance
- Functions called from assembly

## Example: Removing nosplit

```go
// Before (nosplit - limited to 792 bytes)
//go:nosplit
func someFunction() {
    // ... code that might overflow
}

// After (allows stack growth)
func someFunction() {
    // ... code that might overflow - Go runtime handles it
}
```

## Stack Growth Overhead

When a non-nosplit function needs more stack:
1. Function prologue checks stack space (~10-20 instructions)
2. If insufficient, calls `runtime.morestack`
3. `morestack` allocates new stack segment
4. Copies function frame to new stack
5. Resumes execution

**Overhead**: ~100-200 cycles per stack growth event (rare in practice)

For kernel development, this overhead is negligible compared to the flexibility gained.

## Conclusion

**Best approach for kernel development**:
1. ✅ Use static allocation (already done for framebuffer)
2. ✅ Remove `//go:nosplit` from functions that need more stack
3. ✅ Keep `nosplit` only on critical/interrupt paths
4. ✅ Let Go runtime handle stack growth automatically

This gives you:
- No runtime modifications needed
- Automatic stack management
- 512MB of available stack space
- Simple, maintainable code


