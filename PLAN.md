# MAZ-71 — Kernel → linux shepherd notification protocol

**Ticket:** [MAZ-71](https://linear.app/mazarin/issue/MAZ-71)
**Branch:** `feat/MAZ-71` (base: `origin/master @ 443c2b0e`)
**Phase 0 commit on this branch:** `d0009995` (NotificationEvent + NotificationQueue stub + 8 red tests + Taskfile expansion)
**Test command:** `task test`

## Definition of Done

This ticket is done when **all** are true and observable:

1. **The kernel can enqueue a lifecycle event and the queue holds it.** Push works for all three event types (EventChildExit, EventParentDeath, EventExecComplete); FIFO ordering is preserved; capacity is bounded at `MaxNotificationEvents` (256); overflow returns `ErrQueueFull`.
   How to verify: all 8 `TestNotification*` tests pass under `task test`.

2. **A documented overflow policy.** Kernel callers that Push have a defined behavior when `ErrQueueFull` returns — proposed: drop-oldest with a printk-style warning. The policy is documented in the implementation's docstrings.
   How to verify: code review confirms callers handle `ErrQueueFull`; no silent failures.

3. **Delivery path designed.** The linux shepherd has a registered uring dispatcher handler for a new `ProtoProcessNotify` protocol discriminator that decodes a `NotificationEvent`. Wiring the handler to actually do something (raise SIGCHLD, wake wait4) is out of scope — that's MAZ-80 / MAZ-89.
   How to verify: the new protocol discriminator is registered; a smoke test synthesizes a kernel-side Push and observes the handler logging the decoded event.

Out of scope: wait4 wakeup (MAZ-80), SIGCHLD raise (MAZ-89), Pdeathsig handling.

## Work items

### 1. Implement `NotificationQueue` in `kmazarin/proc/notification.go`

**Files:** `kmazarin/proc/notification.go` (single file)
**Depends on:** none

**Detailed steps:**

a. Add private fields to `NotificationQueue`:
   - `events [MaxNotificationEvents]NotificationEvent` — fixed array, ~3 KB
   - `head uint32` — next slot to Pop
   - `tail uint32` — next slot to Push
   - `count uint32` — current depth (avoids head==tail full-vs-empty ambiguity)

b. `Push(ev) error`:
   - If `count == MaxNotificationEvents`, return `ErrQueueFull` (no mutation).
   - `events[tail] = ev`; `tail = (tail + 1) % MaxNotificationEvents`; `count++`; return `nil`.

c. `Pop() (NotificationEvent, bool)`:
   - If `count == 0`, return zero-value + false.
   - `ev := events[head]`; `head = (head + 1) % MaxNotificationEvents`; `count--`; return ev + true.

d. `Len() int` returns `int(count)`.

e. `Cap() int` already correct (returns `MaxNotificationEvents`).

f. Preserve `//go:nosplit` on Push/Pop/Len.

g. No heap allocations.

**Done when:** all 8 `TestNotification*` tests pass under `task test`.

### 2. Delivery hook + linux-shepherd handler

**Files:**
- `shared/ipc/protocol.go` — add `ProtoProcessNotify` constant (pick a non-colliding value)
- `kmazarin/kmazarin/uring_ipc.go` — new `KernelPublishProcessNotify(targetSID, ev)`
- `maz/linux/main.go` — register handler for the new discriminator

**Depends on:** Item 1

**Detailed steps:**

a. Pick a new protocol discriminator value in `shared/ipc/protocol.go` that doesn't collide with existing constants (e.g., the ProtoDeath / ProtoShepherdNotify range). Document.

b. Define wire encoding of `NotificationEvent` inside `UringIPCMsg`. Direct memcpy is fine (~12 bytes; UringIPCMsg body is much larger).

c. Implement `KernelPublishProcessNotify(targetSID proc.ShepherdId, ev proc.NotificationEvent)`:
   - Locate the target shepherd via `proc.FindShepherdBySID`
   - Build a `UringIPCMsg` with the new discriminator + encoded payload
   - Call `KernelWriteToRing(targetSID, &msg)`
   - Mark `//go:nosplit`

d. In linux shepherd's `startUringDispatchers()`: register a handler for `ProtoProcessNotify` that decodes the body into `NotificationEvent` and (for v1) logs it via the existing log infrastructure.

e. Smoke test: synthesize a kernel-side Push and observe the handler logging the decoded event.

**Done when:** new discriminator registered; smoke test passes; existing test suite remains all-green.

## Parallelism analysis

- **Items eligible for parallel execution:** items 1 and 2 are coupled (item 2 uses item 1's type). **Recommended execution: serial** (one agent does both).
- **Sequential dependencies:** Item 2 → after Item 1.
- **Recommended execution:** single agent does both items.

## Cross-leaf dependencies

- **Upstream:** nothing in MAZ-61's batch blocks this. MAZ-70 (process state record) is independent.
- **Downstream:** MAZ-77 (kernel child-exit notification) is the natural producer site that will call `KernelPublishProcessNotify`. MAZ-80 (wait4) and MAZ-89 (SIGCHLD raise) are downstream consumers in the linux shepherd's event-handler logic.

## Implementation notes

- **Phase 0 tests are the spec.** Don't modify them.
- **Overflow policy:** propose drop-oldest with a printk-style warning. Document in `notification.go` and at the caller in `releaseShepherdSchedLockHeld` once that ticket lands.
- **Single global queue.** Recipient encoded in `Event.Pid`. Don't make NotificationQueue per-shepherd.
- **Don't import `kmazarin` from `proc`.** Delivery code lives in `kmazarin/kmazarin/uring_ipc.go`. `kmazarin/proc/` stays import-clean.
- **`//go:nosplit` on Push + any helper.** Pop side is user code (linux shepherd reader goroutine).
- **Loose Queue/delivery-hook coupling.** For v1 the kernel may call the delivery hook directly without staging through the Queue; the Queue's role solidifies as more producers land.

## Transport mechanism (from investigation)

The kernel→shepherd transport is **already proven**: `KernelWriteToRing(linuxSID, &msg)` writes to the target shepherd's uring ring 0. The ring is kernel-allocated and mapped into both address spaces; the shepherd reader (goroutine) drains it. FIFO ordering is preserved by the ring's lock-free structure with atomic Head/Tail.

The same mechanism is already used for:
- `ProtoDeath` (peer death notifications) — `kmazarin/ksyscall/death_subscribe.go:61-75`
- `ProtoIdleFlushHint` — `kmazarin/kmazarin/threads.go:1644`

MAZ-71 adds a new protocol discriminator on the same pipe.

## References

- [MAZ-61](https://linear.app/mazarin/issue/MAZ-61) — Parent container
- [MAZ-70](https://linear.app/mazarin/issue/MAZ-70) — Sibling: process state record (independent)
- [MAZ-77](https://linear.app/mazarin/issue/MAZ-77) — Downstream: child-exit notification (calls into this protocol)
- [MAZ-80](https://linear.app/mazarin/issue/MAZ-80) — Downstream: wait4
- [MAZ-89](https://linear.app/mazarin/issue/MAZ-89) — Downstream: SIGCHLD raise
- Phase 0 commit on this branch: `d0009995`
