# Net userspace shepherd — design

**Status**: planned. The kernel-side wiring that MAZ-26 lands (MSI-X
configuration, the per-device IRQ dispatch branch, a stopgap RX buffer pool
drained on thread 0) is the foundation; this document describes the shepherd
that consumes that foundation, the kernel deltas required when it does, and
the architectural commitments those decisions imply.

## Summary

The net shepherd is the sole consumer of the kernel virtio-net device, in
exact parallel to the way fs is the sole consumer of the kernel virtio-block
device. Protocol logic (Ethernet, ARP, IPv4/IPv6, TCP, UDP, ICMP, QUIC)
lives in net's address space — in full Go context, with a normal goroutine
stack and GC — and reaches clients through two parallel APIs: a native
shared-page interface for in-mazzy users, and a POSIX socket veneer hosted
in the linux shepherd for unmodified userland code. The kernel does only
what block does for fs: at IRQ time it pops the used ring, pushes
io_uring completion events to net's CQ, and wakes net via the same
`priorityWakePending` context-switch path that gives fs its microsecond
IRQ-to-shepherd latency.

## Motivation

Net belongs in userspace for the same reasons fs does. Protocol stacks are
large (TCP alone wants growable stacks, generic data structures, error
formatting), they evolve faster than the kernel does, and they need
graceful failure modes that are awkward to encode in the kernel's
`//go:nosplit` regions. The block precedent established that a userspace
shepherd can drive a virtio device at sub-100µs IRQ-to-completion latency
without any kernel-side protocol code — what makes that work is the
io_uring CQ-push + `priorityWakePending` context-switch primitive, not
anything block-specific. Net inherits the same model.

The alternative — building TCP/IP into the kernel — would expand the
nosplit budget review surface from "drain a used ring + push completions"
(small, fixed, already-proven) to "TCP state machine + reassembly + ARP
table + routing" (large, unbounded stack, allocations on the data path).
The block-shepherd split exists exactly to keep that complexity in Go
code with full runtime support; net inherits the split unchanged.

## Background — how block and fs achieve microsecond IRQ→shepherd latency

The single most important piece of context for the net design is that
block + fs do **not** route IRQ work through Go's goroutine scheduler. The
wake is done end-to-end by kernel-thread-scheduler primitives that the
Go runtime is not aware of. Net must inherit this model, not try to
improve on it.

The full path, with file:line references that are timeless:

1. fs blocks on `sys.IOUringEnter(ringID, 1, minComplete=1, 0)` at
   [`maz/fs/main.go:414`](../maz/fs/main.go). The third argument
   `min_complete = 1` parks fs's kernel thread until at least one CQE
   appears.

2. The kernel marks the thread `ThreadBlockedIOUring` at
   [`kmazarin/kmazarin/iouring.go:120`](../kmazarin/kmazarin/iouring.go),
   records `BlockedTID`, `BlockedPtr`, `MinComplete`, and `BlockDeadline`
   in `IOUringTable[ringID]`, then returns from the SVC. The kernel-thread
   scheduler picks the next runnable thread (typically a different
   shepherd). fs's thread sits idle.

3. The block device raises its MSI-X interrupt. On ARM64 with HVF that
   becomes a GIC SPI delivery; the exception vector lands in
   [`kmazarin/kmazarin/exceptions_arm64.s`](../kmazarin/kmazarin/exceptions_arm64.s)
   at `irq_not_timer`, which swaps `g` to `kmazarinG0Addr` and calls
   `NonTimerIRQTopHalf`. **The active CPU was running whatever shepherd
   was scheduled — thread 0 is not involved at any point.**

4. `NonTimerIRQTopHalf` does the entire block drain inline at
   [`kmazarin/kmazarin/bottom_half.go:388-510`](../kmazarin/kmazarin/bottom_half.go).
   The whole body is `//go:nosplit`. It acks the device ISR, executes a
   `DmaRmb` barrier, then loops `eng.HasUsed()` / `eng.PopUsed()`, reads
   the per-request sidecar status, invalidates the data page's D-cache,
   releases the sidecar slot, decrements the per-clump `InFlight` count
   (with `buddyFreeHook` if munmap raced), and pushes a `CQEntry` onto
   fs's io_uring CQ ring. No allocations, no panic paths, no `klog`.

5. The IRQ body calls `WakeIOUringFromIRQ()` (body at
   [`kmazarin/kmazarin/iouring.go:145-208`](../kmazarin/kmazarin/iouring.go)).
   That function scans all `MaxIORings` slots for blocked waiters whose
   `CQTail - CQHead >= MinComplete`. For each match it marks the thread
   `ThreadReady`, rewinds the saved PC so the SVC re-executes, enqueues
   the thread at scheduler HEAD via `enqueueReadyPrioritySchedLockHeld`,
   and at line 201 sets `atomic.StoreUint32(&priorityWakePending, 1)`.

6. The IRQ-return path in
   [`kmazarin/kmazarin/exceptions_arm64.s:1656`](../kmazarin/kmazarin/exceptions_arm64.s)
   reads `priorityWakePending` after writing GICC_EOIR. When the flag is
   non-zero it runs `CheckThreadPreemption(framePtr)` instead of returning
   to the interrupted thread. `CheckThreadPreemption` finds fs at the head
   of the ready queue, saves the interrupted thread's context into its
   `Thread` struct, restores fs's context, and ERETs into fs's saved
   instruction — the SVC for `IOUringEnter`, which now finds CQEs and
   returns.

What is **not** in this path: thread 0, `KernelIdleLoop`, Go's runtime
scheduler (`schedule`, `goready`, `Gosched`), worker goroutines, or any
channel send. The wake is done entirely by the kernel-thread-scheduler
primitives — `enqueueReadyPrioritySchedLockHeld`, `priorityWakePending`,
`CheckThreadPreemption`. From the Go runtime's point of view nothing
interesting happened; from the OS-thread's point of view it just resumed
inside a syscall.

