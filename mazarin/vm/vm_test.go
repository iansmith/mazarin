package vm

import (
	"fmt"
	"math"
	"strings"
	"testing"
)

// --- Basic arithmetic ---

func TestAddI64(t *testing.T) {
	// 3 + 7 = 10
	prog := &Program{
		Code: []Inst{
			InstConstI64(3),
			InstConstI64(7),
			InstArith(OpAdd, TypeI64),
			InstRet(1),
		},
	}
	results, err := Run(prog)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].AsI64() != 10 {
		t.Fatalf("expected i64(10), got %v", results)
	}
}

func TestSubMulDiv(t *testing.T) {
	// (10 - 3) * 4 / 2 = 14
	prog := &Program{
		Code: []Inst{
			InstConstI64(10),
			InstConstI64(3),
			InstArith(OpSub, TypeI64),
			InstConstI64(4),
			InstArith(OpMul, TypeI64),
			InstConstI64(2),
			InstArith(OpDiv, TypeI64),
			InstRet(1),
		},
	}
	results, err := Run(prog)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].AsI64() != 14 {
		t.Fatalf("expected 14, got %d", results[0].AsI64())
	}
}

func TestDivByZero(t *testing.T) {
	// 42 / 0 = 0 (safe, like BPF)
	prog := &Program{
		Code: []Inst{
			InstConstI64(42),
			InstConstI64(0),
			InstArith(OpDiv, TypeI64),
			InstRet(1),
		},
	}
	results, err := Run(prog)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].AsI64() != 0 {
		t.Fatalf("expected 0 for div-by-zero, got %d", results[0].AsI64())
	}
}

func TestModI64(t *testing.T) {
	// 17 % 5 = 2
	prog := &Program{
		Code: []Inst{
			InstConstI64(17),
			InstConstI64(5),
			InstArith(OpMod, TypeI64),
			InstRet(1),
		},
	}
	results, err := Run(prog)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].AsI64() != 2 {
		t.Fatalf("expected 2, got %d", results[0].AsI64())
	}
}

func TestNegAbs(t *testing.T) {
	// neg(5) = -5, abs(-5) = 5
	prog := &Program{
		Code: []Inst{
			InstConstI64(5),
			InstArith(OpNeg, TypeI64),
			InstArith(OpAbs, TypeI64),
			InstRet(1),
		},
	}
	results, err := Run(prog)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].AsI64() != 5 {
		t.Fatalf("expected 5, got %d", results[0].AsI64())
	}
}

// --- Float arithmetic ---

func TestF64Arithmetic(t *testing.T) {
	// 3.5 + 1.5 = 5.0
	prog := &Program{
		Code: []Inst{
			InstConstF64(3.5),
			InstConstF64(1.5),
			InstArith(OpAdd, TypeF64),
			InstRet(1),
		},
	}
	results, err := Run(prog)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].AsF64() != 5.0 {
		t.Fatalf("expected 5.0, got %g", results[0].AsF64())
	}
}

// --- Locals ---

func TestLocals(t *testing.T) {
	// x = 10; y = 20; return x + y
	prog := &Program{
		Code: []Inst{
			InstConstI64(10),
			InstStore(0),
			InstConstI64(20),
			InstStore(1),
			InstLoad(0),
			InstLoad(1),
			InstArith(OpAdd, TypeI64),
			InstRet(1),
		},
	}
	results, err := Run(prog)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].AsI64() != 30 {
		t.Fatalf("expected 30, got %d", results[0].AsI64())
	}
}

// --- Comparisons ---

