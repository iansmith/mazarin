# Continuation: Block I/O Stall During Large File Loads

## The Problem

When loading large files from ext2 (e.g., clocks.elf at 173 blocks), the block I/O
path in the fs shepherd stalls intermittently. The stall is at the boundary between
the first batch of 8 blocks and the second batch — the first IOUringEnterBlocking
call returns 8 completions successfully, but the doReadBatch loop never continues
to batch 2. The system enters a futex/nanosleep loop and never recovers.

The stall is **intermittent** — sometimes the same file loads fine.

## What We Know Works (Confirmed)

1. **VirtIO device side is perfect.** QEMU trace (`-d trace:virtio_blk_*`) confirms
   all block reads complete with status 0 and the device sends `virtio_notify` for
   each completion. The device processes all requests and stops because the guest
   stops submitting.

2. **The kernel io_uring path works.** The SVC handler (SyscallIOUringEnter) correctly:
   - Submits SQEs to VirtIO (Phase A)
   - Blocks the thread when CQ is empty (BlockForIOUring)
   - Gets woken by WakeIOUringFromIRQ when CQEs arrive
   - Returns the correct completion count on re-entry via RewindToSyscall
   
3. **The IRQ top-half correctly writes CQEs.** Epoch stats confirm `ev=cq` (events
   equals CQEs written), `miss=0`.

4. **IOUringEnterBlocking returns the right value.** Diagnostic showed re-entry #19
   found `cqH=4241 cqT=4249 comp=8 min=8` and returned 8 via fastRet. The SVC
   handler returned 8 to userspace.

5. **`uring.Recv()` uses the identical pattern and works.** Every shepherd's Dispatcher
   reader goroutine calls `runtime_entersyscallblock()` → `RawSyscall6(SysUringRecv)`
   → `runtime_exitsyscall()` and never stalls. This proves the Go runtime's P
   recovery mechanism (entersyscallblock/exitsyscall) is NOT broken.

## What Is NOT The Problem

- **NOT P starvation / "can't recover the P"** — `uring.Recv` uses the same
  `entersyscallblock → exitsyscall` path and works fine. This has been a recurring
  false diagnosis across multiple sessions. Do not go down this path again.
  
- **NOT cache coherency** — MAIR[0] and MAIR[3] both map to 0xFF (Normal WB).
  No aliasing between KernelMMIOOffset mapping and direct mapping.

- **NOT the VirtIO device** — QEMU traces confirm all completions with status 0.

- **NOT the io_uring CQE write path** — CQTail advances correctly, CQEs contain
  correct data (Res = positive UsedLen values).

## Key Observations

1. **The stall happens AFTER IOUringEnterBlocking returns correctly.** The handler
   returned 8. The next thing we see is `[T18:98]` (futex) then `[T18:101]`
   (nanosleep) flood. No more IOUringEnter calls, no more block reads.

2. **The stall is intermittent.** Same code, same file — sometimes works, sometimes
   stalls. This points to a timing-dependent race condition.

3. **Switching to IOUringEnter (P-holding, RawSyscall without entersyscall/exitsyscall)
   eliminates the stall.** But this is likely fixing the symptom by changing timing,
   not addressing the root cause.

4. **The doReadBatch code holds a mutex (`d.mu`) across the IOUringEnterBlocking
   call.** The mutex is acquired in ReadBlocks (line 658) before doReadBatch, and
   released after it returns. Inside doReadBatch, `runtime_entersyscallblock()` 
   releases the P while the mutex is held.

5. **fs does NOT launch a dedicated goroutine for block I/O.** Block reads run on
   whichever goroutine calls ReadBlock/ReadBlocks (bootSequence goroutine or main
   serve loop). In contrast, the uring Dispatcher reader IS a dedicated goroutine
   (`go r.loop()`) that blocks indefinitely.

## Differences Between Recv (works) and Block I/O (stalls)

| Aspect | `uring.Recv` | Block I/O |
|--------|-------------|-----------|
| Goroutine | Dedicated (`go r.loop()`) | Shared (serve loop / bootSequence) |
| Mutex held? | No | Yes (`d.mu`) |
| Wake frequency | Rare (IPC messages) | Rapid (~1ms per batch) |
| Kernel syscall | SysUringRecv | SysIOUringEnter |
| Kernel wake mechanism | RewindToSyscall | RewindToSyscall |
| Iterations | Single blocking call in loop | Nested: batch loop × blocking call |

