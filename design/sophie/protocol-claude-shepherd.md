> **Provenance:** Promoted from the May-25 2026 Sophie protocol-stack design session (`draft-MAZ-48-protocol-claude.md`).
> Originally drafted in gitignored `.claude/`; recovered into `design/` on 2026-06-01.

# protocol-claude shepherd: claude API handler + sophie thin-client reorg

## Motivation

[MAZ-43](https://linear.app/mazarin/issue/MAZ-43) shipped sophie as a fat client. This ticket extracts the Anthropic Messages API semantics + auth into a dedicated shepherd (`protocol-claude`), leaving sophie as a thin requester.

After this ticket and its three sibling tickets (protocol-http, mazarin transfer library, net vectored-IO), the stack looks like:

```
sophie (maz/sophie)
  ↓ mazarin/claudeclient → ClaudeProtocolClient    (uring IPC)
protocol-claude shepherd (maz/protocol-claude)
  owns: Anthropic Messages JSON schema, x-api-key, model, endpoint identity
  ↓ mazarin/httpclient → HttpProtocolClient        (uring IPC)
protocol-http shepherd (maz/protocol-http)
  owns: HTTP/1.1 framing, TLS, x509, CA bundle
  ↓ mazarin/netclient (+ vectored-IO API)
net shepherd
```

This ticket lands the **top layer**: sophie thin-client + protocol-claude shepherd + the `mazarin/claudeclient` surface. The two layers below (protocol-http, vectored-IO) are tracked in their own tickets; this ticket consumes their interfaces.

## Layering decisions

- **`mazarin.ClaudeClient` is the public surface for sophie and any other Claude consumer.** Construction returns a `ClaudeProtocolClient` interface. Options are claude-specific (model, max_tokens, etc.). Same shape applies to `mazarin.HttpClient.New() → HttpProtocolClient` and any future protocol client (TCP, etc.).
- **Explicitly NOT supporting Go's `net/http` shape.** The `*ProtocolClient` interfaces are mazarin-native. Compatibility with stdlib HTTP belongs to a future linux-emulation layer and is not part of any current ticket.
- **protocol-claude no longer owns TLS or HTTP.** It owns the Anthropic Messages API JSON schema, the `x-api-key` header semantics, model defaults, and endpoint identity. Everything below that is `mazarin/httpclient`'s problem.

## Naming

The shepherd is `protocol-claude` — both the binary on disk (`protocol-claude.maz`) and the registered shepherd name. The codename "guardian" is dropped: this reorg will surprise readers who only know sophie as-is, and the name should describe the role unambiguously, not introduce a new mascot. The `protocol-<NAME>` convention from MAZ-43 carries forward.

Note: Go package directories can have hyphens, but Go package identifiers cannot. The package declaration inside `maz/protocol-claude/` is `package protocolclaude`. Document inline at the top of `main.go`.

## Definition of Done

1. **A `protocol-claude.maz` shepherd loads at boot and is sophie's only path to Claude.**
   Verify: `grep -rE 'crypto/tls|crypto/x509|encoding/json|api\.anthropic\.com' maz/sophie/` returns nothing. `task` builds `build/protocol-claude.maz`. Boot reports both `[sophie] PASS …` and a `[protocol-claude] ready` line in the serial log.

2. **Sophie no longer imports crypto/tls, crypto/x509, encoding/json, or any HTTP code.**
   Verify: `go list -deps mazzy/maz/sophie | grep -E 'crypto/tls|crypto/x509|encoding/json|net/http'` returns empty. Sophie's `.maz` shrinks measurably — record before/after sizes in the PR description.

3. **Sophie speaks only "claude protocol" — no HTTP, no JSON, no TLS.**
   Verify: sophie's main reduces to `claudeclient.New(...) → Ask(prompt) → assert on text`. The IPC wire format lives in `shared/protocol/claude/` and is the only sophie ↔ protocol-claude contract.

4. **The API key file lives in `/protocol-claude/secrets/anthropic/`, not `/sophie/secrets/`.**
   Verify: `/sophie/secrets/` no longer present on the ext2 image; `/protocol-claude/secrets/anthropic/claude-api-key.toml` is. (The CA bundle moves separately under the protocol-http ticket — see Dependencies.)

5. **`mazarin/claudeclient` exposes the standard `mazarin.<Foo>Client.New() → <Foo>ProtocolClient` shape.**
   Verify: `mazarin/claudeclient/client.go` exports `New(opts ...Option) (ClaudeProtocolClient, error)`. `ClaudeProtocolClient` is an interface; concrete types are unexported.

6. **End-to-end PASS still works.**
   Verify: `task run-arm64-hvf TIMEOUT=60` → `[sophie] PASS response contains Washington`. The MAZ-43 verification recipes (bad API key, empty CA bundle) still produce FAIL — they apply to the protocol-claude / protocol-http shepherds' files now.

7. **amd64 path defined-but-not-gated.** Same standard as MAZ-43.

## Actors

- **sophie** (`maz/sophie/`) — requester. Imports `mazarin/claudeclient` + uring + sys. No crypto, no JSON, no HTTP knowledge.
- **protocol-claude** (`maz/protocol-claude/`) — claude API handler. Imports `mazarin/httpclient` (consumes lower layer) + an internal Anthropic-Messages JSON encoder (moved here from the deleted `mazarin/claude` package). Owns API key, model defaults, endpoint hostname. Does NOT own TLS or HTTP wire framing.
- **`mazarin/claudeclient`** (new library) — sophie-side client. Constructs IPC requests, talks to protocol-claude over uring.

## Wire protocol (sophie ↔ protocol-claude)

Public surface (v1):

```go
type ClaudeProtocolClient interface {
    Ask(prompt string) (text string, err error)
}
```

IPC wire format:
- `ipc.ProtoClaudeIPCReq` / `ipc.ProtoClaudeIPCResp` constants mirroring the existing `ProtoFSIPCResp` / `ProtoNetIPCResp` pattern.
- Small fields (request kind, error codes) encoded via the **mazarin transfer library**'s length-prefix encoder.
- Large fields (the prompt string, the response text) use the transfer library's two-phase contiguous-VA page handoff. Concretely: sophie says "I want to send N bytes"; protocol-claude allocates a contiguous-VA span sized for N (plus any internal bookkeeping pages) and returns to sophie a VA + a page count that excludes the bookkeeping pages; sophie fills the pages and releases-and-notifies (or notifies-and-releases — order pinned in the transfer-library ticket).

Both encoder and transfer plumbing live in the **mazarin transfer library** ticket. This ticket consumes that surface; it does not define it.

## JSON encoding without copies

Claude-protocol's hot path is: build a JSON request → pass to `mazarin/httpclient` → which goes to protocol-http → which sends over net. Each hop is a potential copy point, and protocol-claude doesn't directly control what its downstream does. The architectural intent is that the bytes live in transfer-library pages from the moment the JSON encoder produces them and stay there all the way to the wire.

Concrete flow:

1. protocol-claude calls `transfer.Reserve(httpShepherdID, kind, estimatedSize)` to get a `Handle` whose pages are owned by protocol-http but mapped into protocol-claude's address space.
2. protocol-claude points `encoding/json` (or a hand-rolled emitter) at `Handle.Writer()`, which writes directly into those pages.
3. protocol-claude `Commit()`s → pages move to protocol-http's address space.
4. protocol-http packages headers + body into a vectored-IO send ([MAZ-51](https://linear.app/mazarin/issue/MAZ-51)) referencing those same pages — no body copy on the way to the wire.

This means [MAZ-49](https://linear.app/mazarin/issue/MAZ-49)'s `Request.Body` is a `transfer.Handle`, not `[]byte`, and [MAZ-50](https://linear.app/mazarin/issue/MAZ-50) exposes `Handle.Writer()` for stdlib-encoder compatibility.

Options for the encoder step (resolve in `/ticket-plan`):

- **A.** Use stdlib `encoding/json` with `Handle.Writer()`. Easy, but requires a size estimate up front. Wasted tail bytes are cheap (Commit truncates).
- **B.** Hand-roll a JSON emitter specific to the Anthropic Messages schema. No size guessing, no stdlib transitive deps. More code, but the schema is small.
- **C.** Encode into a small stack buffer, copy into transfer pages once at the end. One copy, simple. Acceptable v1 if A and B prove risky.

For the current MAZ-43 prompt (~100-byte JSON envelope + a short prompt string), all three are fine. The decision matters when the prompt is large (multi-page) — at which point option A's size-estimation strategy is the most flexible.

**Co-design constraint:** this is the main place MAZ-48, MAZ-49, and MAZ-50 interact. Pin together during `/ticket-plan`, not independently per ticket.

## File-level changes

### New files
- `maz/protocol-claude/Taskfile.yml` — mirrors `maz/sophie/Taskfile.yml`.
- `maz/protocol-claude/main.go` — shepherd orchestration: load API key from `/protocol-claude/secrets/anthropic/...`, init `mazarin/httpclient`, serve `ProtoClaudeIPCReq` over uring, encode Anthropic Messages JSON, parse responses.
- `maz/protocol-claude/internal/anthropic.go` — JSON request/response types + encoder/decoder (the Anthropic schema bits from the now-deleted `mazarin/claude`).
- `mazarin/claudeclient/client.go` — `New(opts ...Option) (ClaudeProtocolClient, error)` + the interface + uring IPC plumbing on top of the transfer library.
- `mazarin/claudeclient/options.go` — `WithModel(string)`, `WithMaxTokens(int)`, `WithShepherdName(string)` (defaults `"protocol-claude"`), etc.
- `shared/protocol/claude/wire.go` — IPC types + decode helpers (the encoder side is the transfer library).
- `shared/ipc/protoIDs.go` (or wherever `ProtoFSIPCResp` lives) — add `ProtoClaudeIPCReq` + `ProtoClaudeIPCResp`.

### Modified files
- `maz/sophie/main.go` — strip crypto/tls, crypto/x509, encoding/json (transitive), netconn import, parseIPv4, IP/key/CA file reads. New body: `claudeclient.New(...)` → `Ask(prompt)` → PASS/FAIL on substring.
- `maz/sophie/netconn.go` — **delete entirely.** Was a `net.Conn` adapter for direct TLS; obsolete.
- `Taskfile.yml` — drop sophie's `/sophie/ssl=...` and `/sophie/secrets=...` mkext2 deps; add `protocol-claude:arm64` to disk-arm64 deps; mkext2 gets `-dir /protocol-claude/secrets=./protocol-claude/secrets` and positional `{{.PROTOCOL_CLAUDE_MAZ}}`. CA bundle wiring is protocol-http ticket's responsibility.
- `config/startup.arm64.toml` + `.amd64.toml` — add `[[shepherd]] name="protocol-claude" path="/protocol-claude.maz"`. (protocol-http entry is in its own ticket.)
- `.gitignore` — repath `/sophie/secrets/**/*.toml` → `/protocol-claude/secrets/**/*.toml` (mirror for `.example`).

### Deleted
- `mazarin/claude/` — the existing fat library. Its JSON+Anthropic-schema content moves into `maz/protocol-claude/internal/`. Its TLS/HTTP/net.Conn code is replaced by `mazarin/httpclient` (protocol-http ticket). Since net/http compat is explicitly not a goal, there's no reason to keep `mazarin/claude` as a public package.

### Moved files
- `sophie/secrets/anthropic/claude-api-key.toml{,.example}` → `protocol-claude/secrets/anthropic/claude-api-key.toml{,.example}`.

### Touched but owned by sibling tickets
- `sophie/ssl/cacert.pem` (+ `.sha256`) → moves to `protocol-http/ssl/cacert.pem` (protocol-http ticket).
- `mazarin/httpclient/` — new package (protocol-http ticket).
- `mazarin/transfer/` — new package (mazarin transfer library ticket).

## Out of scope (deferred)

- Go `net/http` compatibility — explicitly NOT a goal here or anywhere. Future linux-emulation work.
- Streaming, tool use, multimodal, system prompts, conversation history, token accounting.
- DNS — endpoint still configured via the existing `endpoint_ip` field (now on the protocol-http side post-reorg).
- `context.Context` plumbing — [MAZ-47](https://linear.app/mazarin/issue/MAZ-47).
- Page-backed file loader for API key — [MAZ-45](https://linear.app/mazarin/issue/MAZ-45).
- Multiple Claude API versions, capability negotiation, real shepherd registry.
- TCP as its own protocol shepherd — explicitly future, not filed yet. For now protocol-http calls `mazarin/netclient` directly.

## Dependencies

Hard blockers:
- **protocol-http ticket** — protocol-claude consumes `mazarin/httpclient.HttpProtocolClient`. Cannot ship without it.
- **mazarin transfer library ticket** — sophie ↔ protocol-claude wire format consumes it. Cannot ship without it.

Soft (used by but not required):
- [MAZ-45](https://linear.app/mazarin/issue/MAZ-45) — page-backed file loader (protocol-claude becomes a natural consumer).
- [MAZ-47](https://linear.app/mazarin/issue/MAZ-47) — `context.Context` plumbing.
- [MAZ-41](https://linear.app/mazarin/issue/MAZ-41) — DNS (long-term).

Parent: [MAZ-43](https://linear.app/mazarin/issue/MAZ-43).

## Open questions for `/ticket-plan`

1. Does sophie keep its dedicated `sophieFsRing = 1`? Sophie no longer reads files (protocol-claude does the API-key read). Probably remove.
2. `mazarin/claudeclient` options API: explicit positional `New(model, maxTokens, ...)` vs functional options `New(opts ...Option)`. Lean functional options for forward compat.
3. Boot ordering: sophie waits for `protocol-claude`; protocol-claude waits for `protocol-http`; protocol-http waits for `net` and `fs`. Confirm the chain via `WaitForShepherdReady` works without explicit ordering in startup.toml, or pin an order.
4. Connection lifecycle on the IPC side — does `claudeclient.New` open a long-lived uring channel to protocol-claude, or does each `Ask` re-establish? Lean long-lived.

## LOC estimate

- `maz/protocol-claude/main.go` + Anthropic JSON encoder (moved from `mazarin/claude`) — ~200
- `mazarin/claudeclient/{client,options}.go` — ~150
- `shared/protocol/claude/wire.go` — ~60
- `maz/sophie/main.go` rewrite — net ~-150 (~30 new, ~180 stripped)
- `maz/sophie/netconn.go` delete — ~-120
- `mazarin/claude/` delete (most of content moves into `maz/protocol-claude/internal/`) — net ~-100
- Build wiring, startup, gitignore — ~25