func TestCompareI64(t *testing.T) {
	tests := []struct {
		name string
		op   uint8
		a, b int64
		want bool
	}{
		{"3 == 3", OpEq, 3, 3, true},
		{"3 == 4", OpEq, 3, 4, false},
		{"3 != 4", OpNeq, 3, 4, true},
		{"3 < 4", OpLt, 3, 4, true},
		{"4 < 3", OpLt, 4, 3, false},
		{"3 > 4", OpGt, 3, 4, false},
		{"4 > 3", OpGt, 4, 3, true},
		{"3 <= 3", OpLe, 3, 3, true},
		{"3 >= 4", OpGe, 3, 4, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prog := &Program{
				Code: []Inst{
					InstConstI64(tt.a),
					InstConstI64(tt.b),
					InstCmp(tt.op, TypeI64),
					InstRet(1),
				},
			}
			results, err := Run(prog)
			if err != nil {
				t.Fatal(err)
			}
			if results[0].AsBool() != tt.want {
				t.Fatalf("expected %v, got %v", tt.want, results[0].AsBool())
			}
		})
	}
}

// --- Boolean ---

func TestBooleanOps(t *testing.T) {
	// true && false = false
	prog := &Program{
		Code: []Inst{
			InstConstBool(true),
			InstConstBool(false),
			{Opcode: OpAnd},
			InstRet(1),
		},
	}
	results, err := Run(prog)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].AsBool() != false {
		t.Fatal("expected false")
	}

	// true || false = true
	prog2 := &Program{
		Code: []Inst{
			InstConstBool(true),
			InstConstBool(false),
			{Opcode: OpOr},
			InstRet(1),
		},
	}
	results, err = Run(prog2)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].AsBool() != true {
		t.Fatal("expected true")
	}

	// !true = false
	prog3 := &Program{
		Code: []Inst{
			InstConstBool(true),
			{Opcode: OpNot},
			InstRet(1),
		},
	}
	results, err = Run(prog3)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].AsBool() != false {
		t.Fatal("expected false")
	}
}

// --- If/Else ---

func TestIfTrue(t *testing.T) {
	// if true { 42 } else { 99 }
	prog := &Program{
		Code: []Inst{
			InstConstBool(true),
			InstIf(),
			InstConstI64(42),
			InstElse(),
			InstConstI64(99),
			InstEndIf(),
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

func TestIfFalse(t *testing.T) {
	// if false { 42 } else { 99 }
	prog := &Program{
		Code: []Inst{
			InstConstBool(false),
			InstIf(),
			InstConstI64(42),
			InstElse(),
			InstConstI64(99),
			InstEndIf(),
			InstRet(1),
		},
	}
	results, err := Run(prog)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].AsI64() != 99 {
		t.Fatalf("expected 99, got %d", results[0].AsI64())
	}
}

func TestNestedIf(t *testing.T) {
	// if true { if false { 1 } else { 2 } } else { 3 }
	// expected: 2
	prog := &Program{
		Code: []Inst{
			InstConstBool(true),
			InstIf(),
			InstConstBool(false),
			InstIf(),
			InstConstI64(1),
			InstElse(),
			InstConstI64(2),
			InstEndIf(),
			InstElse(),
			InstConstI64(3),
			InstEndIf(),
			InstRet(1),
		},
	}
	results, err := Run(prog)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].AsI64() != 2 {
		t.Fatalf("expected 2, got %d", results[0].AsI64())
	}
}

func TestIfWithoutElse(t *testing.T) {
	// x = 10; if true { x = 20 }; return x
	prog := &Program{
		Code: []Inst{
			InstConstI64(10),
			InstStore(0),
			InstConstBool(true),
			InstIf(),
			InstConstI64(20),
			InstStore(0),
			InstEndIf(),
			InstLoad(0),
			InstRet(1),
		},
	}
	results, err := Run(prog)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].AsI64() != 20 {
		t.Fatalf("expected 20, got %d", results[0].AsI64())
	}
}

// --- Arguments ---

func TestArguments(t *testing.T) {
	// compute(a, b) = a * 2 + b
	// arguments land on the stack: a at bottom, b on top.
	// Store to locals, then compute.
	prog := &Program{
		NumArgs: 2,
		Code: []Inst{
			InstStore(1), // pop b → local 1
			InstStore(0), // pop a → local 0
			InstLoad(0),
			InstConstI64(2),
			InstArith(OpMul, TypeI64),
			InstLoad(1),
			InstArith(OpAdd, TypeI64),
			InstRet(1),
		},
	}
	results, err := Run(prog, I64(5), I64(3))
	if err != nil {
		t.Fatal(err)
	}
	// 5*2 + 3 = 13
	if results[0].AsI64() != 13 {
		t.Fatalf("expected 13, got %d", results[0].AsI64())
	}
}

