# Continuation: Investigate Mailbox Blocking Syscall Hang

## What we're doing

We're trying to get clocks/linux shepherds working and coordinating with rachel on ARM64 HVF. The KernelSVCWorker refactor is complete and working. The DMA read pre-invalidation fix (`kmazarin/ksyscall/iouring.go`) is in place.

## The bug

Rachel hangs after loading fontsvc.maz when using the original load order (fontsvc first, prefs second). The system deadlocks — thread 754 (rachel) is in `ThreadRunning` state but executes zero userspace instructions for 46+ seconds. SVC count freezes. The CPU is stuck in the kernel at `main.EnableIRQs.abi0` (address `0xFFFFFFFF4390FCA8`), which is inside `enableIRQsAndWait` — a WFI loop.

## Key experimental result

**Swapping the load order (prefs first, fontsvc second) eliminates the hang.** Both .maz files load and run. Rachel publishes Ready. This **disproves the cache coherency hypothesis** — the issue is not I-cache/D-cache corruption after .maz loading.

## What the evidence points to

The critical difference between the two orders:

**Original order (HANGS):**
1. `LoadMazBootstrap("/fontsvc")` — succeeds
2. `go mazhost.RunMaz(fontSvcMain)` — starts fontsvc goroutine
3. fontsvc goroutine enters `MailboxRecv()` — **blocking SVC** (thread goes to `ThreadBlockedMailbox`)
4. `runtime.Gosched()` — calls `sched_yield` SVC
5. `go mailboxLoop(rachelCh, inputRing)` — starts another goroutine
6. `runtime.Gosched()` — calls `sched_yield` SVC
7. `mazhost.LaunchMaz("prefs")` — **NEVER REACHED**, thread 754 stuck

**Swapped order (WORKS):**
1. `mazhost.LaunchMaz("prefs")` — succeeds (no goroutines running yet)
2. `LoadMazBootstrap("/fontsvc")` — succeeds
3. `go mazhost.RunMaz(fontSvcMain)` — starts fontsvc goroutine
4. fontsvc enters `MailboxRecv()` — blocking SVC
5. `runtime.Gosched()` etc. — all works fine
6. `rachel Ready=true` — published successfully

## Hypothesis: MailboxRecv blocking + Gosched interaction

Something about fontsvc's goroutine entering `MailboxRecv()` (which does a blocking `RawSyscall6` that transitions the kernel thread to `ThreadBlockedMailbox`) causes the subsequent `runtime.Gosched()` on rachel's main goroutine to get stuck. Possible causes:

1. **Go scheduler sees RawSyscall blocking**: `RawSyscall6` doesn't call `entersyscall`/`exitsyscall` (those are removed in the overlay). The Go runtime may think M0 is available when it's actually blocked in a kernel SVC. When `Gosched()` calls `mcall(gosched_m)` → `schedule()` → `findrunnable()`, something may spin or deadlock because the expected M state doesn't match reality.

2. **Thread state confusion**: fontsvc's goroutine runs on the same OS thread (GOMAXPROCS=1 effectively, single CPU). When fontsvc's `MailboxRecv` blocks the thread at the kernel level, the Go scheduler on that M may not know the thread is blocked. The `Gosched()` SVC tries to yield but the thread state is already confused.

3. **Futex/epoll path in findrunnable**: After `Gosched()`, the Go scheduler's `findrunnable()` may enter `netpoll()` (epoll_pwait) or `futex_wait()`. These are also kernel SVCs. If the thread is somehow in a bad state from the prior MailboxRecv blocking, these SVCs could deadlock.

## Files to investigate

- **`mazarin/sys/mailbox.go`** — `MailboxRecv()` implementation. Check if it uses `RawSyscall6` vs `Syscall6`. The difference matters for Go scheduler awareness.
- **`kmazarin/ksyscall/dispatch.go`** — SVC dispatch table. Check how MailboxRecv SVC is handled.
- **`kmazarin/kmazarin/threads.go`** — `ThreadBlockedMailbox` state transitions. Check if waking from mailbox correctly restores thread state.
- **`kmazarin/ksyscall/stubs.go`** — `SyscallSchedYield`. Check the `thread0HasPendingWork()` guard and `threadFindReadyForYield()` interaction with mailbox-blocked threads.
- **`kmazarin/ksyscall/epoll.go`** — `SyscallEpollPwait`. The Go runtime's `findrunnable()` calls `netpoll()` which calls `epoll_pwait`. Check if this path interacts badly with mailbox state.
- **`kmazarin/ksyscall/futex.go`** — Futex paths called from Go scheduler.

## Epoch data from the hang

```
[E:up=33s] svc=7636 fw=113/7 R=4{0,392,380,87} F=3 S=1 I=0 M=1 D=1 X=1[754s2]
```

- R=4: threads 0, 392, 380, 87 are Ready
- F=3: 3 threads futex-blocked (Go runtime internal)
- S=1: 1 sleeping (sysmon?)
- M=1: 1 mailbox-blocked (fontsvc)
- D=1: 1 delegate-blocked (fs serve loop)
- X=1: thread 754 Running but making no progress
- IL=415975: frozen — no userspace instructions executing
- svc=7636: frozen — no new SVCs being dispatched
- EL1h address at `EnableIRQs.abi0` — CPU in WFI loop in kernel

## Other issues found during this session

1. **`[fs] failed to read /linux.elf`** — in the swapped-order run, linux.elf read failed even though it's on the ext2 disk image. Possibly same DMA cache issue for this specific read. The ext2 disk is created by mkext2 with linux.elf included (`{{.LINUX_ELF}}` in Taskfile.yml line 513).

2. **RISC-V boot failure** — `ERROR: Invalid boot signature` from diplomat FAT32 mount. Pre-existing, unrelated to our changes.

## Current file state

- `kmazarin/ksyscall/iouring.go` — DMA read pre-invalidation fix in place
- `kmazarin/kmazarin/kernel_worker.go` — KernelSVCWorker refactor complete
- `flock/cmd/rachel/main.go` — reverted to original order (fontsvc first, prefs second)
- `flock/cmd/clocks/main.go`, `flock/cmd/linux/main.go` — have pending changes (from git status)
- Bridge files deleted: `loadmaz_bridge.go`, `runmaz_bridge.go`, `runshepherd_bridge.go`, `epoll_bridge.go`
- `kmazarin/kmazarin/threads.go` — `ThreadBlockedKernelWork` replaces old states, `hasPendingKernelWork` moved to kernel_worker.go
