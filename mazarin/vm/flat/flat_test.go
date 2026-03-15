package flat

import (
	"math"
	"testing"

	"mazzy/mazarin/vm"
)

// --- FlatValue scalar round-trips ---

func TestFlatValueI64(t *testing.T) {
	for _, v := range []int64{0, 1, -1, math.MaxInt64, math.MinInt64, 42} {
		fv := NewI64(v)
		if fv.Typ != TypeI64 {
			t.Errorf("type = %d, want %d", fv.Typ, TypeI64)
		}
		if got := fv.AsI64(); got != v {
			t.Errorf("AsI64() = %d, want %d", got, v)
		}
	}
}

func TestFlatValueF64(t *testing.T) {
	for _, v := range []float64{0, 1.5, -3.14, math.MaxFloat64, math.SmallestNonzeroFloat64, math.Inf(1)} {
		fv := NewF64(v)
		if fv.Typ != TypeF64 {
			t.Errorf("type = %d, want %d", fv.Typ, TypeF64)
		}
		if got := fv.AsF64(); got != v {
			t.Errorf("AsF64() = %g, want %g", got, v)
		}
	}
}

func TestFlatValueBool(t *testing.T) {
	for _, v := range []bool{true, false} {
		fv := NewBool(v)
		if fv.Typ != TypeBool {
			t.Errorf("type = %d, want %d", fv.Typ, TypeBool)
		}
		if got := fv.AsBool(); got != v {
			t.Errorf("AsBool() = %v, want %v", got, v)
		}
	}
}

func TestFlatValueTribool(t *testing.T) {
	for _, v := range []int64{0, 1, 2} {
		fv := NewTribool(v)
		if fv.Typ != TypeTribool {
			t.Errorf("type = %d, want %d", fv.Typ, TypeTribool)
		}
		if got := fv.AsTribool(); got != v {
			t.Errorf("AsTribool() = %d, want %d", got, v)
		}
	}
}

func TestFlatValueStr(t *testing.T) {
	ref := FlatStrRef{RegionOffset: 512, Len: 11}
	fv := NewStr(ref)
	if fv.Typ != TypeStr {
		t.Errorf("type = %d, want %d", fv.Typ, TypeStr)
	}
	got := fv.AsStrRef()
	if got.RegionOffset != 512 || got.Len != 11 {
		t.Errorf("AsStrRef() = %+v, want %+v", got, ref)
	}
}

func TestFlatValueCollection(t *testing.T) {
	ref := FlatCollRef{ElemType: TypeI64, RegionOffset: 1024, Count: 5}
	fv := NewCollection(ref)
	if fv.Typ != TypeCollection {
		t.Errorf("type = %d, want %d", fv.Typ, TypeCollection)
	}
	got := fv.AsCollRef()
	if got.ElemType != TypeI64 || got.RegionOffset != 1024 || got.Count != 5 {
		t.Errorf("AsCollRef() = %+v, want %+v", got, ref)
	}
}

// --- Composite type round-trips ---

func TestCompositeTimespec(t *testing.T) {
	fv := NewTimespec(1710000000, 123456789)
	s, n := fv.AsTimespec()
	if s != 1710000000 || n != 123456789 {
		t.Errorf("Timespec = (%d, %d), want (1710000000, 123456789)", s, n)
	}
	// Negative seconds (before epoch).
	fv2 := NewTimespec(-100, -500)
	s2, n2 := fv2.AsTimespec()
	if s2 != -100 || n2 != -500 {
		t.Errorf("Timespec negative = (%d, %d), want (-100, -500)", s2, n2)
	}
}

func TestCompositeTimezone(t *testing.T) {
	for _, v := range []int16{0, 330, -720, 720, -60} {
		fv := NewTimezone(v)
		if got := fv.AsTimezone(); got != v {
			t.Errorf("Timezone(%d) round-trip = %d", v, got)
		}
	}
}

func TestCompositeDuration(t *testing.T) {
	for _, v := range []int64{0, 1_000_000_000, -5_000_000, math.MaxInt64} {
		fv := NewDuration(v)
		if got := fv.AsDuration(); got != v {
			t.Errorf("Duration(%d) round-trip = %d", v, got)
		}
	}
}

func TestCompositeDate(t *testing.T) {
	fv := NewDate(2026, 3, 15, 330)
	y, m, d, tz := fv.AsDate()
	if y != 2026 || m != 3 || d != 15 || tz != 330 {
		t.Errorf("Date = (%d, %d, %d, %d), want (2026, 3, 15, 330)", y, m, d, tz)
	}
	// Negative year (BCE).
	fv2 := NewDate(-44, 3, 15, 0)
	y2, _, _, _ := fv2.AsDate()
	if y2 != -44 {
		t.Errorf("Date year = %d, want -44", y2)
	}
}

