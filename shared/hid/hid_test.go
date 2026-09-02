package hid

import "testing"

// MAZ-194: the Events index for the kernel IRQ-path push must come from
// the compile-time CompletionRingSize constant, never from the Capacity
// field — the header lives in the user-shared ring page, so userspace can
// scribble Capacity at any time. A hostile value must not change which
// slot is written (early-wrap over unconsumed events), must never index
// past the array (kernel bounds panic), and must never reach a mod-zero.
func TestEventSlotIgnoresSharedCapacity(t *testing.T) {
	tails := []uint32{0, 1, 507, 508, 509, 1024, 0xFFFFFFFF}
	for _, hostileCap := range []uint32{0, 1, 3, 256, 509, 1 << 30} {
		r := &CompletionRing{Capacity: hostileCap}
		for _, tail := range tails {
			slot := func() (s uint32) {
				defer func() {
					if p := recover(); p != nil {
						t.Fatalf("EventSlot panicked with scribbled Capacity=%d tail=%d: %v",
							hostileCap, tail, p)
					}
				}()
				return r.EventSlot(tail)
			}()
			want := tail % CompletionRingSize
			if slot != want {
				t.Fatalf("EventSlot(tail=%d) with scribbled Capacity=%d = %d, want %d — index math must use CompletionRingSize",
					tail, hostileCap, slot, want)
			}
		}
	}
}
