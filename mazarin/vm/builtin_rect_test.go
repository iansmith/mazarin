package vm

import "testing"

func TestRect(t *testing.T) {
	prog := &Program{
		Code: []Inst{
			InstConstI64(10),
			InstConstI64(20),
			InstConstI64(110),
			InstConstI64(220),
			InstCallBuiltin(BuiltinRect, 4),
			InstRet(1),
		},
	}
	results, err := Run(prog)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Type() != TypeRectangle {
		t.Fatalf("expected rectangle, got %s", TypeName(results[0].Type()))
	}
	x0, y0, x1, y1 := results[0].AsRectangle()
	if x0 != 10 || y0 != 20 || x1 != 110 || y1 != 220 {
		t.Fatalf("expected (10,20,110,220), got (%d,%d,%d,%d)", x0, y0, x1, y1)
	}
}

func TestRectUnion(t *testing.T) {
	prog := &Program{
		Code: []Inst{
			InstConstI64(10), InstConstI64(20), InstConstI64(100), InstConstI64(200),
			InstCallBuiltin(BuiltinRect, 4),
			InstConstI64(50), InstConstI64(60), InstConstI64(150), InstConstI64(250),
			InstCallBuiltin(BuiltinRect, 4),
			InstCallBuiltin(BuiltinRectUnion, 2),
			InstRet(1),
		},
	}
	results, err := Run(prog)
	if err != nil {
		t.Fatal(err)
	}
	x0, y0, x1, y1 := results[0].AsRectangle()
	if x0 != 10 || y0 != 20 || x1 != 150 || y1 != 250 {
		t.Fatalf("expected (10,20,150,250), got (%d,%d,%d,%d)", x0, y0, x1, y1)
	}
}

func TestRectIntersect(t *testing.T) {
	prog := &Program{
		Code: []Inst{
			InstConstI64(10), InstConstI64(20), InstConstI64(100), InstConstI64(200),
			InstCallBuiltin(BuiltinRect, 4),
			InstConstI64(50), InstConstI64(60), InstConstI64(150), InstConstI64(250),
			InstCallBuiltin(BuiltinRect, 4),
			InstCallBuiltin(BuiltinRectIntersect, 2),
			InstRet(1),
		},
	}
	results, err := Run(prog)
	if err != nil {
		t.Fatal(err)
	}
	x0, y0, x1, y1 := results[0].AsRectangle()
	if x0 != 50 || y0 != 60 || x1 != 100 || y1 != 200 {
		t.Fatalf("expected (50,60,100,200), got (%d,%d,%d,%d)", x0, y0, x1, y1)
	}
}

func TestRectIntersectNoOverlap(t *testing.T) {
	prog := &Program{
		Code: []Inst{
			InstConstI64(0), InstConstI64(0), InstConstI64(10), InstConstI64(10),
			InstCallBuiltin(BuiltinRect, 4),
			InstConstI64(20), InstConstI64(20), InstConstI64(30), InstConstI64(30),
			InstCallBuiltin(BuiltinRect, 4),
			InstCallBuiltin(BuiltinRectIntersect, 2),
			InstCallBuiltin(BuiltinRectEmpty, 1),
			InstRet(1),
		},
	}
	results, err := Run(prog)
	if err != nil {
		t.Fatal(err)
	}
	if !results[0].AsBool() {
		t.Fatal("expected empty rect from non-overlapping intersection")
	}
}

func TestRectOverlaps(t *testing.T) {
	prog := &Program{
		Code: []Inst{
			InstConstI64(0), InstConstI64(0), InstConstI64(10), InstConstI64(10),
			InstCallBuiltin(BuiltinRect, 4),
			InstConstI64(5), InstConstI64(5), InstConstI64(15), InstConstI64(15),
			InstCallBuiltin(BuiltinRect, 4),
			InstCallBuiltin(BuiltinRectOverlaps, 2),
			InstRet(1),
		},
	}
	results, err := Run(prog)
	if err != nil {
		t.Fatal(err)
	}
	if !results[0].AsBool() {
		t.Fatal("expected overlapping rects")
	}
}

func TestRectContains(t *testing.T) {
	prog := &Program{
		Code: []Inst{
			InstConstI64(0), InstConstI64(0), InstConstI64(100), InstConstI64(100),
			InstCallBuiltin(BuiltinRect, 4),
			InstConstI64(10), InstConstI64(10), InstConstI64(50), InstConstI64(50),
			InstCallBuiltin(BuiltinRect, 4),
			InstCallBuiltin(BuiltinRectContains, 2),
			InstRet(1),
		},
	}
	results, err := Run(prog)
	if err != nil {
		t.Fatal(err)
	}
	if !results[0].AsBool() {
		t.Fatal("expected outer contains inner")
	}
}

func TestRectArea(t *testing.T) {
	prog := &Program{
		Code: []Inst{
			InstConstI64(10), InstConstI64(20), InstConstI64(110), InstConstI64(220),
			InstCallBuiltin(BuiltinRect, 4),
			InstCallBuiltin(BuiltinRectArea, 1),
			InstRet(1),
		},
	}
	results, err := Run(prog)
	if err != nil {
		t.Fatal(err)
	}
	// width=100, height=200, area=20000
	if results[0].AsI64() != 20000 {
		t.Fatalf("expected 20000, got %d", results[0].AsI64())
	}
}

func TestRectWidthHeight(t *testing.T) {
	prog := &Program{
		Code: []Inst{
			InstConstI64(5), InstConstI64(10), InstConstI64(25), InstConstI64(40),
			InstCallBuiltin(BuiltinRect, 4),
			InstDup(),
			InstCallBuiltin(BuiltinRectWidth, 1),
			InstStore(0),
			InstCallBuiltin(BuiltinRectHeight, 1),
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
	if results[0].AsI64() != 20 {
		t.Fatalf("expected width=20, got %d", results[0].AsI64())
	}
	if results[1].AsI64() != 30 {
		t.Fatalf("expected height=30, got %d", results[1].AsI64())
	}
}

func TestRectEmpty(t *testing.T) {
	prog := &Program{
		Code: []Inst{
			InstConstI64(0), InstConstI64(0), InstConstI64(0), InstConstI64(0),
			InstCallBuiltin(BuiltinRect, 4),
			InstCallBuiltin(BuiltinRectEmpty, 1),
			InstRet(1),
		},
	}
	results, err := Run(prog)
	if err != nil {
		t.Fatal(err)
	}
	if !results[0].AsBool() {
		t.Fatal("expected empty rect")
	}
}
