package compile

import (
	"strings"
	"testing"

	"mazzy/mazarin/vm"
)

func compileAndRun(t *testing.T, src string, args ...vm.Value) []vm.Value {
	t.Helper()
	result, err := Compile(src)
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}
	vals, err := vm.Run(result.Program, args...)
	if err != nil {
		t.Fatalf("runtime error: %v", err)
	}
	return vals
}

func mustCompileErr(t *testing.T, src string, substr string) {
	t.Helper()
	_, err := Compile(src)
	if err == nil {
		t.Fatal("expected compile error, got nil")
	}
	if !strings.Contains(err.Error(), substr) {
		t.Fatalf("expected error containing %q, got: %v", substr, err)
	}
}

// --- Basic arithmetic ---

func TestCompileAdd(t *testing.T) {
	results := compileAndRun(t, `
func Add(a int64, b int64) int64 {
	return a + b
}
`, vm.I64(3), vm.I64(7))
	if results[0].AsI64() != 10 {
		t.Fatalf("expected 10, got %d", results[0].AsI64())
	}
}

func TestCompileArithmetic(t *testing.T) {
	// (a - b) * c / d
	results := compileAndRun(t, `
func Compute(a int64, b int64, c int64, d int64) int64 {
	return (a - b) * c / d
}
`, vm.I64(10), vm.I64(3), vm.I64(4), vm.I64(2))
	// (10-3)*4/2 = 14
	if results[0].AsI64() != 14 {
		t.Fatalf("expected 14, got %d", results[0].AsI64())
	}
}

func TestCompileNegation(t *testing.T) {
	results := compileAndRun(t, `
func Neg(x int64) int64 {
	return -x
}
`, vm.I64(42))
	if results[0].AsI64() != -42 {
		t.Fatalf("expected -42, got %d", results[0].AsI64())
	}
}

// --- Local variables ---

func TestCompileLocals(t *testing.T) {
	results := compileAndRun(t, `
func Locals(x int64) int64 {
	y := x * 2
	z := y + 3
	return z
}
`, vm.I64(5))
	// 5*2 + 3 = 13
	if results[0].AsI64() != 13 {
		t.Fatalf("expected 13, got %d", results[0].AsI64())
	}
}

// --- If/else ---

func TestCompileIfElse(t *testing.T) {
	src := `
func Clamp(x int64, maxVal int64) int64 {
	if x > maxVal {
		return maxVal
	}
	return x
}
`
	results := compileAndRun(t, src, vm.I64(50), vm.I64(10))
	if results[0].AsI64() != 10 {
		t.Fatalf("expected 10, got %d", results[0].AsI64())
	}

	results = compileAndRun(t, src, vm.I64(5), vm.I64(10))
	if results[0].AsI64() != 5 {
		t.Fatalf("expected 5, got %d", results[0].AsI64())
	}
}

func TestCompileIfElseBranch(t *testing.T) {
	results := compileAndRun(t, `
func Choose(flag bool) int64 {
	if flag {
		return 42
	} else {
		return 99
	}
}
`, vm.Bool(true))
	if results[0].AsI64() != 42 {
		t.Fatalf("expected 42, got %d", results[0].AsI64())
	}
}

// --- Layout constraints ---

func TestCompileLayoutWidth(t *testing.T) {
	results := compileAndRun(t, `
func ChildWidth(parentWidth int64, margin int64, padding int64) int64 {
	return parentWidth - 2*margin - 2*padding
}
`, vm.I64(800), vm.I64(20), vm.I64(10))
	// 800 - 40 - 20 = 740
	if results[0].AsI64() != 740 {
		t.Fatalf("expected 740, got %d", results[0].AsI64())
	}
}

func TestCompileCentering(t *testing.T) {
	results := compileAndRun(t, `
func CenterX(parentWidth int64, childWidth int64, offset int64) int64 {
	return (parentWidth - childWidth) / 2 + offset
}
`, vm.I64(800), vm.I64(200), vm.I64(0))
	// (800-200)/2 + 0 = 300
	if results[0].AsI64() != 300 {
		t.Fatalf("expected 300, got %d", results[0].AsI64())
	}
}

