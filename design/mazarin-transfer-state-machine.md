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
- **Followup:** future work could replace the timeout with a kernel-pushed "shepherd died" notification routed through the dispatcher. Tracked in the deferred list of MAZ-50's plan.

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

## How Modes 2 and 3 extend this

### Mode 2 — server-initiated handoff ([MAZ-53](https://linear.app/mazarin/issue/MAZ-53))

Adds two states / transitions to the foundation:

- `slabCommitted` (or `slabAllocated`, for server-originated payloads) → `slabHandedOff` via `s.HandOff(clientSID)`. Kernel transfers the pages out of the server; the server can no longer touch them.
- `slabHandedOff` is observed by the recipient as a freshly-allocated Slab via `ReceiveHandoff(serverSID)` — they enter their own state machine from a `slabAllocated`-equivalent state.

The pre-posted-buffer pattern (protocol-claude pre-allocates response Slabs and grants them to protocol-http via Mode 3) means Mode 2 ends up being used only for the final hop (protocol-claude → sophie), where ownership genuinely changes hands. Chained handoffs (A → B → C) remain supported for cases like the fs page-loan path.

### Mode 3 — sub-region write mapping ([MAZ-54](https://linear.app/mazarin/issue/MAZ-54))

Layers on top of any state. Adds a parallel sub-state for the granted region:

- `s.GrantWrite(clientSID, offset, length)` produces a `Handle` for the client (over a *byte* range that overlaps one or more pages) without changing the Slab's main state. The server retains ownership; the client has R/W to the granted pages.
- Client's `h.Release()` and the matching `s.RevokeWrite(h)` tear down the sub-region mapping; the Slab continues in whatever main state it was in.

Trust model in v1: page-granularity protection means the client has R/W to whole pages that overlap the granted byte range, including bytes outside the logical grant. v1 accepts this for in-tree clients; VM-enforced isolation is deferred.

## Sanity checks for implementers

- A Slab can only be in one `slabState` at a time. Concurrent IPC + Wait must be serialized via the dispatcher goroutine ordering — never read/write state from two goroutines without a synchronizing channel.
- Never call `Slab.Bytes()` from a path that's also being driven by a uring dispatcher handler unless the state is verified `slabAllocated` or `slabCommitted`. Returning nil is a safety mechanism, not a correctness shield.
- `Handle.Commit()` is **release-first**: `TransferAndUnmap` returns only when the kernel has atomically moved the pages back to the server. The IPC notification that wakes `Wait()` is sent *after* that SVC returns. This ordering is what guarantees the server can't observe torn writes through a stale TLB.

## Cross-references

- [MAZ-50](https://linear.app/mazarin/issue/MAZ-50) — foundation + Mode 1 (this doc).
- [MAZ-53](https://linear.app/mazarin/issue/MAZ-53) — Mode 2: server-initiated handoff. Extends this state machine.
- [MAZ-54](https://linear.app/mazarin/issue/MAZ-54) — Mode 3: sub-region write mapping. Extends this state machine.
- [MAZ-55](https://linear.app/mazarin/issue/MAZ-55) — umbrella ticket.
- `mazarin/fsclient` — partial reference implementation for the cross-VA mapping mechanics; uses `SharePagesWithTarget` (share, not transfer). Reference for IPC dispatcher patterns, NOT for the page-ownership semantics described here.
- `maz/maildb/mail_handler.go` — active producer-consumer reference using `TransferAndUnmap` (closest existing analogue to Mode 1's commit direction).
