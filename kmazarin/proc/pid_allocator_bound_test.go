// Phase 0 red tests for MAZ-150 (wire in the monotonic proc.PIDAllocator).
//
// The kernel allocates every shepherd/child PID from PIDAllocator over
// [MinPID, MaxPID]. Several kernel-side arrays — notifyQueues
// (ksyscall/constraint_notify.go), the uring IPC slots (kmazarin/uring_ipc.go), and
// the channel pending-message arrays (kmazarin/channels.go) — are sized
// [MaxLiveShepherds] and indexed DIRECTLY by the raw ShepherdId, rejecting any
// PID >= MaxLiveShepherds. So the allocator must never hand out a PID that would
// overflow (or be silently rejected by) those satellite arrays. (Pools that
// linear-scan a stored SID field — deathSubscribers, uringIDMap, deferredCleanups —
// bound the live count, not the PID value, so they don't constrain MaxPID.)
//
// Option A (Ian, 2026-07-04): cap MaxPID to MaxLiveShepherds-1 so raw-PID indexing
// stays valid without widening the arrays. These tests fail on the current code
// (MaxPID = 4095) and pass once the cap lands.
//
// MAZ-152 (Option C) slot-maps those arrays and will deliberately lift this bound
// (restoring MaxPID = 4095); update these tests as part of that work.

package proc

import "testing"

// TestMaxPIDWithinSatelliteArrayBound pins the constant-level invariant: the
// allocator's upper bound must stay strictly below the satellite arrays' width.
func TestMaxPIDWithinSatelliteArrayBound(t *testing.T) {
	if MaxPID >= MaxLiveShepherds {
		t.Errorf("MaxPID = %d must be < MaxLiveShepherds = %d: kernel satellite arrays are "+
			"sized [MaxLiveShepherds] and indexed by raw PID, so a PID >= MaxLiveShepherds "+
			"overflows/rejects them (MAZ-150 Option A). MAZ-152 lifts this via slot-mapping.",
			MaxPID, MaxLiveShepherds)
	}
}

// TestMaxPIDEqualsMaxLiveShepherdsMinusOne pins the EXACT Option A cap, not just
// the inequality: MaxPID must be MaxLiveShepherds-1 (255), the largest PID that
// still indexes a [MaxLiveShepherds] satellite array. A looser value (e.g. a
// needlessly small cap, or an off-by-one that wastes a valid PID) would pass the
// `< MaxLiveShepherds` bound check but violate the locked decision.
func TestMaxPIDEqualsMaxLiveShepherdsMinusOne(t *testing.T) {
	if MaxPID != MaxLiveShepherds-1 {
		t.Errorf("MaxPID = %d, want MaxLiveShepherds-1 = %d: MAZ-150 Option A pins the cap to the "+
			"largest PID that still indexes a [MaxLiveShepherds] satellite array; a smaller or "+
			"off-by-one cap would silently pass the looser `< MaxLiveShepherds` check.",
			MaxPID, MaxLiveShepherds-1)
	}
}

// TestAllocatorNeverExceedsSatelliteArrayBound exhausts the allocator and asserts
// every issued PID is a valid index into a [MaxLiveShepherds]-sized array.
func TestAllocatorNeverExceedsSatelliteArrayBound(t *testing.T) {
	a := NewPIDAllocator()
	total := int(MaxPID-MinPID) + 1
	for i := 0; i < total; i++ {
		pid, err := a.Alloc()
		if err != nil {
			t.Fatalf("Alloc #%d returned %v before exhaustion", i, err)
		}
		if int(pid) >= MaxLiveShepherds {
			t.Fatalf("Alloc returned PID %d >= MaxLiveShepherds %d: would overflow a "+
				"raw-PID-indexed kernel satellite array (MAZ-150)", pid, MaxLiveShepherds)
		}
	}
}
