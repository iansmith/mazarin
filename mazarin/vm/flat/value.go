package flat

import (
	"encoding/binary"
	"fmt"
	"math"
	"unsafe"
)

// Value is a 32-byte, pointer-free tagged value.
// Strings and collections reference data in separate regions.
type Value struct {
	Typ  uint8
	_pad [7]byte
	Data [32]byte
}

// Compile-time size assertion.
const _valueSize = unsafe.Sizeof(Value{})

var _ [40 - _valueSize]byte
var _ [_valueSize - 40]byte

const ValueSize = 40

// StrRef describes a string reference in Value.Data.
type StrRef struct {
	RegionOffset uint32 // byte offset into string data region
	Len          uint16 // string length excluding NUL
}

// CollRef describes a collection reference in Value.Data.
type CollRef struct {
	ElemType     uint8  // scalar type tag of each element
	RegionOffset uint32 // byte offset into collection data region
	Count        uint16 // number of elements
}

const (
	StringMaxLen   = 255
	StringSlotSize = 256
)

// --- Scalar constructors ---

func NewI64(v int64) Value {
	var fv Value
	fv.Typ = TypeI64
	binary.LittleEndian.PutUint64(fv.Data[:8], uint64(v))
	return fv
}

func NewF64(v float64) Value {
	var fv Value
	fv.Typ = TypeF64
	binary.LittleEndian.PutUint64(fv.Data[:8], math.Float64bits(v))
	return fv
}

func NewBool(v bool) Value {
	var fv Value
	fv.Typ = TypeBool
	if v {
		fv.Data[0] = 1
	}
	return fv
}

func NewTribool(v int64) Value {
	if v < 0 || v > 2 {
		panic("flat: tribool value must be 0, 1, or 2")
	}
	var fv Value
	fv.Typ = TypeTribool
	fv.Data[0] = uint8(v)
	return fv
}

func NewStr(ref StrRef) Value {
	var fv Value
	fv.Typ = TypeStr
	binary.LittleEndian.PutUint32(fv.Data[0:4], ref.RegionOffset)
	binary.LittleEndian.PutUint16(fv.Data[4:6], ref.Len)
	return fv
}

func NewCollection(ref CollRef) Value {
	var fv Value
	fv.Typ = TypeCollection
	fv.Data[0] = ref.ElemType
	binary.LittleEndian.PutUint32(fv.Data[4:8], ref.RegionOffset)
	binary.LittleEndian.PutUint16(fv.Data[8:10], ref.Count)
	return fv
}

// --- Scalar accessors ---

func (fv Value) AsI64() int64 {
	return int64(binary.LittleEndian.Uint64(fv.Data[:8]))
}

func (fv Value) AsF64() float64 {
	return math.Float64frombits(binary.LittleEndian.Uint64(fv.Data[:8]))
}

func (fv Value) AsBool() bool {
	return fv.Data[0] != 0
}

func (fv Value) AsTribool() int64 {
	return int64(fv.Data[0])
}

func (fv Value) AsStrRef() StrRef {
	return StrRef{
		RegionOffset: binary.LittleEndian.Uint32(fv.Data[0:4]),
		Len:          binary.LittleEndian.Uint16(fv.Data[4:6]),
	}
}

func (fv Value) AsCollRef() CollRef {
	return CollRef{
		ElemType:     fv.Data[0],
		RegionOffset: binary.LittleEndian.Uint32(fv.Data[4:8]),
		Count:        binary.LittleEndian.Uint16(fv.Data[8:10]),
	}
}

// String returns a human-readable representation.
func (fv Value) String() string {
	switch fv.Typ {
	case TypeI64:
		return fmt.Sprintf("i64(%d)", fv.AsI64())
	case TypeF64:
		return fmt.Sprintf("f64(%g)", fv.AsF64())
	case TypeBool:
		return fmt.Sprintf("bool(%v)", fv.AsBool())
	case TypeTribool:
		switch fv.AsTribool() {
		case 0:
			return "tribool(false)"
		case 1:
			return "tribool(true)"
		default:
			return "tribool(unknown)"
		}
	case TypeStr:
		ref := fv.AsStrRef()
		return fmt.Sprintf("str(offset=%d,len=%d)", ref.RegionOffset, ref.Len)
	case TypeCollection:
		ref := fv.AsCollRef()
		return fmt.Sprintf("coll(%s,offset=%d,count=%d)", TypeName(ref.ElemType), ref.RegionOffset, ref.Count)
	default:
		info := LookupType(fv.Typ)
		if info.Name != "" {
			return info.Name + "(...)"
		}
		return "flat(?)"
	}
}
