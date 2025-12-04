# Heap Allocator Implementation Summary

## Overview

Successfully implemented the dynamic memory allocator from the tutorial (https://jsandler18.github.io/tutorial/dyn-mem.html) in Go, using:
- Go's `unsafe` package for pointer arithmetic
- Assembly `bzero` function for memory zeroing
- Best-fit allocation algorithm with segmentation and coalescing

## Files Created/Modified

### 1. `src/asm/lib.s` - Added `bzero` assembly function
- Zeroes memory using byte-by-byte writes
- Signature: `bzero(void *ptr, uint32_t size)`
- Uses AArch64 calling convention (x0 = pointer, w1 = size)

### 2. `src/go/mazarin/kernel.go` - Added `bzero` link
- Linked the assembly `bzero` function using `//go:linkname`
- Uses `unsafe` package for pointer operations

### 3. `src/go/mazarin/heap.go` - Complete heap allocator implementation
- **Constants**: `PAGE_SIZE`, `KERNEL_HEAP_SIZE` (1 MB), `HEAP_ALIGNMENT` (16 bytes)
- **Structure**: `heapSegment` - doubly-linked list node with allocation metadata
- **Functions**:
  - `heapInit(heapStart uintptr)` - Initialize heap at given address
  - `kmalloc(size uint32) unsafe.Pointer` - Allocate memory (best-fit algorithm)
  - `kfree(ptr unsafe.Pointer)` - Free memory (with coalescing)
  - `memInit()` - Initialize heap using `__end` linker symbol

## Implementation Details

### Heap Segment Structure
```go
type heapSegment struct {
    next        *heapSegment // Doubly-linked list
    prev        *heapSegment
    isAllocated uint32       // 0 = free, 1 = allocated
    segmentSize uint32       // Total size including header
}
```

### Key Algorithms

1. **Best-Fit Allocation**: Finds the smallest free segment that fits the request
2. **Segmentation**: Splits large segments when allocating smaller blocks (if diff > 2*header_size)
3. **Coalescing**: Merges adjacent free segments when freeing memory
4. **16-byte Alignment**: All allocations are aligned to 16-byte boundaries

### Pointer Arithmetic

All pointer arithmetic uses Go's `unsafe` package:
- `unsafe.Pointer(uintptr(...))` for casting
- `uintptr` for address calculations
- Proper handling of pointer-to-data vs pointer-to-header

## Usage

### Initialization

Call `memInit()` early in kernel initialization (after basic setup, before first allocation):

```go
memInit() // Initializes 1 MB heap starting after __end
```

**Note**: Currently starts heap right after `__end`. When page management is implemented, this should start after the page metadata array.

### Allocation

```go
ptr := kmalloc(256) // Allocate 256 bytes
if ptr == nil {
    // Out of memory
}
// Use ptr...
```

### Freeing

```go
kfree(ptr) // Free previously allocated memory
```

## Integration with Page Management

Currently, the heap starts immediately after `__end`. According to the tutorial, it should start after the page metadata array. When page management is implemented:

1. Reserve kernel pages (from `0x200000` to `__end`)
2. Reserve heap pages (1 MB for heap)
3. Initialize page metadata array
4. Call `heapInit()` with address after page metadata array

The `memInit()` function has a TODO comment indicating this needs to be updated.

## Compliance with Project Rules

✅ **All functions use `//go:nosplit`** - Required for bare-metal kernel  
✅ **No CGO** - Pure Go + Assembly  
✅ **Uses `unsafe` package** - As requested by user  
✅ **Assembly helper for bzero** - As requested by user  
✅ **Linker symbols** - Uses `//go:linkname` to access `__end`  
✅ **32-bit sizes** - Uses `uint32` for segment sizes (matches C tutorial)  
✅ **64-bit pointers** - Uses `uintptr` for addresses (AArch64)

## Testing Recommendations

1. Test basic allocation/free cycle
2. Test allocation failure (out of memory)
3. Test coalescing (allocate multiple blocks, free some, verify merging)
4. Test segmentation (allocate small block from large segment, verify split)
5. Test alignment (verify all allocations are 16-byte aligned)

## Next Steps

1. ✅ Heap allocator implementation - **COMPLETE**
2. ⏳ Implement page management system
3. ⏳ Integrate heap initialization with page management
4. ⏳ Add heap allocator tests
5. ⏳ Integrate `memInit()` call into kernel startup sequence

## References

- Tutorial: https://jsandler18.github.io/tutorial/dyn-mem.html
- Analysis Document: `docs/dynamic-memory-analysis.md`

