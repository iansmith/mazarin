# RISC-V VirtIO Investigation - PCI vs MMIO Transport (2026-02-12)

## Problem Summary

VirtIO MMIO block I/O on RISC-V diplomat **times out despite perfect configuration**:
- All VirtIO spec requirements met (feature negotiation, queue layout, register order)
- Device never updates `used.idx`
- No interrupt signaled

## Root Cause Analysis

### Transport Mismatch Discovered

**Working configurations (ARM64/x86_64)**:
```bash
-device virtio-blk-pci,drive=drive-virtio-disk0  # PCI transport
```

**Failing configuration (RISC-V diplomat)**:
```bash
-device virtio-blk-device,drive=drive-virtio-disk0  # MMIO transport
```

**Key Finding**: All working configurations use **PCI transport**, while failing RISC-V uses **MMIO transport**.

## Investigation Priorities

### Priority 1: Switch to virtio-blk-pci ✅

**Hypothesis**: QEMU RISC-V may have:
1. Better PCI support than MMIO v1 legacy mode
2. MMIO v1 bugs or incomplete implementation
3. PCI as the primary/tested path

**Implementation**:
- Modified `Taskfile.yml` to support both PCI and MMIO via `VIRTIO_TRANSPORT` variable
- Default: `pci` (matches ARM64/x86_64)
- Fallback: `mmio` (legacy)

**Test command**:
```bash
# PCI transport (default)
$GO tool task run-diplomat-riscv64 TIMEOUT=10

# MMIO transport (fallback)
$GO tool task run-diplomat-riscv64 TIMEOUT=10 VIRTIO_TRANSPORT=mmio
```

### Priority 2: Enable QEMU VirtIO Tracing ✅

Added to Taskfile:
```bash
-trace 'virtio_*' -trace 'virtqueue_*' 2>/tmp/diplomat-riscv64-trace.log
```

This will show:
- Device initialization events
- Queue notifications received
- Descriptor processing
- Interrupt delivery

**View trace**:
```bash
cat /tmp/diplomat-riscv64-trace.log
```

### Priority 3: PCI Support Requirements

**Current Status**: Diplomat has **NO PCI initialization code** for RISC-V.

If PCI transport test fails with "no device found", we need to add:
1. **PCI ECAM discovery** - Read ECAM base from FDT
2. **PCI config space access** - Read device BARs
3. **VirtIO PCI capability parsing** - Find VirtIO registers
4. **VirtIO PCI initialization** - Different from MMIO

**Reference**: Check kmazarin RISC-V PCI code if available.

### Priority 4: Compare with Working Code

**RISC-V direct kernel boot** (line 1012 in Taskfile.yml):
```bash
-device virtio-blk-pci,drive=flock0,bus=pcie.0
```

Uses PCI transport. If kmazarin has working RISC-V PCI VirtIO code, we can adapt it for diplomat.

### Priority 5: Simpler Descriptor Chain

**Current**: 3-descriptor chain (header → data → status)
**Alternative**: Try single descriptor with embedded header+data+status

Will implement if PCI transport also fails.

## Expected Outcomes

### Scenario A: PCI Works Immediately ✅
- Device found via MMIO scan (QEMU may dual-expose)
- VirtIO handshake succeeds
- Block I/O completes
- **Action**: Document and move to Task #4 (FAT32 load kmazarin)

### Scenario B: PCI Device Not Found ❌
- MMIO scan finds no device (PCI-only exposure)
- Need PCI ECAM init code
- **Action**: Add PCI support to diplomat RISC-V

### Scenario C: PCI Init Fails ❌
- Device found but handshake/queue setup fails
- Check trace logs for clues
- **Action**: Debug PCI-specific issues

### Scenario D: PCI Still Times Out ❌
- Device found, init succeeds, but I/O times out
- Same symptom as MMIO
- **Action**: Try simpler descriptor chain, check QEMU version, try different features

## Files Modified

- `Taskfile.yml`:
  - Lines 959-983: `run-diplomat-riscv64-background` task
  - Added `VIRTIO_TRANSPORT` variable (pci/mmio)
  - Added VirtIO tracing (`-trace` flags)
  - Conditional device selection

## Next Steps

1. **Test PCI transport** (this session):
   ```bash
   export GOTOOLCHAIN=auto
   export GO=/opt/homebrew/Cellar/go/1.25.5/libexec/bin/go
   export QEMU=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-aarch64

   $GO tool task run-diplomat-riscv64 TIMEOUT=10
   $GO tool safe-serial-read /tmp/diplomat-riscv64-serial.log | tail -80
   cat /tmp/diplomat-riscv64-trace.log
   ```

2. **Analyze results**:
   - If device found → Check if I/O completes
   - If not found → Add PCI ECAM support
   - If times out → Check trace logs

3. **Compare MMIO vs PCI**:
   ```bash
   # Test MMIO with tracing
   $GO tool task run-diplomat-riscv64 TIMEOUT=10 VIRTIO_TRANSPORT=mmio
   cat /tmp/diplomat-riscv64-trace.log
   ```

4. **Document findings** and decide next action.

## References

- [VirtIO PCI Spec](https://docs.oasis-open.org/virtio/virtio/v1.0/cs01/virtio-v1.0-cs01.html#x1-1040002)
- [VirtIO MMIO Spec](https://docs.oasis-open.org/virtio/virtio/v1.0/cs01/virtio-v1.0-cs01.html#x1-1090002)
- [QEMU RISC-V virt machine](https://www.qemu.org/docs/master/system/riscv/virt.html)
- Taskfile.yml:1012 - RISC-V direct kernel boot with PCI

## Status

**Current**: Testing PCI transport (Priority 1)
**Blocked By**: None
**Blocks**: Task #4 (Load kmazarin.elf from FAT32)
