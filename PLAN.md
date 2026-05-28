# MAZ-72 — Linux shepherd: per-PID record (FD table / cwd / environ / sigmask slot)

**Ticket:** [MAZ-72](https://linear.app/mazarin/issue/MAZ-72)
**Branch:** `feat/MAZ-72` (base: `origin/master @ 443c2b0e`)
**Phase 0 commit:** `c9a19c6c` (new `maz/linux/internal/processrecord/` package + stub Table + 8 red tests + Taskfile expansion)
**Test command:** `task test`

## Definition of Done

This ticket is done when **both** are true and observable:

1. **The linux shepherd has a working per-PID state table.** `Create` adds, `Get` retrieves, `Remove` deletes. Records hold placeholder fields (CWD, Environ, SigMask, PendingSigs, FDTable) for downstream tickets ([MAZ-63](https://linear.app/mazarin/issue/MAZ-63), [MAZ-64](https://linear.app/mazarin/issue/MAZ-64)) to populate.
   How to verify: all 8 `Test*` tests in `mazzy/maz/linux/internal/processrecord` pass under `task test`.

2. **Duplicate-create is rejected; remove is idempotent.** Re-creating an existing PID returns `ErrPIDExists` and does not overwrite. Removing a missing PID is a no-op.
   How to verify: `TestCreateExistingPIDReturnsErr` and `TestRemoveNonExistentIsNoop` pass.

Out of scope: wiring the table into syscall paths (MAZ-63 / MAZ-64), `ProtoProcessNotify` integration (MAZ-71).

## Work items

### 1. Implement `Table` in `maz/linux/internal/processrecord/record.go`

**Files:** `maz/linux/internal/processrecord/record.go` (single file)
**Depends on:** none

**Detailed steps:**

a. Add a private map field to `Table`: `records map[PID]*PerPIDRecord`.

b. `New()`: return `&Table{records: make(map[PID]*PerPIDRecord)}`.

c. `Create(pid)`:
   - If `_, ok := t.records[pid]; ok`, return `nil, ErrPIDExists`.
   - Allocate `rec := &PerPIDRecord{PID: pid}`; `t.records[pid] = rec`; return `rec, nil`.

d. `Get(pid)`: idiomatic two-return map lookup — `rec, ok := t.records[pid]; return rec, ok`.

e. `Remove(pid)`: `delete(t.records, pid)` — built-in is idempotent.

f. `Len()`: `return len(t.records)`.

g. No new imports beyond `errors`.

**Done when:** all 8 tests pass under `task test`; existing test suite remains all-green.

## Parallelism analysis

- **Items eligible for parallel execution:** none — single work item.
- **Recommended execution:** single agent. Trivial implementation.

## Cross-leaf dependencies

- **Upstream:** nothing in MAZ-61's batch blocks this.
- **Downstream:** MAZ-63 (FD table populates `FDTable any` slot), MAZ-64 (sigmask manipulation), MAZ-71 (notification handler calls Create/Remove).

## Implementation notes

- **Phase 0 tests are the spec.** Don't modify them.
- **Use a map.** `map[PID]*PerPIDRecord` is the simplest choice. Heap allocation is fine.
- **No goroutine safety for v1.** Package doc already documents this; don't add a mutex.
- **No new imports.**
- **`FDTable any` is intentional.** MAZ-63 will introduce a concrete type.

## References

- [MAZ-61](https://linear.app/mazarin/issue/MAZ-61) — Parent container
- [MAZ-63](https://linear.app/mazarin/issue/MAZ-63), [MAZ-64](https://linear.app/mazarin/issue/MAZ-64) — Downstream populators of the placeholder fields
- [MAZ-71](https://linear.app/mazarin/issue/MAZ-71) — Notification handler instantiates Create/Remove
- Phase 0 commit on this branch: `c9a19c6c`