// --- Conversions ---

func TestI64ToF64(t *testing.T) {
	prog := &Program{
		Code: []Inst{
			InstConstI64(42),
			{Opcode: OpI64ToF64},
			InstRet(1),
		},
	}
	results, err := Run(prog)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Type() != TypeF64 || results[0].AsF64() != 42.0 {
		t.Fatalf("expected f64(42), got %v", results[0])
	}
}

func TestF64ToI64(t *testing.T) {
	prog := &Program{
		Code: []Inst{
			InstConstF64(3.7),
			{Opcode: OpF64ToI64},
			InstRet(1),
		},
	}
	results, err := Run(prog)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Type() != TypeI64 || results[0].AsI64() != 3 {
		t.Fatalf("expected i64(3), got %v", results[0])
	}
}

// --- String comparison ---

func TestStringEquality(t *testing.T) {
	prog := &Program{
		Strings: []string{"hello", "hello", "world"},
		Code: []Inst{
			{Opcode: OpConstStr, Op1: 0}, // "hello"
			{Opcode: OpConstStr, Op1: 1}, // "hello"
			InstCmp(OpEq, TypeStr),
			InstRet(1),
		},
	}
	results, err := Run(prog)
	if err != nil {
		t.Fatal(err)
	}
	if !results[0].AsBool() {
		t.Fatal("expected true for string equality")
	}

	prog2 := &Program{
		Strings: []string{"hello", "world"},
		Code: []Inst{
			{Opcode: OpConstStr, Op1: 0},
			{Opcode: OpConstStr, Op1: 1},
			InstCmp(OpEq, TypeStr),
			InstRet(1),
		},
	}
	results, err = Run(prog2)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].AsBool() {
		t.Fatal("expected false for string inequality")
	}
}

// --- Instruction limit ---

func TestInstructionLimit(t *testing.T) {
	// A program that burns fuel: store and load in a straight line, no loop.
	// We'll just make a very long program.
	code := make([]Inst, 0, MaxInstructions+10)
	code = append(code, InstConstI64(0))
	for i := 0; i < MaxInstructions+5; i++ {
		code = append(code, InstStore(0))
		code = append(code, InstLoad(0))
	}
	code = append(code, InstRet(1))

	prog := &Program{Code: code}
	_, err := Run(prog)
	if err == nil {
		t.Fatal("expected instruction limit error")
	}
	halt, ok := err.(*ErrHalt)
	if !ok {
		t.Fatalf("expected *ErrHalt, got %T", err)
	}
	if halt.Message != "instruction limit exceeded" {
		t.Fatalf("expected 'instruction limit exceeded', got %q", halt.Message)
	}
}

// --- Stack overflow ---

func TestStackOverflow(t *testing.T) {
	code := make([]Inst, 0, MaxStackDepth+10)
	for i := 0; i < MaxStackDepth+1; i++ {
		code = append(code, InstConstI64(int64(i)))
	}
	code = append(code, InstRet(1))

	prog := &Program{Code: code}
	_, err := Run(prog)
	if err == nil {
		t.Fatal("expected stack overflow error")
	}
	halt := err.(*ErrHalt)
	if halt.Message != "stack overflow" {
		t.Fatalf("expected 'stack overflow', got %q", halt.Message)
	}
}

// --- Multiple return values ---

func TestMultipleReturns(t *testing.T) {
	// return (10, 20)
	prog := &Program{
		Code: []Inst{
			InstConstI64(10),
			InstConstI64(20),
			InstRet(2),
		},
	}
	results, err := Run(prog)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].AsI64() != 10 || results[1].AsI64() != 20 {
		t.Fatalf("expected (10, 20), got (%d, %d)", results[0].AsI64(), results[1].AsI64())
	}
}

