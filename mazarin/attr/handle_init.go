//go:build linux

// handle_init.go — Initialization of the client-side attribute library.
//
// Init() reads the SharedPageHeader from the well-known constraint page VA
// and creates a read-only PageRegion wrapper for shared-page attribute access.

package attr

import (
	"mazzy/mazarin/vm/flat"
	"mazzy/shared/constants"
	"unsafe"
)

// UserConstraintPagesVA is the fixed VA where constraint shared pages are
// mapped read-only into every priest's address space.
const UserConstraintPagesVA = 0x00007FFD00000000

// sharedPR is the package-level read-only PageRegion over constraint shared pages.
var sharedPR *flat.PageRegion

// sharedBase is the base VA of the shared pages (for trie walking).
var sharedBase uintptr

// trieBase is the VA of the trie region start.
var trieBase uintptr

// trieCap is the number of trie nodes available.
var trieCap uint16

// initialized tracks whether Init() has been called.
var handleInitialized bool

// Init sets up the client attribute library by reading the SharedPageHeader
// from the well-known constraint page VA. Must be called before using any
// Handle operations. Safe to call multiple times.
func Init() {
	if handleInitialized {
		return
	}

	sharedBase = UserConstraintPagesVA
	sharedPR = flat.NewPageRegionFromSharedMapping(sharedBase)
	if sharedPR == nil {
		panic("attr.Init: invalid shared page header (bad magic/version)")
	}

	trieBase, trieCap = flat.ReadTrieRegion(sharedBase)
	handleInitialized = true
}

// trieNode returns a read-only pointer to the TrieNode at the given index.
func trieNode(idx uint16) *constants.TrieNode {
	off := uintptr(idx) * constants.TrieNodeSize
	return (*constants.TrieNode)(unsafe.Pointer(trieBase + off))
}