This is the model the net shepherd must inherit. No Go-scheduler
influence; no goroutine-wake mechanics; no thread 0 dependency. Pure
kernel-IRQ-to-userspace-shepherd, via primitives that already exist and
are battle-tested on every disk I/O.

## The MAZ-26 wiring as our foundation

MAZ-26 lands the kernel-side layer below the net shepherd. Specifically:

- MSI-X configuration via the shared `irq.ConfigureMSIXForDevice` helper.
  Net joins block and input as a consumer; the genuine arch split
  (GICv2m vs LAPIC) stays in
  [`kmazarin/device/virtio/irq/msix_{arm64,amd64}.go`](../kmazarin/device/virtio/irq/).
- A net branch in `NonTimerIRQTopHalf` modeled in shape on the block
  branch but minimal in body — it raises an atomic flag, acks the ISR,
  bumps a counter.
- A stub RX buffer pool (32 armed, 96 free on the descriptor table) and
  a `DrainRxFromBottomHalf` function that runs in full Go context
  (invoked inline from `KernelIdleLoop` on observing the flag).
- A `NetVirtualIRQ = 202` constant in `shared/hid/hid.go`, namespace-hygiene
  reservation for the future shepherd; the MAZ-26 handler does **not**
  call `WakeSlotForIRQ` because there is no consumer yet.
- `SendTx` made WFI-driven (the same hybrid pattern as block's
  `bootYieldForIO`) now that net has an MSI-X line that fires on TX
  completion.

The MAZ-26 design discussion explicitly framed the `RxPending` /
`KernelIdleLoop` drain as a **stopgap** chosen because no userspace net
consumer exists yet. Three options were considered for that gap:

1. Drain inline in the nosplit IRQ top-half (block's pattern). Doesn't
   fit: `DrainRx` ends in `Engine.Submit(&d.rxRepostChain)` to re-post
   the consumed buffer, and `Engine.Submit`'s panic-bounds chain alone
   adds ~224 bytes — over the 792-byte nosplit envelope. Block doesn't
   re-post anything from IRQ, which is why its inline drain fits; RX
   does.
2. Send on a channel to a bottom-half goroutine. Doesn't promote: m0 in
   `KernelIdleLoop` never calls `runtime.Gosched` (doing so corrupts the
   saved-thread-context — see the comment block at
   [`kmazarin/kmazarin/threads.go:1553-1562`](../kmazarin/kmazarin/threads.go)),
   so the receiver goroutine sits `Grunnable` until async preempt happens
   to fire at a safe point. Promptness failure, not a correctness one.
3. Drain inline in `KernelIdleLoop`. Works, fits the budget (KernelIdleLoop
   has a full goroutine stack), and was chosen for MAZ-26 verification.
   It has the structural ceiling that the drain rate is bounded by how
   often thread 0 gets the CPU, which under sustained load with multiple
   ready shepherds can fall behind the 128-deep RX ring.

Option 3 was correct for MAZ-26's "send one ARP, see one reply" verification.
It is the wrong choice for production. The proper fix — option 4, never on
the original list — is to **delete the drain from the kernel entirely** by
landing a userspace net shepherd that uses the same io_uring CQ-push
mechanism block uses. The structural latency concern disappears because
the drain no longer waits for thread 0.

This document specifies option 4.

## Architecture overview

Three layers, parallel in every respect to fs/block:

```
┌─────────────────────────────────────────────────────────────────────┐
│ APPLICATION shepherds                                               │
│   Native: import "mazzy/mazarin/netclient"                          │
│   POSIX:  POSIX socket() etc., handled by linux's syscall delegate  │
│                                                                     │
│ NetClient (mazarin/netclient/) — analogous to mazarin/fsclient/.    │
│ Native API uses SharePagesWithTarget for zero-copy; the POSIX       │
│ veneer adds two byte-buffer copies on send/recv, same shape as      │
│ fsclient.Read.                                                      │
└─────────────────────────────────────────────────────────────────────┘
                          ↕  uring IPC (NetIPC protocol)
┌─────────────────────────────────────────────────────────────────────┐
│ net SHEPHERD  (maz/net/main.go — new)                               │
│   Owns the protocol stack: ARP, IPv4/IPv6, TCP, UDP, ICMP, QUIC.    │
│   gvisor.dev/gvisor/pkg/tcpip for the IP/TCP/UDP/ICMP layers,       │
│   quic-go for QUIC on top of netstack's UDP.                        │
│   Demuxes incoming frames → per-connection delivery (page-loan for  │
│   datagrams, in-net reassembly + per-stream ring for streams).      │
│   Multiplexes outbound packets → device TX via io_uring SQE.        │
└─────────────────────────────────────────────────────────────────────┘
                          ↕  io_uring (one ring net ↔ kernel virtio-net)
┌─────────────────────────────────────────────────────────────────────┐
│ KERNEL  (kmazarin/kmazarin + kmazarin/device/virtio/net)            │
│   IRQ top-half: pop used ring, push CQE, WakeIOUringFromIRQ.        │
│     Mirrors the block branch in bottom_half.go:388-510.             │
│   io_uring SQE handlers (full Go context, NOT nosplit, NOT IRQ):    │
│     IOUringOpNetSubmitTx — build virtio TX descriptor, submit.      │
│     IOUringOpNetRearmDesc — re-arm an RX descriptor with a page.    │
│   RX/TX DMA pool pages mapped permanently into net's address space. │
└─────────────────────────────────────────────────────────────────────┘
```

The fs/block analogue is direct: `mazarin/fsclient/` ↔ `mazarin/netclient/`,
`maz/fs/main.go` ↔ `maz/net/main.go`, kernel block driver ↔ kernel net
driver. The IRQ → CQE → priorityWakePending → context-switch path is the
same machinery on both sides.

## Native IPC model — shared pages, not copy-through-data-area

The single most common misreading of fsclient is that `fsclient.Read(handle,
offset, buf)` represents the canonical fs IPC. It does not. `fsclient.Read`
is the Linux-emulation veneer; **native callers do not use it**. They:

1. Allocate their own pages via `mem.AllocPages` (or `mem.AllocContiguous`
   when DMA properties matter; see
   [`mazarin/mem/alloc.go:34`](../mazarin/mem/alloc.go) and
   [`mazarin/mem/mmap.go:67`](../mazarin/mem/mmap.go)).
2. Call `sys.SharePagesWithTarget(targetSID, pageVA, numPages)` to map
   those pages into the service's address space (kernel-side at
   [`kmazarin/ksyscall/map_shared.go:94`](../kmazarin/ksyscall/map_shared.go)).
