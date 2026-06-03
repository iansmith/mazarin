// Tests for pipe.End.Fork() — the fork(2) FD-inheritance primitive added for
// MAZ-63. Fork() mirrors Linux's get_file() reference-count increment inside
// copy_files(): a forked write-end shares the same pipe buffer and increments
// the writer count so that Close on each side correctly signals EOF only when
// BOTH sides have closed.

package pipe

import (
	"errors"
	"testing"
)

// TestForkWriteEnd_WriterCountIncrements verifies that forking a write-end
// increments the shared writer count from 1 to 2.
func TestForkWriteEnd_WriterCountIncrements(t *testing.T) {
	_, w := New(0)
	if w.b.writers != 1 {
		t.Fatalf("initial writers = %d, want 1", w.b.writers)
	}
	wFork := w.Fork()
	if w.b.writers != 2 {
		t.Errorf("writers after Fork = %d, want 2", w.b.writers)
	}
	// Both ends share the same buf.
	if w.b != wFork.b {
		t.Error("forked write-end does not share the parent's buf")
	}
	_ = wFork
}

// TestForkWriteEnd_BothCloseProducesEOF is the errpipe contract:
// parent has (read, write); fork gives write-fork to child; Close on fork
// decrements writers 2→1; Close on original decrements 1→0; read returns EOF.
func TestForkWriteEnd_BothCloseProducesEOF(t *testing.T) {
	r, w := New(0)
	wFork := w.Fork() // child inherits; writers == 2

	// Child's exec sweep closes its copy first.
	wFork.Close()
	if w.b.writers != 1 {
		t.Fatalf("writers after child Close = %d, want 1", w.b.writers)
	}
	// Read must still block — parent's write-end is still open.
	buf := make([]byte, 1)
	n, err := r.Read(buf)
	if n != 0 || !errors.Is(err, ErrWouldBlock) {
		t.Fatalf("Read after child close: got (%d,%v), want (0,ErrWouldBlock)", n, err)
	}

	// Parent closes its own write-end.
	w.Close()
	if w.b.writers != 0 {
		t.Fatalf("writers after parent Close = %d, want 0", w.b.writers)
	}
	// Now read returns EOF.
	n, err = r.Read(buf)
	if n != 0 || err != nil {
		t.Fatalf("Read at EOF: got (%d,%v), want (0,nil)", n, err)
	}
}

// TestForkWriteEnd_CloseIdempotent verifies that calling Close twice on the
// forked write-end only decrements writers once (idempotent, matching End.Close).
func TestForkWriteEnd_CloseIdempotent(t *testing.T) {
	_, w := New(0)
	wFork := w.Fork() // writers == 2
	wFork.Close()
	wFork.Close() // second close must be a no-op
	if w.b.writers != 1 {
		t.Errorf("writers after double-Close on fork = %d, want 1", w.b.writers)
	}
}

// TestForkReadEnd_SharesBuf verifies that forking a read-end produces a new
// End sharing the same buf without changing the writer count.
func TestForkReadEnd_SharesBuf(t *testing.T) {
	r, w := New(0)
	rFork := r.Fork()
	if r.b != rFork.b {
		t.Error("forked read-end does not share the parent's buf")
	}
	if w.b.writers != 1 {
		t.Errorf("writer count changed by read-end Fork: got %d, want 1", w.b.writers)
	}
	_ = w
}

// TestForkReadEnd_SeesData verifies that data written to the write-end is
// readable from both the original and the forked read-end (they share the buf).
func TestForkReadEnd_SeesData(t *testing.T) {
	r, w := New(0)
	rFork := r.Fork()

	if _, err := w.Write([]byte("hi")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// Either end can read the data (first one wins — same buffer).
	got := make([]byte, 2)
	n, err := rFork.Read(got)
	if err != nil || n != 2 || string(got[:n]) != "hi" {
		t.Fatalf("rFork.Read: got (%d,%v,%q), want (2,nil,\"hi\")", n, err, got[:n])
	}
	// Original read-end now sees empty buffer.
	n, err = r.Read(got)
	if n != 0 || !errors.Is(err, ErrWouldBlock) {
		t.Fatalf("r.Read after rFork drained: got (%d,%v), want (0,ErrWouldBlock)", n, err)
	}
}

// TestForkClosedPanics verifies that Fork on a closed End panics.
func TestForkClosedPanics(t *testing.T) {
	_, w := New(0)
	w.Close()
	defer func() {
		if r := recover(); r == nil {
			t.Error("Fork on a closed End did not panic")
		}
	}()
	w.Fork()
}
