# Task Plan — Mazarin / Mazzy

## TOP OF STACK: kmazarin x86_64 won't build — `nosplit stack over 792 byte limit` — 2026-05-01

**Why this is now top priority**: MAZ-10 shipped — ARM64 HVF is verified. x86_64 boot smoke is needed to fully verify the Gap 2 mazlink work on amd64, and it's required for any cross-architecture work going forward. Bug-B is paused until x86_64 boots.

---

## DEFERRED: kmazarin x86_64 won't build — `nosplit stack over 792 byte limit` — 2026-05-01

**Why deferred**: x86_64 boot smoke is not required to verify MAZ-10. The nosplit error is a pre-existing master issue (confirmed at `4842c90`) unrelated to the dynlink/injection changes. ARM64 HVF is the primary architecture and sufficient to validate MAZ-10. Resume x86_64 after MAZ-10 lands.

**Symptom**: `$GO tool task kmazarin:x86_64` fails at link time with the Go linker rejecting NOSPLIT functions whose worst-case stack chains exceed 792 bytes.

```
main.syscallEntry: nosplit stack over 792 byte limit
                                                    56 bytes over limit
[...]
main.isrDev46: nosplit stack over 792 byte limit
                                                    152 bytes over limit
                                                    grows 8 bytes, calls runtime.morestack<0>
                                                    grows 160 bytes, calls runtime.panicBounds64<1>
                                                    grows 40 bytes, calls runtime.panicBounds<1>
```

### What's affected

ARM64 builds fine (different ISR/syscall entry layout — see `kmazarin/kmazarin/exceptions_arm64.s` vs `exceptions_amd64.s`). The error is purely on x86_64 NOSPLIT entry stubs.

### Investigation steps (when resumed)

1. Identify the offending Go runtime change (likely `panicBounds64`/`panicBounds` inlining).
2. Trace the call chain from `common_exception_entry`.
3. Fix via runtime-patches overlay (preferred), refactor exceptions_amd64.s, or bump mazlink NOSPLIT limit.

### Test plan (when resumed)

```bash
export GOTOOLCHAIN=auto GO=/opt/homebrew/bin/go QEMU_X86_64=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-x86_64
$GO tool task kmazarin:x86_64        # must succeed
$GO tool task run-x86_64 TIMEOUT=600
$GO tool safe-serial-read /tmp/diplomat-serial.log | grep -E "\[mail\] cache ready|panic|fatal"
```

Done when: kmazarin x86_64 builds clean, x86_64 boots through `[mail] cache ready`, no new panics.

---

## PAUSED: Bug-B family (kernel runtime panic at/after `[mail] cache ready`) — paused 2026-05-01 by x86_64 work

The mazlink Gap 1 + Gap 2 work shipped — shepherd overlay deletion + GOTPCREL host-mode support. Shepherd-side forensics are structurally unblocked (no auto-overwrite to fight) but `runtime-patches/` is currently only consumed by the kmazarin build; wiring it through to the shepherd build is a small follow-up.

VA-collision hypothesis was refuted by the GG-sweep (10×180s, all `inIPC=132 outIPC=0`). Forensic from GG9 GC SIGSEGV: `X8 = " failed "` ASCII — text data overwrote a register-sized field, points at heap corruption with log-string payload.

Detailed continuation prompt was previously in `next_session_prompt.md` — that's now repurposed for x86_64 work. The bug-B context still lives in `memory/MEMORY.md` Active Bugs section and `task_plan.md` archived sections below.

Resume bug-B once x86_64 builds and boots, AND once `runtime-patches/runtime/traceback.go` is wired into the shepherd build.

---

## ARCHIVED: Mazlink worktree cleanup pass (2026-05-01) ✅

Code review of the Gap 1 + Gap 2 worktree surfaced four follow-up items, all now landed:

- **Gate tightening** (commit `101fdde`): `arm64/asm.go` and `amd64/asm.go` GOTPCREL adddynrel now use a 3-way switch — host gets `AddGotSymStatic`, other ELF (plugin/PIE/dynlink) gets `AddGotSym + GLOB_DAT` (stock-equivalent), non-ELF gets the stock fallback. Original gate was over-broad and would have changed PIE+internal behavior. Plugin path verified byte-identical: `rachel.maz .rela` and `rachel-amd64.maz .rela` both match pre-Gap-2 baseline byte-for-byte.

- **Doc rot** (in same commit `101fdde`): `data.go` third-hook comment updated to reference `AddGotSymStatic` instead of the original incorrect plan prescription. `mazarin/mazhost/doc.go` "and the shepherd overlay" reference dropped (overlay is gone). Empty `# Overlay Generation` section header in root Taskfile removed.

- **Bug fix uncovered** (commit `a9cace0`): Gap 2's `bea6115` deleted the shepherd-overlay tasks but missed `maz/fs/Taskfile.yml`, which still depended on `:merged-shepherd-overlay` and the deleted `MERGED_SHEPHERD_OVERLAY_*` variables. Without this fix, **any build path through `fs:arm64`/`fs:x86_64`** (i.e., anything depending on `disk-arm64`/`disk-x86_64`, including kmazarin staging) hung indefinitely with `task` burning ~28GB of memory. fs is not a mazdl host so it doesn't need dynlink — switched to `mazarin:userspace-overlay` (matching `clocks`/`calc`).

- **Rebase onto master** (8 commits replayed): worktree was branched from `9cced8b`, missing 14 master commits (wedge fixes `a1a4ef8`/`082b164`/`90be746`/`f5c09f8`, log cleanup, bug-B continuation prompt, etc.). Rebased clean; conflicts in `task_plan.md`/`progress.md`/`next_session_prompt.md` resolved by keeping master's structure and prepending Gap 1/2 entries.

ARM64 HVF post-rebase smoke confirmed: reaches `[mail] cache ready` cleanly, no panics. (Worktree was missing user-local `data/mail/mbox/current.mbox` — copied from master to enable smoke.)

x86_64 boot smoke blocked by the kmazarin nosplit build error (now TOP OF STACK above).

---

## ARCHIVED: MAZ-10 — shepherd injection unification + FSClient interface (2026-05-10) ✅

Gap 1 (drop keepalive) + Gap 2 (dynlink host GOTPCREL) already shipped on this branch. The remaining work unified ring/fsclient injection through the generic shepherd and fixed a cross-.maz type identity bug:

- Replaced `*fsclient.Client` concrete type with `FSClient` interface + `clientImpl`. All plugin method calls now route through itab → host code → host type descriptors, fixing the `raw.(ipc.FSIPCRespPayload)` type assertion failure in `callLocked`.
- Added `fsclient` to dlopen-host-packages.txt and `forceKeepMethods()` to prevent deadcode of interface methods.
- Removed unnecessary RespCh bridging in linux/main.go.
- ARM64 HVF: 5/5 font index loaded, 0 new error types, Bug-B at baseline rate (1/5).

**Branch:** `ianster/maz-10-mazlink-gap-1-gap-2-replace-shepherd-overlay-with-dynlink`
**Key commits:** `0e34648` (FSClient interface), `c49cccd` (ShepherdInit injection)

---

## ARCHIVED: Mazlink Gap 2 — replace shepherd-overlay with -dynlink (2026-05-01) ✅

**Branch:** `worktree-agent-a7038135c55f4f577` (worktree). Three commits:
- `647c67a` mazlink: route -dynlink GOTPCREL through Adddynrel for host builds (Gap 2 linker)
- `e8514ca` shepherd: build with -gcflags=-dynlink, drop overlay (Gap 2 build)
- `bea6115` cmd/gen-ast-stubs: drop -mode=shepherd; delete shepherd-overlay tasks (Gap 2)

ARM64 HVF smoke verified: 5/5 reached `[mail] cache ready`. Plugins loading (rachel.maz, linux.maz, fti.maz, maildb.maz, mail.maz). rachel.maz `.rela` section byte-identical before/after — plugin path unaffected.

### What the linker actually needed

The plan's `AddGotSym(..., 0)` prescription was wrong: type 0 = `R_AARCH64_NONE`, leaves the GOT slot zeroed at runtime → SIGSEGV. The actual fix needed a new `AddGotSymStatic` in `lib.go` calling `got.AddAddrPlus(target.Arch, s, 0)`, which emits `R_ADDR` that the linker's own relocsym pass fills with the symbol's static VA.

Additionally, `-dynlink` causes the compiler to emit `R_TLS_IE` / `R_ARM64_TLS_IE` for TLS accesses. Stock guards admit only PIE/plugin; extended both `data.go` and `arm64/asm.go` guards to also admit `*flagDlopenHostExports != ""`.

The host-vs-plugin distinction is gated in `arm64/asm.go` and `amd64/asm.go` by `*ld.FlagDlopenHostPackages != ""` (plugin) vs absence (host → static fill). Exported aliases `FlagDlopenHostPackages` / `FlagDlopenHostExports` added to `mazdl.go` for cross-package access.

### Prior context

Full investigation history in `memory/shepherd_overlay_dynlink_experiment.md` (now RESOLVED).

---

## ARCHIVED: Mazlink Gap 1 — drop MazKeepAliveSymbols force-reference (2026-05-01) ✅

