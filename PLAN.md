# MAZ-69 — Kernel: sequential monotonic PID allocator

Implementation plan for the Phase B agent. This file lives at the worktree root so the agent picks it up automatically. The same plan is also synced into the Linear ticket description.

**Ticket:** [MAZ-69](https://linear.app/mazarin/issue/MAZ-69)
**Branch:** `feat/MAZ-69` (base: `master @ 443c2b0e`)
**Phase 0 commit on this branch:** `be1ca36a` (stub API + 9 red tests + Taskfile expansion)
**Test command:** `task test`

## Definition of Done

This ticket is done when **both** are true and observable:

1. **The kernel can allocate process IDs reliably and avoid PID-reuse hazards.** A request for a new PID returns a unique integer in [2, 4095]. Freed PIDs do NOT come back from the very next `Alloc` — they only re-enter the allocatable pool after the allocator has cycled through the whole range and wrapped around to PID 2.
   How to verify: `task test` reports all 9 `TestPIDAllocator*` tests passing under `mazzy/kmazarin/proc`.

2. **Allocator exhaustion is reported, not silently corrupting.** When all 4094 PIDs are in use, the next allocation attempt receives `ErrPIDExhausted` (which maps to Linux EAGAIN at the syscall boundary) instead of an undefined response.
   How to verify: `TestPIDAllocatorExhaustionReturnsErr` passes.

Out of scope for this ticket: switching today's `createUserspaceThreadImpl` call site from `StaticAllocator.Acquire` to `PIDAllocator.Alloc`. That cutover is part of MAZ-73's audit/sweep.

## Work items

### 1. Implement `PIDAllocator` (replace stub bodies + add private storage fields)

**Files:** `kmazarin/proc/pid_allocator.go` (single file)
**Depends on:** none — the Phase 0 contract is already on this branch
**Parallel-safe with:** none — this is the only work item

**Detailed steps:**

a. Add private fields to the `PIDAllocator` struct:
   - `cursor ShepherdId` — next PID position to consider; starts at `MinPID`
   - `inUse [MaxPID - MinPID + 1]bool` — dense flag per PID (size 4094 bytes; fixed; no heap allocation)
   - `freeCount uint16` — running count of free PIDs; init to `MaxPID - MinPID + 1` (= 4094); decremented on Alloc, incremented on Free

b. Implement `NewPIDAllocator()`:
   - Return `&PIDAllocator{cursor: MinPID, freeCount: MaxPID - MinPID + 1}` (the `inUse` array zero-inits to all-false)

c. Implement `Alloc()`:
   - If `freeCount == 0`, return `(0, ErrPIDExhausted)`
   - Loop: while `inUse[cursor - MinPID]`, advance `cursor`; wrap to `MinPID` when past `MaxPID`
   - Mark `inUse[cursor - MinPID] = true`; capture `pid := cursor`; advance cursor (wrapping if needed) so the *next* Alloc starts past it; decrement `freeCount`; return `(pid, nil)`
   - Invariant: cursor advances monotonically except on wraparound; never moves backward on `Free`

d. Implement `Free(pid)`:
   - If `pid < MinPID || pid > MaxPID`, no-op
   - If `!inUse[pid - MinPID]`, no-op (idempotent)
   - Set `inUse[pid - MinPID] = false`; increment `freeCount`
   - **Do NOT reset cursor.** The "no immediate reuse" rule depends on cursor monotonicity.

e. Implement `InUse(pid)`:
   - If `pid < MinPID || pid > MaxPID`, return `false`
   - Return `inUse[pid - MinPID]`

f. Keep all three methods `//go:nosplit` (already in the stub; preserve the directive).

g. No heap allocations; no slice ops; no string formatting; no calls to non-nosplit functions.

**Done when:** All 9 `TestPIDAllocator*` tests in `kmazarin/proc/pid_allocator_test.go` turn green under `task test`:

- `TestPIDAllocatorAllocReturnsSequentialFromMin`
- `TestPIDAllocatorNeverReturnsPID0Or1`
- `TestPIDAllocatorDoesNotImmediatelyReuse`
- `TestPIDAllocatorExhaustionReturnsErr`
- `TestPIDAllocatorWraparoundReusesFreedAfterFull`
- `TestPIDAllocatorWraparoundSkipsInUse`
- `TestPIDAllocatorFreeIdempotent`
- `TestPIDAllocatorInUseTracksAllocFree`
- `TestPIDAllocatorNeverReturnsPIDOutOfRange`

Plus: the existing `task test` suite (shared/, mazarin/transfer/, mazarin/httpclient/, maz/protocol-http/internal/, kmazarin/proc/) must remain all-green; no regressions.

## Parallelism analysis

- **Items eligible for parallel execution:** none — single work item, single-file scope.
- **Sequential dependencies:** N/A within this ticket.
- **Recommended execution:** single agent.

## Cross-leaf dependencies (for swarm orchestration)

- **Upstream:** nothing within MAZ-61's batch blocks this ticket.
- **Downstream:** MAZ-73 (audit/lift MaxShepherds ceiling) will incorporate `PIDAllocator` into the real shepherd-launch path. MAZ-70 / MAZ-71 / MAZ-72 are independent.

## Implementation notes

- **Phase 0 tests are the spec.** Don't modify them. If you believe a test is wrong, stop and surface to the orchestrator.
- **Mimic the shape of `kmazarin/ds/static_allocator.go`** for the nosplit-safe / fixed-size-array discipline, but explicitly diverge from its LIFO Release semantics — those would immediately reuse freed IDs, which is the bug we're avoiding.
- **Storage:** a dense `[4094]bool` is the simplest implementation. A bitset would save ~3.5KB but the wraparound loop becomes harder to read and we have plenty of kernel BSS.
- **Concurrency:** the stub's docstring says "NOT goroutine-safe; callers hold an appropriate lock." Keep that posture. The existing scheduler lock at the call site covers us. Do NOT add an internal spinlock for v1.
- **No `int16` assumptions.** `ShepherdId` is `int16` today but MAZ-73 may widen it. The arithmetic inside the allocator should treat `ShepherdId` as opaque integer; rely on the type system rather than implicit `int16` ranges.

## References

- [MAZ-68](https://linear.app/mazarin/issue/MAZ-68) — PID space decisions (sequential monotonic, MinPID=2, kernel sentinel reserves PID 1)
- [MAZ-73](https://linear.app/mazarin/issue/MAZ-73) — Cache-key constraint (MaxPID=4095, hard fail with EAGAIN on exhaustion)
- [MAZ-61](https://linear.app/mazarin/issue/MAZ-61) — Parent container: process identity & lifecycle
- Phase 0 commit on this branch: `be1ca36a`
