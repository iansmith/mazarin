//go:build linux

package mancini

// GlobalToLocal transforms global screen coordinates to local coordinates
// relative to the interactor's layout origin. Returns the local coordinates
// and whether the point falls inside the interactor's bounds.
func GlobalToLocal(globalX, globalY int64, d Drawer) (localX, localY int64, inside bool) {
	layouter, ok := d.(Layouter)
	if !ok {
		return 0, 0, false
	}
	layout := layouter.GetLayout()
	if layout == nil {
		return 0, 0, false
	}
	ix := layout.X.Get()
	iy := layout.Y.Get()
	iw := layout.Width.Get()
	ih := layout.Height.Get()
	localX = globalX - ix
	localY = globalY - iy
	inside = localX >= 0 && localX < iw && localY >= 0 && localY < ih
	return
}

// ChildAccessor is implemented by decorator interactors that wrap a single child.
type ChildAccessor interface {
	GetChild() Drawer
}

// detailedCheck runs DetailedHit on target if it implements the interface.
// Returns true if the target passes (or doesn't implement it).
func detailedCheck(target Drawer, localX, localY int64) bool {
	if dht, ok := target.(DetailedHit); ok {
		return dht.DetailedHit(localX, localY)
	}
	return true // no detailed test — bounding box is sufficient
}

// MousePolicy dispatches a mouse press to the appropriate interactor.
// It transforms global coordinates to each interactor's local space,
// checks containment via bounding box, then refines with DetailedHit
// if the interactor supports it. Dispatches to the first hit that
// implements MousePressHandler. If the hit interactor is a decorator
// (ChildAccessor), the child is checked for MousePressHandler as well.
func MousePolicy(x, y int64, interactors []Drawer) {
	for _, d := range interactors {
		localX, localY, inside := GlobalToLocal(x, y, d)
		if !inside {
			continue
		}
		// Check the interactor itself first.
		if handler, ok := d.(MousePressHandler); ok {
			if detailedCheck(d, localX, localY) {
				handler.HandleMousePress(localX, localY)
				return
			}
			continue
		}
		// If it's a decorator, check the child.
		if ca, ok := d.(ChildAccessor); ok {
			child := ca.GetChild()
			if child != nil {
				if handler, ok := child.(MousePressHandler); ok {
					childLX, childLY, _ := GlobalToLocal(x, y, child)
					if detailedCheck(child, childLX, childLY) {
						handler.HandleMousePress(childLX, childLY)
						return
					}
				}
			}
		}
	}
}
