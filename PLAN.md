# MAZ-70 — Kernel: process state record (parent PID, children, zombie, exit status, environ)

Implementation plan for the Phase B agent.

**Ticket:** [MAZ-70](https://linear.app/mazarin/issue/MAZ-70)
**Branch:** `feat/MAZ-70` (base: `origin/master @ 443c2b0e`)
**Phase 0 commit on this branch:** `2454bdf0` (Shepherd struct fields + stub method bodies + 15 red tests + Taskfile expansion)
**Test command:** `task test`

## Definition of Done

This ticket is done when **all** are true and observable:

1. **The Shepherd struct tracks its parent, its live children, its zombie state, and its environ.** A test program can construct a Shepherd, set/check ParentPID, add and remove children with idempotent semantics, mark zombie + read exit status + reap, and store/retrieve an environ block.
   How to verify: `task test` reports all 15 `TestShepherd*` tests in `mazzy/kmazarin/proc` passing.

2. **Capacity limits are enforced cleanly with sentinel errors.** Adding more than `MaxChildrenPerShepherd` (64) children returns `ErrTooManyChildren` and does not corrupt the list. Setting an environ larger than `MaxEnvironBytes` (8192) returns `ErrEnvironTooLarge` and does not mutate the stored bytes.
   How to verify: `TestShepherdAddChildCapacity` and `TestShepherdSetEnvironTooLarge` pass.

3. **All bookkeeping methods are kernel-safe.** No heap allocations; every method preserves the `//go:nosplit` pragma; no imports beyond `errors` added to `kmazarin/proc`.
   How to verify: implementation compiles under existing build constraints; `task test` passes the full suite.

Out of scope: wiring the new fields into the actual fork/wait4/execve syscall paths. Those land in MAZ-77 / MAZ-80 / MAZ-89 and the MAZ-62 container.

## Work items

### 1. Implement the 10 stub methods in `kmazarin/proc/shepherd_state.go`

**Files:** `kmazarin/proc/shepherd_state.go` (single file — Shepherd struct field additions already on the branch from Phase 0)
**Depends on:** none
**Parallel-safe with:** none — single work item

**Detailed steps:**

a. **`AddChild(child ShepherdId) error`**:
   - If `child` is already in `s.Children[0:s.NumChildren]`, return `nil` (idempotent — no-op).
   - If `s.NumChildren >= MaxChildrenPerShepherd`, return `ErrTooManyChildren`.
   - Otherwise: `s.Children[s.NumChildren] = child; s.NumChildren++; return nil`.

b. **`RemoveChild(child ShepherdId)`**:
   - Linear scan `s.Children[0:s.NumChildren]` for `child`.
   - If found at index `i`: swap with the last entry (`s.Children[i] = s.Children[s.NumChildren-1]`), decrement `s.NumChildren`.
   - If not found: no-op.

c. **`HasChild(child ShepherdId) bool`**: linear scan `s.Children[0:s.NumChildren]`; return whether found.

d. **`ChildCount() int32`**: return `s.NumChildren`.

e. **`EachChild(fn func(child ShepherdId) bool)`**:
   - `for i := int32(0); i < s.NumChildren; i++ { if !fn(s.Children[i]) { return } }`

f. **`MarkZombie(status int32)`**: if `!s.Zombie`, set `s.Zombie = true` and `s.ExitStatus = status`; if already zombie, no-op (preserve original status per docstring).

g. **`IsZombie() bool`**: return `s.Zombie`.

h. **`Reap() int32`**:
   - If `!s.Zombie`, return `0` and do not mutate.
   - Otherwise capture `status := s.ExitStatus`, set `s.Zombie = false`, `s.ExitStatus = 0`, return `status`.

i. **`SetEnviron(env []byte) error`**:
   - If `len(env) > MaxEnvironBytes`, return `ErrEnvironTooLarge` (do not mutate).
   - Copy bytes into `s.Environ`; set `s.EnvironLen = uint32(len(env))`; return `nil`.
   - The Go built-in `copy()` is nosplit-safe; either `copy()` or a manual loop is fine.

j. **`GetEnviron() []byte`**:
   - Return `s.Environ[:s.EnvironLen]` (slice into the in-struct array; zero-allocation; caller must not modify per docstring).

k. **Preserve `//go:nosplit`** on all 10 methods.

l. **No new imports.** `errors` already present.

**Done when:** all 15 `TestShepherd*` tests in `kmazarin/proc/shepherd_state_test.go` turn green under `task test`:

- `TestShepherdNewHasNoParent`
- `TestShepherdAddChildAndQuery`
- `TestShepherdAddChildIdempotent`
- `TestShepherdAddChildCapacity`
- `TestShepherdRemoveChild`
- `TestShepherdRemoveChildIdempotent`
- `TestShepherdEachChildIteratesAll`
- `TestShepherdEachChildStopsOnFalse`
- `TestShepherdMarkZombie`
- `TestShepherdReap`
- `TestShepherdReapNonZombie`
- `TestShepherdSetEnvironRoundtrip`
- `TestShepherdSetEnvironTooLarge`
- `TestShepherdSetEnvironEmpty`
- `TestShepherdSetEnvironOverwrite`

Plus the existing `task test` suite must remain all-green.

## Parallelism analysis

- **Items eligible for parallel execution:** none — single work item, single-file scope.
- **Sequential dependencies:** N/A within this ticket.
- **Recommended execution:** single agent.

## Cross-leaf dependencies (for swarm orchestration)

- **Upstream:** nothing in MAZ-61's batch blocks this ticket.
- **Downstream:** MAZ-71 (notification protocol) / MAZ-77 (child-exit notification) / MAZ-80 (wait4) / MAZ-89 (SIGCHLD raise) consume these fields and methods.

## Implementation notes

- **Phase 0 tests are the spec.** Don't modify them.
- **Storage:** dense fixed-size arrays. Swap-with-last on RemoveChild keeps the array compact without shifting — order is documented as implementation-defined.
- **Locking:** assume the caller holds `kmazarin/kmazarin/threads.go`'s `schedulerLock`. Do NOT add an internal spinlock.
- **Initialization is automatic.** New Shepherds get zero values from Go's zero-struct semantics; the release path at `threads.go:2449` does a full struct zero (`proc.ShepherdListData[shepherdIdx] = proc.Shepherd{}`). You do NOT need to add initialization logic to existing sites.
- **No new imports.** Stay package-isolated.
- **`//go:nosplit` on every method.** Already in the stubs; preserve.

## References

- [MAZ-61](https://linear.app/mazarin/issue/MAZ-61) — Parent container: process identity & lifecycle
- [MAZ-69](https://linear.app/mazarin/issue/MAZ-69) — Sibling: kernel PID allocator (independent)
- Phase 0 commit on this branch: `2454bdf0`
