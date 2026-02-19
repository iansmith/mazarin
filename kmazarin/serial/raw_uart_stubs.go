//go:build test_stubs

package serial

// RawUART is a no-op in test builds.
//
//go:nosplit
func RawUART(b byte) {}

// PollWrite is a no-op in test builds.
//
//go:nosplit
func PollWrite(b byte) {}

// RxReady always returns false in test builds.
//
//go:nosplit
func RxReady() bool { return false }

// ReadRxByte returns 0 in test builds.
//
//go:nosplit
func ReadRxByte() byte { return 0 }

// EnableRxInterrupt is a no-op in test builds.
//
//go:nosplit
func EnableRxInterrupt() {}
