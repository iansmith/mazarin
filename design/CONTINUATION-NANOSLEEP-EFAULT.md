# Continuation: Nanosleep EFAULT Bug + sysmon Heartbeat Redundancy

## Context

This document captures findings from investigating why the system is 98% CPU-bound
after boot when it should be nearly idle. The root cause is a bug in `SyscallNanosleep`
that causes every kernel goroutine's `usleep()` call to return EFAULT instantly,
creating a 7M SVC/sec tight loop from sysmon and other Go runtime goroutines.

## The EFAULT Bug

### Symptom
- SID 0 (kernel) generates ~28 million SVCs per 4-second epoch, all syscall 101 (nanosleep)
- `NanosleepCallCount` shows only ~400 actual sleeps in 30 seconds
- `NanosleepEarlyEfault` shows 28.5 million EFAULT returns
- Userspace shepherds are completely starved: clocks gets 0.5 Hz updates instead of 10 Hz
- Thread 0 (idle loop / WFI) runs only 2% of the time
- `IL=` (idle loop count) grows by only ~20 per 1000 timer ticks

### Root Cause Chain

1. Go runtime's `sysmon` goroutine calls `usleep(20)` (20 microseconds)
2. The ARM64 overlay in `runtime-patches/sys_linux_arm64.s` converts this to:
   - Build a `timespec{tv_sec=0, tv_nsec=20000}` on the **kernel stack**
   - `SVC 101` (SYS_nanosleep) with R0 pointing to the stack timespec
3. `SyscallNanosleep` calls `isValidUserAddr(req)` to validate the pointer
4. `isValidUserAddr` calls `proc.CurrentShepherd()` to determine kernel vs userspace context
5. `CurrentShepherd()` calls `currentShepherdImpl()` which reads `t.ShepherdIdx` from the current thread
6. **BUG**: For kernel threads (TIDs 54, 736) that previously ran shepherd goroutines via Go scheduling, `ShepherdIdx` is stale — it still points to the last shepherd context
7. `CurrentShepherd()` returns non-nil (a shepherd), so `isValidUserAddr` treats this as a userspace call
8. The timespec address is on the kernel stack (0xFFFFFFFF...), which has bit 48+ set
9. `isValidUserAddr` rejects it: `(addr & 0xFFFF000000000000) != 0` → returns false
10. `SyscallNanosleep` returns `-14` (EFAULT) immediately without sleeping
11. The `usleep` overlay in assembly **does not check the return value** — it just RET's
12. `sysmon` continues its loop, calls `usleep(20)` again immediately
13. Result: 7 million no-op SVC round-trips per second

### Key Files

- `kmazarin/ksyscall/nanosleep.go` — SyscallNanosleep handler
- `kmazarin/ksyscall/stubs.go:30` — `isValidUserAddr()` function
- `kmazarin/kmazarin/threads.go:963` — `currentShepherdImpl()` — stale ShepherdIdx
- `runtime-patches/sys_linux_arm64.s:100-123` — usleep overlay (builds timespec on kernel stack, does SVC 101)
- `kmazarin/kmem/paging.go:2577` — `ReadUserInt64Pair()` — has correct kernel-address fast path that we never reach

### The Fix (Not Yet Applied)

The simplest fix: remove the `isValidUserAddr` check from `SyscallNanosleep` entirely.
`ReadUserInt64Pair` (kmem/paging.go:2577) already handles both cases correctly:
- Kernel addresses (`isKernelAddr`): direct dereference — no page table walk needed
- User addresses: page table walk via `WalkUserPageTable`

The `isValidUserAddr` gate prevents us from ever reaching this correct logic.

Alternative: fix `currentShepherdImpl` to correctly identify kernel goroutines regardless
of which thread they're running on. But this is a deeper change — the stale `ShepherdIdx`
problem may affect other syscall handlers too.

### Instrumentation Added (should be removed after fix)

Counters added to `kmazarin/ksyscall/futex.go` (var block):
- `NanosleepZeroTickCount` — nanosleep calls with ticks==0
- `NanosleepRealSleepCount` — nanosleep calls that actually block
- `NanosleepDispatchedSID0` — nanosleep dispatches for SID 0
- `NanosleepEarlyNull` — req==0 early returns
- `NanosleepEarlyEfault` — isValidUserAddr rejection
- `NanosleepEarlyReadFail` — ReadUserInt64Pair failures
- `SID0SyscallCounts[256]` — per-syscall-number counters for SID 0

Epoch log additions in `threads.go` — `ns:` line and `sid0/svc:` line.

App-level instrumentation:
- `flock/cmd/clocks/main.go` — drawCount every 10 iterations
- `flock/cmd/linux/main.go` — dirtyTicks/draws/serial/delegate every 10 ticks
- `flock/cmd/rachel/main.go` — notify/appStart/blit/other/hid every 10 blits

## Investigation: sysmon Heartbeat vs Kernel Timer Tick

### What sysmon Does

sysmon is the Go runtime's watchdog goroutine. It runs on its own OS thread, outside the
P system (so it cannot be preempted by the Go scheduler). Its responsibilities:

1. **Preemption enforcement**: Retakes Ps from goroutines stuck in syscalls >20μs.
   Triggers async preemption for goroutines running >10ms on a P.
