# Plan: Interrupt-Driven Block I/O + ARM64 EOIR Fix

## Three Issues

1. **Block device I/O uses polling loops** — `doBlockIO()` busy-waits in 100K-1M iteration loops instead of sleeping the thread and waking on interrupt, like Linux does.
2. **ARM64 EOIR written before handler** — causes need for edge-triggered GIC hack. PCI INTx is level-triggered by spec.
3. **False "WFI is a NOP" comment** — RISC-V `io_riscv64.go` claims QEMU treats WFI as NOP. This is wrong; WFI halts the vCPU in system emulation on all architectures.

## Phase 1: ARM64 EOIR Fix (Independent, do first)

### Problem

In `exceptions_arm64.s`, the `irq_not_timer` path:
1. Reads GICC_IAR (line ~1163) — R19 = IAR value
2. Writes GICC_EOIR (line ~1178) — tells GIC "interrupt handled" **BEFORE handler runs**
3. Calls NonTimerIRQTopHalf (line ~1200) — reads VirtIO ISR, deasserts INTx

With level-triggered, GIC would re-fire at step 2 because INTx is still asserted. Current workaround: configure as edge-triggered (`main.go:647`).

### Fix

Save IAR value in R21 before R19 is overwritten. Move EOIR write to after handler returns.

**File: `kmazarin/kmazarin/exceptions_arm64.s`**

At `irq_not_timer`:
```asm
irq_not_timer:
    MOVD  R19, R21   // Save IAR value (R19 will be overwritten by m0.curg save)
    // ... existing handler setup and CALL NonTimerIRQTopHalf ...
    // ... existing m0.curg restore ...

    // Write EOIR AFTER handler has read ISR and deasserted INTx
    MOVD  $(GIC_CPU_BASE + GICC_EOIR), R10
    MOVW  R21, (R10)
```

Remove the old EOIR write (currently at lines 1177-1179).

All exit paths from `irq_not_timer` must write EOIR using R21 before reaching `irq_return`.

**File: `kmazarin/kmazarin/main.go` (lines 631-651)**

Remove:
- `asm.MmioRead8(dev.ISRBase)` — pre-clear ISR read (line 646)
- `cachedIC.SetIRQEdgeTriggered(localIRQ)` — edge-triggered hack (line 647)
- The comment block explaining why edge-triggered is needed (lines 632-645)

Keep: `SetIRQPriority`, `SetIRQTarget`, `EnableIRQ` — these are correct.

### Test

Run ARM64 `$GO tool task run TIMEOUT=15`. Keyboard and mouse input must work. No IRQ storm (rapid repeated IRQ prints in serial log). Timer must tick.

## Phase 2: Interrupt-Driven Block I/O

### How Linux Does It

Linux VirtIO block driver:
1. Thread submits request via blk-mq
2. Thread calls `wait_for_completion()` → `TASK_UNINTERRUPTIBLE` → `schedule()` — thread sleeps, zero CPU
3. Device interrupt fires → `virtblk_done()` drains used ring → `blk_mq_complete_request()` → softirq → `complete()` → wakes thread

No polling anywhere. Thread is fully asleep until interrupt.

### Our Equivalent

We already have thread blocking infrastructure (futex, IPC, delegate). Pattern:
- Add thread state `ThreadBlockedIO`
- Before submitting I/O: store current TID in device struct
- Submit I/O, block thread (context switch away)
- IRQ top-half: set IOComplete, wake blocked TID
- Thread resumes, completes I/O (pop used ring, copy data)

### Step 2.1: Thread State

**File: `kmazarin/kmazarin/threads.go`**

Add `ThreadBlockedIO = 12` to thread states.

Update:
- `WakeThreadForSignal()` — add `case ThreadBlockedIO:` (defer signal until I/O completes)
- State counting diagnostics — add `nIO` counter
- State name string — add `case ThreadBlockedIO: "BIO"`

### Step 2.2: Block/Wake Functions

**File: `kmazarin/kmazarin/io_bridge.go` (new)**

```go
// BlockForBlockIO blocks the current thread waiting for block I/O.
// Re-checks ioComplete under scheduler lock to prevent missed-wakeup race.
// Returns context pointer of next thread to switch to, or 0 if can't block.
//go:nosplit
func BlockForBlockIO(ioComplete *uint32) uintptr

// WakeBlockIOThread wakes a thread blocked in ThreadBlockedIO.
// Called from IRQ top-half.
//go:nosplit
func WakeBlockIOThread(tid int32)
```