func TestCompositePoint2D(t *testing.T) {
	fv := NewPoint2D(100, -200)
	x, y := fv.AsPoint2D()
	if x != 100 || y != -200 {
		t.Errorf("Point2D = (%d, %d), want (100, -200)", x, y)
	}
}

func TestCompositePoint3D(t *testing.T) {
	fv := NewPoint3D(1, 2, 3)
	x, y, z := fv.AsPoint3D()
	if x != 1 || y != 2 || z != 3 {
		t.Errorf("Point3D = (%d, %d, %d), want (1, 2, 3)", x, y, z)
	}
}

func TestCompositePointF2D(t *testing.T) {
	fv := NewPointF2D(1.5, -2.5)
	x, y := fv.AsPointF2D()
	if x != 1.5 || y != -2.5 {
		t.Errorf("PointF2D = (%g, %g), want (1.5, -2.5)", x, y)
	}
}

func TestCompositePointF3D(t *testing.T) {
	fv := NewPointF3D(math.Pi, math.E, math.Phi)
	x, y, z := fv.AsPointF3D()
	if x != math.Pi || y != math.E || z != math.Phi {
		t.Errorf("PointF3D = (%g, %g, %g), want (pi, e, phi)", x, y, z)
	}
}

func TestCompositeRadiansDegrees(t *testing.T) {
	fv := NewRadians(math.Pi)
	if got := fv.AsRadians(); got != math.Pi {
		t.Errorf("Radians = %g, want pi", got)
	}
	fv2 := NewDegrees(180.0)
	if got := fv2.AsDegrees(); got != 180.0 {
		t.Errorf("Degrees = %g, want 180", got)
	}
}

func TestCompositeIPv4(t *testing.T) {
	fv := NewIPv4(192, 168, 1, 1)
	addr := fv.AsIPv4()
	if addr != [4]byte{192, 168, 1, 1} {
		t.Errorf("IPv4 = %v, want [192 168 1 1]", addr)
	}
}

func TestCompositeIPv6(t *testing.T) {
	var addr [16]byte
	// ::1 (loopback)
	addr[15] = 1
	fv := NewIPv6(addr)
	got := fv.AsIPv6()
	if got != addr {
		t.Errorf("IPv6 round-trip failed")
	}
	// All ones.
	var allOnes [16]byte
	for i := range allOnes {
		allOnes[i] = 0xFF
	}
	fv2 := NewIPv6(allOnes)
	if fv2.AsIPv6() != allOnes {
		t.Errorf("IPv6 all-ones round-trip failed")
	}
}

func TestCompositePriestId(t *testing.T) {
	fv := NewPriestId(42, 1024, 7)
	id, off, ln := fv.AsPriestId()
	if id != 42 || off != 1024 || ln != 7 {
		t.Errorf("PriestId = (%d, %d, %d), want (42, 1024, 7)", id, off, ln)
	}
}

func TestCompositeMazId(t *testing.T) {
	fv := NewMazId(99, 2048, 12)
	id, off, ln := fv.AsMazId()
	if id != 99 || off != 2048 || ln != 12 {
		t.Errorf("MazId = (%d, %d, %d), want (99, 2048, 12)", id, off, ln)
	}
}

func TestCompositeRectangle(t *testing.T) {
	fv := NewRectangle(10, 20, 100, 200)
	x0, y0, x1, y1 := fv.AsRectangle()
	if x0 != 10 || y0 != 20 || x1 != 100 || y1 != 200 {
		t.Errorf("Rectangle = (%d, %d, %d, %d), want (10, 20, 100, 200)", x0, y0, x1, y1)
	}
	if fv.RectEmpty() {
		t.Error("non-empty rectangle reported as empty")
	}
	// Empty rectangle.
	empty := NewRectangle(0, 0, 0, 0)
	if !empty.RectEmpty() {
		t.Error("empty rectangle not reported as empty")
	}
	// Negative-area rectangle.
	neg := NewRectangle(100, 100, 50, 50)
	if !neg.RectEmpty() {
		t.Error("negative-area rectangle not reported as empty")
	}
	// Negative coordinates.
	fv2 := NewRectangle(-100, -200, -50, -50)
	x0, y0, x1, y1 = fv2.AsRectangle()
	if x0 != -100 || y0 != -200 || x1 != -50 || y1 != -50 {
		t.Errorf("negative rect = (%d, %d, %d, %d)", x0, y0, x1, y1)
	}
}

