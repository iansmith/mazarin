# Constraint VM Phase 6 — Dirty Notification

## Context

Phases 1–5C are complete. The constraint VM has:
- Flat shared-page attribute storage with dirty propagation (kernel DFS walk)
- Handle[T] client API with lazy evaluation on Get()
- Cascading constraint chains (evaluate-on-deref)
- handletest priest with 6 passing tests on all 3 architectures
- 120s stability confirmed on ARM64 TCG, x86_64, RISC-V

**Problem**: Currently, a priest must poll `h.IsDirty()` or call `h.Get()` to
discover that a dependency changed. There is no push notification. A GUI priest
would need to spin-loop checking all its display attributes for changes — the
same starvation pattern we fixed for WaitSoftIRQ.

**Solution**: Phase 6 adds eager dirty notification. When a value attribute is
written and the kernel's dirty walk encounters a node with `FlagEagerNotify`,
the kernel enqueues a coalesced soft IRQ to the owning priest. The priest
blocks on a channel (like WaitSoftIRQ) and wakes only when something changed.

## Existing Infrastructure

Already in place (no changes needed):
- `FlagEagerNotify` (bit 1) defined in `mazarin/vm/flat/node.go:14`
- `Handle[T].SetEager(bool)` stores flag locally (`mazarin/attr/handle.go:73`)
- `Owner` field on `FlatAttrNode` identifies the priest that created the attr
- WaitSoftIRQ blocking mechanism (kernel sleeps thread, wakes on events)
- `SoftIRQReturn` struct for batching events back to userspace
- Per-priest thread blocking/waking via `BlockForDelegatedSyscall`/`WakeThread`

## Implementation Plan

### Step 1: Kernel — SetEager syscall (write FlagEagerNotify to shared page)

Currently `Handle[T].SetEager(eager)` only sets a local Go field. It needs to
actually set `FlagEagerNotify` in the shared-page `FlatAttrNode.Flags`.

**File**: `kmazarin/ksyscall/constraint_syscall.go`

Add `SysAttrSetEager(slotNum, eager uint64, ...) int64`:
- Validates slot is owned by calling priest
- Sets or clears `FlagEagerNotify` in `node.Flags`
- Syscall number: `0x1029` (next available after 0x1028)

**File**: `mazarin/sys/syscall.go`

Add `AttrSetEager(slot uint16, eager bool) error` wrapper.

**File**: `mazarin/attr/handle.go`

Update `SetEager()` to call the syscall:
```go
func (h *Handle[T]) SetEager(eager bool) {
    h.eager = eager
    sys.AttrSetEager(h.slot, eager)
}
```

### Step 2: Kernel — Dirty notification queue

During the dirty walk, when the kernel encounters `FlagEagerNotify`, it records
the slot number in a per-priest notification queue. After the walk completes,
the kernel wakes any priest thread blocked on `WaitDirtyNotification`.

**File**: `kmazarin/ksyscall/constraint_dirty.go`

Extend `dirtyWalk` to check `FlagEagerNotify`:
```go
func (mgr *KernelAttrManager) dirtyWalk(slot uint16, gen uint64) {
    node := mgr.node(slot)
    if node.LastWalk == gen {
        return
    }
    node.LastWalk = gen

    node.Flags |= flat.FlagDirty

    // NEW: if eager-notify, enqueue for notification
    if node.Flags&flat.FlagEagerNotify != 0 {
        mgr.enqueueNotification(slot, node.Owner)
    }

    // Walk dependents...
}
```

**File**: `kmazarin/ksyscall/constraint_notify.go` (new)

Per-priest notification ring buffer:
```go
// NotifyQueue holds pending dirty slot notifications for a priest.
// Fixed-size ring buffer — if full, coalesce (priest will re-scan all eager attrs).
type NotifyQueue struct {
    Slots   [64]uint16   // dirty slot numbers
    Head    uint16       // next write position
    Count   uint16       // number of pending notifications
    Overflowed bool      // if true, priest must re-scan all eager attrs
}
```

The `enqueueNotification` method:
- Looks up the priest by `Owner` field
- Appends slot to that priest's `NotifyQueue`
- If queue is full, sets `Overflowed = true` (coalesced notification)
- Wakes the priest's notification-blocked thread (if any)

### Step 3: Kernel — WaitDirtyNotification syscall

**File**: `kmazarin/ksyscall/constraint_notify.go`

`SysAttrWaitDirty(resultBufPtr, maxSlots uint64, ...) int64`:
- If notifications are pending: copy slot numbers to userspace buffer, return count
- If no notifications: block the calling thread (same mechanism as WaitSoftIRQ)
- On overflow: return -1 to signal "re-scan all eager attrs"
- Syscall number: `0x102A`

Thread blocking uses the existing `BlockForDelegatedSyscall` / wake pattern.
When the dirty walk enqueues a notification, it calls the wake function.

### Step 4: Client — OnDirty channel API

**File**: `mazarin/attr/notify.go` (new)

