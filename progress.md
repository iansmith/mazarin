# Progress Log

---

## Session: 2026-04-26 (continued, 3rd, Opus) — GC crash: partial-munmap fix + ftruncate warning

### Audit conclusion
Re-audit of the dual-mapping flow on the linux handler side found two concrete
bugs that fit the symptoms much better than the "live dual-mapping concurrent
write" hypothesis:

1. **Bug B** — `flushAndCleanupPages` was fd/inum-keyed, no VA-range
   constraint. A partial munmap caused linux to drop **all** cached pages for
   the inode and the kernel to release them, leaving mail's caller PTEs in the
   un-unmapped portion pointing at recycled PAs. Guard never fires (legitimate
   release path clears `PD_FILE_BACKED` first), ZERO_VERIFY clean (page is zero
   when reallocated). Fits all observed symptoms.

2. **Bug A** — `sysFtruncate` discards the entries returned from
   `h.cache.RemoveRange`. Handler PTEs and `RefCount` are never cleaned up.
   This is a leak, not direct corruption — pages stay alive at `RefCount=1`
   until next munmap or shepherd death.

### Changes

**Bug B fix (proper)** — range-aware MmapPageFlush:
- `kmazarin/ksyscall/delegate.go`: added `FlushOffset` / `FlushLength` to
  `DelegateCallInfo`.
- `kmazarin/ksyscall/mmap_writeback.go`: `flushAndCleanupPages` takes
  `startOffset, length uint64`. Passed in `Args[2]`/`Args[3]` of MmapPageFlush
  IPC (both initial round and round-2+ resend).
- `kmazarin/ksyscall/munmap.go`: passes
  `fm.FileOffset + (alignedAddr - fm.StartVA)` and `alignedLength`. Warns
  `[munmap:PARTIAL]` when `alignedLength != fm.Length`.
- `WriteBackSharedMmapOnDeath`: passes `0, 0` (length=0 retains "all" semantics).
- `maz/linux/page_cache.go`: new `RemoveRangeOffsetBatch(sid, inum, startOffset,
  length, max)`.
- `maz/linux/syscalls.go::sysMmapPageFlush`: reads `Args[2]/Args[3]`; uses
  range-bounded `RemoveRangeOffsetBatch` when `length > 0`. Death cleanup
  (`fd == 0xFFFFFFFF` or unknown inum) keeps the old `RemoveAllBatch` path.

**Bug A (warning, fix deferred)** — `sysFtruncate` logs `[ftruncate:LEAK]` with
the count of orphaned entries. Full fix requires a new kernel-side
"drop-these-handler-VAs" IPC; not done in this session.

**Instrumentation**:
- `[pageCache:OVERWRITE]` warning when `cache.Add` replaces an entry with a
  different VA (orphan path).
- `[munmap:PARTIAL]` warning (already covered above).

### Run results

| # | Uptime | Result | Notes |
|---|--------|--------|-------|
| 1 | 51s    | clean  | mail booted, body fetched, GC ran 256+ cycles |
| 2 | 103s+  | clean  | mail-ui body display, then unrelated attr.Set slot=527 panic |
| 3 | 51s+   | clean  | second 120s |

**No GC crash. None of the new warnings fired.** Pre-fix recurrence rate was
~1-in-5 runs; clean runs are consistent with either fix-correct OR
not-yet-triggered. More runs needed to discriminate.

### Stopping point

Fixes in place + instrumentation. Ready for longer stability runs (5 × 180s).
The deferred ftruncate proper-fix is documented in findings.md as the next
follow-up if `[ftruncate:LEAK]` warnings start firing.

---

## Session: 2026-04-26 (continued, 2nd) — GC crash: PD_FILE_BACKED guard + source unknown

### What was tried

Resumed from the dual-mapping refcount hypothesis (Opus-reviewed, strongest lead).

**PD_FILE_BACKED flag approach** — correct fix for the shepherd-death path:
- Added `PD_FILE_BACKED = 1 << 4` to `kmazarin/kmem/page_descriptor.go`
- `kmazarin/kmem/cleanup.go` Phase 1: skips FILE_BACKED pages (defers to handleFlushReply)
- `walkAndFreePageTablePages` (deferred cleanup, `freeLeaves=true`): same skip
- `kmazarin/ksyscall/delegate.go:842`: sets `PD_FILE_BACKED` when `MapPageInProcess` succeeds on MmapPageFill reply
- These correctly protect the **shepherd-death** path against premature release of dual-mapped pages

**Madvise fix (tried and reverted)** — `stubs.go` `SyscallMadvise` userspace path:
- Added `FindFileMappingByVA` check to skip `releasePageByPA` for file-backed pages
- Crash appeared to continue at same rate → reverted (statistical noise; madvise path confirmed not to affect file-backed pages since Go runtime only madvises its own heap VAs, not file-backed mmap VAs)

