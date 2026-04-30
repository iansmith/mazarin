# Continuation prompt — Bug-B family (kernel runtime panic at/after `[mail] cache ready`)

## Status

The concurrent-boot-wedge that occupied the previous TOP-OF-STACK is **resolved** as of 2026-04-30 (FF-sweep 0/10). With it fixed, the only remaining intermittent failure on ARM64 HVF is the bug-B family of kernel runtime panics. **This is the next thing to chase.**

## What you should know going in

### Symptom (multiple signatures, all kernel-side runtime panics from a shepherd's goroutine 1)

Recent fires across EE / FF / cleanup-smoke sweeps showed three distinct panic strings:

1. **`fatal error: missing deferreturn`** (EE1, EE10, FF10 — most common in current sweeps)
   - Stack: `runtime.(*_panic).initOpenCodedDefers` → `runtime.(*_panic).nextFrame.func1` → `runtime.systemstack_switch` → `runtime.(*_panic).start`
   - Fires on the main goroutine of a launched shepherd
   - String "missing deferreturn" is the panic-during-panic message; the **original** panic cause is hidden under it

2. **`runtime.(*mheap).alloc` MemStat overflow** (cleanup-smoke 2026-04-30)
   - Stack: `runtime.(*mheap).alloc.func1` → `runtime.systemstack`
   - Decoded stack bytes: "MemStat overflow" — internal Go runtime invariant violation in heap accounting
   - Different shepherd / different goroutine each time

3. **mspan corruption signatures** (historical, pre-wedge-fix sessions): `freeIndex is not valid`, `sweep increased allocation count`, `nelems=341 nalloc=4024` — all from the GC sweep walking corrupted mspan structs in mail-app's Go heap

### Common feature

All three signatures fire after the kernel boot reaches `[mail] cache ready, initial rebalance first=-1 last=-1 vis=0` (the moment mail-app finishes loading its initial collection). Sometimes the crash is during the very next GC cycle on mail-app. Sometimes it's later. Sometimes it's in a different shepherd entirely.

The mspan signatures showed up consistently in mail-app's heap. The newer signatures (`missing deferreturn`, mheap overflow) appear in different shepherds. **It's plausible these are all the same underlying memory corruption manifesting in different goroutine state.** Or they might be separate. We don't know yet.

### Reproduction rate

In recent 60s ARM64 HVF sweeps (EE, FF, cleanup):
- ~1-3 in 10 runs hit one of these signatures
- The wedge fix may have *increased* observable bug-B rate by removing the wedge that previously masked it (some runs that used to wedge now reach the bug-B trigger point)

## What's been ruled out (don't redo this work)

Documented in detail in `task_plan.md` ARCHIVED section "bug B family":

- **Buddy double-free / RefCount underflow / unmapLoop hang** — `ca7f5f6` guards silent across all sweeps.
- **H-T2 stale PTE in another shepherd's PT memory** — `612ed58` Option B verifier, 5×180s, 184K-203K scans/run, 0 hits.
- **H-T1 (missing trailing TLB flush at SyscallMunmap)** — Option A reverted as a no-op.
- **H-T3a kernel write between BuddyFreeTyped and reuse** — `c4684ad` free-canary, 5×180s with crash repro confirmed, 0 canary hits.
- **Page-cache audit Stage 2 (read-only)** — protocol invariants I1-I5 hold in mainline.
- **Page-cache Suspect 5 (`sysMmapPageFlush !inumKnown` over-flush)** — Stage 3 probe, 0 fires across crash-eligible runs.
- **Page-cache Suspect 1 (`[pageCache:OVERWRITE]` same-VA gap)** — Stage 3 probe, 0 fires.

## Current best lead

**Hypothesis**: kernel maps font-cache pages (or other shared pages) at a VA that **collides with mail-app's Go heap region**. When mail-app's GC sweeps the span at that VA, it reads font-file bytes (or other shared content) instead of mspan struct data → wild `nelems`/`nalloc`/etc. → crash.

This was provisionally weakened by one boot-time data point: `va-probe: inIPC=132 outIPC=0 minVA=500000f9a000 maxVA=50000220b000` — all 132 SharePages target VAs landed in the IPC region (0x500000xxxxxx), none overlapping mail-app's Go heap (~0xC000000000+).

**But that data was from a CLEAN run.** We need probe data from a CRASH run to know whether the VA distribution differs. The probe is gated by `vaCollisionProbeEnabled` in `kmazarin/ksyscall/mailbox.go` (default false because the heavy SharePages traffic during click→body-render regresses the system to kernel exit_group).

## Next concrete step (read this first)

### Setup

```bash
export GOTOOLCHAIN=auto
export GO=/opt/homebrew/bin/go
export QEMU=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-aarch64
export QEMU_X86_64=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-x86_64
```

### Step 1: enable the VA-collision probe (boot-only)

Flip `vaCollisionProbeEnabled` to `true` in `kmazarin/ksyscall/mailbox.go`. This logs `[fontslot:VA] caller=N target=M va=X type=T` per `SyscallSharePages` call.

