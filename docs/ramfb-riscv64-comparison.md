# RAMFB Implementation Comparison: Our Code vs. Working RISC-V64 Example

## Repository
https://github.com/CityAceE/qemu-ramfb-riscv64-driver

## Key Differences Found

### 1. **Selector Discovery (CRITICAL)**
**Working Example:**
- Calls `qemu_cfg_find_file()` to dynamically search for "etc/ramfb" in the file directory
- Returns the actual selector value (not hardcoded)
- Uses selector 0x19 only as a fallback/constant for the file directory entry

**Our Code:**
- Hardcodes selector `0x19` for `etc/ramfb`
- **This might be wrong if the selector is different in our QEMU version**

### 2. **DMA Structure Creation**
**Working Example:**
```c
QemuCfgDmaAccess access = { 
    .address = __builtin_bswap64((uint64_t)address),  // address of config struct
    .length = __builtin_bswap32(length), 
    .control = __builtin_bswap32(control) 
};
```

**Our Code:**
- Creates structure with fields in big-endian format (matches)
- Sets address to config structure address (matches)

### 3. **DMA Register Write**
**Working Example:**
```c
mmio_write_bsw64(BASE_ADDR_ADDR, (uint64_t)&access);
// mmio_write_bsw64 does: *mmio_w = __builtin_bswap64(val);
```

**Our Code:**
```go
mmio_write64(uintptr(FW_CFG_DMA_ADDR), swap64(uint64(dmaStructAddr)))
```
- We swap the address before writing (matches)

### 4. **Config Structure Creation**
**Working Example:**
```c
struct QemuRAMFBCfg cfg = {
    .addr   = __builtin_bswap64(fb->fb_addr),
    .fourcc = __builtin_bswap32(DRM_FORMAT_XRGB8888),
    .flags  = __builtin_bswap32(0),
    .width  = __builtin_bswap32(fb->fb_width),
    .height = __builtin_bswap32(fb->fb_height),
    .stride = __builtin_bswap32(fb->fb_stride),
};
```

**Our Code:**
- Uses `SetAddr()`, `SetFourCC()`, etc. which store in big-endian format (matches)
- We swap values before calling Set* methods (matches)

### 5. **Completion Check**
**Working Example:**
```c
while(__builtin_bswap32(access.control) & ~QEMU_CFG_DMA_CTL_ERROR) {}
```

**Our Code:**
```go
control := swap32(controlBE)
if (control & 0xFFFFFFFE) == 0 { ... }
```
- Logic matches (wait while any bits except error bit are set)

## Most Likely Issue

**The selector might be wrong!** The working example finds it dynamically. We should implement `qemu_cfg_find_file()` to get the correct selector for "etc/ramfb" instead of hardcoding 0x19.

## Next Steps

1. Implement `qemu_cfg_find_file()` to dynamically find the selector
2. Use the found selector instead of hardcoded 0x19
3. Verify the DMA transfer completes with the correct selector





