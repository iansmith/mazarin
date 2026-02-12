# RISC-V VirtIO Block I/O Investigation Results (2026-02-12)

## Summary

Device **still times out** despite fixing ALL identified issues. This appears to be a deeper QEMU-specific or descriptor-related problem.

## Issues Fixed ✅

### 1. **Feature Negotiation** - FIXED
**Problem**: Negotiating VIRTIO_F_VERSION_1 (VirtIO 1.0 protocol) while using MMIO v1 (legacy transport)
**Solution**: Detect MMIO version and negotiate appropriately:
- MMIO v1: No VERSION_1 feature (legacy mode)
- MMIO v2: Negotiate VERSION_1 (modern mode)

**Files Changed**: `diplomat/main/boot_riscv64.go:284-321`

### 2. **Missing QUEUE_NUM Write** - FIXED
**Problem**: Not writing to QUEUE_NUM register before QUEUE_PFN
**Root Cause**: QEMU expects QUEUE_NUM write to trigger `virtio_queue_update_rings()`
**Solution**: Added QUEUE_NUM write in correct sequence

**Files Changed**: `diplomat/main/boot_riscv64.go:504-514`

### 3. **Incorrect Register Write Order** - FIXED
**Problem**: Writing QUEUE_ALIGN before QUEUE_NUM
**Correct Order** (per Linux driver):
1. QUEUE_SEL
2. QUEUE_NUM
3. QUEUE_ALIGN
4. QUEUE_PFN

**Files Changed**: `diplomat/main/boot_riscv64.go:516-539`

### 4. **Queue Init After DRIVER_OK** - FIXED
**Problem**: Setting DRIVER_OK before configuring queues
**Correct Sequence** (per VirtIO spec):
1. FEATURES_OK
2. **Queue setup** ← Must be here!
3. **DRIVER_OK** ← Only after queues ready

**Files Changed**: `diplomat/main/boot_riscv64.go:299-310`

## Current Status

### What's Working ✅
- Feature negotiation (correct for MMIO v1)
- QUEUE_NUM/ALIGN/PFN writes in Linux driver order
- Queue init before DRIVER_OK
- Memory barriers at all critical points
- 4KB alignment verified
- Virtqueue layout correct (per spec)
- Device STATUS progression correct (0xB → 0xF)

### What's Still Failing ❌
- Device never updates used.idx
- No interrupt signaled (INTR_STATUS=0x0)
- Request times out after 1M iterations
- Device appears completely unresponsive

### Debug Output (Latest Run)
```
Device offers: low=0x31006ED4 high=0x0
MMIO v1 (legacy) - no VERSION_1 feature
Driver accepts: low=0x31006ED4 high=0x0
FEATURES_OK...Verify...Queues...
  Queue max: 0x400 (using 0x10)
  Device status before queue config: 0xB
  Device status after queue config: 0xB
VirtIO virtqueue initialized (MMIO v0x1)
  Desc table: 0x8030B000
  Avail ring: 0x8030B100
  Used ring:  0x8030C000
  QUEUE_PFN=0x8030B (readback: 0x8030B)
  QUEUE_SEL=0x0 STATUS=0xB INTR=0x0
DRIVER_OK...Complete!
...
  Request setup: type=0x0 sector=0x0
  Desc[0]: addr=0x80304D20 len=0x10 flags=0x1
  Desc[1]: addr=0x80304D30 len=0x200 flags=0x3
  Desc[2]: addr=0x80304F30 len=0x1 flags=0x2
  Avail ring: idx=0x0 -> 0x1
  Device notified, polling...
    Before: used.idx=0x0 last=0x0
    Post-notify: STATUS=0xF INTR=0x0
  After poll: used.idx=0x0 timeout=0x0
ERROR: VirtIO block read timeout
```

## Remaining Possibilities

### 1. **Descriptor Address Issues**
Descriptors point to 0x80304xxx range. Possible issues:
- Guest-physical vs bus address translation?
- RISC-V S-mode address translation quirk?
- QEMU not able to access these addresses?

### 2. **QEMU VirtIO MMIO v1 Bug/Limitation**
- QEMU RISC-V may have incomplete MMIO v1 support
- virtio-blk-device may require additional setup
- Try: virtio-blk-pci instead of virtio-blk-device?

### 3. **Notification Mechanism Issue**
- QUEUE_NOTIFY write not reaching device?
- Need kick/doorbell operation?
- Missing memory fence type?

