# Plan: Uring-Based IPC (Replacing Mailbox + Delegate Systems)

## Context

The mailbox system has a fundamental P-scheduling bug: when fontsvc (a .maz goroutine inside rachel) forwards a message to `rachelCh` and re-enters `MailboxRecv`, `entersyscall` releases the Go P, and the kernel blocks the thread. The P sits in `_Psyscall` with `mailboxLoop`'s goroutine on the runqueue, but no rachel thread actively acquires the P. The system depends on sysmon's `retake` polling, which is unreliable — causing an intermittent 30s stall where rachel never processes linux's AppStart.

The fix: each mancini program (shepherd) gets a **dedicated kernel thread** (like sysmon) that blocks in a uring-wait SVC. When a message arrives, the thread wakes via `exitsyscall` (active P acquisition — pull, not push), reads the message, and sends it on a Go channel. This eliminates the sysmon dependency entirely.

This also replaces the delegate system (`BlockForDelegatedSyscall`/`WakeDelegateThread`), which has the same P-scheduling vulnerability.

---

## 1. Message Ring Structure

- New per-shepherd ring: circular buffer of **128-byte** fixed-size message slots
- Kernel-allocated at shepherd start (in `DoRunShepherdWork`)
- Header per message: protocol discriminator (2-4 bytes) + sender SID + payload
- Ring metadata: head/tail atomics, capacity (e.g., 64 slots = 8KB + metadata, fits in 3 pages)
- Multiple producers (any shepherd), single consumer (uring-reader thread)
- Separate from the existing VirtIO io_uring (different purpose, simpler structure)

**New shared struct:** `shared/ipc/ring.go` — ring layout, message header format

---

## 2. Uring UUID & Discovery

### At shepherd launch (`DoRunShepherdWork` in `kmazarin/ksyscall/runshepherd.go`):
- Kernel generates a unique ID (UUID or 64-bit sequential — TBD)
- Allocates the message ring pages (kernel-owned, shared to the shepherd)
- Stores UUID + ring PA in a **concurrent-safe map** (`sync.Map` or mutex-protected map)
  - This runs on thread 0's growable stack via KernelSVCWorker — full Go allocation is safe
- Stores UUID in `proc.Shepherd` struct (new field)

### Enhanced `SyscallShepherdInfo` (`kmazarin/ksyscall/shepherd_info.go`):
- Add UUID field to `hid.ShepherdInfoEntry`
- Callers can discover any shepherd's uring UUID by listing shepherds

### Convert `SyscallShepherdInfo` to KernelSVCWorker:
- Currently runs directly in SVC context
- Needs heap access for the UUID map lookup
- Route through `KernelSVCWorker` like LoadMaz/RunShepherd

---

## 3. New Syscalls

### `SysUringConnect(uuid)` — Map target's uring into caller's address space
- Kernel looks up UUID in the map → finds target shepherd's ring pages
- Maps ring pages into calling shepherd's page table (read-write for SQ, read for metadata)
- Records the connection: `(callerSID, targetSID)` with **refcount**
- Returns a handle (small integer or the mapped VA)

### `SysUringSend(handle, *msg)` — Submit a 128-byte message to target's ring
- Kernel writes message to target's ring (SQ push)
- Wakes target's blocked uring-reader thread if sleeping
- Returns 0 on success, -EAGAIN if ring full

### `SysUringRecv(*buf)` — Block until message arrives (consumer side)
- Drains one message from own ring → copies to userspace buf
- If ring empty: blocks thread (like current `BlockForMailboxRecv` but cleaner)
- When woken: SVC re-executes via rewind, drains message, returns
- **No WFI fallback needed** — the dedicated thread always has a partner thread to switch to

### `SysUringRelease(handle)` — Release connection to a target's ring
- Decrements refcount for the `(callerSID, targetSID)` connection
- If refcount reaches 0: unmaps ring pages from caller's address space
- Called explicitly by shepherds when done, or during shepherd cleanup

---

## 4. Dedicated Uring-Reader Thread (Userspace)

- Each mancini program spawns a goroutine on a dedicated M (like sysmon uses `newm`)
- This goroutine loops: `SysUringRecv(&msg)` → discriminate by protocol header → send on appropriate Go channel
- The Go runtime creates the M via `clone` → kernel thread exists independently
- When `SysUringRecv` returns, `exitsyscall` actively acquires the P → channel send is immediate
- This is the **standard pattern** — same as how sysmon's M works, well-tested in Go runtime

**New userspace package:** `mazarin/uring/` — ring client, reader thread, channel dispatch

---

## 5. Death Notification & Refcounting

