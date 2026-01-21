//go:build !arm64

package ds

import "testing"

// TestSpinlockStub is a placeholder test for non-ARM64 architectures.
// The spinlock implementation uses ARM64-specific assembly and only
// runs on ARM64 systems. On other architectures, this nil test ensures
// the package can still be built and tested without errors.
func TestSpinlockStub(t *testing.T) {
	t.Skip("spinlock tests only run on arm64 architecture")
}
