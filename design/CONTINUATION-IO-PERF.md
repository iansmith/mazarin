# Continuation: Block I/O Performance Regression Under Load

## What We Did This Session

### 1. Block I/O 500x Speedup (COMMITTED — fa5e7d0)

We diagnosed that file loading (rachel.elf, fontsvc.maz, etc.) took ~6-10 seconds
per file because of the Go runtime's P-stealing (S1MO pattern). When the fs
shepherd's DMA worker blocked in MailboxRecv (a Syscall that releases the P),
sysmon would steal the P after 20μs and hand it to another M. When the IRQ fired
and the mailbox unblocked, exitsyscall couldn't reacquire a P, causing multi-ms
delays per I/O operation.

**Fix**: Userspace spin-poll of the shared-memory completion ring for 500μs before
falling back to blocking MailboxRecv. The completion ring is written directly by the
IRQ top-half, so userspace sees completions without any syscall — no P-release, no
sysmon steal. This brought file loads from seconds to milliseconds:

| File | Before | After |
|------|--------|-------|
| rachel.elf (4MB) | ~10,198ms | **19ms** |
| fontsvc.maz (3.4MB) | ~8,580ms | **14ms** |
| linux.elf (6.2MB) | - | **31ms** |

Code is in `flock/cmd/fs/main.go` — `doReadBlock()` and `doReadBatch()` methods
on `asyncBlockDev`.

### 2. .maz Overlay Build Fix (UNCOMMITTED — gen-overlay change)

Rachel was crashing with `checkdead: inconsistent counts` because .maz modules
(fontsvc.maz, prefs.maz) had full `runtime/proc.go` bodies instead of thin stubs.

**Root cause**: We added `"runtime/proc.go"` to the userspace overlay in
`cmd/gen-overlay/main.go` (line 268) for S1MO breadcrumb instrumentation. The
userspace overlay feeds into BOTH the shepherd overlay (correct — shepherds need
full runtime) AND the maz-overlay (WRONG — .maz modules must use thin stubs from
the thin-overlay). The maz-overlay merge (`gen-overlay -type merge -base thin-overlay
-extra userspace-overlay`) lets the extra override the base, so the thin-overlay's
stubbed proc.go got replaced with the full version.

**Fix**: Removed `"runtime/proc.go": "runtime/proc.go"` from `buildUserspaceOverlay`
in `cmd/gen-overlay/main.go`. The thin-overlay's stubbed proc.go now correctly flows
through to the maz-overlay. Rachel, fontsvc.maz, and prefs.maz all work.

**This change is staged but not yet committed.**

### 3. New Problem: Clocks.elf Load Takes 24 Seconds

After rachel and linux are running (boot sequence complete for the core shepherds),
the startup.toml-driven launch of clocks.elf shows:

```
[fs] PERF /clocks.elf: size=6656554 open=939ms alloc=0ms read=14608ms total=23999ms
```

Compare to linux.elf (similar size, loaded earlier when system is quieter):
```
[fs] PERF /linux.elf: size=6170504 open=104ms alloc=0ms read=31ms total=135ms
```

**14.6 seconds for read vs 31ms** — a 470x regression. Same code path
(`launchShepherd` → `readFileIntoPages` → ext2 ReadInto → asyncBlockDev batched
reads). The only difference is system load: when clocks.elf loads, rachel, linux,
fontsvc.maz, and prefs.maz are all running.

## Analysis: Why Does I/O Collapse Under Load?

### The Spin-Poll Window Is Too Fragile

The 500μs spin-poll works when the fs DMA worker goroutine gets uninterrupted CPU
time. But with multiple active shepherds (each in their own thread, preempted at
100ms quantum), the fs shepherd may:

1. Submit a BlockSubmit SVC
2. Start spinning on the completion ring
3. Get preempted by the timer (100ms quantum expired, or another thread is ready)
4. By the time fs gets CPU again, the 500μs window has long expired
5. Fall through to the `<-d.notifyCh` slow path
6. The slow path blocks in MailboxRecv (a Syscall), releases the P
7. Back to the original S1MO problem

The `open=939ms` confirms this — even ext2's Open (which does 2 single-block reads
for root dir + inode) takes nearly 1 second. Each of those reads goes through
`doReadBlock` → spin-poll (gets preempted, expires) → MailboxRecv slow path → P-steal
→ multi-hundred-ms delay.

### Evidence From Kernel Stats

The 80s run's `[E]` status lines show:
- `PW=11/el1h=0/svc=0/noctx=0/ok=11` — priority wake from IRQ worked 11 times
- `BLK=1221` block submissions but only `PW=11` priority wakes suggests most block
  completions arrive AFTER the non-timer IRQ return path (the fs thread isn't the
  one interrupted by the block IRQ — some other thread is)

This is the fundamental issue: the priority-wake-from-IRQ optimization (Change 1 in
the plan) only helps when the block IRQ interrupts a thread that can be preempted in
favor of the fs thread. If the IRQ interrupts the kernel idle loop or a thread that's
already the fs thread's M, it doesn't help.

### The Architectural Gap

We have two optimizations that each work in isolation:
1. **Spin-poll** — avoids syscalls entirely, but requires uninterrupted CPU time
2. **Priority wake from IRQ** — enables scheduling on non-timer IRQs, but only
   switches to the woken thread if the IRQ interrupted a *different* preemptible thread

