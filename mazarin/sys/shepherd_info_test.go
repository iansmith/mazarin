package sys

import (
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
	const live = 40
	got, err := readAllShepherdEntries(func(buf []hid.ShepherdInfoEntry) (int, error) {
		n := min(live, len(buf))
		for i := range n {
			buf[i].SID = int16(i + 1)
		}
		return n, nil
	})
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if len(got) != live {
		t.Fatalf("got %d entries, want %d (kernel had more than the first buffer held)", len(got), live)
	}
	if got[live-1].SID != live {
		t.Fatalf("last SID = %d, want %d", got[live-1].SID, live)
	}
}
