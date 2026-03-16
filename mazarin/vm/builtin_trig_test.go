package vm

import (
	"math"
	"testing"
)

func TestSinCos(t *testing.T) {
	prog := &Program{
		Code: []Inst{
			InstConstF64(0),
			InstCallBuiltin(BuiltinSin, 1),
			InstStore(0),
			InstConstF64(0),
			InstCallBuiltin(BuiltinCos, 1),
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
	if math.Abs(results[0].AsF64()) > 1e-10 {
		t.Fatalf("sin(0) expected 0, got %g", results[0].AsF64())
	}
	if math.Abs(results[1].AsF64()-1.0) > 1e-10 {
		t.Fatalf("cos(0) expected 1, got %g", results[1].AsF64())
	}
}

func TestSinPiHalf(t *testing.T) {
	prog := &Program{
		Code: []Inst{
			InstConstF64(math.Pi / 2),
			InstCallBuiltin(BuiltinSin, 1),
			InstRet(1),
		},
	}
	results, err := Run(prog)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(results[0].AsF64()-1.0) > 1e-10 {
		t.Fatalf("sin(pi/2) expected 1, got %g", results[0].AsF64())
	}
}

func TestTan(t *testing.T) {
	prog := &Program{
		Code: []Inst{
			InstConstF64(math.Pi / 4),
			InstCallBuiltin(BuiltinTan, 1),
			InstRet(1),
		},
	}
	results, err := Run(prog)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(results[0].AsF64()-1.0) > 1e-10 {
		t.Fatalf("tan(pi/4) expected 1, got %g", results[0].AsF64())
	}
}

func TestAsinAcos(t *testing.T) {
	prog := &Program{
		Code: []Inst{
			InstConstF64(1.0),
			InstCallBuiltin(BuiltinAsin, 1),
			InstStore(0),
			InstConstF64(1.0),
			InstCallBuiltin(BuiltinAcos, 1),
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
	if math.Abs(results[0].AsF64()-math.Pi/2) > 1e-10 {
		t.Fatalf("asin(1) expected pi/2, got %g", results[0].AsF64())
	}
	if math.Abs(results[1].AsF64()) > 1e-10 {
		t.Fatalf("acos(1) expected 0, got %g", results[1].AsF64())
	}
}

func TestAtan2(t *testing.T) {
	prog := &Program{
		Code: []Inst{
			InstConstF64(1.0),
			InstConstF64(1.0),
			InstCallBuiltin(BuiltinAtan2, 2),
			InstRet(1),
		},
	}
	results, err := Run(prog)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(results[0].AsF64()-math.Pi/4) > 1e-10 {
		t.Fatalf("atan2(1,1) expected pi/4, got %g", results[0].AsF64())
	}
}

func TestDegToRadToDeg(t *testing.T) {
	prog := &Program{
		Code: []Inst{
			InstConstF64(180.0),
			InstCallBuiltin(BuiltinDegToRad, 1),
			InstDup(),
			InstCallBuiltin(BuiltinRadToDeg, 1),
			InstRet(2),
		},
	}
	results, err := Run(prog)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(results[0].AsF64()-math.Pi) > 1e-10 {
		t.Fatalf("deg_to_rad(180) expected pi, got %g", results[0].AsF64())
	}
	if math.Abs(results[1].AsF64()-180.0) > 1e-10 {
		t.Fatalf("rad_to_deg(pi) expected 180, got %g", results[1].AsF64())
	}
}

func TestAbsF(t *testing.T) {
	prog := &Program{
		Code: []Inst{
			InstConstF64(-3.14),
			InstCallBuiltin(BuiltinAbsF, 1),
			InstRet(1),
		},
	}
	results, err := Run(prog)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(results[0].AsF64()-3.14) > 1e-10 {
		t.Fatalf("absf(-3.14) expected 3.14, got %g", results[0].AsF64())
	}
}

func TestPow(t *testing.T) {
	prog := &Program{
		Code: []Inst{
			InstConstF64(2.0),
			InstConstF64(10.0),
			InstCallBuiltin(BuiltinPow, 2),
			InstRet(1),
		},
	}
	results, err := Run(prog)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(results[0].AsF64()-1024.0) > 1e-10 {
		t.Fatalf("pow(2,10) expected 1024, got %g", results[0].AsF64())
	}
}
