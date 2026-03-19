//go:build !linux

package mancini

// DetailedHit is a no-op on non-linux.
func (c *Clock) DetailedHit(localX, localY int64) bool { return false }

// HandleMousePress is a no-op on non-linux.
func (c *Clock) HandleMousePress(x, y int64) {}
