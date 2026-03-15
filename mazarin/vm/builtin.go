package vm

import (
	"math"
	"sort"
	"strings"
)

// Wrappers to avoid stuttering math.Math everywhere.
var (
	sqrt  = math.Sqrt
	floor = math.Floor
	ceil  = math.Ceil
	round = math.Round
)

// Builtin function IDs.
const (
	// Math — int64.
	BuiltinMin   uint16 = 0 // (I64, I64) → I64
	BuiltinMax   uint16 = 1 // (I64, I64) → I64
	BuiltinClamp uint16 = 2 // (I64, I64, I64) → I64  clamp(val, lo, hi)

	// Math — float64.
	BuiltinMinF   uint16 = 3 // (F64, F64) → F64
	BuiltinMaxF   uint16 = 4 // (F64, F64) → F64
	BuiltinClampF uint16 = 5 // (F64, F64, F64) → F64
	BuiltinSqrt   uint16 = 6 // (F64) → F64
	BuiltinFloor  uint16 = 7 // (F64) → F64
	BuiltinCeil   uint16 = 8 // (F64) → F64
	BuiltinRound  uint16 = 9 // (F64) → F64

	// String.
	BuiltinStrLen      uint16 = 20 // (Str) → I64
	BuiltinStrConcat   uint16 = 21 // (Str, Str) → Str
	BuiltinStrContains uint16 = 22 // (Str, Str) → Bool
	BuiltinStrSubstr   uint16 = 23 // (Str, I64, I64) → Str  substr(s, start, len)
	BuiltinStrPrefix   uint16 = 24 // (Str, Str) → Bool
	BuiltinStrSuffix   uint16 = 25 // (Str, Str) → Bool
	BuiltinStrUpper    uint16 = 26 // (Str) → Str
	BuiltinStrLower    uint16 = 27 // (Str) → Str

	// Collection — generic over element type.
	BuiltinCollLen    uint16 = 40 // (Coll) → I64
	BuiltinCollGet    uint16 = 41 // (Coll, I64) → element
	BuiltinCollTake   uint16 = 42 // (Coll, I64) → Coll
	BuiltinCollDrop   uint16 = 43 // (Coll, I64) → Coll
	BuiltinCollSort   uint16 = 44 // (Coll) → Coll  (string/int64 only)
	BuiltinCollConcat uint16 = 45 // (Coll, Coll) → Coll
	BuiltinCollPage   uint16 = 46 // (Coll, I64, I64) → Coll  page(coll, pageNum, pageSize)
	BuiltinCollEmpty  uint16 = 47 // () → Coll  (typ from instruction)
)

