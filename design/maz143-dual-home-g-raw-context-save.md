# MAZ-143 (reopened) — systemic dual-home-g hazard in the amd64 raw context save

_Design doc for a working session, 2026-06-16. Status: investigation complete, fix
NOT yet chosen. The merged fix C (PR #79) is incomplete: it guards the wrong path and
the wrong g-home. This doc maps the full hazard and lays out fix options + open
questions. Branch `fix/MAZ-143-effective-g` holds a partial hardening (kept, see §7)._

## 0. TL;DR
The amd64 kernel keeps `g` in **two homes** (R14 + TLS `[FS_BASE-8]`). kmazarin's raw
context save/restore reconstructs them **independently** from an exception frame. In the
g0-transition windows of `runtime.morestack` / `systemstack` / ABI0 wrappers, the two
homes momentarily disagree (one is already `g0`, the other not). Checkpointing that
transient and later restoring it manufactures a `(g==g0, SP not on g0's stack)` state →
the next prologue trips `morestack on g0` (or other GC/scheduler corruption). ARM64 is
immune: single home (X28), restored verbatim, resumes *at* the window instruction which
completes the switch.

**Leading root hypothesis:** `gLooksValid(v) = v!=0 && v < 0x0000800000000000` rejects
*high-canonical* addresses, but **kernel goroutine `g`s are high-canonical** (`0xffff8001…`)
while `g0` is low (`~0x45e1d480`). So `SaveContextFromFrame` **always** discards the
real captured TLS-g for kernel goroutines and falls back to **R14** — correct when
R14==g, catastrophic in the window where R14 is transiently g0.

## 1. The two homes (amd64) and how they're used
- **R14** — the register-ABI g. Compiler stack-overflow prologues compare `SP` against
  `16(R14)` (g.stackguard0). This is what *decides* to call `runtime.morestack`.
- **TLS `[FS_BASE-8]`** — read by ABI0 wrappers (`get_tls; MOVQ g(CX),DI`) and, critically,
  by `runtime.morestack`'s `badmorestackg0` check (`MOVQ g(CX),DI; CMPQ DI, m_g0` →
  throw). So **the throw is driven by the TLS home.**
- ARM64 keeps g only in X28 — no second home to desync. (See `maz143_gspmm_arm64.go`.)

## 2. Save / restore today (amd64)
`SaveContextFromFrame` (kernel branch, `save_context_amd64.go:160-170`):
```
kernelTLSG := *per-frame TLS slot           // captured at common_exception_entry
if gLooksValid(kernelTLSG) {
    Context.TLSG = kernelTLSG                // + detector recordGSPMismatchKernel(...)
} else {
    Context.TLSG = frame[13]                 // R14 fallback — NO detector
}
Context.R14 = frame[13]                      // R14 captured verbatim
```
`load_context_and_iretq` (`exceptions_amd64.s:2231-2235`): writes **TLS-g = Context.TLSG**,
and R14 = Context.R14 (verbatim). So the restored TLS home = `gLooksValid(slot)?slot:R14`.

`gLooksValid(v) = v != 0 && v < 0x0000800000000000` (`save_context_amd64.go:88`).

**The mis-calibration:** kernel goroutine g's are HIGH canonical (`0xffff8001…`, the kernel
Go heap) — `gLooksValid` returns **false** for them. g0 is LOW (`0x45e1d480`, kernel BSS) —
`gLooksValid` returns **true**. Consequences:
- For every real kernel goroutine, the save takes the **else** branch → `Context.TLSG=R14`,
  and the detector (if-branch only) **never runs** → explains the absent `GSPMM`.
- The R14 fallback is right whenever R14==g (the common case), but in a window where R14
  is transiently g0 it sets `Context.TLSG=g0` → restored TLS-g=g0 → `morestack on g0`.

## 3. The g0-transition windows (from the ACTUAL Go 1.26.2 amd64 asm)
Verified by reading `runtime·morestack` / `runtime·systemstack` in
`/opt/homebrew/Cellar/go/1.26.2/libexec/src/runtime/asm_amd64.s`. The two windows want
OPPOSITE "true g" — this is the crux of the fix.

**`morestack` (asm_amd64.s:628-683) — NEVER writes R14.** Only the TLS home:
```
678: MOVQ BX, g(CX)                  // TLS-g := g0     ← only the TLS home is touched
679: MOVQ (g_sched+gobuf_sp)(BX), SP // SP := g0 stack
```
Window = (TLS-g=g0, R14=curg, SP=heap). `badmorestackg0` (line 643) reads g from **TLS**
(DI from line 631). Captured here → per-frame slot = g0 (low, valid) → **Case A / `GSPMM`**.

