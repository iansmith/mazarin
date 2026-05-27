package httpclient

import "errors"

// errUnimplemented is the placeholder return for the planned surface. The
// red tests in *_test.go assert against it; implementation in MAZ-49
// replaces these stubs.
var errUnimplemented = errors.New("httpclient: not yet implemented (MAZ-49)")

// New constructs an HttpProtocolClient backed by the protocol-http shepherd.
//
// Stubbed. Real implementation lands as part of MAZ-49 work item 3.
func New(opts ...Option) (HttpProtocolClient, error) {
	return nil, errUnimplemented
}
