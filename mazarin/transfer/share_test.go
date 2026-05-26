// Phase 0 red tests for MAZ-53 — the merged Mode 2 Share surface.
//
// Covers the userspace-only invariants that don't require IPC scaffolding:
// ShareRange parameter validation, Share.AsBytes length contract, and
// Share.Release first-call success. The kernel-side semantics (actual page
// sharing, RefCount, ProtoDeath cleanup, multi-consumer concurrent share,
// chained share) are exercised by the boot integration test that lands as
// a work item in the plan.

package transfer

import (
	"errors"
	"testing"
	"unsafe"
)

// fakeSlab constructs a Slab pointing at a Go heap slice. Useful only for
// pure userspace tests (parameter validation, byte-length math). The kernel
// never sees this VA so any actual SVC call would fail; tests that exercise
// real sharing live in the boot integration test.
func fakeSlab(t *testing.T, pages int) *Slab {
	t.Helper()
	backing := make([]byte, pages*PageSize)
	return &Slab{
		va:    uintptr(unsafe.Pointer(&backing[0])),
		pages: pages,
		state: slabAllocated,
	}
}

// --- ShareRange parameter validation -----------------------------------------

func TestShareRange_NegativeOffsetRejected(t *testing.T) {
	slab := fakeSlab(t, 1)
	_, err := slab.ShareRange(ShepherdID(7), -1, 100)
	if !errors.Is(err, ErrShareRangeInvalid) {
		t.Fatalf("ShareRange(neg offset): want ErrShareRangeInvalid, got %v", err)
	}
}

func TestShareRange_NegativeLengthRejected(t *testing.T) {
	slab := fakeSlab(t, 1)
	_, err := slab.ShareRange(ShepherdID(7), 0, -1)
	if !errors.Is(err, ErrShareRangeInvalid) {
		t.Fatalf("ShareRange(neg length): want ErrShareRangeInvalid, got %v", err)
	}
}

func TestShareRange_OverflowRejected(t *testing.T) {
	slab := fakeSlab(t, 1) // 4096 bytes total
	// offset=4090, length=100 → overflows by 94 bytes
	_, err := slab.ShareRange(ShepherdID(7), PageSize-6, 100)
	if !errors.Is(err, ErrShareRangeInvalid) {
		t.Fatalf("ShareRange(overflow): want ErrShareRangeInvalid, got %v", err)
	}
}

func TestShareRange_ZeroLengthRejected(t *testing.T) {
	slab := fakeSlab(t, 1)
	// Zero-length share is meaningless — sender exposes nothing. Caller bug.
	_, err := slab.ShareRange(ShepherdID(7), 0, 0)
	if !errors.Is(err, ErrShareRangeInvalid) {
		t.Fatalf("ShareRange(zero length): want ErrShareRangeInvalid, got %v", err)
	}
}

// --- Share.AsBytes length contract ------------------------------------------

func TestShare_AsBytesLengthMatchesBytes(t *testing.T) {
	for _, want := range []int{1, 100, PageSize, PageSize * 4} {
		backing := make([]byte, want)
		s := &Share{VA: uintptr(unsafe.Pointer(&backing[0])), Bytes: want}
		got := s.AsBytes()
		if len(got) != want {
			t.Fatalf("AsBytes len for Bytes=%d: want %d got %d", want, want, len(got))
		}
	}
}

func TestShare_AsBytesAliasesUnderlying(t *testing.T) {
	backing := make([]byte, 256)
	s := &Share{VA: uintptr(unsafe.Pointer(&backing[0])), Bytes: 256}
	view := s.AsBytes()
	if len(view) == 0 {
		t.Fatalf("AsBytes returned empty slice for non-zero Share.Bytes=%d", s.Bytes)
	}
	// Write through view, read via backing.
	view[42] = 0x55
	if backing[42] != 0x55 {
		t.Fatalf("AsBytes view does not alias backing: backing[42]=%#x", backing[42])
	}
}

// --- Share.Release idempotency ----------------------------------------------

func TestShare_ReleaseFirstCallSucceeds(t *testing.T) {
	// In v1, a freshly-constructed Share (mid-test, without IPC scaffolding)
	// represents a consumer-side held share. The Release contract is "first
	// call sends the Release IPC + marks released; returns nil."
	//
	// This test exercises the FIRST-call branch — second-call idempotency
	// is verified by the boot integration test where actual IPC has happened.
	s := &Share{VA: 0x1000, Bytes: 256, id: ShareID(42), senderSID: ShepherdID(7)}
	if err := s.Release(); err != nil {
		t.Fatalf("first Release: want nil, got %v", err)
	}
}