// --- FlatAttrNode ---

func TestFlatAttrNode(t *testing.T) {
	pr := NewPageRegion(16, 64, 0, 0, 0)
	idx, err := pr.AllocNode()
	if err != nil {
		t.Fatal(err)
	}
	node := pr.Node(idx)
	node.Owner = 1
	node.Kind = AttrKindConstraint
	node.ValueType = TypeI64
	node.CachedValue = NewI64(42)
	node.SetDirty(true)

	// Read back.
	n := pr.Node(idx)
	if n.Owner != 1 {
		t.Errorf("Owner = %d, want 1", n.Owner)
	}
	if n.Kind != AttrKindConstraint {
		t.Errorf("Kind = %d, want %d", n.Kind, AttrKindConstraint)
	}
	if n.ValueType != TypeI64 {
		t.Errorf("ValueType = %d, want %d", n.ValueType, TypeI64)
	}
	if n.CachedValue.AsI64() != 42 {
		t.Errorf("CachedValue = %d, want 42", n.CachedValue.AsI64())
	}
	if !n.IsDirty() {
		t.Error("expected dirty")
	}
	n.SetDirty(false)
	if n.IsDirty() {
		t.Error("expected not dirty after clear")
	}
}

// --- Bitmap allocator ---

func TestBitmapAllocator(t *testing.T) {
	pr := NewPageRegion(8, 0, 0, 0, 0) // 8 node slots

	// Allocate all 8.
	var indices []int16
	for i := 0; i < 8; i++ {
		idx, err := pr.AllocNode()
		if err != nil {
			t.Fatalf("AllocNode %d: %v", i, err)
		}
		indices = append(indices, idx)
	}

	// Should be full.
	_, err := pr.AllocNode()
	if err == nil {
		t.Fatal("expected error when full")
	}

	// Free some and reallocate.
	pr.FreeNode(indices[3])
	pr.FreeNode(indices[5])

	idx1, err := pr.AllocNode()
	if err != nil {
		t.Fatal(err)
	}
	if idx1 != indices[3] {
		t.Errorf("expected reuse of slot %d, got %d", indices[3], idx1)
	}
	idx2, err := pr.AllocNode()
	if err != nil {
		t.Fatal(err)
	}
	if idx2 != indices[5] {
		t.Errorf("expected reuse of slot %d, got %d", indices[5], idx2)
	}
}

// --- PageRegion string allocation ---

func TestPageRegionStrings(t *testing.T) {
	pr := NewPageRegion(0, 0, 0, 16, 0)

	// Write and read back.
	ref, err := pr.WriteString("hello world")
	if err != nil {
		t.Fatal(err)
	}
	if ref.Len != 11 {
		t.Errorf("Len = %d, want 11", ref.Len)
	}
	got := pr.ReadString(ref)
	if got != "hello world" {
		t.Errorf("ReadString = %q, want %q", got, "hello world")
	}

	// NUL termination.
	nextByte := pr.Strings[ref.RegionOffset+uint32(ref.Len)]
	if nextByte != 0 {
		t.Errorf("byte after string = %d, want 0 (NUL)", nextByte)
	}

	// Empty string.
	ref2, err := pr.WriteString("")
	if err != nil {
		t.Fatal(err)
	}
	if ref2.Len != 0 {
		t.Errorf("empty string Len = %d", ref2.Len)
	}
	if pr.ReadString(ref2) != "" {
		t.Error("empty string not empty")
	}

	// Max-length string.
	maxStr := make([]byte, FlatStringMaxLen)
	for i := range maxStr {
		maxStr[i] = 'A'
	}
	ref3, err := pr.WriteString(string(maxStr))
	if err != nil {
		t.Fatal(err)
	}
	if pr.ReadString(ref3) != string(maxStr) {
		t.Error("max-length string round-trip failed")
	}

	// Too-long string.
	tooLong := make([]byte, FlatStringMaxLen+1)
	_, err = pr.WriteString(string(tooLong))
	if err == nil {
		t.Error("expected error for too-long string")
	}
}

// --- PageRegion collection allocation ---

