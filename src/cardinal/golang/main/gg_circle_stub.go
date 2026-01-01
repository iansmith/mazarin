//go:build !qemuvirt || !aarch64

package main

// drawGGStartupCircle is a no-op on non-QEMU builds.
func drawGGStartupCircle() {
	// Intentionally empty.
}
