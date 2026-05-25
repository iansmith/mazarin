package transfer

import "unsafe"

// Bytes returns a []byte view over the handle's pages. The slice aliases the
// underlying VA region; the caller must not retain it past the handle's
// Commit (mode 1) / Release / RevokeWrite (modes 2/3).
//
// Lifetime: the kernel keeps the pages mapped at h.VA until Commit. A Go
// slice built via unsafe.Slice doesn't root the underlying memory for GC,
// but the kernel mapping is GC-independent — Go's GC only sees the slice
// header, not the bytes.
func (h Handle) Bytes() []byte {
	if h.Pages == 0 {
		return nil
	}
	return unsafe.Slice((*byte)(unsafe.Pointer(h.VA)), h.Pages*PageSize)
}

// Writer returns an io.Writer over the handle's byte range. Each Write
// advances the position; Writing past the reserved capacity returns
// ErrShortBuffer with n == 0 (atomic — partial writes don't commit).
func (h Handle) Writer() *Writer {
	return &Writer{h: h}
}

// Write copies p into the handle's pages at the current position.
// On overflow returns (0, ErrShortBuffer) without committing any bytes.
func (w *Writer) Write(p []byte) (int, error) {
	capacity := w.h.Pages * PageSize
	if w.written+len(p) > capacity {
		return 0, ErrShortBuffer
	}
	copy(w.h.Bytes()[w.written:], p)
	w.written += len(p)
	return len(p), nil
}

// Written reports the number of bytes written so far via Write.
func (w *Writer) Written() int {
	return w.written
}
