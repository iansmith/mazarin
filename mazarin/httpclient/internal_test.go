package httpclient

import "crypto/tls"

// newConfigForTest applies the supplied options against a zero config and
// returns the result. Exported only to *_test.go files in the same package
// so the option-validation tests can inspect WithXxx behavior without
// constructing a real client.
func newConfigForTest(opts ...Option) *config {
	c := defaultConfigForTest()
	for _, o := range opts {
		o(c)
	}
	return c
}

// defaultConfigForTest mirrors the seed New() uses. Defined via the
// production constants (defaultShepherdName, tls.VersionTLS12) rather
// than literal copies so the test helper can't drift from production
// defaults — if either changes, both sides move together.
func defaultConfigForTest() *config {
	return &config{
		shepherdName:  defaultShepherdName,
		minTLSVersion: tls.VersionTLS12,
	}
}
