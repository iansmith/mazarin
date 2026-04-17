# Go 1.25.5 → 1.26.2 Migration — Continuation Plan

**Status:** Phase 2 in progress (2026-04-16). Safe to resume after restart.

This file is the single source of truth for the in-flight migration. It will
be deleted once Phase 5 (retirement of `bin/old.go.1.25.5`) lands.

## Environment (always export these first)

```bash
export GOTOOLCHAIN=auto
export GO=/opt/homebrew/bin/go   # stock homebrew Go 1.26.2
export QEMU=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-aarch64
export QEMU_X86_64=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-x86_64
```

Key stock reference trees:
- 1.25.5 stock: `/Users/iansmith/sdk/go1.25.5/src/`
- 1.26.2 stock: `/opt/homebrew/Cellar/go/1.26.2/libexec/src/`
- Legacy fallback (keep for bisection): `bin/old.go.1.25.5/bin/go` (Go 1.24.4)

## Phase status

| Phase | Status | Notes |
|-------|--------|-------|
| 0. Revert in-place malloc.go in `bin/old.go.1.25.5` | ✅ done | `diff` against `~/sdk/go1.25.5` shows identical. Shepherds boot, maildb indexes 100 docs. No git impact (tree is gitignored). |
| 1. Switch `GO` to `/opt/homebrew/bin/go` | ✅ done | CLAUDE.md + `memory/feedback_go_binary_path.md` updated. Baseline failure captured. |
| 2. Rebase overlay hunks against 1.26.2 | 🚧 in progress | See detail below. |
| 3. gen-ast-stubs against 1.26.2 | ⏳ pending | Regenerate `build/shepherd-overlay/runtime/sys_linux_arm64.s`. |
| 4. Full validation matrix (ARM64 HVF / x86_64 / RISC-V) | ⏳ pending | Response test + stability test. |
| 5. Retire `bin/old.go.1.25.5` | ⏳ pending | Delete or move to `bin/_attic/`. |

## Hard rules (from MEMORY.md — never violate)

- Discuss architecture changes before implementing.
- **Never** set `asyncpreemptoff=1`. Async preemption stays enabled.
- **Never** set `GOGC=off`. GC must run. `GODEBUG=gccheckmark=1` always.
- Always `$GO tool task` — never bare `go build`.
- Always `$GO tool safe-serial-read` — never `cat`/`head`/`tail` the serial log.
- Always `run-arm64-hvf`, never `run-arm64` (TCG is 100x slower).
- Runtime customization lives in `runtime-patches/`, **not** in-place GOROOT edits.

## Phase 2 — rebase state

### Files already rebased

| File | Action taken | Notes |
|------|--------------|-------|
| `runtime-patches/syscall/syscall_linux.go` | `"internal/itoa"` → `"internal/strconv"`; `itoa.Itoa(fd)` → `strconv.Itoa(fd)` (line ~353) | Kernel overlay. Our delta replaces Syscall6 body to call `ksyscallDispatch`, so we do not need `internal/runtime/syscall/linux` import that stock 1.26.2 uses. |
| `mazarin/overlay/userspace/syscall_linux.go` | Same pair of edits | Userspace shepherd overlay. |
| `runtime-patches/diplomat-linux/syscall_linux.go` | Same pair of edits | Diplomat's syscall overlay. |
| `mazarin/overlay/userspace/runtime/maz_moduledata.go` | `t.Kind_&abi.KindMask == abi.Interface` → `t.Kind() == abi.Interface` at lines 143 and 257 | 1.26.2 removed `abi.KindMask`; `Kind_` field is now pure kind bits (no overloaded DirectIface flag). `t.Kind()` returns `t.Kind_` directly. |

### Current build failure after those fixes

`$GO tool task` now fails on the **kernel** (`kmazarin:arm64`):

```
# runtime
/opt/homebrew/Cellar/go/1.26.2/libexec/src/runtime/malloc_generated.go:6678:5: undefined: runtimeFreegcEnabled
/opt/homebrew/Cellar/go/1.26.2/libexec/src/runtime/malloc_generated.go:6678:31: c.hasReusableNoscan undefined (type *mcache has no field or method hasReusableNoscan)
/opt/homebrew/Cellar/go/1.26.2/libexec/src/runtime/malloc_generated.go:6680:8: undefined: mallocgcSmallNoscanReuse
...
```