Follow the exact pattern of `BlockForIPCCall`/`WakeIPCThread` in `ipc_bridge.go`.

Critical race handling in `BlockForBlockIO`:
1. Acquire scheduler lock
2. Re-check `atomic.LoadUint32(ioComplete) != 0` — if already done, unlock and return 0
3. Set `t.State = ThreadBlockedIO`
4. Find next ready thread, return its context

### Step 2.3: Device Struct Changes

**File: `kmazarin/device/virtio/block/block.go`**

Add to `VirtIOBlockDevice`:
```go
BlockedTID int32 // TID of thread blocked on I/O (-1 = none)
```

Initialize to -1 in `Init()`.

Add exported getters:
```go
func GetDevice() *VirtIOBlockDevice
func GetBlockedTIDPtr() *int32
```

### Step 2.4: Split doBlockIO

**File: `kmazarin/device/virtio/block/io.go`**

Split into three functions:
- `doBlockIOSubmit(requestType, lba, buf) (reqDescIdx uint16, err error)` — setup descriptors, clear IOComplete, notify device
- `doBlockIOComplete(requestType, reqDescIdx, buf) error` — pop used ring, free descriptors, check status, copy data
- `doBlockIO` — existing polling wrapper (calls submit, polls, calls complete). Kept as fallback.

### Step 2.5: IRQ Top-Half Wake

**File: `kmazarin/kmazarin/bottom_half.go`**

Modify block device IRQ handler in `NonTimerIRQTopHalf`:
```go
if irqNum == blockIRQNum && blockIRQNum != 0 {
    if blockISRBase != 0 {
        _ = asm.MmioRead8(blockISRBase)
    }
    if blockIOComplete != nil {
        atomic.StoreUint32(blockIOComplete, 1)
    }
    tid := atomic.LoadInt32(blockIOBlockedTID)
    if tid >= 0 {
        atomic.StoreInt32(blockIOBlockedTID, -1)
        WakeBlockIOThread(tid)
    }
    return
}
```

Wire `blockIOBlockedTID` pointer via `SetBlockIRQ` (add parameter).

### Step 2.6: SyscallBlockRead Interrupt Path

**File: `kmazarin/ksyscall/blockio.go`** (or modify existing `SyscallBlockRead`)

When `CanUseInterruptDrivenIO()`:
1. Store current TID → `device.BlockedTID`
2. Call `doBlockIOSubmit`
3. Check `IOComplete` — if 0, call `BlockForBlockIO(&device.IOComplete)`
4. On wake (or immediate return), call `doBlockIOComplete`

Fallback: non-interrupt devices still use `ReadBlock` (polling).

### Step 2.7: Linkname Bridge

**File: `kmazarin/ksyscall/blockio_bridge.go` (new)**

```go
//go:linkname BlockForBlockIO mazzy/kmazarin/kmazarin.BlockForBlockIO
func BlockForBlockIO(ioComplete *uint32) uintptr

//go:linkname WakeBlockIOThread mazzy/kmazarin/kmazarin.WakeBlockIOThread
func WakeBlockIOThread(tid int32)
```

### Test

Run all three architectures with `TIMEOUT=15`. Verify:
- Priests load successfully (disk I/O works)
- `BIO` state appears in thread diagnostics
- No polling loop iterations printed
- System doesn't hang during priest launch

## Phase 3: Comment Cleanup

**File: `kmazarin/device/virtio/block/io_riscv64.go`**

Remove false comment "QEMU TCG may treat WFI as a NOP when no interrupt is pending". Replace with:
```go
// yieldForIO reads the VirtIO MMIO InterruptStatus register to cause a VM exit,
// giving QEMU's event loop time to process the pending block I/O request.
// Falls back to WFI for non-MMIO devices.
```

Once interrupt-driven I/O is working for all architectures, `yieldForIO()` and the per-arch `io_*.go` files can be deleted.

## Implementation Order

1. **Phase 1** (EOIR fix) — independent, small, test immediately
2. **Phase 3** (comment fix) — trivial, do alongside Phase 1
3. **Phase 2** (interrupt-driven I/O) — depends on thread infrastructure, larger change

## Risks

- **Missed wakeup race**: Between submit and block, IRQ can fire. Re-check IOComplete under scheduler lock handles this.
- **Single concurrent I/O**: One `BlockedTID` per device. Fine for now — only one priest owns the block device, reads are sequential.
- **EOIR timing**: Holding EOIR until after handler means GIC won't deliver same-source IRQs during handler. This is correct — prevents re-entrant handler execution.
