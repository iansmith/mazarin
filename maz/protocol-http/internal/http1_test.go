package internal

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestBuildRequest_BasicGET(t *testing.T) {
	dst := make([]byte, 512)
	n, err := BuildRequest(dst, "GET", "/v1/messages", "api.anthropic.com", nil)
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}
	want := "GET /v1/messages HTTP/1.1\r\nHost: api.anthropic.com\r\n\r\n"
	if string(dst[:n]) != want {
		t.Fatalf("output:\n  got  %q\n  want %q", dst[:n], want)
	}
}

func TestBuildRequest_WithHeaders(t *testing.T) {
	dst := make([]byte, 512)
	hdrs := []Header{
		{Name: "Content-Type", Value: "application/json"},
		{Name: "Content-Length", Value: "42"},
		{Name: "x-api-key", Value: "sk-test"},
	}
	n, err := BuildRequest(dst, "POST", "/v1/messages", "api.anthropic.com", hdrs)
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}
	got := string(dst[:n])
	for _, must := range []string{
		"POST /v1/messages HTTP/1.1\r\n",
		"Host: api.anthropic.com\r\n",
		"Content-Type: application/json\r\n",
		"Content-Length: 42\r\n",
		"x-api-key: sk-test\r\n",
		"\r\n\r\n", // last header line + blank line
	} {
		if !strings.Contains(got, must) {
			t.Fatalf("output missing %q:\n  got %q", must, got)
		}
	}
}

func TestBuildRequest_RejectsEmptyMethod(t *testing.T) {
	dst := make([]byte, 512)
	if _, err := BuildRequest(dst, "", "/", "h", nil); err == nil {
		t.Fatal("BuildRequest with empty method: expected err")
	}
}

func TestBuildRequest_RejectsEmptyPath(t *testing.T) {
	dst := make([]byte, 512)
	if _, err := BuildRequest(dst, "GET", "", "h", nil); err == nil {
		t.Fatal("BuildRequest with empty path: expected err")
	}
}

func TestBuildRequest_RejectsEmptyHost(t *testing.T) {
	dst := make([]byte, 512)
	if _, err := BuildRequest(dst, "GET", "/", "", nil); err == nil {
		t.Fatal("BuildRequest with empty host: expected err")
	}
}

func TestBuildRequest_RejectsInvalidHeaderName(t *testing.T) {
	dst := make([]byte, 512)
	bad := []Header{{Name: "Bad Name", Value: "x"}} // space is not a tchar
	if _, err := BuildRequest(dst, "GET", "/", "h", bad); err == nil {
		t.Fatal("BuildRequest with invalid header name: expected err")
	}
}

func TestBuildRequest_DstTooSmall(t *testing.T) {
	dst := make([]byte, 20) // too small
	_, err := BuildRequest(dst, "GET", "/v1/messages", "api.anthropic.com", nil)
	if !errors.Is(err, ErrHeaderOverflow) {
		t.Fatalf("BuildRequest dst too small: got err=%v, want ErrHeaderOverflow", err)
	}
}

func TestParseStatusLine_OK200(t *testing.T) {
	in := []byte("HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\n\r\nbody")
	code, next, err := ParseStatusLine(in)
	if err != nil {
		t.Fatalf("ParseStatusLine: %v", err)
	}
	if code != 200 {
		t.Fatalf("code: got %d, want 200", code)
	}
	if !bytes.HasPrefix(in[next:], []byte("Content-Type: text/plain\r\n")) {
		t.Fatalf("next offset wrong: tail begins with %q", in[next:])
	}
}

func TestParseStatusLine_NoReasonPhrase(t *testing.T) {
	in := []byte("HTTP/1.1 204\r\n\r\n")
	code, _, err := ParseStatusLine(in)
	if err != nil {
		t.Fatalf("ParseStatusLine no reason phrase: %v", err)
	}
	if code != 204 {
		t.Fatalf("code: got %d, want 204", code)
	}
}

func TestParseStatusLine_Errors(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"no CRLF", "HTTP/1.1 200 OK"},
		{"bad protocol", "FTP/1.1 200 OK\r\n"},
		{"truncated", "HTTP/1.1\r\n"},
		{"non-numeric code", "HTTP/1.1 XXX OK\r\n"},
		{"out of range", "HTTP/1.1 099 Foo\r\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, _, err := ParseStatusLine([]byte(c.in)); err == nil {
				t.Fatalf("expected err for %q", c.in)
			}
		})
	}
}

