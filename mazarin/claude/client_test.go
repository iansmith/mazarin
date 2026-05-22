// Phase 0 RED tests for MAZ-43 — these describe the expected post-fix
// behavior of mazarin/claude. They will not compile until the package's
// public API exists, and they will not pass until the implementation
// does the right thing. That's the RED state.
//
// Internal-package test (`package claude`) for access to internal
// encoders/decoders that are testable in isolation but don't need to
// be part of the public surface.
package claude

import (
	"crypto/tls"
	"crypto/x509"
	"strings"
	"testing"
)

// TestNewClientRejectsEmptyAPIKey — NewClient must validate that the
// API key is non-empty. An empty key would silently produce 401
// responses at request time; failing fast at construction time is the
// right shape.
func TestNewClientRejectsEmptyAPIKey(t *testing.T) {
	pool := x509.NewCertPool()
	_, err := NewClient("", "claude-opus-4-5", pool)
	if err == nil {
		t.Fatal("NewClient should reject empty API key, got nil error")
	}
}

// TestNewClientRejectsNilCAPool — NewClient must reject a nil CA pool.
// Without a trust store, TLS handshake against api.anthropic.com would
// fail with x509: certificate signed by unknown authority. Surfacing
// that as a construction error beats a runtime handshake error far
// from the misconfiguration.
func TestNewClientRejectsNilCAPool(t *testing.T) {
	_, err := NewClient("sk-ant-test", "claude-opus-4-5", nil)
	if err == nil {
		t.Fatal("NewClient should reject nil CA pool, got nil error")
	}
}

// TestNewClientBuildsTLSConfig — the returned Client must expose a
// *tls.Config (via a TLSConfig accessor) with the four fields the
// ticket pinned down: ServerName=api.anthropic.com, MinVersion>=TLS1.2,
// non-nil RootCAs, NextProtos=[http/1.1] (ALPN pins HTTP/1.1).
func TestNewClientBuildsTLSConfig(t *testing.T) {
	pool := x509.NewCertPool()
	c, err := NewClient("sk-ant-test", "claude-opus-4-5", pool)
	if err != nil {
		t.Fatalf("NewClient returned err: %v", err)
	}
	cfg := c.TLSConfig()
	if cfg == nil {
		t.Fatal("TLSConfig() returned nil")
	}
	if cfg.ServerName != "api.anthropic.com" {
		t.Errorf("ServerName = %q, want %q", cfg.ServerName, "api.anthropic.com")
	}
	if cfg.MinVersion < tls.VersionTLS12 {
		t.Errorf("MinVersion = %d, want >= tls.VersionTLS12 (%d)", cfg.MinVersion, tls.VersionTLS12)
	}
	if cfg.RootCAs == nil {
		t.Error("RootCAs is nil, expected the pool passed to NewClient")
	}
	if len(cfg.NextProtos) == 0 || cfg.NextProtos[0] != "http/1.1" {
		t.Errorf("NextProtos = %v, want [\"http/1.1\"] (ALPN-pin HTTP/1.1)", cfg.NextProtos)
	}
}

// TestEncodeMessagesRequest — the internal request encoder must produce
// a JSON body matching the Anthropic Messages API shape (model,
// max_tokens, messages array with {role, content}). Asserts via
// substring rather than byte-equal so the encoder can choose key order
// freely.
func TestEncodeMessagesRequest(t *testing.T) {
	body, err := encodeMessagesRequest("claude-opus-4-5", "What is the capital?", 1024)
	if err != nil {
		t.Fatalf("encodeMessagesRequest err: %v", err)
	}
	s := string(body)
	for _, want := range []string{
		`"model":"claude-opus-4-5"`,
		`"max_tokens":1024`,
		`"role":"user"`,
		`"content":"What is the capital?"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("encoded body missing %s\ngot: %s", want, s)
		}
	}
}

// TestParseMessagesResponse — the internal response parser must extract
// content[0].text from a well-formed 200 Messages API response.
func TestParseMessagesResponse(t *testing.T) {
	canned := []byte(`{
		"id": "msg_test",
		"type": "message",
		"role": "assistant",
		"content": [{"type": "text", "text": "Washington's capital is Olympia."}]
	}`)
	text, err := parseMessagesResponse(canned)
	if err != nil {
		t.Fatalf("parseMessagesResponse err: %v", err)
	}
	if !strings.Contains(text, "Washington") {
		t.Errorf("parsed text doesn't contain Washington: %q", text)
	}
}

// TestParseMessagesResponseRejectsAPIError — API error responses (4xx,
// 5xx) have a different shape ({"type": "error", "error": {...}}). The
// parser must surface them as errors rather than silently returning
// empty text — otherwise sophie's pass condition (substring "Washington")
// would say FAIL on a 401 and nobody'd know whether it was a network
// problem or an auth problem.
func TestParseMessagesResponseRejectsAPIError(t *testing.T) {
	apiError := []byte(`{"type": "error", "error": {"type": "authentication_error", "message": "invalid x-api-key"}}`)
	_, err := parseMessagesResponse(apiError)
	if err == nil {
		t.Fatal("parseMessagesResponse should reject API error responses, got nil")
	}
}