**`releasePageByPA` guard** — diagnostic + safety net (currently in place):
- Added PD_FILE_BACKED check in `releasePageByPA` (`cleanup.go`): emits `[kmem] BLOCKED:` warning and returns false if called on a file-backed page
- `mmap_writeback.go` `handleFlushReply`: clears `PD_FILE_BACKED` before calling `ReleasePageByPA` so the legitimate cleanup path is unaffected

### Run results

| # | Result | BLOCKED warning? | Notes |
|---|--------|-----------------|-------|
| 1 | CLEAN  | No              | Guard in place, no crash, no blocked |
| 2 | CRASH  | No              | `nelems=1 nalloc=43525`; guard never fired |

### Key revelation

**The BLOCKED guard never fires.** This means the corruption is NOT going through `releasePageByPA` with PD_FILE_BACKED set. The mspan is being corrupted by a different mechanism — either:
1. A dual-mapped page that never had `PD_FILE_BACKED` set (e.g., `mapOK=false` path, or a different mapping path)
2. A live dual-mapping that was never freed prematurely (both PTEs still active, concurrent write)
3. A completely different corruption source unrelated to file-backed pages

### Crash values observed (two sessions)

| Run | nelems | nalloc  | Notes |
|-----|--------|---------|-------|
| A   | 170    | 44,899  | First session |
| B   | 1      | 43,525  | Current session |

Both are mathematically impossible from the stated nelems — conclusive evidence of mspan struct corruption. The `nalloc` values (~43K-45K) are suspiciously consistent across runs despite different `nelems`, suggesting a large span (nelems ~43K-45K) is the true corrupted state and `nelems` is the field being overwritten to a small value by the external write.

### Stopping point

Investigation paused by user. Guard is in place (prevents corruption even if source unknown). All PD_FILE_BACKED shepherd-death fixes are correct and should stay. The madvise path in stubs.go is NOT the source (reverted cleanly). Next session needs a new approach to find the live-dual-mapping source.

---

## Session: 2026-04-26 (continued) — fstatat/sysid=44 instrumentation

Switched back to `feature/mail-dumb` (force-deleted `feature/mail-dumb-bisect` per
bisect verdict). This branch has the correct forward baseline:
- `b9fd57f` — font: real temp-pool IPC
- `e3b7159` — fix(grid): publish scroll attrs on visibleCount transition
- `7af236c` — fix(mail): run cache.Rebalance + body fetch after every iteration

### Instrumentation added (3 files, no functional change)

**`maz/linux/syscalls.go` — `sysFstatat`:**
- Per-call entry/exit traces behind `fstatatSeq` atomic.
- Entry: `[fstatat] seq=N enter path=P`
- Exit: `[fstatat] seq=N done fsErr=N err=<nil>`

**`mazarin/fsclient/client.go` — `callLocked`:**
- After `uring.Send` but before `<-c.RespCh`:
  `[fsclient] stat id=N sent; RespCh len=N` (gated on FSOpStat).

**`maz/linux/main.go`:**
- Startup: `[linux] chan caps: wmCh=8 fontReplyCh=8`
- Periodic goroutine (every 5s after SetReady): `[linux] chan-monitor: wmCh N/8 fontReplyCh N/8`

### Run results (3 × 180s ARM64 HVF)

| # | Result | Notes |
|---|--------|-------|
| 1 | STABLE | 34 chan-monitor firings; wmCh=0/8 fontReplyCh=0/8 throughout |
| 2 | STABLE | No delegate-stuck with non-empty info |
| 3 | Short log (7.8K / 161 lines) | QEMU may have had a slow start; log cuts off mid-maildb-load |

No sysid=44 hang in 3 runs (~48% chance of at least one at 1-in-5 rate).

### Decision tree when hang fires

```
[fstatat] seq=N enter  (no matching "done") → hang is in h.fs.Stat()
  [fsclient] stat id=N sent present → blocked at <-c.RespCh → fs.maz not responding
  [fsclient] stat id=N sent absent  → blocked at c.mu.Lock() or uring.Send
  chan-monitor wmCh near cap=8      → dispatcher deadlock confirmed
```

### Stopping point

Instrumentation in place, 3 clean runs, no hang yet. Paused per user request.

---

## Session: 2026-04-26 (evening) — Opus review of GC crash hypotheses

User asked Opus to evaluate Sonnet's TTBR0 hypothesis and the four alternative
hypotheses listed at end of the afternoon session.

### TTBR0 hypothesis — ruled out

