package sys

import "errors"

// Sentinel errors for shepherd name resolution.
var (
	// ErrNoShepherd is returned when no running shepherd matches the given name or SID.
	ErrNoShepherd = errors.New("no shepherd with that name")

	// ErrAmbiguousShepherd is returned when more than one running shepherd matches.
	ErrAmbiguousShepherd = errors.New("more than one shepherd with that name")

	// ErrNotReady is returned when the shepherd exists but has not called SetReady(true).
	ErrNotReady = errors.New("shepherd exists but is not ready")
)