Neither handles the common case under load: fs submits I/O, the block completion IRQ
fires 50-200μs later, but fs is already blocked in MailboxRecv (having exhausted its
spin window), and the priority wake doesn't reach it because the IRQ woke the mailbox
thread, not the fs user thread directly.

## Ideas for Fixing This

### Idea A: Longer / Adaptive Spin Window

Increase the spin-poll duration from 500μs to something longer (2-5ms), or make it
adaptive based on recent completion latency. Problem: burning CPU in a spin loop for
5ms on every I/O operation is wasteful when the system is loaded. And if the thread
gets preempted during the spin, the window length doesn't matter — you still fall
through to the slow path.

### Idea B: Yield-Free Blocking on Completion Ring (New Syscall)

Add a kernel SVC like `WaitCompletionRing(ring, timeout)` that:
- Checks the completion ring in kernel mode (no P-release)
- If empty, puts the thread to sleep on the ring's address (like futex_wait on a
  ring slot)
- The IRQ top-half, after writing to the ring, wakes the sleeping thread directly

This avoids the P-release problem entirely: the thread goes through RawSyscall (no
entersyscall/exitsyscall), sleeps in the kernel, and gets woken directly by the IRQ
handler. No mailbox, no channel, no P-stealing.

**This is essentially futex_wait on the completion ring's write index**, with the
IRQ top-half doing the futex_wake.

### Idea C: Make MailboxRecv a RawSyscall

If MailboxRecv used RawSyscall instead of Syscall, the Go runtime wouldn't release
the P. The M would hold its P while blocked in the kernel, and when the mailbox
send wakes it, the thread returns immediately with its P intact — no sysmon steal,
no exitsyscall slow path.

**Risk**: This violates Go's expectations. RawSyscall is meant for non-blocking
operations. A blocking RawSyscall holds a P hostage, preventing other goroutines
from running on it. With GOMAXPROCS=1 (shepherds), this means ALL goroutines in the
shepherd stall while the M sleeps in the kernel. But for the DMA worker goroutine,
this might be acceptable — it's the only goroutine that matters during I/O, and the
other goroutines (bootSequence, serve loop) are blocked waiting for I/O results
anyway.

**Ian has explicitly said**: keep Syscall/RawSyscall semantics correct and consistent.
Don't swap them to paper over bugs. So this needs careful thought about whether
MailboxRecv genuinely should be a RawSyscall (i.e., is it architecturally correct to
say "this operation is fast enough to hold a P"?).

### Idea D: Kernel-Side Completion Delivery (Skip Mailbox Entirely)

Instead of: IRQ → completion ring → mailbox send → MailboxRecv → channel → dmaWorker

Just: IRQ → completion ring → wake the thread directly

The IRQ top-half already writes to the completion ring. If the kernel also directly
woke the sleeping fs thread (via a registered "wake me when this ring advances"
mechanism), we skip the entire mailbox/channel notification chain. This is similar
to Idea B but from the kernel's perspective.

### Idea E: BlockSubmit + WaitCompletion as a Single Synchronous SVC

Combine BlockSubmit and completion wait into a single SVC:
`BlockReadSync(dev, lba, sectors, buf)` — submits the request AND sleeps until
completion, all within a single SVC. The thread never returns to userspace between
submit and completion, so there's no P-release, no scheduling gap. The IRQ top-half
wakes the thread directly.

This is essentially the original `blockReadInterrupt` approach from the kernel's
block driver, but exposed as a userspace SVC. It's the simplest model: synchronous
I/O from userspace's perspective, async DMA under the hood.

**Downside**: Loses the ability to pipeline — can't submit N blocks and wait for all
of them. Would need a batch variant: `BlockReadBatchSync(dev, lbas[], bufs[])`.

## Recommendation

The root cause is that the notification chain (IRQ → mailbox → channel → goroutine)
goes through Syscall, which releases the P. The spin-poll avoids this when CPU is
available, but fails under load.

**Idea B (WaitCompletionRing SVC)** seems cleanest:
- Keeps userspace in control of submit/wait pipelining
- Uses RawSyscall semantics naturally (the kernel sleep is waiting for a specific
  hardware event, not general-purpose blocking)
- The IRQ top-half already touches the ring; adding a futex-like wake there is
  minimal kernel change
- Doesn't change Syscall/RawSyscall semantics for existing SVCs

**Idea E (BlockReadBatchSync)** is simplest but least flexible.

## Files Involved

- `flock/cmd/fs/main.go` — asyncBlockDev, doReadBlock, doReadBatch (userspace I/O)
- `kmazarin/kmazarin/bottom_half.go` — IRQ top-half, completion ring write, mailbox send
- `kmazarin/kmazarin/mailbox.go` — mailboxSendKernel, mailboxSendFromIRQ
- `kmazarin/ksyscall/block.go` — SyscallBlockSubmit (the BlockSubmit SVC handler)
- `kmazarin/ksyscall/mailbox.go` — SyscallMailboxRecv (the blocking wait)
- `shared/hid/completion_ring.go` — CompletionRing structure, PollCompletionRing
- `mazarin/sys/block.go` — userspace BlockSubmit wrapper
- `mazarin/sys/completion_ring.go` — userspace PollCompletionRing

## Current State of the Code

- `fa5e7d0` is the last commit (spin-poll + priority wake + immediate switch)
- Uncommitted: removal of `runtime/proc.go` from userspace overlay in gen-overlay
- Branch: `fix/rawsyscall-experiment`
- The system boots, all shepherds launch, no crashes — but clocks.elf (and anything
  loaded after core shepherds are active) falls back to the slow notification path
