# RISC-V Diplomat Phase 2 - Task #3: FAT32 Mount Debug

## Session Continuation Prompt

Continue debugging RISC-V diplomat FAT32 mount hang. Task #2 (VirtIO block device) is complete. Task #3 (FAT32 mount) hangs before attempting any block I/O.

## Current Status

**COMPLETED: Task #2 - VirtIO Block Device (MMIO)** ✅
- Switched from PCI to VirtIO MMIO transport (PCI fundamentally broken without UEFI)
- VirtIO MMIO device discovered at 0x10008000
- VirtIO handshake complete (ACKNOWLEDGE → DRIVER → FEATURES_OK → DRIVER_OK)
- Block device initialized successfully
- Output shows: `=== Task #2 Complete ===` then `Mounting FAT32...`

**BLOCKED: Task #3 - FAT32 Filesystem Mount** ⚠️
- System hangs at "Mounting FAT32..." with no further output
- `ReadBlockVirtIO` stub implemented but **NEVER CALLED** (no debug output)
- Hang occurs inside `boot.MountFilesystem(blockDev)` before any block reads
- Likely issues:
  1. Memory allocation in FAT32 mount code (heap not fully initialized?)
  2. Missing syscall or boot service
  3. Infinite loop in FAT32 initialization
  4. String operations triggering allocation

**Current Serial Output:**
```
Found VirtIO MMIO block device at 0x10008000
VirtIO MMIO handshake...
  Reset...ACK...DRIVER...Features...FEATURES_OK...Verify...DRIVER_OK...Complete!
VirtIO MMIO block device ready
=== Task #2 Complete ===

RMMounting FAT32...
[HANGS HERE - timeout after 5-8 seconds]
```

Debug markers: 'R' printed, then 'M' printed, then "Mounting FAT32..." printed, then hang.

## Architecture Context

