// http1.go — HTTP/1.1 request building and response parsing for the
// protocol-http shepherd. v1 is intentionally narrow:
//
//   - Request side: build a request line + headers + CRLF into a caller-
//     supplied destination buffer (the caller positions the result
//     right-aligned within the request Slab's prefix region so headers
//     end exactly at body[0] — see dispatch.go in item 7).
//
//   - Response side: parse status-line + headers from the front of a
//     buffer the wire-read code has filled. Returns the byte offset
//     where the body starts. Body parsing per se is "everything from
//     bodyStart to bodyStart+Content-Length"; chunked transfer-encoding
//     is OUT OF SCOPE (Linear DoD; defer to a future ticket if Anthropic
//     starts using it on non-streaming responses).
//
// Lifted in spirit from mazarin/claude/client.go's hand-rolled
// HTTP/1.1 code; rewritten here without any Anthropic-specific knowledge.
package internal

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
)

// Header is a single HTTP/1.1 header. Mirrors mazarin/httpclient.Header
// but is duplicated here so internal/ has no cross-module dependency.
type Header struct {
	Name, Value string
}

// MethodString maps the IPC HttpMethod enum to the HTTP/1.1 token.
// Lives in this package (not shared/ipc) because the enum is wire
// concern + this is the wire-formatting concern.
//
// Unknown methods produce "" so the caller can detect invalid input.
func MethodString(m uint16) string {
	switch m {
	case 1:
		return "GET"
	case 2:
		return "POST"
	case 3:
		return "PUT"
	case 4:
		return "DELETE"
	case 5:
		return "HEAD"
	case 6:
		return "PATCH"
	default:
		return ""
	}
}

// MaxHeaderSize is the largest serialized request-line + headers block
// BuildRequest will produce before failing. Sized to fit comfortably in
// one 4 KiB extraPages prefix; if the caller reserves less, BuildRequest
// reports the actual size needed via ErrHeaderOverflow.
const MaxHeaderSize = 4096

// ErrHeaderOverflow signals that the formatted request line + headers
// don't fit within MaxHeaderSize (or the dst the caller supplied).
var ErrHeaderOverflow = errors.New("internal/http1: serialized headers exceed buffer capacity")

// BuildRequest writes the HTTP/1.1 request line + supplied headers + the
// trailing empty-line CRLF into dst. Returns the number of bytes written.
//
// Caller is responsible for:
//   - supplying Host as a separate arg (it's the SNI host of the
//     connection, not always derivable from the URL path)
//   - supplying any Content-Length, Content-Type, Authorization, etc.
//     in headers — BuildRequest does NOT auto-add Content-Length from
//     bodyLen because the caller knows the layout (prefix region size,
//     body region size) and we want to keep this function pure.
//
// Format:
//
//	<METHOD> <urlPath> HTTP/1.1\r\n
//	Host: <host>\r\n
//	<each header>: <value>\r\n
//	\r\n
func BuildRequest(dst []byte, method, urlPath, host string, headers []Header) (int, error) {
	if method == "" {
		return 0, errors.New("internal/http1: empty method")
	}
	if urlPath == "" {
		return 0, errors.New("internal/http1: empty URL path")
	}
	if host == "" {
		return 0, errors.New("internal/http1: empty host")
	}
	// We format into a small scratch buffer first so we can fail with a
	// clean ErrHeaderOverflow when dst is too small, rather than partially
	// writing and leaving dst in a half-formatted state.
	var buf bytes.Buffer
	buf.Grow(256 + 64*len(headers))
	buf.WriteString(method)
	buf.WriteByte(' ')
	buf.WriteString(urlPath)
	buf.WriteString(" HTTP/1.1\r\n")
	buf.WriteString("Host: ")
	buf.WriteString(host)
	buf.WriteString("\r\n")
	for _, h := range headers {
		if h.Name == "" {
			return 0, fmt.Errorf("internal/http1: empty header name (value=%q)", h.Value)
		}
		if !isValidTokenString(h.Name) {
			return 0, fmt.Errorf("internal/http1: invalid header name %q", h.Name)
		}
		buf.WriteString(h.Name)
		buf.WriteString(": ")
		buf.WriteString(h.Value)
		buf.WriteString("\r\n")
	}
	buf.WriteString("\r\n")
	n := buf.Len()
	if n > MaxHeaderSize {
		return 0, fmt.Errorf("%w: produced %d bytes, MaxHeaderSize=%d", ErrHeaderOverflow, n, MaxHeaderSize)
	}
	if n > len(dst) {
		return 0, fmt.Errorf("%w: produced %d bytes, dst capacity=%d", ErrHeaderOverflow, n, len(dst))
	}
	copy(dst, buf.Bytes())
	return n, nil
}

