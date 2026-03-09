# Continuation Prompt: AMD64 Priest Initialization Failure

Paste this into a new Claude Code session to investigate and fix.

---

## Prompt

Priests (userspace programs) launch on AMD64 but never produce output — no GC traces, no `[disk] starting disk priest`, nothing. The kernel reports `[boot] disk launched` etc., confirming threads were created and added to the ready queue. ARM64 and RISC-V work perfectly. This is a pre-existing AMD64 issue, not related to the recent MSI-X block I/O change.

**Read these files for context:**
- `design/CONTINUATION-BLOCK-IO-COMPLETE.md` — current status, confirmed pre-existing
- `kmazarin/ksyscall/launch.go` — SyscallLaunch: ELF loading, CreateUserspaceThread
- `kmazarin/kmazarin/threads.go` — thread states, scheduling, ready queue
- `kmazarin/kmazarin/thread_context_amd64.go` — SetupForUserspace: sets RIP, RSP, CS=0x1B, SS=0x23, RFLAGS=0x202
- `kmazarin/kmazarin/exceptions_amd64.s` — `load_context_and_iretq` (line ~1401): restores context, writes FS_BASE MSR, IRETs to Ring 3
- `kmazarin/kmazarin/save_context_amd64.go` — DoContextSwitch / doContextSwitchABI0

---

## Standing Rules

**No architecture changes without discussing with the user first.** Keep ARM64, RISC-V, and AMD64 as similar as possible.

---

## What We Know

1. **Priests are created** — `CreateUserspaceThread` returns a TID, thread is added to the ready queue
2. **Kernel continues running** — event loop (`[EVT]` lines) shows timer ticks, keyboard/mouse IRQs, syscall count increasing (`ksvc=` values change)
3. **No userspace output at all** — not even a single GC trace line. The Go runtime never gets far enough to do anything visible
4. **Polling vs MSI-X doesn't matter** — same failure with block I/O in polling mode (configureBlockInterrupt returning 0)
5. **`[IL1]IrP0`** — mysterious breadcrumb appears after `[boot] stdio launched`. Not found in any Go or assembly source — possibly interleaved single-char output from multiple code paths
6. **`fw=0 fW=0`** — futex wake counters are zero, meaning no futex_wake calls are happening. Go runtime init needs futex heavily

## Likely Failure Points (AMD64-Specific)

### 1. FS_BASE / TLS Not Set Up for Priests
- `SetupForUserspace()` sets `ctx.FSBase = 0` (ThreadContext is zeroed)
- `load_context_and_iretq` (line ~1460): if FSBase == 0, skips FS_BASE MSR write
- Go runtime reads `g` from `FS:-8` — with FS_BASE=0, this reads from address -8 (unmapped → page fault)
- **On ARM64**: g is in register X28, set by the kernel before jumping. No TLS memory needed.
- **On RISC-V**: g is in register X4 (TP), set similarly.
- **On AMD64**: g must be accessible via FS:-8, which requires FS_BASE pointing to a valid TLS block

### 2. CR3 Not Switched to Priest's Page Table
- `load_context_and_iretq` does `MOVQ CR3, AX; MOVQ AX, CR3` (TLB flush) but may not load the priest's page table
- Check: does `DoContextSwitch` set the target thread's CR3 value in the context?

### 3. Priest Entry Point Misaligned or Wrong
- AMD64 Go ELF entry point may need different handling than ARM64/RISC-V
- Check: is the entry point address correct? Does it point to mapped memory in the priest's page table?

### 4. Stack Layout Wrong
- Go runtime expects specific auxv/envp/argv layout on the stack
- Check: is the stack setup for AMD64 matching what the Go runtime expects?

### 5. Page Fault During First Instruction
- If the priest's code page isn't mapped in its page table, the first instruction faults
- Check: is demand paging working for priest threads on AMD64?

## Diagnostic Steps

### Step 1: Add Breadcrumbs to Context Switch
In `exceptions_amd64.s`, before the IRETQ in `load_context_and_iretq`, add:
```asm
MOVW $0x3F8, DX
MOVB $'U', AX; OUTB   // 'U' = about to enter userspace
```
This confirms the priest thread is actually being scheduled and IRETQ'd to.

### Step 2: Check Page Fault Handler
Look at what happens when a priest page fault occurs. Does the AMD64 page fault handler support demand paging for priest threads? Check `handle_page_fault` in `exceptions_amd64.s` and the Go-level handler.

### Step 3: Check FS_BASE Setup
In `launch.go`, after `CreateUserspaceThread`, examine whether FS_BASE is being set. Compare with ARM64's `SetupForUserspace` — ARM64 puts g0 address in X28 (the g register). AMD64 needs an equivalent: allocate a TLS block, set FSBase to point to it, and store g at offset -8.

### Step 4: Run with Serial Breadcrumbs
```bash
export GOTOOLCHAIN=auto GO=/Users/iansmith/sdk/go1.25.5/bin/go
export QEMU=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-aarch64
export QEMU_X86_64=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-x86_64
$GO tool task run-x86_64 TIMEOUT=30
$GO tool safe-serial-read /tmp/diplomat-serial.log
```

### Step 5: Compare ARM64 and AMD64 Launch Paths
Read `kmazarin/kmazarin/thread_context_arm64.go` (or equivalent) and compare SetupForUserspace. The ARM64 version likely sets X28 = g0 address. The AMD64 version needs FSBase set to a TLS block address.

## Key Insight

The most likely root cause is **FS_BASE (TLS) not being set up for priest threads on AMD64**. On ARM64, the g pointer is in a register (X28) that's explicitly set during thread creation. On AMD64, g is accessed via `FS:-8`, which requires:
1. A TLS block allocated in the priest's address space
2. `FS_BASE` MSR pointing to the end of that TLS block (so `FS:-8` works)
3. `g` stored at `[FS_BASE - 8]`

Without this, the very first Go instruction that accesses `g` (which is essentially every instruction) will fault.

---

## Environment

```bash
export GOTOOLCHAIN=auto
export GO=/Users/iansmith/sdk/go1.25.5/bin/go
export QEMU=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-aarch64
export QEMU_X86_64=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-x86_64
```

Build: `$GO tool task`
Run AMD64: `$GO tool task run-x86_64 TIMEOUT=30`
Safe serial log: `$GO tool safe-serial-read /tmp/diplomat-serial.log`

## Related Files

- `kmazarin/kmazarin/thread_context_arm64.go` — ARM64 SetupForUserspace (working reference)
- `kmazarin/kmazarin/thread_context_amd64.go` — AMD64 SetupForUserspace (suspect)
- `kmazarin/kmazarin/exceptions_amd64.s` — `load_context_and_iretq`, `handle_page_fault`
- `kmazarin/kmazarin/save_context_amd64.go` — DoContextSwitch, SaveContextFromFrame
- `kmazarin/ksyscall/launch.go` — SyscallLaunch, CreateUserspaceThread
- `kmazarin/ksyscall/futex.go` — futex syscall (Go runtime needs this during init)
- `kmazarin/ksyscall/mmap.go` — mmap syscall (Go runtime needs this during init)
- `kmazarin/ksyscall/clone.go` — clone syscall (Go runtime creates sysmon + templateThread)
