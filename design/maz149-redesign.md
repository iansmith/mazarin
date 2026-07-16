# MAZ-149 redesign — redirect-flag split (console vs redirected stdio)

Supersedes the first MAZ-149 implementation (commit `aaeb61a7`, PR #85), which a
high-effort code review found to contain a **cross-shepherd deadlock** and a
**PID-reuse race**. This is the vetted re-plan; every item cites the real code it
touches and whether the code supports it.

## Why the first attempt failed

Making **all** fd 1/2 writes blocking delegates (routed via `handler.handle` →
`shep.mu`) reintroduced the exact cycle the old fire-and-forget stdio path
existed to avoid: `handle()` holds `shep.mu` across a blocking fsclient call
(`maz/linux/syscalls.go:360-361` wraps the whole dispatch; `sysOpenat` →
`h.fs.Open` blocks under it), so the single stdout-lane goroutine can wedge on a
shepherd's mu that fs is holding, while fs itself is parked on its own `printf`
→ `write(1)` delegate. Deadlock.

**Root insight:** the writes that cause the deadlock are **console** writes
(fs.maz's `printf`), and those are exactly the ones that DON'T need caller
backpressure — the serial device path is near-lossless already (PollWrite
fallback blocks, doesn't drop). The writes that DO want real backpressure are
the **redirected** ones (`cmd.Output()` capture), and a blocking redirected
write parks on its *parent reader*, not on fs — no cross-shepherd cycle.

So: **split by redirect status.** Console fd 1/2 stay on the non-blocking kernel
fast path (no deadlock). Only *redirected* fd 1/2 divert to a blocking delegate
that routes to the pipe and parks the writer on a full buffer (real
backpressure). To keep console writes off the contended `shep.mu` lane, the
**kernel** must know redirected-vs-console up front → a per-process redirect
flag (approved by Ian).

## Backpressure / drop map (grounds the design)

| sink | full behavior today | under this plan |
|---|---|---|
| serial TX ring (`SysUartWrite`) | PollWrite fallback = block, near-lossless; but a single call >4096 B is silently truncated (finding #5) | fix the 4 KB cap so large writes loop, not truncate; keep block-don't-drop |
| linux-ui display (`dataCh`, cap 32) | non-blocking select = drop | drop + `@@` marker breadcrumb |
| redirected pipe (4 KB buf) | `EWOULDBLOCK`/drop (finding #4) | **park the writer** (real backpressure); `@@` marker only on a bounded-park timeout drop |

## Architecture

- **Non-redirected fd 1/2 (console):** kernel fast path unchanged from
  pre-MAZ-149 — push bytes to the serial TX ring AND fire-and-forget delegate to
  the ring-3 stdout lane, which forwards to the linux-ui display. No `shep.mu`,
  no blocking, no deadlock. (Reverts the problematic parts of items 1/3/4.)
- **Redirected fd 1/2 (flag set):** kernel does a **blocking** delegate (not
  fire-and-forget) to the linux shepherd → `sysWrite` → `KindPipeWrite` →
  `sysWritePipe`, which parks the writer via deferred Reply when the 4 KB buffer
  is full and replies the byte count when it drains. `sysWritePipe` never calls
  fsclient, and the write parks on the parent reader → no fs cycle.

## Work items (vetted against the code)

### 1. Per-process redirect flag — kernel side
- **`kmazarin/proc/proc.go`** — add `StdioRedirectMask uint8` to `Shepherd`
  (near `StartupIntent`/`StartupCwd`, L176-179). Bit 0 = fd 1 redirected, bit 1
  = fd 2 redirected.
- **`kmazarin/proc/shepherd_state.go:198`** — extend `SetStartupState(parentPID,
  intent, cwd)` to also take + store the mask (it already sets the
  race-sensitive startup state atomically under schedulerLock at child creation,
  which is exactly where the flag must be set — before the child is enqueued and
  can run). *Vetted: this is the atomic-at-creation site.*
- **`kmazarin/ksyscall/write.go` `SyscallWrite`** — replace the current
  `IsDelegated` blanket short-circuit (L94-96) with: for fd 1/2, read
  `callerShepherd().StdioRedirectMask`; if the bit is set → blocking
  `DelegateSyscall`; else → the existing UART fast path (serial push +
  fire-and-forget delegate). Non-delegated (linux-self/early-boot) still take
  the fast path. *Vetted: `callerShepherd()` is already fetched at L39.*

### 2. Compute + thread the mask through CloneExec — shepherd + wire
- **`shared/linuxabi/cloneexec_wire.go`** — add `StdioRedirectMask uint8` to
  `CloneExecParams` (L112) and to `MarshalCloneExecParams`/`UnmarshalCloneExecParams`
  (L80/L125). Wire-format change — bump the version/length checks; update
  `cloneexec_wire_test.go`. *Vetted: params are explicit fn args, extendable.*
- **`maz/linux/execve.go` `sysExecve`** — before `sys.CloneExec`, get
  `reservedPID = sys.GetVforkReservedPID(req.CallerTID)` (same call
  `resolveTargetFDT` uses), peek `pendingChildFDTs[reservedPID]`, and set the
  mask bit for each of fd 1/2 whose entry Kind is NOT KindStdout/KindStderr
  (i.e. redirected to a pipe/file). Pass it into `MarshalCloneExecParams`.
  *Vetted: the transient's dup3 has already populated the pending table by
  execve time; reservedPID is obtainable pre-CloneExec.*
- **`kmazarin/ksyscall/clone_exec_svc.go` / `clone_exec.go`** — carry the mask
  from params → `CloneExecRequest` → `CreateCloneExecThread` →
  `createCloneExecThreadImpl` → `SetStartupState` (before enqueue).

### 3. Runtime redirect maintenance (dup3/close on fd 1/2)
- A process that `dup3`s a pipe onto fd 1 *after* exec (or closes it back to
  console) must update the kernel flag. **`maz/linux/syscalls.go`** `sysDup3` /
  `sysClose` / `sysFcntl`: when the mutated fd is 1 or 2, call a new SVC
  `SysSetStdioRedirect(pid, mask)` reflecting the new Kind.
- **SCOPE DECISION:** the fork/exec DoD (forkexectest stage 2) sets the redirect
  pre-exec (item 2 covers it). Runtime re-redirect is rare. **Proposal:** land
  item 2 first (covers the DoD), do item 3 as a fast-follow; note the tiny
  window between table update and SVC (a racing write could take the wrong path
  for one write). Flag for Ian.

### 4. Revert console path to fast path (undo the deadlock-causing parts)
- **`kmazarin/ksyscall/write.go`** — console (flag clear) keeps the UART push +
  fire-and-forget delegate. The shepherd no longer emits KindStdout to serial,
  so **revert `sysWrite`'s KindStdout/KindStderr branch** (`maz/linux/syscalls.go`)
  to the pre-MAZ-149 no-op reply for the (now non-existent) blocking-console case
  — or better, that branch is simply never reached for console.
- **`maz/linux/main.go` stdout lane** — revert to the fire-and-forget forward
  (copy bytes + `ReleaseDelegatePage` + feed `dataCh`); it no longer calls
  `handler.handle`, so it never takes `shep.mu`. **This removes the deadlock.**
  Keep `displayFeed`? No — revert to the original direct `dataCh` feed.

### 5. Redirected write → pipe with writer-park (new backpressure)
- **`maz/linux/internal/pipe/pipe.go`** — add a **writer-waiter** list mirroring
  the reader `waiters` (`buf.waiters` L64, `Park`/`TakeWaiters` L184+). `Write`
  full → the shepherd parks the writer; `Read`/`TakeWaiters` drain → wake parked
  writers. *Vetted: only reader-park exists today; this is a symmetric addition.*
- **`maz/linux/syscalls.go` `sysWritePipe` (~L1099)** — on `ErrWouldBlock`,
  instead of replying `EWOULDBLOCK` (finding #4), park the request (deferred
  Reply) on the pipe's writer-waiter list; a later reader-drain replies the byte
  count. Bound the park with a timeout; on timeout, drop + `@@` marker (item 7).
  Deferred Reply releases `shep.mu` (handle returns) so no lock is held while
  parked — mirrors `sysReadPipe`.

### 6. Fix `SysUartWrite` — 4 KB cap + byte-order (findings #3, #5)
- **`kmazarin/ksyscall/uart_write.go`** — remove the `count > 4096` truncate
  (L45-47); loop over the whole buffer in ≤256 B chunks (it already chunks).
- **Byte order:** don't mix async-ring + sync-poll per byte (finding #3). On
  `QueueByteTry` full, **spin until the ring accepts the byte** (or use
  `QueueByte` which blocks) rather than `PollWrite`-jumping the queue. Preserves
  order; still lossless.

### 7. Drop marker `@@` (or `@\`)
- Where a drop remains possible (display `dataCh` full; bounded-park timeout on
  the pipe), overwrite the last buffered byte(s) with the `@@` breadcrumb so a
  reader can detect the loss. **Pipe/display buffers are touched only under
  `shep.mu` (pure Go, no interrupt)** → race-free to mutate the tail. Do NOT
  attempt to mark the serial TX ring (interrupt-drained concurrently; the byte
  may already be on the wire, needs the driver TX lock) — item 6 makes serial
  block-don't-drop instead. Sentinel `@@` (2 bytes) or `@\` per Ian; multi-char
  to cut collision odds in the boot-test log.

### 8. Fix the PID-reuse race (finding #2)
- `evictStaleChildShepherd` on `EventChildExit` (delegate loop) races the
  reused-PID new child's `getShepherd` (file-lane workers) → can tear down a live
  child's redirected pipe.
- **Options:** (a) **lean on MAZ-150** — monotonic PIDs eliminate immediate
  reuse, so EventChildExit(p) is processed long before p is reused → the close
  is race-free. Sequence MAZ-149 after MAZ-150. (b) **generation guard** — tag
  each `ShepherdFilesystemData` with a creation generation and only evict if it
  matches the exited child (requires the kernel to carry a child generation in
  the exit notification). **Recommendation: (a)** — finding #2 and forkexectest
  stages 3/4 are the *same* LIFO-reuse root cause; MAZ-150 fixes both, and this
  EOF-close becomes correct for free. Note the dependency on the ticket.

## Sequencing

1. **MAZ-150 first** (monotonic PIDs) — makes the EOF-close race-free (item 8a)
   and un-breaks stages 3/4.
2. Items 1+2+4 (redirect flag + console fast-path revert) — gets the DoD
   (forkexectest stage 2) with no deadlock. Verify.
3. Items 5+7 (pipe writer-park + drop marker) — real backpressure for capture.
4. Item 6 (SysUartWrite 4 KB + order) — independent, can land anytime.
5. Item 3 (runtime redirect maintenance) — fast-follow.

## Open questions for Ian

1. **MAZ-150 dependency** (item 8a) vs a standalone generation guard (8b)?
   Recommend depending on MAZ-150.
2. **Runtime re-redirect** (item 3) in-scope now or fast-follow? The DoD only
   needs pre-exec redirect.
3. Redirected writes on the **file lane** (ring 2, low volume) vs their own
   dedicated blocking lane? File lane is simpler; contention is low since
   captured-output programs are rare. Recommend file lane; revisit if measured.
4. Sentinel `@@` vs `@\` — pick one.

## What carries over from commit aaeb61a7 unchanged

- Item 6 (fork/exec `Criticalf` → `Logf`) — keep.
- `SysUartWrite` fast-ring (item 2) — keep, plus the item-6-above fixes.
- The FD-table correctness fixes are on the MAZ-63 base, untouched.
