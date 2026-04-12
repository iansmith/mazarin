# Death Cleanup Continuation — 4 Items

## What's Already Done

Two-phase shepherd death cleanup with refcounting is implemented and building/running
cleanly on ARM64 HVF. Key files changed:

- `shared/mazzy/mazzy.go` — SysDeathAck (slot 63)
- `kmazarin/ksyscall/delegate.go` — HasInFlightDelegateCallsAsCaller, CleanupDelegateForDeadShepherdHandlerOnly, CleanupRemainingDelegateCallsForCaller, SyscallDeathAck
- `kmazarin/kmazarin/threads.go` — DeferredCleanupEntry, CompleteDeferredCleanup, FlushAllDeferredCleanups, two-phase TerminateShepherd, releaseShepherdSchedLockHeld(deferPages)
- `mazarin/sys/death_ack.go` — userspace DeathAck wrapper
- `flock/cmd/linux/main.go` — per-SID refcounting (shepherdState, sidStates), death routing through delegateCh, stdin drain on death via reqQueue.RemoveWhere, stdinDecRefCh for cross-goroutine decRef
- `flock/cmd/linux/syscalls.go` — isStdinRead, callerSID in readDataResponse, sidIncRef at stdin enqueue
- `shared/queue/queue.go` — RemoveWhere method

---

## Item 1: FD Table Cleanup on Shepherd Death

### Problem
The linux shepherd's FD table (`fdt` in `flock/cmd/linux/syscalls.go`) is global — all
caller SIDs share one table. When shepherd A opens `/foo` (gets fd 3) and dies, fd 3
leaks in the FDT and the fs shepherd's file handle is never closed.

### Plan
- Add `ownerSID int16` field to `fdEntry` (in `flock/cmd/linux/fdtable.go`)
- Set `ownerSID` when `sysOpenat` creates an entry
- In `handleDeathNotification`: walk FDT, close all entries where `ownerSID == deadSID`
  - Call `h.fs.Close(e.handle)` for each to release fs-side state
  - Clear the FDT slot
- The syscallHandler needs to be accessible from handleDeathNotification (pass it or
  make it package-level)

### Files
- `flock/cmd/linux/fdtable.go` — add ownerSID to fdEntry
- `flock/cmd/linux/syscalls.go` — set ownerSID in sysOpenat
- `flock/cmd/linux/main.go` — call FDT cleanup in handleDeathNotification

---

## Item 2: SysSubscribeDeaths — Global Death Notification

### Problem
Rachel (window manager) needs to know when ANY shepherd dies so it can withdraw
windows, free backing stores, release pick-rect entries, etc. Currently only uring
peers with a direct connection to the dying shepherd get ProtoDeath.

### Plan

**New syscall: SysSubscribeDeaths = MazzySyscallBase + N** (pick a free slot, e.g. slot 1 or 2)

Kernel side (`kmazarin/ksyscall/death_subscribe.go`, new file):
```
var deathSubscribers [proc.MaxShepherds]struct {
    InUse bool
    SID   int16
}

func SyscallSubscribeDeaths(arg0, ...) int64:
    callerSID := getCurrentSID()
    find empty slot, store SID, return 0

func CleanupDeathSubscriptionsForShepherd(pid int16):
    remove entries where SID == pid
    (called from TerminateShepherd, alongside other cleanup)

func NotifyDeathSubscribers(deadSID int16):
    for each subscriber (excluding deadSID):
        send ProtoDeath via KernelWriteToRing
```

Integration in `kmazarin/kmazarin/threads.go`:
- Call `NotifyDeathSubscribers(pid)` in TerminateShepherd, AFTER CleanupUringIPCForShepherd
  (peers get notified first, then subscribers)
- Call `CleanupDeathSubscriptionsForShepherd(pid)` alongside other cleanup steps

Userspace (`mazarin/sys/subscribe_deaths.go`, new file):
```
func SubscribeDeaths() error {
    _, _, errno := RawSyscall(mazzy.SysSubscribeDeaths, 0, 0, 0, 0, 0, 0)
    ...
}
```

Rachel integration (`flock/cmd/rachel/`):
- Call `sys.SubscribeDeaths()` during startup
- Handle ProtoDeath in existing uring reader loop
- On death: find windows owned by deadSID, withdraw them, free backing stores

**This is a "long syscall" in the sense that it just registers — the actual
notifications arrive asynchronously via the subscriber's existing uring ring.**

### Files
- `shared/mazzy/mazzy.go` — new constant
- `kmazarin/ksyscall/mazzy.go` — dispatch entry
- `kmazarin/ksyscall/death_subscribe.go` — new file (subscriber list, syscall handler, cleanup, notify)
- `kmazarin/kmazarin/threads.go` — call NotifyDeathSubscribers and CleanupDeathSubscriptions
- `mazarin/sys/subscribe_deaths.go` — new file (userspace wrapper)
- Rachel files TBD (depends on rachel's current uring message handling)

---

## Item 3: Shepherd Launch with Arguments

### Problem
`SyscallRunShepherd` currently accepts (name, startVA, numPages, totalBytes) — no way
to pass command-line arguments to the new shepherd. The infrastructure to support this
already exists: `processenv.go:Layout()` accepts an arbitrary `[]string` argv, and
`setupUserStack` in `launch.go` already builds the full Linux-style stack with argc,
argv pointers, envp, and auxv. The Go runtime reads `os.Args` from this.

### Plan

**Extend SyscallRunShepherd** with an args parameter:
- arg4 = pointer to packed args (null-separated strings in caller's address space, or 0 for no args)
- arg5 = total byte length of packed args

Kernel side (`kmazarin/ksyscall/runshepherd.go`):
- `RunShepherdWorkRequest` gets `Args []string` field
- `SyscallRunShepherd`: read packed args from caller's address space, split on null bytes
- `DoRunShepherdWork` → `setupUserStack`: append caller-supplied args to the existing
  `argv := []string{filename, shepherdStr}` before calling `penv.Layout(argv, sw)`

Userspace (`mazarin/sys/runshepherd.go`):
- `RunShepherd` gains `args ...string` variadic parameter
- Pack strings into a page (null-separated), pass pointer + length as arg4/arg5

The new shepherd sees `os.Args = ["/name.elf", "3", "arg1", "arg2", ...]`.

### Files
- `kmazarin/ksyscall/runshepherd.go` — extend RunShepherdWorkRequest, read args
- `kmazarin/ksyscall/launch.go` — pass args through to setupUserStack
- `mazarin/sys/runshepherd.go` — extend RunShepherd wrapper

---

## Item 4: Linux Shepherd FD-per-SID Isolation (Future)

Beyond just cleanup-on-death (#1 above), the linux shepherd's FD table should
probably be per-SID so that fd numbers from different shepherds don't collide.
Currently all callers share one fd namespace. This is a larger refactor and can
be deferred — item #1 (ownerSID + cleanup) is sufficient for correctness.

---

## Build/Verify After All Items

1. `$GO tool task` — all architectures
2. `$GO tool task run-arm64-hvf TIMEOUT=45` — normal boot
3. Kill a shepherd mid-stdin-read — verify linux drains reads, ACKs, kernel frees pages
4. Rachel: verify windows withdrawn on shepherd death
5. Launch shepherd with args — verify `os.Args` contains them
