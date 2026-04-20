// layout_shared.go — Create a read-only PageRegion from a shared memory mapping.
//
// NewPageRegionFromSharedMapping wraps a mapped constraint page region without
// allocating Go heap memory for the backing data. The resulting PageRegion is
// read-only — write operations (WriteString, WriteEdges, etc.) will panic.

package flat

import (
	"encoding/binary"
	"unsafe"
)

// Shared page magic and version expected by the client.
const (
	SharedPageMagic   = 0x4D415A46 // "MAZF"
	SharedPageVersion = 3
)

// SharedPageHeaderSize is the size of the header at offset 0 of the mapping.
const SharedPageHeaderSize = 256

// NewPageRegionFromSharedMapping creates a read-only PageRegion from a shared
// memory mapping. base is the VA of the first byte of the shared pages.
// Returns nil if the header magic/version is invalid.
func NewPageRegionFromSharedMapping(base uintptr) *PageRegion {
	// Read header fields. The SharedPageHeader struct is in kmem which we
	// can't import from userspace, so we read the well-known offsets directly.
	hdr := (*[SharedPageHeaderSize]byte)(unsafe.Pointer(base))

	magic := binary.LittleEndian.Uint32(hdr[0:4])
	version := binary.LittleEndian.Uint32(hdr[4:8])
	if magic != SharedPageMagic || version != SharedPageVersion {
		return nil
	}

	// Read region offsets and capacities from the header.
	// Layout in header (after magic:4, version:4, generation:8 = offset 16):
	//   nodeRegionOff:4, nodeCapacity:2, pad:2
	//   edgeRegionOff:4, edgeCapacity:2, pad:2
	//   bytecodeRegionOff:4, bytecodeCapacity:2, pad:2
	//   stringRegionOff:4, stringCapacity:2, pad:2
	//   collRegionOff:4, collCapacity:4            (v3: widened from uint16+pad)
	//   trieRegionOff:4, trieCapacity:2, pad:2
	off := 16

	nodeOff := binary.LittleEndian.Uint32(hdr[off : off+4])
	nodeCap := binary.LittleEndian.Uint16(hdr[off+4 : off+6])
	off += 8

	edgeOff := binary.LittleEndian.Uint32(hdr[off : off+4])
	edgeCap := binary.LittleEndian.Uint16(hdr[off+4 : off+6])
	off += 8

	bcOff := binary.LittleEndian.Uint32(hdr[off : off+4])
	bcCap := binary.LittleEndian.Uint16(hdr[off+4 : off+6])
	off += 8

	strOff := binary.LittleEndian.Uint32(hdr[off : off+4])
	strCap := binary.LittleEndian.Uint16(hdr[off+4 : off+6])
	off += 8

	collOff := binary.LittleEndian.Uint32(hdr[off : off+4])
	collCap := binary.LittleEndian.Uint32(hdr[off+4 : off+8])
	off += 8

	_ = off // trieOff/trieCap read separately by trie walker

	// Create sub-slices using unsafe.Slice into the mapped memory.
	nodeBytes := int(nodeCap) * AttrNodeSize
	edgeBytes := int(edgeCap) * 2
	bcBytes := int(bcCap) * 16
	strBytes := int(strCap) * StringSlotSize
	collBytes := int(collCap) * ValueSize

	pr := &PageRegion{
		Nodes:          unsafe.Slice((*byte)(unsafe.Pointer(base+uintptr(nodeOff))), nodeBytes),
		Edges:          unsafe.Slice((*byte)(unsafe.Pointer(base+uintptr(edgeOff))), edgeBytes),
		Bytecode:       unsafe.Slice((*byte)(unsafe.Pointer(base+uintptr(bcOff))), bcBytes),
		Strings:        unsafe.Slice((*byte)(unsafe.Pointer(base+uintptr(strOff))), strBytes),
		Collections:    unsafe.Slice((*byte)(unsafe.Pointer(base+uintptr(collOff))), collBytes),
		NodeCapacity:   int(nodeCap),
		StringCapacity: int(strCap),
	}

	return pr
}

// ReadTrieRegion returns the trie region base pointer and capacity from a
// shared mapping. Used by the resolver to walk the trie directly.
func ReadTrieRegion(base uintptr) (trieBase uintptr, trieCap uint16) {
	hdr := (*[SharedPageHeaderSize]byte)(unsafe.Pointer(base))
	// Trie is the 6th region entry, at offset 16 + 5*8 = 56.
	off := 56
	trieOff := binary.LittleEndian.Uint32(hdr[off : off+4])
	trieCap = binary.LittleEndian.Uint16(hdr[off+4 : off+6])
	return base + uintptr(trieOff), trieCap
}
