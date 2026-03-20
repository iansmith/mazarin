//go:build !linux

package mancini

// DetailedHit is a no-op on non-linux.
func (c *Clock) DetailedHit(localX, localY int64) bool { return false }

// Feedback is a no-op on non-linux.
func (c *Clock) Feedback(active bool) {}

// Complete is a no-op on non-linux.
func (c *Clock) Complete(success bool) {}
