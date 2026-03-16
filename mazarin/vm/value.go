package vm

import (
	"encoding/binary"
	"fmt"
	"math"
)

// Type tags for VM values. The first 5 match flat.Type* constants.
// Collection types (6-9) are vm-specific (flat uses a different encoding).
// Composite types start at 0x20 to avoid collision with collection types.
const (
	TypeI64      uint8 = 1
	TypeF64      uint8 = 2
	TypeBool     uint8 = 3
	TypeTribool  uint8 = 4
	TypeStr      uint8 = 5
	TypeCollI64  uint8 = 6
	TypeCollF64  uint8 = 7
	TypeCollBool uint8 = 8
	TypeCollStr  uint8 = 9

	// Composite types — use 0x20+ to avoid collision with collection types.
	// The convert.go bridge maps between these and flat.Type* constants.
	TypeTimespec  uint8 = 0x20
	TypeTimezone  uint8 = 0x21
	TypeDuration  uint8 = 0x22
	TypeDate      uint8 = 0x23
	TypePoint2D   uint8 = 0x24
	TypePoint3D   uint8 = 0x25
	TypePointF2D  uint8 = 0x26
	TypePointF3D  uint8 = 0x27
	TypeRadians   uint8 = 0x28
	TypeDegrees   uint8 = 0x29
	TypeIPv4      uint8 = 0x2A
	TypeIPv6      uint8 = 0x2B
	TypePriestId  uint8 = 0x2C
	TypeMazId     uint8 = 0x2D
	TypeRectangle uint8 = 0x2E
)

// Tribool values.
const (
	TriboolFalse   int64 = 0
	TriboolTrue    int64 = 1
	TriboolUnknown int64 = 2
)

// Value is a tagged value on the VM operand stack.
// The active field is determined by typ.
// Composite types (Rectangle, Timespec, etc.) store their data in the
// data field using the same byte layout as FlatValue.Data.
type Value struct {
	typ  uint8
	i64  int64    // I64, Bool (0/1), Tribool (0/1/2)
	f64  float64  // F64
	str  string   // Str
	coll []Value  // CollI64, CollF64, CollBool, CollStr
	data [24]byte // Composite types (Rectangle, Timespec, Point2D, etc.)
}

func I64(v int64) Value       { return Value{typ: TypeI64, i64: v} }
func F64(v float64) Value     { return Value{typ: TypeF64, f64: v} }
func Bool(v bool) Value       { if v { return Value{typ: TypeBool, i64: 1} }; return Value{typ: TypeBool, i64: 0} }
func Str(v string) Value      { return Value{typ: TypeStr, str: v} }

func Tribool(v int64) Value {
	if v < 0 || v > 2 {
		panic("vm: tribool value must be 0, 1, or 2")
	}
	return Value{typ: TypeTribool, i64: v}
}

func CollI64(vs []int64) Value {
	c := make([]Value, len(vs))
	for i, v := range vs {
		c[i] = I64(v)
	}
	return Value{typ: TypeCollI64, coll: c}
}

func CollStr(vs []string) Value {
	c := make([]Value, len(vs))
	for i, v := range vs {
		c[i] = Str(v)
	}
	return Value{typ: TypeCollStr, coll: c}
}

func (v Value) Type() uint8    { return v.typ }
func (v Value) AsI64() int64   { return v.i64 }
func (v Value) AsF64() float64 { return v.f64 }
func (v Value) AsBool() bool   { return v.i64 != 0 }
func (v Value) AsStr() string  { return v.str }
func (v Value) AsColl() []Value { return v.coll }

// --- Composite constructors ---

func RectangleVal(x0, y0, x1, y1 int32) Value {
	var v Value
	v.typ = TypeRectangle
	binary.LittleEndian.PutUint32(v.data[0:4], uint32(x0))
	binary.LittleEndian.PutUint32(v.data[4:8], uint32(y0))
	binary.LittleEndian.PutUint32(v.data[8:12], uint32(x1))
	binary.LittleEndian.PutUint32(v.data[12:16], uint32(y1))
	return v
}

func TimespecVal(seconds int64, nanos int32) Value {
	var v Value
	v.typ = TypeTimespec
	binary.LittleEndian.PutUint64(v.data[0:8], uint64(seconds))
	binary.LittleEndian.PutUint32(v.data[8:12], uint32(nanos))
	return v
}

func TimezoneVal(offsetMinutes int16) Value {
	var v Value
	v.typ = TypeTimezone
	binary.LittleEndian.PutUint16(v.data[0:2], uint16(offsetMinutes))
	return v
}

func DurationVal(nanos int64) Value {
	var v Value
	v.typ = TypeDuration
	binary.LittleEndian.PutUint64(v.data[0:8], uint64(nanos))
	return v
}

func DateVal(year int16, month uint8, day uint8, tzMinutes int16) Value {
	var v Value
	v.typ = TypeDate
	binary.LittleEndian.PutUint16(v.data[0:2], uint16(year))
	v.data[2] = month
	v.data[3] = day
	binary.LittleEndian.PutUint16(v.data[4:6], uint16(tzMinutes))
	return v
}

