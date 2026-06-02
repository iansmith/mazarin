> **Provenance:** Promoted from the May-25 2026 Sophie protocol-stack design session (`draft-net-vectored-io.md`).
> Originally drafted in gitignored `.claude/`; recovered into `design/` on 2026-06-01.

# net shepherd: vectored-IO send API (scatter-gather (ptr, len) array)

## Pattern boundary

The `mazarin.<Foo>Client.New() → <Foo>ProtocolClient` pattern (introduced in [MAZ-48](https://linear.app/mazarin/issue/MAZ-48) for `mazarin/claudeclient` and [MAZ-49](https://linear.app/mazarin/issue/MAZ-49) for `mazarin/httpclient`) applies at the **protocol** layer — HTTP today, TCP and (probably) UDP when filed. Those layers translate a caller-friendly request/response surface into raw sends/receives on the substrate.

Net is the **substrate**, not a protocol. TCP and UDP (and HTTP, via TCP) will speak `mazarin/netclient` directly. Wrapping net behind a `NetProtocolClient` interface would be ceremony without payoff — there's no semantic translation, just renaming.

So this ticket keeps `mazarin/netclient` as the surface. No `mazarin.NetClient.New()`. The vectored-IO API is added as additional methods on the existing client.

## Motivation

Protocol shepherds higher up the stack — protocol-http per its ticket, and any future protocol that builds outbound messages from multiple pieces — assemble payloads from heterogeneous fragments:

- HTTP request line (small string)
- Several header lines (small strings)
- A body that may itself span many pages (per the mazarin transfer library)

Today's `mazarin/netclient.StreamSend` takes a single `[]byte`. Forcing higher layers to memcpy everything into one contiguous buffer:

- doubles peak memory at send time (the original fragments + the concatenated send buffer)
- defeats the no-copy property of the transfer-library page handles
- adds an extra GC walk for the temporary buffer
- adds latency proportional to total body size for what should be a constant-time descriptor build

This ticket adds a vectored-send API where the caller passes an array of `(ptr, len)` and the net shepherd stitches them into a single outgoing logical send — ideally a single virtio-net descriptor chain.

## Surface (sketch)

```go
// mazarin/netclient
type IOVec struct {
    Ptr uintptr
    Len int
}

func (nc *Client) StreamSendV(connID uint32, vec []IOVec) error
```

Net shepherd internals:
- New uring sub-opcode (e.g. `SUB_OP_STREAM_SEND_V`) carrying the iovec list inline in the SQE payload (or, if too large, by reference to a caller-mapped page).
- Outbound path consumes the iovec list and produces a single virtio-net descriptor chain, avoiding per-piece memcpy.

## Definition of Done

1. `mazarin/netclient.StreamSendV` exists with the surface above (or its pinned variant).
2. Net shepherd handles the vectored opcode and produces a single virtio descriptor chain (or a tight batch of chains) per call.
3. A bench/measurement shows fewer page copies than `StreamSend` on an HTTP-shaped workload (request-line + 3 headers + ~4 KiB body).
4. At least one production caller — protocol-http per its ticket, or a stand-in test — exercises the API end-to-end.
5. Existing `StreamSend` continues to work unchanged (it's not deprecated; not every site needs scatter-gather).

## Out of scope

- Receive-side scatter (`StreamRecvV`). Design is similar but a separate ticket if/when needed.
- Cross-page DMA fragmentation for arbitrarily-aligned pointers. The v1 may require all pointers to be page-aligned or to live in caller-mapped pages already.
- Zero-copy across address spaces. The net shepherd may still copy from the caller's pages into virtio descriptors if alignment doesn't permit DMA-in-place — the win is the *single* copy instead of N+1.

## Dependencies

Blocks:
- protocol-http ticket (primary consumer).

Related:
- [MAZ-22](https://linear.app/mazarin/issue/MAZ-22) — virtio-net TX path (done).
- [MAZ-32](https://linear.app/mazarin/issue/MAZ-32) — gvisor zero-copy RX (mirror problem on the receive side).

## Open questions for `/ticket-plan`

1. Alignment rules — page-aligned only (simplest, no extra copy), or any address (more flexible, may force a copy). Lean page-aligned for v1.
2. Max vector length — virtio-net's descriptor chain has a cap; document and enforce.
3. New uring opcode (cleanest, but a kernel change) vs encoding the iovec list inside the existing `StreamSend` payload header (no kernel change, but uglier). Lean new opcode.
4. Failure semantics — if a partial descriptor chain commits but the rest fails, what does the caller see? Lean all-or-nothing.

## LOC estimate

- `mazarin/netclient` additions: ~50
- Net shepherd opcode handler + virtio descriptor chain construction: ~120
- Bench harness + tests: ~80
