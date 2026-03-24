package flat

import "unsafe"

// Attribute kinds.
const (
	AttrKindValue      uint8 = 0
	AttrKindConstraint uint8 = 1
)

// Attribute flags (bits in FlatAttrNode.Flags).
const (
	FlagDirty       uint32 = 1 << 0
	FlagEagerNotify uint32 = 1 << 1
	FlagTombstoned  uint32 = 1 << 2
)

// FlatAttrNode is the per-attribute record in shared pages.
// Fixed size (128 bytes), no Go pointers, cache-line aligned.
type FlatAttrNode struct {
	// Identity and ownership (4 bytes)
	Owner     uint16 // shepherd ID (0 = kernel)
	Kind      uint8  // AttrKindValue or AttrKindConstraint
	ValueType uint8  // TypeI64, TypeF64, TypeBool, etc.

	// State (8 bytes)
	Flags      uint32 // dirty, eager-notify, tombstoned
	SeqCounter uint32 // seqlock: odd = write in progress, even = stable

	// Cached value (40 bytes)
	CachedValue FlatValue

	// Constraint program (6 bytes)
	ProgramOffset uint32 // byte offset into bytecode region (0 = none)
	ProgramLen    uint16 // byte length of serialized program (MZBC blob)

	// Dependencies — who I depend on (6 bytes)
	DepsCount  uint16
	DepsOffset uint32 // byte offset into edge array region

	// Dependents — who depends on me (8 bytes)
	DependentsCount  uint16
	_pad1            uint16
	DependentsOffset uint32 // byte offset into edge array region

	// Dirty walk generation (8 bytes, offset 64) — used by kernel for diamond
	// dedup. Shepherds see this as read-only noise; co-located for cache locality.
	// Placed before NameOffset to keep 8-byte alignment without implicit padding.
	LastWalk uint64

	// Name (4 bytes)
	NameOffset uint32 // byte offset into string data region (0 = unnamed)

	// Padding to 128 bytes
	_pad2 [44]byte
}

const FlatAttrNodeSize = 128

// Compile-time size assertion.
const _flatAttrNodeSize = unsafe.Sizeof(FlatAttrNode{})

var _ [FlatAttrNodeSize - _flatAttrNodeSize]byte
var _ [_flatAttrNodeSize - FlatAttrNodeSize]byte

// IsDirty returns true if the dirty flag is set.
func (n *FlatAttrNode) IsDirty() bool {
	return n.Flags&FlagDirty != 0
}

// SetDirty sets or clears the dirty flag.
func (n *FlatAttrNode) SetDirty(dirty bool) {
	if dirty {
		n.Flags |= FlagDirty
	} else {
		n.Flags &^= FlagDirty
	}
}

// IsTombstoned returns true if the node has been freed.
func (n *FlatAttrNode) IsTombstoned() bool {
	return n.Flags&FlagTombstoned != 0
}
