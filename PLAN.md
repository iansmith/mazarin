# MAZ-73 — Audit / lift MaxShepherds=32 ceiling

**Ticket:** [MAZ-73](https://linear.app/mazarin/issue/MAZ-73)
**Branch:** `feat/MAZ-73` (base: `origin/master @ 443c2b0e`)
**Phase 0 commit:** `40f0526c` (ShepherdStorage stub + MinPID/MaxPID + MaxLiveShepherds + 10 red tests + Taskfile expansion)
**Test command:** `task test`

## Definition of Done

This ticket is done when **all six** are true and observable:

1. All 10 `TestShepherdStorage*` tests pass.
2. Alloc / release / lookup call sites use `ShepherdStorage`.
3. Iteration call sites use `ForEach`.
4. `notifyQueues` and `deathSubscribers` (parallel arrays sized by MaxShepherds) are resized or restructured.
5. `MaxShepherds = 32` is lifted (removed or aliased to MaxLiveShepherds).
6. `ShepherdId` is widened to int32 throughout.

How to verify each: see the task_plan.md DoD section.

## Work items (in order — sequential)

### 1. Implement `ShepherdStorage` (kmazarin/proc/shepherd_storage.go)

Add private fields:
- `slots [MaxLiveShepherds]Shepherd`
- `inUse [MaxLiveShepherds]bool`
- `pidIndex [MaxPID - MinPID + 1]int16` — initialize to -1 in `New()`
- `count int32`

Implement Allocate (range check → pidIndex check → scan for free slot → mark), Get (range check → pidIndex lookup), Release (range check → pidIndex lookup → clear → decrement), Len, ForEach (scan inUse). All `//go:nosplit`. No heap.

**Done when:** all 10 tests pass.

### 2. Migrate alloc/release/lookup to ShepherdStorage

- Instantiate global `var shepherdStorage = NewShepherdStorage()` in proc.go
- Rewrite `createUserspaceThreadImpl` (threads.go:2487-2489) to call `shepherdStorage.Allocate(pid)`
- Rewrite `releaseShepherdSchedLockHeld` (threads.go:2446-2454) to call `shepherdStorage.Release(pid)`
- Rewrite `FindShepherdBySID` (proc.go:183-190) as a wrapper over `Get`
- Remove `ShepherdListData` and `ShepherdListInUse` once consumers gone

**Done when:** compiles + boot smoke test passes.

### 3. Migrate iteration call sites to ForEach

Sites:
- `kmazarin/ksyscall/runshepherd.go:220`
- `kmazarin/ksyscall/delegate.go:129`
- `kmazarin/ksyscall/shepherd_info.go:24`
- `kmazarin/ksyscall/mmap_writeback.go:217`

Convert `for i := 0; i < proc.MaxShepherds; i++ { if proc.ShepherdListInUse[i] { ... } }` → `proc.ShepherdStorage().ForEach(func(s *proc.Shepherd) bool { ... })`.

**Done when:** `grep -r "for i := 0; i < proc.MaxShepherds"` returns no hits.

### 4. Resolve parallel arrays (notifyQueues, deathSubscribers)

Sites:
- `kmazarin/ksyscall/constraint_notify.go:36`
- `kmazarin/ksyscall/death_subscribe.go:21`

Resize from `[MaxShepherds]` to `[MaxLiveShepherds]`; update bounds checks. (Alternative: move into Shepherd struct — bigger refactor; recommend resize for v1.)

**Done when:** all parallel arrays sized by `MaxLiveShepherds`.

### 5. Lift MaxShepherds=32

Delete the const from `proc.go:13` if no consumers remain, or alias to `MaxLiveShepherds`.

**Done when:** `grep -r "MaxShepherds"` shows no hits except the storage definition or `MaxLiveShepherds`.

### 6. Widen ShepherdId from int16 to int32 (LAST step)

- Change `type ShepherdId int16` → `int32` in `proc.go:28`
- Search-fix all `int16(...)` casts holding ShepherdId values
- Build for arm64 and amd64; fix compiler-surfaced type mismatches

~154 references; mechanical sweep.

**Done when:** mazzy builds cleanly; `task test` and boot smoke test green.

## Parallelism analysis

- All 6 items sequential; no within-leaf parallelism.
- **Recommended execution:** single agent. Estimated 4-6 hours of focused work.

## Cross-leaf dependencies

- **Upstream / coupled:** [MAZ-69](https://linear.app/mazarin/issue/MAZ-69) defines the same MinPID/MaxPID constants. Both can run in parallel; merge conflict on the constant block is trivial (identical values).
- **Downstream:** every shepherd-state ticket (MAZ-70, MAZ-75, MAZ-77, …) sees the refactor when MAZ-73 lands.

## Implementation notes

- **Phase 0 tests are the spec.** Don't modify them.
- **Sparse + pidIndex** for O(1) lookup without heap.
- **Initialize `pidIndex` to -1** in `New()` (don't rely on Go zero-value; 0 aliases slot 0).
- **Single global storage.**
- **`schedulerLock` covers the API.** No internal spinlock.
- **`//go:nosplit` on all methods.**
- **int32 sweep is the LAST step** to avoid mid-refactor type mismatches.

## References

- [MAZ-61](https://linear.app/mazarin/issue/MAZ-61) — Parent container
- [MAZ-68](https://linear.app/mazarin/issue/MAZ-68) — Settled PID decisions (MinPID=2, kernel sentinel)
- [MAZ-69](https://linear.app/mazarin/issue/MAZ-69) — Sibling: PID allocator algorithm (shares MinPID/MaxPID constants)
- Phase 0 commit on this branch: `40f0526c`
