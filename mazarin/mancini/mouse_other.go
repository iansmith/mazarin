//go:build !linux

package mancini

// GlobalToLocal is a no-op on non-linux.
func GlobalToLocal(globalX, globalY int64, d Drawer) (int64, int64, bool) {
	return 0, 0, false
}

// MouseState is a stub on non-linux.
type MouseState struct{}

// NewMouseState is a no-op on non-linux.
func NewMouseState(interactors []Drawer) *MouseState { return &MouseState{} }

// Press is a no-op on non-linux.
func (m *MouseState) Press(x, y int64) {}

// Move is a no-op on non-linux.
func (m *MouseState) Move(x, y int64) {}

// Release is a no-op on non-linux.
func (m *MouseState) Release(x, y int64) {}
