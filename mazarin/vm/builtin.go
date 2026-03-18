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

	// Rectangle.
	BuiltinRect          uint16 = 60 // (I64, I64, I64, I64) → Rectangle
	BuiltinRectUnion     uint16 = 61 // (Rect, Rect) → Rect
	BuiltinRectIntersect uint16 = 62 // (Rect, Rect) → Rect
	BuiltinRectOverlaps  uint16 = 63 // (Rect, Rect) → Bool
	BuiltinRectContains  uint16 = 64 // (Rect, Rect) → Bool
	BuiltinRectEmpty     uint16 = 65 // (Rect) → Bool
	BuiltinRectArea      uint16 = 66 // (Rect) → I64
	BuiltinRectWidth     uint16 = 67 // (Rect) → I64
	BuiltinRectHeight    uint16 = 68 // (Rect) → I64

	// Trig.
	BuiltinSin      uint16 = 70 // (F64) → F64
	BuiltinCos      uint16 = 71 // (F64) → F64
	BuiltinTan      uint16 = 72 // (F64) → F64
	BuiltinAsin     uint16 = 73 // (F64) → F64
	BuiltinAcos     uint16 = 74 // (F64) → F64
	BuiltinAtan2    uint16 = 75 // (F64, F64) → F64
	BuiltinDegToRad uint16 = 76 // (F64) → F64
	BuiltinRadToDeg uint16 = 77 // (F64) → F64
	BuiltinAbsF     uint16 = 78 // (F64) → F64
	BuiltinPow      uint16 = 79 // (F64, F64) → F64

	// Composite constructors/accessors.
	BuiltinTimespec        uint16 = 100 // (I64, I64) → Timespec
	BuiltinTimespecSeconds uint16 = 101 // (Timespec) → I64
	BuiltinTimespecNanos   uint16 = 102 // (Timespec) → I64
	BuiltinTimezone        uint16 = 103 // (I64) → Timezone
	BuiltinTzConvert       uint16 = 104 // (Timespec, Timezone) → Timespec
	BuiltinDuration        uint16 = 105 // (I64) → Duration
	BuiltinDurationNanos   uint16 = 106 // (Duration) → I64
	BuiltinDate            uint16 = 107 // (I64, I64, I64) → Date
	BuiltinDateYear        uint16 = 108 // (Date) → I64
	BuiltinDateMonth       uint16 = 109 // (Date) → I64
	BuiltinDateDay         uint16 = 110 // (Date) → I64
	BuiltinPoint2D         uint16 = 111 // (I64, I64) → Point2D
	BuiltinPoint2DX        uint16 = 112 // (Point2D) → I64
	BuiltinPoint2DY        uint16 = 113 // (Point2D) → I64
	BuiltinPoint3D         uint16 = 114 // (I64, I64, I64) → Point3D
	BuiltinPointF2D        uint16 = 115 // (F64, F64) → PointF2D
	BuiltinPointF3D        uint16 = 116 // (F64, F64, F64) → PointF3D
	BuiltinIPv4            uint16 = 120 // (I64, I64, I64, I64) → IPv4
	BuiltinIPv4Octet       uint16 = 121 // (IPv4, I64) → I64
	BuiltinIPv6            uint16 = 122 // (CollI64) → IPv6
	BuiltinPriestId        uint16 = 130 // (I64) → PriestId
	BuiltinPriestIdNum     uint16 = 131 // (PriestId) → I64
	BuiltinMazId           uint16 = 132 // (I64) → MazId
	BuiltinMazIdNum        uint16 = 133 // (MazId) → I64

	// Service discovery.
	BuiltinFind        uint16 = 200 // (Str) → CollStr
	BuiltinDerefI64    uint16 = 201 // (Str) → I64 or Tribool(unknown)
	BuiltinDerefStr    uint16 = 202 // (Str) → Str or Tribool(unknown)
	BuiltinDerefBool   uint16 = 203 // (Str) → Bool or Tribool(unknown)
	BuiltinDerefF64    uint16 = 204 // (Str) → F64 or Tribool(unknown)
	BuiltinDerefRect   uint16 = 205 // (Str) → Rectangle or Tribool(unknown)
	BuiltinDerefPoint2D uint16 = 206 // (Str) → Point2D or Tribool(unknown)
	BuiltinDerefTribool uint16 = 207 // (Str) → Tribool
	BuiltinExists       uint16 = 208 // (Str) → Bool
	BuiltinURISegment   uint16 = 209 // (Str, I64) → Str
	BuiltinIsUnknown    uint16 = 210 // (any) → Bool

	// Side-effect builtins.
	BuiltinIncrementAtomic uint16 = 220 // (Str) → I64  atomically increment int64 attr
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

	// --- Rectangle ---

	case BuiltinRect:
		y1, err := m.pop()
		if err != nil {
			return err
		}
		x1, err := m.pop()
		if err != nil {
			return err
		}
		y0, err := m.pop()
		if err != nil {
			return err
		}
		x0, err := m.pop()
		if err != nil {
			return err
		}
		return m.push(RectangleVal(int32(x0.i64), int32(y0.i64), int32(x1.i64), int32(y1.i64)))

	case BuiltinRectUnion:
		return m.builtinRectBin(func(ax0, ay0, ax1, ay1, bx0, by0, bx1, by1 int32) Value {
			return RectangleVal(min32(ax0, bx0), min32(ay0, by0), max32(ax1, bx1), max32(ay1, by1))
		})

	case BuiltinRectIntersect:
		return m.builtinRectBin(func(ax0, ay0, ax1, ay1, bx0, by0, bx1, by1 int32) Value {
			ix0, iy0 := max32(ax0, bx0), max32(ay0, by0)
			ix1, iy1 := min32(ax1, bx1), min32(ay1, by1)
			if ix0 >= ix1 || iy0 >= iy1 {
				return RectangleVal(0, 0, 0, 0)
			}
			return RectangleVal(ix0, iy0, ix1, iy1)
		})

	case BuiltinRectOverlaps:
		return m.builtinRectBinBool(func(ax0, ay0, ax1, ay1, bx0, by0, bx1, by1 int32) bool {
			return ax0 < bx1 && bx0 < ax1 && ay0 < by1 && by0 < ay1
		})

	case BuiltinRectContains:
		return m.builtinRectBinBool(func(ax0, ay0, ax1, ay1, bx0, by0, bx1, by1 int32) bool {
			// a contains b
			return ax0 <= bx0 && ay0 <= by0 && ax1 >= bx1 && ay1 >= by1
		})

	case BuiltinRectEmpty:
		a, err := m.pop()
		if err != nil {
			return err
		}
		if a.typ != TypeRectangle {
			return m.haltf("rect_empty requires rectangle, got %s", TypeName(a.typ))
		}
		x0, y0, x1, y1 := a.AsRectangle()
		return m.push(Bool(x0 >= x1 || y0 >= y1))

	case BuiltinRectArea:
		a, err := m.pop()
		if err != nil {
			return err
		}
		if a.typ != TypeRectangle {
			return m.haltf("rect_area requires rectangle, got %s", TypeName(a.typ))
		}
		x0, y0, x1, y1 := a.AsRectangle()
		w := int64(x1 - x0)
		h := int64(y1 - y0)
		if w <= 0 || h <= 0 {
			return m.push(I64(0))
		}
		return m.push(I64(w * h))

	case BuiltinRectWidth:
		a, err := m.pop()
		if err != nil {
			return err
		}
		if a.typ != TypeRectangle {
			return m.haltf("rect_width requires rectangle, got %s", TypeName(a.typ))
		}
		x0, _, x1, _ := a.AsRectangle()
		w := int64(x1 - x0)
		if w < 0 {
			w = 0
		}
		return m.push(I64(w))

	case BuiltinRectHeight:
		a, err := m.pop()
		if err != nil {
			return err
		}
		if a.typ != TypeRectangle {
			return m.haltf("rect_height requires rectangle, got %s", TypeName(a.typ))
		}
		_, y0, _, y1 := a.AsRectangle()
		h := int64(y1 - y0)
		if h < 0 {
			h = 0
		}
		return m.push(I64(h))

	// --- Trig ---

	case BuiltinSin:
		return m.builtinUnaryF64(math.Sin)
	case BuiltinCos:
		return m.builtinUnaryF64(math.Cos)
	case BuiltinTan:
		return m.builtinUnaryF64(math.Tan)
	case BuiltinAsin:
		return m.builtinUnaryF64(math.Asin)
	case BuiltinAcos:
		return m.builtinUnaryF64(math.Acos)
	case BuiltinAtan2:
		return m.builtinBinF64(func(y, x float64) float64 { return math.Atan2(y, x) })
	case BuiltinDegToRad:
		return m.builtinUnaryF64(func(d float64) float64 { return d * math.Pi / 180.0 })
	case BuiltinRadToDeg:
		return m.builtinUnaryF64(func(r float64) float64 { return r * 180.0 / math.Pi })
	case BuiltinAbsF:
		return m.builtinUnaryF64(math.Abs)
	case BuiltinPow:
		return m.builtinBinF64(math.Pow)

	// --- Composite constructors/accessors ---

	case BuiltinTimespec:
		nanos, err := m.pop()
		if err != nil {
			return err
		}
		sec, err := m.pop()
		if err != nil {
			return err
		}
		return m.push(TimespecVal(sec.i64, int32(nanos.i64)))

	case BuiltinTimespecSeconds:
		a, err := m.pop()
		if err != nil {
			return err
		}
		if a.typ != TypeTimespec {
			return m.haltf("timespec_seconds requires timespec, got %s", TypeName(a.typ))
		}
		sec, _ := a.AsTimespec()
		return m.push(I64(sec))

	case BuiltinTimespecNanos:
		a, err := m.pop()
		if err != nil {
			return err
		}
		if a.typ != TypeTimespec {
			return m.haltf("timespec_nanos requires timespec, got %s", TypeName(a.typ))
		}
		_, ns := a.AsTimespec()
		return m.push(I64(int64(ns)))

	case BuiltinTimezone:
		off, err := m.pop()
		if err != nil {
			return err
		}
		return m.push(TimezoneVal(int16(off.i64)))

	case BuiltinTzConvert:
		tz, err := m.pop()
		if err != nil {
			return err
		}
		ts, err := m.pop()
		if err != nil {
			return err
		}
		if ts.typ != TypeTimespec || tz.typ != TypeTimezone {
			return m.haltf("tz_convert requires (timespec, timezone)")
		}
		sec, ns := ts.AsTimespec()
		off := tz.AsTimezone()
		sec += int64(off) * 60
		return m.push(TimespecVal(sec, ns))

	case BuiltinDuration:
		ns, err := m.pop()
		if err != nil {
			return err
		}
		return m.push(DurationVal(ns.i64))

	case BuiltinDurationNanos:
		a, err := m.pop()
		if err != nil {
			return err
		}
		if a.typ != TypeDuration {
			return m.haltf("duration_nanos requires duration, got %s", TypeName(a.typ))
		}
		return m.push(I64(a.AsDuration()))

	case BuiltinDate:
		day, err := m.pop()
		if err != nil {
			return err
		}
		month, err := m.pop()
		if err != nil {
			return err
		}
		year, err := m.pop()
		if err != nil {
			return err
		}
		return m.push(DateVal(int16(year.i64), uint8(month.i64), uint8(day.i64), 0))

	case BuiltinDateYear:
		a, err := m.pop()
		if err != nil {
			return err
		}
		if a.typ != TypeDate {
			return m.haltf("date_year requires date, got %s", TypeName(a.typ))
		}
		y, _, _, _ := a.AsDate()
		return m.push(I64(int64(y)))

	case BuiltinDateMonth:
		a, err := m.pop()
		if err != nil {
			return err
		}
		if a.typ != TypeDate {
			return m.haltf("date_month requires date, got %s", TypeName(a.typ))
		}
		_, m2, _, _ := a.AsDate()
		return m.push(I64(int64(m2)))

	case BuiltinDateDay:
		a, err := m.pop()
		if err != nil {
			return err
		}
		if a.typ != TypeDate {
			return m.haltf("date_day requires date, got %s", TypeName(a.typ))
		}
		_, _, d, _ := a.AsDate()
		return m.push(I64(int64(d)))

	case BuiltinPoint2D:
		y, err := m.pop()
		if err != nil {
			return err
		}
		x, err := m.pop()
		if err != nil {
			return err
		}
		return m.push(Point2DVal(x.i64, y.i64))

	case BuiltinPoint2DX:
		a, err := m.pop()
		if err != nil {
			return err
		}
		if a.typ != TypePoint2D {
			return m.haltf("point2d_x requires point2d, got %s", TypeName(a.typ))
		}
		x, _ := a.AsPoint2D()
		return m.push(I64(x))

	case BuiltinPoint2DY:
		a, err := m.pop()
		if err != nil {
			return err
		}
		if a.typ != TypePoint2D {
			return m.haltf("point2d_y requires point2d, got %s", TypeName(a.typ))
		}
		_, y := a.AsPoint2D()
		return m.push(I64(y))

	case BuiltinPoint3D:
		z, err := m.pop()
		if err != nil {
			return err
		}
		y, err := m.pop()
		if err != nil {
			return err
		}
		x, err := m.pop()
		if err != nil {
			return err
		}
		return m.push(Point3DVal(x.i64, y.i64, z.i64))

	case BuiltinPointF2D:
		y, err := m.pop()
		if err != nil {
			return err
		}
		x, err := m.pop()
		if err != nil {
			return err
		}
		return m.push(PointF2DVal(x.f64, y.f64))

	case BuiltinPointF3D:
		z, err := m.pop()
		if err != nil {
			return err
		}
		y, err := m.pop()
		if err != nil {
			return err
		}
		x, err := m.pop()
		if err != nil {
			return err
		}
		return m.push(PointF3DVal(x.f64, y.f64, z.f64))

	case BuiltinIPv4:
		d, err := m.pop()
		if err != nil {
			return err
		}
		c, err := m.pop()
		if err != nil {
			return err
		}
		b, err := m.pop()
		if err != nil {
			return err
		}
		a, err := m.pop()
		if err != nil {
			return err
		}
		return m.push(IPv4Val(byte(a.i64), byte(b.i64), byte(c.i64), byte(d.i64)))

	case BuiltinIPv4Octet:
		idx, err := m.pop()
		if err != nil {
			return err
		}
		ip, err := m.pop()
		if err != nil {
			return err
		}
		if ip.typ != TypeIPv4 {
			return m.haltf("ipv4_octet requires ipv4, got %s", TypeName(ip.typ))
		}
		i := int(idx.i64)
		if i < 0 || i > 3 {
			return m.haltf("ipv4_octet index %d out of range (0-3)", i)
		}
		addr := ip.AsIPv4()
		return m.push(I64(int64(addr[i])))

	case BuiltinIPv6:
		coll, err := m.pop()
		if err != nil {
			return err
		}
		if !isCollType(coll.typ) || len(coll.coll) != 16 {
			return m.haltf("ipv6 requires collection of 16 bytes")
		}
		var addr [16]byte
		for i := 0; i < 16; i++ {
			addr[i] = byte(coll.coll[i].i64)
		}
		return m.push(IPv6Val(addr))

	case BuiltinPriestId:
		id, err := m.pop()
		if err != nil {
			return err
		}
		return m.push(PriestIdVal(uint16(id.i64)))

	case BuiltinPriestIdNum:
		a, err := m.pop()
		if err != nil {
			return err
		}
		if a.typ != TypePriestId {
			return m.haltf("priest_id_num requires priest_id, got %s", TypeName(a.typ))
		}
		return m.push(I64(int64(a.AsPriestIdNum())))

	case BuiltinMazId:
		id, err := m.pop()
		if err != nil {
			return err
		}
		return m.push(MazIdVal(uint16(id.i64)))

	case BuiltinMazIdNum:
		a, err := m.pop()
		if err != nil {
			return err
		}
		if a.typ != TypeMazId {
			return m.haltf("maz_id_num requires maz_id, got %s", TypeName(a.typ))
		}
		return m.push(I64(int64(a.AsMazIdNum())))

	// --- Service discovery ---

	case BuiltinFind:
		pattern, err := m.pop()
		if err != nil {
			return err
		}
		if pattern.typ != TypeStr {
			return m.haltf("find requires string, got %s", TypeName(pattern.typ))
		}
		if m.resolver == nil {
			return m.haltf("find: no resolver configured")
		}
		uris, querySlot := m.resolver.Find(pattern.str)
		m.readSet.Add(querySlot)
		return m.push(CollStr(uris))

	case BuiltinDerefI64:
		return m.builtinDeref(TypeI64)
	case BuiltinDerefStr:
		return m.builtinDeref(TypeStr)
	case BuiltinDerefBool:
		return m.builtinDeref(TypeBool)
	case BuiltinDerefF64:
		return m.builtinDeref(TypeF64)
	case BuiltinDerefRect:
		return m.builtinDeref(TypeRectangle)
	case BuiltinDerefPoint2D:
		return m.builtinDeref(TypePoint2D)
	case BuiltinDerefTribool:
		return m.builtinDeref(TypeTribool)

	case BuiltinExists:
		prefix, err := m.pop()
		if err != nil {
			return err
		}
		if prefix.typ != TypeStr {
			return m.haltf("exists requires string, got %s", TypeName(prefix.typ))
		}
		if m.resolver == nil {
			return m.haltf("exists: no resolver configured")
		}
		exists, slot := m.resolver.Exists(prefix.str)
		m.readSet.Add(slot)
		return m.push(Bool(exists))

	case BuiltinURISegment:
		idx, err := m.pop()
		if err != nil {
			return err
		}
		uri, err := m.pop()
		if err != nil {
			return err
		}
		if uri.typ != TypeStr {
			return m.haltf("uri_segment requires string, got %s", TypeName(uri.typ))
		}
		seg := uriSegment(uri.str, int(idx.i64))
		return m.push(Str(seg))

	case BuiltinIsUnknown:
		a, err := m.pop()
		if err != nil {
			return err
		}
		return m.push(Bool(a.typ == TypeTribool && a.i64 == TriboolUnknown))

	// --- Side-effect builtins ---

	case BuiltinIncrementAtomic:
		uri, err := m.pop()
		if err != nil {
			return err
		}
		if uri.typ != TypeStr {
			return m.haltf("increment_atomic requires string, got %s", TypeName(uri.typ))
		}
		if m.resolver == nil {
			return m.haltf("increment_atomic: no resolver configured")
		}
		newVal, ok := m.resolver.IncrementI64(uri.str)
		if !ok {
			return m.push(I64(0))
		}
		return m.push(I64(newVal))

	default:
		return m.haltf("unknown builtin %d", id)
	}
}

