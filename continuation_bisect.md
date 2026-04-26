# Continuation prompt: GC crash fix — refined hypotheses

You are continuing work on the Mazzy/Mazarin operating system in
`/Users/iansmith/mazzy` (sister repo at `/Users/iansmith/louis14`).
Read `task_plan.md` (TOP OF STACK section) and the most recent entry
in `progress.md` for full context before doing anything.

## Current situation (2026-04-26 evening, after Opus review)

A previous session (Sonnet) hypothesised the GC crash was caused by
`SyscallMunmap` reading the wrong TTBR0 because "SVC handlers run on
a kernel worker goroutine." **That hypothesis is almost certainly
wrong.** The Opus review traced the SVC path and refined the candidate
list to one strong lead.

```
fatal error: sweep increased allocation count
runtime: nelems=36 nalloc=15375 previous allocCount=1 nfreed=50162
```

`bgsweep` in the mail shepherd. A span with `allocCount=1` has 15375
gcmarkBits set after concurrent mark. Either the gcmarkBits arena was
pre-populated with garbage, OR the mark phase walked into garbage data
that looked like pointers.

### What was already fixed (keep these)

- **MAP_FIXED unmap** (`kmazarin/ksyscall/mmap.go`): `unmapFixedRange()`
  helper releases existing PTEs before re-recording the span. Real
  Linux semantics. Crash persists, but the fix is correct.
- **Constraint-block PD_PINNED** (`kmazarin/kmem/constraint.go`):
  prevents per-shepherd cleanup from releasing the system-shared
  constraint block. Symptomatic of a wider issue (see hypothesis #2
  below): system-shared pages without proper refcounting can be freed
  by per-shepherd cleanup.

### What was probably wrong about the previous hypothesis

`SyscallMunmap` uses `kmem.UnmapUserPage(va)` which reads TTBR0 via
`readCurrentL0PA()`. The previous prompt claimed this is wrong because
"SVC runs on a kernel worker goroutine." The truth (from
`exceptions_arm64.s:233+`):

