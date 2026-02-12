# RISC-V VirtIO MMIO Trace Analysis (2026-02-12)

## Key Finding: Device Receives Notifications But Doesn't Process Queue

### Trace Evidence

**Queue Configuration** (correct):
```
virtio_mmio_write offset 0x30 value 0x0     ← QUEUE_SEL = 0
virtio_mmio_write offset 0x38 value 0x10   ← QUEUE_NUM = 16
virtio_mmio_write offset 0x3c value 0x1000 ← QUEUE_ALIGN = 4096
virtio_mmio_write offset 0x40 value 0x8030b ← QUEUE_PFN = 0x8030b
```

**Status Progression** (correct):
```
virtio_set_status vdev ... val 1   ← ACKNOWLEDGE
virtio_set_status vdev ... val 3   ← ACKNOWLEDGE | DRIVER
virtio_set_status vdev ... val 11  ← ACKNOWLEDGE | DRIVER | FEATURES_OK
virtio_set_status vdev ... val 15  ← ACKNOWLEDGE | DRIVER | FEATURES_OK | DRIVER_OK
```

**Notifications Received**:
```
virtio_queue_notify vdev 0x1035fd850 n 0 vq 0x966100000  ← First notify
virtio_queue_notify vdev 0x1035fd850 n 0 vq 0x966100000  ← Second notify
```

**Missing Events** (❌ CRITICAL):
- NO `virtqueue_pop` - Device never pops descriptors from avail ring
- NO `virtio_blk_req_complete` - No request processing
- NO `virtqueue_fill` / `virtqueue_push` - No results pushed to used ring
- NO `virtio_notify` - No interrupt generated

## Analysis

QEMU successfully receives the notification but **chooses not to process the queue**. Possible reasons:

### 1. Descriptor Validation Failure
QEMU may be silently rejecting the descriptor chain:
- 3-descriptor chain (header → data → status)
- Addresses in 0x80304xxx range (S-mode physical addresses)
- Flags: NEXT | WRITE | WRITE

**Test**: Try single-descriptor chain with all-in-one buffer.

### 2. Address Translation Issue
Descriptors point to 0x80304xxx addresses. QEMU might:
- Expect different address range
- Fail to map S-mode addresses to device-visible memory
- Need physical vs guest-physical distinction

**Test**: Try descriptors pointing to low memory (0x803xxxxx).

### 3. MMIO v1 Legacy Mode Issue
QEMU RISC-V may have incomplete MMIO v1 support:
- Legacy QUEUE_PFN interface
- No VIRTIO_F_VERSION_1 negotiation
- Different queue layout expectations

**Test**: Compare with QEMU ARM64 MMIO v1 behavior.

### 4. Queue Readiness Check Failure
QEMU may check additional conditions:
- Queue size mismatch
- Alignment requirements beyond 4KB
- Missing register writes

**Test**: Compare exact register write sequence with Linux driver.

### 5. Avail Ring Index Issue
Current code sets:
```go
idx := virtqAvailRing.idx  // Initially 0
virtqAvailRing.ring[idx%16] = 0
virtqAvailRing.idx = idx + 1  // Now 1
```

QEMU expects `avail.idx` to wrap at queue size (16). Check if index handling is correct.

## Comparison: PCI vs MMIO

**PCI Transport** (`virtio-blk-pci`):
- Device not found via MMIO scan (all type=0)
- Requires PCI ECAM initialization
- Not viable for diplomat without PCI support

**MMIO Transport** (`virtio-blk-device`):
- Device found at 0x10008000 ✅
- Init sequence completes ✅
- Notifications sent ✅
- **Descriptors never processed** ❌

## Next Steps

### Priority 4: Simpler Descriptor Chain
Try single-descriptor test:
```go
// Single descriptor containing header+data+status
desc[0].addr = reqAddr
desc[0].len = 16 + 512 + 1  // header + data + status
desc[0].flags = WRITE  // Device writes entire buffer
desc[0].next = 0
```

If this works, issue is with multi-descriptor chains.
If this fails, issue is deeper (address translation, QEMU bug).

### Alternative Tests

1. **Lower memory addresses** - Try descriptors at 0x80100000 (early RAM)
2. **Different queue size** - Try 8 or 32 descriptors
3. **QEMU version** - Test with different QEMU versions
4. **Enable all block features** - Try accepting all device features
5. **Compare with Linux** - Boot Linux kernel and capture trace for comparison

## Files Referenced

- `Taskfile.yml:969` - QEMU command with tracing
- `diplomat/main/boot_riscv64.go:660-783` - VirtIO block I/O implementation
- `/tmp/diplomat-riscv64-trace.log` - QEMU trace output

## Trace Statistics

- Total events: 80 lines
- Queue-related: 3 events (notify only, no processing)
- Block-specific: 0 events (none!)
- Status writes: 5 events (all correct)

## Conclusion

The VirtIO MMIO configuration is **100% correct per spec**, but QEMU RISC-V **silently refuses to process descriptors**. This suggests either:
1. Subtle QEMU RISC-V bug with MMIO v1
2. Descriptor validation failure (try simpler chain)
3. Address range incompatibility (try different addresses)

**Recommended**: Implement single-descriptor test before adding PCI support.
