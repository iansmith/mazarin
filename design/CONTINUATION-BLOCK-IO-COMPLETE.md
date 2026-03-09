# Continuation Prompt: Interrupt-Driven Block I/O — Status & AMD64 Port

Paste this into a new Claude Code session to continue this work.

---

## Prompt

We just completed interrupt-driven block I/O for ARM64 and RISC-V. This document captures what was done, what works, design decisions made, and what remains for AMD64.

**Read these files for full context:**
- `design/INTERRUPT-DRIVEN-BLOCK-IO-PLAN.md` — original plan (Phases 1-3)
- `kmazarin/kmazarin/io_bridge.go` — BlockForBlockIO / WakeBlockIOThread
- `kmazarin/ksyscall/blockio.go` — SyscallBlockRead + blockReadInterrupt
- `kmazarin/device/virtio/block/io.go` — DoBlockIOSubmit / DoBlockIOComplete / doBlockIO
- `kmazarin/kmazarin/bottom_half.go` — NonTimerIRQTopHalf block device handler
- `kmazarin/kmazarin/plic_dispatch_riscv64.go` — RISC-V PLIC block handler (separate from NonTimerIRQTopHalf)

---

## Standing Rules

**No architecture changes without discussing with the user first.** This is a hard rule. The interrupt-driven block I/O change was discussed and approved before implementation. Keep all three architectures (ARM64, RISC-V, AMD64) as similar as possible — shared code paths, same thread state model, same blocking/waking pattern.

---

## What Was Implemented (All Complete, Tested)

### Phase 1: ARM64 EOIR Fix

**Problem:** `exceptions_arm64.s` wrote GICC_EOIR *before* the IRQ handler ran. PCI INTx is level-triggered — writing EOIR while INTx is still asserted causes re-fire. Previous workaround: configure GIC for edge-triggered.

**Fix:** Save IAR value in R21 at `irq_not_timer` (before R19 is overwritten by m0.curg save). Write EOIR *after* NonTimerIRQTopHalf returns using R21. All exit paths (`irq_skip_dispatch`, `irq_invalid`) flow through `irq_write_eoir` which uses R21. Timer path branches to `irq_return` (skips EOIR — timer writes its own EOIR earlier).

**Also fixed:** Removed `SetIRQEdgeTriggered` hack from `main.go`. Kept the ISR pre-clear (`asm.MmioRead8(dev.ISRBase)`) because VirtIO devices assert INTx during DRIVER_OK init, and without clearing it before `EnableIRQ`, the handler fires before `SetTopHalfDev` has been called → handler returns without reading ISR → IRQ storm.

### Phase 2: Interrupt-Driven Block I/O

**Pattern (matches IPC, futex, sleep blocking):**

1. `SyscallBlockRead` → `blockReadInterrupt` (in `ksyscall/blockio.go`)
2. Store current TID in `dev.BlockedTID` so IRQ top-half knows who to wake
3. `DoBlockIOSubmit` — set up VirtIO descriptors, clear `IOComplete`, notify device
4. `BlockForBlockIO(&dev.IOComplete)` — disable IRQs, lock scheduler, re-check IOComplete (missed-wakeup race), set `ThreadBlockedIO`, find next ready thread, return context pointer
5. `SetSyscallSwitchTarget(nextCtx)` — SVC return path context-switches to thread 0
6. Thread 0 runs `KernelIdleLoop` → WFI → block IRQ fires
7. IRQ top-half: read ISR (deassert INTx), set `IOComplete`, call `WakeBlockIOThread(tid)` → marks thread `ThreadReady`
8. `KernelIdleLoop` finds ready thread → `YieldToReadyThread` → switches back to us
9. `DoBlockIOComplete` — pop used ring, check status, copy data from DMA buffer

**Key files created:**
- `kmazarin/kmazarin/io_bridge.go` — `BlockForBlockIO` + `WakeBlockIOThread`
- `kmazarin/ksyscall/blockio_bridge.go` — `go:linkname` bridge for ksyscall → main