### 4. **Descriptor Chain Format**
Current setup:
```
Desc[0]: req header (type, sector) - device reads
Desc[1]: data buffer (512 bytes) - device writes
Desc[2]: status byte - device writes
```
Possible issues:
- Wrong descriptor format for legacy mode?
- Buffer addresses not contiguous enough?
- Status byte needs different flags?

### 5. **Device-Specific Config**
Block device may need config space setup:
- Read capacity from VIRTIO_MMIO_CONFIG + 0?
- Set block size somewhere?
- Enable specific block features?

## Next Investigation Steps

### High Priority
1. **Try virtio-blk-pci** instead of virtio-blk-device
   ```bash
   -device virtio-blk-pci,drive=drive-virtio-disk0,addr=0x10
   ```

2. **Enable QEMU VirtIO tracing**
   ```bash
   -trace 'virtio_*' -trace 'virtqueue_*' 2>trace.log
   ```

3. **Compare with working x86_64/ARM64**
   - Check if they use MMIO or PCI
   - Compare descriptor setup
   - Check notification mechanism

4. **Try simpler descriptor chain**
   - Single descriptor instead of 3-descriptor chain
   - Verify device accepts it

### Medium Priority
5. **Check Linux virtio-mmio.c more carefully**
   - Look for RISC-V-specific quirks
   - Check for additional MMIO v1 requirements
   - Verify we're not missing any writes

6. **Examine QEMU source more closely**
   - Check virtio_mmio_write() for all registers
   - See if there's additional state needed
   - Look for legacy mode quirks

7. **Try different QEMU versions**
   - Maybe 10.2.0 has a regression?
   - Try older/newer versions

### Low Priority
8. **Check SBI/OpenSBI requirements**
   - Does OpenSBI need to setup something for VirtIO?
   - Check FDT for missing info

9. **Memory access verification**
   - Verify descriptor addresses are accessible
   - Check for alignment requirements beyond 16-byte

10. **Feature combinations**
    - Try accepting different feature subsets
    - Maybe device requires specific features enabled?

## Key Learnings

1. **MMIO v1 vs v2** - Critical transport version differences
2. **Feature negotiation** - Must match transport version
3. **Init sequence** - Queues BEFORE DRIVER_OK is mandatory
4. **Register order** - Linux driver order is canonical
5. **QUEUE_NUM write** - Required to activate queue in QEMU

## References

- [VirtIO MMIO Spec v1.0](https://docs.oasis-open.org/virtio/virtio/v1.0/csprd01/virtio-v1.0-csprd01.html)
- [Linux virtio_mmio.c](https://github.com/torvalds/linux/blob/master/drivers/virtio/virtio_mmio.c)
- [QEMU virtio-mmio.c](https://github.com/qemu/qemu/blob/master/hw/virtio/virtio-mmio.c)

## Files Modified

- `diplomat/main/boot_riscv64.go`:
  - Lines 116-124: VirtIO block feature constants
  - Lines 179: virtioMMIOVersion tracking
  - Lines 254: Save MMIO version during scan
  - Lines 284-321: Fixed feature negotiation
  - Lines 299-310: Queue init before DRIVER_OK
  - Lines 447-637: Queue initialization reorg
  - Lines 504-539: QUEUE_NUM write + correct order

- `diplomat/main/memory_barrier_riscv64.s`: Memory barrier function

## Test Command

```bash
export GOTOOLCHAIN=auto
export GO=/opt/homebrew/Cellar/go/1.25.5/libexec/bin/go
export QEMU=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-aarch64

$GO tool task diplomat-riscv64
$GO tool task run-diplomat-riscv64 TIMEOUT=10
$GO tool safe-serial-read /tmp/diplomat-riscv64-serial.log | tail -60
```

## Conclusion

We've successfully identified and fixed **four major bugs** in the VirtIO MMIO initialization:
1. Feature negotiation mismatch
2. Missing QUEUE_NUM write
3. Incorrect register order
4. Queues configured after DRIVER_OK

Despite all fixes being correct per spec, Linux driver, and QEMU source, the device **still doesn't respond**.

This suggests the problem may be:
- **QEMU-specific quirk** with RISC-V + virtio-blk-device + MMIO v1
- **Address translation issue** between S-mode addresses and device-visible addresses
- **Missing device-specific setup** not documented in general VirtIO spec

**Recommended next step**: Try virtio-blk-pci instead of virtio-blk-device, or enable QEMU tracing to see if QEMU is receiving our notifications.
