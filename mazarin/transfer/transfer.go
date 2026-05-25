// Package transfer provides cross-shepherd page-backed payload transport.
//
// Three modes are planned; this file currently stubs the foundation surface
// (MAZ-50). Mode 2 is MAZ-53; Mode 3 is MAZ-54. The umbrella is MAZ-55.
//
// PHASE 0 STATUS: types and signatures only. Method bodies return sentinel
// zero values or ErrNotImplemented so the test suite has a real RED state
// (runtime FAIL, not compile error). Real implementations land per the
// /ticket-plan work items.
package transfer

import (
	"errors"

	"mazzy/shared/sysid"
)

// PageSize is the page granularity for all transfers.
const PageSize = 4096

// ErrNotImplemented is returned by Phase 0 stubs.
var ErrNotImplemented = errors.New("transfer: not implemented yet")

// ErrShortBuffer is returned when a Writer is asked to write past the reserved size.
var ErrShortBuffer = errors.New("transfer: write past reserved size")

// ErrTruncated is returned by Decoder methods when the buffer ends mid-field.
var ErrTruncated = errors.New("transfer: truncated input")

// ShepherdID identifies a shepherd in IPC. Aliased to the existing sysid.ID
// while [[shepherd_id_overhaul_plan]] is in flight; may become a richer
// struct later.
type ShepherdID = sysid.ID

// Handle is the client-side view of a contiguous-VA span granted by a server.
type Handle struct {
	VA    uintptr // start of contiguous-VA span in caller's space
	Pages int     // page count caller may access
}

// Bytes returns a []byte view over the handle's pages. The slice aliases the
// underlying VA region; the caller must not retain it past the handle's
// release/commit.
func (h Handle) Bytes() []byte {
	return nil // Phase 0 stub
}

// Writer returns an io.Writer over the same range. Each Write advances the
// position. Writing past the reserved size returns ErrShortBuffer.
func (h Handle) Writer() *Writer {
	return &Writer{} // Phase 0 stub
}

// Commit releases the pages from the client's address space and notifies the
// server. The server's matching Wait() returns after Commit completes.
func (h Handle) Commit() error {
	return ErrNotImplemented
}

// Writer is an io.Writer over a Handle's byte range.
type Writer struct {
	h       Handle
	written int
}

// Write copies p into the handle's pages at the current position.
// Returns ErrShortBuffer if p would exceed the remaining capacity.
func (w *Writer) Write(p []byte) (int, error) {
	return 0, ErrNotImplemented // Phase 0 stub
}

// Written reports the number of bytes written so far via Write.
func (w *Writer) Written() int {
	return w.written
}

// Slab is the server-side view of a payload that the client has filled
// and committed (or that the server allocated for itself).
type Slab struct {
	// server-internal fields populated by Allocate
}

// Bytes returns the server-side []byte view of the Slab's pages.
func (s *Slab) Bytes() []byte {
	return nil // Phase 0 stub
}

// ClientView returns the sub-range of the Slab visible to the client.
// Excludes any server-internal extraPages bookkeeping.
func (s *Slab) ClientView() (va uintptr, pages int) {
	return 0, 0 // Phase 0 stub
}

// Release returns the pages to the allocator.
func (s *Slab) Release() error {
	return ErrNotImplemented
}

// Wait blocks until the client calls Commit on the matching Handle.
// Returns nil when the Slab's bytes are usable; non-nil on client crash.
func (s *Slab) Wait() error {
	return ErrNotImplemented
}

// Reserve is the client-side entry point of mode 1.
// Asks the server to allocate `size` bytes' worth of pages and map them
// into the client's address space contiguously, with caller-side write
// permission. The returned Handle is filled via h.Writer() then h.Commit().
func Reserve(server ShepherdID, kind uint32, size int) (Handle, error) {
	return Handle{}, ErrNotImplemented
}

// Allocate is the server-side entry point of mode 1.
// `extraPages` are additional pages allocated for server bookkeeping that
// the client never sees (e.g. protocol-http reserves a header-space page).
func Allocate(size, extraPages int) (*Slab, error) {
	return nil, ErrNotImplemented
}

// Encoder is the length-prefix encoder for IPC small fields — the part of
// an IPC message that fits in a single uring SQE. Blob bodies use the
// transfer modes (Reserve/Commit here, HandOff and GrantWrite later).
type Encoder struct {
	buf []byte
}

// NewEncoder wraps an externally-provided buf for encoding.
// The encoder appends to buf; use Bytes() to retrieve the result.
func NewEncoder(buf []byte) *Encoder {
	return &Encoder{buf: buf}
}

// Bytes returns the encoded bytes accumulated so far.
func (e *Encoder) Bytes() []byte {
	return e.buf
}

// String appends a length-prefixed string.
func (e *Encoder) String(s string) {
	// Phase 0 stub
}

// AppendBytes appends a length-prefixed byte sequence. (Named AppendBytes
// because the bare Bytes name is the accessor returning the encoded buffer.)
func (e *Encoder) AppendBytes(b []byte) {
	// Phase 0 stub
}

// Uint32 appends a fixed-width uint32.
func (e *Encoder) Uint32(v uint32) {
	// Phase 0 stub
}

// Handle appends a serialized blob reference (not the bytes themselves).
func (e *Encoder) Handle(h Handle) {
	// Phase 0 stub
}

// Decoder is the matching decoder.
type Decoder struct {
	buf []byte
	pos int
}

// NewDecoder wraps buf for decoding.
func NewDecoder(buf []byte) *Decoder {
	return &Decoder{buf: buf}
}

// String decodes a length-prefixed string.
func (d *Decoder) String() (string, error) {
	return "", ErrNotImplemented
}

// Bytes decodes a length-prefixed byte sequence.
func (d *Decoder) Bytes() ([]byte, error) {
	return nil, ErrNotImplemented
}

// Uint32 decodes a fixed-width uint32.
func (d *Decoder) Uint32() (uint32, error) {
	return 0, ErrNotImplemented
}

// Handle decodes a serialized blob reference.
func (d *Decoder) Handle() (Handle, error) {
	return Handle{}, ErrNotImplemented
}
