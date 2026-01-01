# MMU Memory Protection and Initialization Order

## Summary

This document explains the memory protection requirements and initialization order for MMU, framebuffer, and UART ring buffer.

## 1. Framebuffer Memory Protection

### Memory Source
- **ramfb**: Allocated via `kmalloc()` (heap RAM)
- **bochs-display**: Fixed address `0x50000000` (also in RAM)
- Both are in bootloader RAM region (`0x40100000-0x60000000`) which is already mapped

### Protection Strategy
**CRITICAL**: Framebuffer memory allocated via `kmalloc()` must **NEVER** be freed.

- The framebuffer is a permanent resource for the lifetime of the system
- Calling `kfree()` on framebuffer memory would cause:
  - Display corruption
  - System instability
  - Potential reuse of memory for other allocations

### Implementation
- Framebuffer memory is allocated once during initialization
- It is stored in the global `fbinfo.Buf` variable
- **Never call `kfree()` on `fbinfo.Buf`**
- The heap allocator does not have a "reserved" flag, so protection is by convention

### Why Initialize After MMU?
- With identity mapping, framebuffer works before MMU
- However, initializing **after MMU** ensures:
  - Virtual addresses work correctly
  - Consistency with future non-identity mappings
  - Proper memory attribute handling (cacheable vs device)

## 2. UART Ring Buffer

### Memory Source
- Allocated via `kmalloc()` (heap RAM)
- Heap is in bootloader RAM region, already mapped

### Why After MMU?
- The earlier crash was likely due to `store_pointer_nobarrier`, not MMU
- With identity mapping, it could work before MMU
- Initializing after MMU ensures virtual addresses work correctly

### Protection
- UART ring buffer is also permanent (never freed)
- Same protection strategy as framebuffer

## 3. MMU Initialization with Interrupts Disabled

### Best Practice
**YES** - MMU initialization should be done with interrupts **disabled** for atomicity.

### Why?
- MMU initialization modifies critical system registers
- Race conditions could occur if interrupts fire during:
  - Page table setup
  - Register configuration (MAIR, TCR, TTBR0)
  - TLB invalidation
  - MMU enablement

### Implementation
1. **Disable interrupts** before `initMMU()`
2. Initialize page tables
3. Configure MMU registers
4. Enable MMU
5. **Re-enable interrupts** after MMU is fully enabled

### Current Implementation
- `DisableIrqs()` called before MMU init
- `EnableIrqsAsm()` called after MMU enablement
- This ensures atomic MMU initialization

## 4. PCI ECAM Mapping

### Critical Missing Mapping
**PCI ECAM region was NOT mapped in page tables!**

- **Lowmem ECAM**: `0x3F000000 - 0x40000000` (256MB)
- **Highmem ECAM**: `0x4010000000+` (not currently mapped)

### Fix Applied
- Added mapping for lowmem PCI ECAM region
- Mapped as Device-nGnRnE (MMIO)
- Required for PCI configuration space access

### Highmem ECAM
- Currently not mapped (would require 64-bit address handling)
- Code detects and falls back to lowmem if highmem fails
- Can be added later if needed

## Memory Map Summary

### Mapped Regions (After Fix)
1. **ROM**: `0x00000000 - 0x08000000` (Normal, cacheable)
2. **UART**: `0x09000000 - 0x09010000` (Device-nGnRnE)
3. **DTB**: `0x40000000 - 0x40100000` (Device-nGnRnE, read-only)
4. **PCI ECAM**: `0x3F000000 - 0x40000000` (Device-nGnRnE) **[NEW]**
5. **Bootloader RAM**: `0x40100000 - 0x60000000` (Normal, cacheable)
6. **User RAM**: `0x60000000 - 0x61000000` (Normal, cacheable, 16MB test)

### Unmapped Regions (Future)
- **Highmem PCI ECAM**: `0x4010000000+` (if needed)
- **Additional user RAM**: Expand beyond 16MB when needed

## Initialization Order (Recommended)

1. **Early initialization** (before MMU):
   - UART (for debugging)
   - Memory management (heap)
   - Exception handlers

2. **MMU initialization** (interrupts disabled):
   - Disable interrupts
   - Initialize page tables
   - Map all memory regions
   - Enable MMU
   - Re-enable interrupts

3. **Post-MMU initialization** (interrupts enabled):
   - UART ring buffer (uses `kmalloc`, virtual addresses work)
   - Framebuffer (uses `kmalloc` or fixed address, virtual addresses work)
   - Other MMIO devices

## Notes

- All memory allocated via `kmalloc()` for permanent resources (framebuffer, UART ring buffer) should **never** be freed
- The heap allocator does not have explicit "reserved" flags
- Protection is by convention: document and never call `kfree()` on permanent resources
- PCI ECAM mapping is critical for PCI device enumeration and configuration