**Key files modified:**
- `kmazarin/device/virtio/block/io.go` — Split `doBlockIO` into `DoBlockIOSubmit` / `DoBlockIOComplete` / `doBlockIO` (polling fallback for early boot)
- `kmazarin/device/virtio/block/block.go` — Added `BlockedTID int32` field, `GetBlockedTIDPtr()`, `GetDevice()`
- `kmazarin/kmazarin/threads.go` — `ThreadBlockedIO = 12`, diagnostics, `WakeThreadForSignal` handling
- `kmazarin/kmazarin/bottom_half.go` — `blockIOBlockedTID` pointer, `WakeBlockIOThread` call in NonTimerIRQTopHalf
- `kmazarin/kmazarin/plic_dispatch_riscv64.go` — Same `WakeBlockIOThread` call (RISC-V has separate PLIC handler)
- `kmazarin/ksyscall/forward_decl_stubs.go` — Test stub for `BlockForBlockIO`

### Phase 3: RISC-V Comment Cleanup

Removed false "WFI is a NOP" claim from `io_riscv64.go`.

---

## Design Decisions

### Thread 0 is Always Available

When a priest thread calls `BlockForBlockIO`, `findReadyThreadSchedLockHeld()` should **always** find thread 0 on the ready queue. Thread 0 runs `KernelIdleLoop`, which calls `YieldToReadyThread()` → `SaveThread0AndYield()` before switching to any priest. So when a priest is running, thread 0 is always `ThreadReady` on the queue.

The "no thread found" case in `BlockForBlockIO` logs a BUG message and returns 0 — it does NOT silently spin or WFI. If thread 0 isn't on the ready queue, the scheduler is broken and we want to know about it.

**Broader insight (not yet implemented):** Every blocking function called from a priest thread (IPC, futex, sleep, block I/O) should always find thread 0. The "return 0 = don't block" fallback in `BlockForIPCCall`, `ThreadBlockFutex`, `ThreadBlockSleep`, etc. is arguably masking bugs for the same reason. The user wants to revisit this — `findReadyThread` should never return nil when called from a non-thread-0 context.

### No Spin-Waiting

The user explicitly does not want spin-wait / WFE loops for I/O. The correct pattern is: block the thread, switch to thread 0's idle loop, IRQ fires, idle loop finds the woken thread, switches back. This is what Linux does (`wait_for_completion` → `schedule`).

The polling fallback (`doBlockIO`) is retained only for **early boot** (before `KernelIdleLoop` is running), called via `ReadBlock` / `WriteBlock` (e.g., `readBootConfig()`).

### Single I/O Slot

One `BlockedTID` per device. Only one priest owns the block device, and reads are sequential within the `SyscallBlockRead` for-loop. Multi-I/O support would require tracking descriptor indices per request — the VirtIO used ring already returns the head descriptor index for this purpose. Deferred to later.

### RISC-V Has Separate Block IRQ Handler

RISC-V uses `plic_dispatch_riscv64.go` for block device IRQs, NOT `NonTimerIRQTopHalf` from `bottom_half.go`. Both handlers have identical block device logic (read ISR, set IOComplete, WakeBlockIOThread). This is because RISC-V external interrupts go through the PLIC claim/complete path, not the ARM64/AMD64 per-vector dispatch.

---

## Test Results

Both ARM64 and RISC-V tested with 45-second runs. Full input responsiveness (keyboard, mouse, timer), no errors, no BUG messages, GC running normally. Interrupt-driven block I/O handles all disk reads during priest launch (TOML config, ELF loading, FAT32 mount, `.maz` loading).

---

## AMD64 Status: MSI-X Block I/O IMPLEMENTED, Priests Not Running

### MSI-X Block Interrupt (DONE)

AMD64 block device now uses MSI-X (same as input devices). INTx was not feasible because QEMU q35 routes PCI INTx to IOAPIC inputs 16-19 (vectors 48-51), which fall outside the ISR stub range (vectors 32-47, with vector 48 = timer).

Changes:
- `interrupt_amd64.go`: calls `input.ConfigureMSIXForDevice()` → gets IRQ=12 (vector 44)
- `block.go`: writes MSIX_CONFIG + QUEUE_MSIX_VECTOR during VirtIO handshake (gated by `blockMSIXVector()` — returns 0 on AMD64, 0xFFFF on ARM64/RISC-V)
- `interrupt_arm64.go`, `interrupt_riscv64.go`: added `blockMSIXVector() → 0xFFFF`

### Pre-existing Issue: Userspace Priests Don't Run on AMD64

Priests are launched (`[boot] disk launched`, `[boot] stdio launched`) but no userspace output appears — no GC traces, no `[disk] starting disk priest`. This happens with BOTH MSI-X and polling mode, confirming it's unrelated to the block I/O change. The priest Go runtime fails to complete initialization on AMD64.

