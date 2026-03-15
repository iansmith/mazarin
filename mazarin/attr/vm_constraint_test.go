package attr

import (
	"testing"

	"mazzy/mazarin/vm"
	"mazzy/mazarin/vm/compile"
)

// mustCompile compiles a restricted Go source and returns the program.
func mustCompile(t *testing.T, src string) *vm.Program {
	t.Helper()
	result, err := compile.Compile(src)
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}
	return result.Program
}

// --- Basic arithmetic (VM version of TestConstraintBasic) ---

func TestVMConstraintBasic(t *testing.T) {
	foo := NewValue[int64](3)
	bar := NewValue[int64](7)

	prog := mustCompile(t, `
func Sum(a int64, b int64) int64 {
	return a + b
}
`)
	baz := NewVMConstraintI64(prog, DepI64(foo), DepI64(bar))

	if got := baz.Get(); got != 10 {
		t.Fatalf("expected 10, got %d", got)
	}
}

// --- Constraint chain (VM version of TestConstraintChain) ---

func TestVMConstraintChain(t *testing.T) {
	foo := NewValue[int64](0)
	bar := NewValue[int64](0)

	sumProg := mustCompile(t, `
func Sum(a int64, b int64) int64 {
	return a + b
}
`)
	baz := NewVMConstraintI64(sumProg, DepI64(foo), DepI64(bar))

	grik := NewValue[int64](0)
	avgProg := mustCompile(t, `
func Avg(a int64, b int64) int64 {
	return (a + b) / 2
}
`)
	grak := NewVMConstraintI64(avgProg, DepI64(grik), DepI64(baz))

	foo.Set(12)
	bar.Set(4)
	grik.Set(9)

	if got := grak.Get(); got != 12 {
		t.Fatalf("expected 12, got %d", got)
	}
}

// --- Diamond dependency (VM version of TestDiamondDependency) ---
// Note: we can't count computes via side effects in the VM, so this
// test only verifies correctness of computed values.

func TestVMDiamondDependency(t *testing.T) {
	a := NewValue[int64](1)

	mulProg := mustCompile(t, `
func Mul2(x int64) int64 {
	return x * 2
}
`)
	addProg := mustCompile(t, `
func Add10(x int64) int64 {
	return x + 10
}
`)
	sumProg := mustCompile(t, `
func Sum(x int64, y int64) int64 {
	return x + y
}
`)

	b := NewVMConstraintI64(mulProg, DepI64(a))
	c := NewVMConstraintI64(addProg, DepI64(a))
	d := NewVMConstraintI64(sumProg, DepI64(b), DepI64(c))

	a.Set(5)
	if got := d.Get(); got != 25 {
		t.Fatalf("expected 25, got %d", got)
	}

	a.Set(7)
	if got := d.Get(); got != 31 {
		t.Fatalf("expected 31, got %d", got)
	}
}

// --- Set on VM constraint panics ---

func TestVMSetOnConstraintPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on Set() of constraint")
		}
	}()

	a := NewValue[int64](1)
	prog := mustCompile(t, `
func Identity(x int64) int64 {
	return x
}
`)
	b := NewVMConstraintI64(prog, DepI64(a))
	b.Set(42)
}

// --- Layout constraints (realistic UI use case) ---

func TestVMLayoutChildWidth(t *testing.T) {
	parentWidth := NewValue[int64](800)
	margin := NewValue[int64](20)
	padding := NewValue[int64](10)

	prog := mustCompile(t, `
func ChildWidth(parentWidth int64, margin int64, padding int64) int64 {
	return parentWidth - 2*margin - 2*padding
}
`)
	childWidth := NewVMConstraintI64(prog,
		DepI64(parentWidth), DepI64(margin), DepI64(padding))

	if got := childWidth.Get(); got != 740 {
		t.Fatalf("expected 740, got %d", got)
	}

	// Resize parent.
	parentWidth.Set(1024)
	if got := childWidth.Get(); got != 964 {
		t.Fatalf("expected 964, got %d", got)
	}
}

func TestVMLayoutCentering(t *testing.T) {
	parentWidth := NewValue[int64](800)
	childWidth := NewValue[int64](200)
	offset := NewValue[int64](0)

	prog := mustCompile(t, `
func CenterX(parentWidth int64, childWidth int64, offset int64) int64 {
	return (parentWidth - childWidth) / 2 + offset
}
`)
	x := NewVMConstraintI64(prog,
		DepI64(parentWidth), DepI64(childWidth), DepI64(offset))

	if got := x.Get(); got != 300 {
		t.Fatalf("expected 300, got %d", got)
	}

	// Add offset.
	offset.Set(50)
	if got := x.Get(); got != 350 {
		t.Fatalf("expected 350, got %d", got)
	}
}

