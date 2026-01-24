//go:build test_stubs

package console

// breadcrumb is a no-op stub for test builds.
// In real builds, this writes a byte directly to UART hardware.
//
//go:nosplit
func breadcrumb(b byte) {
	// No-op in test builds - no hardware to access
}
