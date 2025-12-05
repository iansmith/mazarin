# QEMU VGA Build Tags

This document explains how to use build tags to build separate versions of the kernel for real hardware (Raspberry Pi) and QEMU emulation.

## Overview

The kernel now supports two different framebuffer initialization paths:

1. **Real Hardware (Raspberry Pi)**: Uses the Raspberry Pi mailbox interface to communicate with the GPU and allocate the framebuffer
2. **QEMU**: Uses a simple memory-mapped framebuffer allocated in kernel memory

Both implementations share the same API, so the rest of the kernel code (like `gpu.go`) doesn't need to know which implementation is being used.

## Build Tags

Go build tags are used to select which implementation to compile:

- **`!qemu`** (default): Raspberry Pi implementation - uses mailbox interface
- **`qemu`**: QEMU implementation - uses memory-mapped framebuffer

## Building

### For Real Hardware (Default)

```bash
cd src
make
make push
```

This builds `kernel.elf` with the Raspberry Pi mailbox implementation.

### For QEMU

```bash
cd src
make kernel-qemu.elf
make push-qemu
```

This builds `kernel-qemu.elf` with the QEMU memory-mapped framebuffer implementation and copies it to `docker/builtin/kernel.elf`.

## Implementation Details

### Raspberry Pi Implementation (`framebuffer_rpi.go`)

- Uses the property mailbox channel to communicate with the GPU
- Requests framebuffer allocation via mailbox messages
- GPU allocates and returns the framebuffer address
- Follows the Raspberry Pi 4 specification

**Build tag**: `!qemu` (default, no tag needed)

### QEMU Implementation (`framebuffer_qemu.go`)

- Writes directly to QEMU's framebuffer memory at a fixed address (0x10000000)
- Uses fixed dimensions (640x480, 24-bit RGB)
- **Does NOT allocate kernel memory** - uses a "side channel" to QEMU's framebuffer
- Address chosen to avoid conflicts with kernel memory:
  - Kernel: 0x200000 (2MB)
  - Stack: 0x400000 (4MB)
  - Heap: 0x500000 (5MB+)
  - Framebuffer: 0x10000000 (256MB) - safe high address
- Same `gpuPutc`/`gpuPuts` interface as Raspberry Pi implementation
- No mailbox communication needed

**Build tag**: `qemu`

**Important**: The framebuffer address (0x10000000) is a fixed address that should match where QEMU maps the VGA/virtio-gpu framebuffer. For VGA devices on `virt` machine type, the actual framebuffer may be in PCI MMIO space (0x40000000+), so you may need to adjust the address or query PCI config space to find the actual framebuffer location.

## Shared API

Both implementations provide the same API:

- `framebufferInit() int32` - Initialize the framebuffer
- `FramebufferInfo` struct - Global `fbinfo` variable with framebuffer information
- Same constants: `COLORDEPTH`, `BYTES_PER_PIXEL`, `CHAR_WIDTH`, `CHAR_HEIGHT`

The `gpu.go` file uses these functions and doesn't need to know which implementation is active.

## Running in QEMU

After building the QEMU version:

```bash
cd src
make push-qemu
```

### Option 1: Using raspi4b machine type (no display device)

```bash
docker/runqemu-vga
```

This uses the `raspi4b` machine type. However, **the framebuffer will NOT be displayed** because:
- The `raspi4b` machine type doesn't support standard VGA/PCI devices
- `ramfb` device is not compatible with `raspi4b` (requires dynamic sysbus, which raspi4b doesn't support)
- The kernel writes to framebuffer memory at `0x10000000`, but QEMU won't display it

**Result**: The framebuffer is written to, but QEMU won't show it via VNC. UART output still works for debugging.

### Option 2: Using virt machine type (with VGA device)

```bash
docker/runqemu-virt-vga
```

This uses the `virt` machine type (generic AArch64 virtual machine) which supports standard VGA devices via PCI. This should provide better display support, though some Raspberry Pi-specific hardware features won't work.

**Note**: The `virt` machine type uses different hardware addresses than Raspberry Pi, so the kernel's UART and other hardware-specific code may not work correctly. This is mainly useful for testing the framebuffer display code.

## File Structure

```
src/go/mazarin/
├── framebuffer_common.go    # Shared types and constants
├── framebuffer_rpi.go       # Raspberry Pi implementation (!qemu)
├── framebuffer_qemu.go      # QEMU implementation (qemu)
└── gpu.go                   # GPU functions (uses framebuffer API)
```

## Makefile Targets

- `make` or `make kernel.elf` - Build for real hardware
- `make kernel-qemu.elf` - Build for QEMU
- `make push` - Copy real hardware kernel to docker/builtin
- `make push-qemu` - Copy QEMU kernel to docker/builtin

## Why Two Implementations?

QEMU's `raspi4b` machine type doesn't fully emulate the Raspberry Pi mailbox interface. The GPU doesn't process mailbox messages, so framebuffer initialization via mailbox fails in QEMU. By using build tags, we can:

1. Test framebuffer code in QEMU using a simple memory-mapped approach
2. Use the correct mailbox implementation for real hardware
3. Keep the same API so other code doesn't need changes

## Future Improvements

For better QEMU display support, we could:

1. Use a different QEMU machine type that supports framebuffer display
2. Add a virtio-gpu device to QEMU
3. Use QEMU's ramfb device for simple framebuffer display

For now, the QEMU build is useful for testing the framebuffer drawing code (character rendering, scrolling, etc.) even if the display isn't automatically shown in QEMU.

