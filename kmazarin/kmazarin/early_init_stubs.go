//go:build test_stubs

package main

// EarlyInit stub for test builds.
// The real EarlyInit tries to access hardware that doesn't exist on the host.
func EarlyInit() {
	// No-op for tests - hardware initialization not possible on host
}

// unmapCardinal stub for test builds.
// The real unmapCardinal modifies page tables which isn't possible on host.
func unmapCardinal() {
	// No-op for tests
}