3. Send a short uring IPC message describing what to do with the pages.
4. The service operates on the pages **in place**.

This pattern is in production use across the codebase:

- [`maz/fontsvc/main.go:125, 920`](../maz/fontsvc/main.go): clients
  SharePages their request and response pages with fontsvc; fontsvc reads
  the request and writes the rasterized glyph data directly into the
  shared pages.
- [`maz/maildb/mbox_import.go:614`](../maz/maildb/mbox_import.go): maildb
  SharePages the mbox-decoded message pages with fti for indexing.
- [`maz/rachel/wm_dispatch.go:567, 753`](../maz/rachel/wm_dispatch.go):
  rachel SharePages window backing-store pages with toolkit apps that
  draw into them.
- [`maz/fs/main.go:230`](../maz/fs/main.go): fs's own `readFileIntoPages`
  uses ext2's `file.ReadInto(dst)` to DMA from the block device directly
  into the mmap'd pages — no intermediate buffer.

`fsclient.Read` and `fsclient.Write` exist only because the Linux POSIX
`read(fd, buf, len)` syscall takes an arbitrary caller-provided `[]byte`,
and the only way to honour that contract is to land bytes in the caller's
`buf`. That forces a copy in the emulation path. Two copies actually
happen on the POSIX path: fs's internal buffer → linux's intermediate
buffer (in linux's address space), then linux's buffer → the POSIX
caller's `buf` (in the caller's address space). Native callers skip both.

Net inherits this model exactly. `netclient`'s native methods take and
return page handles, not `[]byte`. The POSIX `recv(fd, buf, len)`
emulation in linux is the only place a copy happens, and it happens for
the same reason it happens with files.

## VirtIO descriptor mechanics — what the slot/pool/ring model implies

A common source of confusion is treating "the available ring" as a linked
list of buffers. It is not. The descriptor table is one contiguous
allocation of `queueSize` (128 for the net device) `VirtQDesc` records of
16 bytes each — `{Addr uint64, Len uint32, Flags uint16, Next uint16}` —
and three rings (available, used, plus the descriptor table's own free
list) shuffle indices into that one table.

Three states per descriptor at any moment:

- On `vq.FreeHead → desc.Next → ...` — net owns the slot.
- Index on the Available ring — the device owns it.
- Index on the Used ring — the device is done; net hasn't reclaimed yet.

`NumFree + InAvailable + InUsed = 128`.

Each entry's `Addr` is a physical address pointing to a separate buffer.
**Buffers are scattered** — wherever they were allocated. The descriptor
table is the only contiguous piece. The Linux virtio core works the same
way; the slot table is the queue's structural skeleton, buffers are
allocated independently.

"Pulling a buffer out and replacing it" — the operation needed when
handing an RX page off to a client and re-arming the slot with a fresh
page — is just: `PopUsed` returns descriptor index N; net takes the page
at `desc[N].Addr`; net writes a fresh page's PA into `desc[N].Addr`;
`VirtqueueAddToAvailable(N)`. The descriptor stays in slot N; its
contents change. The "ring" is unmoved; the buffer it points to is
swapped.

`desc.Addr` does **not** have to be page-aligned, and `Len` does not have
to start at the page boundary. `Addr = pagePA + offset, Len = L`
describes a sub-page region. This is what makes the TX-headroom pattern
below work in a single descriptor with no chaining — the wire frame can
sit at an offset inside a 4 KB page.

Net does not need to keep all 128 descriptor slots armed. The MAZ-26
implementation keeps 32 on the available ring and 96 on the free list.
The armed count is a tunable representing burst tolerance — how many
back-to-back frames the device can land before net has to re-arm — not
a constraint. Unused slots cost 16 B each in the descriptor table
(~1.5 KB cold memory for 96 unused slots); the real memory cost is the
buffers, not the slots. TX baseline is zero armed; descriptors are armed
only when net has packets to send.

## TX path — page-handoff with offset, zero copies in the native path

Native TX is a page-handoff that mirrors the Linux skbuff `headroom`/`data`
pointer pattern, applied to a single shared page instead of a kernel-internal
allocation. The wire frame sits at an offset inside the page; the headers
that net adds during transmission live in the reserved headroom in front
of the payload.

Sequence:

1. `netclient` publishes a constant `TxHeadroom` sized to fit the maximum
   header sandwich net might prepend. A safe choice covers
   `VirtIONetHdr (12) + Ethernet (14) + IPv6 (40) + TCP (20 + options)`
   — roughly 86 bytes worst-case typical, padded up to 96 or 128.
2. Client allocates a DMA-suitable page via the kernel — a new
   `mem.PageTxDMA` page type (or an extension to the existing DMA-clump
   mechanism in [`mazarin/mem/blockio.go`](../mazarin/mem/blockio.go)
   that block uses).
