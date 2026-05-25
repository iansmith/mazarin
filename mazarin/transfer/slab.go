package transfer

import (
	"errors"
	"sync"
	"unsafe"

	"mazzy/mazarin/mem"
)

// MaxPagesPerSlab is the soft cap on a single Slab's total page count.
// 256 pages = 1 MiB. Reserve and Allocate both reject larger requests
// with ErrPageCapExceeded so callers can distinguish "too big" from
// "kernel out of memory".
const MaxPagesPerSlab = 256

// ErrPageCapExceeded is returned when a Reserve/Allocate request would
// exceed MaxPagesPerSlab.
var ErrPageCapExceeded = errors.New("transfer: page cap exceeded (>256 pages)")

// ErrSlabReleased is returned by Slab methods invoked after Release.
var ErrSlabReleased = errors.New("transfer: slab already released")

// ErrExtraPagesUnsupported is returned by Allocate when extraPages > 0.
// v1 of MAZ-50 supports extraPages == 0 only; see the comment in Allocate
// for the contiguity-after-Commit constraint that blocks the general case.
var ErrExtraPagesUnsupported = errors.New("transfer: extraPages > 0 not supported in v1 (file a kernel SVC enhancement)")

// ErrCommitTimeout is returned by Slab.Wait when the client doesn't Commit
// within the timeout window. Indicates a likely client crash mid-fill.
var ErrCommitTimeout = errors.New("transfer: commit timeout (client crashed mid-fill?)")

// slabState tracks where the Slab's pages currently live.
type slabState uint8

const (
	slabAllocated      slabState = iota // pages live in server's space; no client yet
	slabMappedToClient                  // pages transferred to client; server.Bytes unsafe
	slabCommitted                       // client called Commit; pages back in server's space
	slabReleased                        // pages freed; Slab unusable
)

// Slab is the server-side view of a payload that the client has filled
// and committed (mode 1), or that the server allocated for itself.
//
// Page layout:
//
//	[extraPages][body pages]
//	            ^
//	            ClientView starts here. The body region is what the client
//	            sees; the leading extraPages are server-only bookkeeping
//	            (e.g. protocol-http reserves one page for HTTP headers
//	            it builds after the body is committed).
type Slab struct {
	// mu guards state + va across the dispatcher commit path and the
	// waiter/reader paths. pages and extraPages are immutable after
	// Allocate and can be read without the lock.
	mu sync.Mutex

	va         uintptr // start of the full allocation (includes extraPages)
	pages      int     // total pages allocated (body + extraPages)
	extraPages int     // server-only leading pages
	state      slabState
	commitCh   chan error // signalled by the dispatcher when Commit arrives

	// server + serverKey are populated by the dispatcher after Allocate so
	// the timeout path in WaitTimeout can evict the pending entry. Nil if
	// the Slab wasn't produced by a Reserve handler (e.g. test fixtures).
	server    *Server
	serverKey pendingKey
}

// Allocate is the server-side entry point of mode 1.
//
// size is the body byte count requested by the client. extraPages are
// additional pages the server reserves for its own bookkeeping; the
// client never sees them. Total pages = ceil(size / PageSize) + extraPages,
// capped at MaxPagesPerSlab.
//
// The returned Slab is in slabAllocated state — pages live in the server's
// address space. The Mode 1 IPC dispatcher will transition it to
// slabMappedToClient when the matching Reserve message is handled.
func Allocate(size, extraPages int) (*Slab, error) {
	if size < 0 || extraPages < 0 {
		return nil, errors.New("transfer: negative size or extraPages")
	}
	if extraPages != 0 {
		// v1 limitation: extraPages > 0 needs a kernel SVC that lets the
		// server pin a target VA on TransferPages so the body region lands
		// contiguous with the extraPages prefix after Commit. Without that,
		// the post-Commit Slab fragments across two disjoint VA ranges.
		// Tracked in the deferred list of MAZ-50; MAZ-49 will pick this up
		// when protocol-http actually needs the headers-prefix layout.
		return nil, ErrExtraPagesUnsupported
	}
	bodyPages := (size + PageSize - 1) / PageSize
	total := bodyPages + extraPages
	if total == 0 {
		total = 1 // a Slab must own at least one page even for size=0 requests
	}
	if total > MaxPagesPerSlab {
		return nil, ErrPageCapExceeded
	}
	ptr, err := mem.AllocPages(total, mem.PageShared)
	if err != nil {
		return nil, err
	}
	return &Slab{
		va:         uintptr(ptr),
		pages:      total,
		extraPages: extraPages,
		state:      slabAllocated,
		commitCh:   make(chan error, 1),
	}, nil
}

// Bytes returns the server-side []byte view of the Slab's full allocation,
// including any leading extraPages. Valid while the Slab is in slabAllocated
// or slabCommitted state; returns nil while the pages are mapped to a client
// (slabMappedToClient) because they're not in the server's address space.
//
// After Release, returns nil.
func (s *Slab) Bytes() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state == slabReleased || s.state == slabMappedToClient {
		return nil
	}
	return unsafe.Slice((*byte)(unsafe.Pointer(s.va)), s.pages*PageSize)
}

// ClientView returns the VA and page count of the body region — what the
// client will see after Reserve completes. Excludes the server-only
// extraPages prefix.
func (s *Slab) ClientView() (va uintptr, pages int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state == slabReleased {
		return 0, 0
	}
	return s.va + uintptr(s.extraPages*PageSize), s.pages - s.extraPages
}

// Release returns the Slab's pages to the allocator. Idempotent: a second
// Release returns nil.
func (s *Slab) Release() error {
	s.mu.Lock()
	if s.state == slabReleased {
		s.mu.Unlock()
		return nil
	}
	va := s.va
	pages := s.pages
	s.state = slabReleased
	s.mu.Unlock()
	// FreePages outside the lock — it's a syscall and shouldn't be held
	// while other goroutines may be checking state.
	return mem.FreePages(unsafe.Pointer(va), pages)
}
