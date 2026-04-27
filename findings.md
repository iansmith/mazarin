# Findings

## GC crash — `sweep increased allocation count` in mail bgsweep

### Crash signature

```
runtime: nelems=1 nalloc=43525 previous allocCount=1 nfreed=50162
fatal error: sweep increased allocation count
```

mspan struct corruption — `nelems`/`nalloc` are mathematically impossible. Across
runs the small `nelems` varies (1, 36, 170) but `nalloc` clusters at ~43K-45K,
which means at `countAlloc()` time `nelems` was ≥43525 (so the loop summed
~5440 bytes of mark bits); by panic-print time `nelems` had been overwritten to a
small value. The writer is putting a small number at the `nelems` field offset —
fits a `MsgType`, a flush-response page `count` (0..511), or another mspan's
`nelems` value.

### Active hypothesis: partial-munmap full-flush (Bug B, FIX APPLIED)

`flushAndCleanupPages` was fd/inum-keyed with no VA-range constraint. A partial
munmap caused linux's `RemoveRangeBatch` to return all cached pages for the
inum, the kernel released them all, leaving the caller's still-mapped portion
with live PTEs to PAs that just went back to buddy. linux can then write into
those PAs through stale cache entries via `updateCachedPages` (called from
`sysWrite`/`flushWriteBuf` when the cache lookup matches), corrupting whatever
the runtime later allocated there — including mspan structs.

