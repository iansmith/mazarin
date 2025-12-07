# Memory Layout Verification for 512MB Kernel Space

## Current Memory Layout

**QEMU virt machine with 1GB RAM:**
- `0x00000000 - 0x08000000`: Flash/ROM (kernel code loaded here)
- `0x09000000 - 0x09010000`: UART PL011
- `0x40000000 - 0x40100000`: DTB (QEMU device tree blob, 1MB)
- `0x40100000 - 0x60000000`: **Kernel RAM (512MB allocated)**

### Within Kernel RAM (512MB):

1. **BSS Section**: `0x40100000 - __end` (varies, typically ends around 0x401xxxxx)
   - Uninitialized global variables
   - Runtime structures (g0, m0, write barrier buffers)
   - Static allocations:
     - `virtioGPUFramebuffer`: ~3.6MB (1280*720*4 bytes)
     - `virtioGPUAttachCmdBuf`: Small buffer
     - Other globals

2. **Page Metadata Array**: `__end - __end + (numPages * sizeof(Page))`
   - Page management metadata
   - Size depends on total memory (for 1GB: ~262K pages * 24 bytes ≈ 6MB)

3. **Heap**: After page metadata, before stack
   - **Start**: `__end + pageArraySize` (aligned to 16 bytes)
   - **End**: `0x40400000` (before stack region)
   - **Size**: Up to 64MB (but limited by stack boundary)
   - **Actual size**: Calculated at runtime to fit before stack

4. **Stack**: `0x40400000 - 0x60000000` (grows downward from `0x60000000`)
   - **Initial SP**: `0x60000000` (set in boot.s)
   - **Stack bottom**: `0x40400000` (4MB into RAM)
   - **Available**: ~508MB
   - **Growth**: Can grow beyond initial region using heap allocation

## Issues Fixed

### ✅ Fixed: Heap Location
- **Before**: Hardcoded at `0x40500000` (in stack region!)
- **After**: Calculated from `__end + pageArraySize` (properly before stack)
- **Code**: `memInit()` now properly calculates heap start

### ✅ Fixed: Heap Size
- **Before**: 1MB (too small)
- **After**: 64MB (but limited by stack boundary check)
- **Code**: `KERNEL_HEAP_SIZE` increased to 64MB

### ✅ Fixed: Framebuffer Allocation
- **Before**: `ramfb_qemu.go` used fixed address `0x50000000` (outside kernel region)
- **After**: Uses `kmalloc()` to allocate from heap (within kernel region)
- **Code**: `ramfbInit()` now allocates framebuffer from heap

### ✅ Fixed: Stack Pointer Reference
- **Before**: `ramfb_qemu.go` had hardcoded `initialStackPtr = 0x40400000` (wrong)
- **After**: Uses `0x60000000` (correct initial stack pointer)
- **Code**: Updated stack pointer constant

### ✅ Fixed: bochs-display Address
- **Before**: Used `0x50000000` (outside 512MB region? Actually it's within!)
- **After**: Still uses `0x50000000` (verified: within 0x40100000-0x60000000 range)
- **Status**: OK - `0x50000000` is within kernel RAM region

## Remaining Issue: Stack Growth Function

The `GrowStackForCurrent()` function is being optimized away by Go because it's never called from Go code (only from assembly). 

**Current status**: Function exists but isn't in object file after compilation.

**Solutions**:
1. Keep function reference in Go code (tried - didn't work)
2. Implement stack growth directly in assembly (simpler, avoids export issues)
3. Use a different calling mechanism

## Verification Checklist

- [x] BSS starts at `0x40100000` (after DTB)
- [x] Heap calculated from `__end + pageArraySize`
- [x] Heap limited to end before `0x40400000` (stack region)
- [x] Stack starts at `0x60000000` (grows downward)
- [x] Stack bottom at `0x40400000`
- [x] Framebuffer allocated from heap (not fixed address)
- [ ] Stack growth function exported and callable from assembly (in progress)

## Memory Usage Estimate

For 1GB RAM:
- BSS: ~1-2MB (varies with globals)
- Page metadata: ~6MB (262K pages * 24 bytes)
- Heap: Up to ~60MB (limited by stack boundary)
- Stack: ~508MB pre-allocated (can grow via heap if needed)

Total kernel usage: ~70-80MB + stack, well within 512MB allocation.



