# Progress Log

## Session: 2026-04-27 (late afternoon, Opus) — Option B run, H-T2 ruled out, Option A next

### What ran

1. **Baseline reproducibility check (no instrumentation)**: 3 × 90s ARM64 HVF, no clicks. 0 crashes. Boot-without-clicks does not reliably reproduce the mspan crash at 90s — the historical ~1-in-5 rate is at 180s.
2. **Option B implementation** (`612ed58`): new file `kmazarin/kmem/stale_pte_check.go` adds a diagnostic that walks every live shepherd's userspace page table on every `BuddyAllocTyped` of a user-side page type, looking for a leaf PTE still mapping the just-allocated PA. Telemetry counter (`stalePTEScans`/`stalePTEHits`) surfaced on the periodic `[status]` log line as `stale-pte: enabled scans hits`. Halt-on-first-hit via `klog.Criticalf("S!P!", ...)` mirroring `buddyDoubleFreeHalt`.
   - **nosplit discipline (caught at build time):** `BuddyAllocTyped` is `//go:nosplit` (called from exception-handler chains via `allocPTPage`). The walker must stay nosplit too, which means no `SerialPuts`/`SerialHex16`/`klog` calls from inside the walk (those nested chains bust the budget). Resolution: walker records first hit into package-level globals (`stalePTEHitSID`/`stalePTEHitVA`/`stalePTEHitLeafPA`) and returns; non-nosplit `stalePTEHalt` then formats via `klog.Criticalf` and freezes.
   - **Cost-of-scan triage (caught at first run):** unfiltered, the walker is so expensive that 180s of wall time only reaches fti launch (boot normally finishes in ~10s). Resolution: filter at the call site to user-side page types only (`PageUserText`/`PageUserROData`/`PageUserData`/`PageUserHeap`/`PageUserStack`/`PageFontCache`/`PageIPCBuffer`/`PageSharedIPC`). Kernel-type allocs vastly dominate boot churn and are not the suspected manifestation surface (the bug is mspan corruption in mail-app's heap = `PageUserHeap`).
3. **Option B diagnostic run (verifier on, 5 × 180s)**: B1 154s/183K-scans/0-hits/clean, B2 hung at rachel-boot, B3 114s/203K-scans/0-hits/**reproduced mspan crash** (`nelems=128 nalloc=31291`), B4 168s/181K-scans/0-hits/clean, B5 hung at rachel-boot.
4. **Baseline run (verifier off, 5 × 180s)**: 1 mspan crash (`nelems=341 nalloc=26649`), 1 early-boot hang at `[fs] reading /shepherd.elf`, 3 clean. Crash rate matches the historical 1/5.

### Findings

- **H-T2 (stale PTE in another shepherd's PT memory) ruled out.** Across ~370K user-side allocs scanned by the verifier — including in B3 which reproduced the crash — zero stale PTEs were detected. If a PTE had been left behind at munmap, the verifier would have caught it the moment that PA was reissued.
- **H-T1 (stale TLB) survives as the primary suspect.** The verifier walks PT memory, not TLB caches, so silence here is consistent with H-T1 — a PTE that has been cleared but whose translation still lives in the CPU's TLB cache will not show up in any PT walk.
- **Two side-issues parked:**
  - Rachel-boot hang in B2/B5 — exposed only with the verifier enabled. Could be verifier-induced (use-after-free of an L0 PA mid-teardown when the walker reads `proc.ShepherdListInUse[i]`/`PageTableL0PA` without locking) or pre-existing race exposed by latency. Did NOT reproduce in the verifier-off baseline runs.
  - Early-boot hang at `[fs] reading /shepherd.elf` (baseline run 1) — pre-existing, not introduced by Option B.

### Late-session: Option A applied, then reverted as a no-op

Added `TlbiVMALLE1 + DsbISH + IsbSY` at the end of `SyscallMunmap` (after the per-page unmap+release loop), mirroring `SyscallFreePages`. 5 × 180s ARM64 HVF: A1 163s clean, A2 boot-hang, A3 mspan crash (`nelems=100 nalloc=22790`), A4 161s clean, A5 mspan crash (`nelems=36 nalloc=44307`). 2/5 crashes vs baseline 1/5 — within noise, and the change is functionally a no-op since `UnmapUserPage`/`UnmapUserPageWithL0` already do per-page `tlbiVAE1IS` (inner-shareable broadcast — propagates to all CPUs in the IS domain). A trailing local `TlbiVMALLE1` after a broadcast doesn't add coverage.

**H-T1 (stale TLB) is weakened, not conclusively ruled out.** The cleanest H-T1 test would need ASID swap on munmap or a same-VA-access probe immediately post-`tlbiVAE1IS` to verify it actually invalidated. That's bigger work. For now, pivot to H-T3.

### Late-session: H-T3 Stage 1 sentinel-byte canary — H-T3a ruled out

New file `kmazarin/kmem/free_canary.go`. At `BuddyFreeTyped` (after `buddyInsertFree`) and at `buddyAddRange` (init-time bootstrap pool population), paint the freed block with `0xDEADBEEFDEADBEEF`, skipping the first 8 bytes used by the buddy free-list next-pointer. At `BuddyAllocTyped` (after `buddyRemoveFree`, before split), verify the pattern is intact byte-for-byte. Mismatch → capture pa/offset/expected/found into globals, halt via `klog.Criticalf("K!W!", ...)`. Telemetry on `[status]` line: `free-canary: enabled fills verifies hits`. Same nosplit discipline as Option B verifier.

**Bootstrap fix caught in smoke test:** the very first `BuddyAllocTyped` after `InitBuddyAllocator` was popping a bootstrap-populated pool page that had never been canary-filled, triggering a halt very early — before klog/serial were ready, so the halt path itself faulted (HSHEFAIL FAR=0x20). Fixed by also calling `fillFreeCanary` inside `buddyAddRange` so all pool pages start canary'd.

5 × 180s ARM64 HVF: C1 boot-hang, C2 short clean (~20s+, 346K verifies), C3 boot mspan crash (`nelems=1008 nalloc=23628` at mail-app initial cache rebalance, no clicks, fired before first periodic [status] print), C4 163s click-driven clean (162K fills / 362K verifies), C5 boot-hang. **0 canary hits across ~1.5M+ verify operations including the C3 crash run.**

**H-T3a ruled out.** The corrupting write is NOT happening between `BuddyFreeTyped` and the next `BuddyAllocTyped` of the same PA. The most likely surviving mechanism is **H-T3b: kernel writes AFTER the freed PA has been reissued and legitimately used** — the canary is overwritten by the new owner's first store, then a kernel path with a stale handle writes through. The canary cannot see this case.

The most plausible channel for H-T3b is the **linux page-cache writeback path** (`sysWrite` / `flushWriteBuf` / `updateCachedPages` / `flushAndCleanupPages` / `handleFlushReply`). Earlier session `12e5f0d` added a partial-munmap range guard there; a different variant may still exist.

### Next session

**Stage 2 read-only audit** of the linux page-cache mutation paths. Files to read:
- `maz/linux/page_cache.go`
- `maz/linux/syscalls.go` (sysWrite, sysFtruncate, sysMmapPageFlush)
- `maz/linux/main.go` (flushWriteBuf / updateCachedPages)
- `kmazarin/ksyscall/munmap.go` + `kmazarin/ksyscall/mmap_writeback.go`
- `kmazarin/ksyscall/cleanup.go`

Goal: produce a written audit (in `findings.md` or `task_plan.md`) of every cache-mutation path with yes/no on whether it can leave a stale entry pointing to a freed PA. Concrete fix proposals only after audit is reviewed by user.

### Stopping point

Five commits on the bug-B-family chain in this session: `3942ae8`, `8a64a92`, `4460c14`, `ca7f5f6`, `612ed58`, plus the upcoming free-canary commit. Option A reverted. Tracking docs current.

**Diagnostic toggles in tree:**
- `stalePTECheckEnabled` (`kmazarin/kmem/stale_pte_check.go`) — default false. Telemetry: `stale-pte: enabled scans hits` on [status] line.
- `freeCanaryEnabled` (`kmazarin/kmem/free_canary.go`) — default false. Telemetry: `free-canary: enabled fills verifies hits` on [status] line.

---

## Session: 2026-04-27 (afternoon, Opus) — font leak fix + bug B family localized to kernel

### Findings

The earlier `freeIndex is not valid` crash was attributed to "GC walks `*goFont.Face`'s slice into shared kernel pages and corrupts mspan". **Mechanism is wrong** — Go's `findObject` returns nil for non-arena pointers (shared pages live at 0x500000000000+, Go arenas at 0xC000000000); marking sets bits, doesn't write to mspan struct fields. `[versai:timing]` log prefix in `mancini/std/web_interactor.go` is just hardcoded text; the crashing process is **mail-app** (running mail.elf with louis14 + WebInteractor), not versai.

The actual bugs surface as a family of intermittent kernel hangs / mspan-corruption crashes in the page-management chain. Same root, multiple symptoms.

### Changes

- **A+B leak fix** (commit `3942ae8`): fontsvc `releaseTempSlot` calls `mem.FreePages` on the 4 MB cache pages (was a TODO). provider `CloseTemporaryFont` calls `mem.Munmap` on shared cache+fontData VAs (was just nil'ing the slot, leaving RefCount ≥ 1 forever). Verified: no Buddy OOM, slot reuse works, no errors from new call sites.

- **Caller-first close order + diagnostic checkpoints** (commit `8a64a92`): `provider.CloseTemporaryFont` now munmaps **before** sending the close IPC. Previous order had mail-app's `mem.Munmap` silently hanging after fontsvc's `releaseTempSlot` ran ahead of it. Added checkpoint logs at every step of close (mail-app and fontsvc sides) plus `[munmap:FREED]` in the kernel for shared-page-returns-to-buddy. PD_SHARED filter cuts kernel log volume ~100×.

### Runs

| Build | Symptom | Localization |
|-------|---------|--------------|
| Pre-A+B | 32 kind=1 populates, 27 560 `no free font slots`, no crash | Permanent slot exhaustion confirmed |
| A only (measure→OpenTemp) | 418 kind=2 populates × 4 MB = 1.67 GB → Buddy OOM | Fixed permanent leak, exposed `releaseTempSlot` TODO leak |
| A+B | Slots reuse, no OOM; `sweep increased allocation count` mspan corruption hit | Bug B family still present |
| A+B + caller-first | First close completed cleanly. Close 2 hung at fontsvc reply (`uring.Send`?). | Hang shifted to fontsvc-side reply path |
| A+B + checkpoints | First close hung in fontsvc `releaseTempSlot` (kernel `mem.FreePages` of 1024 pages). | Localized to `unmapUserPages` → `ReleasePageByPA` → `BuddyFreeTyped` |
| A+B + checkpoints (different boot) | mspan corruption at boot during cache rebalance, **no font activity** | Bug fires from any heavy alloc, not font-specific |

### Conclusion

A+B is a real fix and is committed. The remaining symptoms (mspan corruption crashes, kernel hangs) are a single pre-existing kernel bug in the page-free chain. `fontsvc.mem.FreePages(cacheBase, 1024)` is the highest-frequency way to hit it because it frees a large contiguous block including shared pages with active RefCount on the other shepherd, but the bug fires independent of font activity (boot-time cache rebalance produced an mspan crash with zero font calls).

### Next target

**Kernel-side instrumentation of `BuddyFreeTyped` + `releasePageByPA`**, per the new task_plan.md TOP OF STACK. Hypotheses H-K1..H-K4 there. Key suspect: concurrent decrement-and-free race in `releasePageByPA` between `SyscallMunmap` and `SyscallFreePages` on shared pages; if confirmed, fix is a lock around the RefCount manipulation or a double-free guard in `BuddyFreeTyped`.

### Late-session: kernel diagnostic pass (commit `ca7f5f6`)

Added bug-B-family kernel guards on the suspected double-free / RefCount-race path:

- `BuddyFreeTyped`: `buddyContainsPA` walk + `buddyDoubleFreeHalt` halt-with-marker if a PA is already on the free list before insertion.
- `releasePageByPA`: `[kmem:UNDERFLOW]` log when called on a page with `RefCount<=0`.
- `unmapUserPages`: `[unmapLoop] enter/progress/exit` for ≥64-page frees, with per-256-iteration `va`/`pa` checkpoints to localize a hang inside the loop.

**Diagnostic run result: all guards silent.** Crash fired at boot during mail-app's initial cache rebalance, `nelems=512 nalloc=41380`, with **zero font activity** in the run. 16 `[unmapLoop]` events fired (sid=21 shepherd-launch buffer cleanups, 1599 pages each, NOT IPC region). No `DOUBLE FREE`, no `kmem:UNDERFLOW`, no `[provider:close]`, no `[fontsvc:release]`.

That eliminates H-K1 (double-free), H-K2 (stale free-list), and the `releasePageByPA` race as the corrupting mechanism. The corruption is happening from a path that doesn't go through the kernel page-management RefCount accounting at all.

### Refined theory for next session

Three new hypotheses replacing H-K1..H-K4:

- **H-T1 (PRIMARY): stale TLB after `SyscallMunmap`.** That path doesn't `TlbiVMALLE1`+`DsbISH`+`IsbSY` after removing PTEs. `SyscallFreePages` does. A shepherd that munmapped a page can still write to it via stale TLB until something else flushes; if the PA is reallocated to mail-app's heap, that stale write corrupts the new owner.
- **H-T2: stale PTE in another shepherd's page table** — silent failure of `UnmapUserPageWithL0` leaves the entry mapped. Same write-through-stale-mapping outcome.
- **H-T3: direct kernel-side write into a freed page** — kernel scratch mapping, DMA, page-cache writeback hitting a recycled PA.

**Note on the `[unmapLoop]` data:** the 16 large unmaps that DID fire were all sid=21 ELF-load scratch buffer cleanups (1599-page user-region frees, no PD_SHARED). They go straight to buddy and could be reissued to mail-app's heap. If those frees leave stale TLB or PTE state, that's a candidate trigger for the boot-time crash.

### Stopping point (this session)

Four commits clean:
- `3942ae8` — A+B font leak fix.
- `8a64a92` — caller-first close order + diagnostic checkpoints.
- `4460c14` — tracking docs retargeted (freeIndex GC-walk hypothesis retracted, kernel page-free chain named as target).
- `ca7f5f6` — kernel double-free / underflow / loop-progress guards (silent in run, ruling out H-K1..H-K4).

louis14 `pkg/text/measure.go` change owned by louis14-side Claude; per memory rule we do not edit louis14 from mazzy.

Tracking files updated with focused next-session plan including Option A (TLB flush in `SyscallMunmap`, H-T1 fix-test) and Option B (stale-PTE detection at `BuddyAllocTyped` time, H-T1/T2/T4 diagnostic). See `task_plan.md` TOP OF STACK for run plan and files-to-touch tables.

---

## Session: 2026-04-27 — maildb console routing + freeIndex versai crash

### maildb console routing (Task 1 complete)

- Created `maz/maildb/mlog.go`: `mlogInfo` (console-only, drops if full) +
  `mlogErrorf` (console red + stdout fallback). Set via `mlogSetSink`.
- `mazarin/maildbio/maildbio.go`: added `StatusLine{Text string, IsError bool}`;
  changed `StatusCh`/`StatusChannel()` from `chan string` to `chan StatusLine`;
  bumped buffer 16→256.
- `maz/maildb/main.go`: `mlogSetSink` call added; `notifyStatus` closure
  removed; all `fmt.Printf`/`Println` swept to `mlogInfo`/`mlogErrorf` except
  two pre-sink startup lines.
- `maz/maildb/mbox_import.go`: throttled "Stored"/"Indexed" to every 50th
  message; "Storage done: N items stored" + "Indexing done: N items indexed"
  at end of each operation; removed `notify func(string)` params throughout.
- `maz/maildb/mail_handler.go`, `maz/maildb/collection.go`: swept all
  `fmt.Printf`/`Println` to `mlogInfo`/`mlogErrorf`.
- `maz/mail-ui/main.go`: drain updated to consume `StatusLine`; error lines
  rendered in `console.StderrColor()`.

### Run results (2 × 180s ARM64 HVF)

| # | Uptime | Result | Notes |
|---|--------|--------|-------|
| 1 | 180s   | clean  | mlog changes stable; mail console output correct; 2× body click ok |
| 2 | 180s   | CRASH  | `fatal error: freeIndex is not valid` in versai on 2nd message click |

Second run crash sequence:
```
[mail] body: 56448 bytes variant=1          ← second message click
[provider] populateSlot client=1..8 kind=1  ← temp font slot population
fatal error: freeIndex is not valid          ← versai heap corrupted
```

### Root cause analysis — `freeIndex is not valid` in versai

See `findings.md` for full details. Short form:

`populateSlot` (`mazarin/fontcache/provider.go:150`) calls
`goFont.ParseTTF(bytes.NewReader(fontData))` where `fontData` is
`unsafe.Slice` over a shared kernel page — not Go heap. The returned
`*goFont.Face` has internal slice headers pointing into those shared pages.
Go GC walks those as live heap pointers, reads raw font binary data as
pointer-sized values, and corrupts mspan structs (here: `freeIndex` field).

Secondary issues confirmed in source:
- `releaseTempSlot` (fontsvc/main.go:992) leaks cache pages (TODO comment confirms, no `mem.FreePages`)
- fontData dual-mapping: versai holds VA-A (its own AllocPagesSlice) AND
  VA-B (fontsvc reshares the same pages back), RefCount=3 per page, neither
  mapping ever unmapped

### Stopping point

`findings.md` updated with full 4-hypothesis analysis. `progress.md` and
`task_plan.md` updated (this entry). Fix work not yet started.

---

## Session: 2026-04-26 (Opus) — partial-munmap fix + ftruncate warning

Re-audit of the dual-mapping flow on the linux handler side identified two
concrete bugs that fit the GC-crash symptoms much better than Sonnet's
"live dual-mapping concurrent write" hypothesis. See `findings.md` for the
crash analysis.

### Bug B fix (proper) — range-aware `MmapPageFlush`

- `kmazarin/ksyscall/delegate.go`: added `FlushOffset` / `FlushLength` to
  `DelegateCallInfo`.
- `kmazarin/ksyscall/mmap_writeback.go`: `flushAndCleanupPages` takes
  `(startOffset, length uint64)`; passed in `Args[2]`/`Args[3]` of MmapPageFlush
  IPC (initial round + round-2+ resend).
- `kmazarin/ksyscall/munmap.go`: passes
  `fm.FileOffset + (alignedAddr - fm.StartVA)` and `alignedLength`. Warns
  `[munmap:PARTIAL]` when `alignedLength != fm.Length`.
- `WriteBackSharedMmapOnDeath`: passes `0, 0` (length=0 retains "all").
- `maz/linux/page_cache.go`: new `RemoveRangeOffsetBatch(sid, inum, startOffset,
  length, max)`.
- `maz/linux/syscalls.go::sysMmapPageFlush`: reads `Args[2]/Args[3]`; uses
  range-bounded `RemoveRangeOffsetBatch` when `length > 0`. Death cleanup
  (`fd == 0xFFFFFFFF` or unknown inum) keeps `RemoveAllBatch`.

### Bug A (warning, fix deferred)

`sysFtruncate` logs `[ftruncate:LEAK]` with the count of orphaned cache
entries. Full fix requires a new kernel-side "drop-these-handler-VAs" IPC.

### Instrumentation (silent on happy path)

- `[pageCache:OVERWRITE]` — `cache.Add` replaces an entry with a different VA.
- `[munmap:PARTIAL]` — covered above.

### Run results (2 × 120s ARM64 HVF)

| # | Uptime | Result | Notes |
|---|--------|--------|-------|
| 1 | 51s    | clean  | mail booted, body fetched, GC ran 256+ cycles |
| 2 | 103s+  | clean  | mail-ui body display, then unrelated `attr.Set slot=527` panic |

No GC crash. None of the new warnings fired. Pre-fix recurrence was ~1-in-5;
clean runs are consistent with either fix-correct or not-yet-triggered.

### Stopping point

Committed (`12e5f0d`). Ready for 5 × 180s discrimination runs.