### RISC-V Boot Flow
1. OpenSBI firmware loads diplomat at 0x80200000
2. Diplomat runs in S-mode (no UEFI services available)
3. VirtIO MMIO devices at 0x10001000-0x10008000
4. Block device at 0x10008000 (type=2, version=1)
5. Go runtime initialized (Task #1 complete)
6. Memory spans initialized

### VirtIO MMIO vs PCI
- **QEMU config**: Single VirtIO MMIO device (`virtio-blk-device`)
- **Why MMIO**: PCI doesn't work on RISC-V without UEFI firmware
  - PCI ECAM works (config space at 0x30000000)
  - PCI MMIO hangs (any access to 0x0C000000 range)
  - PCI cfg capability triggers QEMU assertion failures
- **Future**: Kmazarin will use PCI when it has full PCI initialization

### Block Device Interface
```go
// ReadBlock stub implemented but not called yet
func ReadBlockVirtIO(lba uint64, buf []byte) error {
    printString("DEBUG: ReadBlockVirtIO called for LBA ")
    printHex(lba)
    printString("\r\n")

    // Stub: fill with zeros
    for i := range buf {
        buf[i] = 0
    }
    return nil
}
```

## Key Files Modified

### Core Implementation
- **`diplomat/main/boot_riscv64.go`** - VirtIO MMIO device discovery and handshake
  - `scanVirtIOMMIO()` - Scans 0x10001000-0x10008000 for block devices
  - `virtioMMIOHandshake()` - Device initialization (COMPLETE)
  - TODO: Implement virtqueue setup and block I/O

- **`diplomat/main/platform_riscv64.go`** - Platform operations
  - `ReadBlockVirtIO()` - Stub that returns zeros (line ~155)
  - `init()` - Wires up `plat.ReadBlockVirtIO = ReadBlockVirtIO`

- **`diplomat/main/uefi_blockdev.go`** - Block device wrapper
  - `ReadBlock()` - Checks if `protocol == 0` (RISC-V), calls `plat.ReadBlockVirtIO()`

- **`diplomat/main/platform.go`** - Platform interface
  - Added `ReadBlockVirtIO func(lba uint64, buf []byte) error` to PlatformOps

- **`diplomat/main/platform_{amd64,arm64}.go`** - Stubs for other platforms
  - `readBlockVirtIOStub()` - Panic if called (should use UEFI)

### Configuration
- **`Taskfile.yml`** - QEMU command (line ~968)
  - `-device virtio-blk-device,drive=drive-virtio-disk0` (MMIO transport)
  - Note: Single device for now, will need PCI device for kmazarin later

## Debugging Strategy

### Step 1: Determine Where Hang Occurs
The hang is in `boot.MountFilesystem(blockDev)` but before calling `ReadBlock`. Need to find exact location.

**Find the function:**
```bash
# Find MountFilesystem implementation
grep -r "func.*MountFilesystem" diplomat/ shared/

# Or find what boot.MountFilesystem maps to
grep "MountFilesystem:" diplomat/main/platform*.go
```

Likely candidates:
- `fat32Mount()` in `diplomat/main/` somewhere
- FAT32 code in `shared/fs/fat32/`

### Step 2: Add Debug Output
Add `printString()` calls at the start of the mount function and at key points:
```go
func fatMount(dev blockdev.BlockDevice) (*fat32.FileSystem, error) {
    printString("DEBUG: fatMount entry\r\n")

    // Check for early allocation issues
    printString("DEBUG: before any allocation\r\n")

    // ... rest of function
}
```

### Step 3: Check for Common Issues

**Early Boot Allocation Pitfalls** (from MEMORY.md):
1. **`new()` calls** - Heap not fully initialized, hangs on allocation
2. **String assignments** - Trigger allocation, cause hangs
3. **Interface conversions** - May allocate

**Solutions:**
- Use global structs instead of `new()`
- Avoid string assignments in early boot
- Use `plat.PrintChar()` for debug output (NOT regular print functions)

### Step 4: Test Commands

```bash
# Set environment
export GOTOOLCHAIN=auto
export GO=/opt/homebrew/Cellar/go/1.25.5/libexec/bin/go
export QEMU=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-aarch64

# Rebuild and test
$GO tool task diplomat-riscv64
$GO tool task run-diplomat-riscv64 TIMEOUT=8

# Check output
$GO tool safe-serial-read /tmp/diplomat-riscv64-serial.log | tail -50
```

## Next Steps After Unblocking FAT32

Once FAT32 mount works (gets past the hang):

### 1. Implement Real VirtIO Block I/O
The stub returns zeros, which won't read real data. Need to implement:
- **Virtqueue setup** (descriptor table, available ring, used ring)
- **Block read requests** (virtio_blk_req header + data buffer)
- **Polling for completion** (check used ring)

Reference: `kmazarin/device/virtio/block/` has existing VirtIO implementation

### 2. Test FAT32 Read
- Boot sector read (LBA 0)
- FAT table read
- Root directory read
- Verify kmazarin.elf is found

### 3. Move to Task #4
Load kmazarin.elf from FAT32 filesystem

## Important Notes

### Memory Regions (OpenSBI PMP)
- RAM: 0x80000000 - 0xFFFFFFFF (2GB)
- VirtIO MMIO: 0x10001000 - 0x10008000
- UART: 0x10000000
- FDT: 0xFFE00000 (passed to kmazarin via auxv)

### Debug Markers
Look for these characters in output to trace execution:
- 'R' - After Task #2 complete
- 'M' - Before "Mounting FAT32..."
- 'N' - After MountFilesystem returns (not reached yet)
- 'O' - After successful mount (not reached yet)

### Git Status
Last commit: "WIP: RISC-V Task #2 - Switch to VirtIO MMIO transport" (d553155)

Untracked files:
- None (all changes committed)

## Task List

- [x] **Task #1**: Platform Initialization (COMPLETE)
- [x] **Task #2**: VirtIO Block Device (COMPLETE)
- [ ] **Task #3**: Mount FAT32 filesystem ⬅️ **DEBUG THIS**
- [ ] **Task #4**: Load kmazarin.elf from disk
- [ ] **Task #5**: Set up kernel page tables (Sv48)
- [ ] **Task #6**: Jump to kmazarin entry point

## Success Criteria for Task #3

When FAT32 mount works, you should see:
```
Mounting FAT32...
DEBUG: ReadBlockVirtIO called for LBA 0x0
[more block reads...]
FAT32 mounted OK
```

Then Task #4 begins (kernel loading).

---

**Previous session ended at token count ~117k with FAT32 hang unresolved.**