Fits all observed symptoms: guard silent (handleFlushReply legitimately clears
`PD_FILE_BACKED` before release), ZERO_VERIFY clean (page is zero when
reallocated to mail's heap), corruption arrives via writes through stranded
cache entries.

### Bug A (correlated leak, FIX DEFERRED)

`sysFtruncate` discards entries returned from `h.cache.RemoveRange`. Handler
PTEs and kernel `RefCount` are never torn down — pages stay alive at
`RefCount=1` until the next munmap of that fd or shepherd death. Logged via
`[ftruncate:LEAK]` for visibility. Proper fix needs a new
"drop-handler-VAs" IPC.

### Ruled out (do not revisit)

- **TTBR0 in SyscallMunmap**: SVC path never changes TTBR0; `stubs.go:279`
  comment is misleading.
- **Cache aliasing scratch↔userVA**: ARM64 PIPT D-cache rules it out.
- **Bump-allocator overlap**: `scanForStalePTEs` (`mmap.go:191`) never logs
  `[mmap:STALE]`.
- **SyscallMadvise on file-backed pages**: Go runtime only madvises its own
  heap VAs, not file-backed mmap VAs (different VA regions).
- **`releasePageByPA` premature free of FILE_BACKED page**: guard never fires.
- **Zero-on-alloc gaps**: every demand-fault / IPC-page path zeroes;
  `ZERO_VERIFY_FAIL` never fires.
- **MAP_FIXED stale PTE**: `unmapFixedRange` releases existing PTEs before
  re-recording the span (fix already in tree).

### Verification status

2 × 120s ARM64 HVF after the partial-munmap fix: clean, no warnings, no GC
crash. Pre-fix recurrence was ~1-in-5 at this duration; multiple longer runs
needed to discriminate fix-correct vs not-yet-triggered.

### Next steps if crash recurs

1. 5+ × 180s ARM64 HVF runs.
2. If crash recurs without `[munmap:PARTIAL]` firing: partial munmap wasn't the
   source. Re-aim at the `cache.Add` overwrite path — add a probe that detects
   the same `(sid, inum, offset)` faulting twice in
   `handleFileMappedPageFault`.
3. If `[ftruncate:LEAK]` fires often, prioritize the proper Bug A fix.

### Active instrumentation (silent on happy path)

- `[munmap:PARTIAL]` — unmap length doesn't equal the matched FileMapping
- `[pageCache:OVERWRITE]` — `cache.Add` replaces an entry with a different VA
- `[ftruncate:LEAK]` — sysFtruncate discards cache entries
- `[kmem] BLOCKED:` — `releasePageByPA` called on a `PD_FILE_BACKED` page

---

## Bug B family — write-through-stale-mapping into mail-app heap (UPDATED 2026-04-27 PM, post free-canary)

**Status:** Five diagnostic rounds in. H-T2 ruled out. H-T1 weakened (Option A no-op). **H-T3a (kernel write between free and reuse) ruled out by sentinel-byte canary** — 0 hits across ~1.5M+ verifies including a confirmed crash repro. The surviving mechanism is **H-T3b: kernel write AFTER the freed PA has been reissued and legitimately used** (canary already overwritten by the new owner before the corrupting write hits). Most likely channel: the linux page-cache writeback path.

| Round | Commit | What it ruled out |
|-------|--------|-------------------|
| Userspace checkpoints | `8a64a92` | Localized hangs to kernel `unmapUserPages` / `releasePageByPA` / `BuddyFreeTyped` chain. Caller-first close order fixed the `mem.Munmap` hang. |
| Kernel diagnostic guards | `ca7f5f6` | Buddy double-free guard (`buddyContainsPA`) silent. `kmem:UNDERFLOW` (RefCount race in `releasePageByPA`) silent. `[unmapLoop]` progress confirms loops complete. |
| Option B stale-PTE verifier | `612ed58` | At every BuddyAllocTyped of a user-side page type, walks every live shepherd's userspace page table for a leaf PTE still mapping the just-allocated PA. 5 × 180s ARM64 HVF, verifier on: 184K–203K scans/run, **0 stale-PTE hits** across all runs, including B3 which reproduced the mspan crash (`nelems=128 nalloc=31291`). H-T2 (stale PTE in PT memory) ruled out. |
| Option A trailing TLB flush in SyscallMunmap | (reverted) | Added `TlbiVMALLE1 + DsbISH + IsbSY` triplet at end of `SyscallMunmap`, mirroring `SyscallFreePages`. 5 × 180s: 2 mspan crashes (A3 `nelems=100 nalloc=22790`, A5 `nelems=36 nalloc=44307`), 1 boot hang, 2 clean. Crash rate unchanged vs baseline 1/5. The added flush is functionally a no-op: `UnmapUserPage`/`UnmapUserPageWithL0` already do per-page `tlbiVAE1IS` (inner-shareable broadcast), and a trailing local `TlbiVMALLE1` after a broadcast is redundant. **H-T1 weakened** — a real H-T1 test would need ASID swap on munmap or a same-VA probe right after `tlbiVAE1IS`. |
| Free-canary at buddy free/alloc | (next commit, default off) | Paint freed pages with `0xDEADBEEFDEADBEEF` (skip first 8 bytes for buddy next-pointer); verify intact at next allocation. 5 × 180s, ~1.5M+ verifies aggregate, **0 hits**, including in C3 which reproduced `nelems=1008 nalloc=23628` at boot during mail-app initial cache rebalance with no clicks. Confirms the corrupting write does NOT happen between `BuddyFreeTyped` and the next `BuddyAllocTyped` of the same PA. **H-T3a ruled out.** |

The corrupting write doesn't go through PageDescriptor accounting, doesn't leave a stale PTE in PT memory, isn't fixed by a redundant TLB flush, and doesn't land in the free→reuse window. mspan corruption still fires (1/5 baseline, 2/5 with Option A, 1/5 with canary, 1/3 of completed Option-B runs — same order of magnitude). The bug fits a "post-reuse stale-handle write" shape: a kernel path with a stale PA-derived pointer writes after that PA has been freed, reissued, and the new owner has overwritten the canary with normal data.

### Crash signatures (all same family)

- `fatal error: sweep increased allocation count`, cluster `nalloc≈22K-45K, small nelems` (1, 36, 100, 128, 170, 341, 1008 across runs). Fires at any heavy-allocation moment.
- `fatal error: freeIndex is not valid`, in mail-app's CSS parser. Different mspan field corrupted, same write pattern.

### Active hypotheses (post free-canary)

- **H-T3b (PRIMARY) — kernel write AFTER the freed PA has been reissued.** A kernel path with a stale handle survives past the free + reissue, and writes to the now-mail-app-owned page after mail-app has filled it with heap data. The canary is gone by then so the existing probe can't see it. **Most likely channel: linux page-cache writeback** — `maz/linux/page_cache.go`, `sysWrite`, `flushWriteBuf`, `updateCachedPages`, `flushAndCleanupPages`, `handleFlushReply`. Earlier session (`12e5f0d`) added a partial-munmap range guard; a different variant may still exist. **Next: read-only audit** of the page-cache mutation paths (Stage 2 in `task_plan.md`).
- **H-T1' (residual)** — TLB cache holds a translation past `tlbiVAE1IS` (HW erratum or barrier ordering). Hard to test; revisit only after the page-cache audit is exhausted.
- **H-T3a (free→reuse window)** — RULED OUT by free-canary.
- **H-T2** — RULED OUT by Option B verifier.

### What was wrong with the earlier hypotheses

**Mechanism-wrong (closed):** `goFont.ParseTTF` over shared pages letting GC walk into kernel memory. Go's `findObject` returns nil for non-arena pointers (shared pages: `0x500000000000+`, Go arenas: `0xC000000000+` — non-overlapping). Marker sets bits, doesn't write mspan struct fields. The earlier sections below preserve this as historical record.

**Right diagnosis but already fixed:**
- H-2 (releaseTempSlot leak): fixed in `3942ae8`.
- H-3 (CloseTemporaryFont not unmapping caller side): fixed in `3942ae8`.
- H-4 (architectural: load @font-face from fs): deferred, not corruption-causal.

**Right diagnosis but ruled out by `ca7f5f6` instrumentation:**
- H-K1 buddy double-free.
- H-K2 stale free-list state from a prior mishandled free.
- H-K3 PD_SHARED bit clear path (not a corruption mechanism in our model).
- H-K4 page descriptor cleared mid-loop.

**Earlier `[versai:timing]` log-prefix confusion:** that prefix is hardcoded in `mancini/std/web_interactor.go` and appears in the log of *any* app running a WebInteractor. The crashing process is **mail-app**.

---

### Crash signature

```
fatal error: freeIndex is not valid
runtime.(*mcache).nextFree  malloc.go:1066
runtime.mallocgcTiny        malloc.go:1331
runtime.mallocgc            malloc.go:1193
runtime.newobject
louis14/pkg/css.parseMediaLength  stylesheet.go:1764
louis14/pkg/css.evaluateMediaCondition
```

mail-app's heap (goroutine 1, m=3). `freeIndex is not valid` = `s.freeindex ≥ s.nelems`
in an mspan — same mspan struct corruption category as the `sweep increased
allocation count` crash, different field corrupted.

### Trigger sequence (reproducible)

```
[mail] body: 56448 bytes variant=1          ← second message click
[provider] populateSlot client=1..8 kind=1  ← font cache pages transferred to mail-app
fatal error: freeIndex is not valid          ← mail-app heap corrupted
```

First message click (28118 bytes, kind=2 font slots) rendered correctly. Second
message click (56448 bytes) triggered the crash during kind=1 font slot population.
`populateSlot` uses TransferAndUnmap or shared-page IPC to hand font cache pages
to the rendering shepherd.

### Architecture (confirmed from source)

**maz/fontsvc/main.go** — font server (runs inside rachel's PID/SID).  
**mazarin/fontcache/provider.go** — client-side GlyphProvider (runs in versai, mail-app, etc.).  
**kmazarin/ksyscall/map_shared.go** — kernel SharePages: `desc.RefCount++`, `PD_SHARED` set; pages mapped RW (`elfFlags=0`).

Page sharing flow for a temp (@font-face) font:
1. mail-app `pushBytesToFontsvc`: `AllocPagesSlice` → copy font bytes → `SharePagesWithTarget` to fontsvc. mail-app keeps the original VA (**VA-A**).
2. Fontsvc `handleRegisterFontBuffer`: records `registeredBytes[].fontDataVA` (fontsvc's mapped copy).
3. mail-app `openTempViaIPC` → fontsvc `handleOpenTemporaryFont`:
   - Reads bytes via `unsafe.Slice(reg.fontDataVA, ...)`, calls `goFont.ParseTTF` to build face.
   - `buildTempCache` → `AllocPagesSlice` + `BuildGlyphCacheInto` → new cache pages.
   - `shareCacheAndReplyTemp` shares both cache pages **and fontData pages** back to mail-app.
     FontData reshare gives mail-app **VA-B** pointing to the SAME physical pages as VA-A.
4. mail-app `populateSlot`: receives `fontAddr`=VA-B, `cacheAddr`=VA-C.
   - `cache = unsafe.Slice(VA-C, cacheSize)` — fine (no pointers into it from Go heap).
   - `fontData = unsafe.Slice(VA-B, fontDataLen)` — shared kernel pages.
   - **`face, _ = goFont.ParseTTF(bytes.NewReader(fontData))`** — allocates `*goFont.Face` on mail-app's Go heap with internal slices/pointers INTO the shared kernel pages.
   - Stores `face` and `fontData` in `p.slots[clientID]`.

### Hypothesis 1 (PRIMARY): GC pointer-invariant violation — `*goFont.Face` points into non-heap shared pages

`goFont.ParseTTF` returns a `*goFont.Face` whose internal tables (cmap, glyf, hmtx, etc.) are represented as Go slices backed by `fontData`. `fontData` is `unsafe.Slice` over a shared kernel page at VA-B — **not part of mail-app's Go heap**.

The Go GC, on its next scan, walks live heap objects, finds the `*goFont.Face`, follows its internal slice headers as live pointers, and lands in the shared kernel pages. Those pages contain raw font binary data, not Go objects. The GC reads whatever bytes it finds there as pointer-sized values, attempts to mark them, and can reach into unrelated spans. If one such "pointer" lands inside an mspan's `freeIndex` field offset, it overwrites it → **`freeIndex is not valid`** on the next `nextFree` call.

This is fully consistent with:
- Crash in `mallocgcTiny` → `nextFree` rather than at the point of corruption (GC ran earlier, corrupted the mspan, crash delayed until next alloc).
- Crash on the SECOND render, not the first: the GC runs between renders (fti completed at ~145s, GC pressure from the first render's face+slice allocation triggers a cycle).
- Crash in mail-app's goroutine 1 (render path), not in a background GC goroutine.

**Root fix**: Do NOT call `goFont.ParseTTF` against shared kernel pages. Parse from a Go-heap copy, or switch the permanent-font path so mail-app receives fontData from fs (via fsclient) rather than from fontsvc's reshare. See §font-loading path below.

### Hypothesis 2: releaseTempSlot leaks cache pages (no `mem.FreePages`)

`releaseTempSlot` (`fontsvc/main.go:992`) just zeros the struct — **confirmed TODO**:
```go
func releaseTempSlot(idx int32) {
    // TODO: when the shared-bytes path lands, unmap the caller's font-data
    // pages from fontsvc's address space here before clearing fontData.
    tempFonts[idx] = fontSlot{}
    tempFontOwner[idx] = -1
}
```
Cache pages allocated by `buildTempCache` are never freed. The pages stay alive in both fontsvc (referenced via old pointer, now dangling from the zeroed struct) and mail-app (still mapped at the shared VA). The kernel refcount never goes to zero. This is a **memory leak** not a corruption source directly, but it means freed temp font slots' cache pages accumulate as ghost mappings in mail-app's address space indefinitely. Over many renders this could exhaust the VA bump-allocator range.

### Hypothesis 3: FontData dual-mapping in mail-app (VA-A and VA-B)

mail-app allocates font bytes at VA-A (`pushBytesToFontsvc`). Fontsvc shares them back as VA-B (`shareCacheAndReplyTemp`). The same physical pages are mapped at TWO VAs in mail-app. The kernel refcount for each page is 3 (mail-app-A, fontsvc, mail-app-B). Neither mail-app mapping is ever unmapped (no `SyscallMunmap` for shared font pages on client side). This is a secondary leak, not a direct corruption source, but the dual mapping is an invariant violation: the kernel's `desc.Owner` is still mail-app (since mail-app allocated via AllocPagesSlice), so fontsvc's reshare is owner→non-owner→back, which may confuse any future ownership-checking paths.

### Hypothesis 4: Correct @font-face load path not implemented

User expectation: mail-app/louis14 should load the @font-face font file **from fs** (via fsclient), keeping the bytes in kernel-page-backed memory with a single ownership chain. The current path (`RegisterBuffer` in `provider.go`) takes a `[]byte` (Go-heap slice, e.g. from an HTTP fetch or base64-decoded email body) and copies it into kernel pages. The copy is correct, but the local `registeredFace.fontData = data` (line 247) retains the original Go-heap slice, and if `openRegistered` is used as fallback, its `fontData` is Go-heap bytes that mail-app's `ParseTTF` does parse — but those ARE proper Go heap objects so GC is fine there. The IPC-success path through `openTempViaIPC`/`populateSlot` is where the problem bites (fontData from shared pages). Aligning on a single fs-based load path would eliminate the dual-source ambiguity.

### Crash on second render, not first — why?

Between the first and second renders:
- mail-app calls `CloseTemporaryFont` for 10 temp slots (server IDs 4096..4105). Client drops `p.slots[1..10] = nil`.
- GC runs: the live `*goFont.Face` objects stored in those 10 now-dropped slots become unreachable, but the face objects themselves have already been walked. The damage from Hypothesis 1 may be during the GC cycle triggered by the heavy first render (10 fonts × ParseTTF allocations). The mspan corruption is written by the GC before the second render starts.
- Second render: `populateSlot` for permanent fonts (server IDs 4..11) does further allocations, and one of those `mallocgcTiny` calls hits the already-corrupted mspan.

### Linux-ui console not shown (same run)

Separate observation: linux-ui console window not appearing. linux shepherd itself
is alive (gc=36..139 across run). Likely a window-placement or z-order issue in
rachel, not a shepherd crash. Investigate separately.

---

## fstatat/sysid=44 delegate hang (PAUSED, ~1-in-5 runs)

Per-call entry/exit instrumentation in `maz/linux/syscalls.go::sysFstatat` +
`mazarin/fsclient/client.go::callLocked` + periodic `chan-monitor` in
`maz/linux/main.go`. No hang in 3 × 180s runs yet; resume after GC crash is
resolved.

Decision tree when it fires:

```
[fstatat] seq=N enter  (no matching "done") → hang in h.fs.Stat() or deeper
  [fsclient] stat id=N sent present → blocked at <-c.RespCh (fs.maz silent)
  [fsclient] stat id=N sent absent  → blocked at c.mu.Lock() or uring.Send
  chan-monitor wmCh near cap=8      → dispatcher deadlock
```

---

## x86_64 Boot OOM — diagnostic shortcut

If `[kmem] Buddy OOM order=0 total=ffffffff20000` at boot:
1. Check `Unified pool:` start address — must be ≤ `0x100000000`.
2. `diplomat/main/pagetable_amd64.go::allocatePhysPages` must use
   `AllocateMaxAddress` with seed `physAddrResult = linearMapMaxPA - 1`.
3. If 2.5 GB contiguous alloc still fails: retry-down ladder
   `[2.5G, 1.5G, 1G, 512M, 256M]` in `kernelvm_amd64.go::PrepareKernelVM`.

Both fixes are in tree (2026-04-24). Note for future regressions.
