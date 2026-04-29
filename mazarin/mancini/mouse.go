package mancini

// StdMouseFeedback is implemented by interactors that respond to
// press-drag-release interactions. The mancini state machine handles
// all armed/disarmed transitions and calls these two methods:
//
//   - Feedback(true)  — you are active (mouse inside, button held)
//   - Feedback(false) — you are inactive (mouse dragged outside)
//   - Complete(true)  — interaction succeeded (released inside)
//   - Complete(false) — interaction cancelled (released outside)
//
// Feedback is called on each state change during the drag. Complete
// is called exactly once when the button is released.
type StdMouseFeedback interface {
	// Feedback reports whether the interactor is currently armed (active).
	Feedback(active bool)
	// Complete is called once when the press-drag-release ends; success is true if released inside.
	Complete(success bool)
}

// DetailedHit is optionally implemented by interactors that need
// finer-grained hit testing than their rectangular bounds. The mouse
// state machine calls DetailedHit with local coordinates after the
// bounding-box check passes. If the interactor does not implement this
// interface, the bounding-box hit is accepted as-is.
type DetailedHit interface {
	// DetailedHit returns true if (localX, localY) is a real hit beyond the bounding-box check.
	DetailedHit(localX, localY int64) bool
}