- SVC fires → EL1h with SP_EL1
- Switches `x28` (Go's g register) to `kmazarinG0Addr` so Go thinks
  it's on g0 (kernel goroutine)
- Calls `SyscallDispatch` directly, nosplit, on the **same CPU thread**
- **TTBR0 is NEVER changed**. There is no `SwitchTTBR0WithASID` call
  between SVC entry and `SyscallMunmap`.

So TTBR0 in `SyscallMunmap` IS the calling shepherd's L0PA. Reading
TTBR0 there is correct. (The misleading comment in `stubs.go:279` is
wrong; the real reason `SyscallMadvise` uses stored L0PA is defensive
coding, not a TTBR0 bug.)

Independent confirmation: `HandleUserPageFault` reads TTBR0 for every
fault and the ZERO_VERIFY_FAIL probe never fires — if TTBR0 were wrong
on synchronous syscalls, faults would already be broken.

**You may still change `SyscallMunmap` to use `UnmapUserPageWithL0`
defensively + correct the misleading comment, but do not expect it to
fix the GC crash.**

## Refined hypothesis ranking (Opus review)

### #1 RULED OUT — Page reuse without zeroing (non-MAP_FIXED path)

Every demand-fault / IPC-page allocation path goes through buddy alloc
+ explicit zero via scratchVA + `CleanPageCache`:
- `HandleUserPageFault` (`paging.go:815`)
- `allocPTPage` (`paging.go:419`)
- `AllocAndMapUserPageWithL0` (`paging.go:2970`)
- `allocAndCopyCallerData` (`delegate.go:518`)
- `allocEmptyDataPage` (`delegate.go:554`)
- `allocAndCopyCallerString` (`delegate.go:583`)
- `handleFileMappedPageFault` (`mmap_pagefault.go:53`)
- `SyscallAllocPages` (`alloc_pages.go:84`)

Non-zeroing paths (`MapContiguousUserPages`, `AllocAndMapUserPageNoZero`)
are correct by design — DMA pages filled by hardware, code pages
overwritten by ELF copy. None of them touch the GC arena.

### #2 STRONG LEAD — Refcount lost on dual-mapped IPC pages

`MapPageInProcess` (`paging.go:2336`) installs a PTE but **does NOT
bump RefCount**. Most callers compensate by manually `desc.RefCount++`
before calling. **One critical site does not:**

`delegate.go:833` — the **MmapPageFill reply path** for file-backed mmap:

```go
// First call (allocEmptyDataPage at delegate.go:561):
//   pa := AllocPage(PageSharedIPC, handlerSID)  // RefCount=1, owner=handler
//   MapPageInProcess(handlerSID, va, pa, 0)    // handler now mapped, RefCount=1

// Later (delegate.go:833, MmapPageFill reply):
mapOK := kmem.MapPageInProcess(info.CallerSID, callerBufVA, pa, elfFlags)
//   ^ NO RefCount++! Now mapped in 2 shepherds, RefCount still 1.
```

Comment at line 838-842 says "Handler-side pageCache tracks the dual
mapping. On munmap/death, flushAndCleanupPages sends IPC rounds to
flush dirty pages and return handler VAs for unmapping — no
kernel-side tracking needed."

**That design partially works** — `WriteBackSharedMmapOnDeath` and
`SyscallMunmap`'s file-backed branch DO call `flushAndCleanupPages`
and skip `ReleasePageByPA` on file-backed pages. But there are leaks:

1. **`SyscallMadvise` has no fileBacked check** (`stubs.go:281-294`).
   If anything calls `madvise(MADV_DONTNEED)` on a file-backed VA,
   the loop calls `ReleasePageByPA(pa)` unconditionally — RefCount
   1→0 → page returned to buddy → handler still has stale PTE.

2. **`CleanupShepherdPages` Phase 1** (`cleanup.go:55-65`) walks all
   spans and calls `releasePageByPA` on every leaf page. File-backed
   pages aren't distinguished. `WriteBackSharedMmapOnDeath` runs
   FIRST (TerminateShepherd Phase 1 pre-lock) and should unmap from
   handler before Phase 1 walks — but if any path skips it, or if
   the handler's `pageCache` and the kernel's tracking diverge, dual
   PTEs can survive.

3. **`PD_SHARED` flag is not set** by the dual-mapping at
   `delegate.go:833`, so even introspection can't distinguish dual
   from single-mapped pages.

The trigger is plausibly: fti or maildb does file mmap, the handler
(linux shepherd) gets dual-mapped pages, then either madvise or a
shepherd-death edge case decrements RefCount to 0, page goes to buddy,
mail allocates that page for its GC arena, linux writes to the stale
PTE (page-cache update) → mail's mark bits scrambled.

**Why the symptom matches:** mail crashes EARLY (right after "cache
ready") and the corruption is in the GC bitmap arena, which is
allocated very early during heap setup.

### #3 RULED OUT — Cache aliasing between scratchVA and userVA

ARM64 D-cache is PIPT-equivalent by architecture. `DC CIVAC` operates
by VA → PA translation; cleaning at scratchVA flushes the line for
that PA regardless of which VA aliases also map it.
`HandleUserPageFault`'s ZERO_VERIFY_FAIL probe reads back from
scratchVA after cleaning and never fires — confirms the scratch
mapping and cache flush both work.

### #4 UNLIKELY — Bump allocator returning overlapping VA

Per-shepherd `BumpPointer` is monotonic, starts at 0x200000000000.
`userBumpAlloc` checks `findSpanOverlapEnd` before claiming a range.
`bumpAllocForShepherd` (kernel-initiated, IPC pages) uses a separate
range starting at 0x500000000000. Go's heap arena hint (0xC000000000)
is below `userMmapStart` and would be redirected by the bump allocator
on the first sysReserveOS call, so Go's later MAP_FIXED uses whatever
VA the kernel returned — self-consistent.

The `scanForStalePTEs` debug check at `mmap.go:191` exists exactly to
catch this; if it ever logged `[mmap:STALE]` we'd see it in the serial
output. Sonnet's analysis didn't mention seeing those messages.

If you want belt-and-braces: keep the check enabled and watch for it
in any future run.

## Recommended next steps (in order)

### Step 1 — Add a probe to confirm hypothesis #2

Don't fix anything yet; first confirm the bug actually triggers.
Add a check in `HandleUserPageFault` AFTER zeroing and TLB invalidate
that reads back the **first 8 bytes via the user VA** (not scratchVA),
and logs if non-zero. If hypothesis #2 is right, we'll see a non-zero
read after a "fresh" page is handed out — proving the page is still
being written through a stale PTE elsewhere.

The existing ZERO_VERIFY at `paging.go:834` only reads via scratchVA.
A user-VA verify is what's missing. Concretely:

```go
// After step 4 (ISB) at paging.go:830
verifyAtUser := *(*uint64)(unsafe.Pointer(pageAddr))
if verifyAtUser != 0 {
    serial.RawUARTPuts("[ZERO_VERIFY_USER_FAIL] PA=0x")
    serial.RawUARTHex64(uint64(framePA))
    serial.RawUARTPuts(" userVA=0x")
    serial.RawUARTHex64(uint64(pageAddr))
    serial.RawUARTPuts(" word0=0x")
    serial.RawUARTHex64(verifyAtUser)
    serial.RawUARTPuts("\r\n")
}
```

If this fires, we have direct evidence of a stale PTE writing through.
The PA in the log lets you grep the page descriptor history (was it
previously file-mapped? which shepherd?).

If this DOESN'T fire and the GC crash still happens, the corruption
is happening AFTER the page fault completes (so during normal Go
operation), which is a different problem.

### Step 2 — If #2 confirmed: fix the dual-mapping refcount

Two-part fix:

**a)** In `delegate.go:833` (MmapPageFill reply), bump RefCount before
mapping:

```go
if desc := kmem.GetPageDescriptor(info.DataPagePA); desc != nil {
    desc.RefCount++
    desc.Flags |= kmem.PD_SHARED
}
mapOK := kmem.MapPageInProcess(info.CallerSID, ...)
if !mapOK {
    desc.RefCount--  // rollback
}
```

**b)** In `SyscallMadvise` userspace path (`stubs.go:281-294`), check
file-backed before calling `ReleasePageByPA`, mirroring `SyscallMunmap`:

```go
shepherd := proc.CurrentShepherd()
fileBacked := false
if shepherd != nil {
    if fm := shepherd.FindFileMappingByVA(alignedAddr); fm != nil {
        fileBacked = true
    }
}
// ... in the loop:
if pa != 0 && !fileBacked {
    kmem.ReleasePageByPA(pa)
}
```

After this, dual-mapped pages have RefCount=2; `ReleasePageByPA` from
either side decrements to 1; the page only returns to buddy when both
sides have unmapped (handler-side cleanup via flushAndCleanupPages or
via WriteBackSharedMmapOnDeath).

### Step 3 — Verify

1. Build: `$GO tool task kmazarin:arm64`
2. Run 60s: `$GO tool task run-arm64-hvf TIMEOUT=60`
3. Run 120s, 180s.
4. If crash gone: update tracking files, resume the morning bisect.
5. If crash persists: the user-VA ZERO_VERIFY_USER_FAIL probe data
   from Step 1 should point at the actual culprit. Look for the
   PA in the page-descriptor history — is it a SharedIPC page, a
   FileMmap page, a previously-PageUserHeap page from another
   shepherd?

## Hard rules

- Never `cat`/`Read` `/tmp/diplomat-*serial*.log` — use
  `$GO tool safe-serial-read` or `grep -a`.
- Always `run-arm64-hvf` (never `run-arm64`).
- Always `$GO tool task` for builds (never bare `go build`).
- Never push without explicit user instruction.
- Don't touch `/Users/iansmith/louis14`.
- GOTOOLCHAIN=auto, GO=/opt/homebrew/bin/go,
  QEMU=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-aarch64.

### Branch: `feature/mail-dumb`
