# Sophie protocol stack — thin-client reorg over a layered protocol/substrate

> **Provenance:** Recovered/consolidated on 2026-06-01 from the May-25 2026 Sophie
> design session (`85d60927`) and the May-27 MAZ-49 session (`fbee55cf`). The
> per-component detail docs were drafted in gitignored `.claude/` and have been
> promoted alongside this overview:
> [protocol-claude-shepherd](protocol-claude-shepherd.md),
> [protocol-http-shepherd](protocol-http-shepherd.md),
> [net-vectored-io](net-vectored-io.md),
> [mazarin-jsonstr](mazarin-jsonstr.md),
> [mazarin-transfer-mode3](mazarin-transfer-mode3.md). Transfer Modes 1–2 live in
> [mazarin-transfer-state-machine](../mazarin-transfer-state-machine.md).

## Goal

[MAZ-43](https://linear.app/mazarin/issue/MAZ-43) shipped **sophie** as a *fat
client* — it owned the Anthropic Messages JSON schema, auth, HTTP, and TLS. This
effort re-layers the stack so sophie becomes a **thin requester** and each concern
lives in its own shepherd, reusable by any future Claude/HTTP consumer.

The driving non-functional requirement is **zero-copy on both the prompt and the
response path** — modern Claude prompts run to ~1 MiB, and a single memcpy at that
size per `Ask` is unacceptable.

## The stack

```
sophie (maz/sophie)                         thin requester — no JSON/HTTP/TLS
  ↓ mazarin/claudeclient → ClaudeProtocolClient        (uring IPC)
protocol-claude shepherd (maz/protocol-claude)
  owns: Anthropic Messages JSON schema, x-api-key, model, endpoint identity
  ↓ mazarin/httpclient → HttpProtocolClient            (uring IPC)
protocol-http shepherd (maz/protocol-http)
  owns: HTTP/1.1 framing, TLS, x509, CA bundle, connection lifecycle
  ↓ mazarin/netclient (+ vectored-IO API)
net shepherd                                substrate (not a protocol)
  ↓
virtio-net
```

**Pattern boundary.** `mazarin.<Foo>Client.New() → <Foo>ProtocolClient` is the
public surface at each *protocol* layer (claude, http; tcp/udp when filed). **net is
the substrate, not a protocol** — TCP/UDP/HTTP speak `mazarin/netclient` directly;
wrapping it behind a `NetProtocolClient` would be ceremony without semantic
translation. So there is no `mazarin.NetClient.New()`; vectored-IO is added as extra
methods on the existing client.

`net` becomes a **fourth "early" shepherd** alongside fs/linux/rachel — it depends
only on its kernel device, nothing else.

## Why the layering matters for sophie's readiness

The shepherd **ready flag** must reflect *useful* readiness. If sophie comes up with
the `protocol-claude` shepherd not actually able to reach Claude, she is "pretty much
useless" — so readiness has to propagate up the whole chain (net → protocol-http →
protocol-claude → sophie), not just signal that each process started.

## Request path — zero copy via sub-region write mapping

The escaped prompt bytes land **directly in protocol-http's body Slab** — the same
physical pages handed to the wire. No memcpy anywhere along the chain.

1. sophie calls `client.Ask("plain Go string")`.
2. claudeclient sizes the escaped prompt (`jsonstr.MaxEscapedSize` / `EstimatedSize`)
   and sends protocol-claude an IPC: *"Ask, prompt budget N bytes."*
3. protocol-claude `Reserve`s a body Slab from protocol-http sized
   `envelope_open + N + envelope_close + extraPages` and writes the JSON **envelope**
   (open + close) into it, leaving the prompt region in the middle empty.
4. protocol-claude **grants claudeclient a write mapping over just the prompt region**
   (transfer **Mode 3**, [mazarin-transfer-mode3](mazarin-transfer-mode3.md)).
5. claudeclient `jsonstr.EncodeString(handle.Writer(), prompt)` — escaped bytes land
   in their final wire position. Then `Release`.