func TestPageRegionCollections(t *testing.T) {
	pr := NewPageRegion(0, 0, 0, 4, 64)

	// I64 collection.
	elems := []FlatValue{NewI64(10), NewI64(20), NewI64(30)}
	ref, err := pr.WriteCollection(TypeI64, elems)
	if err != nil {
		t.Fatal(err)
	}
	if ref.Count != 3 || ref.ElemType != TypeI64 {
		t.Errorf("CollRef = %+v", ref)
	}
	for i, want := range []int64{10, 20, 30} {
		got := pr.ReadCollectionElement(ref, i)
		if got.AsI64() != want {
			t.Errorf("elem[%d] = %d, want %d", i, got.AsI64(), want)
		}
	}

	// Str collection (references into string region).
	sref1, _ := pr.WriteString("foo")
	sref2, _ := pr.WriteString("bar")
	strElems := []FlatValue{NewStr(sref1), NewStr(sref2)}
	cref, err := pr.WriteCollection(TypeStr, strElems)
	if err != nil {
		t.Fatal(err)
	}
	elem0 := pr.ReadCollectionElement(cref, 0)
	if pr.ReadString(elem0.AsStrRef()) != "foo" {
		t.Error("string collection elem 0 failed")
	}
	elem1 := pr.ReadCollectionElement(cref, 1)
	if pr.ReadString(elem1.AsStrRef()) != "bar" {
		t.Error("string collection elem 1 failed")
	}
}

// --- Edge array ---

func TestEdgeArray(t *testing.T) {
	pr := NewPageRegion(16, 128, 0, 0, 0)

	// Allocate two nodes.
	n0, _ := pr.AllocNode()
	n1, _ := pr.AllocNode()
	n2, _ := pr.AllocNode()

	// n0 depends on n1, n2.
	depsOffset, err := pr.WriteEdges([]uint16{uint16(n1), uint16(n2)})
	if err != nil {
		t.Fatal(err)
	}
	node0 := pr.Node(n0)
	node0.DepsOffset = depsOffset
	node0.DepsCount = 2

	// n1 has n0 as dependent.
	deptsOffset, err := pr.WriteEdges([]uint16{uint16(n0)})
	if err != nil {
		t.Fatal(err)
	}
	node1 := pr.Node(n1)
	node1.DependentsOffset = deptsOffset
	node1.DependentsCount = 1

	// Verify traversal.
	node0 = pr.Node(n0) // re-read
	if node0.DepsCount != 2 {
		t.Fatalf("DepsCount = %d", node0.DepsCount)
	}
	dep0 := pr.ReadEdge(node0.DepsOffset, 0)
	dep1 := pr.ReadEdge(node0.DepsOffset, 1)
	if dep0 != uint16(n1) || dep1 != uint16(n2) {
		t.Errorf("deps = [%d, %d], want [%d, %d]", dep0, dep1, n1, n2)
	}

	node1 = pr.Node(n1)
	dpt0 := pr.ReadEdge(node1.DependentsOffset, 0)
	if dpt0 != uint16(n0) {
		t.Errorf("dependent = %d, want %d", dpt0, n0)
	}
}

// --- Page layout no-overlap ---

func TestPageRegionNoOverlap(t *testing.T) {
	pr := NewDefaultPageRegion()

	// Fill each region partially and verify no cross-contamination.
	// Write a node.
	idx, _ := pr.AllocNode()
	node := pr.Node(idx)
	node.Owner = 0xBEEF

	// Write a string.
	sref, _ := pr.WriteString("test-string")

	// Write edges.
	_, _ = pr.WriteEdges([]uint16{0, 1, 2})

	// Write a collection.
	elems := []FlatValue{NewI64(999)}
	_, _ = pr.WriteCollection(TypeI64, elems)

	// Verify everything is intact.
	n := pr.Node(idx)
	if n.Owner != 0xBEEF {
		t.Error("node corrupted")
	}
	if pr.ReadString(sref) != "test-string" {
		t.Error("string corrupted")
	}
}

// --- vm.Value ↔ FlatValue conversion ---

func TestConvertI64RoundTrip(t *testing.T) {
	pr := NewDefaultPageRegion()
	v := vm.I64(42)
	fv, err := ValueToFlat(v, pr)
	if err != nil {
		t.Fatal(err)
	}
	got, err := FlatToValue(fv, pr)
	if err != nil {
		t.Fatal(err)
	}
	if got.AsI64() != 42 {
		t.Errorf("round-trip I64 = %d, want 42", got.AsI64())
	}
}

func TestConvertF64RoundTrip(t *testing.T) {
	pr := NewDefaultPageRegion()
	v := vm.F64(3.14)
	fv, err := ValueToFlat(v, pr)
	if err != nil {
		t.Fatal(err)
	}
	got, err := FlatToValue(fv, pr)
	if err != nil {
		t.Fatal(err)
	}
	if got.AsF64() != 3.14 {
		t.Errorf("round-trip F64 = %g, want 3.14", got.AsF64())
	}
}

