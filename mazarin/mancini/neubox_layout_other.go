//go:build !linux

package mancini

// InitLayout is a no-op on non-linux.
func (n *NeuBox) InitLayout(parent string) {}
