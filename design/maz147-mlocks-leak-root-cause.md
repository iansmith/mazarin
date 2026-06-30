# MAZ-147 — root cause: `m.locks` leaked across the kernel's raw `usleep` switch on g0

Status: **ROOT CAUSE FOUND + LOCALIZED (2026-06-22)**. Fix not yet applied — options below
are an amd64/ARM64-shared design decision (consult before implementing).

MAZ-147 is the `fatal error: schedule: holding locks` crash (the "1b" / borrowed-m0 class,
child of MAZ-136). This doc records the confirmed mechanism, the evidence, why it is **not**
amd64-specific, and the candidate fixes with trade-offs.

> **Sibling, not duplicate, of MAZ-140.** MAZ-140 is the borrowed-m0 *allocation* nil-deref
> (`!F:0 @mallocgcSmallNoscan` — dead `m0.p`/mcache across GC-STW). MAZ-147 (this doc) is the
> *`m.locks` leak* → `schedule: holding locks`. Same family (both child-of-MAZ-136), different
> symptom/mechanism/fix. (The probe code files are named `maz140_mlock_probe_*` — kept from
> before the ticket split; they belong to MAZ-147 and are debug scaffolding to be removed.)

---

## 0. TL;DR

The Go runtime's mutex slow path (`lock2`) backs off via `runtime.usleep` **while running on
`g0` with `m.locks` nested (observed depth 3)**. kmazarin's `nanosleep` handler turns that
`usleep` into a **raw kernel context switch** (`SetSyscallSwitchTarget → doContextSwitchImpl`),
descheduling `g0` while it holds `m.locks`. The leaked `m.locks` rides on `m0`; if the runtime
reuses `m0` (borrowed-m0 syscall dispatch, or a P returning to it) before `g0` resumes and
unlocks, the next `runtime.schedule()` on that M sees `m.locks != 0` and throws.

It is **timing-masked, not architecture-specific**: every link in the chain is shared code.
amd64 only *observes* it because it runs under TCG (~30× slow), which widens the leak→schedule
window; ARM64 runs HVF-primary (fast) and has never been run under a long TCG batch.

---

## 1. The confirmed chain

```
runtime.lock2 (contended runtime mutex, on g0, m.locks++ nested ×3)
  → runtime.osyield → runtime.usleep.abi0            (rip=0x45880863, in the kernel ELF)
    → SYS_nanosleep
      → ksyscall.SyscallNanosleep                    (SHARED, ksyscall/)
        → SetSyscallSwitchTarget                     (SHARED, threads.go)
          → doContextSwitchImpl                      (SHARED, threads.go)
            → raw-switches g0 away while m0.locks = 3   ← THE LEAK
...later, m0 is reused (borrowed-m0 syscall dispatch / P returns) with m0.locks still 3...
  → runtime.schedule() asserts m.locks == 0 → throw("schedule: holding locks")
```

The fatal element is **not** the switch itself — it is that the leaked `m.locks` becomes
visible to *foreign* code (a different goroutine / a `schedule()`) on `m0`. See §5.

---

## 2. Evidence (flight-recorder probe, `maz140_mlock_probe_amd64.go`)

A per-switch flight recorder was added at `doContextSwitchImpl`, recording
`(M, g, rip, m.locks, target)` for every raw switch, with:
- a **first-sighting-of-each-M** emit (to settle the M-model question), and
- a targeted **`G0-LEAK`** live emit whenever the outgoing g is `runtime.g0` with `m.locks>0`,
- the full ring dumped at the kernel fatal via a new `ksyscall.OnKernelFatal` hook (the throw
  never reaches the shepherd-exit dump).

Findings:

1. **The crash (run 4, N=60 batch).** Last switch before the throw:
   `m=0x45e21700 g=0x45e1f640 locks=3 target=0`. Symbol-resolved against the kernel ELF:
   `0x45e21700 = runtime.m0`, `0x45e1f640 = runtime.g0`. So the crash switch is **g0 on m0,
   m.locks=3**. In that 256-entry ring, `g0` appears **exactly once** — only the fatal switch.

2. **The RIP (N=40 `G0-LEAK` hunt).** 3 of 40 **surviving** boots (12, 27, 31) emitted
   `G0-LEAK`, every time:
   ```
   [MLOCK] G0-LEAK m=runtime.m0 g=runtime.g0 rip=0x45880863 locks=3 target=…
                                              rip=0x45880863 locks=2
                                              rip=0x4588071f locks=1
   ```
   `addr2line`: **`0x45880863 = runtime.usleep.abi0`** (+0x43; matches the §9c
   `rip=0x45880863 = runtime.usleep.abi0` identification). `locks=3` is the exact crash
   signature. (`0x4588071f` is the locks=1 tail, in the adjacent abi0 stub region — secondary.)

