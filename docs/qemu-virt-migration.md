# QEMU Migration to `virt` Machine Type

## Summary

Updated QEMU configuration to use the `virt` machine type instead of `raspi4b` for better UART compatibility with bare-metal kernels.

## Changes Made

### 1. `docker/runqemu-fb`
- Changed from `-M raspi4b` to `-M virt`
- Added `-cpu cortex-a72` (same CPU as Raspberry Pi 4)
- Added `-m 512M` (512MB RAM allocation)
- Removed `-append` flag (not needed for bare-metal kernels, that's for Linux)

### 2. `docker/Dockerfile`
- Updated default ENTRYPOINT to use `virt` machine type
- Added `-cpu cortex-a72` and `-m 512M` flags
- Removed `-append` flag from default entrypoint

## Why `virt` Machine Type?

1. **UART Compatibility**: The `virt` machine type has a PL011 UART at `0x09000000`, which matches our kernel code (`uart_qemu.go`)
2. **Better Serial Output**: Serial output works reliably with `-serial stdio`
3. **Standard Configuration**: Well-documented and commonly used for bare-metal development
4. **Same CPU**: Uses Cortex-A72, same as Raspberry Pi 4

## Working Configuration

```bash
qemu-system-aarch64 -M virt \
    -cpu cortex-a72 \
    -m 512M \
    -kernel kernel.elf \
    -serial stdio \
    -display none \
    -no-reboot
```

## Test Results

✅ **UART Output Works**: Serial output appears correctly in terminal
✅ **Kernel Executes**: All kernel code runs successfully
✅ **Tests Visible**: All test output is visible (T1, T2, T3, etc.)

## Files Updated

- `docker/runqemu-fb` - Updated to use `virt` machine type
- `docker/Dockerfile` - Updated default entrypoint to use `virt` machine type
- `bin/runqemu-fb` - Automatically updated (symlink to `docker/runqemu-fb`)

## Notes

- `runqemu-debug` and `runqemu-trace` automatically use the new configuration (they use Dockerfile's ENTRYPOINT)
- Other scripts like `runqemu-vga` and `runqemu-host-fb` still use `raspi4b` for specific purposes
- The `virt` machine type is generic and may not match all Raspberry Pi 4 hardware specifics, but works well for development





