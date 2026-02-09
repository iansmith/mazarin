# VirtIO Block Device Implementation Plan

## Context & Problem Statement

**Goal**: Enable kmazarin kernel (on both ARM64 and x86_64/AMD64) to launch userspace programs (`dapope.elf` and `stdio.elf`) from a VirtIO block device and display their output on the framebuffer.

**Current Status**:
- ✅ x86_64: Kmazarin boots successfully with VirtIO GPU working (1920×1080 framebuffer)
- ✅ x86_64: AMD64 userspace programs built (dapope-amd64.elf, stdio-amd64.elf)
- ✅ x86_64: Programs on disk-amd64.img with correct filenames
- ❌ x86_64: dapope/stdio launch fails - GetBlockDevice() returns false (no block devices registered)
- ❓ ARM64: Not tested with latest changes

**Root Cause**: Kmazarin has **no VirtIO block device driver**. The VirtIO GPU works because it has a PCI driver that scans the bus (`findVirtIOGPU()` in `kmazarin/device/virtio/gpu/gpu.go`). Block devices need the same approach.

## Architecture Analysis

### Current Device Setup

**ARM64 (QEMU virt + UEFI/Diplomat)**:
- VirtIO GPU: PCI (`virtio-gpu-pci`) - discovered via PCI scanning in kmazarin
- VirtIO Block: **MMIO** (`virtio-blk-device`) - requires DTB discovery (NOT IMPLEMENTED)
- VirtIO Input: MMIO - discovered via DTB
- QEMU command line 884: `-device virtio-blk-device,drive=flock0`

**x86_64 (QEMU Q35 + UEFI/Diplomat)**:
- VirtIO GPU: PCI (`virtio-vga`) - discovered via PCI scanning
- VirtIO Block: PCI (`virtio-blk-pci`) - requires PCI driver (NOT IMPLEMENTED)
- QEMU command line 797: `-device virtio-blk-pci,drive=drive-virtio-disk0,bus=pcie.0,addr=0x10`

### Why PCI is Better Than MMIO for Block Devices

1. **Unified implementation**: One driver works on both ARM64 and x86_64
2. **Hardware reality**: x86_64 Q35 only supports VirtIO-PCI (no MMIO option)
3. **Proven pattern**: GPU already uses PCI successfully on both architectures
4. **ARM64 flexibility**: QEMU virt supports both PCI and MMIO - we can choose PCI

## Implementation Plan

### Phase 1: Create VirtIO Block PCI Driver

Create `kmazarin/device/virtio/block/` package with these files:

#### 1.1 `block.go` - Core Driver and PCI Discovery

**Pattern to follow**: Copy structure from `kmazarin/device/virtio/gpu/gpu.go` lines 275-350

```go
package block

// Key functions:
// - Init() bool - Public entry point, scans PCI and initializes
// - findVirtIOBlock() bool - Scans PCI bus for block devices
// - virtioBlockInit() bool - VirtIO protocol handshake
// - setupVirtQueue() - Configure block I/O queue

// PCI Device IDs (from VirtIO spec):
const (
    VIRTIO_VENDOR_ID = 0x1AF4
    VIRTIO_BLK_DEVICE_ID_LEGACY = 0x1001  // Legacy
    VIRTIO_BLK_DEVICE_ID_MODERN = 0x1042  // Modern (1.0+)
)

// VirtIO block device structure
type VirtIOBlockDevice struct {
    // PCI location
    Bus, Slot, Func uint8

    // VirtIO capability BARs (from PCI config)
    CommonCfgBar, NotifyBar, ISRBar, DeviceCfgBar uintptr

    // VirtQueue for I/O requests
    Queue virtio.VirtQueue

    // Device features
    Capacity uint64      // Total sectors
    BlockSize uint32     // Bytes per sector

    // Synchronization
    Lock uint32
}
```

**Key implementation details**:
- Scan PCI bus 0, slots 0-31 like GPU does (line 279-280 in gpu.go)
- Use `pci.ConfigRead32()` to read vendor/device ID
- Call `pci.FindVirtIOCapabilities()` to locate Common/Notify/ISR/Device config
- Map BARs to access VirtIO registers
- Set up single VirtQueue for block I/O (queue index 0)

#### 1.2 `io.go` - Block I/O Operations

```go
// Read reads sectors from the block device
// lba: Logical Block Address (sector number)
// count: Number of sectors to read
// buffer: Destination buffer (must be physically contiguous)
func Read(lba uint64, count uint32, buffer unsafe.Pointer) error

// Write writes sectors to the block device
func Write(lba uint64, count uint32, buffer unsafe.Pointer) error

// GetCapacity returns total sectors
func GetCapacity() uint64

// GetBlockSize returns bytes per sector (typically 512)
func GetBlockSize() uint32
```

**Implementation notes**:
- Build VirtIO block request structures (header + data + status)
- Add descriptor chain to VirtQueue
- Notify device via notify BAR
- Poll Used ring for completion (or use IRQ if available)
- Status byte: 0=OK, 1=IOERR, 2=UNSUPP

#### 1.3 `commands.go` - VirtIO Block Protocol Structures

