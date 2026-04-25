# Findings

---

## linux ring 1 single-reader bottleneck — split-ring fix (2026-04-25, FIXED)

### Symptom
At maildb startup the mmap coherence test hits "Resource Temporarily
Unavailable" (EAGAIN) on `syscall.Open` / `mkdir` / `stat current.mbox`
within the first few seconds, before fti has indexed anything. The
whole data path stalls: mail UI throbber freezes, clicks don't
register, fti idle.

### Root cause
linux's ring 1 (the kernel→linux delegate ring) is a single 64-slot
buffer drained by **one** reader goroutine. The dispatcher callback
on that reader does either:
```go
if req.SysID == sysid.Write && byte(req.Arg0()) <= 2 {
    stdoutCh <- req       // BLOCKING, cap 32
} else {
    delegateCh <- req     // BLOCKING, cap 8
}
```
Both sends are blocking. If `delegateCh` fills up because the
file-lane goroutine is busy waiting for an fs round-trip, **the
single reader blocks on `delegateCh <- req`** — and now also stops
draining pipe-buffered Writes that ride the same ring. Ring 1 fills
to 64, kernel-side `uringSendKernel` returns -EAGAIN, every shepherd's
syscall fails. Meanwhile the linux-ui notify fix (2026-04-24) made the
stdio path actually do work per fmt.Printf, which is what tipped the
balance from "barely keeping up" to "bottlenecked".

### Diagnostic data (probes added then removed)
```
[DLG:EAGAIN] #1..#24  head=176 tail=240 fill=64 sent=233   (frozen)
[DLG:EAGAIN] #25       head=177 tail=241 fill=64 sent=234   (advanced 1)
[DLG:EAGAIN] #26..#30  head=179 tail=243 fill=64 sent=237   (advanced 2)
```
Ring head moves at maybe 1-3 messages per 100ms — file lane is
genuinely starved, not deadlocked.

### Fix (option 2: split traffic classes onto separate rings)
- New kernel global `stdioWriteRingIdx` set via new syscall
  `SysRegisterStdioWriteRing` (0x1042). When set, `DelegateSyscall`
  routes pipe-buffered Writes (Write fd<=2) to that ring index instead
  of the Write handler's default ring.
- linux now creates ring 1 + ring 2 via `uring.Setup(1)` /
  `uring.Setup(2)`. Ring 1 carries blocking delegates (incl. fd>2
  Write); ring 2 carries pipe-buffered stdio Writes only.
- Each ring has its own dispatcher with its own reader goroutine. The
  ring 1 reader feeds `delegateCh`; the ring 2 reader feeds
  `stdoutCh`. Backpressure on either ring is independent of the other.
- linux's `MazarinMain` calls `sys.RegisterStdioWriteRing(2)` after
  registering its delegate handlers.

### Validation
60s ARM64 HVF run after fix: 0 EAGAINs, 0 "[DLG] uring send failed",
0 "Resource Temporarily Unavailable", all mmaptest cases PASS, mbox
import working, fti indexing. The bleve scorch panic that surfaces
later (mmap-of-directory) is a separate bug the verifier identified —
tracked in findings under "FTI bleve scorch merger panic — directory
mmap (2026-04-25)".

---

## linux-ui window disappearing after ~1-2s (2026-04-24, FIXED)

### Symptom
Linux-ui window appears at startup with whatever output happened to
land during initial-layout settling (~28 blits over 1-2s), then
appears frozen while maildb/fti continue spamming console output that
never makes it to the screen. Linux shepherd itself remains alive
indexing emails normally.

### Root cause
`linuxapp.runLoop` blocks in a `select` over `wmCh`, `eagerCh`, and
`notifyCh`. The drain function reads from `writeCh` *inside* the
select case, but `writeCh` is **not** one of the select channels —
it's drained when one of the three above fires. linux-ui passed
`BuildResult{Drain: ...}` with no `NotifyCh`, so `runLoop` swapped
in a never-firing dummy channel. Once startup constraints settle:
- `eagerCh` stops firing (no more dirty constraints)
- `wmCh` stops firing (no user input, no resize)
- `notifyCh` is the dummy chan — never fires

The select blocks forever. `writeCh` fills, the linux shepherd's
`lineAccumulator` falls back to the non-blocking-default branch and
drops further lines silently.

### Fix
Pattern that mail-ui (`maildbio`) was already using:

1. `linuxio.LinuxIO` interface gains `NotifyChannel() <-chan struct{}`.
   `LinuxIOInit` gains `NotifyCh chan struct{}` and updated
   `SetChannels` signature.
2. linux-ui's `MazarinShepherd` creates `make(chan struct{}, 1)`,
   passes it to `SetChannels`, and returns it via
   `BuildResult.NotifyCh` so `runLoop`'s select wakes on it.
3. linux shepherd's `lineAccumulator` does a non-blocking poke
   (`select { case notifyCh <- struct{}{}: default: }`) after each
   successful `writeCh` send.

### Validation
After fix, ARM64 HVF run shows `[rachel:geom] blit#500 sid=N
title="Linux Console"` — i.e. linux-ui has blitted >= 500 times
during the run, vs. the pre-fix ceiling of ~28. Console updates
visibly during fti/maildb activity.

---

## Pipe-buffered Write fd-gating bug (2026-04-24, FIXED)

### Symptom
After implementing pipe-buffered Write delegation, fti would run for many
seconds, then `Fstatat` for `/tmp/fti-N/bleve/store` would EFAULT (the
DataVA mapped into the linux shepherd was found unmapped by the
`SysVerifyMapping` defensive check), and shortly afterwards Go's
`MkdirAll` slow path would panic with
`mkdir /tmp/fti-N/bleve/store: not a directory`.

### Root cause
`DelegateSyscall` in `kmazarin/ksyscall/delegate.go` pipe-buffered ALL
`Write` delegates regardless of fd: skipped `delegateCallInfos[TID]`
`InUse` tracking and returned the byte count without blocking. But the
linux shepherd's stdout lane (which calls `SysReleaseDelegatePage`)
ONLY runs for `fd<=2`. For `fd>2` (regular files), the request is
routed to the file lane and `sysWrite` calls `req.Reply(written)` —
which is a `SyscallReply` syscall.

The kernel's `SyscallReply` looks up `delegateCallInfos[callerTID]`. If
the caller TID is currently blocked on a *different* delegate (e.g.
`Fstatat`), `info.InUse` is `true` for that other syscall — so the
spurious Reply unmaps the *other* syscall's data page, frees its PA,
and wakes the caller with the wrong return value.

### Trace
- `t=0`: caller TID 30 issues `write(fd=3, ...)` → kernel pipe-buffers,
  returns immediately, doesn't set `InUse`.
- `t=1`: caller TID 30 issues `fstatat(...)` → kernel allocs page P_F at
  VA 0x50000116a000 in linux shepherd, sets `InUse=true`, blocks caller.
- `t=2`: linux file lane processes the buffered fd=3 Write, calls
  `req.Reply(written)` → kernel `SyscallReply` reads `delegateCallInfos[30]`
  which now references the Fstatat → `reclaimDataPage` unmaps
  `0x50000116a000`.
- `t=3`: linux file lane processes Fstatat, calls `VerifyMapping(...)`,
  finds it unmapped → returns `EFAULT`. Go `MkdirAll` falls through to
  `Mkdir`, which fails with `ENOTDIR` because the parent's
  `bleve/store` resolution races against half-completed earlier mkdirs.

### Fix
`pipeBuffered := id == sysid.Write && arg0 <= 2` in `DelegateSyscall`.
Only stdio writes are pipe-buffered; fd>2 writes keep the original
synchronous block-and-Reply semantics so the file lane's `req.Reply`
correctly targets its own delegate slot.

### Validation
60s ARM64 HVF run after fix: 0 EFAULTs, 0 panics, no
`mkdir ... not a directory` errors. fti indexed 51+ documents to
completion. Three windows rendered (linuxapp x2, mail). The
`SysVerifyMapping` defensive check in `linux/syscalls.go::handle()`
is left in place as defence-in-depth.

---

## Windows-not-shown: goroutine scheduling bug (2026-04-24, OPEN)

### Symptom

On both ARM64 HVF and x86_64, boot proceeds through
`[rachel:wm] event loop started` and `[linux] Ready=true`, but no
`[rachel:wm] AppStart` ever arrives. Background framebuffer color
visible, no window chrome. Seen freshly after RISC-V removal; likely
present earlier too.

### Visible chain of stalls (with workarounds layered on)

1. `linux-ui.MazarinMain` goroutine never starts.
   - Launched via `go mazhost.RunMaz(uiMain)` in `maz/linux/main.go`.
   - Linux's calling goroutine then runs several raw syscalls
     (`sys.UartWriteString`, `sys.RegisterSyscallHandlersWithRing`,
     etc.) and one `fsclient.Connect` before hitting the stdin
     drainer. None of those yields cause the `go`-enqueued goroutine
     to run.
   - **Workaround in tree:** `runtime.Gosched()` immediately after
     the `go`. Makes MazarinMain run.
   - **Rachel does the analogous `go mazhost.RunMaz(fontSvcMain)`
     and its plugin MazarinMain *does* run** — needs characterizing.

2. `mazhost.LaunchMaz("helloworld")` (CPU-bound `mazdl.OpenBytes`)
   starves linux-ui once it yields inside Bootstrap.
   - **Workaround in tree:** `go mazhost.LaunchMaz(...)` (run in its
     own goroutine). Linux main then reaches stdin drainer and yields.

3. linux-ui Bootstrap → `NewConsole` → `fc.OpenFaceByName` → OpenFont
   IPC to rachel → `fontsvc.handleOpenFont` → `ensureFontIndex` →
   `fontcache.LoadFontIndex` → `sys.LoadFile("/fonts/fonts.csv")`.
   This call happens inside rachel's uring-reader goroutine, under
   the dispatcher callback, blocking it.
   - fs serves the CSV (`[fs] /fonts/fonts.csv: 9083 bytes, 0ms`),
     then `fmt.Printf` in fs's `readFileIntoPages` delegates Write
     to linux — and fs stalls there.
   - **Workaround in tree:** fontsvc preloads `fonts.csv` in its
     `MazarinMain` (before linux is Ready, so Write doesn't delegate).

4. linux-ui's first `fc.OpenFaceByName` (after the CSV preload)
   triggers `sys.LoadFile("/fonts/AtkinsonHyperlegibleMono-Regular.otf")`.
   fs reads the file, calls `fmt.Printf`, Write delegation to linux
   ring 1 — same stall as #3, not yet worked around.

### Kernel-level probe results (reverted at end of session)

For the Write-delegation stall at step 4:
- `DelegateSyscall` runs, copies the user buffer into a handler-mapped
  page, calls `uringSendKernel(-1, handlerSID=linux, ringIdx=1, msg)`.
  `sendResult=0` — message landed in linux's ring 1.
- `UringSendKernel`'s wake path fires: `slot.BlockedTID=N, woke tid=N`.
  The ring 1 reader thread is moved from `ThreadBlockedUringRecv`
  to `ThreadReady`, flagged `PriorityWoken=true`, requeued on the
  priority ready queue, context rewound to re-execute SVC.
- Caller (fs) then runs `blockForDelegatedSyscall`, which calls
  `findNextThreadForBlockSchedLockHeld` → priority-pass finds the
  just-woken reader thread → returns its ctx. Caller does
  `SetSyscallSwitchTarget(ctx)` and returns 0.
- **Expected:** Mazzy kernel switches to the woken thread, SVC
  returns from `SyscallUringRecv` with the message, Go's
  `exitsyscall` resumes the reader goroutine, `r.handler(&msg)`
  runs the ring 1 dispatcher callback.
- **Observed:** `[uring:reader] ring=1 #N proto=...` never prints.
  The reader goroutine doesn't resume.

### Interpretation (revised after user challenge)

The runnable-goroutines-without-M dump I captured was a PANIC
snapshot (my instrumentation caused a stack split), not a normal
stall snapshot. It is NOT evidence of a wakep bug. Go's wakep works
— otherwise no `go f()` anywhere would ever get scheduled on Mazzy,
and we run millions of syscalls successfully.

The real issue is not a scheduler bug, it is a **Linux-emulation
semantic gap**:

On Linux, `write(fd=1)` to a stdout pipe buffer returns **near-
instantly** (it's a buffered, non-blocking operation in the common
case where buffer has space). The writer drops bytes into the pipe;
the reader consumes asynchronously on its own schedule.

On Mazzy, `SyscallWrite` → `syscallWriteDelegate` → `DelegateSyscall`
→ `blockForDelegatedSyscall`. fs's thread is held in
`ThreadBlockedDelegate` until linux's stdout lane handler runs
`SyscallReply`. Writer and reader are **synchronously coupled
across the delegate boundary** — a full IPC round-trip instead of
a pipe-buffer drop.

This synchronous coupling is not itself a bug — it matters only
when we form a cycle. The cycle we hit at linux-ui's first font
request:

1. Rachel's uring reader goroutine calls `sys.LoadFile` inside the
   `handleOpenFont` callback. Rachel can't process any other IPC
   until fs replies.
2. fs's `handleLoadFile` → `readFileIntoPages` calls `fmt.Printf`.
   After linux has registered as Write handler AND called SetReady,
   this Printf goes through Write delegation, blocking fs.
3. linux-ui's `Bootstrap` is stuck in `NewConsole`-triggered
   `fc.OpenFaceByName`, waiting on `fc.ReplyCh` for rachel's
   OpenFont reply.
4. linux's main goroutine has already reached stdin drainer, so
   linux's stdout lane goroutine can run — but with fs deadlocked
   in this chain, nothing else is producing work for it.

Result: fs waits for linux's Write-delegate reply → linux waits for
nothing in particular (its stdout lane IS runnable) — but the
cascade means linux-ui never completes Bootstrap and never issues
an AnnounceToWM. Windows don't appear.

### Why this deadlock didn't exist before (guessing, needs git-blame)

The same cycle used to be broken in at least one of these places:
- fontsvc probably preloaded `fonts.csv` at startup (no LoadFile
  inside the OpenFont handler).
- fs probably didn't `fmt.Printf` inside `readFileIntoPages`.
- linux-ui loaded later in boot, after fs's diagnostic chatter was
  done.
- The ordering of "linux registers Write handler" vs "rachel's
  fontsvc first OpenFont callback" used to put the delegation
  outside the cycle.

Recent changes around font handling (louis14 / fontsvc rework,
callback-based fontsvc injection, two-lane delegate handler) could
each have changed one of these and opened the window.

### Why each workaround worked (correct mechanism)

- **`runtime.Gosched()` after `go mazhost.RunMaz(uiMain)`** lets
  linux-ui's MazarinMain start running *before* linux's main enters
  the stdin drainer — shifting when linux-ui issues the OpenFont
  IPC relative to fs's Write-delegation activation.
- **`go mazhost.LaunchMaz("helloworld")`** — keeps linux main
  from tying up the CPU in CPU-bound OpenBytes while linux-ui is
  trying to finish its font handshake.
- **fontsvc preloading `fonts.csv`** — moves one IPC leg out of
  the `handleOpenFont` callback, eliminating one edge of the
  dependency cycle.
- **500 ms busy loop (diagnostic only)** — forces enough async-
  preemption events that the timing windows of the cycle never
  line up to hold.

None of these are the right fix. The right fix is closing the
emulation gap.

### Fix direction

Make Write delegation behave like a Linux pipe:
- `DelegateSyscall` for `Write`/`Writev` should copy bytes into a
  shared buffer (ring) owned by the handler shepherd and return
  to the caller immediately — no `blockForDelegatedSyscall`.
- The handler's drain goroutine consumes from the ring on its own
  schedule.
- Backpressure only when the ring is truly full (matching pipe-
  buffer-full blocking on Linux).

With that, fs's `fmt.Printf` during `readFileIntoPages` stops
being a deadlock primitive: it writes a few bytes and returns.
The circular wait dissolves.

Revert the three workarounds (Gosched, async LaunchMaz, fontsvc
preload) once the fix lands.

**Confirmed hypothesis #1 (ruled out): Async preemption works.** A
500ms busy loop inserted immediately after `go mazhost.RunMaz(uiMain)`
(instead of `runtime.Gosched()`) lets linux-ui's MazarinMain fire,
make all the way through Bootstrap, and successfully send `AppStart`
to rachel. So sysmon is running and can force preemption when there
is CPU-bound user code to preempt. The symptom isn't "preemption
broken," it's "no preemption opportunity" when the running goroutine
only uses raw syscalls.

**Hypothesis #2 (retracted): runnable-goroutines-without-M dump**
was captured during a panic (my probe caused a stack split). The
snapshot shows normal mid-scheduling state, not a wakep failure.
Go's wakep/futex_wake path works on Mazzy (otherwise `go f()`
would never schedule anywhere). Retracted.