### When a shepherd dies (`TerminateShepherd` / `releaseShepherdSchedLockHeld`):
1. Kernel iterates the connection refcount table
2. For each shepherd holding a connection to the dead shepherd:
   - Submit a **death notification message** (special protocol code) to their uring
   - The connection mapping stays valid (pages remain mapped) — not torn down yet
3. Dead shepherd's own uring is marked "dead" but not freed until all refcounts drop to 0
4. Receiving shepherds process the death notification, clean up state, call `SysUringRelease`

### Refcount lifecycle:
- `SysUringConnect` → refcount++
- `SysUringRelease` → refcount--, unmap if 0
- Shepherd death → death notifications sent, ring stays until last release
- Shepherd cleanup (in `releaseShepherdSchedLockHeld`) deferred until all refs released

### Kernel bookkeeping:
- Per-ring refcount + list of connected SIDs
- New field in `proc.Shepherd`: ring state (active / dead-pending-release)

---

## 6. Removals

### Kernel (`kmazarin/kmazarin/`):
- `mailbox.go`: `mailboxQueues`, `mailboxBlockedTID`, `mailboxBlockedPtr`, `mailboxSendKernel`, `mailboxSendKernelWithSwitch`, `BlockForMailboxRecv`, `drainMailboxQueue`, `CleanupMailboxForShepherd`
- `mailbox.go`: VA translation cache stays (still needed for backing store / font cache page sharing, independent of uring)
- `ipc_bridge.go`: `BlockForDelegatedSyscall`, `BlockForDelegatedRecv`, `WakeDelegateThread`, `WakeDelegateCallerThread`
- `notify_bridge.go`: `BlockForDirtyNotify`, `WakeDirtyNotifyThread` — **keep for now** (constraint dirty notifications use a different path, driven by timer top-half, not inter-shepherd IPC)

### Syscall handlers (`kmazarin/ksyscall/`):
- `mailbox.go`: `SyscallMailboxSend`, `SyscallMailboxRecv`, `SyscallMailboxMapPage` (page sharing syscall stays, just the mailbox send/recv go away)
- `delegate.go`: Full delegate registration/dispatch/reply machinery

### Thread states (`kmazarin/kmazarin/threads.go`):
- `ThreadBlockedMailbox` — replaced by uring block state
- `ThreadBlockedDelegate` — replaced by uring-based request/reply
- `ThreadBlockedDelegateRecv` — replaced by uring-based request/reply

### Userspace (`mazarin/sys/`):
- `mailbox.go`: `MailboxSend`, `MailboxRecv`, `MailboxMapPage` (page sharing stays)
- `delegate.go`: delegate registration/handling

### Shepherd code:
- `flock/cmd/fontsvc/main.go`: `MazarinMain` mailbox loop → replaced by uring channel consumer
- `flock/cmd/rachel/main.go`: `mailboxLoop` goroutine → reads from uring channel instead
- `flock/cmd/linux/main.go`: `announceToWM` uses uring send instead of mailbox
- `flock/cmd/fs/main.go`: delegate handling → uring-based request/reply

---

## 7. What Stays Unchanged

- **VA translation cache** (`vaCache` in `mailbox.go`) — still needed for page sharing (backing stores, font caches). Rename/move to a `page_sharing.go` file.
- **`SyscallMailboxMapPage`** — rename to `SyscallSharePages` or similar, keeps cross-shepherd page mapping
- **Constraint dirty notification system** (`notify_bridge.go`, `constraint_notify.go`) — driven by timer top-half, not inter-shepherd IPC
- **SoftIRQ / WaitSoftIRQ** — device event delivery (keyboard, mouse, block I/O)
- **Existing VirtIO io_uring** — for block device I/O, completely separate
- **KernelSVCWorker** — still needed for LoadMaz, RunShepherd (heavy kernel work on thread 0)

---

## 8. Migration Order (suggested phases)

1. **Ring infrastructure**: shared ring struct, kernel allocation, UUID map, `SysUringConnect`/`SysUringSend`/`SysUringRecv`/`SysUringRelease` syscalls
2. **Userspace uring package**: `mazarin/uring/` — ring client, reader thread
3. **Rachel + linux migration**: replace mailbox send/recv with uring, prove boot chain works
4. **fs delegate migration**: replace delegate system with uring request/reply
5. **Death notification**: refcounting, cleanup, death messages
6. **Removal**: delete old mailbox/delegate code and thread states

---

## Verification

- Build: `$GO tool task clean && $GO tool task run-arm64-hvf TIMEOUT=30`
- Success criteria: `BackingStoreReady` delivered, clocks running
- Regression: 5 consecutive runs with no `[fs] FATAL: linux not ready` stalls
- Safe serial read: `$GO tool safe-serial-read /tmp/diplomat-arm64-serial.log`
