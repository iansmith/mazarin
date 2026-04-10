package flat

// Type tags for flat values. The first 5 values intentionally match the
// heap-based vm.Type* constants for easy conversion.
const (
	TypeI64        uint8 = 0x01
	TypeF64        uint8 = 0x02
	TypeBool       uint8 = 0x03
	TypeTribool    uint8 = 0x04
	TypeStr        uint8 = 0x05
	TypeTimespec   uint8 = 0x06 // {Seconds int64, Nanos int32}
	TypeTimezone   uint8 = 0x07 // {OffsetMinutes int16}
	TypeDuration   uint8 = 0x08 // {Nanos int64}
	TypeDate       uint8 = 0x09 // {Year int16, Month uint8, Day uint8, TzMinutes int16}
	TypePoint2D    uint8 = 0x0A // {X int64, Y int64}
	TypePoint3D    uint8 = 0x0B // {X int64, Y int64, Z int64}
	TypePointF2D   uint8 = 0x0C // {X float64, Y float64}
	TypePointF3D   uint8 = 0x0D // {X float64, Y float64, Z float64}
	TypeRadians    uint8 = 0x0E // {Val float64}
	TypeDegrees    uint8 = 0x0F // {Val float64}
	TypeIPv4       uint8 = 0x10 // {Addr [4]byte}
	TypeIPv6       uint8 = 0x11 // {Addr [16]byte}
	TypeShepherdId   uint8 = 0x12 // {Id uint16, NameOffset uint32, NameLen uint16}
	TypeMazId      uint8 = 0x13 // {Id uint16, PathOffset uint32, PathLen uint16}
	TypeCollection uint8 = 0x14 // CollRef: {ElemType uint8, RegionOffset uint32, Count uint16}
	TypeRectangle  uint8 = 0x15 // {X0 int64, Y0 int64, X1 int64, Y1 int64}
)

// MaxTypeTag is the highest valid type tag.
const MaxTypeTag = TypeRectangle

// TypeInfo holds metadata about a flat type.
type TypeInfo struct {
	Name      string
	DataSize  int  // bytes used in Value.Data
	Composite bool // true for multi-field types
	Scalar    bool // true if usable as a collection element
}

var typeInfoTable = [MaxTypeTag + 1]TypeInfo{
	TypeI64:        {"i64", 8, false, true},
	TypeF64:        {"f64", 8, false, true},
	TypeBool:       {"bool", 1, false, true},
	TypeTribool:    {"tribool", 1, false, true},
	TypeStr:        {"str", 6, false, true},
	TypeTimespec:   {"timespec", 12, true, true},
	TypeTimezone:   {"timezone", 2, true, true},
	TypeDuration:   {"duration", 8, true, true},
	TypeDate:       {"date", 6, true, true},
	TypePoint2D:    {"point2d", 16, true, true},
	TypePoint3D:    {"point3d", 24, true, true},
	TypePointF2D:   {"pointf2d", 16, true, true},
	TypePointF3D:   {"pointf3d", 24, true, true},
	TypeRadians:    {"radians", 8, true, true},
	TypeDegrees:    {"degrees", 8, true, true},
	TypeIPv4:       {"ipv4", 4, true, true},
	TypeIPv6:       {"ipv6", 16, true, true},
	TypeShepherdId:   {"shepherd_id", 10, true, true},
	TypeMazId:      {"maz_id", 10, true, true},
	TypeCollection: {"collection", 10, false, false},
	TypeRectangle:  {"rectangle", 32, true, true},
}

// LookupType returns metadata for a type tag, or zero TypeInfo if invalid.
func LookupType(tag uint8) TypeInfo {
	if tag == 0 || tag > MaxTypeTag {
		return TypeInfo{}
	}
	return typeInfoTable[tag]
}

// TypeName returns the human-readable name for a type tag.
func TypeName(tag uint8) string {
	info := LookupType(tag)
	if info.Name == "" {
		return "unknown"
	}
	return info.Name
}

// IsScalarType returns true if the type can be used as a collection element.
func IsScalarType(tag uint8) bool {
	return LookupType(tag).Scalar
}

// IsCompositeType returns true if the type has multiple sub-fields.
func IsCompositeType(tag uint8) bool {
	return LookupType(tag).Composite
}
