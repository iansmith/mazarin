// MAZ-121 Phase 0: red tests for the linux shepherd's Unix-pipe data plane.
//
// These tests describe the expected post-implementation behavior of the pipe
// package: a pipe2-created (read, write) End pair, O_CLOEXEC/O_NONBLOCK flag
// propagation, EOF on writer close, and the would-block signal a blocking read
// reports so the shepherd can park (deferred-Reply). They fail against the
// stub in pipe.go — that is the point (RED). The implementation phase makes
// them pass.
//
// Mapping to the ticket's named RED tests:
//   - TestPipe2CreatesLinkedPair   → write to write-end, read from read-end
//                                     returns the bytes in order.
//   - TestPipe2OCloexecSetsBit     → both ends Cloexec()==true with O_CLOEXEC;
//                                     false without.
//   - TestPipeEOFWhenWriteEndClosed→ close write-end → read returns 0/EOF.
//   - TestPipeReadBlocksThenWakes  → read with no data + no closed writer
//                                     reports "would block / park".

package pipe

import (
	"bytes"
	"errors"
	"testing"
)

// TestPipe2CreatesLinkedPair verifies the (read, write) pair is linked:
// bytes written to the write end are readable on the read end, in FIFO order.
func TestPipe2CreatesLinkedPair(t *testing.T) {
	r, w := New(0)
	if r == nil || w == nil {
		t.Fatal("New returned a nil end")
	}

	msg := []byte("hello pipe")
	n, err := w.Write(msg)
	if err != nil {
		t.Fatalf("Write: unexpected error %v", err)
	}
	if n != len(msg) {
		t.Fatalf("Write: wrote %d bytes, want %d", n, len(msg))
	}

	got := make([]byte, len(msg))
	rn, rerr := r.Read(got)
	if rerr != nil {
		t.Fatalf("Read: unexpected error %v", rerr)
	}
	if rn != len(msg) {
		t.Fatalf("Read: read %d bytes, want %d", rn, len(msg))
	}
	if !bytes.Equal(got[:rn], msg) {
		t.Fatalf("Read: got %q, want %q", got[:rn], msg)
	}
}

// TestPipe2OCloexecSetsBit verifies O_CLOEXEC propagation to both ends.
func TestPipe2OCloexecSetsBit(t *testing.T) {
	// With O_CLOEXEC: both ends report Cloexec()==true.
	rc, wc := New(OCLOEXEC)
	if !rc.Cloexec() {
		t.Error("read end: Cloexec()==false with O_CLOEXEC, want true")
	}
	if !wc.Cloexec() {
		t.Error("write end: Cloexec()==false with O_CLOEXEC, want true")
	}

	// Without O_CLOEXEC: both ends report Cloexec()==false.
	r, w := New(0)
	if r.Cloexec() {
		t.Error("read end: Cloexec()==true without O_CLOEXEC, want false")
	}
	if w.Cloexec() {
		t.Error("write end: Cloexec()==true without O_CLOEXEC, want false")
	}
}

// TestPipe2ONonblockSetsBit verifies O_NONBLOCK propagation to both ends.
// (The ticket calls out honoring O_NONBLOCK alongside O_CLOEXEC.)
func TestPipe2ONonblockSetsBit(t *testing.T) {
	r, w := New(ONONBLOCK)
	if !r.Nonblock() {
		t.Error("read end: Nonblock()==false with O_NONBLOCK, want true")
	}
	if !w.Nonblock() {
		t.Error("write end: Nonblock()==false with O_NONBLOCK, want true")
	}

	rb, wb := New(0)
	if rb.Nonblock() {
		t.Error("read end: Nonblock()==true without O_NONBLOCK, want false")
	}
	if wb.Nonblock() {
		t.Error("write end: Nonblock()==true without O_NONBLOCK, want false")
	}
}

// TestPipeEOFWhenWriteEndClosed verifies that once the write end is closed and
// the buffer is drained, the read end returns 0 bytes with no error (EOF).
func TestPipeEOFWhenWriteEndClosed(t *testing.T) {
	r, w := New(0)

	// Buffered data is still readable after the writer closes.
	msg := []byte("tail bytes")
	if _, err := w.Write(msg); err != nil {
		t.Fatalf("Write: unexpected error %v", err)
	}
	w.Close()

	got := make([]byte, len(msg))
	n, err := r.Read(got)
	if err != nil {
		t.Fatalf("Read (drain after close): unexpected error %v", err)
	}
	if n != len(msg) || !bytes.Equal(got[:n], msg) {
		t.Fatalf("Read (drain after close): got %q, want %q", got[:n], msg)
	}

	// Buffer now empty + writer closed → EOF: 0 bytes, nil error, NOT would-block.
	n, err = r.Read(got)
	if n != 0 {
		t.Fatalf("Read at EOF: got %d bytes, want 0", n)
	}
	if err != nil {
		t.Fatalf("Read at EOF: got error %v, want nil (EOF is 0,nil)", err)
	}
}

// TestPipeReadBlocksThenWakes verifies the would-block signal: a read with no
// buffered data AND at least one write end still open reports ErrWouldBlock
// (so the shepherd parks the request), and a later read after a write succeeds.
func TestPipeReadBlocksThenWakes(t *testing.T) {
	r, w := New(0)

	// No data, writer still open → would block (park).
	got := make([]byte, 8)
	n, err := r.Read(got)
	if n != 0 {
		t.Fatalf("Read on empty open pipe: got %d bytes, want 0", n)
	}
	if !errors.Is(err, ErrWouldBlock) {
		t.Fatalf("Read on empty open pipe: got error %v, want ErrWouldBlock", err)
	}

	// Writer writes → the same read end now returns the data (the "wake").
	if _, werr := w.Write([]byte("wake")); werr != nil {
		t.Fatalf("Write: unexpected error %v", werr)
	}
	n, err = r.Read(got)
	if err != nil {
		t.Fatalf("Read after write: unexpected error %v", err)
	}
	if string(got[:n]) != "wake" {
		t.Fatalf("Read after write: got %q, want %q", got[:n], "wake")
	}
}