// --- Builtins ---

func TestCompileMinMax(t *testing.T) {
	results := compileAndRun(t, `
func MinMax(a int64, b int64) int64 {
	lo := min(a, b)
	hi := max(a, b)
	return hi - lo
}
`, vm.I64(10), vm.I64(3))
	if results[0].AsI64() != 7 {
		t.Fatalf("expected 7, got %d", results[0].AsI64())
	}
}

func TestCompileClamp(t *testing.T) {
	results := compileAndRun(t, `
func Bounded(val int64) int64 {
	return clamp(val, 0, 100)
}
`, vm.I64(150))
	if results[0].AsI64() != 100 {
		t.Fatalf("expected 100, got %d", results[0].AsI64())
	}
}

// --- String operations ---

func TestCompileStringConcat(t *testing.T) {
	results := compileAndRun(t, `
func Greet(name string) string {
	return "Hello, " + name
}
`, vm.Str("World"))
	if results[0].AsStr() != "Hello, World" {
		t.Fatalf("expected \"Hello, World\", got %q", results[0].AsStr())
	}
}

func TestCompileStringLen(t *testing.T) {
	results := compileAndRun(t, `
func StrLen(s string) int64 {
	return int64(len(s))
}
`, vm.Str("hello"))
	if results[0].AsI64() != 5 {
		t.Fatalf("expected 5, got %d", results[0].AsI64())
	}
}

// --- Boolean logic ---

func TestCompileBooleanLogic(t *testing.T) {
	results := compileAndRun(t, `
func Logic(a bool, b bool) bool {
	return a && !b
}
`, vm.Bool(true), vm.Bool(false))
	if !results[0].AsBool() {
		t.Fatal("expected true")
	}
}

// --- Type conversions ---

func TestCompileI64ToF64(t *testing.T) {
	results := compileAndRun(t, `
func Convert(x int64) float64 {
	return float64(x) * 2.5
}
`, vm.I64(4))
	if results[0].AsF64() != 10.0 {
		t.Fatalf("expected 10.0, got %g", results[0].AsF64())
	}
}

// --- Multiple return values ---

func TestCompileMultiReturn(t *testing.T) {
	results := compileAndRun(t, `
func DivMod(a int64, b int64) (int64, int64) {
	return a / b, a % b
}
`, vm.I64(17), vm.I64(5))
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].AsI64() != 3 {
		t.Fatalf("expected quotient 3, got %d", results[0].AsI64())
	}
	if results[1].AsI64() != 2 {
		t.Fatalf("expected remainder 2, got %d", results[1].AsI64())
	}
}

// --- Comparison ---

func TestCompileComparison(t *testing.T) {
	results := compileAndRun(t, `
func IsPositive(x int64) bool {
	return x > 0
}
`, vm.I64(5))
	if !results[0].AsBool() {
		t.Fatal("expected true")
	}

	results = compileAndRun(t, `
func IsPositive(x int64) bool {
	return x > 0
}
`, vm.I64(-3))
	if results[0].AsBool() {
		t.Fatal("expected false")
	}
}

// --- Restriction violations ---

func TestRejectGoroutine(t *testing.T) {
	mustCompileErr(t, `
func Bad() int64 {
	go func() {}()
	return 0
}
`, "goroutines not allowed")
}

func TestRejectForLoop(t *testing.T) {
	mustCompileErr(t, `
func Bad() int64 {
	for i := 0; i < 10; i++ {
	}
	return 0
}
`, "for-loops not allowed")
}

func TestRejectPointer(t *testing.T) {
	mustCompileErr(t, `
func Bad(x int64) int64 {
	_ = &x
	return 0
}
`, "address-of not allowed")
}

func TestRejectClosure(t *testing.T) {
	mustCompileErr(t, `
func Bad() int64 {
	f := func() int64 { return 1 }
	return f()
}
`, "closures")
}

