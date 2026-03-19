// constraint_trie.go — Flat, pointer-free namespace trie in shared constraint pages.
//
// Each trie node is 128 bytes, stored in the trie region of the shared pages.
// The root node (index 0) has an empty segment; its children are "shepherd" and
// "kernel" (created during init). URI parsing splits on '/', skipping the
// "attr:///" prefix.

package ksyscall

import (
	"mazzy/kmazarin/serial"
	"mazzy/shared/constants"
	"unsafe"
)

// TrieNode is re-exported from shared/constants for kernel use.
type TrieNode = constants.TrieNode

// Local aliases for trie constants.
const trieNodeSize = constants.TrieNodeSize
const trieSegmentLen = constants.TrieSegmentLen
const trieNone = constants.TrieNone

// trieNode returns a writable pointer to the TrieNode at the given index.
//
//go:nosplit
func (mgr *KernelAttrManager) trieNode(idx uint16) *TrieNode {
	off := uintptr(mgr.trieRegionOff) + uintptr(idx)*trieNodeSize
	return (*TrieNode)(unsafe.Pointer(mgr.baseVA + off))
}

// allocTrieNode allocates a trie node from the bitmap.
// Returns node index or trieNone if full.
//
//go:nosplit
func (mgr *KernelAttrManager) allocTrieNode() uint16 {
	for byteIdx := 0; byteIdx < len(mgr.trieBitmap); byteIdx++ {
		b := mgr.trieBitmap[byteIdx]
		if b == 0xFF {
			continue
		}
		for bit := uint(0); bit < 8; bit++ {
			if b&(1<<bit) == 0 {
				idx := uint16(byteIdx*8) + uint16(bit)
				if idx >= mgr.trieCapacity {
					return trieNone
				}
				mgr.trieBitmap[byteIdx] |= 1 << bit
				// Zero the node
				n := mgr.trieNode(idx)
				*n = TrieNode{
					FirstChild:  trieNone,
					NextSibling: trieNone,
					AttrSlot:    trieNone,
				}
				return idx
			}
		}
	}
	return trieNone
}

// freeTrieNode releases a trie node back to the bitmap.
//
//go:nosplit
func (mgr *KernelAttrManager) freeTrieNode(idx uint16) {
	byteIdx := idx / 8
	bit := idx % 8
	mgr.trieBitmap[byteIdx] &^= 1 << bit
}

// initTrie initializes the root node and top-level children ("shepherd", "kernel").
func initTrie() {
	// Allocate root (always slot 0).
	root := attrMgr.allocTrieNode()
	if root != 0 {
		serial.RawUARTPuts("[attr] trie root not slot 0!\r\n")
		return
	}
	rootNode := attrMgr.trieNode(0)
	rootNode.Segment[0] = 0 // empty segment = root

	// Allocate "shepherd" child.
	shepherdIdx := attrMgr.allocTrieNode()
	if shepherdIdx == trieNone {
		serial.RawUARTPuts("[attr] trie: cannot alloc shepherd node\r\n")
		return
	}
	pn := attrMgr.trieNode(shepherdIdx)
	copySegment(&pn.Segment, "shepherd")
	rootNode.FirstChild = shepherdIdx

	// Allocate "kernel" child as sibling of "shepherd".
	kernelIdx := attrMgr.allocTrieNode()
	if kernelIdx == trieNone {
		serial.RawUARTPuts("[attr] trie: cannot alloc kernel node\r\n")
		return
	}
	kn := attrMgr.trieNode(kernelIdx)
	copySegment(&kn.Segment, "kernel")
	pn.NextSibling = kernelIdx
}

// copySegment copies a Go string into a fixed-size NUL-terminated segment.
//
//go:nosplit
func copySegment(dst *[trieSegmentLen]byte, s string) {
	n := len(s)
	if n >= trieSegmentLen {
		n = trieSegmentLen - 1
	}
	for i := 0; i < n; i++ {
		dst[i] = s[i]
	}
	dst[n] = 0
}

// segmentMatch returns true if the trie node's segment matches the given string.
//
//go:nosplit
func segmentMatch(seg *[trieSegmentLen]byte, s string) bool {
	for i := 0; i < len(s); i++ {
		if i >= trieSegmentLen || seg[i] != s[i] {
			return false
		}
	}
	// s is exhausted; check that seg is NUL at that position
	if len(s) >= trieSegmentLen {
		return false
	}
	return seg[len(s)] == 0
}