// --- Layout-style constraint ---

func TestLayoutConstraint(t *testing.T) {
	// Simulates: childWidth = parentWidth - 2*margin - 2*padding
	// parentWidth=800, margin=20, padding=10
	// expected: 800 - 40 - 20 = 740
	prog := &Program{
		NumArgs: 3,
		Code: []Inst{
			// args on stack: parentWidth(bottom), margin, padding(top)
			InstStore(2), // padding → local 2
			InstStore(1), // margin → local 1
			InstStore(0), // parentWidth → local 0

			// parentWidth - 2*margin
			InstLoad(0),
			InstConstI64(2),
			InstLoad(1),
			InstArith(OpMul, TypeI64),
			InstArith(OpSub, TypeI64),

			// - 2*padding
			InstConstI64(2),
			InstLoad(2),
			InstArith(OpMul, TypeI64),
			InstArith(OpSub, TypeI64),

			InstRet(1),
		},
	}
	results, err := Run(prog, I64(800), I64(20), I64(10))
	if err != nil {
		t.Fatal(err)
	}
	if results[0].AsI64() != 740 {
		t.Fatalf("expected 740, got %d", results[0].AsI64())
	}
}

// --- ULC-style centering ---

func TestCenteringConstraint(t *testing.T) {
	// ULC centered: x = (parentWidth - childWidth) / 2 + offset
	// parentWidth=800, childWidth=200, offset=0
	// expected: (800 - 200) / 2 + 0 = 300
	prog := &Program{
		NumArgs: 3,
		Code: []Inst{
			InstStore(2), // offset → local 2
			InstStore(1), // childWidth → local 1
			InstStore(0), // parentWidth → local 0

			InstLoad(0),
			InstLoad(1),
			InstArith(OpSub, TypeI64),
			InstConstI64(2),
			InstArith(OpDiv, TypeI64),
			InstLoad(2),
			InstArith(OpAdd, TypeI64),
			InstRet(1),
		},
	}
	results, err := Run(prog, I64(800), I64(200), I64(0))
	if err != nil {
		t.Fatal(err)
	}
	if results[0].AsI64() != 300 {
		t.Fatalf("expected 300, got %d", results[0].AsI64())
	}
}

// --- Conditional constraint ---

func TestConditionalConstraint(t *testing.T) {
	// if count < 10 { count } else { 10 }
	// (clamping to max 10)
	prog := &Program{
		NumArgs: 1,
		Code: []Inst{
			InstStore(0), // count → local 0

			InstLoad(0),
			InstConstI64(10),
			InstCmp(OpLt, TypeI64),
			InstIf(),
			InstLoad(0),
			InstElse(),
			InstConstI64(10),
			InstEndIf(),
			InstRet(1),
		},
	}

	// count=5 → 5
	results, err := Run(prog, I64(5))
	if err != nil {
		t.Fatal(err)
	}
	if results[0].AsI64() != 5 {
		t.Fatalf("expected 5, got %d", results[0].AsI64())
	}

	// count=50 → 10
	results, err = Run(prog, I64(50))
	if err != nil {
		t.Fatal(err)
	}
	if results[0].AsI64() != 10 {
		t.Fatalf("expected 10, got %d", results[0].AsI64())
	}
}

// --- Type error ---

func TestTypeMismatch(t *testing.T) {
	// Try to add int64 + float64 → should error
	prog := &Program{
		Code: []Inst{
			InstConstI64(1),
			InstConstF64(2.0),
			InstArith(OpAdd, TypeI64),
			InstRet(1),
		},
	}
	_, err := Run(prog)
	if err == nil {
		t.Fatal("expected type error")
	}
}

// --- Missing RET ---

func TestMissingRet(t *testing.T) {
	prog := &Program{
		Code: []Inst{
			InstConstI64(42),
		},
	}
	_, err := Run(prog)
	if err == nil {
		t.Fatal("expected error for missing RET")
	}
}