**Root cause:** our kernel overlay replaces `runtime/malloc.go` and `runtime/mcache.go` with copies based on 1.25.5 content. 1.26.2 added new symbols
(`runtimeFreegcEnabled`, `mcache.hasReusableNoscan`, `mallocgcSmallNoscanReuse`,
`malloc_generated.go`) that the stock 1.26.2 runtime now depends on. Our
1.25.5-based overlays don't expose them, so unrelated 1.26.2 runtime files
fail to compile. Shepherds and userspace build fine because they don't use
the kernel-only `runtime-patches/{malloc,mcache}.go`.

All build logs are at `/tmp/phase2-build-*.log`.

### Files remaining to rebase (priority order)

Drift measured as `diff | wc -l` between 1.25.5 and 1.26.2 stock.

1. **`runtime-patches/malloc.go`** (552-line upstream drift; our delta is 1 constant)
   - Our delta: change `arenaBaseOffset` line to add arm64 and riscv64 terms
     (see existing overlay line 314), with a 4-line comment above it.
   - Approach: **do NOT wholesale `cp` the stock file and re-patch** (the user
     interrupted that approach). Instead: read the stock 1.26.2 `malloc.go`,
     find the anchor point, and apply the delta surgically via Edit so the
     diff review is small and focused.
   - Anchor in 1.26.2 stock: `src/runtime/malloc.go:314`
     `arenaBaseOffset = 0xffff800000000000*goarch.IsAmd64 + 0x0a00000000000000*goos.IsAix`

2. **`runtime-patches/mcache.go`** (63 lines drift; our delta ~24 injected lines replacing 10)
   - Our delta: in `refill()`, replace the sweepgen-check-and-uncache block
     with an expanded version. Diff 1.25.5 vs overlay to see exact hunk.
   - 1.26.2 added `hasReusableNoscan` field and `mallocgcSmallNoscanReuse`
     path — verify our overlay's altered `refill()` doesn't break the new
     refill-path assumptions. This is where the kernel build fails today.
   - Critical: examine 1.26.2's `mcache.go` + `malloc_generated.go` to see if
     our refill() tweak needs any new fields or fast paths.

3. **`runtime-patches/os_linux_arm64.go`** (3 lines drift; our delta ~4 injected lines)
   - Our delta: extends `archauxv` to parse custom auxv entries for heap bounds.
   - Trivial line-offset rebase.

4. **`runtime-patches/tagptr_64bit.go`** (0 lines drift — IDENTICAL)
   - No rebase needed; our overlay should still apply cleanly. Sanity-check only.

5. **`runtime-patches/cgo_mmap.go`** (0 lines drift — IDENTICAL)
   - No rebase needed.

6. **`runtime-patches/fds_unix.go`** (0 lines drift — IDENTICAL)
   - No rebase needed.

7. **`runtime-patches/traceback.go`** (150 lines drift)
   - Our delta (hunks 1-4): guards on `resolveInternal` to fix sp/fp when
     starting from `gp.sched`. Line-offset adjust.
   - Hunk 5: append path for `cgoTraceback`. The `cgoTraceback` symbol still
     exists in 1.26.2 (around line 1631 per the handoff), so the anchor
     likely still matches.

8. **`runtime-patches/preempt.go`** (103 lines drift — **HARDEST, behavioral**)
   - 1.26.2 added `internal/goexperiment` to the import set — re-merge.
   - 1.26.2 added `_Gleaked` and `_Gdeadextra` g-statuses. Verify
     kmazarin preemption code doesn't enumerate statuses in a way that
     misses these. Our `PreemptOffsets` struct exposes only `_Grunning`/
     `_Gscan`, so the struct itself is fine.
   - 1.26.2 tightened `wantAsyncPreempt` to require
     `mp.curg != nil && readgstatus(mp.curg)&^_Gscan != _Gsyscall`.
     Our `TryKernelAsyncPreempt` calls `wantAsyncPreempt(curg)` — confirm
     kernel goroutines in `_Gsyscall` aren't expected to be preempted
     (entersyscall already disables preemption, so this should be fine).
   - 1.26.2 added `asyncPreempt2` mcall wrapper switching to system stack
     and saving extended register state on the P. Injection still uses
     `abi.FuncPCABI0(asyncPreempt)` so same entry — but verify the mcall
     path doesn't break the frame we synthesize from timer IRQ. **Test:**
     trip a preemption from kernel timer IRQ on ARM64 HVF and confirm the
     goroutine resumes correctly.

