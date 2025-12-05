# Detailed Code Comparison: Our Code vs Working Code

## Working Code (C)

### Structure Definitions
```c
struct QemuCfgDmaAccess {
    uint32_t control;
    uint32_t length;
    uint64_t address;
} __attribute__((packed));

struct QemuRamFBCfg {
    uint64_t address;
    uint32_t fourcc;
    uint32_t flags;
    uint32_t width;
    uint32_t height;
    uint32_t stride;
} __attribute__((packed));
```

### Key Functions

#### fw_cfg_dma_transfer
```c
void fw_cfg_dma_transfer(volatile void* address, uint32_t length, uint32_t control) {
    volatile struct QemuCfgDmaAccess dma;  // LOCAL variable on stack
    dma.control = BE32(control);
    dma.address = BE64((uint64_t)address);  // address = RAMFBCfg structure address
    dma.length = BE32(length);

    *dma_address = BE64((uint64_t)&dma);    // Write address of DMA struct
    while (BE32(dma.control) & ~0x01);       // Wait: while any bit except error is set
}
```

#### fw_cfg_dma_write
```c
void fw_cfg_dma_write(void* buf, int e, int length) {
    uint32_t control = (e << 16) | 0x08 | 0x10;  // selector in upper 16 bits
    fw_cfg_dma_transfer(buf, length, control);
}
```

#### qemu_ramfb_configure
```c
extern void qemu_ramfb_configure(struct QemuRamFBCfg* cfg) {
    struct FWCfgFile ramfb_file;
    fw_cfg_find_file(&ramfb_file, "etc/ramfb");  // Find file first!
    fw_cfg_dma_write(cfg, ramfb_file.select, sizeof(struct QemuRamFBCfg));
}
```

#### qemu_ramfb_make_cfg
```c
extern void qemu_ramfb_make_cfg(struct QemuRamFBCfg* cfg, void* fb_address, uint32_t fb_width, uint32_t fb_height) {
    cfg->address = BE64((uint64_t)fb_address);
    cfg->fourcc = BE32(FORMAT_XRGB8888);  // 875713112 = 0x34325258
    cfg->width = BE32(fb_width);
    cfg->height = BE32(fb_height);
    cfg->flags = BE32(0);
    cfg->stride = BE32(fb_width * sizeof(uint32_t));  // width * 4
}
```

## Our Code (Go)

### Structure Definitions
```go
type FWCfgDmaAccess struct {
    Control uint32
    Length  uint32
    Address uint64
    _       uint32  // Padding to align to 16 bytes
}

type RAMFBCfg struct {
    Addr   uint64
    FourCC uint32
    Flags  uint32
    Width  uint32
    Height uint32
    Stride uint32
}
```

### Key Differences

#### 1. DMA Structure Location
- **Working**: Local variable on stack (`volatile struct QemuCfgDmaAccess dma;`)
- **Ours**: Global variable (`var dmaAccessGlobal FWCfgDmaAccess`)
- **Impact**: Should work, but working code uses local

#### 2. DMA Structure Padding
- **Working**: `__attribute__((packed))` - no padding, 16 bytes total
- **Ours**: Has `_ uint32` padding field - 20 bytes total
- **Impact**: ⚠️ **CRITICAL DIFFERENCE** - Our structure is 20 bytes, working is 16 bytes!

#### 3. Wait Condition
- **Working**: `while (BE32(dma.control) & ~0x01);` - waits while any bit except error is set
- **Ours**: Checks `if controlBE == 0` then checks error bit separately
- **Impact**: Our wait condition is different - should match working code exactly

#### 4. FourCC Format
- **Working**: `FORMAT_XRGB8888` = `0x34325258` ('XR24')
- **Ours**: Line 97 still shows `0x34324742` ('BG24') - **NOT UPDATED!**
- **Impact**: ⚠️ **CRITICAL BUG** - We're still using BG24 instead of XRGB8888!

#### 5. File Selector
- **Working**: Finds file dynamically: `fw_cfg_find_file(&ramfb_file, "etc/ramfb")`
- **Ours**: Hardcodes `FW_CFG_RAMFB_SELECT = 0x19`
- **Impact**: Should work, but working code finds it dynamically

#### 6. Structure Field Order
- **Working**: `address, fourcc, flags, width, height, stride`
- **Ours**: `Addr, FourCC, Flags, Width, Height, Stride` - same order ✅

## Critical Issues Found

1. **Line 97**: Still using BG24 (`0x34324742`) instead of XRGB8888 (`0x34325258`)
2. **DMA Structure**: Has padding field making it 20 bytes instead of 16 bytes (packed)
3. **Wait Condition**: Not matching working code exactly

## Fixes Needed

1. Change FourCC to XRGB8888: `0x34325258`
2. Remove padding from FWCfgDmaAccess or make it packed
3. Fix wait condition to match: `while (BE32(dma.control) & ~0x01)`