// --- F64 precision ---

func TestF64Precision(t *testing.T) {
	// pi * r^2 where r=10
	prog := &Program{
		Code: []Inst{
			InstConstF64(math.Pi),
			InstConstF64(10.0),
			InstConstF64(10.0),
			InstArith(OpMul, TypeF64),
			InstArith(OpMul, TypeF64),
			InstRet(1),
		},
	}
	results, err := Run(prog)
	if err != nil {
		t.Fatal(err)
	}
	expected := math.Pi * 100.0
	if math.Abs(results[0].AsF64()-expected) > 1e-10 {
		t.Fatalf("expected %g, got %g", expected, results[0].AsF64())
	}
}

// --- Multi-function call/return ---

func TestCallAndReturn(t *testing.T) {
	// func double(x) { return x * 2 }    -- func[0], pc=0, locals: slot 0
	// func main(a) { return double(a) + 1 }  -- func[1], pc=4, locals: slot 1
	prog := &Program{
		Code: []Inst{
			// double: pc=0
			InstStore(0),              // 0: store arg to slot 0
			InstLoad(0),               // 1: load x
			InstConstI64(2),           // 2: push 2
			InstArith(OpMul, TypeI64), // 3: x * 2
			InstRet(1),                // 4: return

			// main: pc=5
			InstStore(1),              // 5: store arg to slot 1
			InstLoad(1),               // 6: load a
			InstCall(0, 1),            // 7: call double(a)
			InstConstI64(1),           // 8: push 1
			InstArith(OpAdd, TypeI64), // 9: result + 1
			InstRet(1),                // 10: return
		},
		Funcs: []FuncInfo{
			{Name: "double", PC: 0, NumArgs: 1, NumLocals: 1, LocalBase: 0},
			{Name: "main", PC: 5, NumArgs: 1, NumLocals: 1, LocalBase: 1},
		},
		Entry:    1,
		NumArgs:  1,
		ArgTypes: []uint8{TypeI64},
	}
	results, err := Run(prog, I64(21))
	if err != nil {
		t.Fatal(err)
	}
	// double(21) + 1 = 42 + 1 = 43
	if results[0].AsI64() != 43 {
		t.Fatalf("expected 43, got %d", results[0].AsI64())
	}
}

func TestCallMultipleHelpers(t *testing.T) {
	// func double(x) { return x * 2 }      -- func[0], pc=0, slot 0
	// func inc(x) { return x + 1 }          -- func[1], pc=5, slot 1
	// func main(a) { return inc(double(a)) } -- func[2], pc=9, slot 2
	prog := &Program{
		Code: []Inst{
			// double: pc=0
			InstStore(0),
			InstLoad(0),
			InstConstI64(2),
			InstArith(OpMul, TypeI64),
			InstRet(1),

			// inc: pc=5
			InstStore(1),
			InstLoad(1),
			InstConstI64(1),
			InstArith(OpAdd, TypeI64),
			InstRet(1),

			// main: pc=10
			InstStore(2),
			InstLoad(2),
			InstCall(0, 1), // double(a)
			InstCall(1, 1), // inc(result)
			InstRet(1),
		},
		Funcs: []FuncInfo{
			{Name: "double", PC: 0, NumArgs: 1, NumLocals: 1, LocalBase: 0},
			{Name: "inc", PC: 5, NumArgs: 1, NumLocals: 1, LocalBase: 1},
			{Name: "main", PC: 10, NumArgs: 1, NumLocals: 1, LocalBase: 2},
		},
		Entry:    2,
		NumArgs:  1,
		ArgTypes: []uint8{TypeI64},
	}
	results, err := Run(prog, I64(10))
	if err != nil {
		t.Fatal(err)
	}
	// inc(double(10)) = inc(20) = 21
	if results[0].AsI64() != 21 {
		t.Fatalf("expected 21, got %d", results[0].AsI64())
	}
}

