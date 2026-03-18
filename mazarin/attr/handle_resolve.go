//go:build linux

// handle_resolve.go — AttrResolver backed by shared-page reads.
//
// sharedResolver implements vm.AttrResolver using direct reads from the
// constraint shared pages (trie walking + seqlock-protected value reads).

package attr

import (
	"mazzy/mazarin/sys"
	"mazzy/mazarin/vm"
	"mazzy/mazarin/vm/flat"
	"mazzy/shared/constants"
)

// constraintEvaluators maps slot → evaluate-if-dirty function for local
// constraint handles. Used by Deref to cascade evaluation when a deref'd
// attribute is a dirty constraint that hasn't been evaluated yet.
var constraintEvaluators = make(map[uint16]func())

// registerConstraintEvaluator registers a cascade evaluator for a constraint slot.
func registerConstraintEvaluator(slot uint16, fn func()) {
	constraintEvaluators[slot] = fn
}

// sharedResolver implements vm.AttrResolver backed by shared constraint pages.
type sharedResolver struct {
	pr      *flat.PageRegion
	readSet *vm.ReadSet // injected by caller; nil = don't track
}

// Deref resolves a URI to a typed value by walking the shared-page trie and
// reading the attribute's CachedValue via seqlock.
func (r *sharedResolver) Deref(uri string, expectedType uint8) (vm.Value, uint16, bool) {
	slot, found := trieLookupShared(uri)
	if !found {
		return vm.Value{}, 0, false
	}

	// Cascade: if the target slot is a local dirty constraint, evaluate it first.
	if fn, ok := constraintEvaluators[slot]; ok {
		fn()
	}

	node := r.pr.Node(int16(slot))

	// Check type matches the expected flat type.
	vmType := flat.FlatTypeToVM(expectedType)
	if vmType == 0 {
		vmType = expectedType // direct match for scalar types (1-5)
	}

	// Seqlock read.
	var fv flat.FlatValue
	for {
		seq := node.SeqCounter
		if seq&1 != 0 {
			continue
		}
		fv = node.CachedValue
		if node.SeqCounter == seq {
			break
		}
	}

	// Convert to vm.Value.
	val, err := flat.FlatToValue(fv, r.pr)
	if err != nil {
		return vm.Value{}, 0, false
	}

	// Record in read set.
	if r.readSet != nil {
		r.readSet.Add(slot)
	}

	return val, slot, true
}

// Find matches a URI pattern by calling the kernel's query registration syscall.
// Returns matching URIs and the query result slot.
func (r *sharedResolver) Find(pattern string) ([]string, uint16) {
	slot, err := sys.AttrRegisterQuery(pattern)
	if err != nil {
		return nil, 0
	}

	// Record query slot in read set.
	if r.readSet != nil {
		r.readSet.Add(slot)
	}

	// Read the collection from the query result slot.
	node := r.pr.Node(int16(slot))
	var fv flat.FlatValue
	for {
		seq := node.SeqCounter
		if seq&1 != 0 {
			continue
		}
		fv = node.CachedValue
		if node.SeqCounter == seq {
			break
		}
	}

	if fv.Typ != flat.TypeCollection {
		return nil, slot
	}

	ref := fv.AsCollRef()
	result := make([]string, ref.Count)
	for i := 0; i < int(ref.Count); i++ {
		elem := r.pr.ReadCollectionElement(ref, i)
		if elem.Typ == flat.TypeStr {
			sref := elem.AsStrRef()
			result[i] = r.pr.ReadString(sref)
		}
	}
	return result, slot
}

// Exists checks if any attributes exist under the URI prefix by walking the
// shared-page trie and checking ChildCount.
func (r *sharedResolver) Exists(prefix string) (bool, uint16) {
	// Walk the trie to find the node at the prefix.
	segments, nSeg := parseURIShared(prefix)
	if nSeg < 0 {
		return false, 0
	}

	current := uint16(0) // root
	for i := 0; i < nSeg; i++ {
		child := findChildShared(current, segments[i])
		if child == constants.TrieNone {
			return false, 0
		}
		current = child
	}

	node := trieNode(current)
	return node.ChildCount > 0, current
}

// IncrementI64 atomically increments an int64 value attribute by URI.
// Uses trieLookupShared to resolve URI→slot, then calls the kernel syscall.
// Does NOT add to read set — intended for side-effect counters.
func (r *sharedResolver) IncrementI64(uri string) (int64, bool) {
	slot, found := trieLookupShared(uri)
	if !found {
		return 0, false
	}
	newVal, err := sys.AttrIncrementI64(slot)
	if err != nil {
		return 0, false
	}
	return newVal, true
}

// --- Trie walking on shared pages ---

// trieLookupShared looks up a URI in the shared-page trie (read-only).
func trieLookupShared(uri string) (uint16, bool) {
	segments, nSeg := parseURIShared(uri)
	if nSeg < 0 {
		return constants.TrieNone, false
	}

	current := uint16(0) // root
	for i := 0; i < nSeg; i++ {
		child := findChildShared(current, segments[i])
		if child == constants.TrieNone {
			return constants.TrieNone, false
		}
		current = child
	}

	leaf := trieNode(current)
	if leaf.AttrSlot == constants.TrieNone {
		return constants.TrieNone, false
	}
	return leaf.AttrSlot, true
}

// findChildShared searches for a child node with the given segment.
func findChildShared(parentIdx uint16, seg string) uint16 {
	parent := trieNode(parentIdx)
	childIdx := parent.FirstChild
	for childIdx != constants.TrieNone {
		child := trieNode(childIdx)
		if segmentMatchShared(&child.Segment, seg) {
			return childIdx
		}
		childIdx = child.NextSibling
	}
	return constants.TrieNone
}

// segmentMatchShared returns true if the trie node's segment matches the string.
func segmentMatchShared(seg *[constants.TrieSegmentLen]byte, s string) bool {
	for i := 0; i < len(s); i++ {
		if i >= constants.TrieSegmentLen || seg[i] != s[i] {
			return false
		}
	}
	if len(s) >= constants.TrieSegmentLen {
		return false
	}
	return seg[len(s)] == 0
}

// parseURIShared parses a URI into segments. Returns segments and count.
func parseURIShared(uri string) ([constants.MaxURISegments]string, int) {
	var segments [constants.MaxURISegments]string
	prefix := constants.URIPrefix
	if len(uri) < len(prefix) {
		return segments, -1
	}
	for i := 0; i < len(prefix); i++ {
		if uri[i] != prefix[i] {
			return segments, -1
		}
	}

	rest := uri[len(prefix):]
	if len(rest) == 0 {
		return segments, -1
	}

	count := 0
	start := 0
	for i := 0; i <= len(rest); i++ {
		if i == len(rest) || rest[i] == '/' {
			if i == start {
				if i == len(rest) {
					break // trailing slash OK
				}
				return segments, -1 // empty internal segment
			}
			segLen := i - start
			if segLen >= constants.TrieSegmentLen {
				return segments, -1
			}
			if count >= constants.MaxURISegments {
				return segments, -1
			}
			segments[count] = rest[start:i]
			count++
			start = i + 1
		}
	}

	if count == 0 {
		return segments, -1
	}
	return segments, count
}