2. **STW coordination**: Works with GC to freeze/unfreeze the world.
3. **Netpoll**: Polls the network if no other P is doing it (calls `netpoll(0)`).
4. **Timer management**: Fires timers when all Ps are idle.
5. **Deadlock detection**: Calls `checkdead()` on each iteration.

Its main loop (`runtime/proc.go:6239`):
```go
for {
    if idle == 0 { delay = 20 }           // 20μs when active
    else if idle > 50 { delay *= 2 }      // ramp up after 1ms idle
    if delay > 10*1000 { delay = 10000 }  // cap at 10ms
    usleep(delay)
    // ... check P retaking, preemption, GC, timers, network ...
}
```

### What the Kernel Timer Tick Already Does

Kmazarin's 250Hz timer tick (`ProcessDeadlinesTopHalf` and related functions) already
performs most of sysmon's duties at the kernel level:

1. **Thread preemption**: Timer ISR checks `PreemptElapsed` against quantum (100ms).
   When exceeded, saves context and switches to another ready thread. This is the
   kernel-level equivalent of sysmon's P-retaking and async preemption.

2. **Deadline processing**: `processStaticDeadlinesSchedLockHeld()` wakes threads
   whose nanosleep/futex deadlines have expired. This handles all timed waits.

3. **io_uring timeout**: `checkIOUringTimeoutFromTimer()` handles 10ms safety timeout
   for blocked io_uring waiters.

4. **Dirty notification**: Constraint dirty walk runs at 10Hz (every 25th tick at 250Hz),
   driving the UI update cycle via WaitDirty/enqueueNotificationCollectWake.

5. **Console flush**: `softIRQConsole.CheckPendingWake()` runs every tick to wake the
   linux shepherd when serial data arrives.

### The Question: Is sysmon's Heartbeat Redundant?

On real Linux, sysmon is essential because:
- The kernel doesn't know about Go's goroutine scheduling
- P-retaking requires userspace monitoring (the kernel can't see P assignments)
- The Go scheduler is cooperative within a P — sysmon provides the preemptive backstop

In kmazarin, the situation is different:
- The kernel IS the Go runtime — we control the scheduler
- Thread preemption is handled by the hardware timer ISR (250Hz)
- We have direct access to goroutine state from the timer handler
- The 250Hz tick (4ms) is already coarser than sysmon's 20μs-10ms range

The investigation should examine:
- Which of sysmon's specific checks are already covered by the kernel timer tick?
- Which are NOT covered and still need sysmon?
- Could the kernel timer tick absorb sysmon's remaining duties?
- What are the implications for the Go runtime if sysmon sleeps much longer (or differently)?
- Is there a simpler overlay for usleep that avoids the SVC overhead entirely?

### Specific Go Runtime Functions sysmon Calls

From `runtime/proc.go` sysmon loop body (after the usleep):
- `retake()` — steal Ps from threads in syscalls >20μs (forcePreemptNS=10ms)
- `checkdead()` — deadlock detection
- `netpoll(0)` — non-blocking network poll
- `timeSleepUntil()` — find next timer deadline
- `notetsleep()` — deep sleep when truly idle (GC waiting or all Ps idle)
- `injectglist()` — inject ready goroutines found by netpoll

### Other Go Runtime usleep Callers

- `runtime/proc.go:7264` — `usleep(3)` in work-stealing (runnext backoff)
- `runtime/proc.go:1177-1197` — `usleep(1000)` in `freezetheworld` (rare, STW only)
- `runtime/proc.go:642` — `usleep_no_g(100)` in early init
- `runtime/proc.go:2766` — `usleep_no_g(1)` in `startm`

The work-stealing `usleep(3)` is particularly interesting — it's a 3μs backoff to avoid
thrashing goroutines between Ps. This also hits the EFAULT bug and generates SVCs.

## Uncommitted Changes on Branch

The following changes are on `fix/rawsyscall-experiment` but NOT committed:

### WaitDirty Race Fix (kernel)
- `kmazarin/ksyscall/constraint_notify.go` — removed BlockedTID from SyscallAttrWaitDirty, added SetBlockedTID helper, added shepherdSID param
- `kmazarin/ksyscall/constraint_notify_asm.go` — updated forward declaration
- `kmazarin/kmazarin/notify_bridge.go` — BlockForDirtyNotify sets BlockedTID under scheduler lock
- **Status**: Implemented but the user has not yet approved as architecturally sound

### Instrumentation (temporary, remove after fix)
- `kmazarin/ksyscall/nanosleep.go` — early-return counters
- `kmazarin/ksyscall/futex.go` — counter variables
- `kmazarin/ksyscall/dispatch.go` — SID 0 per-syscall counter, nanosleep dispatch counter
- `kmazarin/kmazarin/threads.go` — epoch log ns: line, sid0/svc: line, prevSID0Syscalls
- `flock/cmd/clocks/main.go` — drawCount logging
- `flock/cmd/linux/main.go` — dirtyTicks/draws/serial/delegate logging
- `flock/cmd/rachel/main.go` — message count logging

### Clocks Instrumentation Cleanup (already done)
- Removed nanotime linkname, sync/atomic import, draw perf logging, loop counters
- Main loop is clean: WaitDirty → Draw → sendBlit
