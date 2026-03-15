package flat

import (
	"encoding/binary"
	"math"
)

// --- Timespec: {Seconds int64 @ 0, Nanos int32 @ 8} ---

func NewTimespec(seconds int64, nanos int32) FlatValue {
	var fv FlatValue
	fv.Typ = TypeTimespec
	binary.LittleEndian.PutUint64(fv.Data[0:8], uint64(seconds))
	binary.LittleEndian.PutUint32(fv.Data[8:12], uint32(nanos))
	return fv
}

func (fv FlatValue) AsTimespec() (seconds int64, nanos int32) {
	return int64(binary.LittleEndian.Uint64(fv.Data[0:8])),
		int32(binary.LittleEndian.Uint32(fv.Data[8:12]))
}

// --- Timezone: {OffsetMinutes int16 @ 0} ---

func NewTimezone(offsetMinutes int16) FlatValue {
	var fv FlatValue
	fv.Typ = TypeTimezone
	binary.LittleEndian.PutUint16(fv.Data[0:2], uint16(offsetMinutes))
	return fv
}

func (fv FlatValue) AsTimezone() int16 {
	return int16(binary.LittleEndian.Uint16(fv.Data[0:2]))
}

// --- Duration: {Nanos int64 @ 0} ---

func NewDuration(nanos int64) FlatValue {
	var fv FlatValue
	fv.Typ = TypeDuration
	binary.LittleEndian.PutUint64(fv.Data[0:8], uint64(nanos))
	return fv
}

func (fv FlatValue) AsDuration() int64 {
	return int64(binary.LittleEndian.Uint64(fv.Data[0:8]))
}

// --- Date: {Year int16 @ 0, Month uint8 @ 2, Day uint8 @ 3, TzMinutes int16 @ 4} ---

func NewDate(year int16, month uint8, day uint8, tzMinutes int16) FlatValue {
	var fv FlatValue
	fv.Typ = TypeDate
	binary.LittleEndian.PutUint16(fv.Data[0:2], uint16(year))
	fv.Data[2] = month
	fv.Data[3] = day
	binary.LittleEndian.PutUint16(fv.Data[4:6], uint16(tzMinutes))
	return fv
}

func (fv FlatValue) AsDate() (year int16, month uint8, day uint8, tzMinutes int16) {
	return int16(binary.LittleEndian.Uint16(fv.Data[0:2])),
		fv.Data[2],
		fv.Data[3],
		int16(binary.LittleEndian.Uint16(fv.Data[4:6]))
}

// --- Point2D: {X int64 @ 0, Y int64 @ 8} ---

func NewPoint2D(x, y int64) FlatValue {
	var fv FlatValue
	fv.Typ = TypePoint2D
	binary.LittleEndian.PutUint64(fv.Data[0:8], uint64(x))
	binary.LittleEndian.PutUint64(fv.Data[8:16], uint64(y))
	return fv
}

func (fv FlatValue) AsPoint2D() (x, y int64) {
	return int64(binary.LittleEndian.Uint64(fv.Data[0:8])),
		int64(binary.LittleEndian.Uint64(fv.Data[8:16]))
}

// --- Point3D: {X int64 @ 0, Y int64 @ 8, Z int64 @ 16} ---

func NewPoint3D(x, y, z int64) FlatValue {
	var fv FlatValue
	fv.Typ = TypePoint3D
	binary.LittleEndian.PutUint64(fv.Data[0:8], uint64(x))
	binary.LittleEndian.PutUint64(fv.Data[8:16], uint64(y))
	binary.LittleEndian.PutUint64(fv.Data[16:24], uint64(z))
	return fv
}

func (fv FlatValue) AsPoint3D() (x, y, z int64) {
	return int64(binary.LittleEndian.Uint64(fv.Data[0:8])),
		int64(binary.LittleEndian.Uint64(fv.Data[8:16])),
		int64(binary.LittleEndian.Uint64(fv.Data[16:24]))
}

// --- PointF2D: {X float64 @ 0, Y float64 @ 8} ---

func NewPointF2D(x, y float64) FlatValue {
	var fv FlatValue
	fv.Typ = TypePointF2D
	binary.LittleEndian.PutUint64(fv.Data[0:8], math.Float64bits(x))
	binary.LittleEndian.PutUint64(fv.Data[8:16], math.Float64bits(y))
	return fv
}

func (fv FlatValue) AsPointF2D() (x, y float64) {
	return math.Float64frombits(binary.LittleEndian.Uint64(fv.Data[0:8])),
		math.Float64frombits(binary.LittleEndian.Uint64(fv.Data[8:16]))
}

// --- PointF3D: {X float64 @ 0, Y float64 @ 8, Z float64 @ 16} ---

func NewPointF3D(x, y, z float64) FlatValue {
	var fv FlatValue
	fv.Typ = TypePointF3D
	binary.LittleEndian.PutUint64(fv.Data[0:8], math.Float64bits(x))
	binary.LittleEndian.PutUint64(fv.Data[8:16], math.Float64bits(y))
	binary.LittleEndian.PutUint64(fv.Data[16:24], math.Float64bits(z))
	return fv
}