**`systemstack` entry (555-557):** TLS first, then R14, then SP:
```
555: MOVQ DX, g(CX)   // TLS-g := g0
556: MOVQ DX, R14     // R14   := g0
557: MOVQ ...,     SP // SP    := g0 stack
```
**`systemstack` exit (565-574):** restores TLS=curg (569) and SP (570) — **never restores
R14**; R14 stays the stale g0 from entry until the runtime next resyncs it (a WIDE window):
```
569: MOVQ AX, g(CX)              // TLS-g := curg
570: MOVQ (g_sched+gobuf_sp), SP // SP    := curg stack
     (… no MOVQ → R14 …)   574: RET
```
Window = (TLS-g=curg-high-VA, R14=g0). Captured here → slot=curg `!gLooksValid` → fallback
**R14=g0** → **Case B, no `GSPMM`** = the run 7/11 natural crash. The save's TLS-preference
was written for THIS window; `gLooksValid` rejecting high-VA curg defeats it.

- ABI0 wrappers, e.g. `runtime.usleep.abi0` (run 10): read g from TLS; run in kernel/
  scheduler context where g==g0 *legitimately* while on the exception stack — the detector's
  "g==g0 && SP not in g0.stack" flags this too (g0-on-exc-stack), which may be a benign
  over-flag vs. a real bug; secondary to Case B, needs its own look.

**`mcall` (asm_amd64.s:458-503) — R14-first** (the reverse of systemstack-entry), and HIGH
frequency (the runtime g→g0 switch for `gopark`/`goschedguarded`/`goexit` — every park/yield/
channel block):
```
489: MOVQ SI, R14     // R14   := g0   ← R14 FIRST
491: MOVQ R14, g(CX)  // TLS-g := g0   ← THEN TLS
492: MOVQ (g_sched+gobuf_sp)(R14), SP  // SP := g0 stack
```
Window 489-491 = (R14=g0, TLS-g=curg, SP=curg-stack) = **Case B**. Likely the *dominant*
Case-B source by frequency.

### The crux tension (why TLS-preference AND R14-fallback each break a window)
| Site | Order | Transient window | true g |
|---|---|---|---|
| `morestack` | TLS-only (no R14) | TLS=g0, R14=curg | R14 |
| `systemstack`-entry | TLS→R14→SP | TLS=g0, R14=curg | R14 |
| `systemstack`-exit | TLS=curg, R14 left stale (WIDE) | R14=g0, TLS=curg | TLS |
| `mcall`-entry | R14→TLS→SP | R14=g0, TLS=curg | R14(curg) |

### ENTRY vs EXIT — they want OPPOSITE remedies (the real design crux)
- **ENTRY windows** (morestack / mcall-entry / systemstack-entry): switching *TO* g0; one
  home already g0, SP still curg/heap; destination = g0. "Correct g to curg and resume"
  FIGHTS the in-progress switch (resume mid-`mcall` with g=curg breaks `fn(g)` on the g0
  stack). These are reachable ONLY by an async IRQ/PF (a blocking syscall is never issued
  mid-transition). **Right remedy: DEFER the capture** (let the transition reach g0).
- **EXIT wide window** (systemstack-exit): switching back *to* curg; TLS already curg, R14
  stale g0, SP = curg-stack; destination = curg. A blocking syscall (`usleep`/`futex`)
  issued during this WIDE window → `doContextSwitch` → Case B. **Right remedy: correct to
  curg** — which is exactly what fixing `gLooksValid` achieves (the existing TLS-preference
  fires instead of the R14 fallback). ⇒ The run 7/10 SYSCALL-path crashes are most likely
  THIS window, fixable by `gLooksValid` alone.

### Interaction to reconcile
Fixing `gLooksValid` to accept high-canonical g makes the `effG` guard see `effG=curg≠g0`
for the ENTRY windows → it STOPS deferring them. So `gLooksValid`-fix and the defer-guard
must be reconciled: the guard should defer on "a home==g0 AND SP off g0-stack" (the ENTRY
transient), independent of the `effG`/canonical question used for the EXIT save-correction.

Neither "trust TLS" nor "trust R14" is universally correct. But in BOTH windows exactly one
home is g0 and the other is a valid high-canonical curg ⇒ a candidate **unifying rule**:
**effective g = the non-g0 home** (when exactly one home is g0 and the other is a valid
canonical g); set BOTH `ctx.R14` and `ctx.TLSG` to it so the restore is *consistent* (no
independent reconstruction → no desync). Requires `gLooksValid` fixed to accept
high-canonical g's. ⚠️ OPEN: resuming mid-`morestack` (line 679) after forcing g=curg —
`newstack` runs on the g0 stack expecting `g==g0`, so "correcting" g there may break it.
systemstack-exit resume is safe (resume as curg); morestack-window resume must be proven
(or that window handled by deferring the *capture*, not correcting it).

