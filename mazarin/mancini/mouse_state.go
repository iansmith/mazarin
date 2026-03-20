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

// hitTest checks whether (x, y) in global coordinates hits target,
// using bounding box and optional DetailedHit.
func hitTest(x, y int64, target Drawer) (localX, localY int64, inside bool) {
	localX, localY, inside = GlobalToLocal(x, y, target)
	if !inside {
		return localX, localY, false
	}
	if !detailedCheck(target, localX, localY) {
		return localX, localY, false
	}
	return localX, localY, true
}

// MouseState tracks the press-drag-release state machine for mouse
// interactions. Create one with NewMouseState and call Press/Move/Release
// from the event loop.
type MouseState struct {
	interactors []Drawer
	tracker     StdMouseFeedback // active interactor (nil when idle)
	target      Drawer       // the Drawer used for hit testing
	armed       bool         // true = active/previewing, false = inactive
}

// NewMouseState creates a MouseState for the given set of interactors.
func NewMouseState(interactors []Drawer) *MouseState {
	return &MouseState{interactors: interactors}
}

// SetTargets replaces the set of interactors that receive mouse events.
func (m *MouseState) SetTargets(interactors []Drawer) {
	m.interactors = interactors
}

// Press handles a mouse press at global coordinates (x, y).
// Hit-tests each interactor and begins an interaction if one is found.
func (m *MouseState) Press(x, y int64) {
	// If there's a stale interaction (e.g., lost release), force-cancel it.
	if m.tracker != nil {
		m.tracker.Complete(false)
		m.tracker = nil
		m.target = nil
		m.armed = false
	}

	for _, d := range m.interactors {
		_, _, inside := hitTest(x, y, d)
		if !inside {
			continue
		}
		// Check the interactor itself.
		if tracker, ok := d.(StdMouseFeedback); ok {
			m.tracker = tracker
			m.target = d
			m.armed = true
			tracker.Feedback(true)
			return
		}
		// If it's a decorator, check the child.
		if ca, ok := d.(ChildAccessor); ok {
			child := ca.GetChild()
			if child != nil {
				if tracker, ok := child.(StdMouseFeedback); ok {
					m.tracker = tracker
					m.target = child
					m.armed = true
					tracker.Feedback(true)
					return
				}
			}
		}
	}
}

// Move handles a mouse move at global coordinates (x, y) during a drag.
// Transitions between armed and disarmed states.
func (m *MouseState) Move(x, y int64) {
	if m.tracker == nil {
		return
	}
	_, _, inside := hitTest(x, y, m.target)
	if inside && !m.armed {
		m.armed = true
		m.tracker.Feedback(true)
	} else if !inside && m.armed {
		m.armed = false
		m.tracker.Feedback(false)
	}
}

// Release handles a mouse release at global coordinates (x, y).
// Completes the interaction: success if armed, failure if disarmed.
func (m *MouseState) Release(x, y int64) {
	if m.tracker == nil {
		return
	}
	m.tracker.Complete(m.armed)
	m.tracker = nil
	m.target = nil
	m.armed = false
}
