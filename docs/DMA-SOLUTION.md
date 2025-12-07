# fw_cfg DMA Solution for QEMU AArch64 virt

## The Problem

QEMU's fw_cfg DMA MemoryRegion is configured with `DEVICE_BIG_ENDIAN`. This causes QEMU to automatically byte-swap ALL values written by little-endian guests (AArch64).

When we wrote address `0x40126C00`, QEMU byte-swapped it to `0x006C124000000000`, causing the DMA handler to read from the wrong memory location.

## The Solution

**Write byte-swapped values** that become correct after QEMU's automatic swap:

```go
// Create LOCAL DMA structure on stack (matching working RISC-V example)
var access FWCfgDmaAccess
access.SetControl(swap32(control))      // Store big-endian
access.SetLength(swap32(length))        // Store big-endian  
access.SetAddress(swap64(uint64(uintptr(dataAddr))))  // Store big-endian

// Write swapped struct address to DMA register
// QEMU will byte-swap it back to get correct address
accessAddr := uintptr(unsafe.Pointer(&access))
accessAddrSwapped := swap64(uint64(accessAddr))
mmio_write64(FW_CFG_DMA_ADDR, accessAddrSwapped)
```

## Key Insights

1. **DEVICE_BIG_ENDIAN**: QEMU's MemoryRegion endianness setting causes automatic byte-swapping
2. **Local struct**: Use stack-allocated struct (like working example), not global BSS
3. **Pre-swap address**: Write `swap64(struct_address)` so it becomes correct after QEMU's swap
4. **Structure fields**: Already stored in big-endian, no change needed

## Verification

Built debug QEMU with fprintf statements in fw_cfg_dma_mem_write handler:
- Handler IS called with our writes
- Correct address received after byte-swap: `0x5ffffdb0`
- DMA structure read successfully
- Data transfers complete successfully

## Working RISC-V Example

The working RISC-V code does exactly the same thing:
```c
QemuCfgDmaAccess access = {
    .control = __builtin_bswap32(control),
    .length = __builtin_bswap32(length),
    .address = __builtin_bswap64((uint64_t)address)
};
mmio_write_bsw64(BASE_ADDR_ADDR, (uint64_t)&access);
```

RISC-V is also little-endian, so it faces the same DEVICE_BIG_ENDIAN byte-swapping.

## Status

✅ **DMA is fully functional on AArch64 virt**

The mystery is solved - we needed to account for QEMU's automatic byte-swapping.

