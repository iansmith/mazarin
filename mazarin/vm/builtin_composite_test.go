package vm

import "testing"

func TestTimespecRoundTrip(t *testing.T) {
	prog := &Program{
		Code: []Inst{
			InstConstI64(1710000000),
			InstConstI64(500000000),
			InstCallBuiltin(BuiltinTimespec, 2),
			InstDup(),
			InstCallBuiltin(BuiltinTimespecSeconds, 1),
			InstStore(0),
			InstCallBuiltin(BuiltinTimespecNanos, 1),
			InstStore(1),
			InstLoad(0),
			InstLoad(1),
			InstRet(2),
		},
	}
	results, err := Run(prog)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].AsI64() != 1710000000 {
		t.Fatalf("expected seconds=1710000000, got %d", results[0].AsI64())
	}
	if results[1].AsI64() != 500000000 {
		t.Fatalf("expected nanos=500000000, got %d", results[1].AsI64())
	}
}

func TestTimezoneConvert(t *testing.T) {
	// UTC midnight + UTC+5:30 (330 minutes)
	prog := &Program{
		Code: []Inst{
			InstConstI64(0),   // seconds
			InstConstI64(0),   // nanos
			InstCallBuiltin(BuiltinTimespec, 2),
			InstConstI64(330), // UTC+5:30
			InstCallBuiltin(BuiltinTimezone, 1),
			InstCallBuiltin(BuiltinTzConvert, 2),
			InstCallBuiltin(BuiltinTimespecSeconds, 1),
			InstRet(1),
		},
	}
	results, err := Run(prog)
	if err != nil {
		t.Fatal(err)
	}
	// 330 * 60 = 19800
	if results[0].AsI64() != 19800 {
		t.Fatalf("expected 19800, got %d", results[0].AsI64())
	}
}

func TestDurationRoundTrip(t *testing.T) {
	prog := &Program{
		Code: []Inst{
			InstConstI64(5000000000), // 5 seconds in nanos
			InstCallBuiltin(BuiltinDuration, 1),
			InstCallBuiltin(BuiltinDurationNanos, 1),
			InstRet(1),
		},
	}
	results, err := Run(prog)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].AsI64() != 5000000000 {
		t.Fatalf("expected 5000000000, got %d", results[0].AsI64())
	}
}

func TestDateRoundTrip(t *testing.T) {
	prog := &Program{
		Code: []Inst{
			InstConstI64(2026),
			InstConstI64(3),
			InstConstI64(16),
			InstCallBuiltin(BuiltinDate, 3),
			InstDup(),
			InstDup(),
			InstCallBuiltin(BuiltinDateYear, 1),
			InstStore(0),
			InstCallBuiltin(BuiltinDateMonth, 1),
			InstStore(1),
			InstCallBuiltin(BuiltinDateDay, 1),
			InstStore(2),
			InstLoad(0),
			InstLoad(1),
			InstLoad(2),
			InstRet(3),
		},
	}
	results, err := Run(prog)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].AsI64() != 2026 {
		t.Fatalf("expected year=2026, got %d", results[0].AsI64())
	}
	if results[1].AsI64() != 3 {
		t.Fatalf("expected month=3, got %d", results[1].AsI64())
	}
	if results[2].AsI64() != 16 {
		t.Fatalf("expected day=16, got %d", results[2].AsI64())
	}
}

func TestPoint2DRoundTrip(t *testing.T) {
	prog := &Program{
		Code: []Inst{
			InstConstI64(100),
			InstConstI64(200),
			InstCallBuiltin(BuiltinPoint2D, 2),
			InstDup(),
			InstCallBuiltin(BuiltinPoint2DX, 1),
			InstStore(0),
			InstCallBuiltin(BuiltinPoint2DY, 1),
			InstStore(1),
			InstLoad(0),
			InstLoad(1),
			InstRet(2),
		},
	}
	results, err := Run(prog)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].AsI64() != 100 {
		t.Fatalf("expected x=100, got %d", results[0].AsI64())
	}
	if results[1].AsI64() != 200 {
		t.Fatalf("expected y=200, got %d", results[1].AsI64())
	}
}