// ParseStatusLine consumes the first line of an HTTP/1.1 response.
// Returns the parsed status code and the offset within buf where the
// next line (i.e. the first header) starts.
//
// Accepts "HTTP/1.1 <code> <reason>\r\n" (and "HTTP/1.0" for forgiveness).
// Rejects truncated buffers, missing CRLF, or non-numeric status codes.
func ParseStatusLine(buf []byte) (statusCode int, next int, err error) {
	end := bytes.Index(buf, []byte("\r\n"))
	if end < 0 {
		return 0, 0, errors.New("internal/http1: status line missing CRLF")
	}
	line := buf[:end]
	// "HTTP/1.1 200 OK"
	if !bytes.HasPrefix(line, []byte("HTTP/1.")) {
		return 0, 0, fmt.Errorf("internal/http1: bad protocol prefix in status line %q", line)
	}
	if len(line) < len("HTTP/1.1 200") {
		return 0, 0, fmt.Errorf("internal/http1: truncated status line %q", line)
	}
	// Find the space between version and code.
	sp1 := bytes.IndexByte(line, ' ')
	if sp1 < 0 {
		return 0, 0, fmt.Errorf("internal/http1: no space after protocol version in %q", line)
	}
	rest := line[sp1+1:]
	// Code is everything up to the next space (or end of line if there's
	// no reason phrase, which is permitted by RFC 9112).
	sp2 := bytes.IndexByte(rest, ' ')
	var codeBytes []byte
	if sp2 < 0 {
		codeBytes = rest
	} else {
		codeBytes = rest[:sp2]
	}
	if len(codeBytes) != 3 {
		return 0, 0, fmt.Errorf("internal/http1: status code wrong length in %q", line)
	}
	code, perr := strconv.Atoi(string(codeBytes))
	if perr != nil {
		return 0, 0, fmt.Errorf("internal/http1: status code not numeric: %w (line=%q)", perr, line)
	}
	if code < 100 || code > 999 {
		return 0, 0, fmt.Errorf("internal/http1: status code out of range: %d", code)
	}
	return code, end + 2, nil
}

// ParseHeaders consumes header lines starting at `start` within buf, up
// to and including the terminating empty CRLF. Returns the parsed
// headers and the offset where the body begins.
//
// Accepts: "Name: value\r\n" repeated, ending with bare "\r\n".
// Rejects: malformed lines, missing terminator before buf runs out.
//
// Does NOT support obsolete folding (LWS continuation lines). Servers
// that still use folding will get rejected. Anthropic, example.com, and
// any modern server hasn't folded since RFC 7230 (2014); revisit if a
// real target hits this.
func ParseHeaders(buf []byte, start int) (headers []Header, bodyStart int, err error) {
	pos := start
	for {
		if pos >= len(buf) {
			return nil, 0, errors.New("internal/http1: headers truncated before terminator")
		}
		// End of headers — empty line.
		if pos+1 < len(buf) && buf[pos] == '\r' && buf[pos+1] == '\n' {
			return headers, pos + 2, nil
		}
		// Find CRLF for this header line.
		lineEnd := bytes.Index(buf[pos:], []byte("\r\n"))
		if lineEnd < 0 {
			return nil, 0, errors.New("internal/http1: header line missing CRLF")
		}
		line := buf[pos : pos+lineEnd]
		colon := bytes.IndexByte(line, ':')
		if colon < 0 {
			return nil, 0, fmt.Errorf("internal/http1: header line has no colon: %q", line)
		}
		name := string(line[:colon])
		if name == "" || !isValidTokenString(name) {
			return nil, 0, fmt.Errorf("internal/http1: invalid header name %q", name)
		}
		// Trim leading whitespace from value (single space after colon is
		// canonical; some servers send tabs or multiple spaces).
		valStart := colon + 1
		for valStart < len(line) && (line[valStart] == ' ' || line[valStart] == '\t') {
			valStart++
		}
		// Trim trailing whitespace from value.
		valEnd := len(line)
		for valEnd > valStart && (line[valEnd-1] == ' ' || line[valEnd-1] == '\t') {
			valEnd--
		}
		headers = append(headers, Header{Name: name, Value: string(line[valStart:valEnd])})
		pos += lineEnd + 2
	}
}

// ContentLength looks up the Content-Length header in a parsed header
// list. Returns (-1, nil) when absent, (n, nil) when present and valid,
// or (0, err) when present but malformed. Header lookup is
// case-insensitive per RFC 9110.
func ContentLength(headers []Header) (int, error) {
	for _, h := range headers {
		if !equalsIgnoreCase(h.Name, "Content-Length") {
			continue
		}
		n, err := strconv.Atoi(h.Value)
		if err != nil {
			return 0, fmt.Errorf("internal/http1: malformed Content-Length %q: %w", h.Value, err)
		}
		if n < 0 {
			return 0, fmt.Errorf("internal/http1: negative Content-Length %d", n)
		}
		return n, nil
	}
	return -1, nil
}

// isValidTokenString returns true if s is a non-empty RFC 9110 token
// (i.e. only tchar characters). Used for header-name and method
// sanity-checks. We intentionally don't validate header values — they
// can legitimately contain a wide range of characters depending on the
// header.
func isValidTokenString(s string) bool {
	if s == "" {
		return false
	}
	for i := range len(s) {
		if !isTchar(s[i]) {
			return false
		}
	}
	return true
}

func isTchar(c byte) bool {
	switch c {
	case '!', '#', '$', '%', '&', '\'', '*', '+', '-', '.', '^', '_', '`', '|', '~':
		return true
	}
	return (c >= '0' && c <= '9') || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
}

// equalsIgnoreCase is a small ASCII-fold compare. HTTP header names are
// ASCII so this is correct without bringing in unicode.
func equalsIgnoreCase(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range len(a) {
		ca, cb := a[i], b[i]
		if ca >= 'A' && ca <= 'Z' {
			ca += 32
		}
		if cb >= 'A' && cb <= 'Z' {
			cb += 32
		}
		if ca != cb {
			return false
		}
	}
	return true
}