func Point2DVal(x, y int64) Value {
	var v Value
	v.typ = TypePoint2D
	binary.LittleEndian.PutUint64(v.data[0:8], uint64(x))
	binary.LittleEndian.PutUint64(v.data[8:16], uint64(y))
	return v
}

func Point3DVal(x, y, z int64) Value {
	var v Value
	v.typ = TypePoint3D
	binary.LittleEndian.PutUint64(v.data[0:8], uint64(x))
	binary.LittleEndian.PutUint64(v.data[8:16], uint64(y))
	binary.LittleEndian.PutUint64(v.data[16:24], uint64(z))
	return v
}

func PointF2DVal(x, y float64) Value {
	var v Value
	v.typ = TypePointF2D
	binary.LittleEndian.PutUint64(v.data[0:8], math.Float64bits(x))
	binary.LittleEndian.PutUint64(v.data[8:16], math.Float64bits(y))
	return v
}

func PointF3DVal(x, y, z float64) Value {
	var v Value
	v.typ = TypePointF3D
	binary.LittleEndian.PutUint64(v.data[0:8], math.Float64bits(x))
	binary.LittleEndian.PutUint64(v.data[8:16], math.Float64bits(y))
	binary.LittleEndian.PutUint64(v.data[16:24], math.Float64bits(z))
	return v
}

func RadiansVal(v float64) Value {
	var val Value
	val.typ = TypeRadians
	binary.LittleEndian.PutUint64(val.data[0:8], math.Float64bits(v))
	return val
}

func DegreesVal(v float64) Value {
	var val Value
	val.typ = TypeDegrees
	binary.LittleEndian.PutUint64(val.data[0:8], math.Float64bits(v))
	return val
}

func IPv4Val(a, b, c, d byte) Value {
	var v Value
	v.typ = TypeIPv4
	v.data[0] = a
	v.data[1] = b
	v.data[2] = c
	v.data[3] = d
	return v
}

func IPv6Val(addr [16]byte) Value {
	var v Value
	v.typ = TypeIPv6
	copy(v.data[0:16], addr[:])
	return v
}

func PriestIdVal(id uint16) Value {
	var v Value
	v.typ = TypePriestId
	binary.LittleEndian.PutUint16(v.data[0:2], id)
	return v
}

func MazIdVal(id uint16) Value {
	var v Value
	v.typ = TypeMazId
	binary.LittleEndian.PutUint16(v.data[0:2], id)
	return v
}

// --- Composite accessors ---

func (v Value) AsRectangle() (x0, y0, x1, y1 int32) {
	return int32(binary.LittleEndian.Uint32(v.data[0:4])),
		int32(binary.LittleEndian.Uint32(v.data[4:8])),
		int32(binary.LittleEndian.Uint32(v.data[8:12])),
		int32(binary.LittleEndian.Uint32(v.data[12:16]))
}

func (v Value) AsTimespec() (seconds int64, nanos int32) {
	return int64(binary.LittleEndian.Uint64(v.data[0:8])),
		int32(binary.LittleEndian.Uint32(v.data[8:12]))
}

func (v Value) AsTimezone() int16 {
	return int16(binary.LittleEndian.Uint16(v.data[0:2]))
}

func (v Value) AsDuration() int64 {
	return int64(binary.LittleEndian.Uint64(v.data[0:8]))
}

func (v Value) AsDate() (year int16, month uint8, day uint8, tzMinutes int16) {
	return int16(binary.LittleEndian.Uint16(v.data[0:2])),
		v.data[2],
		v.data[3],
		int16(binary.LittleEndian.Uint16(v.data[4:6]))
}

func (v Value) AsPoint2D() (x, y int64) {
	return int64(binary.LittleEndian.Uint64(v.data[0:8])),
		int64(binary.LittleEndian.Uint64(v.data[8:16]))
}

func (v Value) AsPoint3D() (x, y, z int64) {
	return int64(binary.LittleEndian.Uint64(v.data[0:8])),
		int64(binary.LittleEndian.Uint64(v.data[8:16])),
		int64(binary.LittleEndian.Uint64(v.data[16:24]))
}

func (v Value) AsPointF2D() (x, y float64) {
	return math.Float64frombits(binary.LittleEndian.Uint64(v.data[0:8])),
		math.Float64frombits(binary.LittleEndian.Uint64(v.data[8:16]))
}

func (v Value) AsPointF3D() (x, y, z float64) {
	return math.Float64frombits(binary.LittleEndian.Uint64(v.data[0:8])),
		math.Float64frombits(binary.LittleEndian.Uint64(v.data[8:16])),
		math.Float64frombits(binary.LittleEndian.Uint64(v.data[16:24]))
}

func (v Value) AsRadians() float64 {
	return math.Float64frombits(binary.LittleEndian.Uint64(v.data[0:8]))
}

