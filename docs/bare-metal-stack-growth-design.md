# Bare-Metal Stack Growth Design

## How Go Runtime Stack Growth Works (Standard Library)

### 1. Stack Check Prologue

When a function is **not** marked `//go:nosplit`, the compiler inserts a stack check at function entry:

```go
// Pseudo-code of what compiler generates
func someFunction() {
    // Compiler-generated prologue:
    if sp < stackguard0 {
        runtime.morestack()  // Need more stack
    }
    // ... function body
}
```

**Stack Guard**: A value stored in the goroutine structure (`g.stackguard0`) that represents the minimum safe stack pointer. If `sp < stackguard0`, we need more stack.

### 2. `runtime.morestack` Function

When called, `morestack`:

1. **Checks if growth is needed**: Compares current stack usage vs. available space
2. **Allocates new stack**: Calls OS to allocate memory (e.g., `mmap` on Linux)
3. **Calls `newstack`**: Creates new stack structure
4. **Calls `copystack`**: Copies old stack to new stack
5. **Adjusts pointers**: Updates all pointers that point into the old stack
6. **Resumes execution**: Continues on new stack

### 3. `runtime.newstack` Function

Creates a new stack segment:

```go
// Pseudo-code (simplified)
func newstack() {
    oldsize := g.stack.hi - g.stack.lo
    newsize := oldsize * 2  // Double the size
    
    // Allocate new stack (OS call)
    newstack := sysAlloc(newsize, &memstats.stacks_sys)
    
    // Update goroutine structure
    g.stack.lo = newstack
    g.stack.hi = newstack + newsize
    g.stackguard0 = newstack + _StackGuard
}
```

**Key points**:
- Uses `sysAlloc` which calls OS (mmap/sbrk) - **won't work in bare-metal**
- Doubles stack size each time
- Updates goroutine's stack bounds

### 4. `runtime.copystack` Function

Copies stack frames from old to new:

```go
// Pseudo-code (simplified)
func copystack() {
    oldstack := g.old.stack
    newstack := g.stack
    
    // Calculate how much to copy
    framesize := g.sched.sp - oldstack.lo
    
    // Copy stack frames
    memmove(newstack.lo, oldstack.lo, framesize)
    
    // Adjust all pointers in the stack
    adjustpointers(oldstack, newstack, framesize)
    
    // Update stack pointer
    g.sched.sp = newstack.lo + framesize
}
```

**Key points**:
- Uses `memmove` (simple memory copy)
- Must adjust **all pointers** in the stack (complex!)
- Updates saved registers (SP, PC, etc.)

### 5. Stack Shrinking (`runtime.shrinkstack`)

Go aggressively shrinks stacks when they're not needed:

```go
// Pseudo-code (simplified)
func shrinkstack() {
    if stack_usage < stack_size / 4 {
        // Stack is less than 25% used
        newsize := stack_size / 2  // Halve it
        
        // Allocate smaller stack
        newstack := sysAlloc(newsize, ...)
        
        // Copy to smaller stack
        copystack(...)
        
        // Free old stack (OS call)
        sysFree(oldstack, oldsize, ...)  // **Can't do this in bare-metal!**
    }
}
```

**Problem for bare-metal**:
- `sysFree` calls OS to release memory (e.g., `munmap`)
- We **are** the OS - we use `kfree()` instead
- But we can't "give back" pages to an OS that doesn't exist

## Simplified Bare-Metal Design

### Design Principles

1. **No OS calls**: Use `kmalloc()`/`kfree()` instead of `sysAlloc`/`sysFree`
2. **No aggressive shrinking**: Keep allocated stacks (just mark as free for reuse)
3. **Simple pointer adjustment**: Since we're single-threaded, we can simplify
4. **Contiguous stacks**: Don't need split stacks (we have 512MB available)

### Implementation Plan

#### 1. Stack Structure

```go
// stack.go
type stack struct {
    lo      uintptr  // Low address (bottom of stack)
    hi      uintptr  // High address (top of stack)
    guard0  uintptr  // Stack guard (lo + _StackGuard)
}

var kernelStack stack  // Single kernel stack (no goroutines)
```

#### 2. Stack Check Prologue

We need to provide a `runtime.morestack` function that Go compiler will call:

```go
// runtime_stub.go or new stack.go
//go:linkname morestack runtime.morestack
//go:nosplit
func morestack() {
    // This is called by compiler-generated code
    // We need to:
    // 1. Allocate new larger stack
    // 2. Copy old stack to new
    // 3. Adjust stack pointer
    // 4. Return to continue execution
}
```

**Problem**: `morestack` is called from assembly, and we need to handle the calling convention carefully.

#### 3. Simple Stack Growth

