//go:build arm64 || amd64

package ds

import "testing"

// MonotonicAllocator is the shepherd-PID strategy (proc.PIDAllocator, MAZ-150)
// generalized for reuse by every kernel ID space: monotonic-forward issue,
// wrap at max, skip in-use on wrap, and — critically — NO immediate reissue of
// a freed ID. It replaces the LIFO ds.StaticAllocator for thread and channel
// IDs so a freed ID cannot be handed straight back to the next caller (the
// MAZ-179 TID-recycle collision). These tests pin the same contract the
// proc.PIDAllocator tests pin, expressed on the generic type directly.

// newTestAlloc returns a MonotonicAllocator over IDs [reserved, size) backed by
// a fresh bool array of length size.
func newTestAlloc(size, reserved int) (*MonotonicAllocator[int16], []bool) {
	inUse := make([]bool, size)
	a := &MonotonicAllocator[int16]{}
	a.InitWithReserved(inUse, reserved)
	return a, inUse
}

func TestMonotonicAllocSequentialFromReserved(t *testing.T) {
	a, _ := newTestAlloc(16, 2)
	for i := 0; i < 3; i++ {
		id, ok := a.TryAcquire()
		if !ok {
			t.Fatalf("TryAcquire #%d not ok", i)
		}
		want := int16(2 + i)
		if id != want {
			t.Errorf("TryAcquire #%d = %d, want %d", i, id, want)
		}
	}
}

func TestMonotonicAllocNeverReturnsReserved(t *testing.T) {
	a, _ := newTestAlloc(16, 3)
	for i := 0; i < a.Capacity(); i++ {
		id, ok := a.TryAcquire()
		if !ok {
			t.Fatalf("TryAcquire #%d not ok before exhaustion", i)
		}
		if id < 3 {
			t.Errorf("TryAcquire returned reserved id %d (reserved=3)", id)
		}
	}
}

func TestMonotonicDoesNotImmediatelyReuse(t *testing.T) {
	a, _ := newTestAlloc(16, 2)
	p1, _ := a.TryAcquire()
	a.Release(p1)
	p2, _ := a.TryAcquire()
	if p1 == p2 {
		t.Errorf("reissued freed id %d immediately; expected wraparound-only reuse", p1)
	}
}

func TestMonotonicExhaustion(t *testing.T) {
	a, _ := newTestAlloc(16, 2)
	for i := 0; i < a.Capacity(); i++ {
		if _, ok := a.TryAcquire(); !ok {
			t.Fatalf("TryAcquire #%d not ok before exhaustion", i)
		}
	}
	if id, ok := a.TryAcquire(); ok {
		t.Errorf("TryAcquire on exhausted allocator returned (%d, true), want (_, false)", id)
	}
}

func TestMonotonicAcquirePanicsOnExhaustion(t *testing.T) {
	a, _ := newTestAlloc(4, 2) // capacity 2
	a.Acquire()
	a.Acquire()
	defer func() {
		if recover() == nil {
			t.Errorf("Acquire on exhausted allocator did not panic")
		}
	}()
	a.Acquire() // must panic
}

func TestMonotonicWraparoundReusesFreedAfterFull(t *testing.T) {
	a, _ := newTestAlloc(16, 2)
	allocated := make([]int16, 0, a.Capacity())
	for i := 0; i < a.Capacity(); i++ {
		id, _ := a.TryAcquire()
		allocated = append(allocated, id)
	}
	a.Release(allocated[0])
	a.Release(allocated[5])
	id, ok := a.TryAcquire()
	if !ok {
		t.Fatalf("TryAcquire after Release not ok; want success via wraparound")
	}
	if id != allocated[0] && id != allocated[5] {
		t.Errorf("wraparound = %d; want one of {%d, %d}", id, allocated[0], allocated[5])
	}
}

func TestMonotonicWraparoundSkipsInUse(t *testing.T) {
	a, _ := newTestAlloc(16, 2)
	allocated := make([]int16, 0, a.Capacity())
	for i := 0; i < a.Capacity(); i++ {
		id, _ := a.TryAcquire()
		allocated = append(allocated, id)
	}
	freed := map[int16]bool{allocated[3]: true, allocated[7]: true, allocated[11]: true}
	for id := range freed {
		a.Release(id)
	}
	seen := map[int16]bool{}
	for i := 0; i < len(freed); i++ {
		id, ok := a.TryAcquire()
		if !ok {
			t.Fatalf("TryAcquire #%d after wraparound not ok", i)
		}
		if !freed[id] {
			t.Errorf("returned %d not in freed set %v", id, freed)
		}
		if seen[id] {
			t.Errorf("returned %d twice; should skip in-use", id)
		}
		seen[id] = true
	}
}

func TestMonotonicFreeIdempotent(t *testing.T) {
	a, _ := newTestAlloc(16, 2)
	id, _ := a.TryAcquire()
	a.Release(id)
	a.Release(id) // must not panic or corrupt
	if _, ok := a.TryAcquire(); !ok {
		t.Errorf("TryAcquire after double-Release not ok")
	}
}

func TestMonotonicInUseTracksAllocFree(t *testing.T) {
	a, _ := newTestAlloc(16, 2)
	id, _ := a.TryAcquire()
	if !a.InUse(id) {
		t.Errorf("InUse(%d) = false after acquire; want true", id)
	}
	a.Release(id)
	if a.InUse(id) {
		t.Errorf("InUse(%d) = true after release; want false", id)
	}
}

func TestMonotonicNeverReturnsOutOfRange(t *testing.T) {
	a, _ := newTestAlloc(16, 2)
	for i := 0; i < a.Capacity(); i++ {
		id, ok := a.TryAcquire()
		if !ok {
			t.Fatalf("TryAcquire #%d not ok", i)
		}
		if id < 2 || id >= 16 {
			t.Errorf("id %d outside [2, 16)", id)
		}
	}
}
