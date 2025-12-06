# fw_cfg DMA vs Traditional Interface: Lessons Learned

## Overview

This document summarizes what we learned about using QEMU's fw_cfg (firmware configuration) interface, specifically comparing the DMA (Direct Memory Access) and Traditional interfaces. Both interfaces are now working correctly in our kernel.

## Key Discovery: Endianness Handling

### The Critical Insight

**All `Set*` methods on big-endian structures already handle byte order conversion internally.**

When working with structures that need to be stored in big-endian byte order (like `RAMFBCfg` and `FWCfgDmaAccess`), the `Set*` methods automatically convert native little-endian values to big-endian byte order when storing them in the internal byte array.

### What This Means

**❌ WRONG:**
```go
ramfbCfg.SetWidth(swap32(fbWidth))  // Double-swap! Wrong!
access.SetControl(swap32(control))  // Double-swap! Wrong!
```

**✅ CORRECT:**
```go
ramfbCfg.SetWidth(fbWidth)   // SetWidth handles BE conversion
access.SetControl(control)   // SetControl handles BE conversion
```

The `Set*` methods expect native values and handle the big-endian conversion internally. Passing pre-swapped values causes a double-swap, resulting in incorrect byte order.

## Structure Design Pattern

### Big-Endian Structures

Both `RAMFBCfg` and `FWCfgDmaAccess` follow the same pattern:

1. **Internal Storage**: `[N]byte` array that stores data in big-endian byte order
2. **Get Methods**: Read bytes and convert from big-endian to native format
3. **Set Methods**: Convert native values to big-endian and store in byte array

Example from `RAMFBCfg`:
```go
type RAMFBCfg struct {
    data [28]byte  // Stores data in big-endian byte order
}

func (r *RAMFBCfg) SetWidth(val uint32) {
    // Convert native val to big-endian bytes
    r.data[16] = byte(val >> 24)  // MSB
    r.data[17] = byte(val >> 16)
    r.data[18] = byte(val >> 8)
    r.data[19] = byte(val)        // LSB
}

func (r *RAMFBCfg) Width() uint32 {
    // Convert big-endian bytes to native uint32
    return uint32(r.data[16])<<24 | uint32(r.data[17])<<16 |
           uint32(r.data[18])<<8 | uint32(r.data[19])
}
```

## Traditional Interface

### How It Works

1. Write selector (2 bytes, big-endian) to `FW_CFG_SELECTOR_ADDR`
2. Read/write data (4 bytes at a time) from/to `FW_CFG_DATA_ADDR`

### Endianness Considerations

When writing data via the traditional interface:
- The config structure (`RAMFBCfg`) stores bytes in big-endian order
- `mmio_write()` on a little-endian machine writes 32-bit values in little-endian byte order
- To ensure QEMU receives bytes in big-endian order, we must arrange the bytes correctly

**Solution**: Read bytes from the big-endian structure and assemble them in reverse order for the little-endian write:

```go
// Read 4 bytes from big-endian structure (MSB first)
b0 := cfg.data[i]     // MSB
b1 := cfg.data[i+1]
b2 := cfg.data[i+2]
b3 := cfg.data[i+3]   // LSB

// Assemble for little-endian write (LSB first)
// When written as LE, bytes appear as: b0, b1, b2, b3 (correct BE order)
val := uint32(b0) | (uint32(b1) << 8) | (uint32(b2) << 16) | (uint32(b3) << 24)
mmio_write(FW_CFG_DATA_ADDR, val)
```

### When to Use Traditional Interface

- **File directory reads**: More reliable for multiple consecutive reads
- **Fallback**: When DMA fails or is unavailable
- **Simple operations**: Single reads/writes that don't need DMA performance

## DMA Interface

### How It Works

1. Create a `FWCfgDmaAccess` structure on the stack
2. Set control, length, and address fields (using `Set*` methods - no swapping!)
3. Write the structure's address (swapped) to `FW_CFG_DMA_ADDR`
4. Wait for QEMU to process the DMA transfer
5. Check control field for completion/error

### Endianness Considerations

**DMA Structure Initialization:**
```go
var access FWCfgDmaAccess
access.SetControl(control)   // No swap32() - SetControl handles it!
access.SetLength(length)    // No swap32() - SetLength handles it!
access.SetAddress(addr)      // No swap64() - SetAddress handles it!
```

**DMA Register Write:**
```go
// The structure address must be swapped when written to DMA register
// because QEMU's DMA region is DEVICE_BIG_ENDIAN
accessAddr := uintptr(unsafe.Pointer(&access))
accessAddrSwapped := swap64(uint64(accessAddr))
mmio_write64(FW_CFG_DMA_ADDR, accessAddrSwapped)
```

**Why the address needs swapping:**
- QEMU's DMA MemoryRegion has `DEVICE_BIG_ENDIAN` set
- When we write a 64-bit address, QEMU byte-swaps it
- We pre-swap so QEMU's swap results in the correct address

### Standard DMA Functions

We provide clean APIs that hide the complexity:

```go
// Read from fw_cfg entry using DMA
fw_cfg_dma_read(selector, buf, length)

// Write to fw_cfg entry using DMA
fw_cfg_dma_write(selector, buf, length)
```

These functions handle all the endianness complexity internally.

### When to Use DMA Interface

- **Large transfers**: Better performance for multi-byte operations
- **Primary method**: Preferred for writes (like ramfb config)
- **Single operations**: Works well for one-shot transfers

### Known Limitations

- **Multiple consecutive reads**: May have issues (use traditional for file directory)
- **Initial file discovery**: Traditional interface more reliable

## Practical Example: RAMFB Configuration

### Creating the Config Structure

```go
var ramfbCfg RAMFBCfg

// Set values using native types - Set* methods handle BE conversion
ramfbCfg.SetAddr(uint64(fbAddr))
ramfbCfg.SetFourCC(0x34325258)  // 'XR24' = XRGB8888
ramfbCfg.SetFlags(0)
ramfbCfg.SetWidth(640)
ramfbCfg.SetHeight(480)
ramfbCfg.SetStride(640 * 4)
```

### Writing via DMA (Preferred)

```go
// Use standard DMA write function
fw_cfg_dma_write(ramfbSelector, unsafe.Pointer(&ramfbCfg), 28)
```

### Writing via Traditional (Fallback)

```go
// Select entry
mmio_write16(FW_CFG_SELECTOR_ADDR, swap16(uint16(selector)))

// Write 28 bytes, handling byte order correctly
for i := 0; i < 28; i += 4 {
    // Read bytes from BE structure and arrange for LE write
    b0 := ramfbCfg.data[i]
    b1 := ramfbCfg.data[i+1]
    b2 := ramfbCfg.data[i+2]
    b3 := ramfbCfg.data[i+3]
    val := uint32(b0) | (uint32(b1) << 8) | (uint32(b2) << 16) | (uint32(b3) << 24)
    mmio_write(FW_CFG_DATA_ADDR, val)
}
```

## Summary of Rules

1. **Never swap values before calling `Set*` methods** - they handle conversion internally
2. **Always swap structure addresses** when writing to DMA register (due to `DEVICE_BIG_ENDIAN`)
3. **Use standard functions** (`fw_cfg_dma_read`, `fw_cfg_dma_write`) when possible
4. **Traditional interface** for file directory operations (more reliable)
5. **DMA interface** for large transfers and writes (better performance)

## Related Documents

- `DMA-INVESTIGATION-COMPLETE.md`: Detailed investigation of DMA issues
- `ramfb_qemu.go`: Implementation of both interfaces
