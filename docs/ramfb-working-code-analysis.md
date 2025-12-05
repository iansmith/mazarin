# Working RAMFB Code Analysis

## Source
GitHub: https://github.com/SpeedyCraftah/arm-v8a-virt-ramfb
Files: `ramfb.c`, `ramfb.h`

## Key Differences from Our Implementation

### 1. DMA Register Address
**Working code:**
```c
#define fw_cfg_address ((volatile uint8_t*)0x09020000)
#define dma_address ((volatile uint64_t*)(fw_cfg_address + 16))  // 0x09020010
```
**Our code:** `0x9020010` ✅ **MATCHES**

### 2. DMA Transfer Function
**Working code:**
```c
void fw_cfg_dma_transfer(volatile void* address, uint32_t length, uint32_t control) {
    volatile struct QemuCfgDmaAccess dma;  // LOCAL variable, not global!
    dma.control = BE32(control);
    dma.address = BE64((uint64_t)address);
    dma.length = BE32(length);

    *dma_address = BE64((uint64_t)&dma);  // Write address of local struct
    while (BE32(dma.control) & ~0x01);    // Wait: while any bit except error is set
}
```

**Key observations:**
- Uses **local variable** on stack (not global!)
- Control format: `(selector << 16) | 0x08 | 0x10`
- Wait condition: `while (BE32(dma.control) & ~0x01)` - waits until control is 0 OR error bit set
- Converts control to native endian when checking: `BE32(dma.control)`

### 3. Pixel Format
**Working code:**
- Uses `FORMAT_XRGB8888` (32-bit, 4 bytes per pixel)
- Stride: `fb_width * sizeof(uint32_t)` = `width * 4`

**Our code:**
- Uses `BG24` (24-bit, 3 bytes per pixel)
- Stride: `width * 3`

### 4. File Selector
**Working code:**
- Finds ramfb file dynamically: `fw_cfg_find_file(&ramfb_file, "etc/ramfb")`
- Uses `ramfb_file.select` instead of hardcoding 0x19

**Our code:**
- Hardcodes selector: `FW_CFG_RAMFB_SELECT = 0x19`

### 5. Wait Condition
**Working code:**
```c
while (BE32(dma.control) & ~0x01);  // Wait while any bit except error is set
```
This means:
- Continue waiting if control has ANY bits set (except error bit)
- Stop when control is 0 (success) OR error bit is set (failure)

**Our code:**
```go
if controlBE == 0 {  // Check if big-endian value is 0
    return true
}
```
We check the big-endian value directly, which should work, but maybe we need to convert it?

## Critical Differences to Try

1. **Use XRGB8888 format instead of BG24** (32-bit vs 24-bit)
2. **Check wait condition** - maybe we need to convert to native endian when checking?
3. **Try local variable** instead of global (though global should work too)
4. **Verify control value format** - ensure selector is in upper 16 bits correctly

## Next Steps

1. Try XRGB8888 format (4 bytes per pixel) instead of BG24
2. Fix wait condition to match working code
3. Verify control value calculation matches exactly