// --- If/else conditional constraint ---

func TestVMConditionalClamp(t *testing.T) {
	value := NewValue[int64](50)
	maxVal := NewValue[int64](10)

	prog := mustCompile(t, `
func Clamp(x int64, maxVal int64) int64 {
	if x > maxVal {
		return maxVal
	}
	return x
}
`)
	clamped := NewVMConstraintI64(prog, DepI64(value), DepI64(maxVal))

	if got := clamped.Get(); got != 10 {
		t.Fatalf("expected 10, got %d", got)
	}

	value.Set(5)
	if got := clamped.Get(); got != 5 {
		t.Fatalf("expected 5, got %d", got)
	}
}

// --- Eager VM constraint ---

func TestVMEagerConstraint(t *testing.T) {
	source := NewValue[int64](0)

	prog := mustCompile(t, `
func Double(x int64) int64 {
	return x * 2
}
`)
	display := NewVMConstraintI64(prog, DepI64(source))
	display.SetEager(true)

	source.Set(100)
	// Eager constraint should have already recomputed.
	if got := display.Get(); got != 200 {
		t.Fatalf("expected 200, got %d", got)
	}

	source.Set(50)
	if got := display.Get(); got != 100 {
		t.Fatalf("expected 100, got %d", got)
	}
}

// --- Float constraint ---

func TestVMFloatConstraint(t *testing.T) {
	a := NewValue[float64](3.0)
	b := NewValue[float64](7.0)

	prog := mustCompile(t, `
func Avg(a float64, b float64) float64 {
	return (a + b) / 2.0
}
`)
	avg := NewVMConstraintF64(prog, DepF64(a), DepF64(b))

	if got := avg.Get(); got != 5.0 {
		t.Fatalf("expected 5.0, got %g", got)
	}

	a.Set(10.0)
	if got := avg.Get(); got != 8.5 {
		t.Fatalf("expected 8.5, got %g", got)
	}
}

// --- Boolean constraint ---

func TestVMBoolConstraint(t *testing.T) {
	x := NewValue[int64](5)

	prog := mustCompile(t, `
func IsPositive(x int64) bool {
	return x > 0
}
`)
	positive := NewVMConstraintBool(prog, DepI64(x))

	if !positive.Get() {
		t.Fatal("expected true for x=5")
	}

	x.Set(-3)
	if positive.Get() {
		t.Fatal("expected false for x=-3")
	}
}

// --- String constraint ---

func TestVMStringConstraint(t *testing.T) {
	first := NewValue[string]("Hello")
	second := NewValue[string]("World")

	prog := mustCompile(t, `
func Greet(a string, b string) string {
	return a + ", " + b
}
`)
	greeting := NewVMConstraintStr(prog, DepStr(first), DepStr(second))

	if got := greeting.Get(); got != "Hello, World" {
		t.Fatalf("expected \"Hello, World\", got %q", got)
	}

	second.Set("Mazzy")
	if got := greeting.Get(); got != "Hello, Mazzy" {
		t.Fatalf("expected \"Hello, Mazzy\", got %q", got)
	}
}

// --- Min/max builtin constraint ---

func TestVMBuiltinConstraint(t *testing.T) {
	a := NewValue[int64](10)
	b := NewValue[int64](3)

	prog := mustCompile(t, `
func Range(a int64, b int64) int64 {
	return max(a, b) - min(a, b)
}
`)
	spread := NewVMConstraintI64(prog, DepI64(a), DepI64(b))

	if got := spread.Get(); got != 7 {
		t.Fatalf("expected 7, got %d", got)
	}

	b.Set(15)
	if got := spread.Get(); got != 5 {
		t.Fatalf("expected 5, got %d", got)
	}
}

// --- Multi-level chain with mixed VM and value attributes ---

func TestVMMultiLevelChain(t *testing.T) {
	// width, margin → inner → half
	width := NewValue[int64](1000)
	margin := NewValue[int64](50)

	innerProg := mustCompile(t, `
func Inner(width int64, margin int64) int64 {
	return width - 2 * margin
}
`)
	inner := NewVMConstraintI64(innerProg, DepI64(width), DepI64(margin))

	halfProg := mustCompile(t, `
func Half(x int64) int64 {
	return x / 2
}
`)
	half := NewVMConstraintI64(halfProg, DepI64(inner))

	if got := half.Get(); got != 450 {
		t.Fatalf("expected 450, got %d", got)
	}

	width.Set(800)
	if got := half.Get(); got != 350 {
		t.Fatalf("expected 350, got %d", got)
	}
}