func TestRejectDefer(t *testing.T) {
	mustCompileErr(t, `
func Bad() int64 {
	defer func() {}()
	return 0
}
`, "defer not allowed")
}

func TestRejectUnknownFunc(t *testing.T) {
	mustCompileErr(t, `
func Bad() int64 {
	return foo(1)
}
`, "undefined")
}

func TestRejectMethod(t *testing.T) {
	mustCompileErr(t, `
func Bad(x int64) int64 {
	return x.String()
}
`, "undefined")
}

// --- For-range ---

func TestCompileForRangeSum(t *testing.T) {
	results := compileAndRun(t, `
func Sum(items []int64) int64 {
	total := int64(0)
	for _, v := range items {
		total = total + v
	}
	return total
}
`, vm.CollI64([]int64{10, 20, 30}))
	if results[0].AsI64() != 60 {
		t.Fatalf("expected 60, got %d", results[0].AsI64())
	}
}

func TestCompileForRangeBreak(t *testing.T) {
	results := compileAndRun(t, `
func FirstOver(items []int64, threshold int64) int64 {
	result := int64(-1)
	for _, v := range items {
		if v > threshold {
			result = v
			break
		}
	}
	return result
}
`, vm.CollI64([]int64{1, 5, 12, 3}), vm.I64(10))
	if results[0].AsI64() != 12 {
		t.Fatalf("expected 12, got %d", results[0].AsI64())
	}
}

// --- Else-if chains ---

func TestCompileElseIf(t *testing.T) {
	src := `
func Category(x int64) int64 {
	if x < 0 {
		return -1
	} else if x == 0 {
		return 0
	} else {
		return 1
	}
}
`
	results := compileAndRun(t, src, vm.I64(-5))
	if results[0].AsI64() != -1 {
		t.Fatalf("expected -1, got %d", results[0].AsI64())
	}
	results = compileAndRun(t, src, vm.I64(0))
	if results[0].AsI64() != 0 {
		t.Fatalf("expected 0, got %d", results[0].AsI64())
	}
	results = compileAndRun(t, src, vm.I64(42))
	if results[0].AsI64() != 1 {
		t.Fatalf("expected 1, got %d", results[0].AsI64())
	}
}

// --- Float arithmetic ---

func TestCompileFloatArith(t *testing.T) {
	results := compileAndRun(t, `
func Avg(a float64, b float64) float64 {
	return (a + b) / 2.0
}
`, vm.F64(3.0), vm.F64(7.0))
	if results[0].AsF64() != 5.0 {
		t.Fatalf("expected 5.0, got %g", results[0].AsF64())
	}
}

// --- Inc/Dec ---

func TestCompileIncDec(t *testing.T) {
	results := compileAndRun(t, `
func IncDec(x int64) int64 {
	x++
	x++
	x--
	return x
}
`, vm.I64(10))
	if results[0].AsI64() != 11 {
		t.Fatalf("expected 11, got %d", results[0].AsI64())
	}
}

// --- Modulo ---

func TestCompileModulo(t *testing.T) {
	results := compileAndRun(t, `
func Mod(a int64, b int64) int64 {
	return a % b
}
`, vm.I64(17), vm.I64(5))
	if results[0].AsI64() != 2 {
		t.Fatalf("expected 2, got %d", results[0].AsI64())
	}
}

// --- String builtins ---

func TestCompileStringPrefix(t *testing.T) {
	results := compileAndRun(t, `
func HasPrefix(s string, prefix string) bool {
	return str_prefix(s, prefix)
}
`, vm.Str("hello world"), vm.Str("hello"))
	if !results[0].AsBool() {
		t.Fatal("expected true")
	}
}

// --- No-arg function ---

func TestCompileNoArgs(t *testing.T) {
	results := compileAndRun(t, `
func FortyTwo() int64 {
	return 42
}
`)
	if results[0].AsI64() != 42 {
		t.Fatalf("expected 42, got %d", results[0].AsI64())
	}
}
