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
   — **ROOT-CAUSED AND FIXED.** After the buddy fix, the full config deadlocked
   during `RunShepherd` (rachel/linux launch). The `[SCHEDLK-VIOLATION]` detector +
   `ELR_EL1` + DAIF probes localized it: IRQs are masked through `Lock`/`Acquire`/
   `Shepherds.Allocate` but become enabled while writing the freshly-allocated
   `Shepherd` (`p.*` / `SetStartupState`). That write **demand-faults** (the
   `proc.Shepherds` storage page is non-resident under the lane's reclamation churn),
   and **`sync_return` (the synchronous-exception return path) unconditionally cleared
   `SPSR.DAIF.I` (`BIC $0x80`) before `ERET`** — force-enabling IRQs on *every* fault
   return, including a fault taken inside an IRQ-masked kernel critical section.
   So the page fault returned with IRQs enabled while still holding `schedulerLock`
   → timer fired → top-half re-entered `schedulerLock` → deadlock.
   - **Fix**: condition the `BIC` on the return EL. Force IRQs enabled only when
     returning to **EL0** (userspace must run with IRQs on — there the BIC is
     correctness); **preserve the saved `SPSR.DAIF` on EL1 returns** (a fault under
     `schedulerLock` must return still-masked). Test `SPSR.M[3:2]` (0=EL0, 1=EL1).
   - **Pre-existing**: master never hit it because, with no reclamation churn, the
     Shepherd page is resident → no fault → `sync_return` never runs. The deferred
     reclamation is what makes shepherd-allocation writes demand-fault under the lock,
     reliably exposing this latent exception-return bug. Same mechanism (a fault under
     `schedulerLock` re-enabling IRQs) underlies the teardown deadlock (item 1) too.

## Second latent bug found (tracked as MAZ-128)

`SaveAndDisableIRQs` (exceptions_arm64.s) reads **NZCV instead of DAIF** — `WORD
$0xD53B4200` is `MRS X0, NZCV`; the correct `MRS X0, DAIF` is `$0xD53B4220` (op2=1).
The *mask* (`MSR DAIFSET,#2`) is correct, but the *saved* value is NZCV (DAIF bits
zero), so `RestoreIRQs` has never restored real DAIF — it always lands on
IRQs-enabled. Benign by luck on the common path (callers want IRQs back on), but
wrong for nested-mask scenarios. The buddy fix's `kmem`-local copy had the same typo
(it made `restoreIRQsLocal` force-enable IRQs on every buddy unlock) — **that copy is
corrected to `$0xD53B4220`**; master's `SaveAndDisableIRQs` original still has it.

## A/B evidence (full config, HVF)

| Build | rachel `RunShepherd` violation | boot-complete |
|-------|-------------------------------|---------------|
| master (909d6425, no MAZ-127) | 0/7 | 7/7 |
| 834adcec (deferred commit alone) | 0/4 | 4/4 (2/4 post-boot `!L`) |
| lane, buddy fix, **before** `sync_return` fix | 5/5 | 0/5 |
| lane, **after** `sync_return` EL-conditioning | **0/5** | 4/5 (others slow-boot, no deadlock) |

Long validation run (after the fix): full boot, `[pipe2test]`/`[xfertest] PASS`,
forkexectest starts, child exits status=42, reclamation healthy
(`heap≈10MB`, no Buddy OOM, no `UNDERFLOW`), no `!L`/`SCHEDLK`.

## How it was root-caused (record of method)

Detector-on-master (the detector ALONE on clean master, 8 boots) was **0/8** — so
the violation was NOT pre-existing in master; my work triggered it. Bisect: 834adcec
alone 0/4, lane 5/5. DAIF probes inside `createCloneExecThreadImpl` then localized
the IRQ flip to the `p.*`/`SetStartupState` writes — and a probe-encoding slip
(`MRS NZCV` vs `MRS DAIF`) incidentally exposed the second latent bug above. The
final clue was the in-asm mask-then-read showing "masked", which pointed at the
*call/return boundary* — i.e. a fault → `sync_return` → BIC. Reading `sync_return`
confirmed it. (Lesson: trust the independent `SCHEDLK-VIOLATION` — the timer
actually firing — over hand-rolled DAIF probes; and double-check `MRS`/`MSR` system
register encodings.)

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
