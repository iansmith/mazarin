# RISC-V Diplomat Phase 2 - Task #2: VirtIO Block Device

## Session Continuation Prompt

Continue work on RISC-V diplomat boot sequence. Task #1 (Platform Initialization) is complete. Now implement Task #2: VirtIO block device driver.

## Current Status

**COMPLETED: Task #1 - Platform Initialization** ✅
- Fixed `printHex()` to use `plat.PrintChar()` (works in RISC-V direct mode)
- Implemented hardcoded QEMU virt platform parameters (Option A):
  - RAM: 0x80000000 - 0xFFFFFFFF (2GB)
  - FDT address: 0xFFE00000 (will pass to kmazarin via auxv)
  - Hart ID: 0, CPU count: 1
- Used global struct instead of `new()` to avoid early boot heap allocation
- Memory spans initialized successfully
- Boot sequence: OpenSBI → diplomat @ 0x80200000 → (next: load kmazarin from disk)

**Current output:**
```
Platform: QEMU virt (RISC-V64)
  Hart ID:  0x0
  FDT addr: 0xFFE00000
  RAM:      0x80000000 - 0xFFFFFFFF (0x800 MB)
  CPUs:     0x1

DBG: spans OK
=== RISC-V Diplomat Task #1 Complete ===
Platform init:    SUCCESS (QEMU virt hardcoded)
Memory spans:     SUCCESS
FDT address:      0xFFE00000 (will pass to kmazarin via auxv)

=== Next: Task #2 - VirtIO Block Device ===
ERROR: block device: blockdev: boot services not available
```

## Task List

- [x] **Task #1**: Parse FDT / Platform Initialization (COMPLETE)
- [ ] **Task #2**: Implement VirtIO block device driver ⬅️ **START HERE**
- [ ] **Task #3**: Mount FAT32 filesystem
- [ ] **Task #4**: Load kmazarin.elf from disk
- [ ] **Task #5**: Set up kernel page tables (Sv48)
- [ ] **Task #6**: Jump to kmazarin entry point (high memory)

## Task #2 Details: VirtIO Block Device Driver

### Goal
Implement VirtIO block device driver for RISC-V to load kmazarin.elf from disk. This replaces UEFI HandleProtocol which isn't available in direct boot mode.

### Key Requirements

1. **Share code with kmazarin's VirtIO implementation**
   - Kmazarin has existing VirtIO block driver at: `kmazarin/device/virtio/block/`
   - Diplomat should use compatible structures/approach where possible
   - May need simplified version for bootloader use (no full runtime)

2. **QEMU virt platform VirtIO MMIO addresses**
   - VirtIO devices on QEMU virt platform use MMIO (not PCI on RISC-V)
   - Base addresses: 0x10001000, 0x10002000, ... (check FDT for actual layout)
   - Need to enumerate VirtIO MMIO devices and find block device

