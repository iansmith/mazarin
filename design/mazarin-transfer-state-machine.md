# mazarin/transfer — page-ownership state machine

**Status:** v1 (Mode 1 only). Modes 2 (MAZ-53) and 3 (MAZ-54) extend this state machine; see the per-mode sections below.

**Audience:** anyone implementing, debugging, or consuming `mazarin/transfer`. Read this first when you suspect a page-leak, a stuck `Wait()`, or unclear ownership.

## Why this exists

Several shepherd-to-shepherd flows ship variable-length payloads that don't fit inside a uring SQE — file reads, mail headers, HTTP bodies. Today each producer-consumer pair reinvents some subset of:

- page allocation,
- mapping pages from one shepherd's address space into another's,
- handing off ownership when the data is ready,
- cleaning up on either side's crash.

`mazarin/transfer` factors all of this into one library. Each transfer is described by a single tracked state machine: at any instant, exactly one shepherd has the pages in its address space and is allowed to read/write them; the others see only metadata (a `Handle` carrying VA + page count).

## Surface

```
sophie / claudeclient (client)                   maildb / protocol-http (server)
  │                                                │
  │  h, err := transfer.Reserve(svr, kind, N) ───► │  s, err := transfer.Allocate(N, extra)
  │  // h.Bytes() and h.Writer() are usable        │  // s.Bytes() and s.ClientView() know
  │  // because pages live in client's space        │  // the layout, but the body region
  │  //                                             │  // is mapped in the client, not here
  │                                                 │
  │  io.Copy(h.Writer(), src)                       │  err := s.Wait()
  │                                                 │  // blocks here until Commit
  │                                                 │
  │  err := h.Commit()                       ────► │  // returns; s.Bytes() now safe
  │  // pages back in svr's space                   │  // process the data ...
  │                                                 │  s.Release()
```

Three modes share the foundation:

| Mode | Initiator | What moves | Where used |
|------|-----------|------------|------------|
| **1: Reserve/Commit** | client | pages cycle client → server | MAZ-50, sophie → protocol-claude prompt-budget IPC |
| **2: HandOff** (MAZ-53) | server | full Slab ownership transferred to client | fs page-loan, protocol-claude → sophie response |
| **3: GrantWrite** (MAZ-54) | server | sub-region R/W mapping; server keeps ownership | claudeclient writes prompt into protocol-claude's body Slab |

This doc covers Mode 1 in depth and sketches how Modes 2/3 extend the state machine.

## Mode 1 state machine

```
        ┌──────────────────┐
        │      (start)     │
        └────────┬─────────┘
                 │
                 │ server: Allocate(size, extraPages)
                 ▼
        ┌──────────────────┐
        │   slabAllocated  │ ◄────┐  pages in server's VA;
        └────────┬─────────┘      │  server.Bytes valid;
                 │                │  server.ClientView meaningful but unmapped
                 │ IPC handler:   │
                 │ sys.Transfer   │
                 │ Pages(client)  │
                 ▼                │
        ┌──────────────────────┐  │
        │ slabMappedToClient   │  │  pages in CLIENT's VA;
        └────────┬─────────────┘  │  server.Bytes returns nil;
                 │                │  client.Handle.Bytes/Writer valid;
                 │ client:        │  s.Wait blocks
                 │ Handle.Commit  │
                 │ (TransferAnd-  │
                 │  Unmap →       │
                 │  server)       │
                 ▼                │
        ┌──────────────────┐      │
        │   slabCommitted  │      │  pages back in server's VA;
        └────────┬─────────┘      │  s.Wait returns nil;
                 │                │  server.Bytes valid
                 │                │  client.Handle stale (do not reuse)
                 │ server:        │
                 │ Release        │
                 │ (or crash)     │
                 ▼                │
        ┌──────────────────┐      │
        │   slabReleased   │      │  pages freed
        └──────────────────┘      │
                                  │
              Crash paths ────────┘
              (see below)
```

