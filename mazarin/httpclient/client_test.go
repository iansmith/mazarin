// Package httpclient red tests for MAZ-49.
//
// These tests describe the expected post-implementation behavior of the
// mazarin/httpclient public surface. They fail on current code (which is
// stubbed) and should turn green incrementally as MAZ-49 work items land.
package httpclient

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"testing"

	"mazzy/mazarin/transfer"
)

// TestNew_ReturnsClientWhenRequiredOptionsProvided asserts that New, given a
// CA pool and an endpoint pin, returns a non-nil HttpProtocolClient.
//
// Currently FAILS: New is stubbed to return errUnimplemented.
func TestNew_ReturnsClientWhenRequiredOptionsProvided(t *testing.T) {
	pool := x509.NewCertPool()
	c, err := New(
		WithRootCAs(pool),
		WithEndpointIP("api.anthropic.com", [4]byte{1, 2, 3, 4}),
	)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if c == nil {
		t.Fatal("New returned nil client")
	}
}

// TestNew_RejectsMissingRootCAs asserts that omitting WithRootCAs fails
// construction rather than producing a client that would skip verification.
//
// Currently FAILS: New is stubbed; the validation branch doesn't exist yet.
func TestNew_RejectsMissingRootCAs(t *testing.T) {
	_, err := New(
		WithEndpointIP("api.anthropic.com", [4]byte{1, 2, 3, 4}),
	)
	if err == nil {
		t.Fatal("expected error when WithRootCAs omitted, got nil")
	}
}

// TestDo_RejectsNilRequest asserts that Do bails cleanly on a nil request
// rather than panicking deep in the IPC layer.
//
// Currently FAILS: there's no client to call Do on.
func TestDo_RejectsNilRequest(t *testing.T) {
	pool := x509.NewCertPool()
	c, err := New(
		WithRootCAs(pool),
		WithEndpointIP("h", [4]byte{1, 2, 3, 4}),
	)
	if err != nil {
		t.Skipf("New failed earlier in red-test sequence: %v", err)
	}
	_, err = c.Do(nil, transfer.Handle{})
	if err == nil {
		t.Fatal("expected error on nil request, got nil")
	}
}

// TestDo_RejectsEmptyRespDest asserts Do refuses a zero-page respDest, since
// protocol-http will not allocate response Slabs itself.
//
// Currently FAILS: client doesn't exist.
func TestDo_RejectsEmptyRespDest(t *testing.T) {
	pool := x509.NewCertPool()
	c, err := New(
		WithRootCAs(pool),
		WithEndpointIP("h", [4]byte{1, 2, 3, 4}),
	)
	if err != nil {
		t.Skipf("New failed earlier in red-test sequence: %v", err)
	}
	req := &Request{Method: "POST", URL: "https://h/x"}
	_, err = c.Do(req, transfer.Handle{VA: 0, Pages: 0})
	if err == nil {
		t.Fatal("expected error on zero-page respDest, got nil")
	}
}

// TestWithMinTLSVersion_DefaultsToTLS12 asserts that without explicit
// WithMinTLSVersion the client clamps to TLS 1.2 — the conservative default
// documented in the ticket.
//
// Currently FAILS: there's no way to inspect the applied config yet. The
// implementation in MAZ-49 will expose this via an internal accessor that
// this test uses (the accessor lives in the same package so it stays
// unexported).
func TestWithMinTLSVersion_DefaultsToTLS12(t *testing.T) {
	cfg := newConfigForTest(
		WithRootCAs(x509.NewCertPool()),
		WithEndpointIP("h", [4]byte{1, 2, 3, 4}),
	)
	if cfg.minTLSVersion != tls.VersionTLS12 {
		t.Fatalf("default minTLSVersion = 0x%x, want 0x%x (TLS1.2)",
			cfg.minTLSVersion, tls.VersionTLS12)
	}
}

// TestWithMinTLSVersion_Overrides asserts the option actually takes effect.
//
// Currently FAILS: same reason as above.
func TestWithMinTLSVersion_Overrides(t *testing.T) {
	cfg := newConfigForTest(
		WithRootCAs(x509.NewCertPool()),
		WithEndpointIP("h", [4]byte{1, 2, 3, 4}),
		WithMinTLSVersion(tls.VersionTLS13),
	)
	if cfg.minTLSVersion != tls.VersionTLS13 {
		t.Fatalf("minTLSVersion after override = 0x%x, want 0x%x (TLS1.3)",
			cfg.minTLSVersion, tls.VersionTLS13)
	}
}

// TestErrUnimplementedIsStable is a sanity check that the current stub
// surfaces a well-known sentinel error so callers driving the red phase
// know exactly what they're seeing. Replaced when implementation lands.
func TestErrUnimplementedIsStable(t *testing.T) {
	_, err := New()
	if !errors.Is(err, errUnimplemented) {
		t.Fatalf("expected errUnimplemented during RED phase, got %v", err)
	}
}
