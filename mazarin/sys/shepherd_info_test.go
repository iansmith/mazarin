package sys

import (
	"errors"
	"testing"

	"mazzy/shared/hid"
)

// TestReadAllShepherdEntriesPastFirstBuffer — MAZ-206 review: the kernel
// writes at most len(buf) entries, walking slots in index order. With a fixed
// 32-entry buffer, a live shepherd in a later slot was simply absent from the
// snapshot, so GetShepherdByName reported ErrNoShepherd for it — which
// waitLoop now reads as "seen, then died". A snapshot must hold every live
// shepherd however many there are.
func TestReadAllShepherdEntriesPastFirstBuffer(t *testing.T) {
	// 40: past the first buffer. 32: exactly fills it — full is ambiguous,
	// so it must re-fetch and still return exactly 32.
	for _, live := range []int{40, 32} {
		got, err := readAllShepherdEntries(fakeShepherdFetch(live))
		if err != nil {
			t.Fatalf("live=%d: err = %v, want nil", live, err)
		}
		if len(got) != live {
			t.Fatalf("live=%d: got %d entries, want all %d", live, len(got), live)
		}
		if got[live-1].SID != int16(live) {
			t.Fatalf("live=%d: last SID = %d, want %d", live, got[live-1].SID, live)
		}
	}
}

// TestReadAllShepherdEntriesPropagatesError — a failed fetch must surface as
// an error, never as a short (possibly empty) table: an empty table reads as
// "every shepherd gone" to waitLoop.
func TestReadAllShepherdEntriesPropagatesError(t *testing.T) {
	boom := errors.New("fetch failed")
	calls := 0
	got, err := readAllShepherdEntries(func(buf []hid.ShepherdInfoEntry) (int, error) {
		calls++
		if calls == 1 {
			return fakeShepherdFetch(40)(buf) // full: forces a second fetch
		}
		return 0, boom
	})
	if !errors.Is(err, boom) || got != nil {
		t.Fatalf("got (%d entries, %v), want (nil, %v)", len(got), err, boom)
	}
}

// fakeShepherdFetch mimics SyscallShepherdInfo with live shepherds: it
// writes min(live, len(buf)) entries and returns that count.
func fakeShepherdFetch(live int) func([]hid.ShepherdInfoEntry) (int, error) {
	return func(buf []hid.ShepherdInfoEntry) (int, error) {
		n := min(live, len(buf))
		for i := range n {
			buf[i].SID = int16(i + 1)
		}
		return n, nil
	}
}