**Branch:** `worktree-agent-a7038135c55f4f577` (worktree), commit `b3029f7`.

**Path taken:** Step 2A (hypothesis verified). `appendMazKeepAliveSymbolsFunc` was emptied as a probe; both shepherds built and ran 5/5 boot-to-`[mail] cache ready` with no new panics. Clean removal then landed.

**What was removed:**
- `mazarin/mazhost/keepalive.go` (the `init()` that forced `MazKeepAliveSymbols` reachable)
- `keepAliveEntry` struct, `subPkgInfo`/`subPackages`, `getCompiled` closure, `getCompiledFiles`, `appendMazKeepAliveSymbolsFunc` from `cmd/gen-ast-stubs` (Job 2 entirely gone)
- `shepherdFuncInfo.stub` dead field + `createStub` call in shepherd transform path

**What was not removed:** Job 1 (`//go:noinline` injection) — still needed. That's Gap 2's scope.

**Verification:** 5×60s ARM64 HVF sweep, all reached `[mail] cache ready`, 0 bug-B fires (within noise of 1/10 baseline). Diff: 4 files, 18 insertions / 204 deletions.

**Next (Gap 2):** replace the 370-file gen-ast-stubs shepherd overlay with stock `-dynlink` once mazlink has the R_ARM64_GOTPCREL reloc support. Gap 2 is a separate session.

---

## ARCHIVED: bug-B continuation pre-mazlink (2026-04-30 night) — see PAUSED section above

The bug-B continuation that was TOP OF STACK on 2026-04-30 night is preserved in the PAUSED section above. Concrete first step (kept here for reference): re-enable `vaCollisionProbeEnabled = true` in `kmazarin/ksyscall/mailbox.go` and run a 10×180s boot-only sweep. VA-collision was subsequently refuted; current best lead is heap-corruption forensics via shepherd-side `runtime/traceback.go` overlay (which requires runtime-patches → shepherd wiring as a prerequisite).

---

## ARCHIVED: spawn LoadFile goroutine in fs (deferred follow-up) — 2026-04-30 evening — DONE

**Status**: shipped. Two commits:
- `90be746` fs: spawn worker pool for fsDelegateCh (LoadFile / ReadFilePages)
- `f5c09f8` linux: reduce fileLaneWorkers from 1024 to 32

FF-sweep (10×60s) result: **0/10 wedges**. Trip-magic 0/10. The deferred follow-up that was "expected to drop the 1/10 remaining wedge to near zero" actually drove it to zero.

Pool sized at 16 workers (matches fsDelegateCh cap with 1× headroom). No deadlock concern: LoadFile workers don't recurse through fs.

Original plan retained below for reference.

**Status**: ready to start. Two prerequisite commits landed in the previous task (`a1a4ef8`, `082b164`); remaining wedge (1/10 in EE-sweep) is the case this addresses.

### Plan (small scope, ext2 RWMutex makes it safe)

`maz/fs/main.go:232-251` serve loop processes `fsDelegateCh` (LoadFile/ReadFilePages — large reads) and `fsIPCCh` (Open/Stat/etc — small reads) on the same single goroutine. While `handleLoadFile` runs (~3s for mail.maz), no fsIPCCh request is served.

**Change**: when fsDelegateCh receives a SyscallRequest, spawn a goroutine to handle it:

```go
case raw := <-fsDelegateCh:
    req, ok := raw.(sys.SyscallRequest)
    if !ok { continue }
    go func() {
        switch req.SysID {
        case sysid.LoadFile:
            handleLoadFile(mt, &req)
        case sysid.ReadFilePages:
            handleReadFilePages(&req)
        }
    }()
```

The fs serve loop returns to its select{} immediately, ready to drain fsIPCCh. Each LoadFile worker holds ext2 RLock during the read; multiple concurrent readers OK. Disk I/O serializes via asyncBlockDev's per-chunk d.mu (already in place).

**No worker pool yet** — LoadFile rate is bounded by shepherd-launch traffic (a handful per boot). Unbounded `go` is fine for this scale. Add a pool only if we observe >100 concurrent LoadFile goroutines.

### Risks

1. **Concurrent LoadFile goroutines all use the SAME mt and fsys.** `mt` is a mountTable read after init. `fsys` is the ext2.FileSystem pointer; we now have RWMutex protection. Both safe under concurrent reads.
2. **TransferAndUnmap** (called from handleLoadFile after the read) is a kernel syscall taking the caller's PID + page list. Multiple goroutines transferring to different callers in parallel: each transfer is independent. Should be safe but worth confirming with the kernel.
3. **req.Reply / req.LoadFileReply** are kernel syscalls. Multiple in flight from different goroutines: kernel SyscallReply handles per-callerTID lookup. No shared state. Safe.
4. **scratchPages = 8 in asyncBlockDev** — only 8 DMA pages shared across all readers. With per-chunk lock, slots are reused per chunk. Concurrent goroutines serialize at d.mu, each chunk uses all 8 slots, releases. No conflict.

### Test plan

EE-cadence again (10×60s) after the change. Expect:
- realStuck → 0/10 or 1/10
- cacheReady → ~10/10 (modulo bug-B kernel crashes which are separate)
- No new error log lines from fs

If a regression appears, revert easily — it's a single `go` keyword wrap in fs/main.go.

---

## ARCHIVED: ext2 RWMutex + asyncBlockDev per-chunk lock (2026-04-30 afternoon, COMPLETED)

**Status**: shipped. Three commits landed:
- `c7d73e8` diag(linux): case-branch enter/exit + SLOW handle() timing
- `a1a4ef8` ext2: add sync.RWMutex to FileSystem; lock readers/writers correctly
- `082b164` fs(asyncBlockDev): per-chunk d.mu release + DMA trip-magic sanity check

EE-sweep result: wedge rate dropped from 5/10 (DD-sweep, RWMutex only) to **1/10** (EE-sweep, full fix). Trip-magic fired 0/10 — per-chunk lock release is operationally correct.

The remaining 1/10 wedge is the case where fs's serve loop is mid-handleLoadFile and fsIPCCh requests queue at the userspace level. The new TOP OF STACK (above) addresses this.

Original plan retained below for reference.

### Why this fixes the wedge

The wedge is fs's single-goroutine serve loop blocking ALL IPC requests while `handleLoadFile` reads 6-35MB plugins. The asyncBlockDev mutex held across the entire 600ms read amplifies it: even if fs were multi-goroutine, every read would still serialize on `d.mu`.

Two changes that together unblock small ops:
1. **ext2 `sync.RWMutex`**: makes concurrent readers safe at the FS-state layer. (Reads are already safe per-buffer; this is correctness for future writes.)
2. **asyncBlockDev per-chunk lock release**: a small fsIPCCh op (1-3 blocks) waits at most one chunk-time (~3ms) before squeezing in between a LoadFile's chunks.

A separate follow-up will spawn a goroutine for LoadFile/ReadFilePages so the fs main loop can keep serving fsIPCCh while a big read is in flight.

### Implementation order

**Step 1 — ext2 `mu sync.RWMutex` field**
- Add `mu sync.RWMutex` to `FileSystem` struct in `shared/fs/ext2/reader.go`.
- One line. No behavior change yet.

**Step 2 — wrap write methods with `Lock()`**
File: `shared/fs/ext2/writer.go`. Wrap entry of:
- `WriteInode`
- `Create`
- `WriteFile`
- `Mkdir`
- `Remove`
- `Rename`
- `SetTimes`
- `SetMode`
- `Truncate`
- `Sync`

File: `shared/fs/ext2/pin.go`:
- `reclaimInode` (called from `UnpinInode`)

Internal helpers (`allocBlock`/`freeBlock`/`allocInode`/`freeInode`/`setInodeBlock`) are **not** wrapped — they're only called under a held write lock. Add a comment noting "caller must hold fs.mu" on each.

**Step 3 — wrap read methods with `RLock()`**
File: `shared/fs/ext2/reader.go`:
- `ReadInode`
- `ReadDir`
- `LookupDir`
- `ResolveBlockList`
- `OpenInum`
- `Open`

File: `shared/fs/ext2/writer.go`:
- `ResolveInode` (read-only, lives in writer.go for historical reasons)

File: `shared/fs/ext2/file.go`:
- `File.Read`
- `File.ReadInto`
- `File.Seek` (touches f.pos only; safe but lock for consistency? Actually no — f.pos is per-File state; lock is unnecessary. Skip.)

Internal helpers (`readBytes`/`readBlock`/`readBlocks`) are **not** wrapped.

**Step 4 — verification sweep (no asyncBlockDev change yet)**
Build and run 10×60s ARM64 HVF. Expected outcome: wedge rate similar to baseline (Z-sweep had ~30%). RWMutex alone shouldn't change wedge rate; we're verifying no regression.

**Step 5 — asyncBlockDev per-chunk lock + scratch sanity check**
File: `maz/fs/main.go`.

(a) Move `d.mu.Lock()`/`Unlock()` from `ReadBlocks` (line 646-648) into `doReadBatch`'s chunk loop:

```go
func (d *asyncBlockDev) doReadBatch(blocks []uint32, dst []byte) error {
    total := uint32(len(blocks))
    for blockIdx := uint32(0); blockIdx < total; {
        d.mu.Lock()
        // existing chunk body (lines 534-614, inclusive of the SQE-fill /
        // IOUringEnter / CQE-drain / copy-from-scratch)
        d.mu.Unlock()
        blockIdx += batch
    }
    return nil
}
```

`ReadBlocks` then no longer wraps `doReadBatch` with mu — pass through directly.

(b) Add scratch-buffer trip-magic sanity check inside the chunk-locked region. For each non-sparse SQE submitted: write 16-byte magic to first 16 bytes of the scratch slot BEFORE `IOUringEnter`. After the drain succeeds, verify those 16 bytes WERE overwritten (not equal to the magic). If they're still the magic, the kernel didn't actually write data into that slot → log + return EIO.

```go
const dmaTripMagic uint64 = 0xDEADBEEFCAFEBABE
// ...inside the per-SQE submit loop (after writing the SQE and before mu unlock):
*(*uint64)(unsafe.Pointer(d.scratchVA + off)) = dmaTripMagic
*(*uint64)(unsafe.Pointer(d.scratchVA + off + 8)) = uint64(sectorLBA)
// ...after the CQE drain succeeds, before the copy-out:
got := *(*uint64)(unsafe.Pointer(d.scratchVA + off))
if got == dmaTripMagic {
    sys.UartWriteString(fmt.Sprintf(
      "[dma:TRIP] slot=%d lba=%d magic still present after drain — DMA didn't fire\n",
      i, sectorLBA))
    d.mu.Unlock()
    return syscall.EIO
}
```

This catches: DMA targeting wrong PA, IRQ-wake-without-DMA, cache-invalidate misses. Cost is 16 bytes per slot per chunk, negligible.

**Step 6 — final sweep**
10×60s ARM64 HVF. Expected outcomes:
- `realStuck=N` (count of `delegate stuck:` ≥100ms entries) drops to near zero
- `[linux:wkr] SLOW` count drops or disappears
- `[dma:TRIP]` lines do NOT appear (confirming the per-chunk lock is correct)
- No new error log lines from ext2

