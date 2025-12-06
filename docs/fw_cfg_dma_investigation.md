# fw_cfg DMA Investigation on QEMU AArch64 virt Machine

## Problem Statement
QEMU fw_cfg DMA interface does not work on AArch64 virt machine, preventing ramfb device configuration.

## Experiments Conducted

### Structure Format Verification
**Result**: PERFECT - matches working RISC-V example byte-for-byte
```
DMA Structure Bytes: 0A 00 19 00 04 00 00 00 40 FE FF 5F 00 00 00 00
  Control: 0x0A001900 (selector 0x19, READ|SELECT)
  Length:  0x04000000 (4 bytes)
  Address: 0x40FEFF5F00000000 (byte-swapped buffer address)
```

### DMA Availability Checks
- ✅ Feature bitmap bit 1 set (DMA advertised as available)
- ✅ DMA register reads 0x51454D5520434647 ("QEMU CFG")
- ✅ Traditional interface works (can read signature, feature bitmap, file directory)

### DMA Write Experiments
1. **64-bit write** to 0x09020010: No QEMU response
2. **Two 32-bit writes** (MSB to 0x09020010, LSB to 0x09020014): No QEMU response
3. **Native address** (no byte swap): No QEMU response
4. **Byte-swapped address** (big-endian): No QEMU response
5. **Memory barriers** between descriptor fields: No effect
6. **Write buffer flush** (read-back after write): No effect

### DMA Operation Tests
1. **Simple READ** (signature, 4 bytes): FAILED - data stays 0xDEADBEEF
2. **File directory READ** (4 bytes): FAILED - data stays 0xDEADBEEF
3. **WRITE operation**: FAILED - control unchanged

### Memory/Alignment Checks
- DMA structure at 0x40126C00 (BSS section, RAM)
- 16-byte aligned: YES
- 64-byte aligned: YES
- Control field accessible via both pointer and accessor: YES
- Memory region 0x40000000-0x80000000: Valid QEMU RAM

## Key Observations

1. **Control field never updated by QEMU**: Values (0x0000000A, 0x00000018) are exactly what we wrote. QEMU never modifies them.

2. **Zero DMA trace events**: QEMU logs show fw_cfg_select and fw_cfg_read (traditional interface), but zero DMA operations despite `-trace fw_cfg*` enabled.

3. **Writes complete successfully**: No exceptions, crashes, or errors. The mmio_write operations return normally.

4. **Asymmetric behavior**: Can READ from DMA register (get "QEMU CFG"), but WRITES don't trigger handler.

## Conclusion

QEMU's fw_cfg DMA on AArch64 virt machine (tested on QEMU 10.1.2) is non-functional. The DMA handler does not process register writes despite:
- Advertising DMA availability via feature bit
- Providing readable DMA register with correct signature
- Accepting writes without errors

This appears to be either:
1. A bug in QEMU's AArch64 fw_cfg DMA implementation
2. Missing/undocumented initialization requirement
3. Architectural limitation not present in RISC-V implementation

## Workaround

Use traditional interface (selector/data registers) for READ operations. For WRITE operations (required for ramfb), use alternative display devices:
- virtio-gpu-pci (fully supported on AArch64)
- bochs-display (PCI-based, simpler than virtio)