func TestConvertBoolRoundTrip(t *testing.T) {
	pr := NewDefaultPageRegion()
	for _, b := range []bool{true, false} {
		v := vm.Bool(b)
		fv, err := ValueToFlat(v, pr)
		if err != nil {
			t.Fatal(err)
		}
		got, err := FlatToValue(fv, pr)
		if err != nil {
			t.Fatal(err)
		}
		if got.AsBool() != b {
			t.Errorf("round-trip Bool(%v) = %v", b, got.AsBool())
		}
	}
}

func TestConvertTriboolRoundTrip(t *testing.T) {
	pr := NewDefaultPageRegion()
	for _, tv := range []int64{0, 1, 2} {
		v := vm.Tribool(tv)
		fv, err := ValueToFlat(v, pr)
		if err != nil {
			t.Fatal(err)
		}
		got, err := FlatToValue(fv, pr)
		if err != nil {
			t.Fatal(err)
		}
		if got.AsI64() != tv {
			t.Errorf("round-trip Tribool(%d) = %d", tv, got.AsI64())
		}
	}
}

func TestConvertStrRoundTrip(t *testing.T) {
	pr := NewDefaultPageRegion()
	v := vm.Str("hello constraint")
	fv, err := ValueToFlat(v, pr)
	if err != nil {
		t.Fatal(err)
	}
	got, err := FlatToValue(fv, pr)
	if err != nil {
		t.Fatal(err)
	}
	if got.AsStr() != "hello constraint" {
		t.Errorf("round-trip Str = %q, want %q", got.AsStr(), "hello constraint")
	}
}

func TestConvertCollI64RoundTrip(t *testing.T) {
	pr := NewDefaultPageRegion()
	v := vm.CollI64([]int64{10, 20, 30})
	fv, err := ValueToFlat(v, pr)
	if err != nil {
		t.Fatal(err)
	}
	got, err := FlatToValue(fv, pr)
	if err != nil {
		t.Fatal(err)
	}
	coll := got.AsColl()
	if len(coll) != 3 {
		t.Fatalf("coll len = %d, want 3", len(coll))
	}
	for i, want := range []int64{10, 20, 30} {
		if coll[i].AsI64() != want {
			t.Errorf("coll[%d] = %d, want %d", i, coll[i].AsI64(), want)
		}
	}
}

func TestConvertCollStrRoundTrip(t *testing.T) {
	pr := NewDefaultPageRegion()
	v := vm.CollStr([]string{"alpha", "beta", "gamma"})
	fv, err := ValueToFlat(v, pr)
	if err != nil {
		t.Fatal(err)
	}
	got, err := FlatToValue(fv, pr)
	if err != nil {
		t.Fatal(err)
	}
	coll := got.AsColl()
	if len(coll) != 3 {
		t.Fatalf("coll len = %d, want 3", len(coll))
	}
	for i, want := range []string{"alpha", "beta", "gamma"} {
		if coll[i].AsStr() != want {
			t.Errorf("coll[%d] = %q, want %q", i, coll[i].AsStr(), want)
		}
	}
}

// --- Type metadata ---

func TestTypeInfo(t *testing.T) {
	// All 21 type tags should have non-empty names.
	for tag := uint8(1); tag <= MaxTypeTag; tag++ {
		info := LookupType(tag)
		if info.Name == "" {
			t.Errorf("type tag 0x%02x has empty name", tag)
		}
	}
	// Invalid tags.
	if info := LookupType(0); info.Name != "" {
		t.Errorf("tag 0 should be invalid, got %q", info.Name)
	}
	if info := LookupType(0xFF); info.Name != "" {
		t.Errorf("tag 0xFF should be invalid, got %q", info.Name)
	}
	// Collection is not scalar.
	if IsScalarType(TypeCollection) {
		t.Error("TypeCollection should not be scalar")
	}
	// Rectangle is scalar (usable in collections).
	if !IsScalarType(TypeRectangle) {
		t.Error("TypeRectangle should be scalar")
	}
}

// --- FlatValue.String() ---

func TestFlatValueString(t *testing.T) {
	cases := []struct {
		fv   FlatValue
		want string
	}{
		{NewI64(42), "i64(42)"},
		{NewF64(3.14), "f64(3.14)"},
		{NewBool(true), "bool(true)"},
		{NewBool(false), "bool(false)"},
		{NewTribool(2), "tribool(unknown)"},
		{NewTimespec(0, 0), "timespec(...)"},
		{NewRectangle(1, 2, 3, 4), "rectangle(...)"},
	}
	for _, c := range cases {
		got := c.fv.String()
		if got != c.want {
			t.Errorf("String() = %q, want %q", got, c.want)
		}
	}
}
