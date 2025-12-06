# VirtIO GPU PCI Implementation Guide for AArch64

## Overview

VirtIO GPU PCI is better supported on ARM/AArch64 than bochs-display, which has known PCI memory bar caching issues. This document provides references to working C code and implementation guidance.

## Key Resources

### 1. Linux Kernel VirtIO GPU Driver (C Implementation)
**Location**: Linux kernel source tree
- `drivers/gpu/drm/virtio/virtgpu_drv.c` - Main driver
- `drivers/gpu/drm/virtio/virtgpu_vq.c` - Virtqueue operations
- `drivers/gpu/drm/virtio/virtgpu_kms.c` - Kernel mode setting

**GitHub**: https://github.com/torvalds/linux/tree/master/drivers/gpu/drm/virtio

### 2. VirtIO Specification
**Official Spec**: https://docs.oasis-open.org/virtio/virtio/v1.2/csd01/virtio-v1.2-csd01.pdf

### 3. Rust Implementation (Reference)
**Repository**: https://github.com/rcore-os/virtio-drivers
- Has AArch64 examples
- Can be used as reference for algorithm/structure (translate to C)

## Implementation Steps

### Step 1: PCI Device Discovery
We already have this! The kernel can find virtio-gpu-pci via PCI enumeration:
- Vendor ID: `0x1AF4` (VirtIO)
- Device ID: `0x1050` (VirtIO GPU)

### Step 2: VirtIO PCI Transport Setup
VirtIO PCI devices have special capabilities in PCI config space:
- **Common Config Capability**: Device configuration and status
- **Notify Capability**: Virtqueue notification mechanism
- **ISR Status Capability**: Interrupt status
- **Device Config Capability**: Device-specific configuration

### Step 3: VirtQueue Setup
VirtIO uses virtqueues for communication:
- **Control Queue**: For GPU commands (create resource, set scanout, etc.)
- **Cursor Queue**: For cursor updates (optional)

Each virtqueue needs:
- Descriptor table (guest memory)
- Available ring (guest writes, device reads)
- Used ring (device writes, guest reads)

### Step 4: GPU Command Sequence

```c
// 1. Create 2D resource
VIRTIO_GPU_CMD_RESOURCE_CREATE_2D
  - resource_id: Unique ID for the framebuffer
  - format: Pixel format (e.g., VIRTIO_GPU_FORMAT_B8G8R8A8_UNORM)
  - width, height: Resolution

// 2. Attach backing store (framebuffer memory)
VIRTIO_GPU_CMD_RESOURCE_ATTACH_BACKING
  - resource_id: Same as above
  - entries: Array of memory addresses and lengths

// 3. Set scanout (connect resource to display)
VIRTIO_GPU_CMD_SET_SCANOUT
  - scanout_id: Display output (usually 0)
  - resource_id: The resource to display
  - rectangle: x, y, width, height

// 4. Transfer data to host (update display)
VIRTIO_GPU_CMD_TRANSFER_TO_HOST_2D
  - resource_id: The resource
  - rectangle: Region to transfer
```

## C Code Structure Example

```c
// VirtIO GPU command structures
struct virtio_gpu_ctrl_hdr {
    uint32_t type;      // Command type
    uint32_t flags;     // Command flags
    uint64_t fence_id;  // Fence ID for synchronization
    uint32_t ctx_id;    // Context ID
    uint32_t padding;   // Padding
};

struct virtio_gpu_resource_create_2d {
    struct virtio_gpu_ctrl_hdr hdr;
    uint32_t resource_id;
    uint32_t format;    // VIRTIO_GPU_FORMAT_*
    uint32_t width;
    uint32_t height;
};

struct virtio_gpu_resource_attach_backing {
    struct virtio_gpu_ctrl_hdr hdr;
    uint32_t resource_id;
    uint32_t nr_entries;  // Number of memory entries
    // Followed by array of virtio_gpu_mem_entry
};

struct virtio_gpu_set_scanout {
    struct virtio_gpu_ctrl_hdr hdr;
    struct virtio_gpu_rect r;  // x, y, width, height
    uint32_t scanout_id;
    uint32_t resource_id;
};

struct virtio_gpu_transfer_to_host_2d {
    struct virtio_gpu_ctrl_hdr hdr;
    struct virtio_gpu_rect r;  // Region to transfer
    uint64_t offset;            // Offset in resource
    uint32_t resource_id;
    uint32_t padding;
};
```

## Key Constants

```c
// VirtIO PCI
#define VIRTIO_PCI_VENDOR_ID    0x1AF4
#define VIRTIO_PCI_DEVICE_GPU  0x1050

// VirtIO PCI Capabilities
#define VIRTIO_PCI_CAP_COMMON_CFG  1
#define VIRTIO_PCI_CAP_NOTIFY_CFG  2
#define VIRTIO_PCI_CAP_ISR_CFG     3
#define VIRTIO_PCI_CAP_DEVICE_CFG  4

// VirtIO GPU Commands
#define VIRTIO_GPU_CMD_GET_DISPLAY_INFO       0x0100
#define VIRTIO_GPU_CMD_RESOURCE_CREATE_2D     0x0101
#define VIRTIO_GPU_CMD_RESOURCE_UNREF         0x0102
#define VIRTIO_GPU_CMD_SET_SCANOUT             0x0103
#define VIRTIO_GPU_CMD_RESOURCE_FLUSH          0x0104
#define VIRTIO_GPU_CMD_TRANSFER_TO_HOST_2D    0x0105
#define VIRTIO_GPU_CMD_RESOURCE_ATTACH_BACKING 0x0106
#define VIRTIO_GPU_CMD_RESOURCE_DETACH_BACKING 0x0107

// Pixel Formats
#define VIRTIO_GPU_FORMAT_B8G8R8A8_UNORM  1
#define VIRTIO_GPU_FORMAT_B8G8R8X8_UNORM  2
#define VIRTIO_GPU_FORMAT_R8G8B8A8_UNORM  3
```

## Implementation Complexity

**Compared to bochs-display:**
- **bochs-display**: Simple MMIO register writes (what we have now)
- **virtio-gpu-pci**: Requires:
  1. PCI capability reading
  2. Virtqueue setup (descriptor tables, rings)
  3. Command/response protocol via virtqueues
  4. Memory management for framebuffer
  5. Synchronization (fence IDs)

**Estimated effort**: 2-3x more complex than bochs-display initialization

## Next Steps

1. **Study Linux kernel implementation**: `virtgpu_drv.c` and `virtgpu_vq.c`
2. **Implement VirtIO PCI transport**: Read capabilities, set up virtqueues
3. **Implement GPU commands**: Create resource, attach backing, set scanout
4. **Test with QEMU**: Use `-device virtio-gpu-pci` instead of `-device ramfb`

## References

- Linux kernel virtio-gpu: https://github.com/torvalds/linux/tree/master/drivers/gpu/drm/virtio
- VirtIO 1.2 Spec: https://docs.oasis-open.org/virtio/virtio/v1.2/csd01/virtio-v1.2-csd01.pdf
- rcore-os/virtio-drivers (Rust, but good algorithm reference): https://github.com/rcore-os/virtio-drivers

