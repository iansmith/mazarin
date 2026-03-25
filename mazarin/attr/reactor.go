// reactor.go — Reactor dispatches dirty notifications to typed callbacks.
//
// A Reactor wraps OnDirty() and maintains a dispatch table mapping attribute
// slots to watch entries. When dirty slots arrive, it evaluates constraints
// (phase 1) and fires callbacks only for attributes whose output actually
// changed (phase 2). Exposes a channel C for use in select loops.

package attr

import (
	"mazzy/mazarin/vm"
	"mazzy/mazarin/vm/flat"
)

// watchEntry is a type-erased watch registration.
type watchEntry struct {
	slot uint16
	// eval re-evaluates the attribute and returns true if the output changed.
	eval func() bool
	// notify fires the user's callback.
	notify func()
}

// Reactor dispatches dirty attribute notifications to registered callbacks.
// Use C in a select statement alongside other channels, then call Dispatch
// with the received slots.
type Reactor struct {
	// C receives batches of dirty slot numbers from the kernel.
	// Use in a select{} alongside other channels.
	C       <-chan []uint16
	watches map[uint16]*watchEntry
}

// NewReactor creates a Reactor backed by OnDirty().
func NewReactor() *Reactor {
	return &Reactor{
		C:       OnDirty(),
		watches: make(map[uint16]*watchEntry),
	}
}

// Dispatch evaluates all dirty watched attributes and fires callbacks for
// those whose output changed. Call this from the goroutine that owns the
// select loop — callbacks execute synchronously in the caller's goroutine.
//
// If slots is nil (overflow), all watched attributes are re-evaluated.
func (r *Reactor) Dispatch(slots []uint16) {
	if slots == nil {
		// Overflow — re-evaluate everything.
		var fired []*watchEntry
		for _, w := range r.watches {
			if w.eval() {
				fired = append(fired, w)
			}
		}
		for _, w := range fired {
			w.notify()
		}
		return
	}

	var fired []*watchEntry
	for _, slot := range slots {
		if w, ok := r.watches[slot]; ok {
			if w.eval() {
				fired = append(fired, w)
			}
		}
	}
	for _, w := range fired {
		w.notify()
	}
}

// Run is a convenience that loops forever, dispatching dirty notifications.
// Use this when the Reactor is the only event source. For select loops
// with multiple channels, use C and Dispatch directly.
func (r *Reactor) Run() {
	for slots := range r.C {
		r.Dispatch(slots)
	}
}

// WatchI64 registers a callback for an int64 attribute. The callback fires
// only when the evaluated output changes. The handle is automatically marked
// eager.
func (r *Reactor) WatchI64(h *Attribute[int64], fn func(old, new int64)) {
	h.SetEager(true)
	last := h.Get()
	var pending int64
	r.watches[h.slot] = &watchEntry{
		slot: h.slot,
		eval: func() bool {
			pending = h.Get()
			return pending != last
		},
		notify: func() {
			old := last
			last = pending
			fn(old, last)
		},
	}
}

// WatchF64 registers a callback for a float64 attribute.
func (r *Reactor) WatchF64(h *Attribute[float64], fn func(old, new float64)) {
	h.SetEager(true)
	last := h.Get()
	var pending float64
	r.watches[h.slot] = &watchEntry{
		slot: h.slot,
		eval: func() bool {
			pending = h.Get()
			return pending != last
		},
		notify: func() {
			old := last
			last = pending
			fn(old, last)
		},
	}
}

// WatchBool registers a callback for a bool attribute.
func (r *Reactor) WatchBool(h *Attribute[bool], fn func(old, new bool)) {
	h.SetEager(true)
	last := h.Get()
	var pending bool
	r.watches[h.slot] = &watchEntry{
		slot: h.slot,
		eval: func() bool {
			pending = h.Get()
			return pending != last
		},
		notify: func() {
			old := last
			last = pending
			fn(old, last)
		},
	}
}

// WatchStr registers a callback for a string attribute.
func (r *Reactor) WatchStr(h *Attribute[string], fn func(old, new string)) {
	h.SetEager(true)
	last := h.Get()
	var pending string
	r.watches[h.slot] = &watchEntry{
		slot: h.slot,
		eval: func() bool {
			pending = h.Get()
			return pending != last
		},
		notify: func() {
			old := last
			last = pending
			fn(old, last)
		},
	}
}

// WatchValue registers a callback for a composite attribute (Rectangle,
// Point2D, Timespec, etc.) using FlatValue byte comparison for change
// detection.
func (r *Reactor) WatchValue(h *Attribute[vm.Value], fn func(old, new vm.Value)) {
	h.SetEager(true)
	lastFlat := h.seqlockRead()
	var pendingFlat flat.FlatValue
	r.watches[h.slot] = &watchEntry{
		slot: h.slot,
		eval: func() bool {
			if h.kind == flat.AttrKindConstraint && h.IsDirty() {
				h.evaluate()
			}
			pendingFlat = h.seqlockRead()
			return pendingFlat != lastFlat
		},
		notify: func() {
			oldVal, _ := flat.FlatToValue(lastFlat, sharedPR)
			lastFlat = pendingFlat
			newVal, _ := flat.FlatToValue(lastFlat, sharedPR)
			fn(oldVal, newVal)
		},
	}
}
