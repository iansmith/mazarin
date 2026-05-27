// Package httpclient tests for MAZ-49.
//
// These tests exercise the real implementation of New and Do's argument
// validation paths that don't require live transfer.Slab pages (which
// in turn require the mazzy kernel — host darwin can't service the
// underlying AllocPages syscall). The Slab-bounds branches of validate
// are exercised by the boot integration in MAZ-49 item 8.
package httpclient

import (
	"crypto/tls"
	"crypto/x509"
	"strings"
	"testing"
)

// newClient is a convenience that builds a client with the minimum
// required options.
func newClient(t *testing.T) HttpProtocolClient {
	t.Helper()
	c, err := New(
		WithRootCAs(x509.NewCertPool()),
		WithEndpointIP("api.anthropic.com", [4]byte{1, 2, 3, 4}),
	)
	if err != nil {
		t.Fatalf("newClient: %v", err)
	}
	return c
}

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

func TestNew_RejectsMissingRootCAs(t *testing.T) {
	_, err := New(
		WithEndpointIP("api.anthropic.com", [4]byte{1, 2, 3, 4}),
	)
	if err == nil {
		t.Fatal("expected error when WithRootCAs omitted, got nil")
	}
	if !strings.Contains(err.Error(), "WithRootCAs") {
		t.Fatalf("err should mention WithRootCAs, got: %v", err)
	}
}

func TestNew_RejectsMissingEndpointIP(t *testing.T) {
	_, err := New(
		WithRootCAs(x509.NewCertPool()),
	)
	if err == nil {
		t.Fatal("expected error when WithEndpointIP omitted, got nil")
	}
}

func TestNew_RejectsEmptyShepherdName(t *testing.T) {
	_, err := New(
		WithRootCAs(x509.NewCertPool()),
		WithEndpointIP("h", [4]byte{1, 2, 3, 4}),
		WithShepherdName(""),
	)
	if err == nil {
		t.Fatal("expected error on WithShepherdName(''), got nil")
	}
}

func TestDo_RejectsNilRequest(t *testing.T) {
	c := newClient(t)
	_, err := c.Do(nil, nil)
	if err == nil {
		t.Fatal("expected error on nil request, got nil")
	}
}

func TestDo_RejectsEmptyMethod(t *testing.T) {
	c := newClient(t)
	req := &Request{Method: "", URL: "https://h/x"}
	_, err := c.Do(req, nil)
	if err == nil {
		t.Fatal("expected error on empty method, got nil")
	}
}

func TestDo_RejectsEmptyURL(t *testing.T) {
	c := newClient(t)
	req := &Request{Method: "GET", URL: ""}
	_, err := c.Do(req, nil)
	if err == nil {
		t.Fatal("expected error on empty URL, got nil")
	}
}

func TestDo_RejectsNilBody(t *testing.T) {
	c := newClient(t)
	req := &Request{Method: "GET", URL: "https://h/x", Body: nil}
	_, err := c.Do(req, nil)
	if err == nil {
		t.Fatal("expected error on nil body Slab, got nil")
	}
}

// Tests for HeadersMax/BodyLen out-of-range, nil respDest with non-nil
// body, and the post-validation "not yet wired" sentinel all require
// real transfer.Slab pages. They're covered by the boot integration
// in MAZ-49 item 8.

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