## 4. The raw-save sites (capture points) and triggers
| Site | Function | Reached by | Guarded? | Deferrable? |
|---|---|---|---|---|
| `threads.go:4062` | `checkThreadPreemptionImpl` | timer / device-IRQ preemption | ✅ fix C + effG | yes (skip a tick) |
| `threads.go:3954` | `boostThread0ForPendingWork` | from 4036, downstream of the 4026 guard | ✅ (same frame) | n/a |
| `threads.go:4189` | `doContextSwitchImpl` | **SYSCALL-exit** + **PF-switch** | ❌ **none** | **no (thread blocked)** |

`doContextSwitchImpl` is invoked from `exceptions_amd64.s`:
- **:768 SYSCALL-exit** — when a syscall calls `SetSyscallSwitchTarget`: `nanosleep`(=usleep),
  `futex`, `epoll`, `iouring`, `write`, `clone`, `munmap`, `delegate`, `constraint_notify`,
  **and `runshepherd.go:95`**. ⇒ launching a shepherd switches through here.
- **:943 PF-switch** — #PF handler requests a switch (file-backed mmap fill). A #PF can
  land **mid-window**.

**Why crashes cluster at `RunShepherd start name=…`:** the launch path itself goes through
`DoContextSwitch`, while doing heavy ELF load (`loadSegment`→morestack), allocation (GC),
and blocking fs reads — all converging with g0-transition windows live.

## 5. Evidence (all preserved under ticket-active/MAZ-143 and MAZ-15/evidence)
- **run 11** (baseline `14f2eed3`) & **run 7** (GREEN, with my effG fix): `morestack on g0`,
  **no `GSPMM`**, `sp=0xffff800148acb1f0` (heap), at rachel launch. = Case B (slot high-VA
  `!gLooksValid`, R14=g0 → `Context.TLSG=R14=g0`).
- **run 10** (GREEN): **`GSPMM`** fired — `g=0x45e1d480` (g0, low-VA, valid slot = Case A),
  `sp=0xffffffff44123a50` (exception stack), `rip=0x45880863 = runtime.usleep.abi0`, then
  `schedule: holding locks`. The detector firing proves the **save happened** at an
  unguarded site (my effG guard at checkThreadPreemption would have skipped it) ⇒
  `doContextSwitchImpl`.
- GREEN batch: 2 CRASH / 1 TIMEOUT / 9 SUCCESS in ~12 — **no improvement** over the ~1/12
  baseline. The effG fix is correct but doesn't sit on the live path.

