> **Provenance:** Promoted from the May-25 2026 Sophie protocol-stack design session (`draft-mazarin-transfer-mode3.md`).
> Originally drafted in gitignored `.claude/`; recovered into `design/` on 2026-06-01.

# mazarin/transfer mode 3: sub-region write mapping (windowed R/W grant)

## Motivation

Split out of the original [MAZ-50](https://linear.app/mazarin/issue/MAZ-50) when its scope grew to cover three distinct page-transfer modes. This ticket adds **mode 3: sub-region write mapping** to the `mazarin/transfer` library that [MAZ-50](https://linear.app/mazarin/issue/MAZ-50) establishes.

Sub-region write mapping is **server lends a window** to a client: the server owns a Slab, grants the client temporary R/W mapping to a specific byte-range within it, and revokes the mapping when the client releases. The server's page-state machine never lost ownership.

This is the primitive that enables the zero-copy prompt path in [MAZ-48](https://linear.app/mazarin/issue/MAZ-48):

1. protocol-http allocates a body Slab.
2. protocol-claude takes the Slab (via mode 1 Reserve/Commit), writes the JSON envelope opening and closing into it.
3. protocol-claude `GrantWrite`s claudeclient a window over just the prompt region.
4. claudeclient writes the escaped prompt directly into that window — bytes land in their final wire position.
5. claudeclient `Release`s; protocol-claude `RevokeWrite`s; the full Slab is handed to protocol-http for sending.

Without mode 3, the prompt would have to be memcopied from one Slab to another at protocol-claude — a ~1 MiB memcpy per Ask in the worst case, unacceptable per [MAZ-48](https://linear.app/mazarin/issue/MAZ-48)'s zero-copy requirement.

## v1 escape hatch

If mode 3 turns out hard kernel-side, [MAZ-48](https://linear.app/mazarin/issue/MAZ-48) has a documented fallback: accept one prompt memcpy at protocol-claude, defer this ticket. This means mode 3 can slip without blocking the rest of the chain — at the cost of one perf regression on large prompts. **Worth tracking as a real fallback during /ticket-plan.**

## Surface

```go
// Server-side: grant a client R/W access to a byte-range of a Slab owned
// by the server. The server retains ownership of the Slab.
//
// offset/length define the LOGICAL byte range; the kernel maps the pages
// that cover that range into the client's address space with R/W. Page
// boundaries don't necessarily align with the logical range — see "v1
// trust model" below.
func (s *Slab) GrantWrite(toClient ShepherdID, offset, length int) (Handle, error)

// Server-side: revoke a previously granted mapping. Called after the
// client has Released its end. Page-state machine cleanup.
func (s *Slab) RevokeWrite(h Handle) error

// Client-side: receive a sub-region write grant from a server.
func ReceiveWriteGrant(fromServer ShepherdID) (Handle, error)

// Client-side: release the mapping back to the server. Pages unmap from
// the client's address space. Server can now safely RevokeWrite.
func (h Handle) Release() error
```

## v1 trust model

`GrantWrite` maps pages — not byte ranges. The kernel's protection granularity is the page (4 KiB on ARM64), so when the granted logical range doesn't align with page boundaries:

- The page that holds the byte BEFORE the granted range's start also holds the first byte(s) of the granted range. The client has R/W on the whole page.
- Symmetric at the end.

In the prompt-path use case ([MAZ-48](https://linear.app/mazarin/issue/MAZ-48)), this means the page containing the tail of protocol-claude's envelope-open ALSO contains the head of claudeclient's prompt region. claudeclient has R/W on the whole page. A misbehaving (or buggy) client could scribble outside its granted byte range into the envelope-overlap portion of those pages.

**v1 accepts this trust model.** Justification:
- The offending client (claudeclient → sophie) is in-tree, code-reviewed.
- Corruption manifests loud: malformed JSON to Anthropic → HTTP 400 → sophie's PASS check fails clearly.
- This is a bug-isolation concern, not a security boundary.

## Future enhancement: VM-enforced sub-region isolation

The v1 trust model can be tightened with two small (≤4 KiB each) memcpies at the start and end of the granted range:

- The server pre-positions the first up-to-4 KiB of "client-visible content" into a server-private staging page that it copies into the client-visible aligned region before/after the client writes. Same at the tail.
- The grant restricts the client's R/W access to ONLY the pages **fully inside** the granted byte range — no overlap pages.

This closes the misbehavior gap entirely, at the cost of two small memcpies per grant. Worth doing once we have a non-trusted client, or once an M-sweep stability bug traces back to a sub-region scribble. **Defer until needed.**

## Definition of Done

1. `GrantWrite`, `RevokeWrite`, `ReceiveWriteGrant` exist with the surface above (or its pinned variant).
2. **Used in production by the prompt-path zero-copy in [MAZ-48](https://linear.app/mazarin/issue/MAZ-48)** — claudeclient writes the escaped prompt directly into protocol-claude's body Slab via a write grant, no intermediate copy.
3. **No body-sized copies anywhere on the request path** — verified by inspection of the protocol-claude code and by a benchmark showing constant-time wallclock for `Ask` calls regardless of prompt size, up to the 1 MiB soft cap.
4. **v1 trust model documented** — callers know that a `GrantWrite` over a non-page-aligned range gives the client R/W on the overlap pages, and that the page-overlap bytes are trust-only.
5. M-sweep stability survives grant/release churn (allocate → grant → write → release → revoke loops) with no leaked pages, no stuck grants, clean shutdown on crash on either side.
6. **Future-VM-isolation note retained** in the ticket so we don't lose the design when we want to harden.

## Out of scope

- VM-enforced sub-region isolation (future enhancement, deferred — see above).
- Read-only sub-region mappings. Different mode, different ticket if/when needed (lean: file when there's a concrete consumer).
- Sub-region grants from a Slab the granter doesn't own (e.g. a transitively-handed-off Slab). Only the current owner can `GrantWrite`.
- Concurrent grants on overlapping byte ranges of the same Slab. Lean: refuse with a typed error; tighten if a use case shows up.

## Dependencies

Hard blockers:
- [MAZ-50](https://linear.app/mazarin/issue/MAZ-50) — foundation: Slab/Handle/encoder.

Blocks (with escape hatch):
- [MAZ-48](https://linear.app/mazarin/issue/MAZ-48) prompt zero-copy. If this ticket slips, MAZ-48 falls back to one prompt memcpy at protocol-claude.

## Open questions for `/ticket-plan`

1. Notification of release: how does the server learn the client has called `Release()`? Polled? Active notification via the IPC? Lean active notification.
2. Multiple concurrent grants from the same Slab (different non-overlapping ranges to different clients) — supported in v1 or refuse? Lean supported, with non-overlap checked.
3. What happens if the client crashes between `ReceiveWriteGrant` and `Release`? The server's `Slab` is stuck pending a never-arriving Release. Need a timeout or crash-detection mechanism. Pin during planning.
4. Alignment for `offset` and `length`: byte-granular logical input, or require caller to give page-aligned values?
5. Kernel-side: can mode 3 ride on the same SVC as modes 1/2 with a different sub-opcode, or does it need its own?

## LOC estimate

- `GrantWrite`, `RevokeWrite`, `ReceiveWriteGrant` — ~150
- Kernel-side support (cross-VA mapping with R/W on a logical sub-range) — ~100-200 depending on how much can be folded into existing primitives
- Tests including misbehaving-client scenarios — ~100
- Documentation (state machine + trust model + future-VM note) — ~80

Total: ~430-530 LOC. Smaller than the original combined MAZ-50, but with the largest kernel-side uncertainty of the three modes.