3. Client writes payload bytes starting at byte offset `TxHeadroom` within
   the page, for length L. Maximum payload per page is
   `PAGE_SIZE - TxHeadroom`.
4. Client transfers ownership: `sys.SharePagesWithTarget(netSID, pageVA, 1)`
   unmaps the page from the client and maps it into net. **The client
   cannot touch the page again until release.** Clean ownership semantics
   while DMA is in flight.
5. Client sends a short IPC: `NetIPCSend{connID, netVA, payloadLen}`.
6. Net's IPC handler — running in full Go context, not nosplit, not in
   an IRQ — builds the wire headers in the reserved headroom starting at
   `netVA + TxHeadroom - actualHeaderLen`. It computes
   `Addr = pagePA + (TxHeadroom - actualHeaderLen)` and
   `Len = actualHeaderLen + payloadLen`, then submits one descriptor.
   Flags = 0 — **not** `VIRTQ_DESC_F_WRITE`, which means "device writes
   to this buffer" and is set on RX, cleared on TX. No chaining is
   needed; one descriptor per packet.
7. Device DMAs the bytes out. TX-completion IRQ fires. The kernel
   top-half pops the TxEng used ring and pushes a CQE
   `{UserData: txTag, Res: 0}` onto net's io_uring CQ. Calls
   `WakeIOUringFromIRQ`, which sets `priorityWakePending`. Net's
   io_uring waiter wakes on the next IRQ-return.
8. Net reads the CQE, sends the reply IPC to the client. Either
   `sys.UnsharePages` + buddy-free reclaims the page, or net holds the
   page in a per-client TX free-pool for reuse on the next TX from the
   same client — an optimization that avoids the SharePages/UnsharePages
   round-trip in the steady state.

Zero copies of payload bytes anywhere in this path. The page the device
read from is the page the client filled.

### Why not in-net buffer pools with copy-on-Send

The alternative is a fixed TX buffer pool owned by net, with clients
sending payloads through an IPC data area for net to memcpy into a pool
buffer. This is the shape `fsclient.Write` uses. The reasons not to
inherit that shape for native TX:

