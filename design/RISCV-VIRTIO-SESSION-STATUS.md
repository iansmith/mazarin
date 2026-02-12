# RISC-V VirtIO Block I/O - Session Status (2026-02-12)

## Current Status: LAYOUT FIXED - DEEPER ISSUE REMAINS

### What's Working ✅
1. **Memory barriers added** - FENCE instructions at all critical points
2. **VirtIO MMIO version 1 interface** - Switched from v2 to v1 (QUEUE_PFN instead of separate address registers)
3. **4KB alignment achieved** - Manual alignment using 8KB buffer
4. **Queue configuration accepted** - QUEUE_PFN reads back correctly (0x8030B)
5. **Device status preserved** - STATUS=0xF throughout queue configuration
6. **Descriptor chain setup** - Three descriptors configured correctly:
   - Desc[0]: addr=0x80304D20 len=0x10 flags=0x1 (NEXT) - request header
   - Desc[1]: addr=0x80304D30 len=0x200 flags=0x3 (WRITE|NEXT) - data buffer
   - Desc[2]: addr=0x80304F30 len=0x1 flags=0x2 (WRITE) - status byte

### What's Failing ❌
- **Device timeout** - used.idx stays at 0, device never processes request
- **Virtqueue layout now CORRECT** - Used ring at 0x8030C000 (4KB-aligned)
- **All configuration verified correct** - but device still doesn't respond

## Problem: Virtqueue Layout

VirtIO MMIO version 1 requires specific memory layout:
```
QueueAddress (QUEUE_PFN × 4096):
  descriptor_table[Queue Size]          ← 16 descriptors × 16 bytes = 256 bytes
  available_ring                        ← 6 + (2 × queue_size) bytes
  padding to 4KB boundary
  used_ring                             ← 6 + (8 × queue_size) bytes
```

### Current (INCORRECT) Layout
```
Desc table: 0x8030B000               ← QUEUE_PFN base
Avail ring: 0x8030B100 (+256 bytes)  ← Correct offset
Used ring:  0x8030B124 (+292 bytes)  ← WRONG! Overlaps with avail ring!
```

**Avail ring size**: 6 + (2 × 16) = 38 bytes (0x26)
- Starts: 0x8030B100
- Ends:   0x8030B126

**Used ring should start at 4KB boundary** (page-aligned):
- Should be: 0x8030C000 (next 4KB page)
- Currently: 0x8030B124 (overlaps!)

## Fix Required

In `initVirtqueue()`, update the layout calculation:

```go
// Current (wrong):
availOffset := descOffset + unsafe.Sizeof([16]virtqDesc{})
usedOffset := availOffset + unsafe.Sizeof(virtqAvail{})

// Should be (VirtIO v1 spec):
descSize := 16 * 16  // 16 descriptors × 16 bytes = 256 bytes
availSize := 6 + (2 * 16)  // 6 bytes header + 16×2 ring entries = 38 bytes

descOffset := alignedAddr - bufAddr
availOffset := descOffset + descSize  // 0x100
usedOffset := ((availOffset + availSize + 4095) & ^4095) - bufAddr  // Round up to 4KB
```

This will place:
- Desc table: 0x8030B000
- Avail ring: 0x8030B100
- Used ring:  0x8030C000 (4KB-aligned)

##Working Files

Modified files:
- `diplomat/main/boot_riscv64.go`:
  - Lines 383-392: virtqBuffer and pointer declarations
  - Lines 401-423: initVirtqueue() - manual alignment logic
  - Lines 463-480: Queue configuration with QUEUE_PFN
  - Lines 575-585: Descriptor chain debug output

- `diplomat/main/memory_barrier_riscv64.s`:
  - Memory barrier assembly function

## Debug Output (Last Run)

```
Queue max: 0x400 (using 0x10)
Device status before queue config: 0xF
Device status after queue config: 0xF
VirtIO virtqueue initialized (MMIO v1)
  Desc table: 0x8030B000
  Avail ring: 0x8030B100
  Used ring:  0x8030B124  ← PROBLEM: Should be 0x8030C000
  QUEUE_PFN=0x8030B (readback: 0x8030B)
  QUEUE_SEL=0x0 STATUS=0xF
  Request setup: type=0x0 sector=0x0
  Desc[0]: addr=0x80304D20 len=0x10 flags=0x1
  Desc[1]: addr=0x80304D30 len=0x200 flags=0x3
  Desc[2]: addr=0x80304F30 len=0x1 flags=0x2
  Avail ring: idx=0x0 -> 0x1
  Device notified, polling... (used.idx=0x0 last=0x0)
  After poll: used.idx=0x0 timeout=0x0
  INTR_STATUS=0x0 STATUS=0xF
ERROR: VirtIO block read timeout
```

## Latest Test (AFTER LAYOUT FIX)

```
Used ring:  0x8030C000  ← NOW CORRECT (4KB-aligned)!
QUEUE_PFN=0x8030B (readback: 0x8030B)
STATUS=0xF
Desc[0]: addr=0x80304D20 len=0x10 flags=0x1
Desc[1]: addr=0x80304D30 len=0x200 flags=0x3
Desc[2]: addr=0x80304F30 len=0x1 flags=0x2
ERROR: VirtIO block read timeout  ← Still times out!
```

## Remaining Possibilities

Since queue configuration is now PERFECT per VirtIO spec but device still doesn't respond:

1. **Feature negotiation** - Maybe device needs specific features acknowledged?
2. **Queue notification issue** - QUEUE_NOTIFY might not reach device
3. **Descriptor addresses** - Maybe guest-physical vs bus addresses?
4. **QEMU bug/limitation** - VirtIO MMIO v1 may have issues in QEMU RISC-V?
5. **Missing initialization step** - Something else needed before first I/O?

## Next Steps

1. **Add feature negotiation debug** - Print device features, check what's being negotiated
2. **Try simpler descriptor chain** - Single descriptor for testing
3. **QEMU tracing** - Enable VirtIO trace to see what QEMU sees:
   ```bash
   -trace 'virtio_*' -trace 'virtqueue_*'
   ```
4. **Compare with Linux** - Check Linux virtio_mmio.c initialization sequence
5. **Try VirtIO MMIO v2** - QEMU might have better v2 support?

## Key Learnings

1. **VirtIO MMIO v1 vs v2** - Critical difference in queue activation:
   - v1: QUEUE_PFN (single 4KB-aligned address)
   - v2: Separate DESC/AVAIL/USED registers + QUEUE_READY

2. **QUEUE_NUM in v1** - Read-only, use QUEUE_NUM_MAX

3. **4KB alignment in Go** - `//go:align` directive unreliable, use manual alignment

4. **Layout matters** - VirtIO spec defines exact memory layout, not just structure offsets

## References

- [VirtIO MMIO spec](https://docs.oasis-open.org/virtio/virtio/v1.0/csprd01/virtio-v1.0-csprd01.html)
- [Linux virtio_mmio.c driver](https://github.com/torvalds/linux/blob/master/drivers/virtio/virtio_mmio.c)
- Previous session: `design/RISCV-PHASE2-TASK3-VIRTIO.md`

## Test Commands

```bash
export GOTOOLCHAIN=auto
export GO=/opt/homebrew/Cellar/go/1.25.5/libexec/bin/go
export QEMU=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-aarch64

$GO tool task diplomat-riscv64
$GO tool task run-diplomat-riscv64 TIMEOUT=8
$GO tool safe-serial-read /tmp/diplomat-riscv64-serial.log | tail -50
```

---

**Next session**: Fix virtqueue layout padding, then test. If successful, move to Task #4 (load kmazarin.elf from FAT32).

