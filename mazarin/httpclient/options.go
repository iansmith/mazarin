package httpclient

import "crypto/x509"

// Option configures an HttpProtocolClient at construction time.
//
// Options are applied left-to-right; later occurrences win.
type Option func(*config)

// config is the internal accumulator of applied options. Unexported on purpose
// — callers compose behavior through Option, not by reaching into a struct.
type config struct {
	rootCAs        *x509.CertPool
	endpointHost   string
	endpointIP     [4]byte
	endpointIPSet  bool
	shepherdName   string
	minTLSVersion  uint16
}

// WithRootCAs supplies the x509 pool the TLS handshake will verify against.
// Populated by the caller from /protocol-http/ssl/cacert.pem in production.
func WithRootCAs(pool *x509.CertPool) Option {
	return func(c *config) { c.rootCAs = pool }
}

// WithEndpointIP pins an IPv4 address for a hostname while DNS (MAZ-41) is
// not yet wired. Calling more than once overrides earlier pins.
func WithEndpointIP(host string, ip4 [4]byte) Option {
	return func(c *config) {
		c.endpointHost = host
		c.endpointIP = ip4
		c.endpointIPSet = true
	}
}

// WithShepherdName overrides the default "protocol-http" registry name. Used
// in tests and for swapping in alternate implementations of the shepherd.
func WithShepherdName(name string) Option {
	return func(c *config) { c.shepherdName = name }
}

// WithMinTLSVersion lower-bounds the TLS version the handshake will accept.
// Defaults to tls.VersionTLS12.
func WithMinTLSVersion(v uint16) Option {
	return func(c *config) { c.minTLSVersion = v }
}
