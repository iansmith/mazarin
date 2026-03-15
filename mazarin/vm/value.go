package vm

import "fmt"

// Type tags for VM values.
const (
	TypeI64     uint8 = 1
	TypeF64     uint8 = 2
	TypeBool    uint8 = 3
	TypeTribool uint8 = 4
	TypeStr     uint8 = 5
	TypeCollI64 uint8 = 6
	TypeCollF64 uint8 = 7
	TypeCollBool uint8 = 8
	TypeCollStr uint8 = 9
)

// Tribool values.
const (
	TriboolFalse   int64 = 0
	TriboolTrue    int64 = 1
	TriboolUnknown int64 = 2
)

// Value is a tagged value on the VM operand stack.
// The active field is determined by typ.
type Value struct {
	typ  uint8
	i64  int64   // I64, Bool (0/1), Tribool (0/1/2)
	f64  float64 // F64
	str  string  // Str
	coll []Value // CollI64, CollF64, CollBool, CollStr
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
	default:
		return "unknown"
	}
}
