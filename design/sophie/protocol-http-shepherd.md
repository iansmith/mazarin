> **Provenance:** Promoted from the May-25 2026 Sophie protocol-stack design session (`draft-protocol-http.md`).
> Originally drafted in gitignored `.claude/`; recovered into `design/` on 2026-06-01.

# protocol-http shepherd: HTTP/HTTPS protocol handler

## Motivation

Per the architectural pattern landed in [MAZ-48](https://linear.app/mazarin/issue/MAZ-48), protocol-claude wants to ship JSON+headers to a remote server and read JSON+headers back. The HTTP/1.1 framing, TLS handshake, x509 chain validation, and connection management belong in a dedicated shepherd that protocol-claude (and any other HTTP consumer) calls into via a `mazarin/httpclient.HttpProtocolClient` interface.

After this ticket:
- **protocol-http** owns: HTTP/1.1 message framing, request/response parsing, TLS, x509, CA bundle, connection lifecycle.
- **`mazarin/httpclient`** exposes the request-level surface: build a request (method, URL, headers, body), get a response (status, headers, body).
- **protocol-claude** no longer touches `crypto/tls`, `crypto/x509`, or HTTP wire formatting.

## Layering

```
mazarin/httpclient → HttpProtocolClient    (uring IPC)
  ↓
protocol-http shepherd
  uses mazarin/netclient for TCP
  uses the vectored-IO API (separate ticket) for scatter-gather sends
  ↓
net shepherd
```

TCP-as-its-own-protocol-handler is intentionally deferred. For now protocol-http calls `mazarin/netclient` directly. The future `protocol-tcp` shepherd would slot in between but is not filed yet.

## Surface

Same `mazarin.<Foo>Client.New() → <Foo>ProtocolClient` pattern as `mazarin/claudeclient`:

```go
package httpclient

func New(opts ...Option) (HttpProtocolClient, error)

type HttpProtocolClient interface {
    Do(req *Request) (*Response, error)
}

type Request struct {
    Method  string                  // "GET", "POST", …
    URL     string                  // "https://api.anthropic.com/v1/messages"
    Headers []Header
    Body    transfer.Handle         // body lives in transfer pages; caller writes via Body.Writer() or Body.Bytes()
}

type Header struct {
    Name, Value string
}

type Response struct {
    StatusCode int
    Headers    []Header
    Body       transfer.Handle      // response body lives in transfer pages too
}
```

**`HttpProtocolClient` is NOT compatible with Go's `net/http`.** Types are mazarin-native. Any future `net/http` adapter belongs in a hypothetical linux-emulation layer and is explicitly out of scope.

**Body is always a `transfer.Handle`, not `[]byte`.** This is deliberate: it lets protocol-claude's JSON encoder write directly into the pages that protocol-http will hand to the wire, eliminating the copy at the protocol-claude → protocol-http boundary. See [MAZ-48](https://linear.app/mazarin/issue/MAZ-48)'s "JSON encoding without copies" section for the rationale. Callers with a small literal payload use `transfer.Reserve(httpSID, ..., len(payload))`, copy in once, then `Commit()` — cheap and uniform.

## Options (claude-protocol use case)

- `WithRootCAs(*x509.CertPool)` — populated from `/protocol-http/ssl/cacert.pem`.
- `WithEndpointIP(host string, ip4 [4]byte)` — name→IP shortcut while [MAZ-41](https://linear.app/mazarin/issue/MAZ-41) is unfinished. Lets protocol-claude say "api.anthropic.com → 1.2.3.4" without protocol-http doing DNS.
- `WithShepherdName(string)` — defaults `"protocol-http"`.
- `WithMinTLSVersion(uint16)` — defaults `tls.VersionTLS12`.

## Definition of Done

1. **`protocol-http.maz` builds, boots, registers as `protocol-http`.**
   Verify: `grep "protocol-http" config/startup.arm64.toml`; boot log shows `[protocol-http] ready`.

2. **CA bundle lives in `/protocol-http/ssl/cacert.pem`.**
   Verify: `/sophie/ssl/` and `/protocol-claude/ssl/` are absent from the ext2 image; `/protocol-http/ssl/cacert.pem` is present and matches the recorded SHA-256.

3. **`mazarin/httpclient.New(...).Do(req)` posts to `api.anthropic.com/v1/messages` end-to-end.**
   Verify: a smoke test (either MAZ-48's sophie chain or a new `xfertest:httpsExample` stage) gets a 200 with the Washington-substring PASS.

4. **No `crypto/tls`, `crypto/x509`, or HTTP wire-framing code outside `maz/protocol-http/`.**
   Verify: `grep -rE 'crypto/tls|crypto/x509' mazarin/ maz/sophie/ maz/protocol-claude/` returns nothing. `grep -rE 'CRLF\|HTTP/1\.1' mazarin/ maz/sophie/ maz/protocol-claude/` returns nothing.

5. **The vectored-IO API is exercised on the request path.**
   Verify: the request line + headers + body are sent via `netclient.StreamSendV` (or equivalent — see vectored-IO ticket), not as a memcopied single buffer.

6. **amd64 path defined-but-not-gated.** Same standard as MAZ-43.

## File-level changes (sketch)

### New
- `maz/protocol-http/Taskfile.yml`
- `maz/protocol-http/main.go` — shepherd orchestration + IPC dispatch
- `maz/protocol-http/internal/tls.go` — TLS handshake glue (calls `crypto/tls.Client` over an adapter built on `mazarin/netclient`)
- `maz/protocol-http/internal/http1.go` — request building, response parsing, header handling
- `mazarin/httpclient/client.go` — `New(...) (HttpProtocolClient, error)`
- `mazarin/httpclient/options.go`
- `mazarin/httpclient/types.go` — `Request`, `Response`, `Header`
- `shared/protocol/http/wire.go` — IPC wire format
- `shared/ipc/protoIDs.go` — add `ProtoHttpIPCReq` / `ProtoHttpIPCResp`
- `protocol-http/ssl/cacert.pem` + `.sha256`

### Modified
- `Taskfile.yml` — protocol-http build target + mkext2 deps for `/protocol-http/ssl/`
- `config/startup.arm64.toml` + `.amd64.toml` — add `[[shepherd]] name="protocol-http" path="/protocol-http.maz"`

## Out of scope

- HTTP/2, HTTP/3 — separate shepherds when needed.
- Connection reuse / pooling — v1 is `Connection: close` per request (same as MAZ-43).
- Redirect following — defer; surface 3xx to the caller.
- Cookies, compression, chunked transfer-encoding for the request body — only response chunked decoding is needed for the Anthropic case and only if Anthropic uses it.
- DNS — see [MAZ-41](https://linear.app/mazarin/issue/MAZ-41).
- Receive-side scatter-gather (`StreamRecvV`) — only the send side is in scope here.
- `net/http` compatibility — explicitly future linux-emulation work.

## Dependencies

Hard blockers:
- **mazarin transfer library ticket** — IPC payload encoding (small fields and the body page handoff).
- **net vectored-IO ticket** — scatter-gather sends on the wire side.

Soft:
- [MAZ-41](https://linear.app/mazarin/issue/MAZ-41) (DNS) — long-term.
- [MAZ-32](https://linear.app/mazarin/issue/MAZ-32) (gvisor zero-copy RX) — improves response path but not required.

Related:
- [MAZ-48](https://linear.app/mazarin/issue/MAZ-48) — primary consumer.
- [MAZ-43](https://linear.app/mazarin/issue/MAZ-43) — original fat sophie.

## Open questions for `/ticket-plan`

1. URL parsing — mazarin-native parser (in `mazarin/httpclient/internal/url.go`) vs reusing Go's `net/url`. Stdlib is convenient; `net/url` doesn't transitively pull `net/http` so it might be ok. Pin.
2. Where does redirect policy live (when we add it)?
3. Response-body buffering strategy: stream into transfer-library pages as bytes arrive vs allocate the whole body up front. Lean stream.
4. TLS handshake currently uses `crypto/tls.Client(conn net.Conn, …)`. Adapter over `mazarin/netclient` for `net.Conn` is the same ~50-100 line pattern that lived in MAZ-43's `maz/sophie/netconn.go` — does that code move into `maz/protocol-http/internal/` essentially unchanged, or do we rewrite?

## LOC estimate

- protocol-http shepherd + TLS glue + HTTP/1.1 framing: ~400
- `net.Conn` adapter (moved/rewritten from sophie's old `netconn.go`): ~120
- `mazarin/httpclient` (client + options + types + uring framing): ~150
- IPC wire format + protoIDs: ~80
- Build wiring, startup, file moves: ~30
