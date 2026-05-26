// share.go — Mode 2 Share surface (MAZ-53).
//
// PHASE 0 STATUS: type declarations + method signatures only. Bodies return
// sentinel errors or zero values so the red tests in share_test.go fail at
// runtime, not compile-time. Real implementations land per the /ticket-plan
// work items.
//
// Under the merged Mode 2 framing (MAZ-53 absorbs MAZ-54), Share publishes
// a kernel-level R/W mapping of a Slab (or a sub-range) into a consumer's
// address space. The sender retains ownership; kernel RefCount tracks total
// active mappings; consumers signal Release when done.

package transfer

import "errors"

// ShareID correlates a Share publication with the consumer's eventual Release.
// Sender-assigned; consumers echo it back in the Release IPC.
type ShareID uint32

// ErrShareRangeInvalid is returned when ShareRange's offset/length are out
// of bounds (negative, or extending past the Slab's total capacity).
var ErrShareRangeInvalid = errors.New("transfer: share range offset/length out of bounds")

// ErrShareReleased is returned when Release is called on an already-released
// Share. Idempotent semantic: a second Release returns nil, not this error.
// This sentinel exists for explicit checks if a caller wants to distinguish.
var ErrShareReleased = errors.New("transfer: share already released")

// Share is the consumer-side view of a server's published mapping.
//
// VA + Bytes describe the byte range the consumer has R/W access to. For a
// whole-Slab Share, Bytes equals the Slab's total capacity. For ShareRange,
// Bytes equals the length argument and may be sub-page sized.
//
// Bytes is an int field (not the Slab.Bytes() []byte method) because Share
// is byte-granular while Slab is page-granular. AsBytes() returns the slice.
type Share struct {
	VA    uintptr
	Bytes int

	// Internal — set by ReceiveShare for the Release IPC path.
	id        ShareID
	senderSID ShepherdID
	released  bool
}

// ShareRange exposes [offset, offset+length) bytes of the Slab as a R/W
// mapping in the consumer's address space.
//
// Kernel-level: calls sys.SharePagesWithTarget for the page(s) containing
// the byte range. Page granularity means non-aligned (offset, length) may
// expose neighboring bytes outside the logical range — see "v1 trust model"
// in the ticket description.
//
// Returns a ShareID the sender uses to track outstanding shares in its
// in-flight table.
func (s *Slab) ShareRange(with ShepherdID, offset, length int) (ShareID, error) {
	return 0, errors.New("transfer: ShareRange not implemented yet")
}

// Share is the whole-Slab convenience wrapper. Equivalent to:
//
//	s.ShareRange(with, 0, s.pages*PageSize)
//
// Per design: Share is implemented as a one-line delegate so there's exactly
// one real method and one parameter-validation site.
func (s *Slab) Share(with ShepherdID) (ShareID, error) {
	return s.ShareRange(with, 0, s.pages*PageSize)
}

// ReceiveShare blocks until the next Share IPC from `from` arrives. Returns
// a Share giving the consumer R/W access to the bytes the sender published.
//
// The Share carries the internal ShareID + senderSID needed by Release; the
// consumer holds onto it (as a *Share) until done.
func ReceiveShare(from ShepherdID) (*Share, error) {
	return nil, errors.New("transfer: ReceiveShare not implemented yet")
}

// AsBytes returns a R/W byte-slice view over the shared region. len(AsBytes())
// equals s.Bytes. The slice aliases the kernel-mapped memory; the caller must
// not retain it past Release.
func (s *Share) AsBytes() []byte {
	return nil
}

// Release tells the sender we're done with the share; the kernel can unmap
// from this address space and the sender can decrement its in-flight count.
//
// Idempotent: a second Release returns nil (not ErrShareReleased).
func (s *Share) Release() error {
	return errors.New("transfer: Release not implemented yet")
}
