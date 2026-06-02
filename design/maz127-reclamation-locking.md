# MAZ-127 — Shepherd page reclamation & the lock/IRQ-discipline cascade

Status: **in investigation** (2026-06-02). Captures the design rationale and the
chain of deadlocks surfaced while making fork/exec + heap reclamation work, so the
reasoning survives across sessions. Pairs with PR #61 and Linear MAZ-127.

## What is solid (keep)

- **Kernel-emulated vfork double-return** (core MAZ-127): process-flavor clone
  spawns a transient child-context thread, suspends the parent, runs the in-child
  block, and the execve flush wakes the parent with the child PID. Unblocks Go's
  `os/exec` rawVforkSyscall.
- **Framebuffer double-free fix**: the FB (a system-shared resource) was being
  added to per-shepherd cleanup `Spans`, so the first shepherd death freed it and
  later deaths underflowed. Excluded from `Spans`.
- **Heap-reclaim leak diagnosis**: the normal (non-deferred) death path never freed
  the demand-paged mmap/Go-heap region — it's tracked by `BumpPointer`, never added
  to `Spans`, and Phase 2 ran `freeLeaves=false`. Every normal shepherd death leaked
  its whole heap → fork/exec churn exhausted the buddy pool (~597K UserHeap pages →
  OOM). Reclamation must walk the page table (`freeLeaves=true`) to cover the heap.

## The core constraint

Heap/page reclamation is **heavy — potentially milliseconds** (full page-table walk
+ per-page free). A milliseconds-long mutual-exclusion need **cannot**:
- be held under `schedulerLock` (blocks the scheduler for ms), nor
- be IRQ-masked for its duration (blocks the timer → "kernel stops cycling", which
  empirically breaks the kernel in timing-dependent ways).

So the long critical section must run **preemptibly** (IRQs enabled, schedulable)
with mutual exclusion via a **single-owner / sleepable** mechanism — NOT a spinlock.
Spinlocks are only legitimate for **microsecond** holds.

The deferred-reclamation design embodies this: the ms-long walk runs on thread 0's
idle loop (`DrainDeferredCleanups`), preemptible, IRQs enabled, single reclaimer.
The only spinlock is the **buddy free-list lock**, held **per-page for microseconds**
— not across the walk. This design is correct for the ms constraint; do NOT revert it.

## The deadlock cascade (each fix exposed the next)

1. **Teardown `schedulerLock` deadlock** — original code ran `CleanupShepherdPages`
   (heavy, non-nosplit) under `schedulerLock` on the non-growable exception stack.
   morestack / console-yield there re-opened preemption and the timer top-half
   re-entered `schedulerLock`. **Fixed** by deferring reclamation off the critical
   section onto thread 0's growable, lock-free `DrainDeferredCleanups`.

2. **Buddy-lock (`kmem.Spinlock`) deadlock** — the buddy lock is a plain CAS-spin via
   `yieldProcessor()` (WFE), **no IRQ masking, no timeout** (spins forever). It is
   acquired from BOTH preemptible context (the thread-0 drain) AND the **IRQ-masked
   demand-fault allocator** (the ARM64 data-abort handler runs with DAIF.I set and
   calls `AllocUserFrame → BuddyAllocTyped`). With the drain freeing pages
   preemptibly, thread 0 can be preempted mid-`BuddyFreeTyped` while holding the
   buddy lock; a fault-time allocator then spins on it IRQ-masked forever. This
   hazard is **pre-existing** (any preemptible buddy holder + a faulting allocator);
   the drain just made a preemptible holder frequent. Observed as
   `BuddyAllocTyped → yieldProcessor` spinning forever.
   - **Attempted fix**: make `kmem.Spinlock` IRQ-atomic (mask IRQs only for the
     microsecond hold; `savedDAIF` stashed in the struct; call sites unchanged).
     This eliminated the buddy hang — but reliably surfaced cascade item 3.

3. **`createCloneExecThreadImpl` `schedulerLock`-with-IRQs-enabled violation**
   (OPEN). After the buddy fix, the full config deadlocks during `RunShepherd`
   (rachel/linux launch). The `[SCHEDLK-VIOLATION]` detector + `ELR_EL1` capture
   confirm the holder: `main.createCloneExecThreadImpl` at `0x4590c384` (a field
   store deep inside its `schedulerLock` region) is running with **IRQs enabled**.
   It *does* call `sf.DisableAndSaveDAIF()` before locking, and the disassembly
   between `Lock` and that PC shows **no DAIF write and no call** — yet IRQs are on.
   The buddy fix's logic was reviewed ~7× and cannot itself enable IRQs under
   `schedulerLock` (it only restores the prior state). Working hypothesis: the
   violation is **pre-existing** (a genuine IRQ-enable-under-`schedulerLock` in the
   thread-creation path) and the buddy fix's IRQ-masking merely **perturbs timer
   delivery** enough to make the timer reliably land in that window.

## A/B evidence (full config, HVF)

| Build | rachel `RunShepherd` violation | boot-complete |
|-------|-------------------------------|---------------|
| master (909d6425, no MAZ-127) | 0/7 | 7/7 |
| 834adcec (deferred commit alone) | 0/4 | 4/4 (2/4 had a post-boot `!L`, the forkexectest-teardown path w/o the buddy fix) |
| lane (834adcec + CodeRabbit fixes + buddy fix) | 5/5 | 0/5 |

So the rachel violation is reliably triggered only with the working-tree fixes
(buddy fix the prime suspect via timing), and is NOT present on master in 7 runs.

## Decisive next test

Port **only** the detector (`ds.Spinlock.IsLocked` + `readELR_EL1` +
`ProcessDeadlinesTopHalf` holder-PC capture) onto the clean master worktree — no
buddy fix, no deferred work — and boot it many times.
- If master **also** catches `createCloneExecThreadImpl` → the
  IRQ-under-`schedulerLock` bug is **pre-existing**; fix that path, keep the buddy
  fix + deferred design.
- If master is **clean** even with the detector → my changes genuinely enable IRQs
  there; keep digging on the mechanism.

## The detector (keep, debug-gated)

`ProcessDeadlinesTopHalf` reads `ELR_EL1` first (the interrupted PC — reliable: the
ARM64 IRQ handler only *reads* ELR before the `CALL`), then if `schedulerLock`
`IsLocked()`, prints `[SCHEDLK-VIOLATION] ... holder PC=<elr>` via raw UART
(violation-only path). This found two real latent invariant violations this session
and is worth keeping as a cheap guard.

## Signatures (how to tell the deadlocks apart)

- `!L <hex>` = a `ds.Spinlock` dead-handler fired; hex = the lock address
  (`bin/target-nm build/kmazarin.elf | grep <addr>`; `45defa90` = `schedulerLock`).
- `[SCHEDLK-VIOLATION] ... holder PC=<elr>` = timer top-half found `schedulerLock`
  held with IRQs enabled; `<elr>` = the holder's PC.
- Silent hang spinning in `BuddyAllocTyped → yieldProcessor` (no `!L`) = the buddy
  `kmem.Spinlock` deadlock (no timeout/dead-handler on that lock type).
