# Progress Log

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
