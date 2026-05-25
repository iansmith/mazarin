// Phase 0 red tests for MAZ-50 — mazarin/transfer foundation.
//
// These tests describe the expected post-implementation behavior of the
// userspace-only foundation pieces: the length-prefix Encoder/Decoder, the
// Writer bounds invariants, and the Handle.Bytes view that aliases the
// underlying VA region.
//
// They fail on the Phase 0 stub. They turn green when the foundation work
// items land. Mode 1 Reserve/Commit lifecycle + the kernel-side cross-VA
// mapping primitive are exercised by a separate boot-integration test
// (see the /ticket-plan work items).
package transfer

import (
	"bytes"
	"errors"
	"testing"
	"unsafe"
)

// --- Encoder/Decoder roundtrips ---------------------------------------------

func TestEncoderDecoder_StringRoundtrip(t *testing.T) {
	cases := []string{
		"",
		"hello",
		"unicode: λ µ ✓",
		string(make([]byte, 1024)), // 1 KiB
	}
	for _, want := range cases {
		t.Run("len="+itoa(len(want)), func(t *testing.T) {
			e := NewEncoder(nil)
			e.String(want)
			d := NewDecoder(e.Bytes())
			got, err := d.String()
			if err != nil {
				t.Fatalf("Decoder.String returned err=%v", err)
			}
			if got != want {
				t.Fatalf("roundtrip: want %q got %q", want, got)
			}
		})
	}
}

