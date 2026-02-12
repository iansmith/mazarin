# RISC-V Diplomat Phase 2 - Task #3: VirtIO Block I/O Debug

## Session Continuation Prompt

Continue debugging RISC-V VirtIO block I/O timeout issue. Virtqueue is initialized and device notified, but used ring never updates.

## Previous Session Summary

### Major Breakthrough: Error Interface Allocation Issue SOLVED ✅

**Root Cause Identified**: Error interface conversions trigger heap allocation on RISC-V during early boot, causing hangs before runtime is fully initialized.

**Solution Implemented**:
1. Created `ReadBlockVirtIONoError()` - no error interface return
2. Used global variables (`globalFS`, `bootSectorBuf`) - avoid local allocation
3. Created `ParseBPBStandalone()` - standalone function, no method calls
4. Direct function calls - bypass `plat` struct function pointers
5. Helper functions: `CopyBootSector()`, `FATBuffer()` for safe access

**Result**: Boot sequence no longer hangs! Successfully calls block read function.

Commits:
- `a3fb6c8`: WIP: RISC-V FAT32 mount - breakthrough on error interface allocation
- `5f761a5`: WIP: RISC-V VirtIO block I/O - virtqueue implementation (timeout issue)

---

## Current Problem: VirtIO Device Timeout

### What's Working
✅ Virtqueue structures allocated (static, no heap)
✅ Initialization completes successfully
✅ Descriptor chain configured (3 descriptors)
✅ Queue addresses set in MMIO registers
✅ QUEUE_READY=1, device notified via QUEUE_NOTIFY
✅ No crashes, clean debug output

### What's Failing
⚠️ Device never processes request - used ring index stays at 0
⚠️ Timeout after polling 1,000,000 iterations
⚠️ No response from device whatsoever

### Debug Output
```
VirtIO virtqueue initialized
  Desc table: 0x80303E80
  Avail ring: 0x80303900
  Used ring:  0x80303CA0
  Request setup: type=0x0 sector=0x0
  Avail ring: idx=0x0 -> 0x1
  Device notified, polling... (used.idx=0x0 last=0x0)
  After poll: used.idx=0x0 timeout=0x0
ERROR: VirtIO block read timeout
```

### Descriptor Chain Setup
```c
// Descriptor 0: Request header (device reads)
desc[0].addr  = &virtioReq (type, reserved, sector)
desc[0].len   = 16
desc[0].flags = NEXT
desc[0].next  = 1

// Descriptor 1: Data buffer (device writes)
desc[1].addr  = &virtioReq.data
desc[1].len   = 512
desc[1].flags = WRITE | NEXT
desc[1].next  = 2

// Descriptor 2: Status byte (device writes)
desc[2].addr  = &virtioReq.status
desc[2].len   = 1
desc[2].flags = WRITE
desc[2].next  = 0
```

### Request Structure
```go
type virtioBlkReq struct {
    reqType   uint32  // VIRTIO_BLK_T_IN = 0
    reserved  uint32  // 0
    sector    uint64  // LBA
    data      [512]byte
    status    uint8   // 0xFF initially
    _padding  [7]uint8
}
```

---

## Likely Root Causes (Priority Order)

### 1. **Missing Memory Barriers** (MOST LIKELY)
VirtIO requires memory barriers to ensure:
- Descriptor writes complete before updating avail.idx
- Avail.idx write completes before QUEUE_NOTIFY
- Used.idx read happens after device update

**RISC-V fence instruction needed**: `fence rw,rw`

**Fix**: Add assembly fences in `readBlockVirtIO()`:
```go
// After setting up descriptors, before avail.idx update
asm("fence rw,rw")

// After avail.idx update, before QUEUE_NOTIFY
asm("fence rw,rw")
```

### 2. **Queue Alignment Requirements**
VirtIO spec requires:
- Descriptor table: 16-byte aligned
- Available ring: 2-byte aligned
- Used ring: 4-byte aligned

**Current**: Go global variables (alignment unknown)

**Fix**: Use `//go:align 16` or allocate from aligned memory

### 3. **Physical vs Virtual Addresses**
RISC-V diplomat runs with MMU disabled (identity mapping), but device might expect physical addresses.

**Check**: Are addresses in correct range? (0x80000000+ for QEMU virt)

### 4. **Descriptor Chain Errors**
- Wrong flags (WRITE on wrong descriptors?)
- Incorrect lengths
- Chain termination issue

### 5. **Device Not Ready**
Need to verify device state after QUEUE_READY=1:
- Check QUEUE_READY register reads back as 1
- Check device STATUS register still shows DRIVER_OK
- Verify QUEUE_SEL stayed at 0

---

## Debugging Strategy

