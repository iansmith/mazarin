# Critical Fix: RAMFBCfg Structure Size

## Problem Found

**Working code structure size**: 28 bytes (with `__attribute__((packed))`)
**Our original Go structure size**: 32 bytes (Go adds padding!)

## Root Cause

Go structs are NOT packed by default. The structure had:
- Addr: uint64 (8 bytes)
- FourCC: uint32 (4 bytes) - but Go pads this to 8 bytes for alignment
- Flags: uint32 (4 bytes)
- Width: uint32 (4 bytes)
- Height: uint32 (4 bytes)
- Stride: uint32 (4 bytes)

Go aligns uint32 fields to 8-byte boundaries after a uint64, so:
- Addr: 8 bytes (offset 0)
- FourCC: 4 bytes (offset 8) - but padding to 8 bytes
- Flags: 4 bytes (offset 16)
- Width: 4 bytes (offset 20)
- Height: 4 bytes (offset 24)
- Stride: 4 bytes (offset 28)
**Total: 32 bytes** ❌

But the working code uses `__attribute__((packed))` which makes it exactly 28 bytes with no padding.

## Solution

Converted RAMFBCfg to use a `[28]byte` backing array (like we did for FWCfgDmaAccess):
- Exactly 28 bytes, no padding
- Accessor methods handle big-endian conversion
- Layout: [0-7: Addr][8-11: FourCC][12-15: Flags][16-19: Width][20-23: Height][24-27: Stride]

## Verification

- Structure size: ✅ 28 bytes
- DMA length: ✅ 0x1C (28 bytes)
- DMA transfer: ✅ Completes successfully

## Status

The structure size is now correct. The DMA transfer completes, but QEMU still shows "Guest has not initialized the display (yet)". This suggests there may be another issue with the configuration format or QEMU's ramfb implementation.










