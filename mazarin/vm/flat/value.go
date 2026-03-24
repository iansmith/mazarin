package flat

import (
	"encoding/binary"
	"fmt"
	"math"
	"unsafe"
)

// FlatValue is a 32-byte, pointer-free tagged value.
// Strings and collections reference data in separate regions.
type FlatValue struct {
	Typ  uint8
	_pad [7]byte
	Data [32]byte
}

// Compile-time size assertion.
const _flatValueSize = unsafe.Sizeof(FlatValue{})

var _ [40 - _flatValueSize]byte
var _ [_flatValueSize - 40]byte

const FlatValueSize = 40

// FlatStrRef describes a string reference in FlatValue.Data.
type FlatStrRef struct {
	RegionOffset uint32 // byte offset into string data region
	Len          uint16 // string length excluding NUL
}

// FlatCollRef describes a collection reference in FlatValue.Data.
type FlatCollRef struct {
	ElemType     uint8  // scalar type tag of each element
	RegionOffset uint32 // byte offset into collection data region
	Count        uint16 // number of elements
}

const (
	FlatStringMaxLen   = 255
	FlatStringSlotSize = 256
)

// --- Scalar constructors ---

func NewI64(v int64) FlatValue {
	var fv FlatValue
	fv.Typ = TypeI64
	binary.LittleEndian.PutUint64(fv.Data[:8], uint64(v))
	return fv
}

func NewF64(v float64) FlatValue {
	var fv FlatValue
	fv.Typ = TypeF64
	binary.LittleEndian.PutUint64(fv.Data[:8], math.Float64bits(v))
	return fv
}

func NewBool(v bool) FlatValue {
	var fv FlatValue
	fv.Typ = TypeBool
	if v {
		fv.Data[0] = 1
	}
	return fv
}

func NewTribool(v int64) FlatValue {
	if v < 0 || v > 2 {
		panic("flat: tribool value must be 0, 1, or 2")
	}
	var fv FlatValue
	fv.Typ = TypeTribool
	fv.Data[0] = uint8(v)
	return fv
}

func NewStr(ref FlatStrRef) FlatValue {
	var fv FlatValue
	fv.Typ = TypeStr
	binary.LittleEndian.PutUint32(fv.Data[0:4], ref.RegionOffset)
	binary.LittleEndian.PutUint16(fv.Data[4:6], ref.Len)
	return fv
}

func NewCollection(ref FlatCollRef) FlatValue {
	var fv FlatValue
	fv.Typ = TypeCollection
	fv.Data[0] = ref.ElemType
	binary.LittleEndian.PutUint32(fv.Data[4:8], ref.RegionOffset)
	binary.LittleEndian.PutUint16(fv.Data[8:10], ref.Count)
	return fv
}

// --- Scalar accessors ---

func (fv FlatValue) AsI64() int64 {
	return int64(binary.LittleEndian.Uint64(fv.Data[:8]))
}

func (fv FlatValue) AsF64() float64 {
	return math.Float64frombits(binary.LittleEndian.Uint64(fv.Data[:8]))
}

func (fv FlatValue) AsBool() bool {
	return fv.Data[0] != 0
}

func (fv FlatValue) AsTribool() int64 {
	return int64(fv.Data[0])
}

func (fv FlatValue) AsStrRef() FlatStrRef {
	return FlatStrRef{
		RegionOffset: binary.LittleEndian.Uint32(fv.Data[0:4]),
		Len:          binary.LittleEndian.Uint16(fv.Data[4:6]),
	}
}

func (fv FlatValue) AsCollRef() FlatCollRef {
	return FlatCollRef{
		ElemType:     fv.Data[0],
		RegionOffset: binary.LittleEndian.Uint32(fv.Data[4:8]),
		Count:        binary.LittleEndian.Uint16(fv.Data[8:10]),
	}
}

// String returns a human-readable representation.
func (fv FlatValue) String() string {
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