func (fv FlatValue) AsPointF3D() (x, y, z float64) {
	return math.Float64frombits(binary.LittleEndian.Uint64(fv.Data[0:8])),
		math.Float64frombits(binary.LittleEndian.Uint64(fv.Data[8:16])),
		math.Float64frombits(binary.LittleEndian.Uint64(fv.Data[16:24]))
}

// --- Radians: {Val float64 @ 0} ---

func NewRadians(v float64) FlatValue {
	var fv FlatValue
	fv.Typ = TypeRadians
	binary.LittleEndian.PutUint64(fv.Data[0:8], math.Float64bits(v))
	return fv
}

func (fv FlatValue) AsRadians() float64 {
	return math.Float64frombits(binary.LittleEndian.Uint64(fv.Data[0:8]))
}

// --- Degrees: {Val float64 @ 0} ---

func NewDegrees(v float64) FlatValue {
	var fv FlatValue
	fv.Typ = TypeDegrees
	binary.LittleEndian.PutUint64(fv.Data[0:8], math.Float64bits(v))
	return fv
}

func (fv FlatValue) AsDegrees() float64 {
	return math.Float64frombits(binary.LittleEndian.Uint64(fv.Data[0:8]))
}

// --- IPv4: {Addr [4]byte @ 0} ---

func NewIPv4(a, b, c, d byte) FlatValue {
	var fv FlatValue
	fv.Typ = TypeIPv4
	fv.Data[0] = a
	fv.Data[1] = b
	fv.Data[2] = c
	fv.Data[3] = d
	return fv
}

func (fv FlatValue) AsIPv4() [4]byte {
	return [4]byte{fv.Data[0], fv.Data[1], fv.Data[2], fv.Data[3]}
}

// --- IPv6: {Addr [16]byte @ 0} ---

func NewIPv6(addr [16]byte) FlatValue {
	var fv FlatValue
	fv.Typ = TypeIPv6
	copy(fv.Data[0:16], addr[:])
	return fv
}

func (fv FlatValue) AsIPv6() [16]byte {
	var addr [16]byte
	copy(addr[:], fv.Data[0:16])
	return addr
}

// --- PriestId: {Id uint16 @ 0, NameOffset uint32 @ 4, NameLen uint16 @ 8} ---

func NewPriestId(id uint16, nameOffset uint32, nameLen uint16) FlatValue {
	var fv FlatValue
	fv.Typ = TypePriestId
	binary.LittleEndian.PutUint16(fv.Data[0:2], id)
	binary.LittleEndian.PutUint32(fv.Data[4:8], nameOffset)
	binary.LittleEndian.PutUint16(fv.Data[8:10], nameLen)
	return fv
}

func (fv FlatValue) AsPriestId() (id uint16, nameOffset uint32, nameLen uint16) {
	return binary.LittleEndian.Uint16(fv.Data[0:2]),
		binary.LittleEndian.Uint32(fv.Data[4:8]),
		binary.LittleEndian.Uint16(fv.Data[8:10])
}

// --- MazId: {Id uint16 @ 0, PathOffset uint32 @ 4, PathLen uint16 @ 8} ---

func NewMazId(id uint16, pathOffset uint32, pathLen uint16) FlatValue {
	var fv FlatValue
	fv.Typ = TypeMazId
	binary.LittleEndian.PutUint16(fv.Data[0:2], id)
	binary.LittleEndian.PutUint32(fv.Data[4:8], pathOffset)
	binary.LittleEndian.PutUint16(fv.Data[8:10], pathLen)
	return fv
}

func (fv FlatValue) AsMazId() (id uint16, pathOffset uint32, pathLen uint16) {
	return binary.LittleEndian.Uint16(fv.Data[0:2]),
		binary.LittleEndian.Uint32(fv.Data[4:8]),
		binary.LittleEndian.Uint16(fv.Data[8:10])
}

// --- Rectangle: {X0 int32 @ 0, Y0 int32 @ 4, X1 int32 @ 8, Y1 int32 @ 12} ---

func NewRectangle(x0, y0, x1, y1 int32) FlatValue {
	var fv FlatValue
	fv.Typ = TypeRectangle
	binary.LittleEndian.PutUint32(fv.Data[0:4], uint32(x0))
	binary.LittleEndian.PutUint32(fv.Data[4:8], uint32(y0))
	binary.LittleEndian.PutUint32(fv.Data[8:12], uint32(x1))
	binary.LittleEndian.PutUint32(fv.Data[12:16], uint32(y1))
	return fv
}

func (fv FlatValue) AsRectangle() (x0, y0, x1, y1 int32) {
	return int32(binary.LittleEndian.Uint32(fv.Data[0:4])),
		int32(binary.LittleEndian.Uint32(fv.Data[4:8])),
		int32(binary.LittleEndian.Uint32(fv.Data[8:12])),
		int32(binary.LittleEndian.Uint32(fv.Data[12:16]))
}

// RectEmpty returns true if the rectangle has zero or negative area.
func (fv FlatValue) RectEmpty() bool {
	x0, y0, x1, y1 := fv.AsRectangle()
	return x0 >= x1 || y0 >= y1
}
