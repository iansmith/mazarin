//go:build test_stubs

package main

// uartTopHalf is a no-op in test builds.
//
//go:nosplit
func uartTopHalf(_ uint32) {}