// --- Builtin helpers ---

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

func (m *machine) builtinUnaryF64(op func(float64) float64) error {
	a, err := m.pop()
	if err != nil {
		return err
	}
	return m.push(F64(op(a.f64)))
}

func (m *machine) builtinRectBin(op func(ax0, ay0, ax1, ay1, bx0, by0, bx1, by1 int32) Value) error {
	b, err := m.pop()
	if err != nil {
		return err
	}
	a, err := m.pop()
	if err != nil {
		return err
	}
	if a.typ != TypeRectangle || b.typ != TypeRectangle {
		return m.haltf("rect operation requires two rectangles, got %s and %s", TypeName(a.typ), TypeName(b.typ))
	}
	ax0, ay0, ax1, ay1 := a.AsRectangle()
	bx0, by0, bx1, by1 := b.AsRectangle()
	return m.push(op(ax0, ay0, ax1, ay1, bx0, by0, bx1, by1))
}

func (m *machine) builtinRectBinBool(op func(ax0, ay0, ax1, ay1, bx0, by0, bx1, by1 int32) bool) error {
	b, err := m.pop()
	if err != nil {
		return err
	}
	a, err := m.pop()
	if err != nil {
		return err
	}
	if a.typ != TypeRectangle || b.typ != TypeRectangle {
		return m.haltf("rect operation requires two rectangles, got %s and %s", TypeName(a.typ), TypeName(b.typ))
	}
	ax0, ay0, ax1, ay1 := a.AsRectangle()
	bx0, by0, bx1, by1 := b.AsRectangle()
	return m.push(Bool(op(ax0, ay0, ax1, ay1, bx0, by0, bx1, by1)))
}