3. **Early boot constraints**
   - No heap allocation with `new()` (use global/stack structs)
   - No string assignments (triggers allocation)
   - Use `plat.PrintChar()` for debug output
   - Can call Go functions (runtime is initialized after Task #1)

### Implementation Approach

**Option A: Parse FDT for VirtIO MMIO devices**
- FDT parser already exists: `diplomat/main/fdt_parse.go`
- Look for `/soc/virtio_mmio@*` nodes
- Extract `reg` property for base address and size
- Find device with `compatible = "virtio,mmio"` and device type 2 (block)

**Option B: Hardcoded QEMU virt addresses** (faster, matches Task #1 approach)
- Hardcode VirtIO MMIO base addresses for QEMU virt platform
- Probe each address for block device (device type = 2)
- Document as QEMU-specific, same as RAM layout

**Recommended: Option B** (consistency with Task #1 hardcoded approach)

### Key Files to Review

**Existing VirtIO code (kmazarin):**
- `kmazarin/device/virtio/block/` - Block device implementation
- `kmazarin/device/virtio/virtio.go` - VirtIO common structures
- `shared/blockdev/blockdev.go` - BlockDevice interface

**Diplomat boot code:**
- `diplomat/main/boot_riscv64.go` - Add `GetBootDeviceRISCV()` implementation
- `diplomat/main/platform_riscv64.go` - Platform-specific hooks
- `diplomat/main/main.go` - BlockDevice interface usage

**Error to fix:**
```go
// diplomat/main/boot_riscv64.go:101
func GetBootDeviceRISCV() (*UEFIBlockDevice, error) {
    // Currently returns errBootServicesNotAvailable
    // Need to implement VirtIO block device probe/init here
}
```

### VirtIO MMIO Device Structure (Simplified)

```go
// VirtIO MMIO register offsets (from spec 1.0)
const (
    VIRTIO_MMIO_MAGIC_VALUE    = 0x000 // 0x74726976 ('virt')
    VIRTIO_MMIO_VERSION        = 0x004 // Version (should be 2)
    VIRTIO_MMIO_DEVICE_ID      = 0x008 // Device type (2 = block)
    VIRTIO_MMIO_VENDOR_ID      = 0x00c
    VIRTIO_MMIO_DEVICE_FEATURES= 0x010
    VIRTIO_MMIO_QUEUE_SEL      = 0x030
    VIRTIO_MMIO_QUEUE_NUM_MAX  = 0x034
    VIRTIO_MMIO_QUEUE_NUM      = 0x038
    VIRTIO_MMIO_STATUS         = 0x070
)

const (
    VIRTIO_DEVICE_TYPE_BLOCK = 2
)
```

### Expected Steps

1. **Probe VirtIO MMIO devices**
   - Check MMIO addresses for valid VirtIO magic value
   - Find device with type = 2 (block device)
   - Verify version and features

2. **Initialize VirtIO block device**
   - Reset device (STATUS = 0)
   - Set ACKNOWLEDGE and DRIVER status bits
   - Negotiate features
   - Set up virtqueue (simplified for bootloader)
   - Set DRIVER_OK status bit

3. **Wrap in UEFIBlockDevice interface**
   - Implement `Read(lba, count, buffer)` method
   - Return `*UEFIBlockDevice` compatible with existing code
   - This allows Tasks #3-4 to proceed unchanged

4. **Test**
   - Boot diplomat-riscv64
   - Verify VirtIO device detected
   - Attempt to read block 0 (MBR/GPT header)
   - Should progress to Task #3 (FAT32 mount)

### QEMU Command Reference

```bash
export GOTOOLCHAIN=auto
export GO=/opt/homebrew/Cellar/go/1.25.5/libexec/bin/go
export QEMU=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-aarch64

# Build and run (3 second timeout)
$GO tool task diplomat-riscv64
$GO tool task run-diplomat-riscv64 TIMEOUT=3

# Check serial output
$GO tool safe-serial-read /tmp/diplomat-riscv64-serial.log
```

### Important Notes

1. **Kmazarin already loads at high memory** (0xFFFFFFFF43800000)
   - No changes needed for Task #5
   - PrepareKernelVM already sets up Sv48 page tables
   - KernelTextBase = KernelMMIOOffset + KmazarinLoadAddr

2. **Early boot allocation pitfalls** (from Task #1 debugging)
   - Don't use `new()` - use global structs
   - Don't assign strings - triggers allocation
   - Use `plat.PrintChar()` not `printChar()`

3. **FDT will be passed to kmazarin**
   - Diplomat stores FDT address: 0xFFE00000
   - Will pass via auxv entry: AT_FDT_ADDR
   - Kmazarin can parse FDT for full device discovery

4. **Disk image**
   - `build/disk-riscv64.img` already exists
   - Contains FAT32 filesystem with kmazarin.elf
   - Created by `task disk-riscv64-minimal`

## Success Criteria for Task #2

When complete, diplomat should:
1. Detect VirtIO block device at MMIO address
2. Initialize device successfully
3. Return functional BlockDevice from `GetBootDeviceRISCV()`
4. Progress to Task #3: "Mounting FAT32 filesystem..."
5. No longer show "ERROR: block device: boot services not available"

## Next Session Instructions

1. Review existing VirtIO code in kmazarin
2. Decide between FDT parsing vs hardcoded addresses (recommend hardcoded)
3. Implement VirtIO MMIO device probe and init
4. Test with QEMU virt platform
5. Move to Task #3 when block device read works

---

**Previous session summary**: Successfully completed Task #1 (platform init) by hardcoding QEMU virt parameters and fixing early boot allocation issues. PrintHex now works, FDT address preserved, memory spans initialized. Ready for VirtIO block device implementation.