9. **`runtime-patches/sys_linux_arm64.s`** (16 lines drift; wholesale-authored)
   - Per MEMORY.md, `GOEXPERIMENT_runtimesecret` blocks in walltime/
     nanotime1 are irrelevant because we replace those functions. But
     grep stock 1.26.2 for any new `runtime·*` symbol that didn't exist
     in 1.25.5 and confirm we don't miss one the 1.26.2 runtime now
     references.
   - Comparison commands:
     ```
     diff ~/sdk/go1.25.5/src/runtime/sys_linux_arm64.s \
          /opt/homebrew/Cellar/go/1.26.2/libexec/src/runtime/sys_linux_arm64.s
     grep -n '^TEXT runtime·' /opt/homebrew/Cellar/go/1.26.2/libexec/src/runtime/sys_linux_arm64.s
     grep -n '^TEXT runtime·' runtime-patches/sys_linux_arm64.s
     ```

## Rebasing approach (per-file discipline)

For each file, the mechanical recipe:

1. **Read our delta:** `diff ~/sdk/go1.25.5/src/runtime/<file> runtime-patches/<file>`
2. **Read upstream drift:** `diff ~/sdk/go1.25.5/src/runtime/<file> /opt/homebrew/Cellar/go/1.26.2/libexec/src/runtime/<file>`
3. **Surgically apply our delta onto 1.26.2:** Use `Edit` on the overlay
   file, anchoring on stable surrounding lines. **Do NOT wholesale `cp`
   the stock file over our overlay** — the user prefers targeted edits so
   each delta is auditable. (See memory `feedback_prefer_surgical_rebase.md`.)
4. **Rebuild incrementally:** `$GO tool task clean && $GO tool task` and
   collect the next error batch.

## Git state at the pause point

Tracked-file changes not yet committed:

- `CLAUDE.md` — 4 path updates + migration-in-progress callout.
- `mazarin/overlay/userspace/runtime/maz_moduledata.go` — `KindMask` → `Kind()`.
- `mazarin/overlay/userspace/syscall_linux.go` — `internal/itoa` → `internal/strconv`.
- `runtime-patches/diplomat-linux/syscall_linux.go` — same.
- `runtime-patches/syscall/syscall_linux.go` — same.

Gitignored tree changes (no git impact):

- `bin/old.go.1.25.5/src/runtime/malloc.go` reverted to stock 1.25.5
  (in-place arenaBaseOffset edit removed; overlay is sole source of truth).

**No git commit has been made for the migration work.** The user has not asked
for one yet. When they do, prefer one commit per logical phase/file group so
the history is reviewable and bisectable.

## Quick resume recipe

```bash
# 1. Set env
export GOTOOLCHAIN=auto
export GO=/opt/homebrew/bin/go
export QEMU=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-aarch64
export QEMU_X86_64=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-x86_64

# 2. Confirm where we are
git status
diff ~/sdk/go1.25.5/src/runtime/malloc.go runtime-patches/malloc.go | head -10

# 3. Reproduce the current failure
$GO tool task clean
$GO tool task 2>&1 | tee /tmp/phase2-build-next.log | grep -E "error|undefined" | sort -u

# 4. Rebase the next file in the priority list above.
```

## Validation checklist (Phase 4)

For ARM64 HVF, x86_64, and RISC-V each:

- [ ] `$GO tool task clean && $GO tool task <arch>` — clean build.
- [ ] `$GO tool task run-arm64-hvf TIMEOUT=30` (or equivalent per-arch) —
      kernel boots, shepherds launch, stdio renders.
- [ ] 60-second response test (see memory `response_test_failures.md`) —
      compare against 1.25.5 baseline.
- [ ] `gccheckmark=1` remains enabled; no `asyncpreemptoff` regression.
- [ ] Exercise async preemption from kernel timer IRQ given `preempt.go`
      behavioral changes (sysmon-driven preempt of tight-loop kernel
      goroutine).

## References

- Memory index: `/Users/iansmith/.claude/projects/-Users-iansmith-mazzy/memory/MEMORY.md`
- Relevant memories:
  - `feedback_no_patched_goroot.md` — why we moved off patched GOROOT.
  - `feedback_go_binary_path.md` — current GO= path (1.26.2).
  - `kernel_overlay_sys_linux_arm64.md` — sys_linux_arm64.s overlay rationale.
