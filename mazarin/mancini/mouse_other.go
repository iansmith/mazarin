//go:build !linux

package mancini

// GlobalToLocal is a no-op on non-linux.
func GlobalToLocal(globalX, globalY int64, d Drawer) (int64, int64, bool) {
	return 0, 0, false
}

// MousePolicy is a no-op on non-linux.
func MousePolicy(x, y int64, interactors []Drawer) {}