```go
// WaitDirty blocks until one or more eager-notify attributes become dirty.
// Returns the slot numbers of the dirty attributes (or nil on overflow,
// meaning the caller should re-check all eager attributes).
func WaitDirty() []uint16 {
    var buf [64]uint16
    n := sys.AttrWaitDirty(buf[:])
    if n < 0 {
        return nil // overflow — re-scan all
    }
    return buf[:n]
}
```

Higher-level channel API (optional, for goroutine-based consumers):
```go
// OnDirty returns a channel that receives a batch of dirty slot numbers
// whenever eager-notify attributes change. Spawns a background goroutine.
func OnDirty() <-chan []uint16 {
    ch := make(chan []uint16, 4)
    go func() {
        for {
            slots := WaitDirty()
            ch <- slots
        }
    }()
    return ch
}
```

### Step 5: Change-gated propagation (optimization)

Currently, dirty propagation is unconditional — if A depends on B and B is
written, A is marked dirty even if B's new value equals the old value.

Add change-gating to `SyscallAttrWrite` (value write path):
- Before propagating, compare new FlatValue with old CachedValue
- If bitwise equal, skip dirty propagation entirely
- This prevents redundant constraint re-evaluation when a value is "set" to
  its current value

**File**: `kmazarin/ksyscall/constraint_syscall.go`

In the value write path (isConstraintResult=0), before calling `dirtyPropagate`:
```go
if node.CachedValue == newValue {
    return 0 // no change, skip propagation
}
```

For constraint results (isConstraintResult=1), the change gate goes in the
client-side `evaluate()` method — only call `SysAttrWriteResult` if the
computed result differs from the current cached value.

### Step 6: handletest additions

Add two new test cases to `flock/cmd/handletest/main.go`:

**Test 7: Eager notification round-trip**
- Create value `a` and constraint `doubled = a * 2`
- Call `doubled.SetEager(true)`
- In a goroutine, call `WaitDirty()` (blocks)
- In main goroutine, `a.Set(10)` — triggers dirty walk → eager notification
- Goroutine receives notification with `doubled`'s slot
- Verify `doubled.Get() == 20`

**Test 8: Change-gated propagation**
- Create value `a` and constraint `sum = a + 0`
- `a.Set(5)`, verify `sum.Get() == 5`
- `sum.SetEager(true)`
- Spawn goroutine with `WaitDirty()` (blocks)
- `a.Set(5)` again (same value)
- Verify goroutine does NOT wake (change-gated)
- `a.Set(6)` — goroutine DOES wake
- Use a short timeout to distinguish "didn't wake" from "slow"

## Verification

1. Build all 3 architectures
2. Run handletest on ARM64 TCG, x86_64, RISC-V — all 8 tests pass
3. 120s stability test on all 3 platforms — 0 panics

## Syscall number summary

| Number | Name | Phase |
|--------|------|-------|
| 0x1021 | SysAttrCreate | 3 |
| 0x1022 | SysAttrWrite | 3 |
| 0x1023 | SysAttrWriteURI | 3 |
| 0x1024 | SysAttrAddDep | 3 |
| 0x1025 | SysAttrUpdateDeps | 3 |
| 0x1026 | SysAttrRegisterQuery | 3 |
| 0x1027 | SysAttrWriteResult | 5B |
| 0x1028 | SysAttrWriteString | 5B |
| 0x1029 | SysAttrSetEager | 6 |
| 0x102A | SysAttrWaitDirty | 6 |

## Files changed (estimated)

### New files
- `kmazarin/ksyscall/constraint_notify.go` — notification queue + WaitDirty syscall
- `mazarin/attr/notify.go` — WaitDirty() + OnDirty() client API

### Modified files
- `kmazarin/ksyscall/constraint_dirty.go` — eager enqueue in dirtyWalk
- `kmazarin/ksyscall/constraint_syscall.go` — SysAttrSetEager + change-gating
- `kmazarin/ksyscall/syscall_dispatch.go` — register 0x1029, 0x102A
- `mazarin/sys/syscall.go` — AttrSetEager, AttrWaitDirty wrappers
- `mazarin/attr/handle.go` — SetEager() calls syscall
- `flock/cmd/handletest/main.go` — tests 7 and 8

## Notes

- The notification queue is per-priest, not per-attribute. A single WaitDirty
  call returns ALL dirty eager slots for that priest. This avoids needing one
  soft IRQ slot per attribute.
- Overflow handling (>64 pending notifications) uses a simple "re-scan all"
  flag. In practice, UIs have O(10) eager attributes, not O(1000).
- The blocking mechanism mirrors WaitSoftIRQ: kernel blocks the thread,
  another thread runs, notification enqueue wakes the blocked thread.
- Change-gating uses bitwise FlatValue comparison (32 bytes). This is exact
  for integers, bools, and fixed-point types. For floats, bitwise equality
  means NaN != NaN and +0 == +0, which is the correct behavior for display
  change detection.
