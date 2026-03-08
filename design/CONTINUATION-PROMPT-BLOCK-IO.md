# Continuation Prompt: Interrupt-Driven Block I/O + ARM64 EOIR Fix

Paste this into a new Claude Code session to continue this work.

---

## Prompt

I'm implementing three fixes described in `design/INTERRUPT-DRIVEN-BLOCK-IO-PLAN.md`. Read that file first — it contains the complete plan with specific file changes.

**Start with Phase 1 (ARM64 EOIR fix)** because it's independent and small:

1. Read `kmazarin/kmazarin/exceptions_arm64.s` and find the `irq_not_timer` label. The current code writes GICC_EOIR at lines ~1177-1179 BEFORE calling NonTimerIRQTopHalf at line ~1200. Move the EOIR write to AFTER the handler returns. Save the IAR value (currently in R19) into R21 at the top of `irq_not_timer` because R19 gets overwritten by the m0.curg save at line ~1194. All exit paths from `irq_not_timer` must write EOIR using R21.

2. In `kmazarin/kmazarin/main.go` lines ~631-651, remove the edge-triggered workaround:
   - Remove `asm.MmioRead8(dev.ISRBase)` (ISR pre-clear)
   - Remove `cachedIC.SetIRQEdgeTriggered(localIRQ)`
   - Remove the comment block explaining edge-triggered
   - Keep `SetIRQPriority`, `SetIRQTarget`, `EnableIRQ`

3. Fix the false WFI comment in `kmazarin/device/virtio/block/io_riscv64.go` — WFI works correctly in QEMU system emulation on all architectures. Remove the claim that "QEMU TCG may treat WFI as a NOP".

4. Build with `$GO tool task` and run ARM64 with `$GO tool task run TIMEOUT=15`. Verify keyboard/mouse input still works and there's no IRQ storm.

**Then do Phase 2 (interrupt-driven block I/O)** which is larger:

The key pattern: look at how `BlockForIPCCall`/`WakeIPCThread` work in `kmazarin/kmazarin/ipc_bridge.go` and how `ThreadBlockedIPC` / `ThreadBlockedFutex` states work. Block I/O follows the exact same pattern.

Key files to read before starting Phase 2:
- `kmazarin/kmazarin/ipc_bridge.go` — BlockForIPCCall pattern to copy
- `kmazarin/kmazarin/threads.go` — thread states, WakeThreadForSignal, diagnostics
- `kmazarin/kmazarin/bottom_half.go` — NonTimerIRQTopHalf block device handler
- `kmazarin/device/virtio/block/io.go` — doBlockIO to split into submit/complete
- `kmazarin/ksyscall/blockio.go` — SyscallBlockRead to add interrupt-driven path

Critical: the missed-wakeup race. Between doBlockIOSubmit and BlockForBlockIO, the IRQ can fire. BlockForBlockIO must re-check IOComplete under the scheduler lock. If already complete, return 0 (don't block).

Environment setup:
```bash
export GOTOOLCHAIN=auto
export GO=/Users/iansmith/sdk/go1.25.5/bin/go
export QEMU=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-aarch64
export QEMU_X86_64=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-x86_64
```

Build: `$GO tool task`
Run ARM64: `$GO tool task run TIMEOUT=15`
Run RISC-V: `$GO tool task run-riscv64 TIMEOUT=15`
Safe serial log reader: `$GO tool safe-serial-read /tmp/diplomat-arm64-serial.log`