func (m *machine) builtinDeref(expectedType uint8) error {
	uri, err := m.pop()
	if err != nil {
		return err
	}
	if uri.typ != TypeStr {
		return m.haltf("deref requires string, got %s", TypeName(uri.typ))
	}
	if m.resolver == nil {
		return m.haltf("deref: no resolver configured")
	}
	val, slot, ok := m.resolver.Deref(uri.str, expectedType)
	m.readSet.Add(slot)
	if !ok {
		return m.push(Tribool(TriboolUnknown))
	}
	return m.push(val)
}

func min32(a, b int32) int32 {
	if a < b {
		return a
	}
	return b
}

func max32(a, b int32) int32 {
	if a > b {
		return a
	}
	return b
}

// uriSegment extracts segment at index from a URI string.
// URI format: "attr:///seg0/seg1/seg2/..."
func uriSegment(uri string, index int) string {
	const prefix = "attr:///"
	if len(uri) < len(prefix) || uri[:len(prefix)] != prefix {
		return ""
	}
	rest := uri[len(prefix):]
	seg := 0
	start := 0
	for i := 0; i <= len(rest); i++ {
		if i == len(rest) || rest[i] == '/' {
			if i > start {
				if seg == index {
					return rest[start:i]
				}
				seg++
			}
			start = i + 1
		}
	}
	return ""
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