6. protocol-http owns the full Slab. It builds HTTP headers right-aligned into the
   leading **extraPages** region (headers END exactly at the body's first byte) and
   issues one contiguous `StreamSend` over headers + envelope + prompt. One small
   header-shaped memcpy, **zero body copy**.

**The extraPages trick is what retired the vectored-IO blocker** — see below.

**v1 trust model.** Page boundaries don't align with the logical envelope/prompt
boundaries, so claudeclient's R/W mapping technically spans a few envelope-overlap
bytes. v1 *trusts* claudeclient not to scribble there (sophie's code is in-tree,
review catches it, and corruption yields a loud HTTP 400). This is a bug-isolation
concern, not security; VM-enforced isolation (page-aligned region + two ≤4 KiB
overhang memcpies) is future work.

## Response path — zero copy via server-initiated handoff + pre-posted pool

1. protocol-http reads the HTTP response body directly into Slab pages — the single
   unavoidable net→pages copy at the wire boundary.
2. **The response Slab is owned by protocol-claude's pool, not allocated ad-hoc.**
   protocol-claude maintains a **pre-posted pool of response Slabs** (transfer
   **Mode 3** grant) and posts them to protocol-http — exactly how the net driver
   pre-posts RX buffers to virtio-net. No allocation latency on the hot path; a
   single ownership chain; predictable RSS. (`maz/protocol-claude/internal/respool.go`.)
3. protocol-claude parses *just enough* JSON to locate the byte range of
   `.content[0].text` (excludes the outer `"` delimiters, includes internal escapes).
   Range-finding only — no extraction, no copy, no unescape.
4. protocol-claude hands the Slab to sophie (transfer **Mode 2**, server-initiated
   handoff — the one hop where ownership truly transfers, since sophie outlives the
   Slab until `Release`) plus a `(textStart, textEnd)` hint.
5. sophie gets a `*ClaudeResponse` backed by the Slab:
   - `resp.Text() []byte` — zero-copy sub-slice.
   - `resp.TextString() string` — `unsafe.String` view, valid until `Release`.
   - `resp.UnescapedReader() io.Reader` — streaming unescape, no allocation.
6. sophie computes in place, then `resp.Release()` returns the pages to the pool.

```go
func (c *ClaudeClient) Ask(prompt string) (*ClaudeResponse, error)

type ClaudeResponse struct { /* slab + text range, unexported */ }
func (r *ClaudeResponse) Text() []byte
func (r *ClaudeResponse) TextString() string   // unsafe.String view — Slab lifetime
func (r *ClaudeResponse) Release()              // caller MUST call when done
```

## Headline decision: scatter-gather (vectored-IO) is NOT a v1 blocker

MAZ-51 (vectored-IO) was originally a hard blocker for protocol-http, which assembles
messages from heterogeneous pieces (request line + headers + body) and wanted to
avoid memcpying them into one contiguous buffer.

But transfer Mode 1's `Allocate(..., extraPages)` already solves it: the server
reserves leading bookkeeping pages (contiguous in its VA) that the client never sees.
protocol-http right-aligns the HTTP headers into that region so they abut the body,
then sends one contiguous span. **MAZ-51 becomes a future perf win, off the critical
path.**

## Naming decisions captured

- **`guardian` → `protocol-claude`.** "guardian" read as a separate concept and would
  surprise people who just *use* sophie; `protocol-claude` names the role plainly.
- **`Share` / `ReceiveShare`** for the shared-mapping verbs (over alternatives).
- **`mazarin/jsonstr`** for the JSON-string escape/unescape + page-backed helpers
  (alternatives `jsonpage`/`jsonio`/`pagejson` left open for `/ticket-plan`).

## Ticket map & final state (closing summary, May-25 session)

| Ticket | State |
|---|---|
| [MAZ-48](https://linear.app/mazarin/issue/MAZ-48) — protocol-claude + sophie reorg | blockedBy [49, 50, 52, 53, 54]; `Ask` returns `*ClaudeResponse`; zero-copy prompt + response; response-page pool in protocol-claude |
| [MAZ-49](https://linear.app/mazarin/issue/MAZ-49) — protocol-http | blockedBy [50, 53] (51 removed); extraPages dance replaces vectored-IO; pre-posted buffers from protocol-claude on the response side; `Do(req, respDest)` signature |
| [MAZ-50](https://linear.app/mazarin/issue/MAZ-50) — transfer foundation + Mode 1 | retitled and trimmed to foundation scope |
| [MAZ-51](https://linear.app/mazarin/issue/MAZ-51) — vectored-IO | no longer blocking; future perf; v1-escape-hatch closer for MAZ-48 |
| [MAZ-52](https://linear.app/mazarin/issue/MAZ-52) — mazarin/jsonstr | unchanged this round |
| [MAZ-53](https://linear.app/mazarin/issue/MAZ-53) — Mode 2 handoff | net-shepherd-informed pre-posted-buffer strategy is the documented response-path pattern; Mode 2 is the protocol-claude → sophie hop only; pool in `maz/protocol-claude/internal/respool.go` |
| [MAZ-54](https://linear.app/mazarin/issue/MAZ-54) — Mode 3 sub-region grant | serves both prompt-path (claudeclient gets a prompt-region grant) AND response-path (protocol-http gets a full-Slab grant) |

**Key design points captured across the tickets:**

- **Net-shepherd ownership pattern adopted for responses** — protocol-claude owns the
  response Slab pool and pre-posts via Mode 3; protocol-http fills in place and never
  holds long-lived response data. Mirrors virtio-net RX pre-posting.
- **Mode 3 is used twice** — prompt sub-region (claudeclient writes into
  protocol-claude's body Slab) AND response full-region (protocol-http writes into
  protocol-claude's response Slab). One primitive, two consumers.
- **Mode 2 is just the protocol-claude → sophie hop** — the only place ownership
  actually transfers.
- **MAZ-51 (vectored-IO) is officially off the critical path.**

**Phase 0 ordering:** MAZ-50 first → MAZ-53 + MAZ-54 in parallel (MAZ-54 carries the
v1 escape hatch if it stalls).
