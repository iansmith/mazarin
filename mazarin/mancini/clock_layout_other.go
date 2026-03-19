//go:build !linux

package mancini

// InitLayout is a no-op on non-linux.
func (c *Clock) InitLayout(parent string) {}
