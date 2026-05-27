// tls.go — TLS handshake glue for protocol-http. Builds a *tls.Config
// from a CA pool + host + min-version, wraps a Conn (netconn-over-
// netclient) with tls.Client, and runs the explicit handshake.
//
// Lifted from mazarin/claude/client.go's NewClient/TLSConfig/Ask
// fragments in MAZ-49 item 4. Kept tiny on purpose: the only knobs
// protocol-http v1 needs are SNI host, the trust pool, the minimum
// negotiated version, and ALPN pinning to http/1.1. Anything richer
// (client certs, session resumption, OCSP stapling, etc.) lands in a
// future ticket if needed.
package internal

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
)

// TLSConfig builds a *tls.Config for a single endpoint. host is used
// both as the SNI ServerName and for cert-chain verification against
// the supplied root pool. minVersion clamps the handshake; if zero,
// defaults to tls.VersionTLS12.
//
// ALPN is hard-pinned to http/1.1. Without this the remote may
// happily negotiate HTTP/2, but protocol-http v1 only parses HTTP/1.1.
func TLSConfig(host string, roots *x509.CertPool, minVersion uint16) (*tls.Config, error) {
	if host == "" {
		return nil, errors.New("internal/tls: empty host")
	}
	if roots == nil {
		return nil, errors.New("internal/tls: nil CA pool")
	}
	if minVersion == 0 {
		minVersion = tls.VersionTLS12
	}
	return &tls.Config{
		ServerName: host,
		RootCAs:    roots,
		MinVersion: minVersion,
		NextProtos: []string{"http/1.1"},
	}, nil
}

// DialTLS wraps a transport-level net.Conn with tls.Client using cfg,
// runs the explicit handshake, and returns the resulting *tls.Conn.
// On handshake failure the underlying conn is left as-is — the caller
// owns its lifecycle.
//
// Doing Handshake explicitly (rather than letting it fire lazily on
// the first Write) keeps handshake errors from getting tangled with
// application-protocol I/O later.
func DialTLS(conn net.Conn, cfg *tls.Config) (*tls.Conn, error) {
	if conn == nil {
		return nil, errors.New("internal/tls: nil conn")
	}
	if cfg == nil {
		return nil, errors.New("internal/tls: nil config")
	}
	tlsConn := tls.Client(conn, cfg)
	if err := tlsConn.Handshake(); err != nil {
		return nil, fmt.Errorf("internal/tls: handshake: %w", err)
	}
	return tlsConn, nil
}

// LoadCAPool parses a PEM-encoded byte slice into a fresh *x509.CertPool.
// Returns an error if no usable cert was parsed (a common failure mode
// for corrupted or empty bundle files — surface it early rather than
// failing the first TLS handshake with a cryptic chain-verify error).
func LoadCAPool(pem []byte) (*x509.CertPool, error) {
	if len(pem) == 0 {
		return nil, errors.New("internal/tls: empty CA PEM input")
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, errors.New("internal/tls: no usable CA certs parsed from PEM")
	}
	return pool, nil
}