func TestPoint3D(t *testing.T) {
	prog := &Program{
		Code: []Inst{
			InstConstI64(1),
			InstConstI64(2),
			InstConstI64(3),
			InstCallBuiltin(BuiltinPoint3D, 3),
			InstRet(1),
		},
	}
	results, err := Run(prog)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Type() != TypePoint3D {
		t.Fatalf("expected point3d, got %s", TypeName(results[0].Type()))
	}
	x, y, z := results[0].AsPoint3D()
	if x != 1 || y != 2 || z != 3 {
		t.Fatalf("expected (1,2,3), got (%d,%d,%d)", x, y, z)
	}
}

func TestPointF2D(t *testing.T) {
	prog := &Program{
		Code: []Inst{
			InstConstF64(1.5),
			InstConstF64(2.5),
			InstCallBuiltin(BuiltinPointF2D, 2),
			InstRet(1),
		},
	}
	results, err := Run(prog)
	if err != nil {
		t.Fatal(err)
	}
	x, y := results[0].AsPointF2D()
	if x != 1.5 || y != 2.5 {
		t.Fatalf("expected (1.5,2.5), got (%g,%g)", x, y)
	}
}

func TestPointF3D(t *testing.T) {
	prog := &Program{
		Code: []Inst{
			InstConstF64(1.5),
			InstConstF64(2.5),
			InstConstF64(3.5),
			InstCallBuiltin(BuiltinPointF3D, 3),
			InstRet(1),
		},
	}
	results, err := Run(prog)
	if err != nil {
		t.Fatal(err)
	}
	x, y, z := results[0].AsPointF3D()
	if x != 1.5 || y != 2.5 || z != 3.5 {
		t.Fatalf("expected (1.5,2.5,3.5), got (%g,%g,%g)", x, y, z)
	}
}

func TestIPv4RoundTrip(t *testing.T) {
	prog := &Program{
		Code: []Inst{
			InstConstI64(192),
			InstConstI64(168),
			InstConstI64(1),
			InstConstI64(1),
			InstCallBuiltin(BuiltinIPv4, 4),
			InstDup(),
			InstConstI64(0),
			InstCallBuiltin(BuiltinIPv4Octet, 2),
			InstStore(0),
			InstConstI64(3),
			InstCallBuiltin(BuiltinIPv4Octet, 2),
			InstStore(1),
			InstLoad(0),
			InstLoad(1),
			InstRet(2),
		},
	}
	results, err := Run(prog)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].AsI64() != 192 {
		t.Fatalf("expected octet[0]=192, got %d", results[0].AsI64())
	}
	if results[1].AsI64() != 1 {
		t.Fatalf("expected octet[3]=1, got %d", results[1].AsI64())
	}
}

func TestPriestIdRoundTrip(t *testing.T) {
	prog := &Program{
		Code: []Inst{
			InstConstI64(42),
			InstCallBuiltin(BuiltinPriestId, 1),
			InstCallBuiltin(BuiltinPriestIdNum, 1),
			InstRet(1),
		},
	}
	results, err := Run(prog)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].AsI64() != 42 {
		t.Fatalf("expected 42, got %d", results[0].AsI64())
	}
}

func TestMazIdRoundTrip(t *testing.T) {
	prog := &Program{
		Code: []Inst{
			InstConstI64(7),
			InstCallBuiltin(BuiltinMazId, 1),
			InstCallBuiltin(BuiltinMazIdNum, 1),
			InstRet(1),
		},
	}
	results, err := Run(prog)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].AsI64() != 7 {
		t.Fatalf("expected 7, got %d", results[0].AsI64())
	}
}