func TestEncoderDecoder_BytesRoundtrip(t *testing.T) {
	want := []byte{0x00, 0xff, 0x42, 0x13, 0x37}
	e := NewEncoder(nil)
	e.AppendBytes(want)
	d := NewDecoder(e.Bytes())
	got, err := d.Bytes()
	if err != nil {
		t.Fatalf("Decoder.Bytes returned err=%v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("roundtrip: want %v got %v", want, got)
	}
}

func TestEncoderDecoder_Uint32Roundtrip(t *testing.T) {
	cases := []uint32{0, 1, 42, 0xdeadbeef, 0xffffffff}
	for _, want := range cases {
		e := NewEncoder(nil)
		e.Uint32(want)
		d := NewDecoder(e.Bytes())
		got, err := d.Uint32()
		if err != nil {
			t.Fatalf("Decoder.Uint32 returned err=%v for %#x", err, want)
		}
		if got != want {
			t.Fatalf("roundtrip: want %#x got %#x", want, got)
		}
	}
}

func TestEncoderDecoder_HandleRoundtrip(t *testing.T) {
	want := Handle{VA: 0x4000_0000, Pages: 42}
	e := NewEncoder(nil)
	e.Handle(want)
	d := NewDecoder(e.Bytes())
	got, err := d.Handle()
	if err != nil {
		t.Fatalf("Decoder.Handle returned err=%v", err)
	}
	if got != want {
		t.Fatalf("roundtrip: want %+v got %+v", want, got)
	}
}

func TestEncoderDecoder_MixedFields(t *testing.T) {
	wantS := "mixed"
	wantB := []byte{1, 2, 3}
	wantU := uint32(0xcafebabe)
	wantH := Handle{VA: 0x9000_0000, Pages: 7}

	e := NewEncoder(nil)
	e.String(wantS)
	e.AppendBytes(wantB)
	e.Uint32(wantU)
	e.Handle(wantH)

	d := NewDecoder(e.Bytes())
	gotS, err := d.String()
	if err != nil || gotS != wantS {
		t.Fatalf("String: want %q nil-err, got %q err=%v", wantS, gotS, err)
	}
	gotB, err := d.Bytes()
	if err != nil || !bytes.Equal(gotB, wantB) {
		t.Fatalf("Bytes: want %v nil-err, got %v err=%v", wantB, gotB, err)
	}
	gotU, err := d.Uint32()
	if err != nil || gotU != wantU {
		t.Fatalf("Uint32: want %#x nil-err, got %#x err=%v", wantU, gotU, err)
	}
	gotH, err := d.Handle()
	if err != nil || gotH != wantH {
		t.Fatalf("Handle: want %+v nil-err, got %+v err=%v", wantH, gotH, err)
	}
}

func TestDecoder_TruncatedInput(t *testing.T) {
	// A decoder fed a buffer that doesn't contain a full length-prefixed
	// string must return ErrTruncated, NOT panic or read past the end.
	d := NewDecoder([]byte{0x00, 0x00, 0x00, 0x05, 'h', 'i'}) // claims 5 bytes, only 2 present
	_, err := d.String()
	if !errors.Is(err, ErrTruncated) {
		t.Fatalf("expected ErrTruncated on truncated input, got err=%v", err)
	}
}

// --- Writer bounds ----------------------------------------------------------

// fakeHandle allocates a backing []byte and constructs a Handle pointing at
// it. Used to exercise Writer/Bytes without the kernel-side IPC machinery.
func fakeHandle(t *testing.T, pages int) (Handle, []byte) {
	t.Helper()
	backing := make([]byte, pages*PageSize)
	return Handle{VA: uintptr(unsafe.Pointer(&backing[0])), Pages: pages}, backing
}

func TestWriter_WriteAdvancesPosition(t *testing.T) {
	h, _ := fakeHandle(t, 1)
	w := h.Writer()
	n, err := w.Write([]byte("hello"))
	if err != nil {
		t.Fatalf("Write err=%v", err)
	}
	if n != 5 {
		t.Fatalf("Write n: want 5 got %d", n)
	}
	if got := w.Written(); got != 5 {
		t.Fatalf("Written: want 5 got %d", got)
	}
	n, err = w.Write([]byte(" world"))
	if err != nil {
		t.Fatalf("second Write err=%v", err)
	}
	if n != 6 {
		t.Fatalf("second Write n: want 6 got %d", n)
	}
	if got := w.Written(); got != 11 {
		t.Fatalf("Written after two writes: want 11 got %d", got)
	}
}

func TestWriter_WritePastCapacityReturnsErrShortBuffer(t *testing.T) {
	h, _ := fakeHandle(t, 1) // 4096 bytes capacity
	w := h.Writer()
	if _, err := w.Write(make([]byte, PageSize)); err != nil {
		t.Fatalf("filling capacity: unexpected err=%v", err)
	}
	// One more byte must fail with ErrShortBuffer.
	n, err := w.Write([]byte{0x42})
	if !errors.Is(err, ErrShortBuffer) {
		t.Fatalf("write past capacity: want ErrShortBuffer, got n=%d err=%v", n, err)
	}
}

func TestWriter_PartialWriteAtBoundary(t *testing.T) {
	h, _ := fakeHandle(t, 1) // 4096 bytes capacity
	w := h.Writer()
	if _, err := w.Write(make([]byte, PageSize-3)); err != nil {
		t.Fatalf("priming: unexpected err=%v", err)
	}
	// 5 more bytes — only 3 fit. Behavior: ErrShortBuffer, n reports
	// however many bytes were committed (0 or 3 — implementation chooses,
	// but the error is what's load-bearing here).
	n, err := w.Write([]byte("hello"))
	if !errors.Is(err, ErrShortBuffer) {
		t.Fatalf("partial-fit write: want ErrShortBuffer, got n=%d err=%v", n, err)
	}
}

// --- Handle.Bytes view ------------------------------------------------------

func TestHandleBytes_LengthMatchesPages(t *testing.T) {
	for _, pages := range []int{1, 4, 16} {
		h, _ := fakeHandle(t, pages)
		got := h.Bytes()
		want := pages * PageSize
		if len(got) != want {
			t.Fatalf("Bytes len for %d pages: want %d got %d", pages, want, len(got))
		}
	}
}

func TestHandleBytes_AliasesUnderlyingMemory(t *testing.T) {
	h, backing := fakeHandle(t, 1)
	view := h.Bytes()
	// Write through Bytes view, observe via backing.
	view[0] = 0x11
	view[PageSize-1] = 0x22
	if backing[0] != 0x11 || backing[PageSize-1] != 0x22 {
		t.Fatalf("Bytes view does not alias backing memory: backing[0]=%#x backing[end]=%#x",
			backing[0], backing[PageSize-1])
	}
	// Write through backing, observe via view.
	backing[42] = 0x33
	if view[42] != 0x33 {
		t.Fatalf("backing write not visible through Bytes view: view[42]=%#x", view[42])
	}
}

// --- Tiny helpers (avoid pulling strconv into a focused test file) ----------

// itoa stringifies a non-negative int. Only used to label subtests with
// string lengths, which are always >= 0.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