```go
// VirtIO block request header
type VirtIOBlockReq struct {
    Type   uint32  // VIRTIO_BLK_T_IN (0) or VIRTIO_BLK_T_OUT (1)
    _      uint32  // Reserved
    Sector uint64  // LBA
}

// VirtIO block request status (written by device)
type VirtIOBlockStatus uint8

const (
    VIRTIO_BLK_S_OK     = 0
    VIRTIO_BLK_S_IOERR  = 1
    VIRTIO_BLK_S_UNSUPP = 2
)

const (
    VIRTIO_BLK_T_IN  = 0  // Read
    VIRTIO_BLK_T_OUT = 1  // Write
    VIRTIO_BLK_T_FLUSH = 4
)
```

### Phase 2: Integrate with Device Manager

#### 2.1 Register Block Device

In `kmazarin/device/manager.go`, the `BlockDevice` interface and `RegisterBlockDevice()` already exist. We just need to call it:

```go
// In block/block.go Init():
device.RegisterBlockDevice(&virtioBlockDevice)
```

#### 2.2 Call from Main

In `kmazarin/kmazarin/main.go`, after line 592 (GPU init):

```go
// Initialize VirtIO block device
console.KPrintln("[Main] About to init VirtIO Block...")
if block.Init() {
    console.KPrintln("[Main] VirtIO Block init done")
} else {
    console.KPrintln("[Main] VirtIO Block init failed (no device found?)")
}
```

### Phase 3: Update QEMU Configuration

#### 3.1 ARM64 - Switch to PCI Block Device

File: `Taskfile.yml` line 884

**Change FROM**:
```yaml
-device virtio-blk-device,drive=flock0
```

**Change TO**:
```yaml
-device virtio-blk-pci,drive=flock0,bus=pcie.0
```

This makes ARM64 consistent with x86_64 (both use PCI).

#### 3.2 x86_64 - Already Correct

Line 797 already has: `-device virtio-blk-pci,drive=drive-virtio-disk0,bus=pcie.0,addr=0x10`

### Phase 4: Testing

1. **Build and test x86_64**:
   ```bash
   export GOTOOLCHAIN=auto GO=/opt/homebrew/Cellar/go/1.25.5/libexec/bin/go QEMU_AMD64=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-x86_64
   $GO tool task run-diplomat-kmazarin TIMEOUT=15
   ```

   Expected output:
   ```
   [VirtIO Block] Scanning PCI bus...
   [VirtIO Block] Found device at bus=0 slot=X func=0
   [VirtIO Block] Capacity: XXXXX sectors (XXX MB)
   [Launch] /dapope.elf
   [Load] Read 0xXXXXXX of 0xXXXXXX bytes
   [main] dapope launched
   ```

2. **Build and test ARM64**:
   ```bash
   export GOTOOLCHAIN=auto GO=/opt/homebrew/Cellar/go/1.25.5/libexec/bin/go QEMU=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-aarch64
   $GO tool task run-diplomat-arm64 TIMEOUT=15
   ```

3. **Verify framebuffer output**: Both architectures should show dapope/stdio output on display

## File Locations Reference

**VirtIO GPU implementation** (pattern to copy):
- `kmazarin/device/virtio/gpu/gpu.go` - PCI scanning (lines 275-350)
- `kmazarin/device/virtio/gpu/init.go` - Initialization flow

**PCI utilities**:
- `kmazarin/device/pci/` - ConfigRead32/Write32, FindVirtIOCapabilities

**VirtIO common**:
- `kmazarin/device/virtio/virtio.go` - VirtQueue structures and helpers

**Device manager**:
- `kmazarin/device/manager.go` - BlockDevice interface, RegisterBlockDevice()

**Launch syscall** (needs block device):
- `kmazarin/ksyscall/launch.go` line 141 - GetBlockDevice() call

## Key Implementation Notes

1. **No heap allocation in nosplit paths**: Use static arrays or pre-allocated buffers
2. **Physical addresses**: Use `kmem.VirtToPhys()` for descriptor addresses
3. **Cache coherency**: Use `asm.Dsb()` after writing descriptors
4. **Error handling**: Return false on any failure, print debug messages
5. **Simplicity first**: Implement synchronous polling I/O first (no IRQ handling)
6. **Copy GPU pattern**: The GPU driver works - follow its structure closely

## Success Criteria

- [x] Commit WIP with auxv migration and AMD64 userspace
- [ ] VirtIO block PCI driver implemented
- [ ] x86_64: dapope/stdio launch successfully
- [ ] x86_64: Framebuffer shows program output
- [ ] ARM64: Switched to virtio-blk-pci
- [ ] ARM64: dapope/stdio launch successfully
- [ ] ARM64: Framebuffer shows program output
- [ ] Both: No device discovery errors in serial log

## Continuation Prompt

When resuming this work, start with:

1. Read this implementation plan
2. Create `kmazarin/device/virtio/block/` directory
3. Implement `block.go` by copying GPU PCI scanning pattern
4. Test on x86_64 first (already has virtio-blk-pci attached)
5. Update ARM64 QEMU config
6. Test on ARM64
7. Commit working implementation

The GPU driver in `kmazarin/device/virtio/gpu/gpu.go` is the **gold standard** - it successfully scans PCI, finds devices, and performs VirtIO I/O on both architectures. Copy this pattern for block devices.
