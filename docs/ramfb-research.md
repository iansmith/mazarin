# RAMFB Research - Working Examples and Alternatives

## Working Example Repository

**GitHub: SpeedyCraftah/arm-v8a-virt-ramfb**
- URL: https://github.com/SpeedyCraftah/arm-v8a-virt-ramfb
- Provides working C code for ramfb initialization
- Files: `ramfb.h`, `ramfb.c`, `main.cpp`, `_start.asm`
- Demonstrates working fw_cfg DMA implementation

## Key Findings

### 1. fw_cfg DMA Register Details
- **Base address**: `0x9020000`
- **DMA register**: `0x9020010` (base + 0x10 offset) ✅ Matches our code
- **Write method**: Write physical address of `FWCfgDmaAccess` structure to DMA register
- **Format**: Address must be in big-endian format when written

### 2. Working C Code Pattern (from examples)

```c
// Structure must be in accessible memory (global or allocated)
FWCfgDmaAccess dma_access = {
    .control = htonl((selector << 16) | FW_CFG_DMA_CTL_SELECT | FW_CFG_DMA_CTL_WRITE),
    .length = htonl(sizeof(ramfb_cfg)),
    .address = htobe64((uintptr_t)&ramfb_cfg)  // Address of RAMFBCfg structure
};

// Write physical address of dma_access to DMA register
volatile uint64_t *fw_cfg_dma_reg = (uint64_t *)(0x9020010);
*fw_cfg_dma_reg = htobe64((uintptr_t)&dma_access);

// Wait for completion - control field becomes 0 when done
while (dma_access.control != 0) {
    // Check for error bit
    if (dma_access.control & htonl(FW_CFG_DMA_CTL_ERROR)) {
        // Error occurred
        break;
    }
}
```

### 3. Our Current Implementation Issues

1. ✅ We're writing the physical address of `FWCfgDmaAccess` structure (correct)
2. ✅ We're converting to big-endian (correct)
3. ✅ We're using global variable for structure (correct)
4. ❓ Control register not clearing to 0 - transfer not completing
5. ❓ May need to check structure alignment or memory region

### 4. Alternative: DTB Parsing

The Device Tree Blob (DTB) is at `0x40000000` and can be parsed to find framebuffer information:
- DTB location: Start of RAM (`0x40000000`)
- Can use `libfdt` library to parse
- Framebuffer node: `/framebuffer` or `/chosen/framebuffer0`
- Provides framebuffer address, width, height, stride, format

**Pros**: No fw_cfg DMA needed, just parse DTB
**Cons**: Requires DTB parsing library, more complex

### 5. Alternative: bochs-display (Simpler)

- Uses PCI BARs (no fw_cfg needed)
- Framebuffer at BAR0
- MMIO registers at BAR2
- Uses VBE (VESA BIOS Extensions) for mode setting
- **Issue**: Designed for x86, may not work on AArch64

## Recommended Next Steps

### Option 1: Fix ramfb DMA (Best long-term)
1. Check the actual `ramfb.c` from the GitHub repository
2. Compare our implementation line-by-line
3. Verify structure alignment (may need 16-byte alignment)
4. Check if we need memory barriers or cache operations
5. Verify the DMA register write is actually triggering

### Option 2: Try DTB Parsing (Simpler, no DMA)
1. Use `libfdt` or implement simple DTB parser
2. Find framebuffer node in DTB
3. Extract framebuffer address and properties
4. Write directly to framebuffer (no configuration needed)

### Option 3: Verify bochs-display (Already implemented)
1. Check if bochs-display is actually being found
2. Verify PCI BAR addresses are correct
3. Test if framebuffer writes work without VBE initialization

## Resources

1. **Working Example**: https://github.com/SpeedyCraftah/arm-v8a-virt-ramfb
2. **QEMU fw_cfg Spec**: https://www.qemu.org/docs/master/specs/fw_cfg.html
3. **QEMU ramfb Docs**: https://www.qemu.org/docs/master/specs/ramfb.html
4. **DTB Parsing**: QEMU virt machine DTB at `0x40000000`

## Current Status

- ✅ ramfb initialization code written
- ✅ fw_cfg DMA structure setup correct
- ✅ Big-endian conversion implemented
- ❌ DMA transfer not completing (control register stuck)
- ❌ Framebuffer not displaying

## Questions to Investigate

1. Is the `FWCfgDmaAccess` structure properly aligned?
2. Do we need cache coherency operations (dsb, cache flush)?
3. Is the structure address accessible to QEMU's DMA engine?
4. Should we use two 32-bit writes instead of one 64-bit write?
5. Is there a memory barrier needed before/after the write?