func (v Value) AsDegrees() float64 {
	return math.Float64frombits(binary.LittleEndian.Uint64(v.data[0:8]))
}

func (v Value) AsIPv4() [4]byte {
	return [4]byte{v.data[0], v.data[1], v.data[2], v.data[3]}
}

func (v Value) AsIPv6() [16]byte {
	var addr [16]byte
	copy(addr[:], v.data[0:16])
	return addr
}

func (v Value) AsPriestIdNum() uint16 {
	return binary.LittleEndian.Uint16(v.data[0:2])
}

func (v Value) AsMazIdNum() uint16 {
	return binary.LittleEndian.Uint16(v.data[0:2])
}

// IsCompositeType returns true for types that use the data field.
func IsCompositeType(t uint8) bool {
	return t >= TypeTimespec && t <= TypeRectangle
}

// CompositeData returns the raw data bytes for composite types.
// Used by the flat conversion layer.
func (v Value) CompositeData() []byte {
	return v.data[:]
}

// CompositeFromData constructs a Value of a composite type from raw data bytes.
// The data layout must match the FlatValue.Data layout for the given type.
func CompositeFromData(typ uint8, data [24]byte) Value {
	var v Value
	v.typ = typ
	v.data = data
	return v
}

func (v Value) String() string {
	switch v.typ {
	case TypeI64:
		return fmt.Sprintf("i64(%d)", v.i64)
	case TypeF64:
		return fmt.Sprintf("f64(%g)", v.f64)
	case TypeBool:
		if v.i64 != 0 {
			return "bool(true)"
		}
		return "bool(false)"
	case TypeTribool:
		switch v.i64 {
		case 0:
			return "tribool(false)"
		case 1:
			return "tribool(true)"
		default:
			return "tribool(unknown)"
		}
	case TypeStr:
		return fmt.Sprintf("str(%q)", v.str)
	case TypeCollI64, TypeCollF64, TypeCollBool, TypeCollStr:
		return fmt.Sprintf("coll(len=%d)", len(v.coll))
	case TypeRectangle:
		x0, y0, x1, y1 := v.AsRectangle()
		return fmt.Sprintf("rect(%d,%d,%d,%d)", x0, y0, x1, y1)
	case TypeTimespec:
		sec, ns := v.AsTimespec()
		return fmt.Sprintf("timespec(%d,%d)", sec, ns)
	case TypeTimezone:
		return fmt.Sprintf("timezone(%d)", v.AsTimezone())
	case TypeDuration:
		return fmt.Sprintf("duration(%d)", v.AsDuration())
	case TypeDate:
		y, m, d, tz := v.AsDate()
		return fmt.Sprintf("date(%d-%02d-%02d,tz=%d)", y, m, d, tz)
	case TypePoint2D:
		x, y := v.AsPoint2D()
		return fmt.Sprintf("point2d(%d,%d)", x, y)
	case TypePoint3D:
		x, y, z := v.AsPoint3D()
		return fmt.Sprintf("point3d(%d,%d,%d)", x, y, z)
	case TypePointF2D:
		x, y := v.AsPointF2D()
		return fmt.Sprintf("pointf2d(%g,%g)", x, y)
	case TypePointF3D:
		x, y, z := v.AsPointF3D()
		return fmt.Sprintf("pointf3d(%g,%g,%g)", x, y, z)
	case TypeIPv4:
		a := v.AsIPv4()
		return fmt.Sprintf("ipv4(%d.%d.%d.%d)", a[0], a[1], a[2], a[3])
	case TypePriestId:
		return fmt.Sprintf("priest_id(%d)", v.AsPriestIdNum())
	case TypeMazId:
		return fmt.Sprintf("maz_id(%d)", v.AsMazIdNum())
	default:
		return "value(?)"
	}
}

// TypeName returns a human-readable name for a type tag.
func TypeName(t uint8) string {
	switch t {
	case TypeI64:
		return "int64"
	case TypeF64:
		return "float64"
	case TypeBool:
		return "bool"
	case TypeTribool:
		return "tribool"
	case TypeStr:
		return "string"
	case TypeCollI64:
		return "[]int64"
	case TypeCollF64:
		return "[]float64"
	case TypeCollBool:
		return "[]bool"
	case TypeCollStr:
		return "[]string"
	case TypeTimespec:
		return "timespec"
	case TypeTimezone:
		return "timezone"
	case TypeDuration:
		return "duration"
	case TypeDate:
		return "date"
	case TypePoint2D:
		return "point2d"
	case TypePoint3D:
		return "point3d"
	case TypePointF2D:
		return "pointf2d"
	case TypePointF3D:
		return "pointf3d"
	case TypeRadians:
		return "radians"
	case TypeDegrees:
		return "degrees"
	case TypeIPv4:
		return "ipv4"
	case TypeIPv6:
		return "ipv6"
	case TypePriestId:
		return "priest_id"
	case TypeMazId:
		return "maz_id"
	case TypeRectangle:
		return "rectangle"
	default:
		return "unknown"
	}
}
