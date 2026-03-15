package flat

import (
	"encoding/binary"
	"fmt"
	"unsafe"
)

// Default region capacities.
const (
	DefaultNodeCapacity       = 512
	DefaultEdgeCapacity       = 4096  // number of uint16 edges
	DefaultBytecodeCapacity   = 2048  // number of 16-byte instructions
	DefaultStringCapacity     = 256   // number of 256-byte string slots
	DefaultCollectionCapacity = 1024  // number of FlatValue elements
)

// PageRegion wraps a contiguous byte buffer organized into typed regions.
// For Phase 1 this is a Go-allocated []byte. In later phases it will
// wrap shared memory pages.
type PageRegion struct {
	// Region data (sub-slices of a contiguous backing buffer).
	Nodes       []byte
	Edges       []byte
	Bytecode    []byte
	Strings     []byte
	Collections []byte

	// Capacities in items.
	NodeCapacity   int
	StringCapacity int

	// Bitmap allocators (1 bit per slot).
	nodeBitmap   []byte
	stringBitmap []byte

	// Bump allocator state (bytes used within respective regions).
	edgeUsed       int
	bytecodeUsed   int
	collectionUsed int
}

// NewPageRegion creates a PageRegion backed by a single contiguous buffer.
func NewPageRegion(nodeCap, edgeCap, bytecodeCap, stringCap, collectionCap int) *PageRegion {
	nodeBytes := nodeCap * FlatAttrNodeSize
	edgeBytes := edgeCap * 2 // uint16 per edge
	bytecodeBytes := bytecodeCap * 16
	stringBytes := stringCap * FlatStringSlotSize
	collectionBytes := collectionCap * FlatValueSize

	total := nodeBytes + edgeBytes + bytecodeBytes + stringBytes + collectionBytes
	buf := make([]byte, total)

	off := 0
	pr := &PageRegion{
		NodeCapacity:   nodeCap,
		StringCapacity: stringCap,
	}
	pr.Nodes = buf[off : off+nodeBytes]
	off += nodeBytes
	pr.Edges = buf[off : off+edgeBytes]
	off += edgeBytes
	pr.Bytecode = buf[off : off+bytecodeBytes]
	off += bytecodeBytes
	pr.Strings = buf[off : off+stringBytes]
	off += stringBytes
	pr.Collections = buf[off : off+collectionBytes]

	pr.nodeBitmap = make([]byte, (nodeCap+7)/8)
	pr.stringBitmap = make([]byte, (stringCap+7)/8)

	return pr
}

// NewDefaultPageRegion creates a PageRegion with default capacities.
func NewDefaultPageRegion() *PageRegion {
	return NewPageRegion(
		DefaultNodeCapacity,
		DefaultEdgeCapacity,
		DefaultBytecodeCapacity,
		DefaultStringCapacity,
		DefaultCollectionCapacity,
	)
}

// Node returns a pointer to the FlatAttrNode at the given slot index.
// The returned pointer is directly into the backing buffer.
func (pr *PageRegion) Node(idx int16) *FlatAttrNode {
	off := int(idx) * FlatAttrNodeSize
	return (*FlatAttrNode)(unsafe.Pointer(&pr.Nodes[off]))
}

// WriteString allocates a string slot and writes the string content.
func (pr *PageRegion) WriteString(s string) (FlatStrRef, error) {
	if len(s) > FlatStringMaxLen {
		return FlatStrRef{}, fmt.Errorf("flat: string too long (%d bytes, max %d)", len(s), FlatStringMaxLen)
	}
	idx, err := bitmapAlloc(pr.stringBitmap, pr.StringCapacity)
	if err != nil {
		return FlatStrRef{}, fmt.Errorf("flat: string region full")
	}
	off := idx * FlatStringSlotSize
	copy(pr.Strings[off:off+len(s)], s)
	pr.Strings[off+len(s)] = 0 // NUL terminator
	return FlatStrRef{
		RegionOffset: uint32(off),
		Len:          uint16(len(s)),
	}, nil
}

// ReadString reads a string from the string data region.
func (pr *PageRegion) ReadString(ref FlatStrRef) string {
	off := int(ref.RegionOffset)
	return string(pr.Strings[off : off+int(ref.Len)])
}

// WriteCollection allocates space for a collection and writes the elements.
func (pr *PageRegion) WriteCollection(elemType uint8, elems []FlatValue) (FlatCollRef, error) {
	needed := len(elems) * FlatValueSize
	if pr.collectionUsed+needed > len(pr.Collections) {
		return FlatCollRef{}, fmt.Errorf("flat: collection region full")
	}
	off := pr.collectionUsed
	for i, elem := range elems {
		dst := off + i*FlatValueSize
		*(*FlatValue)(unsafe.Pointer(&pr.Collections[dst])) = elem
	}
	pr.collectionUsed += needed
	return FlatCollRef{
		ElemType:     elemType,
		RegionOffset: uint32(off),
		Count:        uint16(len(elems)),
	}, nil
}

// ReadCollectionElement reads one element from a collection.
func (pr *PageRegion) ReadCollectionElement(ref FlatCollRef, idx int) FlatValue {
	off := int(ref.RegionOffset) + idx*FlatValueSize
	return *(*FlatValue)(unsafe.Pointer(&pr.Collections[off]))
}

// WriteEdges allocates space in the edge array and writes slot indices.
func (pr *PageRegion) WriteEdges(slots []uint16) (uint32, error) {
	needed := len(slots) * 2
	if pr.edgeUsed+needed > len(pr.Edges) {
		return 0, fmt.Errorf("flat: edge region full")
	}
	off := pr.edgeUsed
	for i, s := range slots {
		binary.LittleEndian.PutUint16(pr.Edges[off+i*2:off+i*2+2], s)
	}
	pr.edgeUsed += needed
	return uint32(off), nil
}

// ReadEdge reads a single edge (slot index) from the edge array.
func (pr *PageRegion) ReadEdge(offset uint32, idx int) uint16 {
	off := int(offset) + idx*2
	return binary.LittleEndian.Uint16(pr.Edges[off : off+2])
}