// parseURI splits a URI into segments. Returns segment count, or -1 on error.
// Segments are written into the provided buffer.
// Valid format: "attr:///shepherd/<name>/..." or "attr:///kernel/..."
//
//go:nosplit
func parseURI(uri string, segments *[maxURISegments]string) int {
	const prefix = "attr:///"
	if len(uri) < len(prefix) {
		return -1
	}
	for i := 0; i < len(prefix); i++ {
		if uri[i] != prefix[i] {
			return -1
		}
	}

	rest := uri[len(prefix):]
	if len(rest) == 0 {
		return -1 // no segments after prefix
	}

	count := 0
	start := 0
	for i := 0; i <= len(rest); i++ {
		if i == len(rest) || rest[i] == '/' {
			if i == start {
				// Empty segment (double slash or trailing slash)
				if i == len(rest) {
					break // trailing slash is OK, just ignore
				}
				return -1 // empty internal segment
			}
			segLen := i - start
			if segLen >= trieSegmentLen {
				return -1 // segment too long
			}
			if count >= maxURISegments {
				return -1 // too many segments
			}
			segments[count] = rest[start:i]
			count++
			start = i + 1
		}
	}

	if count == 0 {
		return -1
	}
	return count
}

// trieInsert inserts an attribute URI into the trie, associating it with attrSlot.
// Returns 0 on success, or a negative errno.
func (mgr *KernelAttrManager) trieInsert(uri string, attrSlot uint16) int64 {
	var segments [maxURISegments]string
	nSeg := parseURI(uri, &segments)
	if nSeg < 0 {
		return -22 // EINVAL
	}

	// Walk/create path from root.
	current := uint16(0) // root
	for i := 0; i < nSeg; i++ {
		seg := segments[i]
		child := mgr.findChild(current, seg)
		if child == trieNone {
			// Create new trie node.
			child = mgr.allocTrieNode()
			if child == trieNone {
				return -12 // ENOMEM
			}
			cn := mgr.trieNode(child)
			copySegment(&cn.Segment, seg)
			// Prepend as first child of current node.
			parent := mgr.trieNode(current)
			cn.NextSibling = parent.FirstChild
			parent.FirstChild = child
		}
		current = child
	}

	// current is the leaf node — set the attribute slot.
	leaf := mgr.trieNode(current)
	if leaf.AttrSlot != trieNone {
		return -17 // EEXIST
	}
	leaf.AttrSlot = attrSlot

	// Increment ChildCount on all ancestors.
	current = uint16(0)
	for i := 0; i < nSeg; i++ {
		node := mgr.trieNode(current)
		node.ChildCount++
		current = mgr.findChild(current, segments[i])
	}

	mgr.bumpGeneration()
	return 0
}

// trieLookup looks up a URI in the trie.
// Returns (attrSlot, true) if found, or (trieNone, false) if not.
func (mgr *KernelAttrManager) trieLookup(uri string) (uint16, bool) {
	var segments [maxURISegments]string
	nSeg := parseURI(uri, &segments)
	if nSeg < 0 {
		return trieNone, false
	}

	current := uint16(0) // root
	for i := 0; i < nSeg; i++ {
		child := mgr.findChild(current, segments[i])
		if child == trieNone {
			return trieNone, false
		}
		current = child
	}

	leaf := mgr.trieNode(current)
	if leaf.AttrSlot == trieNone {
		return trieNone, false
	}
	return leaf.AttrSlot, true
}

// trieRemove removes a URI from the trie.
// Returns 0 on success, or a negative errno.
func (mgr *KernelAttrManager) trieRemove(uri string) int64 {
	var segments [maxURISegments]string
	nSeg := parseURI(uri, &segments)
	if nSeg < 0 {
		return -22 // EINVAL
	}

	// Walk to find the leaf and track the path for ChildCount updates.
	var path [maxURISegments]uint16
	current := uint16(0)
	for i := 0; i < nSeg; i++ {
		path[i] = current
		child := mgr.findChild(current, segments[i])
		if child == trieNone {
			return -2 // ENOENT
		}
		current = child
	}

	leaf := mgr.trieNode(current)
	if leaf.AttrSlot == trieNone {
		return -2 // ENOENT
	}
	leaf.AttrSlot = trieNone

	// Decrement ChildCount on ancestors.
	for i := 0; i < nSeg; i++ {
		node := mgr.trieNode(path[i])
		if node.ChildCount > 0 {
			node.ChildCount--
		}
	}

	// Prune empty branch nodes (bottom-up): if a non-leaf node has no children
	// and no attribute, remove it from its parent's child list.
	for i := nSeg - 1; i >= 1; i-- {
		nodeIdx := mgr.findChild(path[i], segments[i])
		if nodeIdx == trieNone {
			break
		}
		node := mgr.trieNode(nodeIdx)
		if node.FirstChild != trieNone || node.AttrSlot != trieNone {
			break // still has children or is a leaf
		}
		// Remove from parent's child list.
		mgr.removeChild(path[i], nodeIdx)
		mgr.freeTrieNode(nodeIdx)
	}

	mgr.bumpGeneration()
	return 0
}

// trieMatchPattern finds all attribute slots matching a pattern with '*' wildcards.
// Writes matches into results buffer, returns count.
func (mgr *KernelAttrManager) trieMatchPattern(pattern string, results []uint16) int {
	var segments [maxURISegments]string
	nSeg := parseURI(pattern, &segments)
	if nSeg < 0 {
		return 0
	}

	count := 0
	mgr.trieMatchWalk(0, segments[:nSeg], 0, results, &count)
	return count
}

