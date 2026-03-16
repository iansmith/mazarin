package vm

import (
	"testing"
)

func TestMarshalRoundTrip(t *testing.T) {
	prog := &Program{
		Code: []Inst{
			InstConstI64(42),
			InstConstI64(58),
			InstArith(OpAdd, TypeI64),
			InstRet(1),
		},
		Strings: []string{"hello", "world"},
		Funcs: []FuncInfo{
			{Name: "main", PC: 0, NumArgs: 0, NumLocals: 2, LocalBase: 0},
		},
		Entry: 0,
	}

	data := prog.Marshal()
	if len(data) == 0 {
		t.Fatal("Marshal returned empty data")
	}

	got, err := UnmarshalProgram(data)
	if err != nil {
		t.Fatalf("UnmarshalProgram failed: %v", err)
	}

	// Check code.
	if len(got.Code) != len(prog.Code) {
		t.Fatalf("code length: got %d, want %d", len(got.Code), len(prog.Code))
	}
	for i, inst := range prog.Code {
		if got.Code[i] != inst {
			t.Fatalf("code[%d]: got %+v, want %+v", i, got.Code[i], inst)
		}
	}

	// Check strings.
	if len(got.Strings) != len(prog.Strings) {
		t.Fatalf("strings length: got %d, want %d", len(got.Strings), len(prog.Strings))
	}
	for i, s := range prog.Strings {
		if got.Strings[i] != s {
			t.Fatalf("strings[%d]: got %q, want %q", i, got.Strings[i], s)
		}
	}

	// Check funcs.
	if len(got.Funcs) != len(prog.Funcs) {
		t.Fatalf("funcs length: got %d, want %d", len(got.Funcs), len(prog.Funcs))
	}
	for i, f := range prog.Funcs {
		gf := got.Funcs[i]
		if gf.Name != f.Name || gf.PC != f.PC || gf.NumArgs != f.NumArgs ||
			gf.NumLocals != f.NumLocals || gf.LocalBase != f.LocalBase {
			t.Fatalf("funcs[%d]: got %+v, want %+v", i, gf, f)
		}
	}

	// Check entry.
	if got.Entry != prog.Entry {
		t.Fatalf("entry: got %d, want %d", got.Entry, prog.Entry)
	}
}

func TestMarshalSimpleProgram(t *testing.T) {
	// Simple program with no strings or funcs.
	prog := &Program{
		Code: []Inst{
			InstConstI64(100),
			InstRet(1),
		},
	}

	data := prog.Marshal()
	got, err := UnmarshalProgram(data)
	if err != nil {
		t.Fatalf("UnmarshalProgram failed: %v", err)
	}

	// Run both programs to verify they produce the same result.
	r1, err := Run(prog)
	if err != nil {
		t.Fatalf("Run original failed: %v", err)
	}
	r2, err := Run(got)
	if err != nil {
		t.Fatalf("Run deserialized failed: %v", err)
	}

	if len(r1) != 1 || len(r2) != 1 {
		t.Fatalf("expected 1 result each, got %d and %d", len(r1), len(r2))
	}
	if r1[0].AsI64() != 100 || r2[0].AsI64() != 100 {
		t.Fatalf("expected 100, got %d and %d", r1[0].AsI64(), r2[0].AsI64())
	}
}

func TestUnmarshalInvalidMagic(t *testing.T) {
	data := []byte{0x00, 0x00, 0x00, 0x00, 0, 0, 0, 0, 0, 0, 0, 0}
	_, err := UnmarshalProgram(data)
	if err == nil {
		t.Fatal("expected error for invalid magic")
	}
}

func TestUnmarshalTooShort(t *testing.T) {
	_, err := UnmarshalProgram([]byte{1, 2, 3})
	if err == nil {
		t.Fatal("expected error for too-short data")
	}
}
