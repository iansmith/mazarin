package internal

import (
	"crypto/tls"
	"crypto/x509"
	"net"
	"strings"
	"testing"
	"time"
)

const minimalPEM = `-----BEGIN CERTIFICATE-----
MIIBhTCCASugAwIBAgIQIRi6zePL6mKjOipn+dktWjAKBggqhkjOPQQDAjASMRAw
DgYDVQQKEwdBY21lIENvMB4XDTE3MTAyMDE5NDMwNloXDTE4MTAyMDE5NDMwNlow
EjEQMA4GA1UEChMHQWNtZSBDbzBZMBMGByqGSM49AgEGCCqGSM49AwEHA0IABD0d
7VNhbWvZLWPuj/RtHFjvtJBEwOkhbN/BnnE8rnZR8+sbwnc/KhCk3FhnpHZnQz7B
5aETbbIgmuvewdjvSBSjYzBhMA4GA1UdDwEB/wQEAwICpDATBgNVHSUEDDAKBggr
BgEFBQcDATAPBgNVHRMBAf8EBTADAQH/MCkGA1UdEQQiMCCCDmxvY2FsaG9zdDo1
NDUzgg4xMjcuMC4wLjE6NTQ1MzAKBggqhkjOPQQDAgNIADBFAiEA2zpJEPQyz6/l
Wf86aX6PepsntZv2GYlA5UpabfT2EZICICpJ5h/iI+i341gBmLiAFQOyTDT+/wQc
6MF9+Yw1Yy0t
-----END CERTIFICATE-----`

func TestTLSConfig_DefaultsAndPins(t *testing.T) {
	pool := x509.NewCertPool()
	cfg, err := TLSConfig("api.anthropic.com", pool, 0)
	if err != nil {
		t.Fatalf("TLSConfig: %v", err)
	}
	if cfg.ServerName != "api.anthropic.com" {
		t.Fatalf("ServerName: got %q, want api.anthropic.com", cfg.ServerName)
	}
	if cfg.RootCAs != pool {
		t.Fatal("RootCAs not propagated")
	}
	if cfg.MinVersion != tls.VersionTLS12 {
		t.Fatalf("MinVersion default: got 0x%x, want 0x%x", cfg.MinVersion, tls.VersionTLS12)
	}
	if len(cfg.NextProtos) != 1 || cfg.NextProtos[0] != "http/1.1" {
		t.Fatalf("NextProtos: got %v, want [http/1.1]", cfg.NextProtos)
	}
}

func TestTLSConfig_ExplicitMinVersionOverride(t *testing.T) {
	pool := x509.NewCertPool()
	cfg, err := TLSConfig("h", pool, tls.VersionTLS13)
	if err != nil {
		t.Fatalf("TLSConfig: %v", err)
	}
	if cfg.MinVersion != tls.VersionTLS13 {
		t.Fatalf("MinVersion: got 0x%x, want 0x%x (TLS1.3)", cfg.MinVersion, tls.VersionTLS13)
	}
}

func TestTLSConfig_RejectsEmptyHost(t *testing.T) {
	pool := x509.NewCertPool()
	if _, err := TLSConfig("", pool, 0); err == nil {
		t.Fatal("TLSConfig with empty host: expected err, got nil")
	}
}

func TestTLSConfig_RejectsNilPool(t *testing.T) {
	if _, err := TLSConfig("h", nil, 0); err == nil {
		t.Fatal("TLSConfig with nil pool: expected err, got nil")
	}
}

func TestDialTLS_RejectsNilConn(t *testing.T) {
	cfg := &tls.Config{ServerName: "h", RootCAs: x509.NewCertPool()}
	if _, err := DialTLS(nil, cfg); err == nil {
		t.Fatal("DialTLS(nil conn): expected err, got nil")
	}
}

func TestDialTLS_RejectsNilConfig(t *testing.T) {
	// A nil net.Conn pointer wrapped in interface is rejected first;
	// nil cfg is the assertion here.
	if _, err := DialTLS(&fakeConn{}, nil); err == nil {
		t.Fatal("DialTLS(nil cfg): expected err, got nil")
	}
}

func TestLoadCAPool_RejectsEmpty(t *testing.T) {
	if _, err := LoadCAPool(nil); err == nil {
		t.Fatal("LoadCAPool(nil): expected err, got nil")
	}
}

func TestLoadCAPool_RejectsGarbage(t *testing.T) {
	if _, err := LoadCAPool([]byte("not a cert")); err == nil {
		t.Fatal("LoadCAPool(garbage): expected err, got nil")
	}
}

func TestLoadCAPool_ParsesValidPEM(t *testing.T) {
	pool, err := LoadCAPool([]byte(minimalPEM))
	if err != nil {
		t.Fatalf("LoadCAPool: %v", err)
	}
	if pool == nil {
		t.Fatal("LoadCAPool: nil pool")
	}
}

func TestLoadCAPool_RejectsTrailingGarbageOnly(t *testing.T) {
	// Trailing garbage AFTER a valid cert is tolerated by AppendCertsFromPEM
	// (the valid cert was parsed); but garbage-only input fails. The
	// assertion: a PEM block header that doesn't decode counts as garbage.
	bad := strings.Repeat("-----BEGIN GARBAGE-----\nzzzz\n-----END GARBAGE-----\n", 3)
	if _, err := LoadCAPool([]byte(bad)); err == nil {
		t.Fatal("LoadCAPool with only non-cert PEM blocks: expected err, got nil")
	}
}

// fakeConn is a no-op net.Conn used to exercise DialTLS's argument
// validation. Methods panic if called — we only need the type to satisfy
// the interface for the nil-cfg test.
type fakeConn struct{}

func (fakeConn) Read([]byte) (int, error)        { panic("unused") }
func (fakeConn) Write([]byte) (int, error)       { panic("unused") }
func (fakeConn) Close() error                    { panic("unused") }
func (fakeConn) LocalAddr() net.Addr             { return &net.IPAddr{IP: net.IPv4zero} }
func (fakeConn) RemoteAddr() net.Addr            { return &net.IPAddr{IP: net.IPv4zero} }
func (fakeConn) SetDeadline(time.Time) error     { return nil }
func (fakeConn) SetReadDeadline(time.Time) error { return nil }
func (fakeConn) SetWriteDeadline(time.Time) error { return nil }
