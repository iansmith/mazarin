package httpclient

// newConfigForTest applies the supplied options against a zero config and
// returns the result. Exported only to *_test.go files in the same package
// so the red tests can inspect option behavior without a real client.
//
// The implementation in MAZ-49 will populate the defaults inside New —
// this helper mirrors that future code path so the tests describe the
// finished behavior, not the stub.
func newConfigForTest(opts ...Option) *config {
	c := defaultConfigForTest()
	for _, o := range opts {
		o(c)
	}
	return c
}

// defaultConfigForTest is the seed New() will use once implemented. Kept
// in the test-only file so it can be evolved without touching the
// production constructor while red tests are pinning the contract.
func defaultConfigForTest() *config {
	return &config{
		shepherdName:  "protocol-http",
		minTLSVersion: 0x0303, // tls.VersionTLS12
	}
}