3. **The leak pattern is mostly benign.** 3/40 boots emitted `G0-LEAK` and **survived** (0
   crashes that batch). The pattern (~7.5%) is far more common than the crash (~1/40 pre-probe,
   ~1/95 with the probe's per-switch overhead). So the g0/`usleep`/`m.locks` switch is
   *tolerated* most of the time — it only throws when the leaked count reaches a `schedule()`
   on `m0` before g0 resumes and unlocks. (This answers the "is a locked raw-switch always a
   bug?" question: no — only when it breaks the binding; see §5.)

Evidence preserved under `~/.claude/ticket-active/MAZ-141/evidence/`:
`run4-holdinglocks-mlockring.log`, `run{12,27,31}-g0leak.log`, batch summaries.

---

## 3. Why this is NOT limited to amd64

Every component is shared, non-arch-gated code (verified 2026-06-22):

| Component | Location | Arch |
|---|---|---|
| `nanosleep` handler (`SyscallNanosleep`) | `ksyscall/` | shared |
| `SetSyscallSwitchTarget` → `doContextSwitchImpl` | `kmazarin/threads.go` | shared |
| Borrowed `g0/m0` for user syscalls | MPNIL tripwire in `exceptions_amd64.s` **and** `exceptions_arm64.s` | both |
| `lock2`/`usleep`; `schedule()`'s `m.locks` assert | Go runtime | arch-independent |

There is no arch-specific link. **Contrast MAZ-143** (the morestack bug), which *was* genuinely
amd64-specific because it hinged on the **dual-home g** (R14 + TLS) — ARM64's single g-home
(X28) made it benign. That immunity does **not** transfer here: MAZ-147 is about `m.locks`, a
single field in the M struct, not an arch-homed register. ARM64 is latently vulnerable; it is
timing-masked on HVF and has never been exposed under a long ARM64 TCG batch.

**Validation plan:** port the probe to ARM64 (real `recordSwitchMLocks`, not the no-op stub)
and run a long ARM64 TCG batch. If `G0-LEAK` (g0 + `usleep` + `m.locks`) fires there too, the
both-arch nature is proven and any fix MUST be arch-neutral (in the shared switch/nanosleep
path, not an amd64 carve-out).

---

## 4. The invariant being violated

Stock Go guarantees: **a goroutine holding `m.locks > 0` is never preempted or descheduled,
and never migrated off its M.** `m.locks` is the runtime's "don't move me, don't preempt me"
flag, protecting per-M/per-P state (`mcache`, the current `P`, etc.). When `lock2` calls
`futexsleep`/`usleep` with `m.locks` held, stock Go **blocks the OS thread in the syscall** —
the M stays bound, `m.locks` stays set, and the goroutine resumes its critical section intact.
Nothing else runs on that M in the meantime.

kmazarin's raw switch **breaks this**: it deschedules `g0` (switches m0's thread away) while
`m.locks > 0`, and m0 can then be reused (borrowed-m0 dispatch / P return) before g0 resumes.
The leaked `m.locks` is then visible to foreign code → `schedule: holding locks`.

The benign heap-M `lock2` spinners (≈2245 locked switches/boot, 0 crashes) survive because a
*regular* goroutine resumes on its *own* dedicated M with `m.locks` intact — the binding is
preserved. `g0`/`m0` is the **shared, borrowed** system M, so its leaked `m.locks` reaches
foreign borrowers. That is the crux difference.

---

## 5. Fix options

Constraints any fix must satisfy:
- **C1 — the yield is mandatory.** g0's `lock2` backoff must release the P so the lock holder
  can run (GOMAXPROCS=1: one P). Busy-spinning g0 while holding the P deadlocks the lock
  holder. (This is why `stubs.go:92` deliberately allows locked yields.)
- **C2 — no foreign visibility of `m.locks`.** Foreign code on m0 must not observe g0's
  `m.locks`.
- **C3 — arch-neutral.** The bug is in shared code (§3).
- **C4 — don't regress the benign spinners.** The ≈2245/boot heap-M locked yields must keep
  working unchanged.

### Option A — Refuse the raw switch when `m.locks > 0` (mirror the existing guards)
`checkThreadPreemptionImpl` and `SaveThread0AndYield` already skip when `m.locks>0`; add the
same check to the `nanosleep` switch.
- **Verdict: ✗ rejected.** Violates **C1**. If g0 can't yield it busy-spins holding the P;
  with GOMAXPROCS=1 the lock holder can never get the P → deadlock. (Async preemption does not
  rescue it — the timer can't move the P off a spinning g0 that holds `m.locks`.) This is
  precisely the deadlock `stubs.go:92` warns about.

### Option B — Checkpoint `m.locks` across the raw switch (faithful isolation) — RECOMMENDED
In `doContextSwitchImpl`, when descheduling a g with `m.locks > 0`: **save** the outgoing M's
`m.locks` into the per-thread context, **zero the live `m.locks`** (so the M, if reused or
borrowed, presents `m.locks == 0`), and **restore** it when that thread/g resumes.
- Satisfies **C1** (the yield still happens — P moves, lock holder runs), **C2** (foreign code
  on m0 sees 0), **C3** (shared `doContextSwitchImpl`), **C4** (only changes `m.locks>0`
  switches; everything else identical).
- It is the natural extension of the **MAZ-139/143 "make the per-thread save faithful"**
  lineage — `m.locks` becomes one more piece of M-state checkpointed per thread, exactly like
  the XMM/TLS-g/vector slots were.
- **Correctness note:** zeroing is safe because the *actual* runtime lock g0 holds stays locked
  in memory (foreign code contending for it still blocks correctly); only the non-preemption
  *hint* (`m.locks`) is hidden for the foreign window, which foreign code does not need. On g0
  resume, the count is restored and `lock2` continues.
- **Risk:** save/restore timing must be atomic w.r.t. the switch; needs a deterministic
  RED→GREEN selftest (mirror `runGSPMismatchSelfTest`) proving foreign code in the window sees
  0 and g0 resumes with the count restored.

### Option C — Guard the borrow (don't borrow m0 while its g0 is parked with `m.locks`)
The leak only *surfaces* via the borrowed-m0 syscall path. Add a check at the borrow site
(`exceptions_{amd64,arm64}.s`): if `m0.locks > 0` and g0 is parked mid-`lock2`, defer/spin the
borrowing handler instead of running it on m0.
- Satisfies C1; targets C2 at the visibility point.
- **Risk:** adds a check to the **hot user-syscall borrow path** (every vector-129/SVC);
  complex; and it only covers the *borrow* visibility, not a P returning to m0 in the runtime.
  Narrower and more fragile than B.

### Option D — Replace the raw switch with a runtime-aware P-releasing park (most correct, heaviest)
The real need is "g0's `lock2` backoff should release the P like a blocking syscall, and let the
*runtime* account for `m.locks`." Make the locked-`usleep` path use **`entersyscallblock`
semantics** (handoffp) that the Go runtime already understands — so `m.locks` is managed by the
runtime, never leaked by a raw switch.
- Most semantically correct; aligns kmazarin with stock-Go expectations.
- **Risk:** most invasive — requires a runtime overlay touch and careful interplay with the
  borrowed-m0 model; highest blast radius. (Keep as the "right long-term" direction if B proves
  insufficient.)

### DECISION (2026-06-22): Option B
**Chosen: Option B** (checkpoint `m.locks` across the raw switch). It removes the leak at the
exact shared site, keeps the mandatory yield (no deadlock), is arch-neutral, doesn't regress the
benign spinners, and fits the established "faithful per-thread save" pattern from MAZ-139/143.

**Option D is the close second** — kept as the fallback / long-term direction if B proves
insufficient (e.g. if the zero-window turns out to be observable by a path other than the
borrow). C is a narrower variant of B's goal; A is ruled out by the deadlock.

Implementation + validation sequencing is in the continuation prompt:
`~/.claude/ticket-active/MAZ-147/continuation-option-b-fix.md`.

---

## 6. Validation plan (ORDERED — amd64 first, ARM64 only after amd64 is proven)
1. **Deterministic RED→GREEN selftest** (mirror `runGSPMismatchSelfTest` / the MAZ-139 D2
   canary): drive a synthetic g0 raw-save with `m.locks=3`, assert the fix presents 0 to a
   foreign reader in the window and restores 3 on resume. RED before B, GREEN after.
2. **amd64 TCG batch** grepping for `schedule: holding locks` (want 0) and `G0-LEAK` — with the
   fix, the `G0-LEAK` switch should either not occur or be provably leak-free. This is the
   **gate**: do not proceed to ARM64 until amd64 is clean over a large batch (≥N=60, ideally
   more given the ~1/40 base rate).
3. **(DEFERRED until step 2 passes)** ARM64 confirmation — port the probe off its no-op stub,
   run a long ARM64 TCG batch: first *confirm* the both-arch leak (expect `G0-LEAK`), then
   verify the same arch-neutral Option-B fix closes it on ARM64 too. Per the user's sequencing,
   this happens **after** amd64 is solved, not before.

## 7. Probe / scaffolding (remove before the fix PR) — ✅ DONE 2026-06-24
`maz140_mlock_probe_{amd64,arm64}.go`, the `recordSwitchMLocks`/`recordPreemptMLocks` calls, the
`mlockPreemptSkips`/`mlockRearmCount` counters + the asm `INCQ`, and the `ksyscall.OnKernelFatal`
hook + its registration are all **debug scaffolding** — they proved the root cause and confirmed the
fix mechanism, and have now been **stripped**. Verified: both arches build (frameaudit 23/23 amd64,
4/4 arm64), `nm` shows zero probe symbols in the ELF, and the asm restore + `g0PreemptHoldsMLocks`
guard + `mlockCheckpointSave` + `runMLockCheckpointSelfTest` remain intact.

The fix-only tree (this PR): `kmazarin/kmazarin/maz147_mlocks_checkpoint_{amd64,arm64}.go` (new), the
`exceptions_amd64.s` asm restore, the `threads.go` save + skip-guard, the `context_marshal_amd64.go`
selftest call, and this design doc.

The **MAZ-15 ring-trace instrumentation** (`maz/{fs,rachel,shepherd}`, `mazarin/fsclient`) is a
SEPARATE ticket's debug — **stashed** (`git stash` "MAZ-15 rachel boot-stall instrumentation"), NOT
in this PR. Restore it onto a MAZ-15 branch when that investigation resumes.

---

## 8. OPEN #1 RESOLVED (audit 2026-06-22): g0 resumes via `checkThreadPreemptionImpl`, NOT `doContextSwitchImpl` — the first-cut fix is mis-placed

**Question (continuation OPEN #1, "biggest risk"):** does g0 ALWAYS resume via `doContextSwitchImpl`,
where `mlockCheckpointRestore` is hooked?

**Answer: NO — and it almost never does.** kmazarin has **two independent Go-level context-switch
implementations** that never call each other:

| Funnel | Used for | Saves via | Returns | Restore hook? |
|---|---|---|---|---|
| `doContextSwitchImpl` (threads.go:4187) | directed/syscall switches (nanosleep, futex, clone, sigreturn) | `SaveContextFromFrame` | `&newThread.Context` | ✅ drafted (`mlockCheckpointRestore(newThread)`) |
| `checkThreadPreemptionImpl` (threads.go:3999) | timer preemption, priority-wake (`PriorityWakeSwitch`→here), idle-CPU pickup | `SaveContextFromFrame` | `&next.Context` (4172), idle `ctxPtr` (4014), boost (4036) | ❌ NONE |

They converge only at the assembly chokepoint **`load_context_and_iretq`** (exceptions_amd64.s:2149),
into which every switch-producer `JMP`s with `R12=&ThreadContext`.

**The g0 nanosleep resume trace (proves the miss):**
1. g0 `lock2`→`usleep`→`nanosleep`. `SyscallNanosleep` (real sleep) calls `AddDeadlineStatic` +
   **`ThreadBlockSleep()` → g0's thread is marked SLEEPING**, then `SetSyscallSwitchTarget(next)`.
   (Zero-tick backoff → `ThreadFindReady()`; thread stays `ThreadRunning`.)
2. SVC-return raw-switches via **`doContextSwitchImpl`** ← the SAVE site (`mlockCheckpointSave` zeroes
   `g0.m.locks`, stashes the count). g0's thread is now sleeping (deadline queue) or ready.
3. Deadline expires → timer ISR wakes g0's thread → **READY**.
4. g0's thread is RESUMED when **`checkThreadPreemptionImpl`** (timer) or **`tryPickupWorkIdleCPU`**
   (idle) picks it via `findReadyThreadSchedLockHeld()` and returns its context. **No restore runs.**

**Consequence:** the drafted restore fires only in the *minority* case where some *other* thread's
blocking syscall happens to pick g0's thread as its `doContextSwitchImpl` target. On the dominant
timer-preemption/idle resume it is MISSED → g0 resumes with `m.locks==0` (count LOST) and the global
`savedG0MLocks` stays stuck at 3. This swaps the original foreign-visibility corruption for a new
g0-side one (runtime non-preemption accounting underflows; a later incoming-g0 `doContextSwitchImpl`
stamps the stale count at the wrong time). The single global slot + wrong chokepoint is **unsound**.
**⇒ The first-cut placement (restore only in `doContextSwitchImpl`) is incorrect and must move.**

**Where the hooks actually belong (two viable shapes — DECISION NEEDED):**
- **R1 — assembly restore at `load_context_and_iretq`** (the single true chokepoint). Sits right next
  to the **existing MAZ-135 per-thread TLS-g restore** there (exceptions_amd64.s:2271–2278) — i.e. it
  reuses the *established* "restore per-thread state at the one resume chokepoint" pattern. Most robust
  (covers every resume path by construction). Cost: nosplit/no-frame asm, register pressure, both
  arches eventually (R12=&ctx live; would read `kmazarinG0Addr`+`savedG0MLocks`, compare `ctx.TLSG`,
  conditional 2 writes via the g→m / m→locks offsets). **Recommended for robustness + consistency.**
- **R2 — multi-site Go restore.** Call `mlockCheckpointRestore` at every context-returning Go site:
  `checkThreadPreemptionImpl` (the `&next.Context` return + the idle-pickup + boostThread0 sub-returns)
  **and** `doContextSwitchImpl` (already done). Avoids asm but is exactly the "miss-a-path" fragility
  this OPEN flag warns about; must also confirm `RunFirstThread`/`YieldToReadyThread`/`SaveThread0AndYield`
  cannot resume g0 (RunFirstThread is first-boot only, before g0 ever runs `lock2` → safe).

**Symmetry note (the SAVE side):** for faithfulness the save should also move to the shared point
(`SaveContextFromFrame`, or a hook right after it in both funnels) so a *timer preemption* of g0 while
it holds `m.locks` (not just the usleep raw switch) is covered too. Evidence (§2) shows the leak RIP is
always `usleep.abi0` (the `doContextSwitchImpl` path dominates), so save-in-`doContextSwitchImpl` covers
the *proven* case — but R1 naturally symmetrizes both sides at the asm save/restore chokepoints.

This is an **amd64 architectural decision** (shared scheduler internals) — surfaced for Ian per the
"no arch decisions without consulting; extra force on amd64" rule before re-placing the hooks.

### 8a. DECISION + IMPLEMENTATION (2026-06-22): R1 (asm chokepoint)

Ian chose **R1**. Implemented:
- **RESTORE** — pure asm at `load_context_and_iretq` (exceptions_amd64.s, right after the RSP0
  rotation, before the FS_BASE/TLS-g restore): `if savedG0MLocks!=0 && ctx.R14==kmazarinG0Addr {
  *savedG0MLocksPtr = savedG0MLocks; savedG0MLocks = 0 }`. Gates on **ctx.R14** (not ctx.TLSG) to
  match the save key EXACTLY (the save fired on `GetGRegister()==g0`, and the loaded ctx is that same
  saved struct → ctx.R14 is byte-identical to save time; more robust than TLSG which can transiently
  disagree per MAZ-135). Writes via the **precomputed `savedG0MLocksPtr`** so the nosplit/no-frame
  path needs no cross-package `kirq` offset math. AX/BX/R13 scratch; R12 (&ctx) preserved. objdump
  confirmed in the ELF: `MOVQ 0x68(R12),AX` (0x68=104=R14 offset ✓), `CMPQ AX,kmazarinG0Addr`, all
  skips → `mlock_restore_done`, `MOVL R13,(AX)`, `MOVL $0,savedG0MLocks`.
- **SAVE** — `mlockCheckpointSave(oldThread)` stays in `doContextSwitchImpl`, **moved past the
  `newThread==nil` guard** (placing it earlier could zero g0.m.locks on a no-switch return where g0
  keeps running → count lost). Stashes count + `&g0.m.locks` + zeroes the live count. Still inside the
  schedulerLock+IRQs-off critical section.
- **amd64-gated** (`maz147_mlocks_checkpoint_amd64.go` + arm64 no-op stub): a shared save without the
  ARM64 asm restore would zero g0.m.locks and never re-arm it. ARM64 save+restore land together in the
  deferred ARM64 phase. ⚠ Flagged divergence, sequencing-only.
- **Selftest** — `runMLockCheckpointSelfTest` (gated by `ctx_marshal_test`): RED→GREEN on the save-side
  invariant (foreign reader sees 0), + the re-arm data contract + the non-g0 gate. The asm restore
  instructions themselves are covered by objdump + the TCG batch (a context load can't be unit-invoked).
- Build: `kmazarin:x86_64` GREEN, frameaudit 23/23, nosplit OK; new kernel confirmed embedded in
  `build/esp-kmazarin.img`.

**Known residual (evidence-bounded, documented):** the SAVE covers only the `doContextSwitchImpl`
switch-out (the *proven* usleep/nanosleep leak path, RIP always `usleep.abi0`). A timer preemption of
g0 *while holding m.locks outside a syscall* (switch-out via `checkThreadPreemptionImpl`, unobserved by
the probe) is not zeroed → leak-vulnerable on that path, but g0 stays self-consistent (no save → asm
restore is a no-op → count never lost). The RESTORE is robust for ALL resume paths regardless. If the
batch surfaces a `G0-LEAK` at a non-`usleep` RIP, add the save to `checkThreadPreemptionImpl` too
(symmetric, after its `next!=nil` commit point) — a follow-up decision, not done unilaterally.

### 8b. GATE RESULT (2026-06-22): R1 FAILED — the residual is load-bearing

N=100 batch (killed at 55 by a session suspend; 55 is conclusive). **1× `schedule: holding locks`
(run 16)** — gate wants 0, so **R1 as built does NOT close the leak.** Tally: SUCCESS 39, CRASH 2
(run16 = MAZ-147 holding-locks; run32 = `gcmarknewobject during checkmark` = MAZ-140, separate),
STALL-RACHEL 12 (MAZ-15), TIMEOUT/DIED 2. MAZ-147 rate ~1/55 sits inside the pre-fix band
(~1/40 bare / ~1/95 with-probe) → at n=1 event, statistically indistinguishable from "fix did nothing".

Run 16 forensics: the probe's `G0-LEAK` fired once (the usleep switch — save zeroed correctly), yet the
throw hit in `runtime.schedule()` ← `park_m` ← `mcall` **on g0's stack** with `m.locks` nonzero, and
`g0Leak=1` (the leak still escaped despite the save). The throw is "panic during panic". This matches
§8a's residual: after the asm re-arms `g0.m.locks`, the **timer re-preempts g0 via
`checkThreadPreemptionImpl` (no save)** → re-leak onto a borrowed m0. The probe is blind to that path
(it only instruments `doContextSwitchImpl`), explaining `g0Leak=1` while the leak escaped.

**Decision (Ian): confirm-then-fix.** Added confirmation instrumentation (debug, removed with the
probe): `recordPreemptMLocks` in `checkThreadPreemptionImpl` (emits `PREEMPT-G0-LEAK` + counts
`mlockPreemptG0Switches`/`mlockPreemptG0Leak` when g0 is preempted holding m.locks; cheap g0-only
fast-reject on the hot path), and an `INCQ mlockRearmCount` at the asm re-arm.

### 8c. CONFIRMED (2026-06-22): the leak is the TIMER-PREEMPT path, not usleep — and it's DIRECT

Confirmation batch (killed at 9 by a suspend; conclusive): **`PREEMPT-G0-LEAK` fired 6× across 4/9
boots (~44%)** — g0 (`0x45e20640`) preempted by the timer while holding `m.locks`. Two refinements
that change the fix:

1. **`rearms=0` on every emit.** The asm re-arm had NOT fired — so this is **NOT** "re-arm then
   re-preempt" (my §8b guess). It is a **direct, independent leak** via the timer-preempt funnel,
   happening *before/without* any usleep checkpoint.
2. **Varied rips, `locks=1–2`** (e.g. `0x45875d64`, `0x4581b4c0`, `0x4587bc92`, `0x4582e21f`,
   `0x4583af7c`, `0x4581b920`) — runtime-internal lock2/critical sections, **not** `usleep.abi0`.
   The original "leak rip is ALWAYS usleep" was a **probe artifact**: the old `recordSwitchMLocks`
   only instrumented `doContextSwitchImpl`, so it was structurally blind to the preempt-path leaks.

**Conclusion:** the DOMINANT leak is `checkThreadPreemptionImpl` (the timer) switching g0 out while it
holds `m.locks`, with no save. R1 fixed the *minority* path (usleep/`doContextSwitchImpl`). The audit
also found a **third** g0 switch-out path: `boostThread0ForPendingWork` (SaveContextFromFrame →
SetCurrentThreadGlobal(thread0) → return thread0 ctx) — also unguarded.

### 8d. Fix shapes for the preempt paths (DECISION NEEDED)

Both `checkThreadPreemptionImpl` and `boostThread0ForPendingWork` need handling. Two shapes:

- **SKIP (recommended) — mirror stock Go: don't *involuntarily* preempt g0 while `m.locks>0`.** Add a
  guard (like the existing `gspUnsafeKernelResume` skip) that returns 0 (no switch) when oldThread is g0
  and `m.locks>0`. g0 keeps running its short critical section, then either drops `m.locks` (preemptible
  again) or hits lock2 backoff → `usleep` (the *voluntary* yield, handled by R1's save/restore). **No
  leak, no save state on the hot path, no asm dependency.** The design doc's "Option A rejected" applies
  ONLY to the usleep path (C1: usleep MUST yield to release the P or deadlock) — it does NOT apply to
  *involuntary* timer preemption, where letting g0 finish is exactly stock-Go semantics (`m.locks` =
  non-preemptible). lock2 active-spin is bounded (~4 iters) → no livelock; backoff yields via usleep.
- **SAVE (Ian's first instinct) — symmetric checkpoint.** Call `mlockCheckpointSave(oldThread)` at the
  commit point of BOTH preempt paths (zero+stash; the asm restore re-arms on resume). Uniform with R1,
  keeps g0 preemptible, but adds save state to the hot path and is **3 enumerated sites** (fragile — the
  audit's recurring "miss a path" risk; a 4th switch-out path would silently re-open the leak).

**Hybrid (the actual recommendation):** keep **R1 save/restore for usleep** (mandatory yield) + **SKIP
for the two preempt paths** (involuntary). This matches stock Go precisely: voluntary `m.locks` yields
are checkpointed; involuntary preemption is suppressed while `m.locks>0`.

### 8e. DECISION + IMPLEMENTATION (2026-06-23): SKIP (the hybrid)

Ian chose **SKIP**. Implemented:
- `g0PreemptHoldsMLocks(framePtr)` (maz147_mlocks_checkpoint_amd64.go) — reads the LIVE interrupted g
  from the exception frame (same effective-g rule as `gspUnsafeKernelResume`: `gLooksValid(slot)?slot:R14`),
  returns true iff effG==g0 and `g0.m.locks>0`.
- Guard in `checkThreadPreemptionImpl` **right after the `gspUnsafeKernelResume` guard, before the lock**
  → `if g0PreemptHoldsMLocks(framePtr) { return 0 }`. One guard covers BOTH the main preempt path and
  `boostThread0ForPendingWork` (which runs later in the same function). g0 keeps running; no switch-out,
  no leak.
- R1's usleep save/restore (asm chokepoint) is UNCHANGED (mandatory-yield path still needs it).
- amd64-gated (arm64 stub returns false). Build GREEN (frameaudit 23/23, nosplit OK); objdump-verified
  the guard call + the skip in the ELF; fresh esp `17482e61be90`.
- Probe kept for validation: expect `PREEMPT-G0-LEAK` → ~0 (was ~6 across 4/9 boots), a nonzero
  `skips=` counter (guard actively firing), and **0 `schedule: holding locks`**.

Validation batch (N=60) running. NOTE: prior batches were repeatedly killed by session suspends
(gate@55, confirm@9) — partial data still confirms the mechanism (skips>0, leak→0 visible per-boot);
the crash-rate gate (0 holding-locks) needs the larger N to be statistically meaningful.

### 8f. GATE PASSED (2026-06-24): N=100 clean sweep — SKIP closes the leak

Full **N=100** nohup-detached batch (esp `17482e61be90`, survived the suspend that killed the prior
tracked runs): **SUCCESS=100, CRASH=0, STALL=0, TIMEOUT=0, GSPMM-total=0.**
- **GATE: 0 `schedule: holding locks` in 100 boots** (vs ~1.8 expected at the pre-fix ~1/55 rate; the
  R1-only build had 1/55). The MAZ-147 crash is closed.
- **MECHANISM: `PREEMPT-G0-LEAK` = 0 across all 100 logs** (the confirmation batch showed ~6 across
  4/9 boots ≈ 44%/boot; 0/100 where ~44 were expected is overwhelming proof the skip fires before the
  recorder). doContextSwitchImpl `G0-LEAK` = 0 too (R1 still covers the usleep path).
- No regressions: 8/8 shepherds every run, GSPMM silent (MAZ-141/143 clean), no morestack/!F:0. Runs
  with `GODEBUG=gccheckmark=1` (kernel default) → the gcmarknewobject-checkmark family (gate run 32 /
  MAZ-140) did NOT recur here ⇒ the gccheckmark soak (validation step 4) is satisfied by these 100 boots.
- Caveat: the direct `skips=` counter prints only at shepherd-exit/kernel-fatal, which clean SUCCESS
  boots never trigger, so it's unshown — the `PREEMPT-G0-LEAK=0/100` proxy + the gate are conclusive.

**REMAINING:** strip all debug scaffolding (the `maz140_mlock_probe_*` recorders, `recordSwitchMLocks`/
`recordPreemptMLocks` calls, `mlockPreemptSkips`/`mlockRearmCount` + the asm `INCQ`, the `OnKernelFatal`
hook + registration, and the SEPARATE MAZ-15 instrumentation in `maz/{fs,rachel,shepherd}` +
`mazarin/fsclient`) → fix-only tree = the `g0PreemptHoldsMLocks` guard + R1 save/asm-restore +
`runMLockCheckpointSelfTest` + this design doc → `/ticket-pr`. ARM64 save/restore/skip port deferred.

---

## 9. ARM64 CONFIRMED-VULNERABLE (2026-06-25): timing-masked, NOT immune — reproduced under `-icount`

The amd64 fix merged (PR #82, `5daf3753`). Before porting to ARM64 we tested whether ARM64 even
has the bug, since HVF never showed it and the §3 "both-arch" claim was a code-reading, not a repro.
Answer: **ARM64 has the identical bug; it was only ever timing-masked.** Three lines of evidence:

### 9a. Code symmetry confirmed (the H2/H3 refutation)
- **Leak source is shared** — `lock2→usleep→nanosleep→doContextSwitchImpl` is Go-runtime + `threads.go`,
  non-arch-gated. ARM64's `mlockCheckpointSave` / `g0PreemptHoldsMLocks` are **no-op stubs**, so ARM64
  is *unprotected*, not safe: if g0 is switched out holding `m.locks`, the count rides m0 with zero
  checkpointing.
- **Fatal sink is symmetric** — the borrowed-g0/m0 syscall dispatch is NOT amd64-specific. ARM64's
  `el0_sync_handler` (`exceptions_arm64.s` ~L1918) loads `kmazarinG0Addr` into X28 and runs the syscall
  handler on the borrowed g0/m0 with the same MAZ-136 MPNIL tripwire — the in-code comment names it the
  "amd64 twin: the vector-129 block in exceptions_amd64.s". So a foreign syscall can reach `schedule()`
  on the borrowed m0 on BOTH arches.
- ARM64's `gspUnsafeKernelResume` is *also* a no-op (MAZ-143 is benign on ARM64), so **more** timer
  preempts proceed on ARM64 → if anything a *wider* leak window, not narrower. H3 ("leak can't occur on
  ARM64") is refuted by construction.

### 9b. Empirical repro — `-icount` rate curve (THE proof)
Stock ARM64 TCG is *same-arch* (aarch64-on-aarch64) and boots in ~33 s — ~30–100× faster than amd64's
*cross-arch* (x86-on-aarch64) TCG that originally exposed this at ~1/40–55. So the leak→`schedule()`
race window is too narrow to catch at stock speed. QEMU `-icount shift=N` makes guest time advance per
instruction (2^N ns/instr), so the fixed-period timer fires after *fewer* instructions → g0's
`m.locks` window spans more preemptions → reproduces amd64-TCG tick density deterministically (and
**host-load-independent**, which is why it's a reliable harness).

| condition | `schedule: holding locks` rate |
|---|---|
| stock ARM64 TCG (≈shift 0), N=60 (2× baselines) | **0/60** |
| `-icount shift=4`, N=20 | **9/20 (45%)** (+6 MAZ-140-family crashes, +5 MAZ-15 rachel stalls) |
| `-icount shift=5`, N=20 | **16/20 (80%)** |
| `-icount shift=6`, N=20 | **20/20 (100%)** — every crash is `holding locks`, none other |
| `-icount shift=8/10` | UNUSABLE — UEFI watchdog resets during early ELF load (slowdown artifact) |

The rate climbs **monotonically with slowdown** (0% → 45% → 80% → 100%), which *is* the timing
relationship made quantitative. A representative shift=6 throw: two interleaved fatals
`gcmarknewobject called while doing checkmark` (MAZ-140) + `schedule: holding locks` (MAZ-147,
`runtime.schedule()` at threads.go:1607, m=nil) — the borrowed-m0 family co-occurring, exactly as on
amd64 §8b.

### 9c. Conclusion + the ARM64 RED harness
**The ARM64 port is MANDATORY for correctness, not defensive.** ARM64 is architecturally identical and
equally vulnerable; the only reason it looked clean is same-arch TCG speed.

**`-icount shift=6` is a 100% deterministic RED harness** (20/20, crash in ~18–40 s/boot) — the ARM64
analogue of amd64's N=100 batch gate. The port is validated when shift=6 goes **20/20 → 0/20** (plus a
shift=4/5 sweep to confirm the whole curve collapses). Harness:
`~/.claude/ticket-active/MAZ-147/arm64-tcg-batch-parallel.sh` (parallel-safe: per-sweep `TAG`,
targeted `kill $QPID` not broad `pkill`, `ICOUNT=N`). Baseline harness (broad-pkill, single sweep):
`arm64-tcg-batch.sh`.

**Port scope (the three stubs → real):** (1) the asm `m.locks` restore at the ARM64 resume chokepoint
(the `load_context_and_iretq` analogue in `exceptions_arm64.s`), keyed on the restored g==g0 — note
ARM64's single g-home (X28) simplifies the effective-g read vs amd64's dual-home; (2) `mlockCheckpointSave`
(shared-shaped → as landed, hoisted to an untagged file rather than an arm64 stub becoming real; see §10);
(3) the `g0PreemptHoldsMLocks` skip-guard
(arm64 frame layout: read the interrupted g from the EL1 exception frame). High-risk unknown = the asm
restore chokepoint convergence (redo the §8 funnel analysis for ARM64). amd64 guard is CPU-0-scoped →
full SMP is MAZ-142.

## 10. ARM64 GREEN (2026-06-25/26): port landed, RED harness collapses 100% → 0%

The three stubs are now real. Shape of the port:
- **Hoist** — the arch-neutral save path, package globals, and `runMLockCheckpointSelfTest` moved to an
  untagged `maz147_mlocks_checkpoint.go`; the amd64 file keeps only its skip-guard, the arm64 file gains
  the real frame-reading `g0PreemptHoldsMLocks` (interrupted g via X28 at the EL1 frame, kernel-mode via
  `SPSR.M` — no `gLooksValid` deref needed).
- **Unified asm restore** — one `mlockRearmFromFrame<>` subroutine in `exceptions_arm64.s`, `BL`'d from
  the 3 `CTX_RESTORE_TO_FRAME` sites (LR-dead verified for all three return paths).
- Selftest wired into the arm64 ctx-marshal runner. Builds green both arches; `frameaudit` 23/23 (amd64,
  hoist didn't regress) + 4/4 (arm64).

**objdump-verified in the fixed ELF** (esp `57d86e7445b0`): exactly 1 subroutine + 3 `bl` callsites; body
reads the frame's saved X28 at `[sp,#224]`, g-matches `savedG`, writes `savedMLocks`→`*savedMPtr`, then
one-shot-clears `savedMLocks`. Faithful port of the amd64 save/restore arm (§8 hybrid).

### 10a. RED → GREEN sweep (same `-icount` harness, N=20 each)
| icount | RED (esp `9016e9b67b49`, stubs) | GREEN (esp `57d86e7445b0`, fixed) |
|---|---|---|
| shift 4 | CRASH 15, **holding-locks 9/20 (45%)** | CRASH 0, **holding-locks 0/20** |
| shift 5 | CRASH 20, **holding-locks 16/20 (80%)** | CRASH 1¹, **holding-locks 0/20** |
| shift 6 | CRASH 20, **holding-locks 20/20 (100%)** | CRASH 1¹, **holding-locks 0/20** |

¹ Both GREEN crashes are `fatal error:` at the `linux` shepherd — a known unrelated family, `holdLocks=0`,
not a `schedule: holding locks` throw. The §9c gate (**shift=6 20/20 → 0/20**) passed, and the whole curve
collapsed with it.

### 10b. No boot regression — the "STALL not SUCCESS" artifact
The GREEN sweeps show `SUCCESS=0 / STALL≈all`, which is **not** a regression: under `-icount shift 4/5/6`
neither RED nor GREEN reaches the harness's `boot sequence complete` gate within the deadline (even RED's
5 non-crashing ic4 runs stalled identically). RED runs ended early by *crashing* on the leak; the fixed
build doesn't crash, so it runs to the deadline → `STALL-RACHEL`, having progressed through 5–11 shepherds
(late test-fixture stage) and `uptime` into the tens-of-thousands. Confirmed by a **clean HVF boot of the
fixed esp**: `[rachel] ready in 186ms`, all 15 shepherds, **`boot sequence complete`**, steady-state
`uptime=70→100`, zero crash signatures.

### 10c. Harness caveats (for the next reader)
- The sweep's `START` banner prints a **hardcoded** `UNFIXED/stubs` string — it is *not* esp-derived. Trust
  the `esp=<sha>` field + objdump, not the label.
- `arm64-tcg-batch-parallel.sh` uses `set -u`; with `ICOUNT` empty (stock), `"${ICOUNT_ARGS[@]}"` expands
  an empty array → "unbound variable" on bash 3.2 and QEMU never launches (`shep=0 reached=[]`). The
  "stock GREEN" sweep was invalid for this reason — use a non-empty `ICOUNT` or guard the array expansion.

amd64 guard is still CPU-0-scoped → full SMP is MAZ-142.