```go
// stack.go
const (
    _StackMin = 2048        // Minimum stack (2KB)
    _StackGuard = 928       // Guard space
    _StackSystem = 0        // No OS overhead
)

var kernelStack stack

//go:nosplit
func growStack() {
    oldsize := kernelStack.hi - kernelStack.lo
    newsize := oldsize * 2  // Double it
    
    // Allocate new stack using kmalloc
    newstack := kmalloc(uint32(newsize))
    if newstack == nil {
        // Out of memory - panic
        panic("stack growth failed")
    }
    
    // Calculate how much of old stack is used
    currentSP := get_stack_pointer()
    usedSize := kernelStack.hi - currentSP
    
    // Copy used portion to new stack
    // New stack grows downward, so:
    //   old: [lo ... currentSP ... hi]
    //   new: [newlo ... newSP ... newhi]
    newSP := pointerToUintptr(newstack) + newsize - usedSize
    memmove(unsafe.Pointer(newSP), unsafe.Pointer(currentSP), usedSize)
    
    // Free old stack (but keep it for now - see below)
    // kfree(unsafe.Pointer(kernelStack.lo))  // Don't free yet!
    
    // Update stack structure
    kernelStack.lo = pointerToUintptr(newstack)
    kernelStack.hi = pointerToUintptr(newstack) + newsize
    kernelStack.guard0 = kernelStack.lo + _StackGuard
    
    // Update stack pointer register
    // This is tricky - we need assembly to update SP
    set_stack_pointer(newSP)
}
```

#### 4. Pointer Adjustment (Simplified)

Since we're single-threaded and have a simple memory layout, we can simplify pointer adjustment:

```go
// For bare-metal, we might be able to skip pointer adjustment if:
// 1. We're identity-mapped (virtual = physical)
// 2. We don't have complex pointer structures on stack
// 3. We're single-threaded

// However, if we do need it:
func adjustStackPointers(oldStack, newStack uintptr, size uintptr) {
    // Scan through stack looking for pointers
    // Adjust any pointer that points into old stack range
    // This is complex and error-prone
    // 
    // For now, we might skip this if we can guarantee
    // no pointers to stack-allocated objects
}
```

**Simplification**: If we avoid storing pointers to stack-allocated objects, we might not need pointer adjustment.

#### 5. Stack Shrinking (Disabled)

```go
// We don't aggressively shrink stacks
// Just keep them allocated
// When function returns, stack pointer moves back up
// But we keep the memory allocated for future growth

// Optional: Track stack usage and only shrink if:
// - Stack usage < 10% of allocated size
// - And we're low on memory
// But for kernel development, this is probably unnecessary
```

### Required Assembly Functions

We'll need assembly helpers:

```assembly
// lib.s or new stack.s

// get_stack_pointer() - already exists
// Returns current SP in x0

// set_stack_pointer(sp uintptr)
// Sets stack pointer to value in x0
.global set_stack_pointer
set_stack_pointer:
    mov sp, x0
    ret

// morestack() - called by compiler
// This is complex because it's called from function prologue
// Need to save all registers, grow stack, restore, return
.global runtime.morestack
runtime.morestack:
    // Save all registers to current stack
    // Call growStack() (Go function)
    // Restore registers from new stack
    // Return to continue execution
    // This is non-trivial!
```

### Challenges

1. **`morestack` calling convention**: Called from compiler-generated code, must preserve all state
2. **Register saving**: Must save all registers before growing stack
3. **Return address**: Must adjust return address to continue on new stack
4. **Pointer adjustment**: May need to adjust pointers in stack frames
5. **Stack pointer update**: Must update SP register atomically

### Simpler Alternative: Pre-allocate Large Stack

Instead of dynamic growth, we could:

1. **Pre-allocate large stack** (e.g., 64MB) at boot
2. **Remove `nosplit` from heavy functions**
3. **Let stack grow naturally** within pre-allocated space
4. **No copying needed** - just use the space

This is much simpler and works well for kernel development:

```go
// At boot time:
var kernelStackSpace [64 * 1024 * 1024]byte  // 64MB static allocation

func initKernelStack() {
    kernelStack.lo = pointerToUintptr(&kernelStackSpace[0])
    kernelStack.hi = kernelStack.lo + uintptr(len(kernelStackSpace))
    kernelStack.guard0 = kernelStack.lo + _StackGuard
    
    // Set initial stack pointer to top
    set_stack_pointer(kernelStack.hi)
}
```

**Pros**:
- No dynamic allocation
- No stack copying
- No pointer adjustment
- Simple and fast

**Cons**:
- Uses 64MB even if not needed
- But we have 512MB allocated, so this is fine

## Recommendation

**For kernel development**: Use **pre-allocated large stack** approach:

1. Allocate 64MB stack statically at boot
2. Remove `//go:nosplit` from functions that need more stack
3. Let Go compiler generate stack checks (which will always pass)
4. No need to implement `morestack` - it will never be called if stack is large enough

This is the simplest approach and works perfectly for your use case.