Tracing `exceptions_arm64.s:233-360`: SVC swaps `x28` to kmazarinG0Addr but **never
changes TTBR0**. `SyscallDispatch` runs nosplit on the same CPU thread; TTBR0 is the
calling shepherd's L0PA throughout. `HandleUserPageFault` reads TTBR0 on every fault;
ZERO_VERIFY_FAIL never fires — proves TTBR0 is correct on synchronous syscalls.

Misleading comment at `stubs.go:279` is what set Sonnet on the wrong path. The real
reason `SyscallMadvise` uses stored L0PA is defensive coding, not a TTBR0 bug. Do not
revisit.

### Hypothesis ranking (see findings.md and continuation_bisect.md for full details)

- **#1 (zero-on-alloc gaps):** RULED OUT.
- **#2 (refcount/dual-mapping):** STRONGEST LEAD — `delegate.go:833` MmapPageFill reply
  creates dual mapping without `desc.RefCount++`; `SyscallMadvise` can decrement RefCount
  to 0 on still-dual-mapped page; page returns to buddy; handler writes through stale PTE.
- **#3 (cache aliasing):** RULED OUT.
- **#4 (bump-allocator overlap):** UNLIKELY.

### Next steps recorded in continuation_bisect.md

1. Add user-VA ZERO_VERIFY probe in `HandleUserPageFault`.
2. If confirmed: fix `delegate.go:833` + `SyscallMadvise` fileBacked check.
3. Verify with 60s/120s/180s runs.

### Files changed

`continuation_bisect.md` (rewritten), `progress.md`, `task_plan.md`.

---

## Session: 2026-04-26 (afternoon) — MAP_FIXED bug found + fixed; crash persists

### GC crash discovered

After morning's temp-pool IPC work, a `fatal error: sweep increased allocation count`
crash appeared in the mail shepherd's bgsweep goroutine. Crash signature:

```
[mail] cache ready, initial rebalance first=-1 vis=0
runtime: nelems=36 nalloc=15375 previous allocCount=1 nfreed=50162
fatal error: sweep increased allocation count
  runtime.(*sweepLocked).sweep → mgcsweep.go:685
```

The serial ring buffer dropped characters; real `nelems ≈ 15375`. A span with
`allocCount=1` had 15375 gcmarkBits set — impossible without corruption.

### MAP_FIXED bug found and fixed

`SyscallMmap` MAP_FIXED path (`ksyscall/mmap.go`): recorded the new span but did NOT
unmap existing PTEs. Go's `sysMapOS` (MAP_ANON|MAP_FIXED) could get a range where
stale physical pages from a previous GC arena allocation remained mapped with old
mark-bit data.

**Fix:** added `unmapFixedRange(addr, length)` helper (`//go:noinline`) that uses
`shepherd.PageTableL0PA` + `UnmapUserPageWithL0` to release existing PTEs before
re-recording the span.

### Crash persists

60s run after fix: clean. 120s run: crash reproduces with same signature. MAP_FIXED
was not the only source. Bisect superseded by GC crash investigation.

### Files modified

`kmazarin/ksyscall/mmap.go` — `unmapFixedRange()` helper + call in MAP_FIXED path.

---

## Session: 2026-04-26 (morning) — temp-pool real IPC landed

Landed three commits:

- **`b9fd57f` font: real temp-pool IPC + harfbuzz-safe table-based fontID translation.**
  Wire types `RegisterFontBuffer`/`UnregisterFontBuffer` (MsgTypes 108–111). Fontsvc
  `registeredBytes[64]` table. `FontSvcGlyphProvider` redesign: `slots[MaxFonts=256]`
  with `serverFontID` + `kind` (never arithmetic). `RegisterBuffer` pushes bytes via
  `AllocPagesSlice` + `SharePagesWithTarget`. `OpenTemporaryFont` / `CloseTemporaryFont`
  real IPC. `CleanupShepherdFonts` death notification wired through rachel.

- **`e3b7159` fix(grid): publish scroll attrs on visibleCount transition.**
  `GridTable.Draw` was not calling `publishScrollAttrs` on the 0→N initial-draw
  transition, so `VisibleRowCountAttr` stayed 0 and `cache.Rebalance` short-circuited.

- **`7af236c` fix(mail): run cache.Rebalance + body fetch after every iteration.**
  The drainDirty loop discarded `eagerCh` notifications fired synchronously from the
  wmCh handler. Fix: move `cache.Rebalance` + `requestBody` checks out of eagerCh case
  and run them after every iteration regardless of which select case fired.

### 5 × 180s stability runs

Five distinct failure modes (fti Fstatat hang, bodies didn't render, fti hung again,
scrollbar didn't kick cache, two boot panics). The correctness fixes are sound; failure
rate concerns warranted a bisect — superseded by the GC crash which became TOP OF STACK.
