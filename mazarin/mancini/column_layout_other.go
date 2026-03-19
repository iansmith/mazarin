//go:build !linux

package mancini

// InitLayout is a no-op on non-linux.
func (c *Column) InitLayout(parent string) {}
