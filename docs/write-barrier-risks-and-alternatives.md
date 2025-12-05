# Write Barrier Risks and Alternatives

## What Are Write Barriers?

Write barriers are mechanisms used by Go's garbage collector to track pointer writes. When you write a pointer to memory (especially to global variables), the write barrier ensures the GC can track that pointer so it doesn't get collected while still in use.

In your bare-metal kernel:
- Write barriers are **enabled** by default (set in `boot.s`)
- They're triggered when assigning pointers to global variables
- The write barrier code (`gcWriteBarrier`) buffers pointer writes for GC tracking

## Risks of Disabling Write Barriers

### 1. **Garbage Collection Issues** ⚠️
**Risk Level: HIGH if GC runs, LOW if GC never runs**

- **If GC runs**: The GC won't know about pointers written without barriers, leading to:
  - Premature object collection (use-after-free bugs)
  - Memory corruption
  - Crashes or undefined behavior

- **If GC never runs**: No immediate risk, but:
  - If GC is accidentally triggered later, problems will surface
  - Some Go runtime code may assume barriers are enabled

### 2. **Runtime Assumptions** ⚠️
**Risk Level: MEDIUM**

- Some Go runtime code may assume write barriers are enabled
- Compiler optimizations may rely on barrier behavior
- Future Go versions might have stronger assumptions

### 3. **In Your Specific Case** ✅
**Risk Level: LOW**

Looking at your kernel:
- **GC is never explicitly invoked** - no `runtime.GC()` calls
- **No automatic GC triggers** - bare-metal environment doesn't trigger GC
- **You already have `disableWriteBarrier()` function** - suggests this was considered
- **Write barriers are mostly overhead** - they add function call overhead on every pointer assignment

**Conclusion**: In your bare-metal kernel where GC never runs, disabling write barriers is relatively safe.

## Alternatives to Disabling Write Barriers

### Option 1: Increase Stack Size ✅ **RECOMMENDED**
**Risk: NONE**  
**Effort: LOW**

**What it does**: Allocate more stack space so the write barrier call chain doesn't overflow.

**How to implement**:
1. Modify `linker.ld` to increase stack size
2. Or modify `boot.s` to set a larger initial stack pointer

**Pros**:
- No functional changes to code
- Maintains write barrier safety
- Simple fix

**Cons**:
- Uses more memory (typically 4KB-8KB more)

**Example** (in `linker.ld`):
```ld
.stack : {
    . = ALIGN(16);
    . += 0x4000;  /* Increase from 0x1000 (4KB) to 0x4000 (16KB) */
    stack_top = .;
}
```

### Option 2: Static Framebuffer Allocation ✅ **GOOD ALTERNATIVE**
**Risk: NONE**  
**Effort: MEDIUM**

**What it does**: Pre-allocate the framebuffer as a static/global variable instead of using `kmalloc()`.

**How to implement**:
```go
// In virtio_gpu.go
var virtioGPUFramebuffer [1280 * 720 * 4]byte // Static allocation

func virtioGPUSetupFramebuffer(width, height uint32) bool {
    // Use &virtioGPUFramebuffer[0] instead of kmalloc()
    virtioGPUDevice.Framebuffer = unsafe.Pointer(&virtioGPUFramebuffer[0])
    // ... rest of setup
}
```

**Pros**:
- No heap allocation = no write barriers triggered
- Predictable memory usage
- No stack growth

**Cons**:
- Uses memory even if VirtIO GPU isn't used
- Less flexible (fixed size)

### Option 3: Disable Write Barriers Temporarily ⚠️ **RISKY BUT POSSIBLE**
**Risk: LOW (if GC never runs)**  
**Effort: LOW**

**What it does**: Disable write barriers just for the framebuffer allocation, then re-enable.

**How to implement**:
```go
func virtioGPUSetupFramebuffer(width, height uint32) bool {
    // Temporarily disable write barriers
    oldFlag := readMemory32(0x3582C0)
    writeMemory32(0x3582C0, 0) // Disable
    
    // Allocate framebuffer (no barriers triggered)
    fbSize := width * height * 4
    fbMem := kmalloc(fbSize)
    
    // Re-enable write barriers
    writeMemory32(0x3582C0, oldFlag)
    
    // ... rest of setup
}
```

**Pros**:
- Minimal code changes
- Only disables barriers for specific operation

**Cons**:
- If GC runs during allocation, problems occur
- Race conditions if interrupts are enabled later
- Not thread-safe (though you're single-threaded)

### Option 4: Use `allocPage()` Instead of `kmalloc()` ✅ **GOOD ALTERNATIVE**
**Risk: NONE**  
**Effort: MEDIUM**

**What it does**: Use page allocator instead of heap allocator. Page allocator might have less stack overhead.

**How to implement**:
```go
func virtioGPUSetupFramebuffer(width, height uint32) bool {
    fbSize := width * height * 4
    pagesNeeded := (fbSize + 4095) / 4096 // Round up to pages
    
    // Allocate pages (might have less stack overhead)
    fbMem := allocPage()
    if fbMem == nil {
        return false
    }
    // ... use fbMem
}
```

**Pros**:
- Different code path might avoid write barriers
- Simpler allocation logic

**Cons**:
- May still trigger write barriers if it updates global state
- Less flexible (page-aligned only)

## Recommendation

**Best approach**: **Option 1 (Increase Stack Size)**

1. **Safest**: No functional changes, maintains all safety guarantees
2. **Simplest**: Just modify linker script
3. **Future-proof**: Works even if you add more code later

**Quick fix if needed**: **Option 2 (Static Allocation)**

If you need a quick fix and don't mind using static memory, this avoids the stack issue entirely.

**Avoid if possible**: **Option 3 (Temporarily Disable Barriers)**

Only use if you're certain GC will never run and you understand the risks.

## Current Situation

Your kernel currently:
- ✅ Has write barriers enabled
- ✅ Has infrastructure to disable them (`disableWriteBarrier()`)
- ✅ Never invokes GC explicitly
- ❌ Has stack overflow when allocating large framebuffer via `kmalloc()`

**The stack overflow happens because**:
- `kmalloc()` updates global heap structures
- These updates trigger write barriers
- Write barrier code (`gcWriteBarrier` → `wbBufFlush` → `systemstack`) has deep call chain
- Call chain exceeds 792-byte `nosplit` stack limit

## Implementation: Increase Stack Size

To fix the immediate issue, modify the stack size in `boot.s` or `linker.ld`:

**In `boot.s`** (if stack is set there):
```assembly
// Change from:
mov sp, #0x400000  // 4MB stack

// To:
mov sp, #0x410000  // 4MB + 64KB stack
```

**In `linker.ld`** (if stack is defined there):
```ld
.stack : {
    . = ALIGN(16);
    . += 0x10000;  /* 64KB stack instead of 4KB */
    stack_top = .;
}
```

This should give enough stack space for the write barrier call chain.
