package pipe

import (
	"bytes"
	"testing"
)

// MAZ-149 — writer-park backpressure spec. A blocking write that finds the
// buffer full is parked by the shepherd (ParkWriter); a reader draining free
// space releases the parked tokens (TakeWriterWaiters) so the shepherd can
// retry the writes. Symmetric to the reader Park/TakeWaiters pair.

func fillPipe(t *testing.T, w *End) {
	t.Helper()
	chunk := bytes.Repeat([]byte{'x'}, 512)
	for {
		n, err := w.Write(chunk)
		if err == ErrWouldBlock {
			return
		}
		if err != nil {
			t.Fatalf("fill write err: %v", err)
		}
		if n == 0 {
			t.Fatalf("fill write accepted 0 bytes without ErrWouldBlock")
		}
	}
}

// TestWriterParkHeldWhileFull — tokens parked on a full buffer stay parked
// (TakeWriterWaiters returns nil) until a reader frees space.
func TestWriterParkHeldWhileFull(t *testing.T) {
	r, w := New(0)
	fillPipe(t, w)

	w.ParkWriter("tok1")
	if got := w.TakeWriterWaiters(); got != nil {
		t.Fatalf("TakeWriterWaiters on full buffer = %v, want nil (still blocked)", got)
	}

	// Drain some bytes — space is now free, tokens release in FIFO order.
	var buf [100]byte
	if n, err := r.Read(buf[:]); err != nil || n != 100 {
		t.Fatalf("drain read = (%d, %v), want (100, nil)", n, err)
	}
	got := w.TakeWriterWaiters()
	if len(got) != 1 || got[0] != "tok1" {
		t.Fatalf("TakeWriterWaiters after drain = %v, want [tok1]", got)
	}
	// Cleared after take.
	if got := w.TakeWriterWaiters(); got != nil {
		t.Fatalf("second TakeWriterWaiters = %v, want nil (cleared)", got)
	}
}

// TestWriterParkFIFOOrder — multiple parked writers release in park order so
// the shepherd retries the oldest writer first.
func TestWriterParkFIFOOrder(t *testing.T) {
	r, w := New(0)
	fillPipe(t, w)
	w.ParkWriter("first")
	w.ParkWriter("second")

	var buf [10]byte
	if _, err := r.Read(buf[:]); err != nil {
		t.Fatalf("drain read err: %v", err)
	}
	got := w.TakeWriterWaiters()
	if len(got) != 2 || got[0] != "first" || got[1] != "second" {
		t.Fatalf("TakeWriterWaiters = %v, want [first second]", got)
	}
}

// TestWriterParkNoWaitersNil — an empty writer-waiter list yields nil even
// with free space (nothing to wake).
func TestWriterParkNoWaitersNil(t *testing.T) {
	_, w := New(0)
	if got := w.TakeWriterWaiters(); got != nil {
		t.Fatalf("TakeWriterWaiters with no parks = %v, want nil", got)
	}
}

// TestWriterParkReadEndTake — TakeWriterWaiters works from either End (both
// share the buf), matching how the shepherd wakes writers from the read path.
func TestWriterParkReadEndTake(t *testing.T) {
	r, w := New(0)
	fillPipe(t, w)
	w.ParkWriter("tok")
	var buf [8]byte
	if _, err := r.Read(buf[:]); err != nil {
		t.Fatalf("drain read err: %v", err)
	}
	if got := r.TakeWriterWaiters(); len(got) != 1 || got[0] != "tok" {
		t.Fatalf("read-end TakeWriterWaiters = %v, want [tok]", got)
	}
}

// TestMarkDropOverwritesTail — the `@\` breadcrumb replaces the buffer tail
// (bounded-park timeout drop) without changing the buffered length, so a
// reader can detect the loss. MarkDrop only latches the request (it may be
// called from the watchdog goroutine); the marker lands at the next Read,
// in the reader's dispatch context.
func TestMarkDropOverwritesTail(t *testing.T) {
	r, w := New(0)
	if _, err := w.Write([]byte("abcdef")); err != nil {
		t.Fatalf("write err: %v", err)
	}
	w.MarkDrop()
	var buf [16]byte
	n, err := r.Read(buf[:])
	if err != nil || n != 6 {
		t.Fatalf("read = (%d, %v), want (6, nil)", n, err)
	}
	if string(buf[:n]) != "abcd@\\" {
		t.Fatalf("post-MarkDrop data = %q, want %q", buf[:n], "abcd@\\")
	}
}

// TestMarkDropShortBufferUntouched — fewer buffered bytes than the marker:
// leave them alone (the missing data itself implies the loss).
func TestMarkDropShortBufferUntouched(t *testing.T) {
	r, w := New(0)
	if _, err := w.Write([]byte("z")); err != nil {
		t.Fatalf("write err: %v", err)
	}
	w.MarkDrop()
	var buf [4]byte
	n, err := r.Read(buf[:])
	if err != nil || n != 1 || buf[0] != 'z' {
		t.Fatalf("read = (%q, %v), want (\"z\", nil)", buf[:n], err)
	}
}

// TestMarkDropAppliesOnce — the latch is consumed by the read that applies
// (or skips) it; data written afterward is not re-marked.
func TestMarkDropAppliesOnce(t *testing.T) {
	r, w := New(0)
	if _, err := w.Write([]byte("abcdef")); err != nil {
		t.Fatalf("write err: %v", err)
	}
	w.MarkDrop()
	var buf [16]byte
	n, _ := r.Read(buf[:])
	if string(buf[:n]) != "abcd@\\" {
		t.Fatalf("first read = %q, want %q", buf[:n], "abcd@\\")
	}
	if _, err := w.Write([]byte("ghijkl")); err != nil {
		t.Fatalf("second write err: %v", err)
	}
	n, _ = r.Read(buf[:])
	if string(buf[:n]) != "ghijkl" {
		t.Fatalf("second read = %q, want %q (no re-mark)", buf[:n], "ghijkl")
	}
}
