# Continuation prompt — bug B family Stage 4 (GOGC=5 + VA-collision probe)

## What this is

A self-contained briefing for the next Claude Code session on the mazzy project. We are chasing an intermittent kernel bug that corrupts an mspan struct field in mail-app's heap. Seven diagnostic rounds have ruled out buddy double-free, stale PTEs, stale TLB flush, the free→reuse window, and all page-cache paths (Suspects 5 and 1). The strongest remaining signal is a **crash timing lock**: every crash fires at exactly the same point — right after `populateSlot client=0 server=4 kind=1 cacheLen=49152 fontDataLen=53504` and the subsequent `initial rebalance first=-1 last=-1 vis=0`. The active hypothesis is a **VA collision**: the kernel maps 12 font-cache pages at a VA in mail-app's address space that overlaps Go's heap region.

## Project context

- **Repo:** `/Users/iansmith/mazzy`, branch `feature/mail-dumb`.
- **Build/run:** See `CLAUDE.md`. Key: `$GO tool task` for builds, `$GO tool task run-arm64-hvf TIMEOUT=N` for runs (always `run-arm64-hvf`, never `run-arm64`), `$GO tool safe-serial-read /tmp/diplomat-arm64-serial.log` for log inspection. Required env every Bash call: `GOTOOLCHAIN=auto GO=/opt/homebrew/bin/go QEMU=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-aarch64 QEMU_X86_64=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-x86_64`.
- **Tracking docs:** `task_plan.md` (TOP OF STACK), `findings.md` (Stage 3 results + Stage 4 plan), `progress.md` (latest entry at top).
- **Last bug-B commit:** `c4684ad` (free-canary, default off).
- **Uncommitted work in tree:** Stage 3 probe edits (`maz/linux/page_cache.go:78`, `maz/linux/syscalls.go` `sysMmapPageFlush`) — diagnostic-only, leave in place. Unrelated maildb-console-routing changes (`maz/maildb/*`, `maz/mail-ui/main.go`, `mazarin/maildbio/maildbio.go`, `maz/maildb/mlog.go`, `maildb_console_plan.md`) — leave alone.

## What's been ruled out (don't re-investigate)

| Hypothesis | Status |
|------------|--------|
| Buddy double-free / RefCount underflow | ruled out (`ca7f5f6` guards silent) |
| H-T2 stale PTE in PT memory | ruled out (`612ed58` Option B verifier 0 hits across 5 × 180s) |
| H-T1 missing TLB flush at SyscallMunmap | ruled out as no-op (per-page `tlbiVAE1IS` already broadcasts) |
| H-T3a write between BuddyFreeTyped and reuse | ruled out (`c4684ad` free-canary ~1.5M+ verifies, 0 hits) |
| Suspect 5 `sysMmapPageFlush` fallback over-flush | ruled out (Stage 3 `[pageCache:FALLBACK_ALLFDS]` probe 0 fires across 6 crash-eligible runs including 2 crash repros) |
| Suspect 1 `[pageCache:OVERWRITE]` same-VA gap | ruled out (Stage 3 broadened probe 0 fires same runs) |

## Crash timing lock (primary signal)

Every crash across all seven rounds fires at:
```
[provider] populateSlot client=0 server=4 kind=1 cacheLen=49152 fontDataLen=53504
[mail] cache ready, initial rebalance first=-1 last=-1 vis=0
[mem:linux] heap=...
<crash: sweep increased allocation count  OR  kernel EL1 write-fault FAR=0x9000000>
```
No crash has ever fired before this point. No probe has ever fired before a crash.

## What to do (in order)

### Step 1 — GOGC=5 verification and run

Check `kmazarin/ksyscall/launch.go` to see what env vars mail.elf receives. If `GOGC` is not set to `"5"` for the mail shepherd, add it (match the pattern already used for other shepherds). Then run 5 × 180s ARM64 HVF and check whether the crash still locks to the same program point.

**Goal:** confirm the corruption window is bounded to the `populateSlot server=4` → initial-rebalance moment, not a delayed discovery of an earlier event. If timing lock holds under aggressive GC → proceed to Step 2. If crashes appear at different points → note new timing and report before proceeding.

### Step 2 — VA-collision probe (Stage 4 Option 1)

Add a diagnostic log in the kernel's map-user-pages path that prints the VA being mapped into mail-app when font-cache pages are established for `server=4`. The goal is to compare that VA against mail-app's Go heap range (`~0xC000000000+` on ARM64).

Candidate locations: `kmazarin/ksyscall/map_shared.go` (SharePages / MapUserConstraintPagesWithL0), or the demand-fault handler that maps file-backed pages into mail-app. Print: `[fontslot:VA] sid=N va=X pages=K type=T` at minimum. Use `klog.Logf` (kernel code).

After applying the probe: build + smoke run. Inspect the logged VAs. If any VA falls in the Go heap range for ARM64, that is the smoking gun — stop and report before changing anything else.

### Step 3 (if Steps 1–2 come back clean) — report to user

If GOGC=5 changes the crash timing AND/OR the VA probe shows no collision, report the new data and wait for direction. Do not proceed to the lower-priority pivots (VirtIO DMA audit, heap forensics, H-T1' proper test) without discussing with the user first.

## Decision tree from Step 2

- **VA in Go heap range fires + crash reproduces** → VA collision confirmed. Stop; propose fix candidates to user (e.g. ensure font-slot VA is allocated from a non-heap region; check VA picker in `MapUserConstraintPagesWithL0` for off-by-one or wrong base).
- **VA probe shows sane VAs but crash still fires** → VA collision disproven. Move to Step 3 (report).
- **No crash in 5 × 180s after GOGC=5** → lower-than-expected crash rate; run another 5 × 180s before concluding.

## Reminders

- **Don't** edit `~/louis14` (read-only by default).
- **Don't** commit unless the user asks.
- **Don't** run `go build` / `go vet` directly — always `$GO tool task`.
- **Use** `klog.Logf`/`klog.Errf` for kernel logs, `fmt.Printf` for shepherd logs.
- **Serial log safety:** never `cat`/`head`/`tail`/Read the raw log; always `$GO tool safe-serial-read`.
- **Always** use `run-arm64-hvf`, never `run-arm64` (TCG is 100× slower).
- **Crash grep:** `$GO tool safe-serial-read /tmp/diplomat-arm64-serial.log 2>&1 | grep -E "fatal error|sweep increased|HSHEFAIL|!!FAIL"`
- **Probe grep:** `$GO tool safe-serial-read /tmp/... 2>&1 | grep -E "pageCache:FALLBACK_ALLFDS|pageCache:DRAIN|pageCache:OVERWRITE|fontslot:VA"`
- **Diagnostic toggles in tree (default off):** `stalePTECheckEnabled` (`kmazarin/kmem/stale_pte_check.go`), `freeCanaryEnabled` (`kmazarin/kmem/free_canary.go`).

Good luck.