**Critical: do NOT click in the probe-enabled run.** Heavy click→body-render SharePages traffic regresses the system to kernel exit_group. Boot-only is safe.

### Step 2: run a crash-eligible boot-only sweep

```bash
$GO tool task
for i in $(seq 1 10); do
  $GO tool task run-arm64-hvf TIMEOUT=180 > /tmp/GG${i}.out 2>&1
  cp /tmp/diplomat-arm64-serial.log /tmp/GG${i}-180s.log
  $GO tool task stop-arm64
  sleep 2
done
```

(180s gives time to reach the post-cache-ready window where mspan-class crashes fire. 60s sweeps have lower bug-B rate because some don't reach the trigger.)

### Step 3: filter for crash runs and probe data

```bash
for i in $(seq 1 10); do
  hit=$($GO tool safe-serial-read /tmp/GG${i}-180s.log 2>/dev/null \
        | grep -cE "missing deferreturn|MemStat overflow|freeIndex|sweep increased|KERNEL EXIT GROUP")
  echo "GG${i} crash-hits=${hit}"
done
```

For each crash run (`hit > 0`):

```bash
$GO tool safe-serial-read /tmp/GGN-180s.log | grep "\[fontslot:VA\]" | head -20
$GO tool safe-serial-read /tmp/GGN-180s.log | tail -30   # crash signature
```

### Step 4: decision tree

- **If a crash run shows ≥1 `[fontslot:VA] va=` outside `0x500000xxxxxx`** (i.e. inside Go heap range or anywhere unexpected) → **VA-collision is the bug**. Fix: change `SyscallSharePages` target VA picker to never pick from a range that overlaps Go's heap. Audit `pickShareTargetVA` (or equivalent) in `kmazarin/ksyscall/mailbox.go`.

- **If a crash run shows all VAs in `0x500000xxxxxx`** → VA-collision is fully ruled out. Move to:
  - **Option B (VirtIO DMA target-PA audit)**: maildb reads BBolt pages from disk via VirtIO block. If the block driver's DMA descriptor references a PA that was freed and reissued to mail-app as heap, the DMA write would corrupt it. Audit `kmazarin/kvirtio/block*.go` for DMA target buffer lifecycle. Question: is the DMA target PA derived from a user-mapped VA (which could be freed mid-request), or from a stable kernel-allocated buffer?
  - **Option C (heap-corruption forensics)**: add a small patch to `runtime.(*sweepLocked).sweep` (or a pre-sweep hook) that dumps the raw mspan bytes when corruption is detected. The byte values should identify the source — font-file magic? PTE values? IPC header fields? maildb badger pages? Add overlay in `runtime-patches/` not in-place GOROOT edits (per CLAUDE.md).

## Reminders / non-negotiables

- **NEVER set `asyncpreemptoff=1`** (CLAUDE.md mandatory rule). The previous "freeIndex is not valid" → "sweep increased allocation count" history involved wrong fixes that turned this off; do not repeat.
- **NEVER set `GOGC=off`** or use `runtime.GC()` to "fix" symptoms.
- **GODEBUG=gccheckmark=1** must stay set on both kernel and shepherds.
- **Bug-B can fire before `[status]`** prints, so some kernel telemetry may be missing in crash logs. The pre-existing instrumentation (free-canary, stale-pte-check) can be re-enabled if needed; both default off.
- **Don't depend on bug-B being fixed for unrelated work** until the VA-collision question is settled.
- **The wedge is fixed** — don't roll back ext2 RWMutex, asyncBlockDev per-chunk lock, or fs delegate worker pool. Commits: `a1a4ef8`, `082b164`, `90be746`, `f5c09f8`.

## Pointers

- **Investigation history**: `task_plan.md` ARCHIVED "bug B family" section — full audit log of all hypotheses tested. Read this before starting.
- **Kernel-side instrumentation toggles**: `kmazarin/kmem/stale_pte_check.go`, `kmazarin/kmem/free_canary.go`, `kmazarin/ksyscall/mailbox.go` (VA probe).
- **Bug memory**: `memory/MEMORY.md` Active Bugs section + `bug_attr_init_crash.md` (FIXED, kept for context).
- **The `time.NewTicker` / `time.After` SIGSEGV** in .maz init (memory file `maz_time_ticker_bug.md`) is a SEPARATE pitfall that masks as `internal/godebug.(*Setting).Value` SIGSEGV. The `missing deferreturn` panic looks similar but isn't from that — it has a different stack signature (panic.start vs time.syncTimer). Don't confuse them.

## Why this is worth solving now

- It's the only consistent failure mode left on ARM64 HVF.
- It blocks any meaningful click-test sweep (which the user has queued as the next phase) — clicks would amplify whatever's corrupting memory.
- The wedge fix may have made bug-B more reproducible by removing the prior masking effect.

## Done when

- 30 boots in a row (e.g. 3×10 sweeps at 180s each) without ANY of the bug-B signatures.
- Mechanism understood and documented in `memory/`.
- The fix is targeted (e.g., specific VA range exclusion or DMA buffer lifecycle change), not a workaround that disables a feature.