- TCP/QUIC payloads can be the full path MTU (1500 typical, larger with
  jumbo or with virtio's GSO support). The per-packet memcpy adds
  ~100ns × 4 KB / GB-bandwidth ≈ small but non-zero, and it scales with
  the throughput goal.
- Page-handoff is the same primitive fontsvc and rachel already use.
  Adding it to net costs nothing additional in kernel surface; it just
  reuses `sys.SharePagesWithTarget`.
- The Linux POSIX `send(fd, buf, len)` veneer in linux still pays one
  copy (buf → page allocated by linux on the caller's behalf), exactly
  matching the file-write veneer's cost. Native callers pay zero.

## RX path — permanently-owned pool, descriptor re-arm, two delivery models

Net's RX pool is established once at startup. The dynamics differ from
TX in one key respect: every RX buffer must be live at all times so the
device has somewhere to land arriving frames.

Bootstrap:

- Net calls `sys.AllocRxPool(N, mem.PageRxDMA)` once at startup. Suggested
  N = 128: 32 armed on the available ring + 32 budget for client loans
  + 64 free reserve. Total 512 KB.
- The kernel maps all N pages into net's address space **permanently**.
  Net keeps ownership through its entire lifetime; the pages are never
  unmapped from net even when individual buffers are loaned to clients.
- Net populates descriptor entries: `desc[0].Addr = poolPA[0]`,
  `desc[1].Addr = poolPA[1]`, etc. It publishes 32 of them on the
  available ring; the remaining 96 sit on net's internal free reserve
  list.

When a frame arrives at descriptor 5:

- Kernel IRQ top-half pops the RxEng used ring, pushes a CQE with
  `UserData = encode(rxType, descIdx=5)` and `Res = frameLen`. Calls
  `WakeIOUringFromIRQ`.
- Net's RX-consumer goroutine wakes (priorityWakePending → context-switch
  directly into net's `IOUringEnter` SVC return), reads the CQE,
  accesses the wire frame at `rxPoolVA[5]` starting at byte 0.
- Net's decoder chain (Ethernet → IP → UDP/TCP → app) operates by
  **pointer-passing inside net's address space**. Each layer takes a
  slice into the same page. Initial implementation uses statically-linked
  Go packages for the decoders; .maz plugins for runtime decoder
  swapping are a possible future addition but not needed at the start.

The crucial decision is what happens to the page after decoding. There
are two delivery models, picked per-protocol:

### Datagram delivery — page-loan, zero copy

For UDP, QUIC datagrams, raw Ethernet, and any other protocol where the
packet **is** the deliverable, the page can be loaned to the client
unchanged:

1. Net decides "client X gets this packet."
2. Net calls `sys.SharePagesWithTarget(clientX_SID, rxPoolVA[5], 1)`.
   The page is now mapped in both net and client X. (Net retains its
   mapping; the page stays in net's pool.)
3. Net sends IPC:
   `NetIPCRx{connID, bufVA: clientRemoteVA, payloadOff, payloadLen, releaseToken: 5}`.
4. Client reads bytes at `clientRemoteVA + payloadOff`. Reads from the
   same physical page the device DMA'd into.
5. When done, client sends `NetIPCRelease{token: 5}`.
6. Net `sys.UnsharePages` from client, re-arms descriptor 5 with either
   this same page or a fresh page pulled from the free reserve, and
   adds 5 back to the available ring.

The "buffer pull-and-relink" intuition is exactly this: descriptor 5 is
"off the available ring" for the duration of client X's read; net
refills the device-visible inventory by arming a different descriptor
with a fresh page from the free reserve. The 64-page free reserve
absorbs the latency of clients holding pages.

Per-client outstanding-page tracking is required. If a client's loaned-page
count exceeds a watermark (suggest 16 pages), net refuses to loan more
and either drops the packet or falls back to copying into a per-client
ring. This is the same shape as the Linux kernel's per-socket buffer
limits — clients that don't drain their RX promptly are quarantined
rather than allowed to starve other clients.

### Stream delivery — one in-net copy, per-stream ring or POSIX recv

Stream protocols (TCP, QUIC at the stream level) do not fit page-handoff
because the bytes of one logical stream are spread across multiple wire
frames in arbitrary order with retransmits, reordering, and per-segment
header overhead. Reassembly must happen somewhere; the place it happens
in net is the only place the bytes are in stream order. The sequence is:

1. Wire RX page arrives at descriptor 5.
2. TCP engine (or QUIC engine's STREAM frame processing) extracts the
   segment payload and **copies it into the connection's reassembly
   buffer** (a windowed receive buffer in net's Go heap).
3. The wire page is released back to net's free reserve immediately;
   descriptor 5 is re-armed within microseconds.

This one copy is in net's address space — single-shepherd memcpy at
~150 ns/KB on modern hardware, negligible against TCP, TLS, or QUIC
crypto cost.

Delivery from reassembly buffer to client has two flavours, picked
per-connection at open time:

- **Per-stream shared ring (native, zero IPC copy).** Client allocates
  one or more ring pages at stream open, `SharePagesWithTarget(netSID,
  ringVA, N)`. Net writes reassembled bytes into the ring; client reads
  via head/tail pointers. SPSC discipline. No IPC copy in the steady
  state; an IPC message is needed only to wake a blocked client (and
  even then, only on empty→non-empty transitions — see the epoll
  section).

- **POSIX recv copy (linux veneer).** Client calls
  `nc.ReadStream(streamID, buf)`; net copies from reassembly buffer
  into `buf` via the IPC data area. One IPC copy, same shape as
  `fsclient.Read`. Simpler API at the cost of throughput.

Native callers use the shared ring. The Linux POSIX `recv(fd, buf, len)`
emulation in linux uses the copy path, because POSIX `recv` takes a
caller-provided `buf` and the only way to honour it is to land bytes
there.

## Protocol stack — gVisor's netstack for TCP/IP, quic-go for QUIC

Building TCP from scratch is not worth the time. The mazzy project should
adopt `gvisor.dev/gvisor/pkg/tcpip` — gVisor's userspace TCP/IP stack,
written in pure Go, production-tested in gVisor itself and in Tailscale's
`wireguard-go`. Integration is via the `LinkEndpoint` interface: net
hands netstack raw Ethernet frames and receives a socket-style API for
connections. The "frame in an RX page" model maps cleanly onto netstack's
buffer abstractions.

What netstack covers:

- Ethernet, ARP, IPv4, IPv6
- TCP (full state machine, congestion control, SACK)
- UDP
- ICMP / ICMPv6
- Routing table
- Network-level demultiplexing

What sits on top, separately:

- **QUIC**: `github.com/quic-go/quic-go`, the mature pure-Go QUIC
  implementation. It runs on top of a UDP socket, which netstack
  provides. Single coherent network stack covering both transports.

What net writes itself:

- The `LinkEndpoint` adapter that bridges netstack to the io_uring
  RX/TX path (this is small — netstack's LinkEndpoint is a narrow
  interface).
- The NetIPC dispatcher and protocol decoders for the native IPC layer.
- The per-connection delivery logic (datagram page-loan, stream
  reassembly + per-stream ring).
- DNS resolution (likely; see open questions).

### Why this and not a from-scratch stack

A scratch TCP implementation is, conservatively, multiple person-years of
work to reach gVisor's level of bug-fixing across pathological
sequences, congestion-control corner cases, and RFC compliance. Mazzy
gains nothing architectural from owning that code. Netstack is permissively
licensed, actively maintained, and its existing users (gVisor, Tailscale)
exercise it at scale.

### Caveat — net is likely an `.elf` host, not a `.maz` plugin

Netstack pulls in roughly 50,000 lines of Go. .maz plugins share the host
runtime, which constrains import surface and complicates dependency
management for a transitively-large module. The pragmatic choice is to
build net as an `.elf`-style host with its own runtime (the same shape as
`fs.elf` and the recommended pattern for shepherds with large dependency
trees per
[`feedback_overlay_vs_runtime`](../docs/feedback_overlay_vs_runtime.md)).
Worth re-checking when the ticket lands but not a blocker on the design.

## Linux POSIX socket emulation — thin veneer over NetClient

The linux shepherd handles POSIX syscall delegation. Socket emulation
maps onto NetClient calls in a one-to-one shape that mirrors how file
syscalls map onto FSClient calls. Linux owns the fd-table side; net owns
the connection-state side; the IPC between them is short messages
referencing `connID` and `listenID` handles.

| POSIX syscall                       | Linux shepherd does                                             | NetClient call                                       |
|-------------------------------------|-----------------------------------------------------------------|------------------------------------------------------|
| `socket(AF_INET, SOCK_STREAM, 0)`   | Allocate fd slot; mark as unbound TCP                           | (no IPC yet)                                         |
| `bind(fd, sa, salen)`               | Send IPC                                                        | `nc.BindTCP(localAddr) → connID`                     |
| `listen(fd, backlog)`               | Send IPC                                                        | `nc.Listen(connID, backlog) → listenID`              |
| `accept(fd, sa, salen)`             | Block on readiness IPC; on result, allocate new fd              | `nc.Accept(listenID) → (newConnID, remoteAddr)`      |
| `connect(fd, sa, salen)`            | Send IPC; block until ESTABLISHED                               | `nc.Connect(connID, remoteAddr)`                     |
| `send(fd, buf, len, flags)`         | Copy `buf` → page allocated by linux; SharePages → net          | `nc.Send(connID, txPage)`                            |
| `recv(fd, buf, len, flags)`         | Block for data; copy net's stream buffer → POSIX `buf`          | `nc.ReadStream(connID, buf)`                         |
| `close(fd)`                         | Send close IPC; free fd slot                                    | `nc.Close(connID)`                                   |
| `shutdown(fd, how)`                 | Send half-close IPC                                             | `nc.Shutdown(connID, dir)`                           |
| `setsockopt(fd, ...)`               | Translate option; send                                          | `nc.SetOption(connID, opt, val)`                     |
| `epoll_wait` (readiness for socket) | Wait on linux's epoll readiness ring; see the next section      | (uses the shared readiness ring, not direct IPC)     |

Two byte-buffer copies on the `send`/`recv` paths — the expected
emulation tax, the same as `fsclient.Read` for files. Native NetClient
callers skip both. AF_UNIX is a separate concern and not in scope for
the net shepherd (see non-goals); AF_PACKET raw sockets are out of scope
indefinitely (`/dev/bpf`-style access is not on the roadmap).

## The epoll readiness channel — the one piece worth deliberate up-front design

POSIX `epoll`/`poll`/`select` apps register fds and expect notification
on readability/writability transitions. Cross-shepherd readiness signalling
is structurally different from per-fd request/response IPC and warrants
its own dedicated channel rather than reuse of the per-connection IPC.

**Design:** a single bidirectional io_uring ring shared between net and
linux carrying `{connID, eventMask}` transition events. Net pushes
readiness transitions on its CQ side; linux's epoll-dispatch goroutine
reads them and translates into wakeups for any thread blocked in
`epoll_wait` on the corresponding fd.

Edge-triggered semantics at the ring level (net naturally emits
transitions, not levels). Linux's epoll already does level-vs-edge
translation for its own fd types (timerfd, signalfd, eventfd, pipes,
files); socket fds plug into that existing translation. Net just feeds
the underlying transitions.

Coalescing is essential. On a busy connection the per-packet event rate
would overwhelm the ring. Net buffers reassembled stream bytes anyway,
and the meaningful transitions are:

- **Readable**: empty → non-empty in the receive buffer.
- **Writable**: full → not-full in the send buffer (or — for unbuffered
  TX — never; "writable" is the default state and `epoll` reports it
  immediately on registration).
- **Hangup**: peer closed or RST received.
- **Error**: protocol error not deliverable through other means.

Only "empty → non-empty" and similar level-crossings are pushed onto the
ring; the per-packet RX events that don't change the buffer's "has data
or not" state are not.

The standard "atomicity around register-and-check" problem applies — if
linux registers an fd in the epoll set and the readiness transition
already happened before registration completed, the wake event was lost.
Solved the standard Linux way: linux checks the readiness state in the
fd's metadata after registering, not via a separate IPC round-trip. Net
guarantees that the state visible to linux at IPC-handler time matches
the last-known readiness, and the ring transition is sent under the same
lock so observed state ≥ transitions seen on the ring.

## Bootstrap — net is not in the critical-infrastructure list (today)

fs is `//go:embed`'d into the kernel binary because fs is the only thing
that can read the disk — chicken and egg. The relevant code is
[`kmazarin/kmazarin/embedded_fs.go:5`](../kmazarin/kmazarin/embedded_fs.go)
(the `//go:embed fs.elf` declaration) and
[`kmazarin/kmazarin/main.go:648`](../kmazarin/kmazarin/main.go)
(`launchEmbeddedFS`).

Net is a runtime device, not a boot device. There is no equivalent
chicken-and-egg constraint:

- No network in `diplomat/`'s UEFI boot path.
- No network entry in the kernel's auxv
  ([`shared/constants/auxv.go`](../shared/constants/auxv.go)).
- No DHCP / TFTP / iPXE / network-boot capability.

fs's `bootSequence` at [`maz/fs/main.go:328-356`](../maz/fs/main.go)
launches rachel + linux only (rachel first; linux depends on rachel for
fonts/WM and on fs for IPC). `startup.toml` is read after both are ready.

Today, no startup shepherd needs network. linux's syscall delegation is
POSIX-file-centric; rachel uses virtio-input + virtio-gpu; mail / maildb
/ fti are local-file-only. Net therefore slots into `startup.toml` as a
regular shepherd entry, launched after the core three.

Net moves into `bootSequence` only when both of these happen:

1. linux's syscall delegation gains socket-family syscalls
   (`socket` / `bind` / `connect` / `recvfrom` / `sendto`).
2. Some startup shepherd actually uses them at boot.

When that day arrives, net slots between rachel and linux in
`bootSequence`, and linux's startup adds
`sys.WaitForShepherdReady("net", 30)` before completing init. Until then,
net's startup position has no special status.

## Buffer sizing — concrete recommendations

These are the starting numbers. Tune as load characterizations land.

- **RX pool**: 128 pages = 512 KB total. Split: 32 armed on the available
  ring + 32 budget for in-flight client loans + 64 free reserve. The
  free reserve sized to absorb the latency of clients holding pages
  during processing.
- **TX**: on-demand page allocation per packet. A small per-client free
  pool of pre-allocated pages, suggested 4-8 pages, as an optimization
  to avoid SharePages/UnsharePages on every send. Bypassable for
  one-shot sends.
- **Per-connection TCP reassembly buffer**: 64 KB default, configurable
  via `SO_RCVBUF`. Sits in net's Go heap.
- **Per-connection TCP send buffer**: 64 KB default, configurable via
  `SO_SNDBUF`. Sits in net's Go heap (writes copy into here, then are
  segmented into TX pages).
- **`TxHeadroom` constant**: 96 bytes — comfortably covers
  `VirtIONetHdr (12) + Ethernet (14) + IPv6 (40) + TCP (20+options)`,
  with margin for VLAN tags or future encapsulation. Worth re-verifying
  against netstack's actual header construction before locking in.
- **Per-stream shared ring (when used)**: 2 pages = 8 KB. Tunable at
  stream open via NetClient option.
- **Epoll readiness ring**: standard `iouring.IORing` page (4 KB; see
  [`shared/iouring/iouring.go`](../shared/iouring/iouring.go)).
  SQ depth 32, CQ depth 64 is the existing layout and works fine for
  edge-triggered readiness transitions.

## Kernel deltas — what changes in `kmazarin/` when net lands

The kernel shrinks and net grows. Net runs in normal Go context (full
stacks, GC, channels, mutexes), which is the protocol code's natural
habitat.

### Delete from the kernel

These become dead code once the net shepherd lands and consumes its
io_uring ring directly:

- `net.RxPending` flag.
- `DrainRxFromBottomHalf` function and the entire in-kernel drain.
- The `net.RxPending` swap branch in `KernelIdleLoop` at
  [`kmazarin/kmazarin/threads.go:1585`](../kmazarin/kmazarin/threads.go).
- The current `SendTx` busy-loop in
  [`kmazarin/device/virtio/net/tx.go`](../kmazarin/device/virtio/net/tx.go),
  along with its `txInUse` atomic CAS. TX moves to the io_uring SQE
  handler in full Go context; serialization can use a regular
  `sync.Mutex` there, not the atomic CAS the MAZ-26 nosplit context
  required.
- The current MAZ-26 RX buffer pool (`netRxBufCount = 32`, packed 2 per
  page in `rx.go`). Replaced by net-allocated DMA pages — **one per
  buffer**, no 2-per-page packing, because each RX page is potentially
  handed to a client and must not share its bytes with another
  in-flight buffer.

### Add to the kernel

- `mem.PageRxDMA` and `mem.PageTxDMA` page-type constants (or an
  extension of the existing DMA-clump mechanism in
  [`mazarin/mem/blockio.go`](../mazarin/mem/blockio.go) that block uses).
  These pages must be DMA-suitable in the same sense as block's DMA
  clump pages — physically backed, kernel-tracked, with the right cache
  attributes.
- Syscalls:
  - `AllocRxPool(N) → []pageVA` — bulk-allocate the RX pool and map it
    permanently into net.
  - `AllocTxBuffer(size) → pageVA` — single-page TX allocation. May be
    folded into a flagged variant of the existing `AllocPages` syscall.
- io_uring SQE opcodes for net, added to
  [`shared/iouring/iouring.go`](../shared/iouring/iouring.go) alongside
  the existing `IOUringOpRead`/`IOUringOpWrite`:
  - `IOUringOpNetSubmitTx{pagePA, offset, len, txTag}` — kernel handler
    builds the virtio TX descriptor chain (which today is the work of
    `SendTx`), submits to TxEng, notifies the device. Records a
    pending-TX entry indexed by `txTag`.
  - `IOUringOpNetRearmDesc{descIdx, pagePA}` — kernel handler rewrites
    `desc[descIdx].Addr = pagePA`, adds the descriptor to the available
    ring, notifies the device.

  **Both SQE handlers run in full Go context** (the existing io_uring
  SQE dispatch path, not nosplit, not in IRQ). Handler bodies can
  allocate, take locks, panic on bad inputs, etc.

- Net branch in `NonTimerIRQTopHalf` at
  [`kmazarin/kmazarin/bottom_half.go`](../kmazarin/kmazarin/bottom_half.go),
  modeled exactly on the block branch (388-510). The shape:

  ```go
  if irqNum == netIRQNum && netIRQNum != 0 {
      atomic.AddUint32(&dbgNetIRQCount, 1)
      if netISRBase != 0 {
          _ = asm.MmioRead8(netISRBase)  // ack at device
      }
      asm.DmaRmb()

      // Drain RxEng used ring → CQEs.
      for rxEng.HasUsed() {
          info := rxEng.PopUsed()
          if info.Tag == virtio.InvalidIOTag { break }
          pushCQEForNet(encodeRxUserData(info.Tag), int32(info.UsedLen))
      }
      // Drain TxEng used ring → CQEs.
      for txEng.HasUsed() {
          info := txEng.PopUsed()
          if info.Tag == virtio.InvalidIOTag { break }
          pushCQEForNet(encodeTxUserData(info.Tag), 0)
      }

      WakeIOUringFromIRQ()
      return
  }
  ```

  No drain logic, no re-post, no `Engine.Submit`, no `panicBounds` chain
  pulled in by `Submit`. The net shepherd does all re-posting via
  `IOUringOpNetRearmDesc` SQEs. The IRQ body stays small and fits the
  nosplit budget — the same shape that lets block fit (block also only
  consumes; userspace re-fills via SQEs).

- `WakeSlotForIRQ(hid.NetVirtualIRQ)` next to the `WakeIOUringFromIRQ`
  call, parallel to the block branch's
  `WakeSlotForIRQ(hid.BlockVirtualIRQ)`. This catches any future soft-IRQ
  consumers; today net's io_uring waiter is the only waker target but
  the call is cheap and keeps the parallel with block exact.

## Non-goals — explicitly out of scope for the initial cut

These are deferred to keep the scope tractable and ship the architectural
shape first. None are precluded by the design; each is a follow-up if
load justifies it.

- **Jumbo frames** (MTU > 1500). Adds RX page-size sensitivity and
  TCP MSS negotiation. Defer until a workload demonstrates the win.
- **`MRG_RXBUF`** (multi-buffer RX, where a single frame spans multiple
  descriptors). VirtIO supports it; netstack accepts the data shape;
  the buffer-management code is harder. Defer.
- **TSO/LRO** (large-send/large-receive offload). Wins throughput at
  the cost of code complexity in the segmentation/reassembly paths.
  Defer.
- **IPv6 priority**. The stack supports both protocols; production
  dual-stack typically prefers IPv6, but pinning the preference rules
  is a follow-up tuning exercise.
- **Multi-queue virtio-net**. The device supports it; a single RX/TX
  queue pair is enough for the bring-up cut. Multi-queue is a
  scaling/RSS concern that surfaces when one CPU is the IRQ bottleneck.
- **`SO_REUSEPORT`**. Useful for multi-process servers; the linux
  veneer would need to track the option semantics. Defer.
- **AF_UNIX**. Unix-domain sockets are a separate IPC mechanism that
  does not touch virtio-net at all. They belong in linux (or in their
  own dedicated shepherd) and are not a net concern.
- **AF_PACKET raw sockets**. Direct frame-level access bypasses the
  protocol stack and would expose net's internal page-handoff to
  arbitrary clients. Not on the roadmap.

## Open questions for implementation

- **`TxHeadroom` exact value.** 96 covers the common case; netstack's
  actual maximum header sequence (including TCP option max, IPv6
  extension headers if implemented, virtio-net header variants) should
  be measured before locking in.
- **Net binary form: `.elf` host or `.maz` plugin.** The dependency
  weight of netstack strongly suggests `.elf`; the working assumption
  in this document is `.elf`. Re-verify when the implementation ticket
  lands and the real dependency closure is known.
- **Epoll level-vs-edge semantic details.** Linux POSIX `EPOLLET` vs
  level-triggered: net emits edge transitions, linux translates. The
  exact translation rules — particularly around `EPOLLONESHOT`,
  `EPOLLRDHUP`, the half-close cases — need a small spec.
- **Per-client outstanding-page watermark.** 16 pages is the suggested
  starting value; the right number depends on typical RX burst sizes
  and client processing latency. Worth instrumenting from day one
  (`dbgNetClientPagesOutstandingMax` style counter per client).
- **DNS resolver location.** Almost certainly belongs in net (DNS is a
  network protocol). Could also live in a separate small shepherd that
  consumes NetClient. The deciding factor is whether DNS needs to share
  state with the rest of netstack (resolver caches, search lists,
  protocol-version selection) — probably yes, which argues for in-net.
  Worth confirming.
- **Cross-CPU implications.** The priority-wake path is single-CPU-centric
  (per-CPU ready queues, `perCPU.NeedsThreadPreempt`). On SMP, net IRQ
  may hit a CPU where net's thread isn't pinned. The fix is the same
  IPI-style signal block will eventually need; flag for the SMP rework.
- **Backpressure for the epoll readiness ring.** If linux falls behind
  on draining the ring (which holds level-crossing transitions), net
  must decide whether to overwrite, drop, or block. Standard io_uring
  semantics drop on overflow with a marker; net should follow.

## References

Architecture-level — these don't change with releases:

- The io_uring CQ-push + priorityWakePending wake pattern:
  [`kmazarin/kmazarin/iouring.go:145-208`](../kmazarin/kmazarin/iouring.go)
  (`WakeIOUringFromIRQ`),
  [`kmazarin/kmazarin/exceptions_arm64.s:1656`](../kmazarin/kmazarin/exceptions_arm64.s)
  (IRQ-return preemption check).
- The block IRQ branch — copy this shape for net:
  [`kmazarin/kmazarin/bottom_half.go:388-510`](../kmazarin/kmazarin/bottom_half.go).
- fs userspace pattern — model `maz/net/main.go` on this:
  [`maz/fs/main.go`](../maz/fs/main.go).
- FSClient interface for the API shape:
  [`mazarin/fsclient/client.go`](../mazarin/fsclient/client.go).
- io_uring ring layout, SQE/CQE shapes:
  [`shared/iouring/iouring.go`](../shared/iouring/iouring.go).
- SharePagesWithTarget syscall implementation:
  [`kmazarin/ksyscall/map_shared.go:94`](../kmazarin/ksyscall/map_shared.go).
- VirtualIRQ namespace (200 = timer, 201 = block, 202 = net):
  [`shared/hid/hid.go`](../shared/hid/hid.go).
- Boot order:
  [`maz/fs/main.go:328-356`](../maz/fs/main.go),
  [`kmazarin/kmazarin/embedded_fs.go`](../kmazarin/kmazarin/embedded_fs.go).
- Native shared-page IPC examples:
  [`maz/fontsvc/main.go:125, 920`](../maz/fontsvc/main.go),
  [`maz/maildb/mbox_import.go:614`](../maz/maildb/mbox_import.go),
  [`maz/rachel/wm_dispatch.go:567, 753`](../maz/rachel/wm_dispatch.go).
- Page allocation API:
  [`mazarin/mem/alloc.go`](../mazarin/mem/alloc.go),
  [`mazarin/mem/mmap.go`](../mazarin/mem/mmap.go),
  [`mazarin/mem/blockio.go`](../mazarin/mem/blockio.go).

External:

- gVisor netstack: `gvisor.dev/gvisor/pkg/tcpip` —
  https://pkg.go.dev/gvisor.dev/gvisor/pkg/tcpip
- quic-go: `github.com/quic-go/quic-go` — https://github.com/quic-go/quic-go
- Linux virtio-pci transport layer (the precedent for putting MSI-X /
  virtqueue interrupt setup in transport rather than per-driver):
  `drivers/virtio/virtio_pci_*.c` in the Linux kernel tree.