**Step 7 — commit each step**
- Commit 1: Step 1 + Step 2 + Step 3 (ext2 RWMutex, all together — they don't make sense individually)
- Commit 2: Step 5 (asyncBlockDev per-chunk + trip magic)

**Step 8 — defer follow-up**
A separate commit (next session): spawn a goroutine pool for LoadFile/ReadFilePages on `fsDelegateCh` so fs serve loop can keep serving fsIPCCh while a big read is in flight.

### Risks (flagged from audit + plan review)

1. **`fs.sb` mutable fields**: writers mutate `FreeBlocksCount`, `FreeInodesCount` while readers might read them. Today readers don't touch those specific fields; RWMutex makes this future-proof.
2. **Cross-chunk cache coherency**: each chunk's pre-DMA invalidate (kernel side) covers its own region; scratch buffer fully consumed-then-released within one chunk's lock window. Verified safe.
3. **SQ/CQ ring state across chunks**: each chunk leaves zero in-flight (drains N after submitting N). Verified by reading kernel `SyscallIOUringEnter`.
4. **`File` per-handle state**: not lock-protected. If callers pass same `*File` to two goroutines, races already exist; we don't fix or break them. All current callers open fresh `*File` per request — safe.
5. **DMA delivery silent-fail**: trip-magic detects this in Steps 5/6.

### Success criteria

- 10/10 runs without `realStuck≥100ms` delegates
- `[dma:TRIP]` count = 0 across all sweeps
- No new ext2 error log lines (`ErrCorrupted`, `ErrReadFailed`)
- No new kernel crashes unrelated to bug-B family

---

## ARCHIVED: concurrent-boot-wedge localized to linux file-lane (2026-04-30 late morning)

After M-cadence sweep + A1 misdiagnosis (below), pivoted to investigate the N5 wedge (3 shepherds parked on Readlinkat ~48s). 5 follow-up sweeps (W/X/Y/Z/AA, ~50 runs total) localized the wedge to **between kernel ring 1 send and linux's file-lane reader's `case sys.SyscallRequest` branch**.

### Z2 (the cleanest captured wedge)

3 stuck Readlinkats from sids 2, 12, 14 (TIDs 85/716/695). Same TIDs successfully completed Openat→Read→Close earlier (logged in `[linux:flr]`). The (sid, 50) tuple never reached the file-lane reader. Kernel-side `uringSendKernel(ringIdx=1)` succeeded (`svc/delegated:50=3`, no `[DLG] uring send failed`). So the requests sit in kernel ring 1 → linux dispatcher → `delegateCh` (cap 8) → file-lane reader pipeline, somewhere upstream of the case-branch where my instrumentation fires.

### Key ruled-out paths

- Per-shepherd `mu` — 3 different SIDs, no contention.
- Worker exhaustion — Z2 `wIn=wOut=9`, all workers return cleanly.
- Kernel-side send failure — no `[DLG] uring send failed`.
- Sysid-specific bug — fires on Openat (X3, X9), Readlinkat (N5, Z2), Fstatat (L-sweep). "First delegated syscall in a boot race window."

### Most likely remaining hypothesis

The file-lane reader (`maz/linux/main.go:276-302`) iterates `for raw := range delegateCh`. Between iterations, if a `deathNotification`/`idleFlushNotification`/`stdinDecRefNotification` is dispatched and its handler (`handleDeathNotification`, `flushOneBuffer`, or `sidDecRef`) blocks, the reader stops draining `delegateCh`. The dispatcher callback then blocks on `delegateCh <- req` (cap 8 fills), but `delegateDispatcher` is single-goroutine — it stops processing ring 1 messages, even though kernel keeps sending. The 3 stuck Readlinkats are queued in kernel ring 1 with no consumer.

The N5 wedge happened during rachel-plugin-load (helloworld exits, plugins launch concurrently) → `deathNotification` traffic spike. Strong correlation with `handleDeathNotification` blocking.

### Next concrete step

Instrument the non-`SyscallRequest` case branches in `maz/linux/main.go:276-302`:

```go
case deathNotification:
    fmt.Printf("[linux:flr-death] enter sid=%d\n", v.deadSID)
    handleDeathNotification(v.deadSID)
    fmt.Printf("[linux:flr-death] exit  sid=%d\n", v.deadSID)
case idleFlushNotification:
    fmt.Printf("[linux:flr-idle] enter\n")
    handler.flushOneBuffer()
    fmt.Printf("[linux:flr-idle] exit\n")
case stdinDecRefNotification:
    fmt.Printf("[linux:flr-decref] enter sid=%d\n", v.sid)
    sidDecRef(v.sid)
    fmt.Printf("[linux:flr-decref] exit  sid=%d\n", v.sid)
```

A wedge run with `enter` and no matching `exit` for one of these branches names the culprit. Then audit the relevant function for its blocking path.

### Open mini-bugs found along the way

- **44k delegated syscalls/sec on ring 1 normally** — surprising, much higher than `svc/delegated` counters track. Worth investigating separately whether this volume is necessary or a regression.
- **`time.NewTicker` SIGSEGV in linux.maz init** — `internal/godebug.(*Setting).Value` returns a nil pointer in .maz binaries (init functions don't run, see MEMORY.md). Worked around by NOT using `time.NewTicker` in linux. The existing `time.After` at `main.go:734` works because it's in the running goroutine, not init. Worth a memory file entry — anyone reaching for `time.NewTicker` in .maz code will hit this.

### Instrumentation left in tree (uncommitted)

- `maz/linux/main.go` — `numWorkingWorkers`, `firstSeenPairs sync.Map`, `firstSeenStage()`, `[linux:flr]/[linux:wkr]` first-occurrence-per-(sid,sysid) logs (~50 lines/run).
- `kmazarin/kmazarin/threads.go` — `[status] blk: ... emptySnap=N/raw=R/last=L` (A1 work, morning).
- `mazarin/mancini/std/draw.go` — A2 NEGATIVE one-shot `debug.Stack()` (didn't fire).

---

## ARCHIVED: M-cadence no-click sweep + emptyIRQ misdiagnosis (2026-04-30 morning)

Returned to systems debugging. Ran 5×180s ARM64 HVF no-click sweep (M1–M5), categorized open issues, attempted A1 (emptyIRQ), discovered the documented hypothesis was wrong.

### Sweep summary

4/5 clean to 180s, 1/5 (M4) `KERNEL EXIT GROUP` at `[mail] cache ready, initial rebalance` — bug-B-family. Counter signature consistent across clean runs: `irqs≈33000 drained=66552 emptyIRQ=496–557 missed=0 tmoBlkNE=0 uart-ring: dropped=0 va-probe: inIPC=132 outIPC=0`.

### Categorization (no-click only)

| Category | Item | Status |
|----------|------|--------|
| A: clear+fixable, every-run | A1 emptyIRQ counter (memory said missing barrier) | **DISPROVEN — barriers already present, raw==last on snap; not a bug** |
| A: clear+fixable, one-run   | A2 `[localRect] NEGATIVE` (iy1=2481568 against 828x438 bounds) | open, low priority |
| B: intermittent             | B1 bug-B kernel mspan/GC EXIT_GROUP | 1/5 (~historical 1/5), mechanism unknown after 7 rounds |
| C: not bugs in this sweep   | concurrent-boot-wedge, attr.Init, mail.elf-load, fsclient TIMEOUT, maildb EAGAIN, fontsvc errors, ≥1s delegate-stuck | 0/5 fires (concurrent-boot-wedge likely click-driven) |

### A1 attempt — what we learned

Pre-attempt code review found `asm.InvalidateDCacheRange + asm.DmaRmb + asm.MmioRead16` already present in both `NonTimerIRQTopHalf` and `VirtqueueHasUsed`. The "missing barrier" claim in `bug_virtio_emptyirq_hang.md` was wrong.

**Diagnostic added** (uncommitted): `[status]` `blk:` line now reports the existing one-shot `dbgBlockEmptyRawUsedIdx`/`dbgBlockEmptyLastUsedIdx` snap as `emptySnap=N/raw=R/last=L`. Edit in `kmazarin/kmazarin/threads.go`.

90s smoke result: `emptySnap=1/raw=72/last=72`. **raw == last** — when first emptyIRQ fired, we had already drained everything. Benign re-IRQ (probable INTx level-trigger sharing or coalesced-completion duplicate), not a missed completion. Memory file `bug_virtio_emptyirq_hang.md` rewritten to reflect this.

The 10s body-fetch hang is therefore a different bug, still open. emptyIRQ is mostly cosmetic noise.

### Open from this sweep

1. **Click sweep (next)** — 5×180s with input. Will likely surface concurrent-boot-wedge (0/5 here vs 3/5 historical L-sweep) and the actual body-fetch hang mechanism.
2. **A2 localRect NEGATIVE** — small standalone bug, find the attribute feeding `iy1=2481568` and the layout site that consumed it without bounds-check.
3. **B1 next concrete step (carried over from prior session)** — boot-only run with `vaCollisionProbeEnabled=true` to capture probe data on a crash run.
4. **emptyIRQ realMissed counter** — extend the one-shot snap into a running counter incremented when `raw > last`. Confirms emptyIRQ is fully benign or finds the rare miss.

---

## ARCHIVED: website updates pivot (2026-04-29)

`fix/concurrent-boot-wedge` partial-fix landed and merged; the underlying boot wedge is documented but not fully resolved (audit was incomplete — see ARCHIVED section below).

---

## ARCHIVED: `fix/concurrent-boot-wedge` — partial fix, wedge persists (2026-04-29)

**Branch:** `fix/concurrent-boot-wedge`, off `fix/uring-missed-retries@5352357` (merged from `diag/mail-elf-load-hang`).

**Symptom:** ~1-in-5 of 180 s ARM64 HVF boots, three shepherds (whichever happen to be in startup at the same moment) get stuck on their per-shepherd `shep.mu` for 60–110 s. The lock holder is parked in `fsclient.callLocked` waiting on a fs reply that doesn't come for an extended period. Subsequent same-shepherd syscalls (notably stateless `Readlinkat`) queue behind it and surface as `delegate stuck: tid=X/sid=Y/sysid=50/for=60000ms+`. Other shepherds proceed normally — the system as a whole stays alive (worker pool from `ef449b5` ensures this).

**NOT mail-related.** Mail is just one possible victim (when it's in the wedge window). H5's wedged shepherds were sid=20/28/29 — none was mail; they were rachel-plugin-load shepherds (fontsvc / prefs / keymapper / linux-ui / helloworld). The wedge fires whenever fs is busy with a slow op (typically a multi-MB LoadFile read off blockdev) while several shepherds simultaneously try fs operations through the shared `fsclient.Client.mu`.

**Audit completed 2026-04-29.** Hypothesis 2 (reply silently dropped) is confirmed by source inspection. Hypothesis 1 (fs serve-loop bottleneck) is weakened by K1 (fs was alive serving IPCs throughout the 148 s wedge).

### Mechanism (confirmed by source)

1. Linux's ring-0 dispatcher (`mazarin/uring/reader.go:198`) does a **blocking** `route.ch <- typed` to one of four destination channels: `wmCh` (cap 8), `fontReplyCh` (cap 8), `delegateCh` (cap 8), `fsClient.RespCh` (cap 4).
2. If any of those channels fill (most plausibly `wmCh`/`fontReplyCh` during a boot WM/font traffic spike), the dispatcher reader blocks, stops draining ring 0.
3. New messages from any sender (rachel, fontsvc, fs) accumulate in the kernel-side ring 0.
4. When the kernel ring fills, `uring.Send` returns `EAGAIN` after the 30 ms pacing budget (`fix/uring-missed-retries`).
5. **`maz/fs/fsipc.go:178-183`'s `respond()` logs and DROPS the reply on any error** — including EAGAIN. No retry.
6. Linux's worker parked on `<-fsClient.RespCh` waits forever.
7. Worker holds per-shepherd `shep.mu`. Subsequent same-shepherd syscalls queue. `delegate stuck`.

This is consistent with `fix/uring-missed-retries`: pre-fix, `uring.Send` retried forever via Gosched; the drop-on-error path in `respond()` was effectively unreachable. The new bounded backpressure made it reachable, and the wedge surfaced.

### Channel-depth rationalization

| Channel | Cap | Producer→Consumer | Justified? |
|---------|-----|-------------------|------------|
| `RespCh` | 4 | dispatcher → `callLocked` (1-in-flight via `c.mu`) | NO — at most 1 ever in flight by construction |
| `wmCh` | 8 | dispatcher → linux→linux-ui forwarder | bursty, plausible |
| `fontReplyCh` | 8 | dispatcher → font reply forwarder | bursty, plausible |
| `delegateCh` | 8 | dispatcher → file-lane reader → workCh(1024) | drains fast post-phase-2 |

The buffers were defensive scaffolding. They don't fix the consumer-stall they were meant to absorb — they just delay the cascade by N messages. Smaller buffers surface the bug faster; bigger ones hide it longer in tests.

### Final state (2026-04-29 evening)

**Landed permanent fixes** (in `86e2482`):
- Bounded retry on EAGAIN in `fs.respond()` (`maz/fs/fsipc.go`). 100 × 30 ms = ~3 s budget. Distinguishes EAGAIN (retryable) from other errors (terminal). Real bug — pre-`fix/uring-missed-retries`, `uring.Send` retried forever via `Gosched`; the new bounded backpressure made the silent-drop path reachable. The retry is unconditionally correct defense-in-depth.
- `fsclient.Client.RespCh` shrunk from cap 4 to cap 1. By construction (`c.mu` serialization) at most 1 in-flight call exists; cap 4 was arbitrary defensive padding.

**TEMP-DIAGNOSTIC items REMOVED before merge** (per the MANDATORY EXIT CRITERION):
- 30 s timeout + stale-reply drain in `fsclient.callLocked`.
- `[fs:serve]` per-iteration instrumentation in fs's serve loop.
- `[fsclient:TIMEOUT]`, `[fsclient:STALE-DRAIN]` log lines.

**L-cadence sweep results** (5 × 180 s after fix landed but before TEMP removal):

| Run | Outcome | Detail |
|-----|---------|--------|
| L1 | wedge | sid=1 (rachel) tid=63 Fstatat 155 s — NO `[fsclient:TIMEOUT]`, NO `[fs:ipc]` errors. Worker not in `callLocked`; bug is upstream of fsclient. |
| L2 | clean 161 s | |
| L3 | clean 164 s | |
| L4 | wedge | sid=9 tid=238 Fstatat 156 s — same signature as L1 |
| L5 | severe | boot incomplete (0 shepherds main entered), 3 shepherds wedged ~116 s, timeout DID fire, stale-drain DID drop a reply with mismatched reqID |

3 wedges in 5 runs (60%) — wedge rate did NOT improve with the fix. Conclusion: the EAGAIN-drop is real but is NOT the sole cause; the audit was incomplete. The wedge has at least one other failure mode upstream of fsclient (likely in linux's ring-1 dispatcher path → file-lane reader → worker pool pipeline) that we haven't located.

**Why we paused:** the user moved focus to website updates. The partial fix is correct on its own merits and is being merged. The remaining bug is documented for resumption.

### Reminders for future resumption

- The audit found a real EAGAIN-drop bug in `fs.respond()` — fixed and merged.
- The wedge symptom signature (`delegate stuck: tid=X/sid=Y/sysid=44/for=N+ms`) for tens of seconds with NO `[fs:ipc]` errors and NO timeout firing suggests the request never reaches a worker, OR the worker is parked in something other than `callLocked`.
- Next concrete step (saved): add worker-entry/exit instrumentation in `handler.handle` (sid+sysid+reqID, enter/return) to find whether wedged requests reach a worker at all.
- If they don't reach a worker, look at `delegateDispatcher.OnFunc(ipc.ProtoFSDelegateReq, sys.DecodeFSDelegateReq, ...)` in `maz/linux/main.go` and the `<-` to `delegateCh` for silent drops.
- Do NOT roll back phase 2 (worker pool) or shepherd unification — those are stable.

### Side context

- This wedge is independent of the original mail.elf-load DIVERSION (still hasn't fired under phase 2).
- Channels `wmCh`/`fontReplyCh`/`delegateCh` left at cap 8 — their consumers are tight forwarder loops; shrinking adds little value.
- `bug_attr_init_crash.md` is a separate constraint-VM bug seen in I1; not related.

---

## ARCHIVED: `diag/mail-elf-load-hang` — linux per-request goroutines + fti.maz + shepherd unification (2026-04-29)

**Branch:** `diag/mail-elf-load-hang`, off `fix/uring-missed-retries@e7422c5`. Now merged into `fix/uring-missed-retries` (fast-forward to `5352357`). 8 commits.

**Commits:** `f466010` (linux per-request goroutines + fti.maz + checkpoints), `6ae4d63`, `37f1956` (pageCache.data race fix), `ef449b5` (1024-worker pool), `c0c2b04`, `e9247bc` (mail.maz migration), `aabdfa2` (drop dual-builds + delete launchShepherd legacy body), `5352357`.

**What was done:**
- **fti.elf → fti.maz migration (stage 1 of "ONE shepherd binary" cleanup).** Dual-build pattern in `maz/fti/Taskfile.yml` mirroring maildb's. `MazarinMain` shim added to `maz/fti/main.go`. `Taskfile.yml` root + `disk-arm64`/`disk-x86_64` updated. `config/startup.{arm64,amd64}.toml` flipped to `/fti.maz`.
- **Linux dispatcher concurrency.** Migrating fti to .maz exposed a head-of-line block: 3 shepherds stuck on `Readlinkat` for 110+s because linux's file-lane was a single goroutine and one slow `fsclient.call` queued every other delegated syscall behind it (even stateless ones). Fix: file lane spawns `go handler.handle(req)` per `SyscallRequest`. Per-shepherd ordering preserved by `ShepherdFilesystemData.mu`. Cross-shepherd state (`syscallHandler.shepherds`/`orphanHandles`, `pageCache`, `flockTable`) gets short-held mutexes. `FlushAllPagesForInum/SID` converted to snapshot-then-write so `pc.mu` isn't held across fsclient. Notifications (death/stdinDecRef/idleFlush) stay on the reader. `fsclient.Client` was already self-locked.
- **Launch-path checkpoint instrumentation** (separate, supports the original DIVERSION). `kmazarin/ksyscall/runshepherd.go` gained post-unmap / post-FB+constraint-map / pre-loadELF checkpoints. `maz/fs/main.go` gained post-Open / pre-ReadInto / read-done / calling-RunShepherd checkpoints in `launchShepherd`/`launchPluginShepherd` and in `readFileIntoPages`. One-shot per launch.

**Verified across 5×180s G-/H-cadence sweep + initial G2 boot test:**

| Run | Outcome | Notes |
|-----|---------|-------|
| G2  | clean 171s | initial phase-2 boot test (unbounded `go ...`) |
| G3, G4, G5 | clean | unbounded `go ...`, transient ms-scale stalls |
| G6  | SIGSEGV | direct unsynced read of `h.cache.data` — fix `37f1956` |
| H1  | SIGSEGV | runtime unwinder under goroutine churn — fix: worker pool `ef449b5` |
| H2  | steady state then `bug_attr_init_crash` | pre-existing constraint-VM bug, unrelated |
| H3  | clean 180s | numGoroutine=1041 stable |
| H4  | clean 180s | numGoroutine=1041 stable |
| H5  | mid-boot stall | 3 shepherds wedged on per-shepherd lock; underlying fs-reply wedge — see "Not yet done" #3 |

Two real bugs caught and fixed during the sweep: (1) `pageCache.data` direct map access bypassing `pc.mu` in `sysMmapPageFlush`'s diagnostic block (commit `37f1956`); (2) unbounded `go h.handle(req)` was generating ~14k goroutine spawns per 180s run, exposing a runtime crash in `traceback.go:resolveInternal` during copystack of freshly-spawned workers (likely interacting with goroutine leakage when fsclient.call wedged) — replaced with 1024-worker persistent pool (commit `ef449b5`).

**Active plan:**

### A. Shepherd unification — DONE

Stage 2 (`mail.elf` → `mail.maz`, commit `e9247bc`) and stage 3 + cleanup (drop dual-builds + delete `launchShepherd` legacy body, commit `aabdfa2`). All shepherds in startup.toml are now `.maz` plugins launched via `/shepherd.elf`. Disk ships only `.maz` for fti / maildb / mail (plus rachel / linux / fontsvc / linux-ui etc. that were already plugin-only). `launchShepherd` in `maz/fs/main.go` is a single-path function — the legacy ET_EXEC body is gone.

**Verified:** I1 reached full mail-app steady state then hit the pre-existing intermittent attr.Init/exit_group family (unrelated to migration). I2 clean 154s+. J1 (post-cleanup) clean 172s. Mechanically working.

### B. Underlying fs-reply wedge — triage (DEFERRED — pick up after A)

Reproduced in G-fti-maz-1 (3 readlinkats stuck 110s) and H5 (3 shepherds wedged 60s+, sid=20/28/29). Same signature: a worker holds its per-shepherd `shep.mu` waiting for an fs reply that never comes; subsequent same-shepherd syscalls queue. ~1-in-5 rate at 180s. Phase 2 + worker pool prevents system-wide impact (other shepherds continue) but the wedged shepherds hang.

**Hypotheses to investigate:**
1. **fs's single-goroutine serve loop is the bottleneck.** While fs is mid-read on one large file (e.g., 23 MB LoadFile), it can't process incoming `fsIPCCh` requests. If a linux worker is doing fsclient.call (which goes through fs's `fsIPCCh`), it waits for fs's serve loop to come around. If the serve loop is mid-LoadFile for many seconds, the linux worker waits many seconds. Backed by H5's wedge appearing during the rachel-plugin-load phase when fs is reading rachel.maz / fontsvc.maz / etc.
2. **Reply was sent but linux's RespCh demuxer dropped it.** fsclient.Client.RespCh has capacity 4. If multiple replies arrive faster than the dispatch reader pumps them, kernel-side ring fills. Our recent uring fix gives 10ms block-with-deadline; after that the sender sees EAGAIN and the request is lost (no retry on fsclient side). Audit fsclient for "uring.Send EAGAIN swallowed" paths.
3. **Cleanup-on-disconnect race.** If a shepherd dies during fs IPC, fs might tear down state without replying to in-flight requests.

**First steps when picking this up:**
- Add timeout to `fsclient.callLocked` (e.g. 30s). On timeout, log + return error so the worker unwedges.
- Add an instrumentation log in fs's serve loop: when does it pick up an `fsIPCCh` message vs an `fsDelegateCh` message? Are LoadFile delegate operations starving IPC requests?
- Reproduce H5 deterministically (it appeared during heavy concurrent boot LoadFiles — should be reproducible by adding more shepherds to startup.toml or by making the launches more parallel).

**Reminders:**
- The original DIVERSION (mail.elf-load boot hang) hasn't fired in any phase-2 run. Could be: (a) related to the same delegate-saturation pattern that phase 2 fixes, (b) instrumentation perturbed timing, (c) intermittent and we got lucky. Instrumentation is in place for next time.

---

## ARCHIVED: `fix/uring-missed-retries` — kernel-side block-with-deadline (2026-04-28 night)

**Branch:** `fix/uring-missed-retries`, off `feature/mail-dumb` at `68a7254`. Now committed (b907e8d kernel, 4ba1a15 userspace, e7422c5 docs). `diag/mail-elf-load-hang` builds on top.

**Why:** F1-F15 chase identified the root cause of `OpenFontReply EAGAIN`: synchronous UART writes (`klog.Criticalf`, etc.) run with ARM64 SVC's hardware-default DAIF.I=masked, and on `-smp 1` QEMU this IRQ-blocks the entire system for ~7 ms per call. Receiver's userspace reader gets starved → ring fills → sender gets EAGAIN. See `memory/sync_uart_irq_masked.md` for the architectural write-up.

**Architecture (agreed with user):**
- **Kernel-side block with deadline** for both `SyscallUringSend` and `pushStringFull` (`topHalfUartRing` push). 10 ms ceiling, woken early by drainer; deadline expiry surfaces EAGAIN cleanly.
- **First-come-first-served, single blocker per slot.** Second sender returns -EAGAIN immediately.
- **Userspace pacing**: 3-attempt retry in `mazarin/uring/syscall.go` with `nanosleep` (real deadline-queue block, not yield) when a previous attempt returned faster than the per-attempt deadline. No `runtime.Gosched()` anywhere — one was found in `pushStringFull`, removed; userspace had a 256× Gosched retry, removed.
- **`Send`/`Recv`/`Connect`** are one-line ring-0 wrappers; `*WithRing` are the primitives.

**Status:** Steps 1-7 done. Builds clean. F16-F20 sweep: 3 clean reached-steady-state runs (F16 162s, F17 171s, F19 133s) + 2 pre-existing mail.elf-load boot hangs (F18, F20 — same site as B2/B5, A2, C1/C5; not from this branch). Across all 5: 0 retried, 0 FAILED, 0 fontsvc errors, 0 uart-ring dropped, 0 panics. **Fix verified at F-cadence; ready for commit pending diff review.** Uncommitted. See `progress.md` for per-step detail.

- Step 1 (DONE): `ThreadBlockedUringSend` / `ThreadBlockedKernelRingPush` states + `Thread.UringSendBlockedSlotPtr` / `UringSendDeadlineExpired` / `UringIPCSlot.BlockedSenderTID,BlockedSenderPtr` data shape.
- Step 2 (DONE): kernel uring block path — `UringSendKernel` parks userspace senders with 10 ms deadline, single blocker per slot, race-free publish under `schedulerLock`, deadline-expired short-circuit on rewind, `WakeSenderAfterDrain` from `SyscallUringRecv`, `processStaticDeadlinesSchedLockHeld` deadline branch, `CleanupUringIPCForShepherd` wakes blocked senders on receiver death.
- Step 3 (DONE): same block-with-deadline pattern for `pushStringFull` + `topHalfUartRing` consumer, dropped the last kernel `runtime.Gosched()`, added `pushBlockerThreadPtr` + `pushBlockerDeadlineExpired`, `[status]` line gained `uart-ring: dropped=N`.
- Step 4 (DONE, this session): `mazarin/uring/syscall.go` rewrite — `SendWithStats` removed; `SendWithRing` is the primitive with 3-attempt retry, conditional `time.Sleep(10 ms)` pacing (only when prev attempt fast-bounced in <8 ms), single `fmt.Printf` log on retry-success / exhausted failure, silent on first-try success; `Send` is a one-line ring-0 wrapper. Zero `runtime.Gosched()`.
- Step 5 (DONE, this session): `maz/fontsvc/main.go` reverted — `shareCacheAndReply` back to 4-arg signature, `uring.SendWithStats` → `uring.Send`, `OpenFontReply FAILED/retried` instrumentation gone, `itoaBytes` helper dropped, `rawPutsInt` restored to inline form.
- Step 6 (DONE prior session): `$GO tool task` clean; `run-arm64-hvf TIMEOUT=180` clean — kernel stable, `uart-ring: dropped=0`, no retry/FAILED log lines surfaced (idle no-click run).
- Step 7 (DONE 2026-04-29): F16-F20 5×180s ARM64 HVF no-click sweep. F16/F17/F19 clean reached-steady-state; F18/F20 hit pre-existing intermittent mail.elf-load boot hang (not a regression — same site as B2/B5/A2/C1/C5). Across all 5: 0 retried / 0 FAILED / 0 fontsvc / 0 dropped / 0 panic. Fix verified.

**Reminders / non-negotiables:**
- No `runtime.Gosched()` anywhere in this fix — by user policy: "yields cover up bugs that will bite later." Both kernel-side and userspace-side are now Gosched-free.
- No new architecture additions beyond the agreed scope without further discussion.
- Don't commit until user reviews the diff.

---

## DIVERSION: intermittent mail.elf-load boot hang (cross-session) — instrumentation landed, hang didn't fire under phase 2

**Status update (2026-04-29):** Phase-2 boot-test 171s clean — hang did NOT reproduce. Instrumentation (post-unmap / mapped FB+constraint / pre-loadELF in kernel; post-Open / pre-ReadInto / read-done / calling-RunShepherd in fs) is in place on `diag/mail-elf-load-hang@f466010` for the next reproduction attempt. May or may not still happen — phase 2's removal of the linux file-lane head-of-line block could have eliminated the trigger if it was related to delegate-dispatcher saturation. Track but don't actively chase until it recurs.

**Symptom:** Boot reaches `[fs] launching mail from /mail.elf` → `[fs] reading /mail.elf...` → optionally `[RS][RunShepherd] start name=mail pages=6644 bytes=27210767` and/or `[RS][RunShepherd] mail: copied 27210767 bytes from user`, then **silence**. No further log output for the full 180s timeout. No panic, no data abort, no `EE25` / `EXIT_GROUP`. No status lines ever print, so we have no telemetry from the hung run.

**Sites observed (independent of branch / instrumentation):**
- B2, B5 — Option B stale-PTE verifier session (2026-04-27 late afternoon)
- A2 — Option A trailing-`TlbiVMALLE1`-on-munmap session (same)
- C1, C5 — H-T3 free-canary session (2026-04-27 evening)
- F18, F20 — `fix/uring-missed-retries` step-7 sweep (2026-04-29)
- (Plus a baseline-no-instrumentation hit at "[fs] reading /shepherd.elf" earlier — likely the same family but at the prior shepherd in the launch chain.)

**Cross-cutting observations:**
- Hits ~1-in-3 to 1-in-5 of 180s ARM64 HVF runs. Independent of branch (seen on `feature/mail-dumb` baseline AND on this branch's tree).
- Always at the same point in the launch sequence: just after the kernel finishes copying mail.elf bytes and before mail-app's first goroutine runs / first `[mail] main() entered` log.
- Never produces any kernel-side panic or post-mortem print. The system is not dead — it's stuck somewhere that doesn't loop or fault, just doesn't progress.
- Has occurred with very different working-tree states (different probes / instrumentation / branch-specific changes), so it's not gated on any one of those.

**What we DO NOT know yet:**
- Whether it's mail-app userspace stuck during Go-runtime init / dynamic loading, or a kernel-side stall in the post-`copyPagesFromUser` / `loadELF` / first-thread-creation path.
- Whether `[uring:reader] ring1 got msg #2 proto=9` (which fires just before the hang in F18) is related — it appeared in F18 but NOT in F20 immediately before the hang.
- Whether disabling some of the launch-time instrumentation makes it more or less frequent (no controlled study yet).

**Why diverted, not paused:** The boot hang is NOT something this branch's work touches (`fix/uring-missed-retries` is about ring-full block-with-deadline, which doesn't run during ELF load), and it's NOT the bug-B-family target either (which is the mspan-corruption / GC-crash family that fires AFTER mail-app reaches steady state). It's a third, independent, pre-existing issue that's been hiding in the background.

**Plausible candidates (not yet investigated):**
1. **`preGrowStack` interaction with mail.elf** — mail.elf is the largest binary in the launch chain (6644 pages = 27 MB). The preGrowStack workaround (`MEMORY.md` § ".maz Morestack Bug") forces stack to 64 KB; if the goroutine running `mazMain` for mail-app somehow doesn't go through that path on certain timings, the hang would match a stack-growth-into-bad-newstack lockup.
2. **Demand-paging stall during init** — large user-text region (>27 MB) means many demand faults early; if any single fault path can deadlock against the launcher, we'd see exactly this.
3. **Linux shepherd dispatcher-not-yet-ready race** — `[uring:reader] ring1 got msg #2` in F18 before the hang suggests linux shepherd is processing IPCs; if the ordering between "linux is up" and "mail-app starts making syscalls" can flip, an early mail-app syscall might wait for a service that's not registered yet.

**Next-action when this is picked up (revised 2026-04-29):**
- F-cadence reproduction attempt on `diag/mail-elf-load-hang@f466010` (or successor) — 5×180s ARM64 HVF, watch for the silence-after-mail-launch signature. The new checkpoints will say which silent gap the hang is in.
- If hang fires between `copied X bytes from user` and `unmapped N caller pages` → kernel `unmapUserPages` of 6644 pages.
- If between `unmapped` and `mapped FB+constraint` → `CreateProcessPageTable` / `MapUserFramebufferWithL0` / `MapUserConstraintPagesWithL0`.
- If between `mapped FB+constraint` and `pre-loadELF` → `buildSymbolTable` / `findHighestVA`.
- If between `pre-loadELF` and `loadELF ok` → `loadELF` itself.
- If between `loadELF ok` and `created userspace thread` → `CreateUserspaceThread`.
- If between `created userspace thread` and userspace-side `[mail] main()` → kernel never schedules the new thread, OR mail's runtime hangs before first syscall.
- For the fs-side (F18-style) variant: post-Open / pre-ReadInto / read-done split says whether `Open` returned, whether `ReadInto` was entered, and how far the 27 MB read got.

**Status:** Tracked, instrumented. Resume only if the hang recurs on the new branch.

---

## PAUSED: bug B family — VA-collision strongly disconfirmed, GC mspan crash didn't reproduce in 15 boots

**Branch:** `feature/mail-dumb`
**Last commits:** `3942ae8` (A+B font leak fix) → `8a64a92` (caller-first close + checkpoints) → `4460c14` (docs retarget) → `ca7f5f6` (kernel double-free / underflow / loop-progress guards) → `612ed58` (Option B stale-PTE verifier — H-T2 ruled out) → `c4684ad` (free-canary — H-T3a ruled out) → `8b91d34` (maildb console routing) → `24ee044` (Stage 3 page-cache probes) → `b039800` (Stage 4 prep — GOGC=5 + VA-collision probe) → `ade5319` (docs) → `459dab0` (gate VA probe — fix click-induced regression) → `2fbd078` (docs)

### Where we are (2026-04-28)

Stage 4 prep landed. GOGC=5 for mail-app verified active (`gc=6176` in 180s vs prior tens). VA-collision probe in `SyscallSharePages` produced **132 [fontslot:VA] entries from one boot — every VA in `0x500000xxxxxx` (IPC region), none in mail-app's Go heap (`~0xC000000000+`).** Probe was unconditional; clicking once after boot triggered a heavy SharePages burst during body render, the synchronous Criticalfs regressed the system to a kernel `runtime.throw()` / `exit_group` with no panic message visible. Probe now gated behind `vaCollisionProbeEnabled` (default false).

**Provisional read on VA-collision hypothesis:** weakened. The kernel's SharePages target VA picker is consistently picking from the IPC region — Go GC's `findObject` returns nil for non-arena pointers, so marking should never walk into 0x500000xxxxxx. One sample is not conclusive but the consistency is a strong signal. To fully rule out we need a crash run with the probe firing — see Stage 4 plan below.

### Where we are (2026-04-27 evening)

The font close cycle exposes an intermittent kernel bug that always corrupts an mspan struct field in mail-app's heap. Seven diagnostic rounds have ruled out:

- **Buddy double-free / RefCount underflow / unmapLoop hang** (`ca7f5f6` guards silent).
- **H-T2 stale PTE in another shepherd's PT memory** (`612ed58` Option B verifier silent across 5 × 180s, 184K–203K scans/run, 0 hits).
- **H-T1 (specifically: missing trailing TLB flush at `SyscallMunmap`)** — Option A reverted as a no-op (per-page `tlbiVAE1IS` already broadcasts in IS domain; trailing local `TlbiVMALLE1` is redundant).
- **H-T3a kernel write between `BuddyFreeTyped` and reuse** — `c4684ad` free-canary 5 × 180s, ~1.5M+ verifies aggregate, 0 hits, including a confirmed crash repro (C3). The corrupting write is NOT in the free→reuse window.
- **Page-cache audit (Stage 2 read-only)** — protocol invariants I1–I5 in `findings.md` all hold in mainline. Audit surfaced Suspects 5 and 1 for Stage 3.
- **Suspect 5 — `sysMmapPageFlush` `!inumKnown` fallback over-flush** — Stage 3 probe `[pageCache:FALLBACK_ALLFDS]` applied and ran 6 × crash-eligible runs (smoke + 5 × 180s). **0 fires.** Disproven.
- **Suspect 1 — `[pageCache:OVERWRITE]` same-VA coverage gap** — Stage 3 broadened probe (dropped `old.VA != va` predicate) applied same runs. **0 fires.** Disproven.

### Strongest lead: crash timing is locked to a single program point

Every crash across all sessions fires at exactly the same point:
```
[provider] populateSlot client=0 server=4 kind=1 cacheLen=49152 fontDataLen=53504
...
[mail] cache ready, initial rebalance first=-1 last=-1 vis=0
[mem:linux] heap=NkB ...
<crash>
```
This is the mail-app-specific font slot (server=4) being populated, followed immediately by the first collection rebalance. The crash fires during the GC sweep that follows this allocation spike — before any click-driven activity. This temporal lock is the strongest signal we have. Corruption is happening in (or caused by) the populateSlot IPC / initial rebalance window, and the GC discovers it on the very next sweep.

### Active hypothesis: kernel maps font-cache pages at a VA that collides with mail-app's Go heap

H-T3b (stale-handle write after reissue) is no longer the primary frame. The Stage 3 results, combined with the crash-timing lock, point toward a **VA collision** during `populateSlot server=4`: the kernel maps the 12 font-cache pages (49152 bytes) into mail-app's address space at a VA that overlaps Go's heap region. When the GC next sweeps the span that happens to live at that VA, it reads font-file bytes instead of mspan struct data → `nelems`/`nalloc` nonsensical → crash.

This would also explain D1's variant failure: kernel EL1 data-abort write-fault at `FAR=0x9000000` (UART), `ESR=0x96000045` (translation fault L1, write). Kernel page-table pages could themselves be the victim in that run rather than mail-app's heap.

**H-T1' (residual)** — TLB holding stale translation past `tlbiVAE1IS`. Hard to test. Lower priority now.

### Preliminary experiment: GOGC=5 for mail-app

Before Stage 4 probes, run 5 × 180s with mail-app's GC throttle lowered from default 100% to 5% (matching other shepherds). Rationale: if the crash still locks to the same `populateSlot server=4` + initial-rebalance point under aggressive GC, it confirms the corruption is bounded to what happens in that window — not a delayed discovery of an earlier event. File to change: `kmazarin/ksyscall/launch.go` — ensure `GOGC=5` is set for mail.elf (verify it isn't already; CLAUDE.md says shepherds get GOGC=5 but confirm the actual code).

### Stage 4 pivot options (in priority order)

**Option 1 (FIRST, IN PROGRESS) — VA-collision probe.** Implemented in `SyscallSharePages` as `[fontslot:VA]` log, gated by `vaCollisionProbeEnabled` (default false; flip to true in `kmazarin/ksyscall/mailbox.go` for boot-only runs). One boot's data shows all VAs in `0x500000xxxxxx` (IPC region). To finish: re-enable probe, run **boot-only** 5×180s (no clicks — render-time SharePages traffic regresses the system), capture VAs from any crash run. If a crash repros AND VAs remain in 0x500000xxxxxx → VA-collision at SharePages layer fully ruled out → move to Option 3.

**Option 3 (SECOND) — VirtIO DMA target-PA audit.** maildb reads BBolt pages from disk via VirtIO block. If the block driver's DMA descriptor references a PA that was freed and reissued to mail-app as heap, the DMA write would corrupt it. Audit: check whether the DMA target buffer PA is derived from a user-mapped VA (which could be freed mid-request), or from a stable kernel-allocated buffer. Focus on `kmazarin/kvirtio/block*.go` and the descriptor-setup path.

**Option 4 (THIRD) — heap-corruption forensics.** Record exactly which mspan bytes are corrupted and what they contain. The value that overwrites `nalloc` or `nelems` could identify the source (font-file magic bytes, PTE values, IPC header fields). Add a small patch to `runtime.(*sweepLocked).sweep` or a pre-sweep hook that dumps the raw mspan bytes when corruption is detected. This would narrow the mechanism without needing to catch it in the act.

**Option 2 (FOURTH) — H-T1' proper test.** ASID swap on munmap (force TLB invalidation for a specific ASID), or a same-VA read probe immediately post-`tlbiVAE1IS` to verify the invalidation actually reached the CPU's TLB. More invasive than the above; try only if Options 1–3/4 come back clean.

**Side issue surfaced 2026-04-28** — `[maildb] send to SID 29 failed: resource temporarily unavailable` flooded ~151× during fti shepherd launch in E2/E3. Source: `maz/maildb/mail_handler.go::sendMailMsg` (uring.Send EAGAIN). Could be pre-existing (was previously routed through `fmt.Printf` and possibly silent in heavy boot logs) or a new ordering issue between maildb and a freshly-launched mail-app. Audit before drawing further conclusions about Stage 4 results.

### Run plan

1. ✅ Baseline: 5 × 180s → 1 mspan crash, 1 boot hang, 3 clean.
2. ✅ Option B verifier (`612ed58`): 5 × 180s → 1 mspan crash, 2 boot hangs, 2 clean. **H-T2 RULED OUT** (0 hits).
3. ✅ Option A TLB flush at SyscallMunmap end: 5 × 180s → 2 mspan crashes, 1 boot hang, 2 clean. Crash rate unchanged. **Reverted** (no-op vs per-page `tlbiVAE1IS`).
4. ✅ H-T3 Stage 1 sentinel-byte canary (`c4684ad`): 5 × 180s → 1 mspan crash (C3, no clicks), 2 boot hangs, 2 clean (one click-driven). Canary 0 hits across ~1.5M+ verifies. **H-T3a RULED OUT.**
5. ✅ Stage 2 page-cache audit (read-only): protocol invariants I1–I5 hold in mainline; surfaced Suspects 5 and 1.
6. ✅ Stage 3 Suspect 5 + Suspect 1 probes: smoke + 5 × 180s → 2 crashes (D1 kernel EL1 abort, D2 mspan `nelems=341 nalloc=4024`), 3 clean. **0 probe fires across all runs.** Suspects 5 and 1 DISPROVEN.
7. ✅ Stage 4 prep — GOGC=5 plumbing + VA-collision probe. E1 (180s, probe ON, click) → 132 [fontslot:VA] entries all in 0x500000xxxxxx, then click→`KERNEL EXIT GROUP` regression. Probe gated. E2/E3 (180s each, probe OFF, no clicks) → 2 clean runs at GOGC=5 (mail-app gc=6176). Crash rate 0/2 within noise vs baseline 1/5.
8. **NEXT — boot-only 5×180s with `vaCollisionProbeEnabled=true`.** No clicks. Capture probe data from any crash run. Decision tree below.

### Reminders — diagnostic toggles in tree

- **Option B stale-PTE verifier** at `612ed58`. Default `stalePTECheckEnabled = false` in `kmazarin/kmem/stale_pte_check.go`. Telemetry on `[status]` line: `stale-pte: enabled scans hits`. Flip the var to true to re-enable for any future PT-memory diagnostic.
- **Free-canary** at `c4684ad`. Default `freeCanaryEnabled = false` in `kmazarin/kmem/free_canary.go`. Telemetry on `[status]` line: `free-canary: enabled fills verifies hits`. Flip the var to true to re-enable for any future free→reuse-window diagnostic.
- **VA-collision probe** at `b039800`/`459dab0`. Default `vaCollisionProbeEnabled = false` in `kmazarin/ksyscall/mailbox.go`. **Boot-only safe** — heavy SharePages traffic during body-render after click regresses the system to kernel exit_group. Logs `[fontslot:VA] caller=N target=M va=X type=T` per SharePages call.

### Active instrumentation (already in tree)

- `[munmap:FREED]` (kernel) — fires when a PD_SHARED IPC-region page returns to buddy. `8a64a92`.
- `[fontsvc:release]`, `[fontsvc:close] preRelease/postRelease/preSend/postSend/postReply` — `8a64a92`.
- `[provider:close] enter/preMunmapCache/postMunmapCache/preMunmapFont/postMunmapFont/preIPC/postIPC/exit` — `8a64a92`.
- `[BUDDY] DOUBLE FREE!` halt — `ca7f5f6`. Has not fired.
- `[kmem:UNDERFLOW]` — `ca7f5f6`. Has not fired.
- `[unmapLoop] enter/progress/exit` — `ca7f5f6`. Fires for ≥64-page frees.
- Older from `12e5f0d`: `[munmap:PARTIAL]`, `[pageCache:OVERWRITE]`, `[ftruncate:LEAK]`, `[kmem] BLOCKED:`. All silent.

### Superseded / closed hypotheses

- ~~`goFont.ParseTTF` over shared pages causing GC to walk kernel memory~~ — wrong mechanism. Go's `findObject` returns nil for non-arena pointers; marking sets bits, doesn't write mspan fields.
- ~~Buddy double-free / `releasePageByPA` RefCount race~~ — `ca7f5f6` diagnostic confirms neither fires before the crash.
- ~~`releaseTempSlot` TODO leak~~ — *fixed* in `3942ae8`.
- ~~`provider.CloseTemporaryFont` not unmapping~~ — *fixed* in `3942ae8`.
- ~~IPC-then-munmap close order~~ — *fixed* in `8a64a92`.

### Prior GC crashes — earlier hypotheses (now superseded)

The earlier `freeIndex is not valid` hypothesis (Hypothesis 1: `goFont.ParseTTF` over shared pages letting GC walk into kernel memory) was **wrong on the mechanism** (Go's `findObject` returns nil for non-arena pointers; marking doesn't write to mspan fields). Crash signature shifted from `freeIndex` to `sweep increased allocation count` and now fires with **no font activity at all** (boot-time cache rebalance). All evidence now points to the kernel page-free path, not the GC scan.

The mlog (maildb console routing) work earlier in the session is unrelated and untouched here.

### Active instrumentation (in `8a64a92`)

- `[munmap:FREED] sid=N va=X pa=Y preRefCount=R origOwner=O` — fires from `SyscallMunmap` and `unmapUserPages` when a page that was ever PD_SHARED returns to the buddy. `va >= 0x500000000000` (IPC region) and `wasShared` filter keeps this quiet on the happy path.
- `[fontsvc:release] idx=I srvID=S cacheVA=V cachePages=K` — entry of `releaseTempSlot`, before `mem.FreePages`.
- `[fontsvc:close] preRelease/postRelease/preSend/postSend/postReply idx=I` — around `releaseTempSlot` + `sendCloseTempFontReply` in fontsvc's close handler.
- `[provider:close] enter/preMunmapCache/postMunmapCache/preMunmapFont/postMunmapFont/preIPC/postIPC/exit fontID=N srvID=S cacheVA=V cacheBytes=B fontVA=W fontBytes=B` — checkpoint chain around mail-app's close path.
- Older instrumentation still in tree from prior session: `[munmap:PARTIAL]`, `[pageCache:OVERWRITE]`, `[ftruncate:LEAK]`, `[kmem] BLOCKED:` — all silent in current runs.

### Side-issues parked

- `[localRect] NEGATIVE: lw=354 lh=-2691673` rachel layout corruption — likely collateral from a prior crash. Reassess after kernel bug resolved.
- linux-ui console window not appearing in some runs — separate placement/z-order issue.
- `sysFtruncate` cache-discard leak (Bug A from prior session) — not implicated in current symptoms; defer.
- **Rachel-boot hang (race) — exposed by Option B verifier (2026-04-27)**: With `stalePTECheckEnabled=true`, runs B2 and B5 (out of 5) hung at the exact same point — last log line `[shepherd] loading /rachel.maz (sid=0)`, immediately after a 1599-page `unmapLoop exit` for the previous shepherd that loaded rachel.maz (sid=3 in B2, sid=20 in B5). Both saved at `/tmp/B{2,5}-filtered-180s.log`. Hypothesis: the verifier's PT walk reads `proc.ShepherdListInUse[i]` and `PageTableL0PA` without locking, racing with shepherd teardown / rachel-launch. Could be verifier-caused (use-after-free of an L0/L1 PA mid-teardown) or just a pre-existing race exposed by the verifier's added latency. Did NOT reproduce in baseline runs without the verifier. **To be investigated** — needed only if Option A's TLB flush also relies on similar concurrent walks, or if we re-enable Option B for further diagnostics.

---

## PAUSED: fstatat/sysid=44 hang — instrumentation in place

Three clean 180s runs, no hang yet (~1-in-5 expected). Decision tree in
`findings.md`. Resume after GC crash is fixed (don't layer changes on an
unstable baseline).

---

## PAUSED: stability bisect — did `b9fd57f` regress boot reliability?

After landing real temp-pool IPC (`b9fd57f`), 5 × 180s produced 5 distinct
failure modes (fti Fstatat hang, boot panics, `attr.Init: invalid shared page
header`). None touch the changed code. Resume after GC crash is fixed:

- **Stable** → b9fd57f exonerated; gremlins are pre-existing.
- **Unstable** → bisect b9fd57f off; if that stabilizes, split the slot-table
  redesign from the IPC client rewrite and re-apply selectively.

---

## Resumable: Console rewrite (foundation ready)

Grid scrollbar (item 1) is DONE (commit `cc230e5`). Console rewrite (item 2)
not started.

### Spec

- Same logic as the mail header grid's row machinery.
- Fixed row interactors over a 500-line ring buffer.
- Row count determined by viewing-area height — stack full rows only, never
  partial.
- Switch console rows to **DynamicLabel** (drop `consoleLine` mono renderer).
- Exports same attrs as grid: line height, visible line count, total line
  count.
- Scrollbar same shape as grid scrollbar — reuse `GreaterI64Bool` /
  `ThumbFracPermille` / `NonnegSubI64`.

### Files to touch

- `mazarin/mancini/std/console.go` — rewrite to DynamicLabel, dynamic row
  count, 500-line ring buffer.
- `mazarin/mancini/std/console_frame.go` — NEW, analogous to `GridFrame`:
  NeuBox + Console + Scrollbar.
- Callers of `NewConsole` / `NewConsoleWithBox` — switch to `NewConsoleFrame`.

### Attrs to publish (mirror GridTable)

- `LineHeightAttr` — refreshed each Draw.
- `VisibleLineCountAttr` — full rows that fit, computed in Draw.
- `TotalLineCountAttr` — `len(content)`, capped at 500.
- `ScrollOffsetAttr` — lines from buffer start to first visible. Default
  tail-anchored.

### ConsoleFrame scrollbar wiring

```
scrollNeededAttr  = GreaterI64Bool(TotalLineCountAttr, VisibleLineCountAttr)
scrollMaxAttr     = NonnegSubI64(TotalLineCountAttr, VisibleLineCountAttr)
thumbFracAttr     = ThumbFracPermille(VisibleLineCountAttr, TotalLineCountAttr)
scrollbar.Visible             = EqualBool(scrollNeededAttr)
scrollbar.ValueAttr           = console.ScrollOffsetAttr   (shared)
scrollbar.MaxAttr             = scrollMaxAttr
scrollbar.ThumbFracPermilleAttr = thumbFracAttr
```

---

## Resumable: mail-dumb easy part (blocked on stability)

Once GC crash + stability bisect are resolved:

1. **Body display** — HTML body pane in the mail app. Requires temp-font
   fallback chain (`@font-face` → registered buffer → fontsvc OpenFont →
   default sans).
2. **PageUp/PageDown** — extend `GridTable.MoveSelection` / `ScrollBy`.
3. **Mark-read** — `MsgTypeMarkRead` IPC to maildb; update `Flags.IsRead`.
4. **Delete** — `MsgTypeMarkDeleted`; remove from displayed collection.
5. **Polish** — click→body fetch latency audit; prefetch tuning.

### Mail program deferred follow-ups

- Click→body fetch latency / prefetch-ahead audit (5 clicks → 12 body
  fetches; possibly excessive).
- maildb working set bounded check (~140 MB badger LSM; add periodic
  `[maildb:mem]` log).
- linux-ui transient fontsvc-boot wedge (not seen since uring.Send retry fix;
  watch).