## Per-state invariants

### slabAllocated

- **Pages live in:** server.
- **server.Bytes()** returns a slice over the full allocation (`extraPages*PageSize + body`). Valid to read/write the entire region; the server hasn't yet promised any of it to a client.
- **server.ClientView()** returns `(va + extraPages*PageSize, pages - extraPages)` — the layout that *will* be exposed to the client when the IPC handler maps it across, but **the client doesn't see anything yet**.
- **client.Handle:** does not exist; client hasn't called Reserve yet.
- **Triggers leaving this state:**
  - The Mode 1 IPC dispatcher receives a `ProtoTransferReq{Op: Reserve}`, calls `sys.TransferPages(clientSID, ...)` to move the body region to the client, transitions to `slabMappedToClient`, and replies with `ProtoTransferResp{VA: clientVA, Pages: bodyPages}`.

### slabMappedToClient

- **Pages live in:** client.
- **server.Bytes()** returns `nil`. The pages aren't in the server's VA; reading would fault.
- **client.Handle:** valid. `h.Bytes()` and `h.Writer()` are the canonical access path.
- **`s.Wait()`** blocks on the Slab's `commitCh`. The dispatcher closes this channel (or sends on it) when the matching Commit arrives.
- **Triggers leaving this state:**
  - Client calls `h.Commit()`: invokes `sys.TransferAndUnmap(serverSID, h.VA, h.Pages)` (atomic — release-first), then sends `ProtoTransferReq{Op: Commit, VA, Pages}` to wake `Wait`. State transitions to `slabCommitted`.
  - Client crash mid-fill: kernel reclaims the orphaned mapping; server's `Wait` times out after a configurable interval (default 30s) and returns `ErrCommitTimeout`. State transitions to `slabReleased`.

### slabCommitted

- **Pages live in:** server.
- **server.Bytes()** returns a slice over the full allocation; the body region now contains what the client wrote. The leading `extraPages` (server-only) are still whatever the server initialized them with.
- **client.Handle:** stale. The pages are no longer mapped at `h.VA`; touching `h.Bytes()` would fault. Implementations should not retain a Handle past Commit.
- **`s.Wait()`** has returned nil.
- **Triggers leaving this state:**
  - Server calls `Release()`. Pages freed; state transitions to `slabReleased`.

### slabReleased

- **Pages live in:** the allocator's free list.
- **server.Bytes()** returns `nil`.
- **server.ClientView()** returns `(0, 0)`.
- **`s.Release()`** is idempotent; calling it again returns nil.
- **`s.Wait()`** on a released Slab is a programming error (no defined behavior; v1 panics with a "Wait on released Slab" message).

## Crash semantics

The hard cases. Each entry: who crashed, when, what the other side observes, and how pages get reclaimed.

### Client crash between Reserve and Commit

**State at crash:** `slabMappedToClient`. Pages live in client's now-defunct address space.

- **Kernel:** sees the client's process exit, reclaims its page mappings via `CleanupShepherdPages`. The physical pages return to the free list immediately, but the server doesn't know that yet — its Slab still references the (now-freed) page range.
- **Server:** `Wait()` is blocked on `commitCh`. After the timeout (default 30s), `Wait` returns `ErrCommitTimeout`. The Slab transitions to `slabReleased`; calling `Release()` on it is idempotent and skips the `FreePages` call (kernel already reclaimed them).
- **Follow-up:** future work could replace the timeout with a kernel-pushed "shepherd died" notification routed through the dispatcher. Tracked in the deferred list of MAZ-50's plan.

### Server crash before Wait returns

**State at crash:** `slabMappedToClient` (Reserve was handled; client is mid-fill).