// trieMatchWalk recursively matches pattern segments against trie nodes.
func (mgr *KernelAttrManager) trieMatchWalk(nodeIdx uint16, segments []string, depth int, results []uint16, count *int) {
	if depth >= len(segments) {
		// All segments consumed — check if this node is a leaf.
		node := mgr.trieNode(nodeIdx)
		if node.AttrSlot != trieNone && *count < len(results) {
			results[*count] = node.AttrSlot
			*count++
		}
		return
	}

	seg := segments[depth]
	node := mgr.trieNode(nodeIdx)

	if seg == "*" {
		// Wildcard: match all children at this level.
		childIdx := node.FirstChild
		for childIdx != trieNone {
			mgr.trieMatchWalk(childIdx, segments, depth+1, results, count)
			child := mgr.trieNode(childIdx)
			childIdx = child.NextSibling
		}
	} else {
		// Exact match.
		child := mgr.findChild(nodeIdx, seg)
		if child != trieNone {
			mgr.trieMatchWalk(child, segments, depth+1, results, count)
		}
	}
}

// trieMatchPatternURIs finds all URIs matching a pattern with '*' wildcards.
// Reconstructs full URIs by walking the trie and building the path.
// Writes matched URIs into uris buffer, returns count.
func (mgr *KernelAttrManager) trieMatchPatternURIs(pattern string, uris []string) int {
	var segments [maxURISegments]string
	nSeg := parseURI(pattern, &segments)
	if nSeg < 0 {
		return 0
	}

	count := 0
	var pathBuf [512]byte
	prefix := constants.URIPrefix // "attr:///"
	copy(pathBuf[:], prefix)
	mgr.trieMatchWalkURI(0, segments[:nSeg], 0, uris, &count, pathBuf[:], len(prefix))
	return count
}

// trieMatchWalkURI recursively matches pattern segments, building full URIs.
// path[:pathLen] contains the URI built so far.
func (mgr *KernelAttrManager) trieMatchWalkURI(nodeIdx uint16, segments []string, depth int, uris []string, count *int, path []byte, pathLen int) {
	if depth >= len(segments) {
		node := mgr.trieNode(nodeIdx)
		if node.AttrSlot != trieNone && *count < len(uris) {
			uris[*count] = string(path[:pathLen])
			*count++
		}
		return
	}

	seg := segments[depth]
	node := mgr.trieNode(nodeIdx)

	if seg == "*" {
		// Wildcard: match all children at this level.
		childIdx := node.FirstChild
		for childIdx != trieNone {
			child := mgr.trieNode(childIdx)
			segStr := trieSegmentString(&child.Segment)
			newLen := pathLen + len(segStr)
			if depth > 0 {
				newLen++ // '/' separator
			}
			if newLen < len(path) {
				if depth > 0 {
					path[pathLen] = '/'
					copy(path[pathLen+1:], segStr)
				} else {
					copy(path[pathLen:], segStr)
				}
				mgr.trieMatchWalkURI(childIdx, segments, depth+1, uris, count, path, newLen)
			}
			childIdx = child.NextSibling
		}
	} else {
		// Exact match.
		child := mgr.findChild(nodeIdx, seg)
		if child != trieNone {
			newLen := pathLen + len(seg)
			if depth > 0 {
				newLen++
			}
			if newLen < len(path) {
				if depth > 0 {
					path[pathLen] = '/'
					copy(path[pathLen+1:], seg)
				} else {
					copy(path[pathLen:], seg)
				}
				mgr.trieMatchWalkURI(child, segments, depth+1, uris, count, path, newLen)
			}
		}
	}
}

// trieSegmentString returns a Go string from a NUL-terminated trie segment.
func trieSegmentString(seg *[trieSegmentLen]byte) string {
	for i := 0; i < trieSegmentLen; i++ {
		if seg[i] == 0 {
			return string(seg[:i])
		}
	}
	return string(seg[:])
}

// findChild searches for a child node with the given segment name.
// Returns child index or trieNone.
//
//go:nosplit
func (mgr *KernelAttrManager) findChild(parentIdx uint16, seg string) uint16 {
	parent := mgr.trieNode(parentIdx)
	childIdx := parent.FirstChild
	for childIdx != trieNone {
		child := mgr.trieNode(childIdx)
		if segmentMatch(&child.Segment, seg) {
			return childIdx
		}
		childIdx = child.NextSibling
	}
	return trieNone
}

// removeChild removes childIdx from parentIdx's child linked list.
//
//go:nosplit
func (mgr *KernelAttrManager) removeChild(parentIdx, childIdx uint16) {
	parent := mgr.trieNode(parentIdx)
	if parent.FirstChild == childIdx {
		child := mgr.trieNode(childIdx)
		parent.FirstChild = child.NextSibling
		return
	}
	prev := parent.FirstChild
	for prev != trieNone {
		prevNode := mgr.trieNode(prev)
		if prevNode.NextSibling == childIdx {
			child := mgr.trieNode(childIdx)
			prevNode.NextSibling = child.NextSibling
			return
		}
		prev = prevNode.NextSibling
	}
}
