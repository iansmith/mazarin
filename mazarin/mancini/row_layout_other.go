//go:build !linux

package mancini

// InitLayout is a no-op on non-linux.
func (r *Row) InitLayout(parent string) {}