func TestCallHelperTwice(t *testing.T) {
	// func adjust(m) { if m < 0 { return 0 } return m * 2 }  -- func[0], slots 0
	// func main(w, m, p) { return w - adjust(m) - adjust(p) } -- func[1], slots 1-3
	prog := &Program{
		Code: []Inst{
			// adjust: pc=0
			InstStore(0),                // store m
			InstLoad(0),                 // load m
			InstConstI64(0),             // push 0
			InstCmp(OpLt, TypeI64),      // m < 0
			InstIf(),                    // if
			InstConstI64(0),             // push 0
			InstRet(1),                  // return 0
			InstEndIf(),                 // end if
			InstLoad(0),                 // load m
			InstConstI64(2),             // push 2
			InstArith(OpMul, TypeI64),   // m * 2
			InstRet(1),                  // return

			// main: pc=12
			InstStore(3),                // store padding (slot 3)
			InstStore(2),                // store margin (slot 2)
			InstStore(1),                // store width (slot 1)
			InstLoad(1),                 // load width
			InstLoad(2),                 // load margin
			InstCall(0, 1),              // adjust(margin)
			InstArith(OpSub, TypeI64),   // width - adjust(margin)
			InstLoad(3),                 // load padding
			InstCall(0, 1),              // adjust(padding)
			InstArith(OpSub, TypeI64),   // ... - adjust(padding)
			InstRet(1),
		},
		Funcs: []FuncInfo{
			{Name: "adjust", PC: 0, NumArgs: 1, NumLocals: 1, LocalBase: 0},
			{Name: "main", PC: 12, NumArgs: 3, NumLocals: 3, LocalBase: 1},
		},
		Entry:    1,
		NumArgs:  3,
		ArgTypes: []uint8{TypeI64, TypeI64, TypeI64},
	}
	results, err := Run(prog, I64(800), I64(20), I64(10))
	if err != nil {
		t.Fatal(err)
	}
	// 800 - adjust(20) - adjust(10) = 800 - 40 - 20 = 740
	if results[0].AsI64() != 740 {
		t.Fatalf("expected 740, got %d", results[0].AsI64())
	}

	// Test with negative margin.
	results, err = Run(prog, I64(800), I64(-5), I64(10))
	if err != nil {
		t.Fatal(err)
	}
	// 800 - adjust(-5) - adjust(10) = 800 - 0 - 20 = 780
	if results[0].AsI64() != 780 {
		t.Fatalf("expected 780, got %d", results[0].AsI64())
	}
}

func TestCallDepthExceeded(t *testing.T) {
	// Build a chain of 9 functions, each calling the next (exceeds MaxCallDepth=8).
	var code []Inst
	funcs := make([]FuncInfo, 10)
	pc := uint16(0)
	for i := 0; i < 9; i++ {
		funcs[i] = FuncInfo{Name: fmt.Sprintf("f%d", i), PC: pc, NumArgs: 1, NumLocals: 1, LocalBase: uint16(i)}
		code = append(code,
			InstStore(uint16(i)),       // store arg
			InstLoad(uint16(i)),        // load arg
			InstCall(uint16(i+1), 1),   // call next
			InstRet(1),
		)
		pc += 4
	}
	// Last function just returns its arg.
	funcs[9] = FuncInfo{Name: "f9", PC: pc, NumArgs: 1, NumLocals: 1, LocalBase: 9}
	code = append(code, InstStore(9), InstLoad(9), InstRet(1))

	prog := &Program{
		Code:     code,
		Funcs:    funcs,
		Entry:    0,
		NumArgs:  1,
		ArgTypes: []uint8{TypeI64},
	}
	_, err := Run(prog, I64(1))
	if err == nil {
		t.Fatal("expected call depth error")
	}
	halt, ok := err.(*ErrHalt)
	if !ok {
		t.Fatalf("expected ErrHalt, got %T: %v", err, err)
	}
	if !strings.Contains(halt.Message, "call depth") {
		t.Fatalf("expected call depth message, got: %s", halt.Message)
	}
}
