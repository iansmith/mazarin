# Memory Layout for 512MB Kernel Space

## Current Layout (After Changes)

**QEMU virt machine with 1GB RAM:**
- `0x00000000 - 0x08000000`: Flash/ROM (kernel code loaded here)
- `0x09000000 - 0x09010000`: UART PL011
- `0x40000000 - 0x40100000`: DTB (QEMU device tree blob, 1MB)
- `0x40100000 - 0x60000000`: **Kernel RAM (512MB allocated)**

### Within Kernel RAM (512MB):

1. **BSS Section**: `0x40100000 - 0x401xxxxx` (ends at `__end`)
   - Uninitialized global variables
   - Runtime structures (g0, m0, write barrier buffers)
   - Static allocations (virtioGPUFramebuffer, etc.)

2. **Page Metadata Array**: `__end - __end + (numPages * sizeof(Page))`
   - Page management metadata
   - Size depends on total memory

3. **Heap**: After page metadata, before stack
   - Should start: `__end + pageArraySize` (aligned to 16 bytes)
   - Should end: `0x40400000` (before stack region)
   - Current size: 1MB (too small - should be larger)

4. **Stack**: `0x40400000 - 0x60000000` (grows downward from `0x60000000`)
   - Initial SP: `0x60000000` (set in boot.s)
   - Stack bottom: `0x40400000` (4MB into RAM)
   - Available: ~508MB

## Issues Found

1. **kernel.go** hardcodes heap at `0x40500000` - **WRONG**
   - This is in the stack region!
   - Should use `memInit()` which calculates from `__end`

2. **memInit()** is defined but **never called**
   - kernel.go does manual heap init instead
   - Should call `memInit(0)` to properly initialize

3. **ramfb_qemu.go** uses `0x50000000` for framebuffer - **OUTSIDE kernel region**
   - `0x50000000` is outside our 512MB (ends at `0x60000000`)
   - Should use heap allocation or static allocation

4. **pci_qemu.go** uses `0x50000000` for bochs-display - **OUTSIDE kernel region**
   - Same issue as ramfb

5. **Heap size is only 1MB** - too small for development
   - With 512MB available, we can make it much larger
   - Should be at least 64MB or more

## Corrected Layout

```
0x40100000: BSS start (after DTB)
   ...
   __end: BSS end (varies)
   __end + pageArraySize: Heap start
   ...
0x40200000: Heap region (could be 64MB+)
   ...
0x40400000: Stack bottom (heap ends here)
   ...
0x60000000: Stack top (initial SP, grows downward)
```

## Fixes Needed

1. Remove hardcoded heap address in kernel.go
2. Call `memInit(0)` to properly initialize heap
3. Increase `KERNEL_HEAP_SIZE` to 64MB or more
4. Fix framebuffer addresses to use heap or be within kernel region
5. Update comments to reflect new layout
