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
// Internally locked: the watchdog goroutine and the dispatch goroutines
// touch the set concurrently.
type ParkSet struct {
	mu   sync.Mutex
	live map[any]int16 // token → owner SID
	dead map[int16]bool
}

// NewParkSet returns an empty registry.
func NewParkSet() *ParkSet {
	return &ParkSet{live: make(map[any]int16), dead: make(map[int16]bool)}
}

// Park registers tok as a live parked request owned by the shepherd with
// the given SID. Returns true if registered, false if the owner was already
// dropped (SIDs are monotonic — once dead, always dead). A false return
// means the caller must NOT park the token on the pipe's waiter list.
func (s *ParkSet) Park(tok any, owner int16) bool {
	s.mu.Lock()
	if s.dead[owner] {
		s.mu.Unlock()
		return false
	}
	s.live[tok] = owner
	s.mu.Unlock()
	return true
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

// DropOwner abandons every park owned by the given SID and marks the SID
// as dead. Future Park calls for this owner are rejected (SIDs are
// monotonic — a dead SID can never come back). Removal IS the abandon
// signal — no Reply is sent for dropped tokens.
func (s *ParkSet) DropOwner(owner int16) {
	s.mu.Lock()
	s.dead[owner] = true
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