## Theories of the Case

### Theory A: Dedicated goroutine matters
The uring reader runs on a dedicated goroutine created specifically for blocking.
Block I/O runs on a goroutine that was originally doing other work (serve loop,
boot sequence). Maybe the Go runtime handles the M/goroutine association differently
when a goroutine that was previously doing normal work transitions to a blocking
syscall pattern.

**Test:** Move block I/O to a dedicated goroutine with a channel interface:
```go
type blockReq struct {
    ringID    int
    submitted uint32
    minComp   uint32
    result    chan blockResp
}
type blockResp struct {
    nret int
    err  error
}
```
Launch `go blockIOWorker()` that loops on the channel, calls IOUringEnterBlocking,
sends results back. This mirrors the Recv pattern exactly.

### Theory B: Mutex + entersyscallblock interaction
Holding `sync.Mutex` across `entersyscallblock` may create a problematic state.
When entersyscallblock releases the P, another goroutine can run. If that goroutine
tries to lock the same mutex, it blocks (mutex semaphore). When the original
goroutine's exitsyscall tries to resume, there may be a scheduling interaction
with the mutex waiter that causes the goroutine to be lost.

**Test:** Release the mutex before IOUringEnterBlocking and reacquire after. This
requires restructuring so the ring state is consistent across the unlock/lock gap.

### Theory C: RewindToSyscall corrupts goroutine state for entersyscallblock path
When the kernel does RewindToSyscall, it modifies the thread's saved register
context (ELR, x0). If the goroutine is parked by exitsyscall0 and later resumed
via `gogo(&gp.sched)`, `gogo` restores PC/SP from `gp.sched` which was saved by
`entersyscallblock` — pointing to the `RawSyscall6` call. The goroutine would
re-execute RawSyscall6, causing a SECOND SVC. This double-execution might interact
badly with the kernel's ring state or the goroutine's stack.

**Test:** Add UART diagnostics inside the RawSyscall6 assembly stub (or a wrapper)
to detect if it's called twice for a single IOUringEnterBlocking invocation.

### Theory D: Rapid entersyscallblock/exitsyscall cycling
Block I/O calls entersyscallblock → exitsyscall every ~1ms in rapid succession
(once per batch). Each cycle: release P → handoffp → create/wake M → exitsyscall
→ reacquire P (or park). The rapid cycling might exhaust some runtime resource
(M pool, scheduler state) or trigger a race that slow Recv calls never hit.

**Test:** Add artificial delay between batches to see if slowing the cycle prevents
the stall. Also check if the stall correlates with the number of clone (new M)
syscalls.

## Recommended Next Step

**Theory A is the most direct test** and aligns with the user's architectural
preference. Move block I/O to a dedicated goroutine, mirroring the Recv pattern.
If the stall disappears, it confirms the dedicated-goroutine architecture is
correct and narrows the root cause to something about goroutine lifecycle or
the shared-goroutine interaction with entersyscallblock.

## Key Files

- `flock/cmd/fs/main.go` — doReadBatch (line ~515), ReadBlocks (line ~653)
- `mazarin/sys/iouring.go` — IOUringEnterBlocking (line 51)
- `mazarin/uring/syscall.go` — Recv (line 72), same entersyscallblock pattern
- `mazarin/uring/reader.go` — Reader.loop (line 39), dedicated goroutine
- `kmazarin/ksyscall/iouring.go` — SyscallIOUringEnter handler
- `kmazarin/kmazarin/iouring.go` — BlockForIOUring, WakeIOUringFromIRQ

## QEMU Tracing (for future use)

To enable VirtIO device-side tracing, add to the QEMU command line in Taskfile.yml
(run-arm64-hvf-background task):
```
-d trace:virtio_blk_handle_read,trace:virtio_blk_rw_complete,trace:virtio_queue_notify,trace:virtio_notify -D /tmp/qemu-arm64-trace.log
```
This writes to a separate file from the serial log. The trace file can be large
(200K+ lines for a 10s run).