### TODO

- [ ] Investigate AMD64 priest initialization failure (pre-existing, not related to block I/O)
- [ ] Check for use of polling anywhere in the codebase

### What Already Works on AMD64

- **IDT + device IRQ routing**: Vectors 32-47 → `handle_device_irq` → `NonTimerIRQTopHalf` → LAPIC EOI. **EOI is already written after handler returns** (correct for level-triggered).
- **NonTimerIRQTopHalf is shared**: The block device handler with `WakeBlockIOThread` already compiles for AMD64.
- **io_bridge.go is shared**: `BlockForBlockIO` / `WakeBlockIOThread` have no build tags — they compile for AMD64.
- **Input devices use MSI-X**: `input_amd64.go` has a working `platformConfigureDeviceIRQ` that programs MSI-X table entries with LAPIC vectors 32-47. There's even a public wrapper: `ConfigureMSIXForDevice(bus, slot, funcNum)`.
- **UART IRQ**: Fully working on AMD64 (proof the NonTimerIRQTopHalf dispatch works).

### What's Missing

**One file**: `kmazarin/device/virtio/block/interrupt_amd64.go` — currently a stub returning 0:

```go
func configureBlockInterrupt(_, _, _ uint8) uint32 {
    return 0
}
```

Because it returns 0, `block.GetIRQNum()` returns 0, and `SyscallBlockRead` falls back to polling (`blk.ReadBlock`).

### How to Implement AMD64

The input devices already solved this — they use MSI-X via `ConfigureMSIXForDevice`. The block device should do the same:

1. **`interrupt_amd64.go`**: Call `input.ConfigureMSIXForDevice(bus, slot, funcNum)` or duplicate the MSI-X setup logic. This programs the MSI-X table to deliver to the next available LAPIC vector (42-47 range), and returns the IRQ number (vector - 32) that `NonTimerIRQTopHalf` uses.

2. **That might be it.** The rest of the chain is shared:
   - `SetBlockIRQ(irq, isrBase, ioComplete, blockedTID)` wires it into NonTimerIRQTopHalf
   - `NonTimerIRQTopHalf` already handles block device IRQs with WakeBlockIOThread
   - `BlockForBlockIO` / `WakeBlockIOThread` are shared
   - `blockReadInterrupt` in `ksyscall/blockio.go` is shared
   - `DoBlockIOSubmit` / `DoBlockIOComplete` in `io.go` are shared

3. **Potential issue**: The block device's ISR register. On ARM64/RISC-V, `block.GetISRBase()` returns the VirtIO ISR capability BAR VA. On AMD64 with MSI-X, reading the ISR register deasserts the MSI-X pending bit. Verify that `blockISRBase` is correctly set for AMD64 PCI — it should be, since VirtIO PCI capability parsing is shared code in `block.go`.

4. **Test**: `$GO tool task run-x86_64 TIMEOUT=15`. Verify priests launch (disk I/O works via interrupts, not polling).

### Important: MSI-X vs INTx

ARM64 and RISC-V use **PCI INTx** (legacy level-triggered interrupts routed through GIC/PLIC). AMD64 input devices use **MSI-X** (message-signaled interrupts that bypass the IOAPIC and write directly to the LAPIC). The block device on AMD64 should also use MSI-X for consistency with the input devices. This is an architectural decision — discuss with the user if INTx via IOAPIC is preferred instead.

The `ConfigureMSIXForDevice` function in `input_amd64.go` allocates LAPIC vectors starting at 42. Currently keyboard and mouse use vectors 42-43. The block device would get vector 44 (IRQ number 12 in the NonTimerIRQTopHalf convention).

---

## Environment

```bash
export GOTOOLCHAIN=auto
export GO=/Users/iansmith/sdk/go1.25.5/bin/go
export QEMU=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-aarch64
export QEMU_X86_64=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-x86_64
```

Build: `$GO tool task`
Run ARM64: `$GO tool task run TIMEOUT=15`
Run RISC-V: `$GO tool task run-riscv64 TIMEOUT=15`
Run AMD64: `$GO tool task run-x86_64 TIMEOUT=15`
Safe serial log: `$GO tool safe-serial-read /tmp/diplomat-arm64-serial.log`
