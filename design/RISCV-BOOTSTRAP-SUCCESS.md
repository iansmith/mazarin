# RISC-V Bootstrap - SUCCESS

**Date**: 2026-02-11
**Status**: ✅ WORKING - Bootstrap executes, UART output confirmed

## Summary

Successfully implemented RISC-V bootstrap for diplomat bootloader using `-bios none` approach. The trampoline executes and writes to UART, producing 1.1MB+ of '!' characters.

## The Bug

The `fix-go-elf` tool was calculating the entry point incorrectly after relocating segments:

```
Original:  e_entry = 0x64b30 (vaddr before relocation)
Segment:   vaddr 0x10000 at file offset 0
Target:    Relocate to vaddr 0x80000000 at file offset 0x1000

Wrong calculation:  0x64b30 + 0x7FFF0000 = 0x80054b30
Right calculation:  0x64b30 + 0x7FFF0000 - 0x1000 = 0x80053b30
```

The issue: When p_offset changed from 0 to 0x1000, addresses shifted down by 0x1000 bytes, but the entry point calculation didn't account for this.

## The Fix

Updated `cmd/fix-go-elf/main.go` line 215:

```go
// OLD
newEntry := e_entry + relocOffset

// NEW
newEntry := e_entry + relocOffset - 0x1000
```

This accounts for the p_offset shift when relocating the first segment.

## Test Results

**Binary loading** (`-device loader`):
```bash
qemu-system-riscv64 -bios none \
  -device loader,file=diplomat.bin,addr=0x80000000
```
Result: **1,119,999 bytes of '!'**

**ELF loading** (`-kernel`):
```bash
qemu-system-riscv64 -bios none -kernel diplomat-riscv64.elf
```
Result: **1,097,525 bytes of '!'**

Both approaches work correctly!

## How It Works

1. **Bootstrap stub** (file offset 0x1000, vaddr 0x80000000):
   - Single JAL instruction jumping +0x53b30 bytes

2. **Trampoline** (file offset 0x54b30, vaddr 0x80053b30):
   - From overlay: `runtime-patches/diplomat-linux/trampoline_riscv64.s`
   - Replaces `_rt0_riscv64_linux` in Go runtime
   - Infinite UART loop writing '!' character

3. **Execution flow**:
   - QEMU loads ELF, jumps to entry point (0x80000000)
   - JAL jumps to trampoline (0x80053b30)
   - Trampoline writes '!' to UART at 0x10000000 in infinite loop

## Files Modified

- `cmd/fix-go-elf/main.go`: Fixed entry point calculation (-0x1000 adjustment)
- `diplomat/main/main_riscv64.go`: Updated forward declarations (underscore prefix)
- `diplomat/main/bootstrap_riscv64.s`: Created (though not used yet - overlay works)

## Next Steps

Phase 1 complete! Ready for:

**Phase 2**: UART driver + printString in Go
- Replace infinite UART loop with actual Go code
- Implement UART driver for console output
- Get printString() working

**Phase 3**: Physical memory allocator
- Parse FDT to discover memory regions
- Implement bump allocator for early boot

**Phase 4**: Continue with full boot sequence
- FDT parsing
- VirtIO block device
- FAT32 filesystem
- Load kmazarin kernel