### Phase 1: Add Memory Barriers
1. Add `fence` instructions before/after critical points
2. Create helper function in assembly:
   ```asm
   TEXT ·memoryBarrier(SB), NOSPLIT, $0-0
       FENCE
       RET
   ```
3. Call before avail.idx update, after update, after used.idx read

### Phase 2: Verify Device State
1. After QUEUE_READY=1, read it back to confirm
2. Check STATUS register for any error flags
3. Read INTERRUPT_STATUS to see if device tried to signal
4. Verify QUEUE_SEL still = 0

### Phase 3: Simplify Descriptor Chain
Try single-descriptor test:
```c
// Single descriptor: just read header (no data)
desc[0].addr  = &virtioReq
desc[0].len   = sizeof(virtioReq)
desc[0].flags = WRITE
desc[0].next  = 0
```

### Phase 4: Check Alignment
Print descriptor addresses with alignment info:
```go
printHex(uintptr(unsafe.Pointer(&virtqDescTable)) % 16)  // Should be 0
```

### Phase 5: Reference Implementation
Compare with working VirtIO code in:
- `kmazarin/device/virtio/block/` - kmazarin VirtIO driver
- Linux kernel virtio-blk driver
- QEMU virtio-mmio spec

---

## Key Files

### Modified in Previous Sessions
- `diplomat/main/boot_riscv64.go`:
  - Lines 310-372: Static virtqueue structures
  - Lines 374-461: `initVirtqueue()`
  - Lines 463-526: `readBlockVirtIO()`

- `diplomat/main/platform_riscv64.go`:
  - Lines 170-184: `ReadBlockVirtIONoError()` wrapper

- `diplomat/main/main.go`:
  - Lines 479-481: Global `bootSectorBuf`
  - Lines 507-548: `fat32MountOrDie()` allocation-free wrapper

- `shared/fs/fat32/fat32.go`:
  - Lines 283-339: `ParseBPBStandalone()`
  - Lines 287: `CopyBootSector()`, `FATBuffer()`

### VirtIO MMIO Register Offsets
Located in `boot_riscv64.go` lines 108-141:
- `VIRTIO_MMIO_QUEUE_SEL = 0x030`
- `VIRTIO_MMIO_QUEUE_NOTIFY = 0x050`
- `VIRTIO_MMIO_QUEUE_DESC_LOW/HIGH = 0x080/0x084`
- `VIRTIO_MMIO_QUEUE_AVAIL_LOW/HIGH = 0x090/0x094`
- `VIRTIO_MMIO_QUEUE_USED_LOW/HIGH = 0x0A0/0x0A4`

---

## Test Commands

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

---

## Expected Success Output

When VirtIO I/O works, you should see:
```
VirtIO virtqueue initialized
  Desc table: 0x80303E80
  Avail ring: 0x80303900
  Used ring:  0x80303CA0
  Request setup: type=0x0 sector=0x0
  Avail ring: idx=0x0 -> 0x1
  Device notified, polling... (used.idx=0x0 last=0x0)
  After poll: used.idx=0x1 timeout=999999  ← CHANGED!
6789PQRSTUV  ← FAT32 parsing markers
OFAT32 mounted OK
La  ← LoadKernel started
```

Then move to **Task #4**: Load kmazarin.elf from FAT32 filesystem.

---

## Architecture Context

- **Platform**: QEMU virt (RISC-V 64-bit)
- **Boot flow**: OpenSBI → diplomat (0x80200000) → kmazarin (0x43800000)
- **Mode**: S-mode, no UEFI, MMU disabled (identity mapping)
- **VirtIO device**: 0x10008000, type=2 (block), version=1, MMIO transport
- **Disk**: build/disk-riscv64.img (FAT32, 257 MB, kmazarin.elf at /EFI/Linux/)

---

## Important Memory Notes (from MEMORY.md)

**RISC-V Early Boot Allocation Rules**:
1. NO `new()` calls - heap not initialized
2. NO string assignments - trigger allocation
3. NO error interface returns - interface conversion allocates
4. NO interface method calls - vtable dispatch allocates
5. NO method calls on structs with interface fields - triggers allocation
6. NO local variables containing interfaces - allocation

**Solutions**:
- Use global structs
- Use standalone functions returning bool (not error)
- Call concrete functions directly
- Use `plat.PrintChar()` for debug output
- Use `uartDebugMarker(byte)` for NOSPLIT markers

---

## Git Status

Last commits:
- `5f761a5`: WIP: RISC-V VirtIO block I/O - virtqueue implementation (timeout issue)
- `a3fb6c8`: WIP: RISC-V FAT32 mount - breakthrough on error interface allocation

Branch: `riscv-boot`

---

**Start here**: Add memory barriers (fence instructions) before/after avail.idx updates and QUEUE_NOTIFY, then test. If that doesn't work, verify device state registers, then simplify descriptor chain.