func TestParseHeaders_ParsesSimple(t *testing.T) {
	in := []byte("HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\nContent-Length: 5\r\n\r\nhello")
	_, after, err := ParseStatusLine(in)
	if err != nil {
		t.Fatalf("ParseStatusLine: %v", err)
	}
	hdrs, bodyStart, err := ParseHeaders(in, after)
	if err != nil {
		t.Fatalf("ParseHeaders: %v", err)
	}
	if len(hdrs) != 2 {
		t.Fatalf("header count: got %d, want 2", len(hdrs))
	}
	if hdrs[0].Name != "Content-Type" || hdrs[0].Value != "text/plain" {
		t.Fatalf("hdr[0]: got %+v, want Content-Type: text/plain", hdrs[0])
	}
	if hdrs[1].Name != "Content-Length" || hdrs[1].Value != "5" {
		t.Fatalf("hdr[1]: got %+v, want Content-Length: 5", hdrs[1])
	}
	if string(in[bodyStart:]) != "hello" {
		t.Fatalf("body: got %q, want hello", in[bodyStart:])
	}
}

func TestParseHeaders_TrimsLeadingTrailingWhitespace(t *testing.T) {
	in := []byte("HTTP/1.1 200 OK\r\nX-Test:    value   \t\r\n\r\n")
	_, after, _ := ParseStatusLine(in)
	hdrs, _, err := ParseHeaders(in, after)
	if err != nil {
		t.Fatalf("ParseHeaders: %v", err)
	}
	if hdrs[0].Value != "value" {
		t.Fatalf("trimmed value: got %q, want %q", hdrs[0].Value, "value")
	}
}

func TestParseHeaders_NoHeaders(t *testing.T) {
	in := []byte("HTTP/1.1 200 OK\r\n\r\nbody")
	_, after, _ := ParseStatusLine(in)
	hdrs, bodyStart, err := ParseHeaders(in, after)
	if err != nil {
		t.Fatalf("ParseHeaders: %v", err)
	}
	if len(hdrs) != 0 {
		t.Fatalf("header count: got %d, want 0", len(hdrs))
	}
	if string(in[bodyStart:]) != "body" {
		t.Fatalf("body: got %q, want body", in[bodyStart:])
	}
}

func TestParseHeaders_Errors(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"truncated (no terminator)", "HTTP/1.1 200 OK\r\nX: y\r\n"},
		{"line missing colon", "HTTP/1.1 200 OK\r\nNoColonHere\r\n\r\n"},
		{"empty name", "HTTP/1.1 200 OK\r\n: noname\r\n\r\n"},
		{"invalid name char", "HTTP/1.1 200 OK\r\nBad Name: x\r\n\r\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := []byte(c.in)
			_, after, _ := ParseStatusLine(in)
			if _, _, err := ParseHeaders(in, after); err == nil {
				t.Fatalf("expected err for %q", c.in)
			}
		})
	}
}

func TestContentLength_Present(t *testing.T) {
	n, err := ContentLength([]Header{
		{Name: "Foo", Value: "bar"},
		{Name: "content-length", Value: "1234"},
	})
	if err != nil {
		t.Fatalf("ContentLength: %v", err)
	}
	if n != 1234 {
		t.Fatalf("n: got %d, want 1234", n)
	}
}

func TestContentLength_Absent(t *testing.T) {
	n, err := ContentLength([]Header{{Name: "X", Value: "y"}})
	if err != nil {
		t.Fatalf("ContentLength absent: %v", err)
	}
	if n != -1 {
		t.Fatalf("absent: got %d, want -1", n)
	}
}

func TestContentLength_Malformed(t *testing.T) {
	_, err := ContentLength([]Header{{Name: "Content-Length", Value: "abc"}})
	if err == nil {
		t.Fatal("malformed CL: expected err")
	}
}

func TestContentLength_Negative(t *testing.T) {
	_, err := ContentLength([]Header{{Name: "Content-Length", Value: "-5"}})
	if err == nil {
		t.Fatal("negative CL: expected err")
	}
}

func TestMethodString(t *testing.T) {
	cases := []struct {
		m    uint16
		want string
	}{
		{1, "GET"}, {2, "POST"}, {3, "PUT"},
		{4, "DELETE"}, {5, "HEAD"}, {6, "PATCH"},
		{0, ""}, {99, ""}, // invalid
	}
	for _, c := range cases {
		if got := MethodString(c.m); got != c.want {
			t.Errorf("MethodString(%d): got %q, want %q", c.m, got, c.want)
		}
	}
}
