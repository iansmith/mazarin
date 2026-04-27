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

## Bug B family — write-through-stale-mapping into mail-app heap (UPDATED 2026-04-27 PM, post `ca7f5f6`)

**Status:** All earlier hypotheses superseded. After three rounds of progressively-tighter instrumentation:

| Round | Commit | What it ruled out |
|-------|--------|-------------------|
| Userspace checkpoints | `8a64a92` | Localized hangs to kernel `unmapUserPages` / `releasePageByPA` / `BuddyFreeTyped` chain. Caller-first close order fixed the `mem.Munmap` hang. |
| Kernel diagnostic guards | `ca7f5f6` | Buddy double-free guard (`buddyContainsPA`) silent. `kmem:UNDERFLOW` (RefCount race in `releasePageByPA`) silent. `[unmapLoop]` progress confirms loops complete. |

The kernel page-free chain is **not** double-freeing or underflowing RefCount. Yet mspan corruption still fires, even at boot during mail-app's first cache rebalance with **zero font activity** in the run. The corruption is happening from a path that doesn't go through the kernel page-management RefCount accounting at all — most likely a write through a stale TLB entry or stale PTE from another shepherd, into a PA that's been freed-and-reissued to mail-app's heap.

### Crash signatures (all same family)

- `fatal error: sweep increased allocation count`, cluster `nalloc≈37K-45K, small nelems`. Fires at any heavy-allocation moment.
- `fatal error: freeIndex is not valid`, in mail-app's CSS parser. Different mspan field corrupted, same write pattern (small int written at a struct-field offset).

### Refined hypotheses (in `task_plan.md` TOP OF STACK)

- **H-T1** stale TLB after `SyscallMunmap` (it doesn't `TlbiVMALLE1` at end, unlike `SyscallFreePages`). PRIMARY suspect. Option A in plan: add the TLB flush; Option B: prove via stale-PTE detection at allocation time.
- **H-T2** stale PTE in another shepherd's page table.
- **H-T3** direct kernel-side write into a freed page (scratch mapping, DMA, page-cache writeback).

The `[unmapLoop]` data from the `ca7f5f6` run shows sid=21 doing 16 × 1599-page user-region frees right before the boot-time crash. These are ELF-load scratch buffers, not shared pages, but freed PAs go straight to buddy and could be reissued to mail-app's heap. If shepherd-launch leaves stale TLB/PTE state, that's a plausible boot-time trigger.

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