## 6. Fix options
**A. Consistent-g capture via the unifying rule (leading candidate).** Two coupled changes
at the SAVE (covers ALL sites, no "defer"):
  1. Fix `gLooksValid` to a proper *canonical* test — ACCEPT high-canonical kernel g's
     (`>= 0xFFFF800000000000`), reject only the non-canonical hole + zero.
  2. Apply the **unifying rule** (§3): when exactly one g-home is g0 and the other is a
     valid canonical g, the effective g is the **non-g0** home; set BOTH `ctx.R14` and
     `ctx.TLSG` to it. This fixes systemstack-exit (trust TLS=curg, overwrite stale R14=g0)
     AND morestack (trust R14=curg, overwrite transient TLS=g0) with one rule, and restores
     a *consistent* snapshot (the independent-reconstruction desync is the bug).
  - ⚠️ Open Q (morestack-resume safety): if captured in the morestack window and we force
    g=curg, resuming at line 679 enters `newstack` on the g0 stack while g=curg — may break
    `newstack` (it assumes g==g0). May need: for the morestack window specifically, prefer
    DEFER (don't capture) at the deferrable sites, and at the non-deferrable doContextSwitch
    site, ensure the captured RIP resumes BEFORE/cleanly. Trace `newstack` +
    `gosave_systemstack_switch` to settle. (systemstack-exit resume as curg is safe.)
  - Open Q (bringup garbage): is the garbage the R14-fallback was added for *non-canonical*
    (canonical test still rejects it — behavior preserved) or high-canonical-but-bogus
    (accepting it propagates a bad g)? Confirm against the early-boot/clone-child path.

**B. Per-path split.** PF-mid-window switch is deferrable (handle fault, let window
complete, retry) — guard it like the timer path. Blocking-syscall switch is not — handle
via the save-fix (A). Two targeted fixes matched to each path's semantics.

**C. Guard all raw-save sites** with a corrective action that isn't "defer" (the syscall
case can't defer). Likely degenerates into A anyway (you must make the *saved* state safe).

**D. Keep the effG predicate guard (partial, see §7)** as defense-in-depth on the timer
path regardless of which of A/B/C lands.

## 7. What's on the branch now (`fix/MAZ-143-effective-g`, uncommitted)
- `gspUnsafeKernelResume` extended: `effG = gLooksValid(slot)?slot:R14`, checked against g0
  (closes the checkThreadPreemption Case-B gap).
- `runGSPMismatchSelfTest` extended with a deterministic Case-B RED→GREEN assertion
  (RED on the raw-slot guard, GREEN with effG). Non-tautological (Cases 1/2 still pass).
- Ian: **keep** as partial hardening; build the real fix (A/B) on top.
- Note: if option A lands (save preserves real TLS-g), the effG guard's R14 fallback
  branch becomes mostly moot — revisit whether to simplify it then.

## 8. Open design questions for the session
1. Option A vs B as the primary fix. (A is more principled + uniform; B matches path
   semantics but is two fixes.)
2. `gLooksValid` canonical-test definition + proof that bringup garbage stays rejected.
3. Does verbatim dual-home restore actually complete the morestack/systemstack window on
   amd64? (The original code switched to TLS-preference *because* systemstack-exit leaves
   R14 stale — so "trust R14" and "trust TLS" each break a different window. A correct fix
   must handle BOTH systemstack-exit (trust TLS) AND morestack-window (trust… which?).)
4. The detector/selftest: extend to cover Case B + tag SYSCALL vs PF site, as the
   regression guard for whichever fix lands.
5. SMP: still CPU-0-scoped (MAZ-142).
6. Relationship to the separate rachel/shepherd `waitready` silent stall (MAZ-15 facet #2)
   — co-located at RunShepherd, no GSPHUNT/GSPMM; likely distinct, confirm.

## 9b. Code-analysis confirmations (2026-06-16) — one DISPROVEN, refines the fix
**(a) DISPROVEN — bringup garbage is HIGH-canonical, not non-canonical.** The clone path
`doContextSwitchImpl:4210` does `newThread.Context = oldThread.Context` (copies the whole
parent ctx incl. `TLSG`) then overrides only RAX/SP/**R14**/PState/CloneTLS — **never
`ctx.TLSG`**. `SetCloneTLS` sets `FSBase` only (`thread_context_amd64.go:150`);
`SetGRegister` sets `R14` only. So a clone child has `R14=child-g` (correct) but
`ctx.TLSG=parent's-g` — a HIGH-canonical wrong value. The R14 fallback exists *because*
R14 is the reliable home here. ⇒ a naive "accept high-canonical" `gLooksValid` fix would
make the save trust `TLSG=parent-g` → child runs with parent's g → GC/#GP corruption (the
exact failure `4233-4237` warns about). **The leading fix as first stated is unsafe.**
  - **Refinement (the real discriminator):** systemstack-exit is `R14==g0` (stale) with
    TLS=curg; clone-bringup is `R14=child-g ≠ g0` with TLS=parent-g. So the save must key
    on **`R14==g0`**, not canonical-ness:
    ```
    if frame.R14 == g0 && slot is valid-canonical && slot != g0:  ctx.TLSG = slot  // systemstack-exit → curg
    else:                                                          ctx.TLSG = frame.R14 // clone/morestack/normal
    ```
    Fixes systemstack-exit AND stays safe for clone-bringup (R14≠g0 → trust child's R14).
    No `gLooksValid` widening needed (keep it as a canonical sanity check on the slot).

**(b) CONFIRMED — PF path does not capture entry windows.** The PF-switch
(`mmap_pagefault.go:119`) fires only for **file-backed mmap fills**; the faulting access is
ordinary code touching a mapped file, never a runtime g0-transition (those touch only
kernel-mapped g/sched/stacks). So it can't capture morestack/mcall/systemstack-entry. If it
ever catches the *systemstack-exit wide window* (a faulting file access during it), the
save-correction (a) handles it. ⇒ **No PF-specific defer needed.**

**(c) SUPPORTED — run-10 is a separate "switch while holding runtime locks" bug.**
`ScanAccessedBits` runs OUTSIDE `schedulerLock` (`threads.go:1675` vs Lock at 1682);
`usleep.abi0` is the runtime lock-spin path. Chain: kernel runtime lock-spin on g0
(`m.locks>0`) → `usleep` SYSCALL → `doContextSwitch` checkpoints it → reschedule trips
`schedule: holding locks`. Its `GSPMM` (g=g0, exc-stack) is the detector **over-flagging
legitimate g0-on-exc-stack**. Distinct root from the dual-home morestack family; track
separately. (Also implies the detector's "g==g0 && SP∉g0.stack" predicate is too broad —
it should exclude SP-on-the-exception-stack, which is legit for g0.)

## 10. Lightweight probes to PROVE the claims (all detector-resident, low-perturbation)
Method constraint (§9): extend the EXISTING `recordGSPMismatchKernel` (baseline-cost,
already in `SaveContextFromFrame`) + a one-word site-tag global set at each raw-save caller.
NO new hot-path function calls (those perturbed timing and suppressed the crash).

- **Prove (a) — deterministic, zero-perturbation (preferred):** a selftest that runs the
  real clone setup (`SetupForCloneChild` + the `4203-4243` parent-regs copy) against
  `ctxTestThread` with a synthetic parent ctx whose `TLSG=parentG` (high-canonical) and
  `R14=parentG`, child `R14=childG`. Assert `child.Context.TLSG == parentG` (NOT childG) —
  proves the slot carries the parent's high-canonical g. Then assert the **refined save
  rule** picks `childG` (R14, since R14≠g0) — RED on a canonical-only rule, GREEN on the
  R14==g0 rule. Mirrors the existing `runGSPMismatchSelfTest` harness.
- **Prove (a) live (corroborate):** detector emits a `GTWO` marker when slot & R14 are both
  valid-canonical, ≠g0, and DISAGREE (clone-bringup signature) — expect it during shepherd
  launch with slot=parent-g, R14=child-g.
- **Prove (b):** add a one-word `currentRawSaveSite` global (1=checkThreadPreempt,
  2=boost, 3=doContextSwitch-SYSCALL, 4=doContextSwitch-PF) set at each caller; detector
  emits site+RIP. Over a launch batch, confirm site=4 (PF) NEVER carries an entry-window RIP
  (mcall/morestack/systemstack) — only file-access RIPs.
- **Prove (c):** when the detector fires with the g0+exc-stack shape at `rip=usleep.abi0`,
  also read `g0.m.locks` (reachable via the always-mapped g0) and emit it. `m.locks>0`
  proves the lock-spin/"holding locks" root and that the `GSPMM` is a benign over-flag.
- All four ride the one detector + one site-tag global; a single instrumented build + one
  launch batch yields the data for (a-live), (b), (c); the (a) selftest needs no batch.

## 11. PROBE BATCH RESULTS (N=15, instrumented) — the model is REFUTED for morestack
Tally: 11 SUCCESS, 4 CRASH, 0 stall. Crashes split into TWO distinct classes:
- **GSPMM/usleep class** (runs 1, 10, 11 — at *mail* launch, reached 5/8): `GPROBE site=3
  spc=K rip=runtime.usleep.abi0` + matching `GSPMM g=g0 sp=0xffffffff4412…` (exc stack).
  ⇒ `doContextSwitch` checkpointing g0-on-exc-stack during a `usleep` syscall. The detector
  AND the probe BOTH fire. This is confirmation (c) — almost certainly the "switch while
  holding runtime locks" bug (`schedule: holding locks`), a SEPARATE root.
- **morestack-on-g0 class** (run 6 — at *rachel* launch, 1/8; = the original run-11 natural
  crash): `morestack on g0, sp=0xffff8001…` (heap), **NO `GSPMM`, NO real `GPROBE`** anywhere
  before the crash (only sentinel selftest emits). ⇒ **NOT produced by any probe-visible
  `SaveContextFromFrame` capture.** A systemstack-exit-via-doContextSwitch save would have
  emitted `GPROBE` (the gate catches `r14==g0`); run-1 proved site-3 saves DO hit the probe.
  None fired. **So the dual-home save/restore model does NOT explain the actual
  morestack-on-g0 — the ticket's literal subject.**

### Consequence: two crash classes were being conflated
- The dual-home save/restore model (this whole doc, fix C, the effG fix, the two-part
  design) explains the **GSPMM/usleep** class — which is likely the *separate* "switch
  while holding locks" bug, NOT the ticket's morestack.
- The **morestack-on-g0** (run-6 / run-11 / the merged-fix natural repro) emits no
  detector/probe hit → its root is STILL OPEN and is NOT a probe-catchable kernel save.
  Likely needs morestack-ENTRY instrumentation or a gdb catch (the `tools/gdb/maz15-catch.gdb`
  technique) to see the actual prologue/caller and how thread-0's `loadSegment` reaches
  `morestack` with TLS-g already == g0 absent any captured switch.

### Open / blocked
- (a-live) real clone `GTWO` and (b) site-attribution sweep across all runs: NOT yet run —
  the Bash classifier was unavailable for the cross-run grep/addr2line.
- The save-side instrument is the WRONG tool for the morestack class; pivot to morestack-entry.

## 9. Note on method
Live per-save instrumentation perturbs timing (it suppressed the crash and inflated stalls
in the N=24 probe batch), so the proof here is **code analysis + the existing
detector's natural `GSPMM` + a deterministic synthetic selftest**, not a live-catch probe.
Any new instrument must be detector-resident (baseline-cost), not new hot-path calls.

## 12. ROOT CAUSE FOUND — the silent morestack class is the PLAIN exception_return path (2026-06-16 pm)
Static, end-to-end verified against the actual Go 1.26.2 asm + the kmazarin amd64 ELF.
The §11 morestack class (silent: no `GSPMM`/`GPROBE`) is **NOT** a `SaveContextFromFrame`
desync at all. It is the **no-preempt kernel exception return** restoring the TLS-g home
from the **stale frame R14** instead of the faithful per-frame captured slot.

### 12.1 What the throw actually means (corrects §3's premise)
`runtime·morestack` (asm_amd64.s) reads g **from TLS at its TOP** (`MOVQ g(CX),DI` →
`CMPQ DI,m_g0` → `badmorestackg0`) — *before* it sets TLS-g=g0 (line 678 is much later,
the "call newstack on g0 stack" step). So **the throw fires because TLS-g was ALREADY g0
when the overflowing prologue called morestack** — morestack did not set it. And
`badmorestackg0` prints `sp=g.sched.sp`, which morestack just set to **f's (the overflowing
caller's) SP** → `sp=0xffff8001…` is the heap SP of the function that overflowed, running
with TLS-g==g0. The crash dump confirms it is **goroutine 1** (the kernel main /
`KernelIdleLoop`, g=`0xffff8001480081e0`, high VA) on its **heap** stack, with TLS-g desynced
to g0. The "called from" traceback in the log is **empty** (the on-kernel traceback emits
nothing after the header) — so the log never named the caller; the "loadSegment" attribution
was an inference, now superseded by the mechanism below.

### 12.2 The mechanism (amd64 dual-home, verified in the ELF)
1. The compiler keeps g in two homes (R14 + TLS `[FS_BASE-8]`) and **TLS is authoritative**:
   after EVERY g-clobbering call it reloads R14 from TLS — disassembly of the built ELF shows
   `CALL runtime.systemstack.abi0` is *always* followed by `MOVQ FS:0xfffffff8, R14`. Same for
   `morestack`/`mcall`. So R14 is a cache refreshed from TLS; the stale-R14 window is **~1
   instruction** (between the clobbering call's return and that reload).
2. Go 1.26.2 `runtime·systemstack` **exit restores TLS-g=curg and SP=curg but NOT R14**
   (no `MOVQ AX,R14` in the "switch back" block) — so on return R14=g0 (stale) for that one
   instruction, while TLS-g is already curg (=g1).
3. A **timer (v48) or device (v32–47) IRQ** landing in that 1-instruction window: each handler
   borrows g0 (`exceptions_amd64.s` ~1396/1483: writes `[kmazarinFSBase-8]=g0`, `R14=g0`).
   `common_exception_entry` first captures the **live** TLS-g (=g1, correct) into THIS level's
   per-frame slot `excFrameTLSGOff` (the MAZ-139 capture).
4. **No-preempt return** (kernel CS, `svcDepth==0` but `NeedsThreadPreempt==0`, or
   `CheckKernelGoroutinePreempt` declines) → `irq_exception_return` falls through to
   `exception_return` → `eret_kernel_tls` (lines 1936–1943):
   ```
   eret_kernel_tls:
       MOVQ ·kmazarinFSBase(SB), AX
       MOVQ 104(BP), DX          // R14 from the saved GPR frame  ← THE BUG (104(BP)=R14)
       MOVQ DX, -8(AX)           // TLS-g := saved R14 = stale g0
   ```
   It restores TLS-g from the **saved frame R14 = stale g0**, NOT from the faithful per-frame
   `excFrameTLSGOff` slot (=g1). g1 now resumes at the interrupted `MOVQ FS:-8,R14` reload,
   which reads the **corrupted TLS-g=g0** → R14 stays g0 → the desync is self-perpetuating.
5. The next stack-growth prologue compares SP(heap) to `16(R14)=g0.stackguard0` → calls
   `morestack` → morestack reads TLS-g=g0 → `badmorestackg0` → throw.

### 12.3 Why it's invisible, and why every clue fits
- **No `SaveContextFromFrame`** runs on the no-preempt return path ⇒ no `GSPMM`/`GPROBE`/`GTWO`
  — exactly §11. (The preempting timer that lands in the window DOES go through
  `SaveContextFromFrame` and is the *separate* GSPMM/usleep class.)
- goroutine 1, m=0; sp=heap; g0 stack bounds in the message; clusters at shepherd launch
  (`loadSegment`/GC/copystack = densest clobbering-call traffic = most window-instructions);
  rare (~1/12 = the 1-instruction window × must-be-no-preempt × must-hit-a-growing-prologue
  before the next correct TLS-g write). All consistent.
- **amd64-only**: ARM64 has a single home (X28); `sync_return` restores `EXC_FRAME_X28`
  verbatim — there is no "frame R14 vs per-frame TLS-g" divergence to get wrong. (This is the
  exact x86/ARM divergence the memory flags as a maintenance hazard.)

### 12.4 Proposed fix (decision for Ian — not yet applied)
Make the plain return path faithful to the captured dual-home g, exactly as MAZ-135 did for
`load_context_and_iretq` and `YieldToReadyThread` (which already source TLS-g from the
captured g, not R14). In `eret_kernel_tls`, source the TLS-g write from the per-frame slot:
```
MOVQ const_excFrameTLSGOff(SP), DX   // faithful captured TLS-g (was: 104(BP) = stale R14)
MOVQ DX, -8(AX)
```
SP is still the lowered extension here (the MAZ-139 D2 canary just below uses
`const_excFrameCanaryOff(SP)`), so the slot is addressable. The slot = g1 in the window and
= g0 for genuine g0 execution, so it is correct in both cases (the current code only diverges
in the stale-R14 window). Fixing TLS-g alone suffices (the interrupted reload re-derives R14
from the corrected TLS-g); optionally also rewrite `104(BP)` from the same slot for full
dual-home consistency.

### 12.5 Proposed validation (mirror the existing harness)
- **Plain-path detector (baseline-cost, §9-compliant):** in `eret_kernel_tls`, before the
  write, compare `104(BP)` (frame R14) vs `excFrameTLSGOff(SP)` (captured slot); when they
  DISAGREE and one is g0, bump a counter + a one-shot marker (`EGTLS`). It fires on the window
  even when no crash follows → a live signal far more frequent than the crash.
- **Deterministic RED→GREEN selftest:** synthesize a kernel exception frame with
  `frame.R14=g0` but `excFrameTLSGOff=g1`; assert the OLD rule writes TLS-g=g0 (RED) and the
  new rule writes g1 (GREEN). Mirrors `runGSPMismatchSelfTest`.
- Then the standard 10–12-boot TCG/KVM batches + ARM64 HVF parity (no-op there).

### 12.7 STATUS — fix APPLIED 2026-06-17 (verify by TCG regression)
- **§12.4 fix applied** in `eret_kernel_tls` (exceptions_amd64.s): TLS-g now restored from
  `const_excFrameTLSGOff(SP)` (faithful captured slot), not `104(BP)` (stale frame R14).
  objdump-confirmed in the final ELF (write at the eret site sources DX from `0(SP)`, not
  `0x68(BP)`). The EGTLS detector is KEPT as a permanent tripwire (MAZ-139-D2 style): it now
  counts windows the fix CORRECTED (frame R14==g0 AND slot==curg) + one-shot "EGTLS" marker.
- **Validation path chosen (Ian): fix-first + TCG regression** (not the deterministic-selftest
  option). Live confirmation by detector-first was impractical: the bug is TCG-timing-specific —
  a **40/40 KVM batch was completely clean** (EGTLS=0, morestack=0, GSPMM=0), reaffirming the
  prior MAZ-143 10/10-clean KVM result. KVM never lands in the ~1-instruction window. So the
  regression is a local TCG batch watching: morestack=0 (want), GSPMM (separate bug, may
  persist), EGTLS>0 (positive evidence the fix is on the live path).
- Caveat: TCG regression on a ~1/12 bug is probabilistic — absence of morestack is corroboration,
  not proof; the static analysis + objdump-verified write-source change is the primary assurance.

### 12.8 ⛔ THE NAIVE §12.4 FIX IS A REGRESSION — confirmed 2026-06-17 (write the NON-g0 home, not "the slot")
The first TCG batch on the fixed build **deterministically crashed `morestack on g0` 7/7 boots,
EARLY** (right after `Jumping to kmazarin...`, before any selftest/shepherd), `sp=0xffff8001…`
(heap), `EGTLS=0`. Preserved: `/tmp/egtls-BUG-REPRODUCED-run1.log`. This is NOT the original
rare late bug — it is a NEW deterministic regression from the §12.4 write.
- **Root cause of the regression = the §3 ENTRY-vs-EXIT crux, which §12.4 violated.** "Always
  restore TLS-g from the per-frame slot" is wrong because the slot (the TLS home) is **g0 in the
  morestack/systemstack ENTRY windows** while R14 still holds curg (morestack sets TLS-g=g0 at
  asm_amd64.s:678 BEFORE any R14 change). Early kernel boot hammers those entry windows (constant
  stack growth) → an exception captured there has slot=g0, R14=curg → §12.4 writes TLS-g=g0 over a
  live goroutine → immediate `morestack on g0`. The ORIGINAL eret code wrote **R14**, which is
  correct for the entry windows — that's why stock boot worked. Neither home is universally right
  (R14 is g0 in the systemstack-EXIT window; the slot is g0 in the ENTRY windows) — exactly §3.
- **Empirical upside:** this PROVES the eret path really does process frequent (slot≠R14, one==g0)
  windows, and that **R14 is the correct source for the common (entry) case**. The exit window
  (R14=g0, slot=curg) is the only place the slot should win.
- **Corrected fix = the §9b refined rule applied to eret_kernel_tls** (NOT "always slot"):
  ```
  effG = frameR14                                    // default: correct for entry/normal/clone
  if frameR14 == g0 && gLooksValid(slot) && slot != g0:
      effG = slot                                    // systemstack-EXIT window only
  write TLS-g = effG
  ```
  entry window (slot=g0,R14=curg)→R14 ✓; exit window (R14=g0,slot=curg)→slot ✓; normal/g0/clone→R14 ✓.
  Mirrors SaveContextFromFrame's own gLooksValid+R14-fallback discipline. The EGTLS tripwire should
  count ONLY the exit-window corrections (R14==g0 && slot==curg), as before.
- STATUS: naive §12.4 write is in the tree and BROKEN (deterministic boot crash). Must replace with
  the refined rule (or revert). Do NOT ship the naive version.

### 12.9 REFINED RULE APPLIED 2026-06-17 — regression GONE; bug-hunt running
Replaced the naive write with the refined §9b rule in `eret_kernel_tls` (objdump-confirmed:
`MOVQ 0x68(BP),DX` default = R14; `JNE` to write when R14≠g0; slot loaded only on the R14==g0
branch, used iff slot≠0 && slot≠g0). **Regression confirmed gone:** first TCG boot now reaches
all 8 shepherds (`RunShepherd start name=protocol-http`) — no early `morestack on g0`. A large
N=48 TCG bug-hunt is running (halts + preserves the serial on the first `morestack on g0`;
EGTLS tripwire now counts ONLY genuine exit-window corrections). Outcome pending: morestack=0
across the batch = fix holds; any reproduction = preserved log for the next round.

### 12.10 ✅ VALIDATED 2026-06-17 — N=48 TCG bug-hunt: fix holds AND demonstrably corrects the window
`BUGHUNT TCG DONE — morestack=0/48 (FIX HELD)  EGTLS=2/48 (corrected windows)  GSPMM=2/48 (separate bug)  other=0/48  ok=45/48`
- **morestack=0/48** — the target bug never reproduced (≈4 expected over 48 if unfixed at the
  historical ~1/12; the regime that DID reproduce it on the old build).
- **EGTLS=2/48 — the decisive positive evidence** (runs 3, 28): the fix hit the exact
  systemstack-EXIT window (R14==g0, slot==curg) twice, restored `curg` instead of the stale `g0`,
  and **boot completed both times with no morestack**. Not mere absence-of-crash — the fix is
  observably on the live path and correcting it. This is the detector-first confirmation that
  TCG-rarity + KVM-cleanliness had previously denied us.
- **3 non-success = pre-existing MAZ-15 rachel-waitready stalls** (runs 2/9/12: `waitready`/`not
  ready yet`), intermittent on TCG, NOT crashes, NOT this fix. **0 regressions** (the naive
  version's deterministic early crash is gone; 45/48 full boots).
- **GSPMM=2/48** — the SEPARATE locks-held bug's tripwire fired (runs 15, 42) but those runs
  SUCCEEDED (no `schedule: holding locks` crash this batch); untouched by this fix, tracked apart.
- Conclusion: the refined §9b rule in `eret_kernel_tls` is the correct fix for the silent
  `morestack on g0`. amd64-only (ARM64 single-home X28, untouched). Ready for /ticket-pr.

### 12.6 Relationship to fix C / the effG branch / the GSPMM class
This root cause is **disjoint** from the `SaveContextFromFrame` dual-home story that fix C,
the merged guard, and the §7 effG branch address. Those target the **preempting** capture
(the GSPMM/usleep class, likely the "switch while holding locks" bug of §9c). The silent
morestack is the **plain no-preempt return** path. The §7 effG hardening can stay as
defense-in-depth on the preempt path, but it does NOT sit on this path — consistent with the
GREEN-batch "no improvement." The real fix for the ticket's literal subject is §12.4.
