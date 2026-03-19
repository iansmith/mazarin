package mancini

// MousePressHandler is implemented by interactors that respond to mouse presses.
type MousePressHandler interface {
	HandleMousePress(x, y int64)
}

// DetailedHit is optionally implemented by interactors that need
// finer-grained hit testing than their rectangular bounds. MousePolicy
// calls DetailedHit with local coordinates after the bounding-box check
// passes. If the interactor does not implement this interface, the
// bounding-box hit is accepted as-is.
type DetailedHit interface {
	DetailedHit(localX, localY int64) bool
}
