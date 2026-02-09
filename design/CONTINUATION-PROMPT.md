# Continuation Prompt: VirtIO Block Driver Implementation

## Task
Implement a VirtIO block PCI driver for kmazarin so that both ARM64 and x86_64 can launch userspace programs (dapope.elf, stdio.elf) from disk and display their output on the framebuffer.

## Current Status
- ✅ x86_64: Kmazarin boots, VirtIO GPU works perfectly (1920×1080 framebuffer)
- ✅ AMD64 userspace programs built (dapope-amd64.elf, stdio-amd64.elf)
- ✅ Programs on disk with correct filenames (/dapope.elf, /stdio.elf)
- ✅ VirtIO block PCI device attached in QEMU (both x86_64 and ARM64)
- ❌ **Block device not discovered** - kmazarin has no VirtIO block driver

## The Problem
- `ksyscall/launch.go` line 141 calls `device.GetBlockDevice()` → returns false
- No block devices registered because there's no VirtIO block driver
- dapope/stdio launch fails even though files exist on disk

## The Solution
**Implement VirtIO block PCI driver** by copying the pattern from the working VirtIO GPU driver (`kmazarin/device/virtio/gpu/gpu.go` lines 275-350).

## Implementation Checklist

### Step 1: Create VirtIO Block Driver
Create `kmazarin/device/virtio/block/` with:
- `block.go` - PCI scanning, device init (copy gpu.go pattern)
- `io.go` - Read/Write operations
- `commands.go` - VirtIO block protocol structures

**Key pattern to copy from GPU**:
```go
// Scan PCI bus (gpu.go line 279)
for bus := uint8(0); bus < 1; bus++ {
    for slot := uint8(0); slot < 32; slot++ {
        vendorID := pci.ConfigRead32(bus, slot, 0, pci.PCI_VENDOR_ID)
        // Check for VirtIO block: vendor 0x1AF4, device 0x1001/0x1042
        // Call pci.FindVirtIOCapabilities()
        // Setup VirtQueue
        // Register with device.RegisterBlockDevice()
    }
}
```

### Step 2: Integrate with Main
In `kmazarin/kmazarin/main.go` after line 592 (GPU init):
```go
console.KPrintln("[Main] About to init VirtIO Block...")
if block.Init() {
    console.KPrintln("[Main] VirtIO Block init done")
}
```

### Step 3: Update ARM64 QEMU Config
File: `Taskfile.yml` line 884
- Change: `-device virtio-blk-device` → `-device virtio-blk-pci,bus=pcie.0`
- This makes ARM64 use PCI (like x86_64), so one driver works for both

### Step 4: Test Both Architectures
```bash
# x86_64 test
export GOTOOLCHAIN=auto GO=/opt/homebrew/Cellar/go/1.25.5/libexec/bin/go QEMU_AMD64=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-x86_64
$GO tool task run-diplomat-kmazarin TIMEOUT=15

# ARM64 test
export QEMU=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-aarch64
$GO tool task run-diplomat-arm64 TIMEOUT=15
```

Expected: Block device found, dapope/stdio launch successfully, framebuffer shows output

## Critical Files

**Reference implementation** (copy this pattern):
- `kmazarin/device/virtio/gpu/gpu.go` - PCI scanning (lines 275-350)
- `kmazarin/device/virtio/gpu/init.go` - Initialization flow

**PCI utilities**:
- `kmazarin/device/pci/pci.go` - ConfigRead32, FindVirtIOCapabilities

**Device registration**:
- `kmazarin/device/manager.go` - RegisterBlockDevice() (already exists)

**Detailed plan**:
- Read `/Users/iansmith/mazzy/design/VIRTIO-BLOCK-IMPLEMENTATION-PLAN.md` for full details

## Environment Setup
```bash
export GOTOOLCHAIN=auto
export GO=/opt/homebrew/Cellar/go/1.25.5/libexec/bin/go
export QEMU=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-aarch64
export QEMU_AMD64=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-x86_64
```

## Recent Commit
Latest: `5882470` - "WIP: Add AMD64 userspace support and migrate to auxv-based boot params"

## Priority
**HIGH** - Blocking both ARM64 and x86_64 userspace execution. The GPU works perfectly, we just need block I/O to load programs.
