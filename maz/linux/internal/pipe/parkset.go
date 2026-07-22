package pipe

import "sync"

// ParkSet tracks every request the shepherd currently has parked (pipe
// readers on an empty buffer, pipe writers on a full one), keyed by the
// opaque park token with the owner shepherd's SID. It is the
// fulfilled-or-abandoned signal shared by the wake paths and the death
// sweep (MAZ-149 writer protocol, generalized for MAZ-155):
//
//   - a wake path calls TakeLive before replying and SKIPS tokens that are
//     no longer registered — a late Reply for a dead owner would target a
//     stale-or-REUSED caller TID and corrupt an unrelated in-flight delegate;
//   - shepherd teardown calls DropOwner, abandoning the dying SID's parks
//     without replying (the kernel reclaims the data pages via the deferred
//     death cleanup);
//   - the writer-park timeout watchdog calls Sweep to collect expired parks.
//
// Stale tokens left on a pipe's waiter lists after DropOwner/Sweep are
// harmless: whoever takes them from the pipe checks liveness here first.
//
// A wake loop's TakeLive→re-Park sequence must be atomic with respect to
// DropOwner: without exclusion, DropOwner can run between TakeLive removing
// the token and Park re-adding it, leaving a zombie registration for a dead
// owner that no future DropOwner will ever clean (SIDs are monotonic). Wake
// loops bracket each token's window with BeginWake/EndWake; DropOwner waits
// out in-flight windows before sweeping. Fresh parks (dispatch path) need no
// bracket: dispatch holds the per-SID refcount for the whole handle() call,
// and teardown's DropOwner only runs after that refcount drains, so a fresh
// park strictly happens-before its own owner's DropOwner.
//
// Internally locked: the watchdog goroutine and the dispatch goroutines
// touch the set concurrently.
type ParkSet struct {
	mu     sync.Mutex
	live   map[any]int16 // token → owner SID
	wakeMu sync.RWMutex  // excludes DropOwner from wake windows
}

// NewParkSet returns an empty registry.
func NewParkSet() *ParkSet {
	return &ParkSet{live: make(map[any]int16)}
}

// Park registers tok as a live parked request owned by the shepherd with
// the given SID. Re-parking a token (a woken request that must block again)
// simply re-registers it, possibly under a new owner. Re-parks must happen
// inside the BeginWake/EndWake window that took the token.
func (s *ParkSet) Park(tok any, owner int16) {
	s.mu.Lock()
	s.live[tok] = owner
	s.mu.Unlock()
}

// TakeLive removes tok from the registry and reports whether it was still
// registered. A false return means the park was already abandoned (owner
// died, or the watchdog expired it) — the caller must NOT reply to it.
func (s *ParkSet) TakeLive(tok any) bool {
	s.mu.Lock()
	_, live := s.live[tok]
	delete(s.live, tok)
	s.mu.Unlock()
	return live
}

// BeginWake opens a wake window: the TakeLive→reply-or-re-Park sequence for
// one token. DropOwner waits for all open windows, so a re-park inside the
// window always lands before the owner's death sweep. Windows do not block
// each other; hold one only for a single token's processing.
func (s *ParkSet) BeginWake() {
	s.wakeMu.RLock()
}

// EndWake closes the window opened by BeginWake.
func (s *ParkSet) EndWake() {
	s.wakeMu.RUnlock()
}

// DropOwner abandons every park owned by the given SID. Removal IS the
// abandon signal — no Reply is sent for dropped tokens. Waits out in-flight
// wake windows first, so any re-park racing this death lands before the
// sweep and is collected by it.
func (s *ParkSet) DropOwner(owner int16) {
	s.wakeMu.Lock()
	defer s.wakeMu.Unlock()
	s.mu.Lock()
	for tok, o := range s.live {
		if o == owner {
			delete(s.live, tok)
		}
	}
	s.mu.Unlock()
}

// Sweep removes and returns every token the predicate selects, leaving the
// rest live. The predicate runs with the internal lock held — it must not
// call back into the ParkSet.
func (s *ParkSet) Sweep(expire func(tok any) bool) []any {
	var expired []any
	s.mu.Lock()
	for tok := range s.live {
		if expire(tok) {
			delete(s.live, tok)
			expired = append(expired, tok)
		}
	}
	s.mu.Unlock()
	return expired
}
