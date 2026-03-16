// trie_node.go — Shared trie node struct used by both kernel and userspace.
//
// TrieNode is a flat 128-byte structure stored in the trie region of the
// constraint shared pages. Both kernel (writable) and priests (read-only)
// use this same layout.

package constants

import "unsafe"

// Trie layout constants.
const (
	TrieNodeSize   = 128
	TrieSegmentLen = 64 // including NUL terminator
	TrieNone       uint16 = 0xFFFF
)

// TrieNode is a flat 128-byte trie node in the shared pages.
type TrieNode struct {
	Segment     [TrieSegmentLen]byte // URI segment, NUL-terminated
	FirstChild  uint16               // index of first child (0xFFFF = none)
	NextSibling uint16               // index of next sibling (0xFFFF = none)
	AttrSlot    uint16               // attribute slot (0xFFFF = not a leaf)
	ChildCount  uint16               // total descendants for exists() queries
	SeqCounter  uint32               // seqlock: odd = mutation in progress
	_pad        [52]byte
}

// Compile-time size assertion.
const _trieNodeActualSize = unsafe.Sizeof(TrieNode{})

var _ [TrieNodeSize - _trieNodeActualSize]byte
var _ [_trieNodeActualSize - TrieNodeSize]byte

// URI prefix constant.
const URIPrefix = "attr:///"

// MaxURISegments is the maximum number of segments in a URI.
const MaxURISegments = 16
