// handle_find.go — Service discovery: Find and Exists.
//
// Find registers a query pattern with the kernel and returns the matching URIs.
// Exists checks if any attributes exist under a URI prefix.

package attr

import (
	"mazzy/mazarin/sys"
	"mazzy/shared/constants"
)

// Find registers a query pattern and returns the current set of matching URIs.
// The pattern supports '*' as a single-segment wildcard.
// Example: "attr:///shepherd/*/display/width"
func Find(pattern string) []string {
	slot, err := sys.AttrRegisterQuery(pattern)
	if err != nil {
		return nil
	}

	// Read the collection from the query result slot.
	node := sharedPR.Node(int16(slot))
	var fv = node.CachedValue // direct read, query result is always consistent
	if fv.Typ == 0 {
		return nil // empty result
	}

	// For now, return nil — collection reading will be available when the
	// kernel writes collection results (Phase 4+). The slot is registered
	// for future updates.
	_ = fv
	return nil
}

// Exists checks if any attributes exist under the given URI prefix.
// Walks the shared-page trie directly (no syscall).
func Exists(prefix string) bool {
	segments, nSeg := parseURIShared(prefix)
	if nSeg < 0 {
		return false
	}

	current := uint16(0) // root
	for i := 0; i < nSeg; i++ {
		child := findChildShared(current, segments[i])
		if child == constants.TrieNone {
			return false
		}
		current = child
	}

	node := trieNode(current)
	return node.ChildCount > 0 || node.AttrSlot != constants.TrieNone
}