See "Interpretation (revised after user challenge)" below for the
actual diagnosis.

### Workarounds currently in the tree (to remove once real fix lands)

- `maz/linux/main.go`: `runtime.Gosched()` after `go mazhost.RunMaz(uiMain)`.
- `maz/linux/main.go`: `go mazhost.LaunchMaz("helloworld")` (was synchronous).
- `maz/fontsvc/main.go`: `MazarinMain` preloads `fonts.csv` via
  `ensureFontIndex()` (defensible on its own merits, but the
  motivation here was to hide the Write-delegate stall for CSV load).

---

## x86_64 Boot OOM — `total=ffffffff20000` Decoded (2026-04-24)

When kmazarin prints `[kmem] Buddy OOM order=0 total=ffffffff20000` (or
any `total=` value with high bits set) immediately after diplomat's
"Jumping to kmazarin..." line, the bug is **not** in the buddy
allocator. It is in diplomat's UEFI page allocation strategy combined
with a silent underflow in the buddy's 4 GB cap.

### The number's fingerprint

`totalPages = 0xFFFFFFFF20000` in `kmazarin/kmem/buddy.go`. Since
`buddyAlloc.totalPages = uint64(end-start) / PageSize`, that means
`end-start = 0xFFFFFFFF20000000` (uint64). That is `−0xE0000000` in
signed arithmetic — i.e. `start > end` by `0xE0000000` (3.5 GB).

### Why end < start

`buddy.go:95-100` clamps `end` to `linearMapMaxPA = 0x100000000` (4 GB)
because the kernel linear map (`KernelVAOffset = 0xFFFFFFFF00000000`)
wraps for PA ≥ 4 GB. The clamp does NOT touch `start`. If diplomat
hands the buddy a `start` above 4 GB (e.g. `0x1E0000000`), end gets
clamped down, start does not, and `end - start` underflows in uint64.
Every order-0 allocation then returns 0 → instant page fault.

### Why diplomat handed up a high `start`

