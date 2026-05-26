// Package transfer provides cross-shepherd page-backed payload transport.
//
// Two modes share a common foundation:
//   - Mode 1 (Reserve/Commit): client → server. Implemented here (MAZ-50).
//   - Mode 2 (Share/Reshare):  sender publishes a R/W mapping into one or
//     more consumers' address spaces; sender keeps ownership. Whole-Slab and
//     sub-range variants live in share.go (MAZ-53). MAZ-54 was collapsed
//     into MAZ-53.
//
// See design/mazarin-transfer-state-machine.md for the page-ownership state
// machine, crash semantics, and how Mode 2 extends the foundation.
// Umbrella ticket: MAZ-55.
package transfer

import (
	"errors"

	"mazzy/shared/sysid"
)

// PageSize is the page granularity for all transfers.
const PageSize = 4096

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

// Handle.Bytes, Handle.Writer, Writer.Write, and Handle.Commit live in
// handle.go / client.go.

// Writer is an io.Writer over a Handle's byte range. Write/Written are in
// handle.go alongside the rest of the Handle surface.
type Writer struct {
	h       Handle
	written int
}

// Slab, Allocate, and Slab.{Bytes,ClientView,Release} live in slab.go.
// Slab.Wait lives in server.go alongside the rest of the server-side
// dispatcher integration. Reserve / Handle.Commit live in client.go.

// Encoder and Decoder live in encoder.go / decoder.go.
