# DTB Memory Overlap Fix

## Problem

When running `runqemu-virt-vga`, QEMU reported a memory overlap error:

```
The following two regions overlap (in the cpu-memory-0 address space):
  /mnt/builtin/kernel.elf ELF program header segment 1 (addresses 0x0000000040000000 - 0x000000004003b720)
  dtb (addresses 0x0000000040000000 - 0x0000000040100000)
```

## Root Cause

QEMU's `virt` machine type places its Device Tree Blob (DTB) at address `0x40000000` in RAM. The DTB occupies 1MB of space (`0x40000000 - 0x40100000`).

Our kernel's linker script was placing the BSS section at `0x40000000`, which directly overlapped with the DTB region. This caused QEMU to refuse to load the kernel.

## Solution

**Changed BSS start address from `0x40000000` to `0x40100000`** (immediately after the DTB region).

### Files Modified

1. **src/linker.ld** - Updated BSS section placement:
```ld
/* Before: */
. = 0x40000000;
__bss_start = .;

/* After: */
. = 0x40100000;
__bss_start = .;
```

2. **src/asm/boot.s** - Updated comments to reflect DTB region:
```asm
// QEMU virt machine memory layout:
// - 0x00000000-0x08000000: Flash/ROM (kernel loaded at 0x200000)
// - 0x09000000-0x09010000: UART (PL011)
// - 0x40000000-0x40100000: DTB (QEMU device tree blob, 1MB)
// - 0x40100000-end:        RAM (actual writable memory)
```

3. **src/go/mazarin/kernel.go** - Updated memory layout comments

## New Memory Layout

```
Address           Description
==============================================
0x00000000        ROM region start
0x00200000        Kernel code loaded here
0x08000000        ROM region end
0x09000000        UART (PL011)
0x30000000        PCI ECAM (config space)
0x40000000        ┌─ DTB start (1MB, QEMU's device tree)
0x40100000        ├─ BSS section start (kernel writable data)
0x4013b720        ├─ BSS section end (approximate, ~237KB)
                  │  ... free space ...
0x40400000        ├─ Stack pointer (grows downward)
                  │  ... stack space (1MB) ...
0x40500000        └─ Heap start (grows upward, 1MB allocated)
```

## Verification

After the fix, the kernel should load without memory overlap errors:

```bash
# Build and test
cd src
make clean
make kernel-qemu.elf
make push-qemu

# Run with VNC
source /Users/iansmith/mazzy/enable-mazzy
runqemu-virt-vga

# Expected: No overlap errors, kernel boots normally
```

## Why This Matters

### Device Tree Blob (DTB)

The DTB is a data structure that describes hardware to the operating system:
- QEMU generates it automatically
- Contains information about:
  - Available RAM
  - Device addresses (UART, PCI, etc.)
  - CPU configuration
  - Interrupt controllers
  
### Bare-Metal Kernels

For bare-metal kernels (like Mazarin), we:
- **Don't parse the DTB** (we have hardcoded device addresses)
- **Must avoid overwriting it** (QEMU places it in a fixed location)
- **Reserve its memory space** (0x40000000 - 0x40100000)

### Linux vs Bare-Metal

- **Linux kernels**: Parse the DTB to discover hardware
- **Bare-metal kernels**: Use fixed addresses, but must avoid DTB region

## Lessons Learned

1. **QEMU virt machine has reserved regions** - DTB at 0x40000000 is mandatory
2. **Check QEMU documentation** - Memory maps vary by machine type
3. **Linker scripts must account for reserved regions** - Not just our kernel data
4. **Stack and heap placement matters** - Must fit between BSS and stack

## Related Documentation

- `docs/qemu-virt-memory-layout.md` - Complete memory map
- `src/linker.ld` - Linker script with memory layout
- `src/asm/boot.s` - Boot code with memory initialization

## Testing Checklist

- [x] BSS section moved to 0x40100000
- [x] Comments updated in all files
- [x] Kernel builds without errors
- [x] Kernel loads without overlap errors
- [ ] UART output works correctly
- [ ] Framebuffer initialization succeeds
- [ ] VNC connection works
- [ ] Memory allocation (heap) works
- [ ] No crashes or unexpected behavior

## Future Considerations

### Raspberry Pi 4 Real Hardware

On real Raspberry Pi 4 hardware:
- RAM starts at 0x00000000 (no DTB region reservation)
- BSS can be placed at lower addresses
- Different linker script needed for real hardware
- See: `src/linker.ld` comments for RPI4 vs QEMU differences

### Other QEMU Machine Types

Different QEMU machine types have different DTB placements:
- `virt`: DTB at 0x40000000 (this machine type)
- `raspi4b`: Different memory layout, may have different DTB location
- Always check QEMU documentation for your machine type

## Conclusion

The fix is straightforward: **move BSS to 0x40100000 to avoid QEMU's DTB region at 0x40000000**.

This allows the kernel to coexist with QEMU's device tree while maintaining proper memory separation.





