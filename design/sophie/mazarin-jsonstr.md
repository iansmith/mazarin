> **Provenance:** Promoted from the May-25 2026 Sophie protocol-stack design session (`draft-mazarin-jsonstr.md`).
> Originally drafted in gitignored `.claude/`; recovered into `design/` on 2026-06-01.

# mazarin/jsonstr: JSON-string escape/unescape for page-backed payloads

## Motivation

The protocol-claude / sophie reorg ([MAZ-48](https://linear.app/mazarin/issue/MAZ-48)) demands **zero-copy** on both the request (prompt) and response (assistant text) paths. Prompts in modern Claude usage can run to hundreds of KB or up to ~1 MiB; a single memcpy of that size on every Ask is unacceptable.

The mechanism for zero-copy is that the bytes flowing between shepherds are already in JSON-string-escaped form — the form they'll occupy on the wire. claudeclient escapes the prompt while writing it into the body Slab; protocol-claude finds the response text by byte range without ever touching the bytes. This is an abstraction leak — sophie's library is now in the JSON-escape business — but the cost of the alternative (one ~1 MiB memcpy per Ask) is worse.

This ticket factors the JSON-string-escape logic and the page-backed read/write helpers into a single library so:

- callers like sophie don't need to know JSON-escape rules — they call library functions.
- the prompt/response pages aren't touched by any shepherd that doesn't need to: protocol-claude reads enough JSON to find the text range, then hands the raw bytes through.
- both zero-copy (preferred) and copy-into-string (convenience) flavors of encode/decode live in one place.

Library name `mazarin/jsonstr` — open to alternative names (`jsonpage`, `jsonio`, `pagejson`, `json/strcodec`) in `/ticket-plan`.

## Surface

```go
package jsonstr

// ===== Sizing =====

// MaxEscapedSize returns the worst-case byte length of the JSON-string-
// escaped form of s: 2 * len(s) + ε. Use as the upper-bound size for a
// transfer.Reserve when you don't want to scan the input twice.
func MaxEscapedSize(s string) int

// EstimatedSize does an O(n) scan to compute the exact escaped byte
// length of s, without writing. Use when the worst-case-2x allocation
// is too costly (large prompts, tight Slab budget).
func EstimatedSize(s string) int

// ===== Encoder =====

// EncodeString writes s as JSON-string-escaped bytes to w. Returns the
// number of bytes written. Does NOT emit the surrounding `"` quotes —
// the caller controls quote placement (e.g. the JSON envelope around
// the prompt already has them).
func EncodeString(w io.Writer, s string) (int, error)

// EncodeBytes is EncodeString for a []byte input. UTF-8 is assumed; the
// encoder does not re-validate.
func EncodeBytes(w io.Writer, b []byte) (int, error)

// NewEscapingWriter wraps w so any bytes written to the returned Writer
// are JSON-escaped on the fly. Use when the input is being built
// incrementally (e.g. a prompt streamed from multiple sources).
func NewEscapingWriter(w io.Writer) *EscapingWriter

type EscapingWriter struct{ /* internal */ }
func (e *EscapingWriter) Write(p []byte) (int, error)
func (e *EscapingWriter) Written() int  // input bytes consumed
func (e *EscapingWriter) Output() int   // escaped bytes emitted

// ===== Decoder =====

// View returns a Go string aliasing b — zero copy, unsafe.String under
// the hood. The string's content is the RAW JSON-escaped form: a JSON
// `\n` is two bytes (backslash + n), an internal `\"` is two bytes.
//
// CALLER MUST keep b alive (i.e. don't Release the Slab) for as long
// as the returned string is used. Reading b after the underlying
// transfer-library pages are released is undefined behavior.
//
// Cheap subset operations (strings.Contains, strings.Index, bytes.* on
// the underlying []byte) work as long as the substring being searched
// needs no JSON escape. "Washington" is safe. `"`, newline, backslash
// are not — search with their escaped forms (`\"`, `\n`, `\\`) if
// looking through the raw view.
func View(b []byte) string

// NewUnescapingReader returns an io.Reader that yields the UNESCAPED
// bytes corresponding to the JSON-escaped content of b. Streaming —
// no allocation up front. Use when you actually need the original
// characters (rendering, character-level analysis) rather than the
// raw escaped form.
func NewUnescapingReader(b []byte) *UnescapingReader

type UnescapingReader struct{ /* internal */ }
func (r *UnescapingReader) Read(p []byte) (int, error)

// ===== Convenience (with-copy) =====

// EncodeStringToBytes returns the JSON-escaped form of s as a freshly
// allocated []byte. Use when zero-copy isn't worth the bookkeeping
// (small strings, one-off encodings).
func EncodeStringToBytes(s string) []byte

// DecodeBytesToString returns a freshly allocated Go string containing
// the unescaped content of b. Use when the caller wants the unescaped
// form as a Go string and is willing to pay one copy.
func DecodeBytesToString(b []byte) (string, error)

// ===== Low-level helpers (used by protocol-claude for range finding) =====

// IndexAfterString assumes b starts with the opening `"` of a JSON
// string value and returns the byte index immediately AFTER the
// closing `"`. Correctly handles escapes — `\"` is not a terminator.
// Returns -1 if b doesn't start with `"` or the string is unterminated.
//
// Used by protocol-claude to find the byte range of .content[0].text
// in an Anthropic response without invoking a full JSON parser.
func IndexAfterString(b []byte) int
```

## Definition of Done

1. Package `mazarin/jsonstr` exists with the surface above (or its pinned variant).
2. `EncodeString` / `DecodeBytesToString` round-trip a curated test set: ASCII, UTF-8 multi-byte, control characters, all JSON escapes (`\"`, `\\`, `\/`, `\b`, `\f`, `\n`, `\r`, `\t`, `\uXXXX`).
3. `View` returns a string whose `strings.Contains("Washington", …)` works on a synthesized escaped buffer.
4. `NewUnescapingReader` streams an unescaped form that matches `DecodeBytesToString` byte-for-byte.
5. `NewEscapingWriter` round-trips against `EncodeString` (writing the same bytes via either path produces identical output).
6. `IndexAfterString` survives a fuzzer pass on JSON-string-shaped inputs.
7. Used in production by [MAZ-48](https://linear.app/mazarin/issue/MAZ-48): claudeclient calls `EncodeString` writing into the body Slab; sophie calls `View` on the response text range. No copy of the prompt or response bytes on the critical path.
8. Memory-safety doc: `View`'s lifetime invariant (no use after Slab Release) is written down with a short example showing the right pattern.

## Out of scope

- Full JSON parsing (objects, arrays, numbers, nulls). This is a string-only codec; finding the *position* of `.content[0].text` in an Anthropic response is `IndexAfterString` plus a tiny hand-rolled scan inside protocol-claude, not a `jsonstr` parser.
- Unicode normalization. We pass through what we're given.
- JSON pretty-printing.
- Schema validation.
- Non-UTF-8 inputs (the encoder assumes UTF-8; behavior on invalid sequences is "garbage in, garbage out").

## Dependencies

- No hard blockers. Uses only stdlib types (`io.Writer`, `io.Reader`, `[]byte`, `string`, `unsafe`).
- Pairs with [MAZ-50](https://linear.app/mazarin/issue/MAZ-50): `EncodeString`'s typical Writer is `(transfer.Handle).Writer()`; `View`'s typical input is `(*transfer.Slab).Bytes()`. The two libraries are designed together but neither imports the other.

Blocks:
- [MAZ-48](https://linear.app/mazarin/issue/MAZ-48): claudeclient cannot ship without `EncodeString` (prompt write) and `View` (response read).

Related:
- [MAZ-50](https://linear.app/mazarin/issue/MAZ-50): provides the page-backed `io.Writer` and `[]byte`.

## Open questions for `/ticket-plan`

1. **Name.** `mazarin/jsonstr` vs `mazarin/jsonpage` vs something else.
2. **Buffer reuse in `NewEscapingWriter` / `NewUnescapingReader`.** Do they own internal scratch buffers, or take one from the caller? Lean: own a small (~64-byte) internal scratch for the streaming case, since callers shouldn't need to think about it.
3. **`View`'s safety contract.** Returning a string that aliases the input is `unsafe.String` territory. Document the contract; consider returning a wrapper type (`type StringView string` or similar) to make the lifetime constraint visible in the type system. Lean: just document, keep the type as plain `string` for ergonomics.
4. **`IndexAfterString` error reporting.** -1 sentinel vs typed error. Lean -1 for hot-path use.
5. **Should `EncodeStringToBytes` pool the returned buffer?** Lean no — convenience function, used in cold paths.

## LOC estimate

- Sizing (MaxEscapedSize, EstimatedSize) — ~30
- Encoder (EncodeString, EncodeBytes, EscapingWriter) — ~150
- Decoder (View, UnescapingReader) — ~120
- Convenience (EncodeStringToBytes, DecodeBytesToString) — ~40
- Helpers (IndexAfterString) — ~40
- Tests + fuzz harness — ~250
- Documentation — ~80

Total: ~700 LOC, of which ~250 is tests.
