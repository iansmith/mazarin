//go:build test_stubs

package main

// ForceSerialCharacter is a no-op in test builds.
//
//go:nosplit
func ForceSerialCharacter(b byte) {}
