# Continuation prompt — bug B family Stage 4 (boot-only VA-collision sweep)

## Where we left off (2026-04-28)

Bug B family chase. Eight diagnostic rounds in. Recent state:
- GOGC=5 plumbed for mail-app (`config/startup.arm64.toml` + `maz/fs/main.go` + `__MAZZY_GCPERCENT` env). Verified active: `gc=6176` in 180s.
- VA-collision probe in `SyscallSharePages` (`kmazarin/ksyscall/mailbox.go`) implemented but gated behind `vaCollisionProbeEnabled` (default **false**) after click-induced `KERNEL EXIT GROUP` regression.
- One boot's data (E1, probe on, 132 entries): **all VAs in `0x500000xxxxxx` IPC region; none in mail-app's Go heap (`~0xC000000000+`).** Provisionally weakens VA-collision hypothesis at the SharePages layer. Need crash-run confirmation to fully rule out.

## Context (don't re-investigate)

| Hypothesis | Status |
|---|---|
| Buddy double-free / RefCount underflow | ruled out (`ca7f5f6`) |
| H-T2 stale PTE in PT memory | ruled out (`612ed58` Option B verifier) |
| H-T1 missing TLB flush at SyscallMunmap | ruled out as no-op |
| H-T3a write between BuddyFreeTyped and reuse | ruled out (`c4684ad` free-canary) |
| Suspect 5 `sysMmapPageFlush` fallback | ruled out (Stage 3 probe 0 fires) |
| Suspect 1 `[pageCache:OVERWRITE]` gap | ruled out (Stage 3 broadened probe 0 fires) |
| VA-collision at SharePages layer | **provisionally weakened**, needs crash run |

**Crash timing lock:** every historical crash fires at `[provider] populateSlot client=0 server=4 kind=1 cacheLen=49152 fontDataLen=53504` → `[mail] cache ready, initial rebalance first=-1 last=-1 vis=0` → `[mem:linux]` → mspan crash (or kernel EL1 abort variant).

## Project setup (always)

- Branch: `feature/mail-dumb`. Repo: `/Users/iansmith/mazzy`.
- Required env: `GOTOOLCHAIN=auto GO=/opt/homebrew/bin/go QEMU=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-aarch64 QEMU_X86_64=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-x86_64`.
- Build/run: `$GO tool task` for builds, `$GO tool task run-arm64-hvf TIMEOUT=N` to run (always `run-arm64-hvf`, never `run-arm64`), `$GO tool safe-serial-read /tmp/diplomat-arm64-serial.log` for log inspection.
- Logs: `/tmp/diplomat-arm64-serial.log`. Save per-run copies (`/tmp/F1-180s.log`, etc.) for post-hoc compare.

## Step 1 — Boot-only VA probe sweep (5×180s)

1. **Enable probe**: flip `vaCollisionProbeEnabled = true` in `kmazarin/ksyscall/mailbox.go:11`. Build.
2. **Run 5×180s sequentially**, **NO CLICKS** — render-time SharePages burst regresses the system to kernel exit_group. Save each log as `/tmp/F{1..5}-180s.log`.
3. **Per run, capture**:
   - Did `populateSlot client=0 server=4 kind=1 cacheLen=49152 fontDataLen=53504` fire? (yes = boot reached crash trigger point)
   - Did mspan crash fire (`fatal error: sweep increased allocation count`) or kernel EL1 abort?
   - Last 5 `[fontslot:VA]` lines before the crash. What VA range?

## Decision tree from Step 1

- **Crash repros AND last VAs are in `0x500000xxxxxx` (IPC region)** → VA-collision at SharePages layer fully ruled out. Move to Stage 4 Option 3: **VirtIO DMA target-PA audit**. Files: `kmazarin/kvirtio/block*.go`, descriptor-setup path. Question: does `maildb`'s BBolt reads use a DMA target buffer PA derived from a user VA (could be freed mid-request) or a stable kernel buffer?
- **Crash repros AND any VA falls in `~0xC000000000+`** → smoking gun. Stop and propose fix to user (kernel VA picker bug; check `MapPageInProcess` / `ksMapping` allocator).
- **0 crashes in 5 runs** → GOGC=5 may have shifted the timing. Run 5 more (10 total samples) before drawing conclusions.

## Step 2 — Audit `[maildb] send to SID 29 failed` flood (side issue)

Surfaced 2026-04-28 in E2 (151 lines during fti shepherd launch). Source: `maz/maildb/mail_handler.go:543 sendMailMsg`. uring.Send returns EAGAIN. Could be:
- Pre-existing race exposed by mlog routing change (was previously `fmt.Printf`, may have been silent in heavy boot logs).
- New ordering issue between maildb and freshly-launched mail-app.

Quick test: `git stash` the mlog-related changes (commit `8b91d34` would need revert) and re-run; if errors disappear → mlog change is the regression source. If errors persist → pre-existing.

Don't block Step 1 on this; just note frequency in Step 1's runs.

## Reminders / gotchas

- **Don't** edit `~/louis14` (read-only by default).
- **Don't** commit unless the user asks.
- **Don't** run `go build`/`go vet` directly — always `$GO tool task`.
- **Use** `klog.Logf`/`klog.Errf` for kernel logs, `fmt.Printf` for shepherd logs (or `mlogInfo`/`mlogErrorf` inside maildb).
- **Serial log safety**: never `cat`/`head`/`tail`/Read raw log; always `$GO tool safe-serial-read`.
- **Always** `run-arm64-hvf`, never `run-arm64` (TCG is 100× slower).
- **Crash grep**: `$GO tool safe-serial-read /tmp/F1-180s.log 2>&1 | grep -E "fatal error|sweep increased|HSHEFAIL|KERNEL EXIT|\\[fontslot:VA\\]"`
- **Don't enable VA probe with clicks** — it freezes the kernel after first click. Boot-only.

## Diagnostic toggles in tree (default off)

- `stalePTECheckEnabled` — `kmazarin/kmem/stale_pte_check.go`
- `freeCanaryEnabled` — `kmazarin/kmem/free_canary.go`
- `vaCollisionProbeEnabled` — `kmazarin/ksyscall/mailbox.go` (boot-only safe)

Status counters appear on the periodic `[status]` line.