`diplomat/main/pagetable_amd64.go::allocatePhysPages` was using
`AllocateAnyPages`, which lets UEFI pick whatever's free — frequently
above 4 GB on QEMU TCG x86_64 with `-m ≥ 4G`. ARM64 has had the right
fix for ages (`AllocateMaxAddress` with seed `linearMapMaxPA - 1`,
mirroring the 4 GB cap); x86_64 just never got the cross-port. There's
even a memory note about it ("QEMU ARM64 virt Memory: UEFI
`AllocateAnyPages` can return >4GB"). Latent until UEFI's allocator
behavior shifted enough to expose it.

### Second-order trap

Even with `AllocateMaxAddress` in place, requesting 2.5 GB
*contiguous* below 4 GB often fails on QEMU TCG x86_64 — UEFI
fragments low RAM during its own boot. The original code's failure
path zeroed `vm.UnifiedPoolStart`/`End`, which made kmazarin fall back
to FramePool (16 MB), which then OOMed on VirtIO GPU framebuffer
(32 MB) and InitConstraintPages (16 MB), failing fs launch with
error -7. Fix: retry-down ladder
`[2.5G, 1.5G, 1G, 512M, 256M]`, take the first that fits.

### Diagnostic shortcut for next time

If you see ANY suspiciously huge `total=` in `[kmem] Buddy OOM` at
boot, check three things in order:
1. What did diplomat allocate as the unified pool? (`grep "Unified
   pool:" /tmp/diplomat-serial.log`)
2. Is the start address > `0x100000000`?
3. If yes, check the `AllocatePages` flag in
   `diplomat/main/pagetable_<arch>.go::allocatePhysPages`. It must be
   `AllocateMaxAddress`, with the seed `physAddrResult = linearMapMaxPA - 1`.

See also `memory/x86_64_boot_oom_fix.md`.

---

## RISC-V Footprint Survey (2026-04-24)

Survey done before the RISC-V removal phases (task_plan.md "RISC-V
Removal — Phases R1–R6"). Recorded here so future-me can verify the
removal hit everything and understand the scope at a glance.

### Source files (~98 named `*riscv64*`)

- **diplomat/main/** (23): boot, DTB parsing, UART, exception handling,
  paging, firmware params. Key files: `boot_riscv64.go`,
  `bootstrap_riscv64.s`, `diplomat_entry_riscv64.s`,
  `exc_vectors_riscv64.s`, `pagetable_riscv64.go`, `uart_riscv64.go`.
- **kmazarin/** (61 across subdirs):
  - kmazarin/ (27): core init, exception handling, IRQ via PLIC, SMP,
    signal frame/VDSO, UART top-half, context save, preempt.
  - ksyscall/ (11): ELF arch detect, mmap addr translation, hwprobe,
    syscall entry/exit, kernel async wait.
  - kmem/ (5): paging, memory barriers, spinlocks, VDSO sigreturn.
  - kirq/ (3): IRQ dispatch, panic, preempt handlers.
  - device/virtio/ (4): VirtIO block MMIO, IRQ handlers, GPU IRQ, input.
  - ktimer/ (2): platform timer.
  - other (9): PLIC driver, serial UART, PCI ECAM, spinlock, MMIO.
- **kmazarin/arch/riscv64/** — standalone dir: `plic/plic.go`,
  `plic_riscv64.s`. Delete the directory.
- **Remaining 11**: `shared/constants/addresses_riscv64.go`,
  `shared/fs/fat32/debug_riscv64.s`, `runtime-patches/` (3: trampoline,
  rt0, sys_linux), `maz/linux/stat`, `maz/fs/stat`, `mazarin/`
  (maz_name, asm_linux, memcopy).

### Build tags (68 files reference riscv64)

- 45 pure `//go:build riscv64` — safe to delete with the file (Phase R2).
- 23 combined-tag files (Phase R3 surgical edits):
  - `arm64 || amd64 || riscv64` — 6 files (kmazarin/ds/*).
  - `arm64 || riscv64` — clone.go, soft_irq.go.
  - `linux && (amd64 || arm64 || loong64 || riscv64)` — mmap.go,
    cgo_mmap.go.
  - `amd64 || arm64 || loong64 || mips64 || mips64le || ppc64 ||
    ppc64le || riscv64 || s390x || wasm` — tagptr_64bit.go.
  - `!riscv64` — maz_name_other.go (negation; collapses on removal).

### Taskfile targets (11 named tasks)

Root Taskfile.yml: `run-riscv64`, `run-riscv64-background`,
`run-riscv64-direct`, `run-riscv64-direct-background`, `stop-riscv64`,
`stop-riscv64-direct`, `disk-riscv64`, `boot-riscv64`,
`stage-embedded-fs-riscv64`, `stage-kernel-config-riscv64`,
`userspace-build-riscv64`, `shepherd-overlay-riscv64`,
`merged-shepherd-overlay-riscv64`.

Sub-Taskfiles: `diplomat:riscv64`, `kmazarin:riscv64`.

QEMU monitor port: 4447 (vs 4445 x86_64, 4446 arm64).
OpenSBI bin path referenced for `run-riscv64-direct`:
`opensbi-riscv64-generic-fw_dynamic.bin` (homebrew QEMU share dir).

### Configs (3 TOML)

`config/kernel.riscv64.toml`, `config/rachel.riscv64.toml`,
`config/startup.riscv64.toml`.

### Tooling

- `cmd/mkesp/main.go` — arch dispatch case.
- `cmd/gen-ast-stubs/main.go` — `_riscv64` in suffix list.
- `cmd/fix-go-elf/inject.go` — RISC-V JAL trampoline comment block.
- `shared/sysid/sysid.go` — `RiscvHWProbe` enum (#258).
- `cmd/elf2pe/main.go`, `cmd/gen-overlay/main.go` — generic, no edit
  (per survey).

### Docs and memory

- CLAUDE.md: 7 references (env vars, run examples, monitor ports,
  current status section).
- design/: 0 references (clean).
- Memory files: riscv_crash_investigation.md (dedicated), plus partial
  references in MEMORY.md, response_test_failures.md,
  zero_guard_false_positive.md, dma_clump_continuation.md,
  phase5_linux_plugin_done.md, go126_randomized_heap_base.md.
- Separate worktree memory project at
  `-Users-iansmith-mazzy-riscv/memory/` — not touched by this removal
  (different worktree). Note for cleanup: the worktree itself may want
  archiving once removal lands.

### Mazlink / mazdl status

No RISC-V code in `mazlink-patches/` or `mazarin/mazdl/`. RISC-V is
strictly diplomat→kmazarin bootflow + legacy `.maz`+maz-reloc userspace.
Confirms "riscv64 stays on legacy .maz+maz-reloc path" decision.

---

## Fs ↔ Linux Delegate Handler Deadlock (2026-04-24)

### The cycle

When bleve (or any client) writes to /tmp, the call path is:

1. Caller `pwrite(/tmp/…)` → kernel delegates to linux.
2. Ring 1 reader enqueues the request on `delegateCh` (cap 8).
3. `startUringDelegateHandler`'s single goroutine calls `handler.handle(v)` →
   `h.fs.Pwrite64(...)` → `fsclient.call()` → `uring.Send` to fs →
   blocks on `<-c.RespCh`.
4. fs's single serve goroutine runs `ipcWrite`, calls a diagnostic
   `fmt.Printf(...)`.
5. fs's `fmt.Printf` → `Write(fd=1)` syscall → kernel delegates to linux →
   ring 1 → `delegateCh`.
6. Linux's delegate goroutine is still blocked in step 3; message sits in
   `delegateCh`; fs's `Write` never gets `v.Reply()`.
7. fs is blocked in kernel waiting for the reply. fs's IPC serve loop never
   resumes. Linux's `RespCh` never arrives. Both shepherds frozen.

The workaround introduced previously — route fs diagnostics to
`sys.UartWriteString` instead of `fmt.Printf` — silences the cycle for the
one known caller but leaves the class of bug intact. Any future delegated
syscall from inside an fs IPC handler (not just fd≤2 writes) re-trips it.

### Why not "goroutine-per-delegate-request"

Literal Option B (spawn a new goroutine for every delegate request) requires
wrapping a lot of state in mutexes:

- `fsclient.Client` — shared 64KB data area, single `RespCh` with no ReqID
  match, non-atomic `nextID`. Two concurrent callers would clobber each
  other's paths / buffers and receive each other's responses.
- `syscallHandler.shepherds map[int16]*ShepherdFilesystemData` — plain map,
  concurrent access = fatal race.
- `syscallHandler.flocks`, `syscallHandler.cache`, `sidStates`, `reqQueue` —
  all documented single-goroutine.

And once you mutex all of those, fs-bound work serialises right back to
one-at-a-time (fs itself is single-goroutine by design — ext2 is not
thread-safe, and the per-connection shared data area holds one request at a
time). You'd pay the cognitive cost of locks for no throughput win.

### Fix: two-lane delegate handler

Peel stdout (fd ≤ 2 Writes) off onto its own lane, since that's the only
thing that currently needs to proceed while an fs call is in flight:

- Ring-1 reader uses `OnFunc` to inspect each `FSDelegateReq`:
  - `Write` with `fd ≤ 2` → dispatch to `stdoutCh` (stdout lane).
  - everything else → `delegateCh` (file lane, unchanged).
- **Stdout lane goroutine** only does `sidIncRef`, copy bytes, `v.Reply(len)`,
  `sidDecRef`, push to `dataCh`. Never touches `fsclient` or
  `h.shepherds`. Can run concurrently with the file lane.
- **File lane goroutine** is today's existing worker, single-goroutine,
  consumes file/stdin/notification work, blocks on `fsclient.call` as
  needed. No change to per-shepherd state safety.
- `sidStates` is the only state touched by both lanes, so it gets a small
  `sync.Mutex`. It's a counter map, not fs state — trivial surface.

Result: while the file lane is blocked on fs's `RespCh`, the stdout lane
freely pumps fs's `fmt.Printf` bytes through `v.Reply`; fs unblocks; file
lane unblocks.

Making fs (and fsclient) genuinely concurrent is a separate architectural
project — thread-safe ext2 + per-caller data slots + ReqID-routed responses.
Deferred; not required for this fix.

---

## Smart Cache — eagerCh Drain Race (FIXED 2026-04-23)

### Root cause: mailRespCh called Rebalance before redraw

The first `RespCreateCollection` response sets up the collection. The event loop's
`mailRespCh` handler previously called `cache.Rebalance(vis)` before `redraw("mail-resp")`.
At that point `vis=0` (GridTable had never drawn, so `publishScrollAttrs` had never run).
`Rebalance` is a no-op when `visCount==0` — no fetch fired.

After the buggy sequence, `redraw` called `GridTable.Draw`, which called `buildSlotPool(14)`
and then `publishScrollAttrs(vis=14)`, which fired `eagerCh`. But the `drainDirty` loop at
the top of the next outer event loop iteration consumed that `eagerCh` signal before the
`eagerCh:` select case could run and call `Rebalance(14)`. Result: all 14 slots showed "…"
placeholder text indefinitely.

**Fix:** Swap order in `mailRespCh` handler — `redraw` first, then `Rebalance`:
```go
case resp := <-mailRespCh:
    handleMailResponse(resp)
    redraw("mail-resp")   // GridTable.Draw → buildSlotPool → publishScrollAttrs(vis=14)
    if cache != nil {
        cache.Rebalance(first, last, vis)  // vis=14 now; fetchRange(0,41) sent
    }
```

### eagerCh handler ordering is correct (not the same bug)

The `eagerCh` case reads vis from the published attr (`VisibleRowCountAttr().Get()`), which
holds the value set during a prior Draw. By the time eagerCh fires for scroll changes, vis
is already ≥ 14. So `Rebalance` before `redraw` in the `eagerCh` case is fine.

### Arrow key selection: MoveSelection (added 2026-04-23)

`GridTable.MoveSelection(delta int64)` moves `selectedMsgNum` by delta, scrolls to keep the
selection within the visible window, and calls `publishScrollAttrs + DamageAll`. Exposed via
`GridFrame.MoveSelection`. Mail app intercepts `wm.KeyPress{Action: wm.ActionUp/ActionDown}`
in the `wmCh` handler and calls `gridFrame.MoveSelection(±1)`. The existing `needsRedraw=true`
default triggers `redraw("wm-event")` immediately for snappy response.

---

## Smart Cache — Architecture Notes (2026-04-23)

### Virtual scroll: why the pool has exactly `visibleCount` slots

The pool is sized to `visibleCount` — the number of rows that **fully** fit in the grid
content area (`(contentH - headerH) / rowHeight`, integer division, no rounding up). No
partial rows. This avoids fractional-row edge cases in scroll clamping and damage rects.

On font change the pool is rebuilt. Old slot widgets (RowPercentage + DynamicLabel) are
abandoned in the attr registry (their URIs are unique via `poolEpoch`). They become
unreachable objects. The attr system does not have a deregister path; this is acceptable
because pool rebuilds are infrequent (only when font changes) and the count is small.

### Scroll offset and MsgNum assignment

`scrollOffset` is stored in GridTable. On `ScrollBy(delta)`:
1. `scrollOffset = clamp(scrollOffset + delta, 0, max(0, TotalRows - visibleCount))`
2. For i in 0..visibleCount-1: call `slotPool[i].(MsgNumSetter).SetMsgNum(uint32(scrollOffset + i))`
3. `publishScrollAttrs()` — writes FirstVisible, LastVisible, VisibleRowCount attrs
4. `DamageAll()`

The `MsgNumSetter` interface check is structural (no package import needed):
```go
if setter, ok := row.(interface{ SetMsgNum(uint32) }); ok {
    setter.SetMsgNum(uint32(gt.scrollOffset + int64(i)))
}
```

### Cache window math

With `readAhead = 2` and `visibleCount = 9`:
```
prefetch = 2 × 9 = 18
windowLo = max(0, firstVisible - 18)
windowHi = min(collSize-1, lastVisible + 18)
```
Max window size = 9 + 18 + 18 = 45 entries. Well below maildb's 128-entry batch cap.

At Large font (rowHeight=26): `visibleCount ≈ 40` on a 1200px tall grid →
`prefetch = 80` → max window ≈ 200 entries > 128. If window exceeds 128 maildb will
return only 128 entries. The cache fills partially and the user sees "…" for a moment
before Rebalance fires again. Acceptable for MVP; fix by splitting into two requests
if `(hi - lo + 1) > 128`.

### One in-flight request at a time

`MailCache` tracks one in-flight `KeyHeadersReq` at a time (`inFlight bool`,
`inFlightId [16]byte`, `inFlightLo/Hi uint32`). If Rebalance is called again before
the response arrives AND the window changed, the old request is abandoned: when the
response arrives its reqId won't match `inFlightId` → discarded. A new request is
immediately fired for the new window.

This means we may fetch redundant data if the user scrolls quickly. Acceptable: each
request is small (~45 entries × 240 bytes = ~11KB) and the maildb response is fast.

### Collection events (CollectionAdd / CollectionRemove)

When a `CollectionAdd` arrives (MsgNum = insertion point), all cached entries at
positions ≥ `notif.MsgNum` are now stale (their positions shifted +1). Rather than
shifting the map, the MVP evicts the affected range and triggers Rebalance:

```go
// evict entries that shifted
for k := range c.entries {
    if k >= notif.MsgNum { delete(c.entries, k) }
}
c.collSize++
// let next Rebalance re-fetch
```

Similarly for CollectionRemove (evict ≥ notif.MsgNum, collSize--).
`OnUpdated` fires so the grid shows "…" placeholders briefly during refetch.

### Collection expiry (ErrCollectionExpired in RespKeyHeaders)

If `resp.ErrCode == ErrCollectionExpired`, the cache resets and main must call
`requestCreateCollection()` again. MailCache calls a registered `OnExpired func()`
callback. Main's handler: clear cache, re-request collection (same as current
`onCollectionExpired`). Add `OnExpired func()` field to MailCache.

### Pool widget naming convention

Widgets are named `"{gridName}_s{epoch}_{i}"` for slot i in epoch e. Labels:
`"{gridName}_s{epoch}_{i}_c{j}"`. The `_s` prefix (for "slot") distinguishes from
`_r` (row, legacy) and `_hdr` (header). URIs are globally unique within a session.

### Selection state after pool rebuild

`GridTable.buildSlotPool` must call `gt.RefreshSelected()` after creating the new
slots. The old `selectedRow` pointer references a slot object that no longer exists
in the pool. `RefreshSelected` re-publishes `SelectedAttr` from `selectedRow.MsgNum()`.
With virtual rows, `selectedRow.MsgNum()` returns its current `msgNum` field — which
may no longer be the msgNum the user selected. 

Better: GridTable stores the selected **msgNum** (int64) rather than the **GridRow
pointer**. On pool rebuild, find which slot (if any) has `scrollOffset + i == selectedMsgNum`
and set its `SelectionState = 1`. This is a clean change.

Add `selectedMsgNum int64` (init -1) to GridTable. `setSelected` writes to
`selectedMsgNum` (not `selectedIdx`). `buildSlotPool` applies SelectionState from
`selectedMsgNum` after creating slots.

---

## Smart Caching Prep — Architecture Notes (2026-04-22)

### Face.KnownHeight — implementation notes

`dc.GetFontMetrics(fontID)` returns `FontMetrics{Ascent, Descent int32}` in 26.6
fixed-point (divide by 64 for pixels). `LatinTextFaceImpl.KnownHeight` should return:
```go
func (f *LatinTextFaceImpl) KnownHeight(dc DrawContext) int64 {
    if dc == nil { return 0 }
    f.ensureFont(dc)
    m := dc.GetFontMetrics(f.fontID)
    return int64(math.Ceil(float64(m.Ascent+m.Descent) / 64.0))
}
```
GridTable reads this from the first data row's first label face (if any row exists) during
Draw. It calls `lab.Face.KnownHeight(dc)` where `lab` is `gt.dataLabs[0][0]`. If result
is > 0, update `rowHeightAttr` and `visibleRowsAttr`.

`DynamicLabel` needs a `Face()` accessor (or expose the face field) so GridTable can call
`KnownHeight` on it. Currently the face is unexported — add `Face() mancini.Face` to
`DynamicLabel`.

### GridTable click routing — RowPercentage as Clickable

`RowPercentage` implements `Clickable` since it is the interactor the dispatch hit-test
will resolve to (it covers the full row rect). The existing `ClickAgent` in
`mazarin/mancini/agent_click.go` handles single vs double vs triple discrimination
automatically.

`GridTable.AddRow` sets the callback:
```go
rp.OnClick = func(ev *mancini.InputEvent) {
    gt.setSelected(idx, ev)      // idx captured at AddRow time
}
```
`RowPercentage.Click`:
```go
func (rp *RowPercentage) Click(ev *mancini.InputEvent) bool {
    if rp.OnClick != nil { rp.OnClick(ev) }
    return true
}
```

### GridTable.setSelected — selection state machine

```go
// selectedSet is map[GridRow]bool; shift-click detected via hid.Shift(int64(ev.Mods))
func (gt *GridTable) setSelected(rowIdx int, ev *mancini.InputEvent) {
    row := gt.rows[rowIdx]

    if hid.Shift(int64(ev.Mods)) {
        // toggle in set; primary cannot be removed
        if gt.selectedSet[row] && row != gt.rows[gt.selectedIdx] {
            delete(gt.selectedSet, row)
        } else {
            gt.selectedSet[row] = true
        }
        gt.publishSelectedSet()
    } else {
        // normal click: reset set, set new primary
        gt.selectedSet = make(map[GridRow]bool)
        gt.selectedIdx = rowIdx
        gt.selectedSet[row] = true
        gt.SelectedAttr.Set(int64(row.MsgNum()))
        gt.publishSelectedSet()
    }
    gt.DamageAll()
}
```
`publishSelectedSet` builds the collection value and updates both exports:
```go
func (gt *GridTable) publishSelectedSet() {
    count := len(gt.selectedSet)
    gt.SelectedSetCountAttr.Set(int64(count))
    pages := int64(0)
    if count > 0 { pages = (int64(count) + 511) / 512 }
    gt.SelectedSetPagesAttr.Set(pages)
    var msgNums []int64
    switch {
    case count == 0:
        msgNums = nil
    case count > 256:
        // Sentinel: single MaxInt64 signals "large set — use IPC path".
        // Consumer reads SelectedSetCountAttr to know true size.
        msgNums = []int64{math.MaxInt64}
    default:
        msgNums = make([]int64, 0, count)
        for r := range gt.selectedSet { msgNums = append(msgNums, int64(r.MsgNum())) }
    }
    gt.SelectedSetAttr.Set(msgNums)
}
```
`SelectedSetPagesAttr` is a `ValueI64` (not ConstraintI64) computed inline in
`publishSelectedSet` — avoids needing compiled `.vgo` bytecode.
`math.MaxInt64` is a safe sentinel — valid msgNums are uint32 (max 4,294,967,295),
far below MaxInt64 (9,223,372,036,854,775,807).

**TODO (large-collection IPC path, deferred):** When a consumer sees the sentinel,
it allocates `ceil(count / entriesPerPage)` shared pages and passes them to the grid
via a yet-to-be-designed IPC message. The grid iterates `selectedSet` and fills the
pages, then signals completion. Required for bulk mail operations (e.g. move all
messages from a given sender to a folder). The `SelectedSetCountAttr` export exists
precisely to make this allocation calculation possible.

### ValueCollI64 new infrastructure

The existing query-result collection region (`RegionCollCap = 65536`) is **completely
full**: all 65,536 slots are reserved for `MaxQueryPatterns(64) × MaxCollPerQuery(1024)`.
There is no room for user-settable collection attributes.

**Solution:** A new dedicated `ValueColl` region in the shared constraint page.
`ConstraintPageVersion` bumps from 3 → 4.

**Sizing:** `RegionValueCollSlots = 32` attributes × `MaxValueCollEntries = 256`
entries each = 8192 Value elements = 8192 × 40 bytes = 320KB additional shared memory.
The 256-entry cap is a deliberate design rule: the constraint network is for UI-scale
values. Larger collections (e.g. select-all on a 50K mailbox) belong in the maildb
collection protocol as a filter descriptor, not an explicit enumerated set.

**New region layout** (appended after existing `RegionCollSize`):
```
RegionValueCollOff  = RegionCollOff + RegionCollSize
RegionValueCollSize = RegionValueCollSlots * MaxValueCollEntries * valueSize
```
Each value-collection attribute claims one of the 32 slots at creation time
(tracked by a free-list bitmap in the kernel, similar to query slot allocation).

**New syscall:** `SysAttrWriteCollI64 = MazzySyscallBase + 45  // 0x102D`
(slot 45; slot 44 = SysRequestWindowManager was already occupied).

**Kernel handler** in `constraint_syscall.go`:
- Args: `slot uint16, userVA uintptr, count uintptr, isConstraintResult uint`
- Returns EINVAL if `count > MaxValueCollEntries`
- Reads `count` int64 values from `userVA` via `WalkUserPageTable` (same pattern as
  `SyscallUringSend`'s cross-page read; up to 2KB = half a page for 256 entries)
- Writes each as `flat.Value{Typ: TypeI64}` into the attribute's ValueColl region slot
- Constructs `CollRef{ElemType: TypeI64, RegionOffset: slotOff, Count: count}`
- Stores `flat.NewCollection(ref)` into attribute node; calls dirty propagation

**Transport note:** 256 int64s = 2KB — fits within a single page, so at most 2
`WalkUserPageTable` calls (if the slice straddles a page boundary). No multi-page
copying needed.

**Userspace chain:**
```
sys.AttrWriteCollI64(slot, values, false)      // mazarin/sys/constraint.go
attr.ValueCollI64(uri, initial) → isCollI64    // mazarin/attr/attribute_value.go
Attribute[[]int64].Set(v) → isCollI64 branch  // mazarin/attr/attribute.go
```
`ValueCollI64` returns `*Attribute[[]int64]` with `isCollI64: true`.
The `Set` method checks `isCollI64` before `isStr`, casts `v` via `unsafe.Pointer`
to `[]int64`, and calls `sys.AttrWriteCollI64`.
Consumers: `selectedSetAttr.Get()` returns `[]int64` directly.

### RowPercentage selection background

`RowPercentage.Draw` fills the row background before drawing children:
```go
switch rp.SelectionState {
case 1: // primary selection
    dc.SetColor(pal.Highlight())
    dc.DrawRectangle(float64(x), float64(y), float64(w), float64(h))
    dc.Fill()
case 2: // in set, not primary
    dc.SetColor(pal.Accent())
    dc.DrawRectangle(float64(x), float64(y), float64(w), float64(h))
    dc.Fill()
}
```

`GridTable.Draw` sets each row's `SelectionState` before calling its draw:
```go
for i, rp := range gt.rowWidgets {
    msgNum := gt.rows[i].MsgNum()
    switch {
    case gt.selectedIdx == i:
        rp.SelectionState = 1
    case gt.selectedSet[msgNum]:
        rp.SelectionState = 2
    default:
        rp.SelectionState = 0
    }
}
```
`gt.rowWidgets []*RowPercentage` is a new field parallel to `gt.rows`, populated
in `AddRow`.

### SelectedSetPagesAttr — inline computation (not a .vgo constraint)

`SelectedSetPagesAttr` is a `ValueI64` computed directly inside `publishSelectedSet()`:
```go
pages := int64(0)
if count > 0 { pages = (int64(count) + 511) / 512 }
gt.SelectedSetPagesAttr.Set(pages)
```
The `.vgo` / `ProgComputeNeededPages` approach from the original plan was skipped —
`compile-constraints` cannot be run during implementation, and the arithmetic is simple
enough to compute inline. Functionally equivalent: the attr is updated every time
`selectedSet` changes.

### Constraint URI summary

| Attribute | URI | Type | Initial | Notes |
|-----------|-----|------|---------|-------|
| Row height | `layout:///NAME/int64/grid/rowHeight` | ValueI64 | 0 | 0 = not yet known |
| Visible rows | `layout:///NAME/int64/grid/visibleRows` | ValueI64 | 0 | 0 = not yet known |
| Primary selected | `layout:///NAME/int64/grid/selected` | ValueI64 | -1 | -1 = none |
| Selected set | `layout:///NAME/int64/grid/selectedSet` | ValueCollI64 | empty | `[MaxInt64]` = large-set sentinel |
| Selected set count | `layout:///NAME/int64/grid/selectedSetCount` | ValueI64 | 0 | Always the true count; use when sentinel active |
| Selected set pages | `layout:///NAME/int64/grid/selectedSetPages` | ValueI64 | 0 | `ceil(selectedSetCount / 512)`; computed inline in publishSelectedSet; pages to allocate for IPC path |

Note: the selectedSet URI uses `DataTypeInt64` (element type); the collection wrapper
is carried in the flat `Value.Typ = TypeCollection` field, not the URI segment.

`NAME` is the GridTable's `myName` (e.g. `"mail_tbl"`).

### GridRow interface extension

Add `MsgNum() uint32` to `std.GridRow`. Existing implementors:
- `*MailRow` (`mazarin/apps/mail/mail_row.go`): already has the field, trivial to add.
- Any test/stub rows in tests: add `MsgNum() uint32 { return 0 }`.

---

## Existing Protocol (to be removed)

**Protocol IDs:** ProtoMailReq=13, ProtoMailResp=14  
Old message types **MsgTypeGetHeaders(1), MsgTypeGetBody(2), MsgTypeBodyConfirm(3)** are
being deleted.  GetBody/BodyConfirm were not properly designed; KeyHeaders replaces
GetHeaders.  Remove all three handlers from maildb and remove their encode/decode helpers.

**Badger key schema (existing):**
- `<messageId>` → JSON MailMessage (From, Sender, Subject, Timestamp, BodyLen)
- `body:<messageId>` → raw body bytes
- `date:<RFC3339>:<messageId>` → empty (date index, reverse-chron iteration)

**Missing (must add):**
- `read:<messageId>` → empty key presence = IsRead
- `deleted:<messageId>` → empty key presence = IsDeleted

---

## Wire Format Constraints

- `UringIPCMsg.Payload` = 112 bytes
- First 4 bytes = MsgType (uint32); remaining 108 bytes for RequestId + fields
- **RequestId** = [16]byte (raw UUID bytes; display as hyphenated UUID string when needed)
  - All messages carry RequestId — unsolicited notifications use zero UUID when there is
    no originating client request (e.g. new mail arriving from external source)
- After MsgType(4) + RequestId(16) = 20 bytes overhead, **88 bytes remain** for fields

---

## Proposed Protocol Design (approved by user 2026-04-20)

### Protocol IDs
Extend existing **ProtoMailReq=13** (mail→maildb) / **ProtoMailResp=14** (maildb→mail).
New MsgType values added; old 1/2/3 removed.

### Error Codes
```
ErrNone              = 0
ErrCollectionExpired = 1   // collId not in live set; client must CreateCollection again
ErrInvalidMsgNumber  = 2   // msgNum out of range for collection
ErrMessageNotFound   = 3   // messageId not in badger
ErrBadgerError       = 4   // internal DB error
ErrFilterInvalid     = 5   // unknown filter type or malformed FilterArg
```

### Request Messages (mail → maildb)
All requests: MsgType(4) + RequestId[16] + fields.

```
MsgTypeMessageCount     = 10   // no extra fields                                   (20 bytes)
MsgTypeKeyHeaders       = 11   // CollId uint32, From uint32, To uint32             (32 bytes)
MsgTypeAllHeaders       = 12   // CollId uint32, MsgNum uint32                      (28 bytes)
MsgTypeLatestUnread     = 13   // no extra fields                                   (20 bytes)
MsgTypeBody             = 14   // CollId uint32, MsgNum uint32                      (28 bytes)
MsgTypeMarkRead         = 15   // CollId uint32, MsgNum uint32                      (28 bytes)
MsgTypeMarkDeleted      = 16   // CollId uint32, MsgNum uint32                      (28 bytes)
MsgTypeCreateCollection = 17   // FilterType uint32, SortOrder uint32, FilterArg [64]byte  (92 bytes ≤ 108 ✓)
```

**SortOrder values:**
```
SortDesc = 0   // newest-first (default inbox order); badger Reverse:true iterator
SortAsc  = 1   // oldest-first; forward badger iterator
```
`FilterAll + SortDesc` = the inbox. Its `totalSize` comes from `count:all` — no key scan.

### Response Messages (maildb → mail, in reply to a request)
All responses echo the RequestId of the originating request.

```
MsgTypeRespMessageCount     = 50   // Count uint64, ErrCode uint32                  (32 bytes)
MsgTypeRespKeyHeaders       = 51   // TargetVA uint64, NumBytes uint32, Count uint32, ErrCode uint32  (40 bytes)
MsgTypeRespAllHeaders       = 52   // TargetVA uint64, NumBytes uint32, ErrCode uint32               (36 bytes)
MsgTypeRespLatestUnread     = 53   // CollId uint32, MsgNum uint32, TargetVA uint64, NumBytes uint32, ErrCode uint32  (44 bytes)
MsgTypeRespBody             = 54   // TargetVA uint64, NumBytes uint32, ErrCode uint32               (36 bytes)
MsgTypeRespMarkRead         = 55   // ErrCode uint32                                (24 bytes)
MsgTypeRespMarkDeleted      = 56   // ErrCode uint32, NewSize uint32                (28 bytes)
MsgTypeRespCreateCollection = 57   // CollId uint32, Size uint32, ErrCode uint32    (32 bytes)
```

### Unsolicited Notification Messages (maildb → mail, pushed without a request)
RequestId = originating client's request UUID if the change came from a client operation;
zero UUID if the change arrived from an external source (e.g. new mail delivery).
**Clients MUST NOT assume the RequestId matches one of their own outstanding requests.**

```
MsgTypeCollectionAdd    = 60   // CollId uint32, MsgNum uint32, NewSize uint32, RequestId [16]byte  (28 bytes + type = 32)
MsgTypeCollectionRemove = 61   // CollId uint32, MsgNum uint32, NewSize uint32, MsgId [64]byte, RequestId [16]byte  (96 bytes + type = 100 ≤ 108 ✓)
```

- **CollectionAdd**: new message arrived in this collection (new mail or filter match).
  Client should issue KeyHeaders(collId, MsgNum, MsgNum) to fetch header data.
- **CollectionRemove**: message was deleted from this collection (by any client or expiry).
  Includes MsgId so client can find and remove the row even with stale msgNum mapping.
  After receiving this, client must renumber its local row-to-msgNum mapping.

### Page Layouts (for data-returning responses)

**KeyHeaderEntry** (fixed-size; one per message in [From, To]):
```go
type KeyHeaderEntry struct {
    Sender  [64]byte   // null-terminated UTF-8
    Subject [128]byte  // null-terminated UTF-8
    Date    [32]byte   // RFC3339 null-terminated
    MsgNum  uint32     // message number within collection
    Flags   uint32     // bit 0 = IsRead, bit 1 = IsDeleted
    _pad    [8]byte
}
// sizeof = 240 bytes
// 50 entries = 12,000 bytes = 3 pages
```

**AllHeaderEntry** (fixed-size; one message full RFC headers; fits in 1 page):
```go
type AllHeaderEntry struct {
    From        [128]byte
    To          [256]byte
    CC          [256]byte
    Subject     [256]byte
    Date        [64]byte
    MessageId   [128]byte
    ContentType [128]byte
    MsgNum      uint32
    Flags       uint32
    _pad        [8]byte
}
// sizeof = 1,232 bytes < 4096 → 1 page
```

**Body** — raw bytes of stored message body (UTF-8 / quoted-printable / base64 as-is).
Caller decodes MIME if needed.

### Filter Types

```
FilterAll       = 0   // all messages; FilterArg unused; count from count:all
FilterUnread    = 1   // unread messages only; FilterArg unused; count from count:unread
FilterFrom      = 2   // sender contains substring; FilterArg = query bytes; count+hits via fti
FilterSubject   = 3   // subject contains substring; FilterArg = query bytes; count+hits via fti
```
SortOrder applies to all filter types.

FilterFrom and FilterSubject route through the `fti` shepherd:
- `createCollection`: maildb sends SearchMail to fti with `Size=0` → `Total` = totalSize (O(hits) in bleve, no doc retrieval)
- `loadWindow(from, to)`: maildb sends SearchMail to fti with `From=from, Size=128, SortBy=date` → page of messageIds → populate collection.entries

---

## fti Search Protocol (additions to shared/fti/protocol.go)

The existing fti protocol only has indexing (MsgTypeIndexDocument=1).  We add search.
New message types follow the same encode/decode pattern already in place.

### New MsgType constants
```
MsgTypeSearchMail   = 2   // maildb → fti: search request
MsgTypeSearchResult = 20  // fti → maildb: results (page transfer) or count-only
MsgTypeSearchError  = 21  // fti → maildb: search failed
```

### SearchMail request (maildb → fti)
MsgType(4) + RequestId[16] + fields, all ≤ 108 bytes:
```go
type SearchMail struct {
    RequestId  [16]byte  // correlation ID; echoed in response
    QueryType  uint32    // 1=Subject, 2=From (matches FilterSubject/FilterFrom)
    SortOrder  uint32    // SortDesc=0, SortAsc=1
    From       uint32    // offset into result set (for pagination)
    Size       uint32    // hits to return; 0 = count only (no page allocation)
    QueryLen   uint16
    Query      [58]byte  // search term, null-terminated
}
// 16+4+4+4+4+2+58 = 92 bytes + 4 (MsgType) = 96 ≤ 108 ✓
```

### SearchResult response (fti → maildb)
```go
type SearchResult struct {
    RequestId [16]byte  // echoed from request
    TargetVA  uint64    // VA of transferred page in maildb's address space (0 if Size=0)
    NumBytes  uint32    // bytes in page
    Count     uint32    // hits returned in this page
    Total     uint32    // total hits in result set (always set, even for Size=0)
    ErrCode   uint32
}
// 16+8+4+4+4+4 = 40 bytes + 4 (MsgType) = 44 ≤ 108 ✓
```

### SearchResultEntry page layout
Page contains a packed array of `SearchResultEntry`:
```go
type SearchResultEntry struct {
    IdLen uint16
    _pad  [6]byte
    DocId [80]byte   // message ID, matching existing DocId field size in IndexDocument
}
// sizeof = 88 bytes; 46 entries per 4096-byte page; 128 entries = 3 pages
```
fti allocates pages, writes entries, calls `TransferPages` to maildb.
maildb reads entries, populates `collection.entries[from..from+count-1]`, frees pages.

### fti search handler
New `search_handler.go` in `maz/fti/`:
- Receives SearchMail, runs `bleve.SearchRequest{Query, From, Size, SortBy:date}` 
- `Size=0`: return SearchResult with Total set, TargetVA=0, no page allocation
- `Size>0`: allocate pages, pack SearchResultEntry array, TransferPages to requester

---

## BadgerDB Count Capability (v4.9.1) — Confirmed

BadgerDB has **no O(1) count for a key prefix**.  Counting requires iteration.

- `PrefetchValues=false` key-only iteration exists and is fast; maildb already uses it
- `DB.Tables()` has per-table `KeyCount uint32` but not prefix-scoped (partial tables skew it)
- `DB.EstimateSize(prefix)` returns byte sizes only, and misses partial tables
- No `Count()`, `Len()`, or equivalent on `DB` or `Txn`

**Consequence for collection size:** cannot compute a count without an O(n) key scan.

**Solution — persistent counters in badger:**
- `count:all` → little-endian uint64 — maintained on every message import and delete
- `count:unread` → little-endian uint64 — maintained on MarkRead and message import
- These give O(1) totalSize for `FilterAll` and `FilterUnread`
- Ad-hoc filters (`FilterFrom`, `FilterSubject`) still require a key scan at CreateCollection;
  accepted cost since those are user-initiated and infrequent

---

## Collection Design

### Semantics
- maildb holds at most **16 live collections** (fixed-size LRU pool)
- `CollId` is monotonically increasing uint32 starting at 1; 0 = invalid
- LRU eviction: creating the 17th collection evicts the least-recently-used
- `lastUsed` is updated on every request touching a collection
- Looking up a stale CollId → ErrCollectionExpired; client must CreateCollection again
- Collections are **HOT**: maildb pushes CollectionAdd / CollectionRemove unsolicited
  whenever the live set of messages changes for any entry currently loaded in that collection

### Collection is a Sparse Array
A collection is a **sparse array** of msgNum → messageId.  Only entries that have been
explicitly loaded (via a KeyHeaders or similar request) are held in memory.  The rest are
not loaded.  `totalSize` is always known (from the persistent counter or a key scan), but
the individual entries are populated on demand.

```go
type collection struct {
    id          uint32
    filterType  uint32
    sortOrder   uint32        // SortDesc=0, SortAsc=1
    filterArg   [64]byte
    totalSize   int           // from persistent counter (O(1)) or key scan (ad-hoc filters)
    lastUsed    time.Time
    subscribers []int16       // SIDs to notify on Add/Remove

    // Sparse: only loaded entries are present (≤128 entries in a typical window)
    entries    map[uint32]string   // msgNum → messageId
    msgIdToNum map[string]uint32   // reverse: messageId → msgNum (for O(1) removal/notification)
}
```

### Window Loading (lazy, 128-entry cap per load)
When `KeyHeaders(collId, from, to)` arrives and entries [from, to] are not in `entries`:

1. Open a key-only iterator (`PrefetchValues=false`) on the `date:` prefix
2. If `filter == FilterUnread`: iterate only `date:` keys for which `read:<msgId>` does NOT
   exist (requires two lookups per key — acceptable; optimize with bloom filter later)
3. Skip the first `from` matching keys (O(from) key reads, no value loads)
4. Collect the next `to-from+1` messageIds (capped at 128 per load)
5. Store in `entries[from..to]`, store reverse in `msgIdToNum`
6. EnsureReified each loaded messageId in the MessageStore (membership tracking)

**Cost for typical usage:** loading first page (from=0, to=49) costs 0 skips + 50 key reads.
Loading page 2 (from=50, to=99) costs 50 skips + 50 key reads.  For message 10,000:
~10K key reads at ~60 bytes each ≈ 600KB read, likely < 10ms on the ramdisk.

**No cursor persistence:** badger iterators are transaction-scoped and cannot be persisted
between requests.  Each window load opens a fresh read-only transaction.

### Subscription
Each collection has a `subscribers []int16` list.  Today, one subscriber per collection
(the SID that called CreateCollection).  The list supports future multi-subscriber scenarios
(e.g. two mail-ui.maz instances sharing a collection).

---

## Reverse Lookup: Which Collections Contain a MessageId?

**Answer: iterate the 16-slot collection pool and check each `coll.msgIdToNum[msgId]`.**

With a hard cap of 16 live collections, this is 16 map lookups — effectively O(1).
No separate reverse index is needed.  This is correct by construction: if a messageId
is not in `coll.msgIdToNum`, it simply isn't loaded in that collection.

This means **`MessageRecord.memberships` is dropped** from the MessageStore.
The MessageStore becomes a pure **lazy data cache** — no membership tracking.

Notification fan-out for a MarkDeleted or external add:
```
for each coll in collectionPool.slots:
    if msgNum, ok := coll.msgIdToNum[msgId]; ok:
        // this collection has the message loaded — notify its subscribers
        coll.entries[msgNum] = ""  // tombstone
        delete(coll.msgIdToNum, msgId)
        coll.totalSize--
        send CollectionRemove{coll.id, msgNum, coll.totalSize, msgId, reqId}
          to each sid in coll.subscribers
```

---

## MessageStore Data Structure

The MessageStore is a **lazy data cache** keyed by messageId.  It deduplts badger reads
when the same message is loaded across multiple collections.

```go
type MessageRecord struct {
    mu        sync.Mutex
    messageId string

    // Lazy fields — nil = not yet fetched from badger
    headers   *MailMessage
    body      []byte
    isRead    *bool    // nil = unknown
    isDeleted *bool
}

type MessageStore struct {
    mu      sync.RWMutex
    records map[string]*MessageRecord  // messageId → record
}
```

### MessageStore Operations
- `Ensure(msgId)` — create record if absent (no data loaded yet)
- `LoadHeaders(msgId) *MailMessage` — lazy fetch from badger `<msgId>` key
- `LoadFlags(msgId) (isRead, isDeleted bool)` — lazy fetch `read:<msgId>` / `deleted:<msgId>`
- `LoadBody(msgId) []byte` — lazy fetch `body:<msgId>`
- `MarkRead(msgId)` — set isRead=true, write `read:<msgId>` to badger
- `MarkDeleted(msgId)` — set isDeleted=true, write `deleted:<msgId>` to badger;
  caller is responsible for iterating collections and sending notifications
- `Evict(msgId)` — remove record from cache; called after all collections evict it

---

## Notification Flow: MarkDeleted Example

1. mail app sends MarkDeleted{CollId=3, MsgNum=7, RequestId=R1}
2. maildb handler:
   a. Look up collection 3 → confirm MsgNum=7 is loaded → get messageId "abc@example.com-1234"
   b. Call `store.MarkDeleted("abc@example.com-1234")` → persists to badger
   c. Iterate all 16 collection slots: for each coll where `coll.msgIdToNum[msgId]` exists:
      - tombstone coll.entries[msgNum], remove from coll.msgIdToNum, decrement coll.totalSize
      - send CollectionRemove{coll.id, msgNum, coll.totalSize, msgId, R1}
        to every sid in coll.subscribers
        (this includes other clients that have the same message loaded)
   d. send RespMarkDeleted{ErrCode=0, NewSize=N-1} back to the requesting client
3. Requesting client gets RespMarkDeleted first (direct response), then CollectionRemove
   (fan-out notification, may arrive in any order relative to other clients' notifications)

---

## Read/Deleted Persistence in Badger

New keys to add:
- `read:<messageId>` → empty value; key presence = message is read
- `deleted:<messageId>` → empty value; key presence = message is deleted

`LoadFlags` checks for key existence with `db.View` + `txn.Get`.
`MarkRead` / `MarkDeleted` write the key in a `db.Update` transaction.

---

## Uring Infrastructure Notes

- `mazarin/uring/reader.go`: `Dispatcher.On(protocol, decodeFn, channel)` — typed dispatch
- `mazarin/uring/syscall.go`: `Send(targetSID, msg)` — non-blocking send
- Pages for data responses: `mem.AllocPagesSlice(n, mem.PageShared)` in maildb
- After `TransferPages`, maildb MUST NOT access the transferred VA
- Caller (mail app) calls `mem.FreePages(va, numPages)` after consuming data

### UringIPCMsg is always exactly 128 bytes

`UringIPCMsg` is a fixed-size struct (enforced by compile-time assertion in
`shared/ipc/uring_ring.go:93`):
```
Protocol(4) + SenderSID(2) + Flags(2) + SenderID(8) + Payload(112) = 128 bytes
```
`UringIPCSlotSize = 128` is a ring-layout constant (64 slots × 128B per slot = one ring
page). This size cannot change without restructuring the ring.

### MapPAToKernelScratch is free — no release needed

`kmem.MapPAToKernelScratch(pa)` is `pa + KernelMMIOOffset` — a pure arithmetic
translation. Diplomat sets up permanent 2MB block descriptors covering all physical RAM
at that offset. There are no TLB entries to flush, no page table writes, and nothing to
release after use. The function is safe to call from any goroutine at any time.
`KernelScratchVA` (the old single-slot PT approach) is dead code; ignore it.

### CollectionAdd double-counting race and fix (2026-04-21)

**The race:** `createCollection` counted date-index keys to determine `totalSize`, but the
count was taken BEFORE acquiring `cs.mu`. Meanwhile the import goroutine could commit a
message, yield (preempted by Go scheduler), then resume and call `addMessage` for that
same message. `addMessage` found a live collection and incremented `totalSize` again →
spurious CollectionAdd with `newSize = actual + 1`.

**Fix contract:** Both `createCollection` and `addMessage` must hold `cs.mu` when accessing
or mutating `totalSize`, AND the count used to initialize `totalSize` must also be taken
under `cs.mu`. This makes the "count + assign `totalSize`" atomic with respect to `addMessage`'s
"count + compare + increment".

**Guard in `addMessage`:**
```go
currentCount, _ := cs.countDateIndex()   // inside cs.mu
if currentCount <= coll.totalSize {
    continue  // already counted at createCollection time
}
```
`currentCount` is the ground-truth date-index size. If it equals `coll.totalSize`, the
message was committed to badger before `createCollection` finalized → skip. The guard is
safe for concurrent imports: each `addMessage` call adds exactly one specific `msgId`, and
the comparison is monotonically correct because the import goroutine commits messages
one-at-a-time in sequence.

**RefreshRequest pattern:** When `handleCollectionAdd` shifts existing rows, any row whose
`KeyHeaders` request is still in flight must re-fire the request with the updated `msgNum`.
The old in-flight request will return data for position N (which is now occupied by the
newly inserted message, not the row that was shifted). `MailRow.RefreshRequest(newReqId)`
atomically updates the row's `requestId`, fires a new request, and returns the old ID for
removal from the `rowByReqId` lookup table.

---

### FTI bleve scorch merger panic — directory mmap (2026-04-25, OPEN)

After the linux ring 1 split-ring fix unblocked the indexing pipeline,
the scorch merger panic still surfaces. Captured in flight by the
ext2 dirent verifier:

```
[fs:read EISDIR] handle=220 path="/tmp/fti-1/bleve" inum=12 ftype=2
                 isDir=true openSize=4096 offset=0 count=4096
[mmap-fill] sid=1 fd=6 offset=0 READ ERR: errno -21
[fti] PANIC in handleIndexDocument id=...: runtime error: invalid
  memory address or nil pointer dereference — marking index corrupted
[fti] SCORCH ASYNC ERROR: source: merger, async panic error: panic:
  ... path: /tmp/fti-1/bleve/store
```

**Real root cause: bleve mmap'd the index DIRECTORY (`/tmp/fti-1/bleve`),
not a `.zap` segment file.** fti's user-space fd=6 was opened against
the directory itself; scorch then called `mmap(fd=6, ...)` and the
kernel happily accepted it (no fd-type validation). The first page
fault triggered `sysMmapPageFill`, which delegated to fs.maz; fs.maz's
`ipcRead` opened the inode, found `IsDir() == true`, and returned
`ext2.ErrNotFile` (mapped to errno -21 = EISDIR). The kernel-side
`SyscallReply` for `MmapPageFill` then **mapped the zero-filled page
into the caller's address space anyway** and woke the faulting code,
which read zeros where it expected a segment header → nil deref.

**Why it previously looked like dirent corruption**: the earlier "no
such file or directory" symptom is a *different* aspect of the same
panic chain — once one scorch operation panics, the index is marked
corrupted; subsequent merge/persist attempts try to open files scorch
*thought* it had created in earlier iterations, fail with ENOENT, and
log the visible "no such file or directory" line.

**On real Linux**, `mmap()` of a directory fd returns `ENODEV`
("Operation not supported"). Bleve handles that error cleanly without
corrupting state. Mazzy's `SyscallMmap`
(`kmazarin/ksyscall/mmap.go::SyscallMmap`) doesn't validate fd type at
mmap-time; the failure is deferred to page-fault time where the
recovery path is wrong (zeroed page mapped instead of SIGBUS or error
propagation).

**Verifier wired up to catch this**: `shared/fs/ext2/writer.go` has a
`DirVerifyHook` (used during this investigation) plus a one-shot
`[fs:read EISDIR]` print in `maz/fs/fsipc.go::ipcRead` that fires when
ext2 returns ErrNotFile from a Read, dumping path/inum/ftype/isDir.
Both stay in tree as defense-in-depth diagnostic.

### Bleve source code review — 2026-04-25

User asked: does bleve actually expect to mmap a directory? Read the
relevant sources:

- **`zapx/v15/segment.go::ZapPlugin.Open`** — `os.Open(path)` then
  `mmap.Map(f, mmap.RDONLY, 0)`. Path is always a `.zap` segment file
  (e.g. `<dir>/00000001.zap`), never a directory.
- **`bleve/v2/index/scorch/persister.go`** — `s.segPlugin.Open(path)`
  at lines 807 / 1019 with `path = s.path + "/" + filename` where
  filename is `zapFileName(epoch)` ⇒ always a `.zap`.
- **`bleve/v2/index/scorch/scorch.go`** — opens `bolt.Open(s.path +
  "/root.bolt")`. Bolt then mmaps the bolt file's fd, not the dir.
- **bbolt `bolt_unix.go`** — `unix.Mmap(int(db.file.Fd()), 0, sz,
  PROT_READ, MAP_SHARED|MmapFlags)`. `db.file` is the bolt regular
  file, set up with `os.OpenFile(path, ...)`.
- bleve does also call `os.ReadDir(s.path)` in `diskFileStats` and
  similar — opens the dir, reads dirents via `Readdirnames`, **closes**
  the fd. No mmap.

So **bleve never mmaps a directory on Linux**. On Linux, `mmap()` of a
directory fd would return `ENODEV` upfront and bleve would handle it
cleanly. **The bug is on our side.**

### Refined diagnosis (2026-04-25)

Re-running with `[fs:read EISDIR]`, `[fs:open-dir]`, `[fs:mkdir-ok]`
diagnostic prints surfaced **two distinct symptoms**, both of which
trace back to ext2 metadata not matching the path bleve actually used:

1. **EISDIR on mmap-fill**: `[fs:read EISDIR] handle=N
   path="/tmp/fti-X/bleve" inum=12 isDir=true`. Handle was opened
   against the directory itself; later page fault calls fs.Read which
   returns EISDIR because the inode is a dir. We thought bleve was
   mmap'ing the dir — actually no; what's happening is that handle =
   the dir was opened by Go's `os.ReadDir` for `diskFileStats`, the fd
   was closed, but somewhere the mmap path on a *different* file ended
   up using a stale handle that still points at the dir inum.

2. **EISDIR on write**: `merging err: write
   /tmp/fti-X/bleve/store/000000000044.zap: is a directory`. fs.maz's
   `ipcWrite` opened inum=N at openat-time when N was a regular file,
   but by write-time the on-disk inode at N has been rewritten with
   `Mode = TypeDir`. That can only happen if `Mkdir`'s
   `allocInode` returned an inum that was concurrently held by an
   open file — i.e. **inode-reuse race**.

3. **ENOENT on reopen**: `error opening new segment at
   .../00000000001d.zap: no such file or directory`. Persister wrote
   the file, closed it, and a later reopen sees no entry by that
   name. Same root: the dirent or inode allocation got tangled.

The dirent verifier (left in tree) **never fires** — block-level
invariants are intact, so it's not dirent-block corruption. The bug
is at the inode-allocation / lifecycle level: `freeInode` + `allocInode`
are not coordinated with file-handle lifetime. fs.maz handles each
IPC sequentially so it's not classic concurrency, but Remove + Mkdir
of unrelated paths within a single fs.maz pass can still recycle an
inum that another (earlier-issued) open is still relying on.

### Fix plan — inode lifecycle (approved 2026-04-25)

**Approach (a): reference-count inodes by open handles** — Linux
"unlink-while-open" semantics. `freeInode` becomes "mark for delete";
actual reclamation happens when the last open handle closes. The
dirent is removed eagerly so path lookups return ENOENT, but the
on-disk inode + data blocks + bitmap bit stay alive until refcount
hits zero. `allocInode` naturally skips pinned inums because their
bitmap bit is still set.

**Implementation steps:**

1. **`shared/fs/ext2/`**: add `inodeRefs map[uint32]int` and
   `pendingFreeSet map[uint32]bool` to `FileSystem`. New methods
   `PinInode`, `UnpinInode`, `isInodePinned`. `UnpinInode` performs
   the deferred reclaim (`freeInodeBlocks` + zero inode +
   `freeInode`) when refcount hits 0 and inum is in
   `pendingFreeSet`.

2. **`shared/fs/ext2/writer.go`**: modify `Remove`, `WriteFile`
   overwrite branch, `Rename` target-overwrite branch — always
   remove dirent first; if `isInodePinned`, mark `pendingFreeSet`
   and return; else free immediately as today.

3. **`maz/fs/fsipc.go`**: `ipcOpen` calls `fsys.PinInode(inum)`
   after `conn.allocHandle` succeeds. `ipcClose` (signature widened
   to take `mt *mountTable`) calls `fsys.UnpinInode(h.inum)` before
   `conn.freeHandle`.

4. **Build + 120s ARM64 HVF run**. Expected: 0 EISDIR-on-write,
   0 EISDIR-on-mmap-fill, 0 ENOENT-on-zap-reopen, fti indexes 200+
   docs without `bleve index corrupted`, mail click renders body.

**Risk surface:**
- Pin/Unpin imbalance — every Pin must have exactly one Unpin.
  fsHandle is the natural pairing (Pin in ipcOpen, Unpin in
  ipcClose). Audit `conn.freeHandle` callsites to verify there's
  no path that destroys a handle without unpinning.
- Map growth on read-only mount — gated by `fs.writable` check.

**Decisions deferred to follow-ups (intentional):**

The decisions below are intentionally NOT part of this fix. They are
all tracked separately so we don't rewrite this scope mid-flight:

- **Shepherd-death cleanup of fs handles** — fs.maz's `OnDeath`
  callback (in `maz/fs/main.go::main`) currently just logs the
  death; it does NOT walk the dead shepherd's `conn.handles[]`
  array, close each open handle, or call `UnpinInode`. With (a) in
  place, a shepherd dying with handles still open will leak refs:
  the inodes stay pinned forever, dirents-of-removed-files-with-
  open-handles never reclaim, fs.maz `conn.handles[]` slots leak
  too. Bounded growth (only matters across many shepherd deaths in
  one boot) but real. **Tracked as task #9.** Fix is small once
  inode-lifecycle is correct: the OnDeath callback iterates
  `conn.handles[]`, calls `UnpinInode` for each, removes the
  conn from `s.conns`.

- **Memory-fault-then-kill design** — the kernel's
  `SyscallReply`/`MmapPageFill` path currently maps a zeroed page
  and resumes execution when fs returns an error from a fault.
  Linux semantics here is `SIGBUS` to the offender. With inode
  lifecycle correct, this path becomes mostly unreachable, but if
  it's ever reached the current behavior silently corrupts state.
  **Tracked as task #10.** Needs design — how the kernel signals
  the faulting thread, who terminates the offender, what the
  shepherd's `OnDeath` chain does in response.

- **Hardlink semantics** — ext2's `Remove` doesn't decrement
  `LinksCount`; it assumes 1. Real Linux frees the inode only
  when both `LinksCount == 0` AND no open handles. Our usage
  doesn't make hardlinks today. **Tracked as task #11.** Comment
  the assumption in `Remove` for now.

Tracked as task #8 (re-scoped from "reject mmap on dir fds" to
"inode-lifecycle correctness via Pin/Unpin").

### Implementation status (2026-04-25, LANDED — verification mixed)

All four implementation steps shipped exactly to spec in commit
`2d22496 feat: kernel/mail plumbing for bleve-on-tmpfs`:

- `shared/fs/ext2/reader.go` grew `pinMu sync.Mutex`, `inodeRefs
  map[uint32]int`, `pendingFreeSet map[uint32]bool`. `MountRW`
  initializes the maps; read-only mount leaves them nil and Pin/
  Unpin no-op on `!fs.writable`.
- `shared/fs/ext2/pin.go` (new) — `PinInode`, `UnpinInode`,
  `isInodePinnedLocked`, `markPendingFreeLocked`, `reclaimInode`.
  `UnpinInode` runs deferred reclaim (`freeInodeBlocks` + zero
  inode + `freeInode`) only when refs reach 0 AND inum is in
  `pendingFreeSet`.
- `shared/fs/ext2/writer.go` — `Remove`, `WriteFile`-overwrite,
  `Rename`-target-overwrite all reordered: `removeDirEntry` first,
  then check `isInodePinnedLocked`; if pinned, `markPendingFree
  Locked` and skip. Immediate-free behavior kept verbatim per path
  (Remove zeroes the inode, Rename does not — matches the existing
  inconsistency rather than introducing a behavior change).
- `maz/fs/fsipc.go` — `ipcOpen` calls `fsys.PinInode(h.inum)`
  after `conn.allocHandle` succeeds, both Create branch and
  regular open. `ipcClose` widened to `(conn, req, resp, mt
  *mountTable)` and calls `mt.getFS(h.kind).UnpinInode(h.inum)`
  before `freeHandle`. `freeHandle` audited — only one caller.

Build: `$GO tool task fs:arm64`, `fs:x86_64`, default arm64
`task` all clean.

**180s ARM64 HVF run:** boot reaches mail; fti indexes 14 docs
cleanly; then:

```
[fti] SCORCH ASYNC ERROR: source: merger, persist error:
merging err: open /tmp/fti-30/bleve/store/00000000001d.zap:
no such file or directory
```

Body fetch via badger served one click successfully (`[mail]
body: 138219 bytes variant=1`). Subsequent clicks froze the UI —
fti's `corrupted` flag prevents further Index calls and mail-ui
waits on a body path that can't reply.

**Open question:** ENOENT-on-zap-reopen recurred *with* the fix
in place. Pin/Unpin/defer-free are silent — we have no signal
whether the deferred-reclaim path fired even once. Two
possibilities, no way to distinguish without instrumentation:

1. Defer fires for `.zap` files, but ENOENT comes from a path
   the fix doesn't cover (real bleve unlink, or fsync ordering
   in the merger persist path).
2. Defer never fires — handles are already closed by the time
   dirents are lost; the bug isn't unlink-while-open at all.

**Next step (planned, not started): instrumentation.** One-shot
prints in `PinInode` (refs 0→1), in `UnpinInode`'s deferred
branch, and in the pinned branch of Remove/WriteFile-overwrite/
Rename-overwrite. Re-run, then triage.

### FTI bleve persister panic — write/mmap coherence (CONFIRMED FIXED 2026-04-22)

Bleve scorch's `persisterLoop` goroutine has a `defer recover()`. When it panics, it calls
`fireAsyncError(ErrAsyncPanic)`, which sets `h.corrupted = true` in FTI's index handler.
All subsequent `Index()` calls then return `IndexError("bleve index corrupted after internal panic")`.

**Root cause:** `sysWrite` in `maz/linux/syscalls.go` buffers sequential writes in
`fdEntry.writeBuf` without writing to ext2 immediately. Bleve writes `.zap` segment data
via `write()`, then mmaps the same fd. The mmap page fault invokes `sysMmapPageFill`, which
read from ext2 — which had zeros because the write buffer had never been flushed. Bleve
reads back zeros, dereferences a nil pointer, and the persister panics.

**What the stale-data theory got wrong:** `/tmp` is on the ramdisk which resets on each
QEMU boot, so `os.RemoveAll(blevePath)` at FTI startup succeeds — no stale bleve state.
Making ext2 writable would not have helped.

**Mitigation in place:** `maz/maildb/mbox_import.go` `waitForOne` deduplicates error
notifications — the MailDB window shows the first error and then only every 50th repeat.

**Root fix (2026-04-21, confirmed 2026-04-22):** `sysMmapPageFill` in `maz/linux/syscalls.go`
now calls `h.flushWriteBuf(callerPID, fd, e)` immediately after the nil check on `e`, before
zeroing the page buffer and reading from ext2. Confirmed in ARM64 HVF 120s run: all mmap
coherence tests pass; 100/100 docs indexed without persister panic or `corrupted` flag.

### Maildb mmap coherence test failure (CONFIRMED FIXED 2026-04-22)

`maildb` runs a startup coherence test at launch. Test 1 writes data via `syscall.Write`
(sequential, buffered), mmaps the same fd, then reads back — expecting to see the written
data. On ARM64 HVF the read-back returns all zeros:

```
[mmaptest] FAIL: mmap read-back: expected 'A' got 00 00
[maildb] WARNING: mmap coherence test FAILED
```

Maildb continues using direct-I/O (no mmap for badger data pages). No functional crash
has been observed — badger falls back cleanly. But this indicated mmap page faults were
not seeing data written via `write()`.

**Root cause:** `sysWrite` buffers sequential writes in `fdEntry.writeBuf` without
flushing to ext2 immediately. A subsequent mmap page fault calls `sysMmapPageFill`, which
read directly from ext2 — finding zeros because the write buffer had never been flushed.

**Note:** `sysPwrite64` already writes through ext2 directly, so the pwrite→mmap path was
already coherent. The failing test used `syscall.Write` (buffered sequential path), not
`syscall.Pwrite`.

**Root fix (2026-04-21, confirmed 2026-04-22):** `sysMmapPageFill` in `maz/linux/syscalls.go`
now flushes `e.writeBuf` via `h.flushWriteBuf` before reading from ext2. Confirmed: ARM64
HVF 120s run shows `[mmaptest] === ALL TESTS PASSED ===` with no `WARNING: mmap coherence
test FAILED` line.

---

### SyscallUringSend cross-page fix (2026-04-21)

The kernel rejected `SyscallUringSend` calls where the 128-byte `UringIPCMsg` buffer
spans a page boundary (`(msgPtr & 0xFFF) + 128 > 4096`), returning EINVAL. On x86_64 the
mail app's stack layout places the `msg` variable at a page offset that triggers this.

Fix in `kmazarin/ksyscall/uring_ipc.go`: when the fast-path check fails, a slow path
copies both partial pages into a 128-byte kernel stack buffer, then uses that buffer as
the message source. No resources are allocated — `MapPAToKernelScratch` is arithmetic,
and the local buffer lives on the kernel goroutine's stack for the duration of the call.

**Why two `WalkUserPageTable` calls are required even though the source is a stack:**
A goroutine stack is contiguous in virtual address space, so the two pages involved are
always adjacent VAs. However, virtual contiguity does not imply physical contiguity — the
buddy allocator hands out independent frames for each page, so the two physical addresses
can be anywhere in RAM. Each must be resolved separately via a full L0→L3 page table walk.
The `HandleUserPageFault` fallback on the second page is a correctness guard; in practice
both pages are demand-paged in before the shepherd reaches the syscall.

---

## MailRow Interactor Design

- `MailRow` wraps `ColumnPercentage` (consistent with other grid rows)
- State machine: **Pending → Loading → Loaded | CollectionExpired | Error**
- On construction: fire KeyHeaders(collId, msgNum, msgNum); transition to Loading
- On response: unpack KeyHeaderEntry[0] from transferred page, free pages, → Loaded
- On ErrCollectionExpired: invoke `onCollectionExpired(collId)` callback on parent;
  parent re-creates collection and rebuilds all rows
- On CollectionRemove notification: parent removes row from GridTable, renumbers rows
- On CollectionAdd notification: parent inserts new MailRow at correct position
- Implements `std.GridRow` (Sender/Subject/Date); returns "…" placeholder while Loading
- Click → fire `onRowSelected(collId, msgNum)` event

---

## Key File Paths

| What | Path |
|------|------|
| New protocol package | `shared/mailproto/` (to create) |
| Maildb main | `maz/maildb/main.go` |
| Maildb handler (old, to gut) | `maz/maildb/mail_handler.go` |
| Maildb collection store | `maz/maildb/collection.go` (to create) |
| Maildb message store | `maz/maildb/msgstore.go` (to create) |
| Mail app main | `mazarin/apps/mail/main.go` |
| New mail row interactor | `mazarin/apps/mail/mail_row.go` (to create) |
| Page alloc (userspace) | `mazarin/sys/sharedmem.go`, `mazarin/sys/mem/` |
| Grid table | `mazarin/mancini/std/grid_table.go` |
| Badger flags persistence | new keys in `maz/maildb/mail_handler.go` |

---

## mazdl / mazlink Architecture

### Four-piece design
| Piece | Location | Responsibility |
|---|---|---|
| **mazlink** | `mazlink-patches/cmd/link/` | Emit plugin-shape ELF: UNDEF dynsym for host imports, PLT/JUMP_SLOT for function imports, GLOB_DAT for data imports, strip unreferenced host code, NOP host `init.N` entries. Also: emit host's export dynsym when linking the host binary. |
| **mazdl** | `mazarin/mazdl/` | Real dlopen: mmap segments via kernel primitive, apply `R_*_RELATIVE`, resolve UNDEF symbols against global module table, patch GOT/PLT, run `DT_INIT_ARRAY`, return handle. `dlsym` walks export table. |
| **Kernel** | `kmazarin/ksyscall/` | Single new primitive `SysMapELFSegment(fd, offset, len, vaddr, perms)` — mmap + W^X enforcement. No ELF parsing, no relocations, no symbol names. |
| **maz-reloc** | `cmd/maz-reloc/` | Retires on arm64/amd64 once Phase 5 lands. Stays alive for riscv64 until Phase 7. |

Rule of thumb: if the work requires understanding ELF structure, it lives in `mazdl`,
not the kernel. The kernel only touches page tables.

### Plugin ELF shape (ET_DYN)
- `.dynsym`: UNDEF entries for host-imported symbols; DEFINED for exports
- `.rela.dyn`: `R_*_RELATIVE` for internal pointers; `R_*_GLOB_DAT` for data imports
- `.rela.plt` + `.plt` + `.got.plt`: function imports via JUMP_SLOT — eager binding
- `.dynamic`: `DT_NEEDED="mazarin-host"`, `DT_JMPREL`, `DT_PLTREL=DT_RELA`, etc.
- `.init_array`: `_mazdl_register_moduledata` wrapper first, then user inits
- No `R_*_COPY` — single authoritative copy of every host datum
- No lazy .plt resolver — `mazdl.Open` fills every slot before returning

### Host policy (Phase-2 starting set)
Packages whose code is stripped from plugins and resolved against the host at load time:
```
runtime
internal/runtime/...   (atomic, gc, maps, math, sys, syscall, exithook, ...)
internal/abi
internal/cpu
internal/bytealg
internal/goarch
internal/goos
internal/goexperiment
```
`internal/runtime/...` kept wholesale: atomic CAS primitives must be identical on
shared memory. Ambiguous packages (`sync`, `reflect`, `os`, `time`) deferred to Phase 6
— add only when a specific bug forces it.

### Funcval dead-reloc bug (Option A fix — 2026-04-18)
Go emits an 8-byte `.data.rel.ro` funcval object for every function taken as a value
(name ends in `·f`). When mazlink strips a host-policy package's `.text`, the funcval
stays but its `R_*_RELATIVE` addend points into the zero-padding gap between stripped
functions and `runtime.etext`. Any indirect call through the funcval (e.g. map hasher)
branches into padding → `udf #0` → SIGILL.

**Fix:** In `adddynrel`'s `R_ADDR` case, when target is `SDYNIMPORT` AND
`DynimpLib=="mazarin-host"`, emit `R_AARCH64_GLOB_DAT` / `R_X86_64_GLOB_DAT` against
the target's dynsym entry. The dynamic loader writes the host's real address at load
time. The `DynimpLib=="mazarin-host"` gate is load-bearing — prevents accidentally
promoting unrelated SDYNIMPORT entries.
Files: `mazlink-patches/cmd/link/internal/arm64/asm.go`,
       `mazlink-patches/cmd/link/internal/amd64/asm.go`.

### Name-mangling parity
`BuildModePlugin` triggers `ld.mangleTypeSym` which hashes long `type:.*` symbols
to 6-byte base64 tags as dynsym `extname` (e.g. `type:.C9kB2TSL`). Stock exe mode
skips this hashing, so host and plugin would have mismatched dynsym names → "unresolved
symbol" at load time. Mazlink patches `mangleTypeSym` to also run when
`-dlopen-host-exports` is set so names agree.

### Phase 5: x86_64 end-to-end (CURRENT WORK — 2026-04-21)

All production plugins (rachel, helloworld, prefs, fontsvc, keymapper, linux,
maildb, mail-ui, fti, clocks, etc.) are **already** built with the new mazlink
plugin-shape format — every `maz/*/Taskfile.yml` uses
`-buildmode=plugin -dlopen-host-packages`. The shepherd uses `-dlopen-host-exports`.
`mazhost/load.go` already calls `mazdl.RegisterHost()` + `mazdl.OpenBytes`.
The ARM64 HVF system is stable and fully exercising this path.

**What remains:** The x86_64 system has not been tested with a disk image.
CLAUDE.md notes "VirtIO PCI block driver needs testing with disk" for x86_64.
Without a working block driver the shepherd never gets a filesystem, never loads
plugins, and the x86_64 system never runs any userspace.

**Phase 5 work:** Build `disk-x86_64`, run `run-x86_64`, read the serial log,
identify and fix whatever breaks in the VirtIO block / fs / shepherd / plugin
pipeline on x86_64.

### Retirements when Phase 5 lands (arm64/amd64)
| Mechanism | Why it goes away |
|---|---|
| `maz-reloc` thin-stub trampolines | Plugin no longer has its own `morestack` — `.plt` goes directly to host |
| `maz-reloc` `.maz_imports`/`.maz_import_strtab` | Standard `.dynsym`/`.rela.plt` replace them |
| `syncMazWriteBarriers` | Plugin's `runtime.writeBarrier` is gone; GOT slot points at host's flag |
| `preGrowStack` | No plugin `morestack`; host handler runs correctly |
| Kernel symbol-name hunt for `MazarinMain` | Replaced by `mazdl.Sym` |
| `RegisterMazModuledata` host helper | `_mazdl_register_moduledata` init-array entry runs from inside the plugin |

---

## Delegate IPC Latency Measurements (ARM64 HVF, 2026-04-21)

Kernel-side RTT measured in `SyscallReply` via `ktimer.ReadCounter()` at 62.5 MHz
(ARM64 CNTFRQ_EL0). RTT = elapsed ticks × 1,000,000 / frequency, in µs.
Logged via `klog.Criticalf` (direct UART; survives soft-IRQ ring saturation).

### Write (sysid=10) — small stdio/bleve flushes

Typical warm-path small writes:
- First few boot writes: ~255µs (cold path, page faults, initial setup)
- Steady-state writes (1–4 KB payloads): **10–50µs**

### Pwrite64 (sysid=66) — bleve scorch journal page flushes

Bleve scorch writes 4 KB journal pages during indexing:
- Typical: **64–290µs**
- Occasional GC pause spikes: entry #449 = 2590µs, entry #641 = **8392µs**

### Interpretation

The 10–50µs warm Write RTT is the full round-trip:
  fti → DelegateSyscall (kernel) → linux shepherd uring ring → fs shepherd ext2 → reply

This is within expected range for a two-hop IPC path with shared-memory rings.
The GC-induced 8.4ms outlier corresponds to a GC STW pause in fti or linux shepherd
stopping the reply processing; not a kernel or IPC pathology.

### klog.Logf vs klog.Criticalf for delegate diagnostics

`klog.Logf` routes through the linux shepherd's soft-IRQ uring ring. During the initial
bleve write burst (~580 Pwrite64 calls in rapid succession) this ring fills and messages
are silently dropped. `klog.Criticalf` writes directly to UART via the kernel serial
driver, bypassing the ring — guaranteed delivery even under full ring saturation.
Any delegate timing instrumentation MUST use `klog.Criticalf`.

---

## fti: bleve sync write performance (2026-04-21)

### Current configuration (unsafe_batch removed)

```go
index, err := bleve.NewUsing(blevePath, mapping, "scorch", "scorch", map[string]interface{}{
    "asyncErrorCallbackName": "log",
})
```

`unsafe_batch: true` was present in earlier builds and has been **removed** so that
bleve waits for each segment flush to reach disk before returning from `Index()`.

### ARM64 HVF: sync write RTT (60s run, 100 emails, 2026-04-21)

Delegate write RTT for sysid=66 (4096-byte pwrite to ext2/BadgerDB), n=25:

| Metric | unsafe_batch (old) | sync (current) |
|--------|-------------------|----------------|
| count | 7 | 25 |
| min | 50µs | **30µs** |
| median | 175µs | **39µs** |
| avg | 979µs | **425µs** |
| max | 5347µs | 6254µs |

Removing unsafe_batch increased write frequency 3.6× (scorch flushes smaller segments
more often) but dramatically lowered individual write latency — median 39µs vs 175µs.
Throughput cost: 2.98 MB/s → **0.70 MB/s** for 100-message corpus (expected for durable writes).

### x86_64 TCG: why sync writes take 15–30s per document

On x86_64 TCG the same bleve sync write path takes 15–30 seconds per document.
This is **not a kernel bug** — it is a compounding of two TCG-specific factors:

**Factor 1 — low scheduler throughput:**
x86_64 TCG context-switch rate is 8.5/sec vs 265/sec on ARM64 HVF (31× lower).
Each context switch costs ~118ms in wall time.

**Factor 2 — per-document write count:**
A ~41KB bleve segment = ~82 × 512-byte sector writes. Each `pwrite()` is a delegated
syscall requiring 2 context switches (yield to handler, yield back).

**Combined:** 82 writes × 2 ctx_switches × 118ms ≈ **19s per document**. This matches
the observed 15–30s.

The ARM64 HVF median write RTT is 39µs; on x86_64 TCG the equivalent is ~236ms — a
6000× difference, far beyond the raw 10–50× CPU emulation factor. The multiplier comes
entirely from scheduler starvation: TCG executes so slowly that the timer interrupt
(203Hz) barely dents the CPU monopolisation by the active thread.

**Implication:** x86_64 TCG + bleve sync writes is unusably slow. This is acceptable
— x86_64 TCG is a development/debugging target only; production use is ARM64 HVF
(fast) and eventually bare-metal x86_64 (faster than HVF). No fix is needed.

### x86_64 TCG: file loading time inconsistency

File loading times on x86_64 TCG are wildly inconsistent:

| File | Size | Time | Throughput | µs/page |
|------|------|------|------------|---------|
| shepherd.elf | 6.8MB | 113ms | 60 MB/s | 68µs |
| fontsvc.maz | 6.2MB | 46ms | 135 MB/s | 30µs |
| keymapper.maz | 3.5MB | 1137ms | 3.0 MB/s | 1349µs |
| fti.elf | 19.7MB | 15970ms | 1.2 MB/s | 3314µs |

Root cause: **ext2 file fragmentation**. Contiguous files are read with 4096-byte
multi-sector VirtIO requests (1 round-trip per page, ~30–68µs). Fragmented files
require one 512-byte single-sector request per ext2 block (8 round-trips per 4KB
page × ~170µs each ≈ 1360µs/page). fti.elf requires ~19 requests per page on average.
The disk image layout determines which files are fragmented; this varies by build order.

---

## CFF Write-Barrier Investigation (paused 2026-04-17)

### Symptom
fontsvc.maz crashes during CFF glyph rendering in `go-text/typesetting` after loading
the Italic font (Regular succeeds). Two modes, non-deterministic:
- `SIGSEGV addr=0x70000000000000` at `(*CharstringReader).ensureClosePath`
  (the `append(out.Segments, ...)` inside)
- `panic: runtime error: growslice: len out of range` — cap field is garbage
  before growslice is entered

Always happens after one full GC cycle (2 writeBarrier transitions).

### Confirmed NOT the bug
- Library is correct on stock Go 1.26.2 (standalone test renders 362 glyphs each arch, 0 panics)
- `RegisterMazWriteBarrier` IS called; `syncMazWriteBarriers` IS firing at STW exit
- Compiled fontsvc.maz code reads the correct `runtime.writeBarrier` VA
- Body trampolines (`morestack.abi0`, `wbBufFlush.abi0`) are patched correctly
- Go P-struct wbBuf offsets match between host and .maz (both Go 1.26.2)

### Still suspicious
1. Timing gap: `setGCPhase(_GCmark)` flips `writeBarrier.enabled=true` during STW;
   `syncMazWriteBarriers` runs in `startTheWorldWithSema` — on paper fine, but
   not runtime-verified.
2. `[]ot.Segment` GC bitmap after `buildCompleteTypemap` type-redirect — if the
   redirected `*_type` has wrong `Size_` or GC bitmap, growslice computes wrong cap.
3. Race between growslice return and slice-header store (cap written before array
   pointer at `a5350`/`a5360`) — if write barriers don't fire, GC could miss new array.

### Architecture context
- fontsvc.maz is loaded by **rachel** (rachel is the HOST, not kmazarin)
- rachel uses the userspace overlay at `mazarin/overlay/userspace/runtime/`
- fontsvc.maz uses the thin-overlay at `build/shepherd-overlay/runtime/`
- Instrumentation still in `mazarin/overlay/userspace/runtime/maz_moduledata.go`

### Next diagnostic steps (if resuming before mazdl Phase 5)
1. Force `runtime.GC()` before every glyph render in fontsvc to isolate GC-correlation
2. Add growslice instrumentation in the userspace overlay at growslice entry
3. Verify `[]ot.Segment` type descriptor after typelinksinit + buildCompleteTypemap
   (adrp x4, 0x21c000 + #0x880 = 0x21c880 in fontsvc binary)

**Revert before next boot:** `config/kernel.arm64.toml` `go_mem_limit=256` → `24`


---

## Linux/fs delegate consistency investigation (2026-04-24)

### Architecture (confirmed)
- **Linux shepherd** (`maz/linux`) handles all path-based syscalls (`openat`, `getdents64`,
  `read`, `write`, `renameat`, `fstat`, `unlinkat`, etc.). It maintains a per-shepherd
  FD table and a write-buffer cache per FD.
- **Linux delegates** all actual filesystem operations to **fs.maz** (`maz/fs`) over a
  uring IPC channel (`ProtoFSIPCReq` / `ProtoFSIPCResp`). The `fsclient.Client` in
  `mazarin/fsclient` wraps the IPC.
- **fs.maz** runs a single-goroutine select loop (`maz/fs/main.go:223`) over both the
  delegate channel and the IPC channel — so all ext2 access is **serial**. Comment at
  `main.go:220-221` confirms: *"Both are processed in the main goroutine to avoid
  concurrent filesystem access (ext2 is not thread-safe)."*
- The on-disk filesystem implementation is `shared/fs/ext2/`. Two mounts: read-only root
  (block device) and read-write `/tmp` (in-memory ramdisk via MemBlockDevice).

### Bug A — directory enumeration loses entries  ✅ ROOT CAUSE FOUND

**Location**: `maz/linux/syscalls.go:548-584`, function `sysGetdents64`.

The IPC ReadDir call uses pagination: `fs.ReadDir(handle, startIdx)` returns
`(dataLen, entryCount, err)` where `entryCount` is how many dirents fs.maz packed
into its shared data area (size **65536 bytes**, capacity ~1500 dirents). Linux
then copies the response into the *caller's* user buffer:

```go
dataLen, entryCount, err := h.fs.ReadDir(e.handle, int(e.offset))
n := dataLen
if n > len(buf) {
    n = len(buf)        // truncate to user buf (typically 4096 bytes)
}
copy(buf[:n], h.fs.DataSlice(n))
e.offset += int64(entryCount)  // BUG: advances by what fs marshalled, not what was delivered
```

If fs.maz marshalled 1500 entries (~65KB) but the user buffer is only 4KB and
holds ~80 dirents, the offset jumps to 1500. The next `getdents64` call asks
fs.maz for entries from index 1500+, **silently skipping the ~1420 dirents in
the middle that were marshalled but never delivered to userspace.**

This explains exactly the observed behavior: walked 209/317 emlx files, with
the largest "Messages" directory (231 entries) the worst affected.

**Fix**: advance `e.offset` by the number of dirent records that actually fit
in the truncated copy, not by `entryCount`. Easiest implementation is to walk
the dirent records in the buffer up to `n` bytes and count them. Alternatively,
have fs.maz pack only as many dirents as fit in `min(fsDataLen, requestedSize)`
where the user passes their buf size in the request payload.

### Bug B — write/open ENOENT on freshly-created file  ⚠️ NEEDS MORE DATA

**Symptoms**:
- `[fti] SCORCH ASYNC ERROR: source: persister, persist error: ... open .../000000000016.zap: no such file or directory`
- `[fti] SCORCH ASYNC ERROR: source: merger, persist error: merging err: open .../000000000067.zap: no such file or directory`

**What we ruled out**:
- ❌ Concurrency: fs.maz is strictly single-goroutine.
- ❌ ext2 directory-block enumeration: `LookupDir → ReadDir` walks all data blocks
  of the dir inode (`shared/fs/ext2/reader.go:285-339`). So path lookup should
  succeed even for huge directories.
- ❌ Long-lived inode/dirent cache: `shared/fs/ext2/reader.go` has only a
  per-call indirect-block-pointer cache.

**Suspect areas**:
- linux's per-FD write buffer (`syscalls.go:362,873-912`) defers IPC writes
  until close/fsync. Close DOES flush (`sysClose:157`), but the flush is
  *best-effort: ignore error on close* — a silent drop here would leave the
  file empty/missing on disk.
- ext2 `Rename` (`writer.go:631`) does add-then-remove, not atomic; if the
  add succeeds and remove fails, file has both names. If a fault aborts mid-
  rename, file may have neither name. SCORCH uses tmpfile+rename for atomic
  publish, so a partial rename matters.
- ext2 `addDirEntry` (`writer.go:248`) walks directory blocks to find slack;
  writer.go uses linear allocation. Possible bug: a write after delete could
  reuse a slot in a way that confuses subsequent lookups. Needs trace.

**Next step**: instrument fs.maz's `ipcOpen`, `ipcWrite`, `ipcRename`, and
`ipcReadDir` with `klog.Criticalf` traces showing path + result + (for opens)
whether O_CREAT was set + (for renames) old/new paths + (for dir ops) which
inode + the entry count returned. Reproduce the bleve ENOENT and inspect the
sequence of fs operations leading up to it.


---

## Bug A — empirical validation (2026-04-24, 60s ARM64 HVF run)

After applying the `deliveredDirents` fix in `sysGetdents64`:

| Metric                      | Before (broken) | After (fix)  |
|-----------------------------|-----------------|--------------|
| `emlxImport: walked` count  | 209             | **309**      |
| messages parsed             | 204             | 306          |
| `emlxImport: skip`          | 5               | 3            |
| `emlxImport: walk error`    | 3               | 8            |
| FTI deadlock                | yes             | no           |
| `cleaning up dead shepherd` | yes             | no           |
| FTI documents indexed       | ~5 (then died)  | 306          |

The +100 newly-visible files match the missing-from-readdir hypothesis exactly
(the 231-entry `Messages/` dir was the worst victim and now mostly works).
The persistent 11 file failures (8 walk + 3 skip) are NOT directory truncation
— they're the same dirent-listing-vs-lookup gap that powers Bug B.

`[linux] getdents64: fs marshalled X entries (Y B), user buf Z B held W entries`
fired 83 times — every truncation case where the new code path correctly
preserved the un-delivered entries for the next call.

---

## Bug B — refined diagnosis (2026-04-24)

### Trace data (after Bug B instrumentation in `maz/fs/fsipc.go`)
- 579 successful `OPEN ok`
- 362 successful `OPEN+CREAT ok`
- 5 `OPEN FAIL`:
  - `/tmp/maildb-16/badger/MANIFEST` (`ext2: not found`) — **benign** (badger
    probes-then-creates).
  - `/tmp/maildb-16/badger/KEYREGISTRY` — **benign** (same pattern).
  - `/tmp/fti-15/blev` — **benign** (typo'd path bleve probes for).
  - `/tmp/fti-15/bleve/store/0000000000a2.zap` — **REAL Bug B**.
  - `/tmp/fti-15/bleve/store/0000000000b4.zap` — **REAL Bug B**.
- 2 `RENAME ok`, 327 `REMOVE ok`, 0 `*FAIL` of either.
- 86,804 successful `WRITE` (untraced after fix #2 to avoid UART backpressure).
- 3 `SCORCH ASYNC ERROR` lines mention exactly the same two `.zap` files.

### What this rules in / out
- ✅ Files ARE being created successfully (we see the OPEN+CREAT ok earlier in
  the log for the failing files — need to grep the next run to capture both
  sides).
- ✅ The failure happens minutes after creation, by which time many neighboring
  files in `bleve/store/` have been REMOVE'd (327 successful removes).
- ❌ NOT a bleve-specific bug — emlx walker on `/data` shows the same shape
  (filepath.Walk lists files that subsequent `os.Open` can't find).
- ❌ NOT concurrency (fs.maz strictly single-goroutine) and NOT ext2 dir-block
  enumeration (LookupDir walks all blocks).

### New leading hypothesis
The `327 REMOVE ok` count is large (~1.6× the failing dir's max population at
any one time). Bleve's segment merger creates a new merged segment, opens
several old segments to read from, then REMOVES the old ones. If `removeDirEntry`
in `shared/fs/ext2/writer.go` mutates dirent `reclen` slack in a way that
later invalidates a sibling entry's offset (e.g., by extending the previous
record's reclen to swallow a neighbor that's still being looked up by name),
that would produce exactly this pattern.

### Concrete next step
Re-run, grep the trace for the 2 failing `.zap` files. Capture:
1. The OPEN+CREAT ok line (proves it was created and what handle/inum).
2. Every subsequent OPEN/REMOVE/RENAME of any file in the same `bleve/store/`
   directory between create and the eventual OPEN FAIL.
3. Compare against `removeDirEntry` (`shared/fs/ext2/writer.go`) — look for
   the slack-coalesce logic that might consume a sibling.

### Bug A fix code reference
`maz/linux/syscalls.go::sysGetdents64` and new helper `deliveredDirents`.

---

## Pin/Unpin hypothesis disproven (2026-04-25)

The unlink-while-open hypothesis (task #8 in task_plan.md) was tested
with a three-hook diagnostic in `shared/fs/ext2/{pin.go,writer.go}` and
`maz/fs/main.go`:

| Hook                       | Fires when                                                   | Count in 180s run |
|----------------------------|--------------------------------------------------------------|-------------------|
| `[ext2:pin]`               | `PinInode` 0→1 (every regular-file open)                     | 276               |
| `[ext2:unpin defer-reclaim]` | `UnpinInode` releases an inode marked pendingFreeSet         | **0**             |
| `[ext2:defer-free]`        | Remove / WriteFile-overwrite / Rename-overwrite hits pinned inum | **0**             |

The 180s ARM64 HVF run *did* reproduce SCORCH ENOENT
(`error opening new segment at /tmp/fti-17/bleve/store/00000000002b.zap`)
yet defer-free fired zero times. By the time bleve unlinks/replaces a
file, no fs.maz handle is still holding that inum. Pin/Unpin can't help
because there's nothing to defer.

**Adjacent signal in the same run** (worth investigating separately):
- One user click correlated with a 13.7-second `Index()` call on the
  next document (`fti.go indexed ... 13.7s`).
- A 20-second gap in `[status]` log lines (uptime 92s → 112s) where
  the kernel emitted *no* status snapshots at all.
- Later isolated `Index()` of a 1069-byte doc took **41.5 seconds**.
- Suggests scheduling/IPC starvation around heavy IPC load (click
  handling vs persister/merger goroutines), not a filesystem-data bug.

### Hypotheses to consider for the actual root cause

1. **Linux shepherd write-buffer drops bytes silently on close.** The
   PersistSegmentBase path (`zapx/v16/build.go:34`) does
   `OpenFile(O_RDWR|O_CREATE)` → `WriteTo` → `Sync` → `Close` and
   returns nil. If linux's `flushWriteBuf` best-effort discards a
   final write batch on close, the file would still appear created
   (size 0 or short) but the persister's reopen would still succeed —
   so this would manifest as "file present but empty/short", not
   ENOENT. **Unlikely** unless the create path itself can fail
   silently.

2. **Path-resolution divergence.** Some part of linux's openat path
   resolves `/tmp/fti-17/bleve/store/00000000002b.zap` differently
   for create vs open (cwd drift, fdcwd flag mishandling).

3. **Same-PID scheduling stall during persister run.** Under click
   load, the persister goroutine's IPC for `OPEN` may time out and
   the syscall layer returns ENOENT spuriously (vs blocking). The
   13.7s/41.5s `Index()` stalls support that the system is
   pathologically scheduled during click handling.

### Code stripped 2026-04-25
Diagnostic hooks removed from `shared/fs/ext2/pin.go`,
`shared/fs/ext2/writer.go`, `maz/fs/main.go`. Pin/Unpin/pendingFreeSet
machinery itself retained — it's defensive and harmless on the cold
path (refs always 0 at unlink time in current usage).

---

## Three real bugs surfaced after silencing traces (2026-04-25 late)

A multi-iteration loop chasing "click → 70-second freeze" produced
several wrong diagnoses in sequence (path-resolution divergence,
delegated-syscall stall, scheduling starvation). The final iteration
silenced the high-volume `[fs:rpc]` and `[lin:fstatat]` traces and ran
clean. **The freeze symptom did not reproduce.** The user reported the
system was responsive: clicks worked, body fetch worked, inbox
rendered.

**Conclusion: the freeze was Heisenbug-induced by the diagnostic
traces themselves.** Per-RPC `fmt.Printf` from fs.maz (~570 lines/run)
combined with per-fstatat printf in linux saturated the UART
sufficiently to:
- starve rachel's input acquisition goroutine (no `[rachel:click]`
  events in the entire freeze window),
- block `klog.Criticalf`'s `[status]` periodic snapshot,
- cause `[DLG:W][DLG:W]` doubled-prefix output (concurrent UART
  writers stomping on each other),
- and create the appearance of a 70+ second deadlock that ultimately
  cleared once the trace burst subsided.

The kernel-side instrumentation we added during this loop
(`Thread.DelegateBlockSinceTick` / `DelegateBlockSysID`,
`RecordDelegateBlock` linkname'd from ksyscall, `delegate stuck:` line
in `printEpochStatus`, `sys.DumpKernelStatus` callable from .maz) is
**kept**. It costs nothing when no thread is stuck and the data it
produced under load was real and load-bearing for the (mistaken)
delegate-stall hypothesis. Useful for future diagnoses.

### Three bugs visible against the stable baseline

**1. Diversion #6 EISDIR — bleve scorch persister.**

```
[fti] SCORCH ASYNC ERROR: source: persister, persist error:
  segment: /tmp/fti-4/bleve/store/000000000060.zap persist err:
  write /tmp/fti-4/bleve/store/000000000060.zap: is a directory
```

286 SCORCH errors in a single 90s run. This is the original
Diversion #6 task #7 we'd been after before the unlink-while-open
detour. The `[fs:read EISDIR]` diagnostic in fs.maz fired with:

```
[fs:read EISDIR] handle=228 path="/tmp/fti-4/bleve" inum=12 ftype=2
                 isDir=true openSize=4096 offset=0 count=4096
```

Path resolution lands on the bleve-store directory inode (inum=12)
— bleve `OpenFile(".../60.zap", O_RDWR|O_CREAT, 0600)` returns a
handle whose backing inode is a directory. `ipcOpen` doesn't enforce
Linux's `open(dir, O_RDWR) → EISDIR` rule, so the EISDIR surfaces only
when the caller's first `write` hits an "is a directory" error deep
in ext2. The follow-on Remove also fails (`got err removing file:
000000000008.zap, err: ...: is a directory`), confirming the dirent
points at a directory inode and not just a one-shot mistake.

Open question: how does `.../60.zap` end up resolving to inum=12?
Two suspects in `shared/fs/ext2/writer.go::Create`:
- `allocInode()` bitmap drift → handing out an inum that's still in
  use elsewhere.
- Upstream dirent for the `.zap` already exists pointing at the
  directory's inum before Create is even called.

**Just-landed mitigation (2026-04-25):** `maz/fs/fsipc.go::ipcOpen`
now rejects opens of directory inodes with `O_RDWR|O_WRONLY`,
returning EISDIR immediately and logging
`[fs:open EISDIR-on-write] path=... inum=... flags=... mode=...`.
This (a) matches Linux semantics, (b) surfaces the offending path at
open time instead of write time, (c) gives bleve the standard error
shape it already handles.

**Verification run (2026-04-25, 90s ARM64 HVF):**
- 0 `[fs:open EISDIR-on-write]` traces fired (no `.zap` open hit a
  directory inode this run).
- 255 SCORCH errors — but **all ENOENT**, not EISDIR:
  ```
  [fti] SCORCH ASYNC ERROR: source: merger, persist error:
    merging err: open /tmp/fti-20/bleve/store/000000000073.zap:
    no such file or directory
  ```
- The accompanying `[fs:open .zap RESOLVE FAIL]` trace agrees the
  file genuinely doesn't exist:
  `path="/tmp/fti-20/bleve/store/000000000108.zap" oCREAT=false err=ext2: not found`.
- Most failures are **merger** trying to reopen *existing* segments
  for compaction; some are **persister** trying to reopen
  just-created segments. Both report ENOENT.

**Conclusion:** the underlying bug is dirent ↔ inode bookkeeping,
and it manifests in two ways depending on timing:
- **EISDIR variant** (prior run): the `.zap` dirent points at the
  bleve-store *directory* inode (inum collision / bitmap drift).
- **ENOENT variant** (this run): the `.zap` dirent doesn't exist at
  all — bleve persisted a segment file but the dirent or inode is
  gone by the time merger tries to reopen.

Both reduce to "inode bookkeeping in `shared/fs/ext2/writer.go` is
losing track of which inums belong to which dirents." The EISDIR
enforcement is good Linux-semantics hygiene either way and stays.

**Open: dive into `shared/fs/ext2/writer.go::Create` and `freeInode`.**
The clearest hypothesis is that an inum is being freed (via Remove,
WriteFile-overwrite, or Rename-overwrite) while its dirent still
exists, OR the bitmap is being cleared but the on-disk inode
mode/links aren't. Pin/Unpin was supposed to defer this; we kept
the code but it's apparently not preventing the failure path.

### Bug #4 — mail-app memory blowup → OOM crash

**Discovered in this run.** Per-SID kernel memory dump from the OOM
fault:
```
SID=1c: 172d6 pages (5cb58 KB)   ≈ 371 MB
SID=0:  e8c8 pages (3a320 KB)    ≈ 232 MB
SID=2:  c589 pages (31624 KB)    ≈ 197 MB
SID=4:  c82a pages (320a8 KB)    ≈ 200 MB
SID=e:  f026 pages (3c098 KB)    ≈ 240 MB
...
fatal error: runtime: out of memory
```

SID=28 (mail-app, the only shepherd in that range — mail was started
last) holds 371 MB and the kernel's overall user-page total is over
1.5 GB. The system OOMs at uptime ≈ 26s.

This is **independent** of bugs #1, #2, #3. mail-app is leaking — most
likely body buffers from fetched messages, glyph caches, or rendered
backing store. Root cause not yet identified but it'll have its own
investigation. Once memory pressure builds, file ops can fail in
new and confusing ways (allocation failures during ext2 metadata
writes), which may explain why the SCORCH variant changes between
runs.

### Summary — four bugs in scope

1. **Diversion #6 dirent/inode bookkeeping** (EISDIR/ENOENT variants)
   — `shared/fs/ext2/writer.go::{Create, allocInode, freeInode,
   removeDirEntry}`. The active bug.
2. **emlxImport stale dirent (Bug B)** — same root family as #1,
   visible on read-only `/data/` walks.
3. **linux-ui boot wedge** — `[fontsvc] uring.Send OpenFontReply
   failed`, intermittent. Improved error logging in place.
4. **mail-app memory blowup** — 371 MB resident at 26s, leads to
   OOM panic. New finding.

**2. `emlxImport` stale dirent (Bug B revisited).**

```
[maildb] emlxImport: walk error at /data/.../382874.emlx:
         lstat ...: no such file or directory
[maildb] emlxImport: skip /data/.../382978.emlx:
         open ...: no such file or directory
```

~6 walk errors per run, plus more "skip" errors. `filepath.Walk`'s
dirent enumeration lists files whose subsequent `lstat`/`open`
returns ENOENT. Same shape as the bleve-side ENOENT we saw early in
the session, but on the read-only `/data/...` mbox path. Confirms
the bug isn't a write-side lifecycle — it's directory enumeration
itself returning entries that don't actually exist (or that someone
unlinks during the walk, but `/data/` is read-only so that's
unlikely).

This was the bug Pin/Unpin was supposed to fix and didn't. Still
unresolved. May share root cause with bug #1 if both reduce to
"dirent → inum mapping is incorrect."

**3. `linux-ui` boot wedge (fontsvc uring.Send fail).**

```
[linuxapp] Bootstrap entered: Linux Console
[fontsvc] uring.Send OpenFontReply failed
... linux-ui never produces "backing store ready" or AppStart ...
```

Seen in 2 of the recent runs (intermittent). linux-ui calls
fontsvc to open its console font during Bootstrap. fontsvc tries to
reply via uring; `uring.Send` returns an error; fontsvc logs and
moves on; linux-ui blocks forever waiting for a reply that never
comes. No Linux Console window appears.

Improved diagnostic now in place
(`maz/fontsvc/main.go::handleOpenFont`):

```go
if err := uring.Send(senderSID, &encoded); err != nil {
    rawPuts("[fontsvc] uring.Send OpenFontReply FAILED: senderSID=")
    rawPutsInt(senderSID)
    rawPuts(" err=")
    rawPuts(err.Error())
    rawPuts("\n")
}
```

Next time it fires we'll see the actual uring error (EAGAIN? ESRCH?
buffer full?) and which sender. Independent of bugs #1 and #2.

### Why the trace reduction was decisive

The session's per-run line counts:

| Run shape                    | Total lines | Click freeze? | Kernel `[status]`? |
|------------------------------|-------------|--------------|--------------------|
| `[fs:rpc]` ENTER+REPLY all ops | ~1500       | yes          | 0                  |
| `[fs:rpc]` path-bearing ops    | ~1290       | yes          | 0                  |
| All fs:rpc + fstatat silenced  | ~990        | **no**       | 0 (other cause)    |

Removing the per-RPC traces dropped trace volume by ~35% and
eliminated the click-freeze entirely. The remaining `[status]`
silence has a separate cause (likely the kernel klog goroutine being
out-prioritized — not load-bearing for the EISDIR investigation).

Lesson: when chasing scheduling-shaped symptoms, instrument with
**counters and one-shot prints on failure**, not per-event ENTER/REPLY
pairs. The kernel-side `delegate stuck:` line is a good template:
it costs zero CPU/UART when nothing is wrong and produces a single
line per stuck thread when something is.

---

## Diversion #6 closure (2026-04-25 evening)

After the wrong-hypothesis chases (Pin/Unpin, path-resolution
divergence, scheduling stall) and the Heisenbug retraction, four
distinct root causes turned out to compound:

### Root cause 1: kernel string-copy truncation

`kmazarin/ksyscall/delegate.go::allocAndCopyCallerString` capped its
`CopyFromUser` at the end of the caller's first page:
```go
maxCopy := uintptr(4096) - (callerStrVA & 0xFFF)
...
if !kmem.CopyFromUser(dst, callerStrVA, int(maxCopy)) { ... }
for i := uintptr(0); i < maxCopy; i++ {
    if dst[i] == 0 { strLen = uint64(i); break }
}
if strLen == 0 && maxCopy > 0 && dst[0] != 0 {
    strLen = uint64(maxCopy)   // ← truncated, no null
}
```
When Go's allocator placed the C-string near a page boundary, the
returned string was a *prefix* of the real path. Bleve's
`os.OpenFile("/tmp/fti-N/bleve/store/XXXXXXXXXXXX.zap", ...)` arrived
at fs.maz as `"/tmp/fti-N/bleve"` (16 chars — exactly the page-end
prefix), which resolved to the bleve directory's inode and produced
EISDIR or ENOENT depending on race timing.

**Fix:** two-phase copy. After the first page, if no null was found,
follow into the next page (capped at the 4 KB scratch). If still no
null, return an error rather than fabricate a strLen.

### Root cause 2: fsclient.Client raced

No mutex anywhere on the IPC client. linux shepherd's many concurrent
delegate-handler goroutines all shared one `*fsclient.Client` and:
- two goroutines called `setPath` → both overwrote the shared data
  area, the later one's path won, but the earlier one's PathLen
  pointed past a stale null;
- `nextID++` was non-atomic;
- multiple `<-c.RespCh` waiters could grab each other's responses.

**Fix:** added `sync.Mutex` to `Client`. Every public method takes the
lock for its full IPC round-trip (path setup → send → receive →
data-area copy). `Read`/`Write`/`Stat`/`Fstat`/`ReadDir` were
refactored to take a `buf` parameter so the data-area copy happens
under the lock. The old `WriteData`/`DataSlice` methods were removed
— they were the racy seam between two API calls.

### Root cause 3: GC was effectively disabled

`config/kernel.{arm64,amd64}.toml` had `gc_percentage = 10000`
(GOGC=10000). The intent was to let `GOMEMLIMIT=256 MB` be the
governor instead of frequent GC. In practice, long-running shepherds
drifted to hundreds of MB heap without ever GCing, and the
**kernel-side user-page budget** ran out across all shepherds before
any individual shepherd hit GOMEMLIMIT. The OOM panic that killed
linux was a symptom, not the cause; the *system* ran out of physical
pages.

Per-shepherd memstats (added in the same session) were the
diagnostic: showed linux at 162 → 178 MB heap with `gc=0` over a 5s
window, while other shepherds (different alloc patterns) had GC'd a
few times.

**Fix:** dropped `gc_percentage` to `100` (Go default). Linux's
steady-state heap dropped from 178 MB → 5 MB. fti went 87 MB → 3 MB.
Mail-app ~10–15 MB. maildb ~140 MB (badger LSM working set, bounded;
GC fires when heap doubles from previous live set, which sits at the
limit of badger's working memory).

### Root cause 4: fontsvc dropped uring replies on EAGAIN

`mazarin/uring/syscall.go::SendWithRing` is non-blocking; the kernel
returns `-11 EAGAIN` when the target's ring is full. Fontsvc just
logged "failed" and moved on — linux-ui's font request blocked
forever waiting for a reply that was discarded. This was the
intermittent "linux-ui window never appears" bug.

**Fix:** `SendWithRing` now retries on EAGAIN with `runtime.Gosched()`
between attempts (256-attempt budget — closest equivalent to Linux's
"write to full pipe blocks until drain"). Applies system-wide so any
sender benefits, not just fontsvc.

### Side fixes that landed in the same session

- **mmap-survives-close (Linux semantics).** Page cache re-keyed from
  `(sid, fd, offset)` → `(sid, inum, offset)`. Cache now stores
  `Handle` per page; `sysClose` no longer drains it. Orphan fs handles
  whose owning fd has been closed are tracked in
  `syscallHandler.orphanHandles` and released on the eventual
  `sysMmapPageFlush` drain or shepherd death.
- **`ipcOpen` Linux-compat polish.** EMFILE rollback, O_EXCL→EEXIST,
  EISDIR-on-write enforcement.
- **O_CLOEXEC one-shot warning** — visible seam if exec is ever added.
- **Per-shepherd `runtime.MemStats` periodic logger** — load-bearing
  for the GC diagnosis above.
- **Kernel `delegate stuck:` line** + `Thread.DelegateBlockSinceTick`
  / `RecordDelegateBlock`. Zero cost when no thread is stuck.
- **`sys.DumpKernelStatus()`** — on-demand kernel `[status]` snapshot
  via `SysDebugPrint` marker `0xDB7`. Wired into fti's SLOW Index()
  and SCORCH paths.

### Verification — 180s run, 5 user clicks

- 5 `[rachel:click]` → 5 `[mail:click]` → 5 `[click-agent]` → 12
  body fetches (some chain prefetch).
- 0 SCORCH, 0 EISDIR, 0 OOM, 0 dead shepherd.
- Heap stable across the run.
- 1 `O_CLOEXEC` warning at boot (one-shot, per design).

### Notes for future investigations in this neighborhood

- The page cache is now inum-keyed and supports the close-survives-mmap
  semantic. If fd reuse ever causes issues, the `orphanHandles` map is
  the seam to investigate.
- The kernel string-copy fix is a 4 KB max path; if anyone ever passes
  a longer path, we now reject cleanly instead of truncating.
- The Pin/Unpin code in `shared/fs/ext2/{pin.go,writer.go}` is kept
  as defensive infrastructure even though we proved it didn't apply
  to this bug. It's zero-cost on the cold path and the mechanism
  itself is correct — it just wasn't the right hypothesis for #6.
- GOGC=100 + GOMEMLIMIT=256 is the new baseline. If memory pressure
  shows up later we can lower GOMEMLIMIT further (e.g., 128 MB) or
  drop GOGC to 50 for tighter compaction.
