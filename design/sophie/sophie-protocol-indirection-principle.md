# Sophie & the protocol-indirection principle (the "why" under the protocol stack)

> **Provenance:** Recovered on 2026-06-01 from the May-22 2026 session (`2799c4c5`,
> the one that created [MAZ-43](https://linear.app/mazarin/issue/MAZ-43) "Claude
> Example"). This is the foundational design articulation ("Repeating back the
> scheme") that the protocol-claude / sophie reorg later implemented. The companion
> "how" docs are [sophie-protocol-stack](sophie-protocol-stack.md) and the
> per-component docs it links. This doc captures the *why* — including the
> guardian/monitor layer, which appears nowhere else in `design/`.
>
> Naming note: "gilles" was the placeholder name for the handler shepherd in this
> session; it was renamed **`protocol-claude`** in the May-25 design session.

## The model: a capability/protocol indirection layer, not a direct dependency

Sophie is a **requester**. She wants something *semantically* — "get a response from
Claude." She speaks at the level of "I want the **claude API protocol**." She does
not know, and does not care, how that protocol is satisfied.

### The actors

- **Sophie** — the requester. Sends a high-level "Ask" and waits. No protocol knowledge.
- **`protocol-claude`** (placeholder "gilles") — the Claude-protocol handler shepherd.
  Owns TLS, HTTPS, JSON, headers, the Anthropic Messages API wire shape, retries — all
  of it is *his* problem.
- **A lookup/discovery service** — sophie asks "who supports the claude API protocol?"
  and gets back the handler. (v1 shortcut: no registry; sophie uses
  `sys.WaitForShepherdReady("protocol-claude", timeout)` against a well-known name.
  A real registry can be swapped in later.)
- **uring IPC** — the transport between sophie and the handler once she knows where to
  find him. Standard shepherd-to-shepherd comms.

### The flow (post-reorg)

1. Sophie boots, decides she wants the **claude API protocol**.
2. She resolves the handler (registry query, or well-known name in v1).
3. She opens a uring channel and sends `{prompt}` — e.g. *"What is the capital of the
   US state that contains Seattle?"*
4. The handler does whatever is needed — TLS handshake to api.anthropic.com,
   `POST /v1/messages`, JSON parse, etc. **Sophie has zero visibility into any of it.**
5. The handler returns `{text}` over uring — *"Washington's capital is Olympia…"*
6. Sophie checks for "Washington", logs PASS/FAIL.

### What sophie deliberately does NOT know

- That TLS exists. That CAs exist. That `crypto/tls` exists.
- That HTTPS exists. That JSON exists. That the Anthropic Messages API exists.
- The actual hostname `api.anthropic.com`.
- Whether the handler hits a real endpoint, a local mock, a cached response, or a
  VPN-tunneled endpoint.
- Whether the handler runs every request past a **monitor program** that watches for
  sensitive actions and requires user confirmation before the request leaves the
  machine.
- Whether the handler is logging, redacting, rate-limiting, or retrying.

All of that is the *handler's* business. Sophie just sends a message and waits.

## Why the indirection earns its keep

Because the protocol is defined at a **high semantic level** ("claude API" = "send a
prompt, get text back"), the implementation has room to do useful work that is
**transparent to the requester**:

- **Audit / monitoring (the guardian layer)** — the handler can show the request to a
  separate monitor shepherd that watches for actions the user should approve, and gate
  the outbound request on the user's confirmation. Sophie doesn't know this happens —
  she just sees a slightly longer round trip.
- **VPN / tunnel** — the handler could transparently tunnel. Same idea for a
  TCP/IP-level protocol handler: sophie asks for "TCP", the handler may route over a VPN.
- **Caching, mocking, redacting, proxying** — same shape, all transparent.

**The general principle:** *protocols should be the highest-level semantic the
requester actually cares about.* The lower the level, the less room the handler has to
do anything useful without breaking the requester's assumptions. "I want Claude to
answer this question" leaves enormous implementation freedom; "I want a TLS-encrypted
TCP socket to 1.2.3.4:443 with this exact JSON body" locks the handler out of all the
useful indirection it could be doing.

Three protocol examples made the principle concrete: **claude API**, **TCP/IP** (where
VPN-under-TCP is the transparent value-add), and **HTTP**.

## Naming & namespace consequences

- Handler shepherds are named **`protocol-BLAH`** (`protocol-claude`, `protocol-http`, …).
- Data files migrate from sophie's namespace to the handler's: the CA bundle and API
  key become `/protocol-claude/ssl/cacert.pem` and
  `/protocol-claude/secrets/anthropic/claude-api-key.toml` — they are the *handler's*
  data, not sophie's. Sophie reads no `cacert.pem` because she has no use for one.
- Sophie's import list collapses to: uring IPC + handler lookup + a tiny
  `{prompt}`/`{text}` wire struct. **No `crypto/tls`, no `crypto/x509`, no
  `encoding/json` in sophie** (the sophie↔handler wire shape is shorter and simpler
  than Anthropic's).

## Why this was NOT done in MAZ-43 (deliberately deferred)

MAZ-43 intentionally shipped sophie as a **fat client** (owning TLS+HTTP+JSON via
`mazarin/claude`) because the goal was *proving end-to-end connectivity to Anthropic*
across many moving parts. The reorg requires a new wire format, new uring patterns,
namespace migration, and a boot-sequence reshuffle — so: **prove the stack works
end-to-end first, reorg after.** That reorg is the protocol-claude / sophie effort
captured in [sophie-protocol-stack](sophie-protocol-stack.md).
