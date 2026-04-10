// handle_find.go — Service discovery: Find and Exists.
//
// Find registers a query pattern with the kernel and returns the matching URIs.
// Exists checks if any attributes exist under a URI prefix.

package attr

import (
	"mazzy/mazarin/sys"
	"mazzy/mazarin/vm/flat"
	"mazzy/shared/constants"
)

// Find registers a query pattern and returns the current set of matching URIs.
// The pattern supports '*' as a single-segment wildcard.
// Example: "attr:///shepherd/*/str/*/layout/Parent"
func Find(pattern string) []string {
	slot, err := sys.AttrRegisterQuery(pattern)
	if err != nil {
		return nil
	}

	// Read the collection from the query result slot using seqlock.
	node := sharedPR.Node(int16(slot))
	var fv flat.Value
	for {
		seq := node.SeqCounter
		if seq&1 != 0 {
			continue // writer in progress
		}
		fv = node.CachedValue
		if node.SeqCounter == seq {
			break // consistent read
		}
	}

	if fv.Typ != flat.TypeCollection {
		return nil
	}

	ref := fv.AsCollRef()
	if ref.Count == 0 {
		return nil
	}
	result := make([]string, ref.Count)
	for i := 0; i < int(ref.Count); i++ {
		elem := sharedPR.ReadCollectionElement(ref, i)
		if elem.Typ == flat.TypeStr {
			sref := elem.AsStrRef()
			result[i] = sharedPR.ReadString(sref)
		}
	}
	return result
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
