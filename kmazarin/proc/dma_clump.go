package proc

import "sync/atomic"

// MaxDMAClumps is the maximum number of MAZARIN_CONTIGUOUS allocations per
// shepherd. Fixed-size so registration is nosplit-safe on the g0 stack.
const MaxDMAClumps = 16

// DMAClump tracks a set of physically contiguous pages allocated by userspace
// via mmap(MAZARIN_CONTIGUOUS). The kernel uses this to resolve userspace VAs
// to physical addresses for DMA I/O, and to manage page lifetime (deferred
// release when I/O is in flight during munmap or shepherd death).
type DMAClump struct {
	StartPA        uintptr // Physical address of first page
	StartVA        uintptr // Userspace virtual address of first page
	NumPages       int     // Number of contiguous pages
	BuddyOrder     int     // Buddy allocator order (for freeing)
	InFlight       int32   // atomic: incremented on BlockSubmit, decremented on completion
	ShepherdDead   bool    // set when owning shepherd exits
	PendingRelease bool    // set when munmap called with InFlight > 0
}

// LookupPA returns the physical address for a userspace VA within this clump.
// Returns 0 if the VA is not in the clump.
//
//go:nosplit
func (c *DMAClump) LookupPA(va uintptr) uintptr {
	if va < c.StartVA {
		return 0
	}
	offset := va - c.StartVA
	if offset >= uintptr(c.NumPages)*4096 {
		return 0
	}
	return c.StartPA + offset
}

// ContainsVA reports whether va falls within this clump's VA range.
//
//go:nosplit
func (c *DMAClump) ContainsVA(va uintptr) bool {
	return va >= c.StartVA && va < c.StartVA+uintptr(c.NumPages)*4096
}

// FindClumpByVA searches a shepherd's clump array for the clump containing va.
// Returns a pointer to the clump, or nil if not found. Nosplit-safe.
//
//go:nosplit
func (s *Shepherd) FindClumpByVA(va uintptr) *DMAClump {
	n := s.NumDMAClumps
	for i := int32(0); i < n; i++ {
		if s.DMAClumps[i].ContainsVA(va) {
			return &s.DMAClumps[i]
		}
	}
	return nil
}

// AddClump registers a new DMA clump for this shepherd.
// Returns a pointer to the new clump, or nil if the array is full. Nosplit-safe.
//
//go:nosplit
func (s *Shepherd) AddClump(startPA, startVA uintptr, numPages, buddyOrder int) *DMAClump {
	if s.NumDMAClumps >= MaxDMAClumps {
		return nil
	}
	idx := s.NumDMAClumps
	s.DMAClumps[idx] = DMAClump{
		StartPA:    startPA,
		StartVA:    startVA,
		NumPages:   numPages,
		BuddyOrder: buddyOrder,
	}
	s.NumDMAClumps++
	return &s.DMAClumps[idx]
}

// LockClumps acquires the per-shepherd clump spinlock.
// Nosplit-safe; safe to call from any context since it's a simple CAS spin.
//
//go:nosplit
func (s *Shepherd) LockClumps() {
	for !atomic.CompareAndSwapInt32(&s.clumpSpin, 0, 1) {
	}
}

// unlockClumps releases the per-shepherd clump spinlock.
//
//go:nosplit
func (s *Shepherd) UnlockClumps() {
	atomic.StoreInt32(&s.clumpSpin, 0)
}

// RemoveClump removes the clump at index idx by swapping with the last element.
// Caller must hold s.LockClumps().
// Nosplit-safe.
//
//go:nosplit
func (s *Shepherd) RemoveClump(idx int32) {
	last := s.NumDMAClumps - 1
	if idx != last {
		s.DMAClumps[idx] = s.DMAClumps[last]
	}
	s.DMAClumps[last] = DMAClump{} // zero out
	s.NumDMAClumps--
}