// callBuiltin dispatches a builtin function call.
func (m *machine) callBuiltin(id, argc uint16, instTyp uint8) error {
	switch id {

	// --- Math: int64 ---

	case BuiltinMin:
		return m.builtinBinI64(func(a, b int64) int64 {
			if a < b {
				return a
			}
			return b
		})

	case BuiltinMax:
		return m.builtinBinI64(func(a, b int64) int64 {
			if a > b {
				return a
			}
			return b
		})

	case BuiltinClamp:
		hi, err := m.pop()
		if err != nil {
			return err
		}
		lo, err := m.pop()
		if err != nil {
			return err
		}
		val, err := m.pop()
		if err != nil {
			return err
		}
		v := val.i64
		if v < lo.i64 {
			v = lo.i64
		}
		if v > hi.i64 {
			v = hi.i64
		}
		return m.push(I64(v))

	// --- Math: float64 ---

	case BuiltinMinF:
		return m.builtinBinF64(func(a, b float64) float64 {
			if a < b {
				return a
			}
			return b
		})

	case BuiltinMaxF:
		return m.builtinBinF64(func(a, b float64) float64 {
			if a > b {
				return a
			}
			return b
		})

	case BuiltinClampF:
		hi, err := m.pop()
		if err != nil {
			return err
		}
		lo, err := m.pop()
		if err != nil {
			return err
		}
		val, err := m.pop()
		if err != nil {
			return err
		}
		v := val.f64
		if v < lo.f64 {
			v = lo.f64
		}
		if v > hi.f64 {
			v = hi.f64
		}
		return m.push(F64(v))

	case BuiltinSqrt:
		a, err := m.pop()
		if err != nil {
			return err
		}
		return m.push(F64(sqrt(a.f64)))

	case BuiltinFloor:
		a, err := m.pop()
		if err != nil {
			return err
		}
		return m.push(F64(floor(a.f64)))

	case BuiltinCeil:
		a, err := m.pop()
		if err != nil {
			return err
		}
		return m.push(F64(ceil(a.f64)))

	case BuiltinRound:
		a, err := m.pop()
		if err != nil {
			return err
		}
		return m.push(F64(round(a.f64)))

	// --- String ---

	case BuiltinStrLen:
		a, err := m.pop()
		if err != nil {
			return err
		}
		if a.typ != TypeStr {
			return m.haltf("str_len requires string, got %s", TypeName(a.typ))
		}
		return m.push(I64(int64(len(a.str))))

	case BuiltinStrConcat:
		b, err := m.pop()
		if err != nil {
			return err
		}
		a, err := m.pop()
		if err != nil {
			return err
		}
		if a.typ != TypeStr || b.typ != TypeStr {
			return m.haltf("str_concat requires strings, got %s and %s", TypeName(a.typ), TypeName(b.typ))
		}
		return m.push(Str(a.str + b.str))

	case BuiltinStrContains:
		b, err := m.pop()
		if err != nil {
			return err
		}
		a, err := m.pop()
		if err != nil {
			return err
		}
		if a.typ != TypeStr || b.typ != TypeStr {
			return m.haltf("str_contains requires strings, got %s and %s", TypeName(a.typ), TypeName(b.typ))
		}
		return m.push(Bool(strings.Contains(a.str, b.str)))

	case BuiltinStrSubstr:
		length, err := m.pop()
		if err != nil {
			return err
		}
		start, err := m.pop()
		if err != nil {
			return err
		}
		s, err := m.pop()
		if err != nil {
			return err
		}
		if s.typ != TypeStr {
			return m.haltf("str_substr requires string, got %s", TypeName(s.typ))
		}
		st := int(start.i64)
		ln := int(length.i64)
		// Bounds-safe: clamp to valid range.
		if st < 0 {
			st = 0
		}
		if st > len(s.str) {
			st = len(s.str)
		}
		end := st + ln
		if end > len(s.str) {
			end = len(s.str)
		}
		return m.push(Str(s.str[st:end]))

	case BuiltinStrPrefix:
		b, err := m.pop()
		if err != nil {
			return err
		}
		a, err := m.pop()
		if err != nil {
			return err
		}
		return m.push(Bool(strings.HasPrefix(a.str, b.str)))

	case BuiltinStrSuffix:
		b, err := m.pop()
		if err != nil {
			return err
		}
		a, err := m.pop()
		if err != nil {
			return err
		}
		return m.push(Bool(strings.HasSuffix(a.str, b.str)))

	case BuiltinStrUpper:
		a, err := m.pop()
		if err != nil {
			return err
		}
		return m.push(Str(strings.ToUpper(a.str)))

	case BuiltinStrLower:
		a, err := m.pop()
		if err != nil {
			return err
		}
		return m.push(Str(strings.ToLower(a.str)))

	// --- Collection ---

	case BuiltinCollLen:
		a, err := m.pop()
		if err != nil {
			return err
		}
		if !isCollType(a.typ) {
			return m.haltf("coll_len requires collection, got %s", TypeName(a.typ))
		}
		return m.push(I64(int64(len(a.coll))))

	case BuiltinCollGet:
		idx, err := m.pop()
		if err != nil {
			return err
		}
		coll, err := m.pop()
		if err != nil {
			return err
		}
		if !isCollType(coll.typ) {
			return m.haltf("coll_get requires collection, got %s", TypeName(coll.typ))
		}
		i := int(idx.i64)
		if i < 0 || i >= len(coll.coll) {
			return m.haltf("coll_get index %d out of range (len %d)", i, len(coll.coll))
		}
		return m.push(coll.coll[i])

	case BuiltinCollTake:
		n, err := m.pop()
		if err != nil {
			return err
		}
		coll, err := m.pop()
		if err != nil {
			return err
		}
		if !isCollType(coll.typ) {
			return m.haltf("coll_take requires collection, got %s", TypeName(coll.typ))
		}
		count := int(n.i64)
		if count < 0 {
			count = 0
		}
		if count > len(coll.coll) {
			count = len(coll.coll)
		}
		result := make([]Value, count)
		copy(result, coll.coll[:count])
		return m.push(Value{typ: coll.typ, coll: result})

	case BuiltinCollDrop:
		n, err := m.pop()
		if err != nil {
			return err
		}
		coll, err := m.pop()
		if err != nil {
			return err
		}
		if !isCollType(coll.typ) {
			return m.haltf("coll_drop requires collection, got %s", TypeName(coll.typ))
		}
		count := int(n.i64)
		if count < 0 {
			count = 0
		}
		if count > len(coll.coll) {
			count = len(coll.coll)
		}
		result := make([]Value, len(coll.coll)-count)
		copy(result, coll.coll[count:])
		return m.push(Value{typ: coll.typ, coll: result})

	case BuiltinCollSort:
		coll, err := m.pop()
		if err != nil {
			return err
		}
		if !isCollType(coll.typ) {
			return m.haltf("coll_sort requires collection, got %s", TypeName(coll.typ))
		}
		sorted := make([]Value, len(coll.coll))
		copy(sorted, coll.coll)
		switch coll.typ {
		case TypeCollI64:
			sort.Slice(sorted, func(i, j int) bool { return sorted[i].i64 < sorted[j].i64 })
		case TypeCollStr:
			sort.Slice(sorted, func(i, j int) bool { return sorted[i].str < sorted[j].str })
		case TypeCollF64:
			sort.Slice(sorted, func(i, j int) bool { return sorted[i].f64 < sorted[j].f64 })
		default:
			return m.haltf("coll_sort not supported for %s", TypeName(coll.typ))
		}
		return m.push(Value{typ: coll.typ, coll: sorted})

	case BuiltinCollConcat:
		b, err := m.pop()
		if err != nil {
			return err
		}
		a, err := m.pop()
		if err != nil {
			return err
		}
		if a.typ != b.typ {
			return m.haltf("coll_concat type mismatch: %s and %s", TypeName(a.typ), TypeName(b.typ))
		}
		result := make([]Value, 0, len(a.coll)+len(b.coll))
		result = append(result, a.coll...)
		result = append(result, b.coll...)
		return m.push(Value{typ: a.typ, coll: result})

	case BuiltinCollPage:
		pageSize, err := m.pop()
		if err != nil {
			return err
		}
		pageNum, err := m.pop()
		if err != nil {
			return err
		}
		coll, err := m.pop()
		if err != nil {
			return err
		}
		if !isCollType(coll.typ) {
			return m.haltf("coll_page requires collection, got %s", TypeName(coll.typ))
		}
		ps := int(pageSize.i64)
		start := int(pageNum.i64) * ps
		if start < 0 {
			start = 0
		}
		if start > len(coll.coll) {
			start = len(coll.coll)
		}
		end := start + ps
		if end > len(coll.coll) {
			end = len(coll.coll)
		}
		result := make([]Value, end-start)
		copy(result, coll.coll[start:end])
		return m.push(Value{typ: coll.typ, coll: result})

	case BuiltinCollEmpty:
		return m.push(Value{typ: instTyp, coll: nil})

	default:
		return m.haltf("unknown builtin %d", id)
	}
}

func (m *machine) builtinBinI64(op func(int64, int64) int64) error {
	b, err := m.pop()
	if err != nil {
		return err
	}
	a, err := m.pop()
	if err != nil {
		return err
	}
	return m.push(I64(op(a.i64, b.i64)))
}

func (m *machine) builtinBinF64(op func(float64, float64) float64) error {
	b, err := m.pop()
	if err != nil {
		return err
	}
	a, err := m.pop()
	if err != nil {
		return err
	}
	return m.push(F64(op(a.f64, b.f64)))
}

func isCollType(t uint8) bool {
	return t >= TypeCollI64 && t <= TypeCollStr
}

// elementType returns the scalar type for a collection type.
func elementType(collTyp uint8) uint8 {
	switch collTyp {
	case TypeCollI64:
		return TypeI64
	case TypeCollF64:
		return TypeF64
	case TypeCollBool:
		return TypeBool
	case TypeCollStr:
		return TypeStr
	default:
		return 0
	}
}