- **Kernel:** reclaims server's process state, including any in-flight uring rings and the Slab metadata.
- **Client:** about to call `Commit`. `sys.TransferAndUnmap(serverSID, ...)` will fail with EFAULT (server's SID is no longer a valid mapping target). `Handle.Commit` returns this error.
- **Pages:** the client's mapping is left dangling but harmless — the client's `TransferAndUnmap` failed before unmapping, so the client retains R/W until it `munmap`s or exits. Best-effort: client should call a cleanup helper (TBD) to drop the mapping.

### Server crash after Wait returns, before Release

**State at crash:** `slabCommitted`.

- **Kernel:** reclaims pages via `CleanupShepherdPages`.
- **Client:** unaffected. The Handle is already stale by this point; the client has moved on.

### Both crash simultaneously

Kernel reclaims both shepherds' page sets independently. No new failure mode.

## How Mode 2 extends this

> Note: The original framing (Mode 2 = ownership handoff / Mode 3 = sub-region
> grant) was superseded during MAZ-53 planning. Both collapsed into a single
> **shared-mapping** primitive. MAZ-54 is closed as a duplicate of MAZ-53.

### Mode 2 — shared mapping ([MAZ-53](https://linear.app/mazarin/issue/MAZ-53))

Mode 2 adds a parallel sharing dimension on top of any `slabAllocated` or
`slabCommitted` Slab. The Slab's main state does not change; the kernel tracks
consumers separately via `PageDescriptor.RefCount`.

**Sender-side state transitions:**

```text
slabAllocated (or slabCommitted)
  │
  ├── Slab.Share(consumer)        → kernel RefCount++ for all Slab pages
  │   or Slab.ShareRange(…)         ProtoShareReq IPC sent to consumer
  │                                 Outstanding-shares table entry added
  │
  │   [consumer mapping active — sender can still read/write; both sides see
  │    the same physical frames via kernel-maintained shared PT entries]
  │
  └── on ProtoShareRelease from consumer →
        sys.UnshareFromTarget(consumer, consumerVA, numPages)
        RefCount-- for shared pages
        PD_SHARED cleared when RefCount drops to owner-only (1)
        Outstanding-shares table entry removed
        releaseHook called (if registered)
```

Multiple consumers can hold shares of the same Slab simultaneously.
RefCount is the kernel's authoritative count; the sender's outstanding-shares
table is the source of truth for *who* owes a Release.

**Consumer-side lifecycle:**

```text
(RegisterShareConsumer wired into dispatcher before d.Start())
  │
  ├── ReceiveShare(sender) blocks on per-sender channel
  │   dispatcher routes ProtoShareReq → channel → returns *Share
  │
  │   Share.VA   = byte-granular start in consumer's address space
  │   Share.Bytes = logical byte count (may be sub-page for ShareRange)
  │
  ├── Share.AsBytes() → unsafe.Slice view over [VA, VA+Bytes)
  │   R/W — writes propagate to sender's Slab view (same physical frames)
  │
  └── Share.Release() → fire-and-forget ProtoShareRelease IPC to sender
        sets Share.released = true; subsequent Release() calls return nil
        IPC sent only for kernel-established Shares (kernelMapped == true)
```

**Crash recovery:**

| Crash scenario | Kernel behaviour | Sender state |
|---|---|---|
| Consumer dies mid-share | `CleanupShepherdPages` decrements RefCount for shared pages (Owner != dead); sender's mapping intact | Sender gets ProtoDeath; can remove consumer's entry from outstanding-shares table via death handler |
| Sender dies mid-share | Kernel frees owner's pages; consumers' shared PT entries become dangling | Consumers register ProtoDeath handlers to invalidate in-flight Shares |
| Both die | Each cleaned up independently; no new failure mode | — |

**Chained shares (A → B → C):**

`SyscallSharePagesWithTarget` permits sharing if the caller owns the page
*or* has a valid shared mapping (`PD_SHARED` set and page is in caller's PT).
This allows protocol-claude to re-share pages it received from protocol-http.
The same relaxation applies to `SyscallUnshareFromTarget` so B can call
`UnshareFromTarget(C, …)` when C's Release IPC arrives.

RefCount tracks the total depth: A shares with B → RefCount=2; B shares with
C → RefCount=3; C releases → RefCount=2 (B's mapping intact); B releases →
RefCount=1 (PD_SHARED cleared, owner-only).

**v1 trust model (boundary pages):**

`SyscallSharePagesWithTarget` maps at page granularity (4 KiB on ARM64).
A `ShareRange(consumer, offset, length)` whose offset or
`offset+length` is not page-aligned exposes bytes outside `[offset,
offset+length)` on the boundary pages. The consumer has R/W on the full
exposed pages.

v1 accepts this for in-tree consumers (protocol-http, protocol-claude, sophie)
that are code-reviewed and won't scribble outside their grants. The boundary-
page staging-copy enhancement (pre-zero or copy bytes outside the grant before
sharing; restore after Release) is filed as a deferred follow-up.

**fs page-loan decision (DoD §9):**

The existing `fsclient` page-loan path (in `mazarin/fsclient`) predates the
`transfer` library and uses `SysSharePagesWithClient` / `SysMapSharedPage`
directly rather than going through `mazarin/transfer`. Decision (pinned during
MAZ-53): **leave `fsclient` as-is for v1.** The `transfer.Share` library is
the canonical path for new code; `fsclient` is the reference implementation
showing the lower-level mechanics. A migration recipe for `fsclient` is a
deferred enhancement — tracked in the MAZ-53 ticket out-of-scope list.

## Sanity checks for implementers

- A Slab can only be in one `slabState` at a time. Concurrent IPC + Wait must be serialized via the dispatcher goroutine ordering — never read/write state from two goroutines without a synchronizing channel.
- Never call `Slab.Bytes()` from a path that's also being driven by a uring dispatcher handler unless the state is verified `slabAllocated` or `slabCommitted`. Returning nil is a safety mechanism, not a correctness shield.
- `Handle.Commit()` is **release-first**: `TransferAndUnmap` returns only when the kernel has atomically moved the pages back to the server. The IPC notification that wakes `Wait()` is sent *after* that SVC returns. This ordering is what guarantees the server can't observe torn writes through a stale TLB.
- `Slab.Share` / `Slab.ShareRange` are map-first then notify: `sys.SharePagesWithTarget` completes before the `ProtoShareReq` IPC is sent. If sharetest crashes after mapping but before the IPC, the consumer never receives the share; the pages remain in the sender's address space with an elevated RefCount. The elevated RefCount is harmless — the consumer has no mapping and cannot access the pages. Kernel cleanup on sender death restores pages to single-owner state. If the IPC is lost in flight (consumer's ring full), the dropped `ProtoShareRelease` means the sender never revokes; pages stay in target's space until consumer exits and kernel's `CleanupShepherdPages` handles them.

## Cross-references

- [MAZ-50](https://linear.app/mazarin/issue/MAZ-50) — foundation + Mode 1 (this doc).
- [MAZ-53](https://linear.app/mazarin/issue/MAZ-53) — Mode 2: shared mapping. Fully implemented; state machine documented here.
- [MAZ-54](https://linear.app/mazarin/issue/MAZ-54) — closed as duplicate of MAZ-53 (Share + ShareRange cover both whole-Slab and sub-region grants).
- [MAZ-55](https://linear.app/mazarin/issue/MAZ-55) — umbrella ticket.
- `mazarin/fsclient` — reference implementation of the lower-level shared-mapping mechanics (predates the transfer library). See "fs page-loan decision" above.
- `maz/maildb/mail_handler.go` — active producer-consumer reference using `TransferAndUnmap` (closest existing analogue to Mode 1's commit direction).
- `maz/sharetest` / `maz/shareprobe` — boot integration smoke tests for Mode 2.
