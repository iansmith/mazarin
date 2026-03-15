package vm

import (
	"testing"
)

// --- Tribool ---

func TestTriboolAnd(t *testing.T) {
	tests := []struct {
		a, b int64
		want int64
	}{
		{TriboolTrue, TriboolTrue, TriboolTrue},
		{TriboolTrue, TriboolFalse, TriboolFalse},
		{TriboolTrue, TriboolUnknown, TriboolUnknown},
		{TriboolFalse, TriboolUnknown, TriboolFalse},
		{TriboolUnknown, TriboolUnknown, TriboolUnknown},
	}
	for _, tt := range tests {
		prog := &Program{
			Code: []Inst{
				{Opcode: OpConstTri, Imm: uint64(tt.a)},
				{Opcode: OpConstTri, Imm: uint64(tt.b)},
				{Opcode: OpAnd3},
				InstRet(1),
			},
		}
		results, err := Run(prog)
		if err != nil {
			t.Fatal(err)
		}
		if results[0].i64 != tt.want {
			t.Fatalf("and3(%d,%d): expected %d, got %d", tt.a, tt.b, tt.want, results[0].i64)
		}
	}
}

func TestTriboolOr(t *testing.T) {
	tests := []struct {
		a, b int64
		want int64
	}{
		{TriboolFalse, TriboolFalse, TriboolFalse},
		{TriboolFalse, TriboolTrue, TriboolTrue},
		{TriboolFalse, TriboolUnknown, TriboolUnknown},
		{TriboolTrue, TriboolUnknown, TriboolTrue},
	}
	for _, tt := range tests {
		prog := &Program{
			Code: []Inst{
				{Opcode: OpConstTri, Imm: uint64(tt.a)},
				{Opcode: OpConstTri, Imm: uint64(tt.b)},
				{Opcode: OpOr3},
				InstRet(1),
			},
		}
		results, err := Run(prog)
		if err != nil {
			t.Fatal(err)
		}
		if results[0].i64 != tt.want {
			t.Fatalf("or3(%d,%d): expected %d, got %d", tt.a, tt.b, tt.want, results[0].i64)
		}
	}
}

func TestTriboolNotAndKnown(t *testing.T) {
	// not3(true) = false
	prog := &Program{
		Code: []Inst{
			{Opcode: OpConstTri, Imm: uint64(TriboolTrue)},
			{Opcode: OpNot3},
			InstRet(1),
		},
	}
	results, err := Run(prog)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].i64 != TriboolFalse {
		t.Fatalf("not3(true): expected false, got %d", results[0].i64)
	}

	// not3(unknown) = unknown
	prog2 := &Program{
		Code: []Inst{
			{Opcode: OpConstTri, Imm: uint64(TriboolUnknown)},
			{Opcode: OpNot3},
			InstRet(1),
		},
	}
	results, err = Run(prog2)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].i64 != TriboolUnknown {
		t.Fatalf("not3(unknown): expected unknown, got %d", results[0].i64)
	}

	// known(true) = true, known(unknown) = false
	prog3 := &Program{
		Code: []Inst{
			{Opcode: OpConstTri, Imm: uint64(TriboolTrue)},
			{Opcode: OpKnown},
			InstRet(1),
		},
	}
	results, err = Run(prog3)
	if err != nil {
		t.Fatal(err)
	}
	if !results[0].AsBool() {
		t.Fatal("known(true) should be true")
	}

	prog4 := &Program{
		Code: []Inst{
			{Opcode: OpConstTri, Imm: uint64(TriboolUnknown)},
			{Opcode: OpKnown},
			InstRet(1),
		},
	}
	results, err = Run(prog4)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].AsBool() {
		t.Fatal("known(unknown) should be false")
	}
}

// --- Builtin: min, max, clamp ---

func TestMinMax(t *testing.T) {
	prog := &Program{
		Code: []Inst{
			InstConstI64(10),
			InstConstI64(3),
			InstCallBuiltin(BuiltinMin, 2),
			InstRet(1),
		},
	}
	results, err := Run(prog)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].AsI64() != 3 {
		t.Fatalf("min(10,3): expected 3, got %d", results[0].AsI64())
	}

	prog2 := &Program{
		Code: []Inst{
			InstConstI64(10),
			InstConstI64(3),
			InstCallBuiltin(BuiltinMax, 2),
			InstRet(1),
		},
	}
	results, err = Run(prog2)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].AsI64() != 10 {
		t.Fatalf("max(10,3): expected 10, got %d", results[0].AsI64())
	}
}

func TestClamp(t *testing.T) {
	// clamp(15, 0, 10) = 10
	prog := &Program{
		Code: []Inst{
			InstConstI64(15),
			InstConstI64(0),
			InstConstI64(10),
			InstCallBuiltin(BuiltinClamp, 3),
			InstRet(1),
		},
	}
	results, err := Run(prog)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].AsI64() != 10 {
		t.Fatalf("clamp(15,0,10): expected 10, got %d", results[0].AsI64())
	}

	// clamp(-5, 0, 10) = 0
	prog2 := &Program{
		Code: []Inst{
			InstConstI64(-5),
			InstConstI64(0),
			InstConstI64(10),
			InstCallBuiltin(BuiltinClamp, 3),
			InstRet(1),
		},
	}
	results, err = Run(prog2)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].AsI64() != 0 {
		t.Fatalf("clamp(-5,0,10): expected 0, got %d", results[0].AsI64())
	}
}

// --- String builtins ---

func TestStrLen(t *testing.T) {
	prog := &Program{
		Strings: []string{"hello"},
		Code: []Inst{
			{Opcode: OpConstStr, Op1: 0},
			InstCallBuiltin(BuiltinStrLen, 1),
			InstRet(1),
		},
	}
	results, err := Run(prog)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].AsI64() != 5 {
		t.Fatalf("str_len(\"hello\"): expected 5, got %d", results[0].AsI64())
	}
}

func TestStrConcat(t *testing.T) {
	prog := &Program{
		Strings: []string{"hello", " world"},
		Code: []Inst{
			{Opcode: OpConstStr, Op1: 0},
			{Opcode: OpConstStr, Op1: 1},
			InstCallBuiltin(BuiltinStrConcat, 2),
			InstRet(1),
		},
	}
	results, err := Run(prog)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].AsStr() != "hello world" {
		t.Fatalf("expected \"hello world\", got %q", results[0].AsStr())
	}
}

func TestStrContains(t *testing.T) {
	prog := &Program{
		Strings: []string{"hello world", "world"},
		Code: []Inst{
			{Opcode: OpConstStr, Op1: 0},
			{Opcode: OpConstStr, Op1: 1},
			InstCallBuiltin(BuiltinStrContains, 2),
			InstRet(1),
		},
	}
	results, err := Run(prog)
	if err != nil {
		t.Fatal(err)
	}
	if !results[0].AsBool() {
		t.Fatal("expected true for contains")
	}
}

func TestStrSubstr(t *testing.T) {
	prog := &Program{
		Strings: []string{"hello world"},
		Code: []Inst{
			{Opcode: OpConstStr, Op1: 0},
			InstConstI64(6),
			InstConstI64(5),
			InstCallBuiltin(BuiltinStrSubstr, 3),
			InstRet(1),
		},
	}
	results, err := Run(prog)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].AsStr() != "world" {
		t.Fatalf("expected \"world\", got %q", results[0].AsStr())
	}
}

func TestStrPrefixSuffix(t *testing.T) {
	prog := &Program{
		Strings: []string{"hello world", "hello"},
		Code: []Inst{
			{Opcode: OpConstStr, Op1: 0},
			{Opcode: OpConstStr, Op1: 1},
			InstCallBuiltin(BuiltinStrPrefix, 2),
			InstRet(1),
		},
	}
	results, err := Run(prog)
	if err != nil {
		t.Fatal(err)
	}
	if !results[0].AsBool() {
		t.Fatal("expected true for prefix")
	}

	prog2 := &Program{
		Strings: []string{"hello world", "world"},
		Code: []Inst{
			{Opcode: OpConstStr, Op1: 0},
			{Opcode: OpConstStr, Op1: 1},
			InstCallBuiltin(BuiltinStrSuffix, 2),
			InstRet(1),
		},
	}
	results, err = Run(prog2)
	if err != nil {
		t.Fatal(err)
	}
	if !results[0].AsBool() {
		t.Fatal("expected true for suffix")
	}
}

func TestStrUpperLower(t *testing.T) {
	prog := &Program{
		Strings: []string{"Hello"},
		Code: []Inst{
			{Opcode: OpConstStr, Op1: 0},
			InstCallBuiltin(BuiltinStrUpper, 1),
			InstRet(1),
		},
	}
	results, err := Run(prog)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].AsStr() != "HELLO" {
		t.Fatalf("expected \"HELLO\", got %q", results[0].AsStr())
	}

	prog2 := &Program{
		Strings: []string{"Hello"},
		Code: []Inst{
			{Opcode: OpConstStr, Op1: 0},
			InstCallBuiltin(BuiltinStrLower, 1),
			InstRet(1),
		},
	}
	results, err = Run(prog2)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].AsStr() != "hello" {
		t.Fatalf("expected \"hello\", got %q", results[0].AsStr())
	}
}

// --- Collection builtins ---

func TestCollLen(t *testing.T) {
	prog := &Program{
		Code: []Inst{
			InstConstI64(10),
			InstConstI64(20),
			InstConstI64(30),
			InstMakeColl(TypeCollI64, 3),
			InstCallBuiltin(BuiltinCollLen, 1),
			InstRet(1),
		},
	}
	results, err := Run(prog)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].AsI64() != 3 {
		t.Fatalf("expected 3, got %d", results[0].AsI64())
	}
}

func TestCollGet(t *testing.T) {
	prog := &Program{
		Strings: []string{"alpha", "beta", "gamma"},
		Code: []Inst{
			{Opcode: OpConstStr, Op1: 0},
			{Opcode: OpConstStr, Op1: 1},
			{Opcode: OpConstStr, Op1: 2},
			InstMakeColl(TypeCollStr, 3),
			InstConstI64(1),
			InstCallBuiltin(BuiltinCollGet, 2),
			InstRet(1),
		},
	}
	results, err := Run(prog)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].AsStr() != "beta" {
		t.Fatalf("expected \"beta\", got %q", results[0].AsStr())
	}
}

func TestCollGetOutOfBounds(t *testing.T) {
	prog := &Program{
		Code: []Inst{
			InstConstI64(1),
			InstMakeColl(TypeCollI64, 1),
			InstConstI64(5),
			InstCallBuiltin(BuiltinCollGet, 2),
			InstRet(1),
		},
	}
	_, err := Run(prog)
	if err == nil {
		t.Fatal("expected error for out-of-bounds get")
	}
}

func TestCollTakeDrop(t *testing.T) {
	// take(["a","b","c","d","e"], 3) = ["a","b","c"]
	prog := &Program{
		Strings: []string{"a", "b", "c", "d", "e"},
		Code: []Inst{
			{Opcode: OpConstStr, Op1: 0},
			{Opcode: OpConstStr, Op1: 1},
			{Opcode: OpConstStr, Op1: 2},
			{Opcode: OpConstStr, Op1: 3},
			{Opcode: OpConstStr, Op1: 4},
			InstMakeColl(TypeCollStr, 5),
			InstConstI64(3),
			InstCallBuiltin(BuiltinCollTake, 2),
			InstCallBuiltin(BuiltinCollLen, 1),
			InstRet(1),
		},
	}
	results, err := Run(prog)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].AsI64() != 3 {
		t.Fatalf("take: expected len 3, got %d", results[0].AsI64())
	}

	// drop(["a","b","c","d","e"], 2) = ["c","d","e"]
	prog2 := &Program{
		Strings: []string{"a", "b", "c", "d", "e"},
		Code: []Inst{
			{Opcode: OpConstStr, Op1: 0},
			{Opcode: OpConstStr, Op1: 1},
			{Opcode: OpConstStr, Op1: 2},
			{Opcode: OpConstStr, Op1: 3},
			{Opcode: OpConstStr, Op1: 4},
			InstMakeColl(TypeCollStr, 5),
			InstConstI64(2),
			InstCallBuiltin(BuiltinCollDrop, 2),
			InstStore(0),
			// Check first element is "c"
			InstLoad(0),
			InstConstI64(0),
			InstCallBuiltin(BuiltinCollGet, 2),
			InstRet(1),
		},
	}
	results, err = Run(prog2)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].AsStr() != "c" {
		t.Fatalf("drop: expected first element \"c\", got %q", results[0].AsStr())
	}
}

func TestCollSort(t *testing.T) {
	prog := &Program{
		Code: []Inst{
			InstConstI64(30),
			InstConstI64(10),
			InstConstI64(20),
			InstMakeColl(TypeCollI64, 3),
			InstCallBuiltin(BuiltinCollSort, 1),
			InstStore(0),
			// sorted[0] should be 10
			InstLoad(0),
			InstConstI64(0),
			InstCallBuiltin(BuiltinCollGet, 2),
			InstRet(1),
		},
	}
	results, err := Run(prog)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].AsI64() != 10 {
		t.Fatalf("sort: expected first element 10, got %d", results[0].AsI64())
	}
}

func TestCollConcat(t *testing.T) {
	prog := &Program{
		Code: []Inst{
			InstConstI64(1),
			InstConstI64(2),
			InstMakeColl(TypeCollI64, 2),
			InstConstI64(3),
			InstConstI64(4),
			InstMakeColl(TypeCollI64, 2),
			InstCallBuiltin(BuiltinCollConcat, 2),
			InstCallBuiltin(BuiltinCollLen, 1),
			InstRet(1),
		},
	}
	results, err := Run(prog)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].AsI64() != 4 {
		t.Fatalf("concat: expected len 4, got %d", results[0].AsI64())
	}
}

func TestCollPage(t *testing.T) {
	// page([1,2,3,4,5,6,7,8,9,10,11,12], pageNum=1, pageSize=5) = [6,7,8,9,10]
	code := make([]Inst, 0, 20)
	for i := int64(1); i <= 12; i++ {
		code = append(code, InstConstI64(i))
	}
	code = append(code,
		InstMakeColl(TypeCollI64, 12),
		InstConstI64(1),           // pageNum
		InstConstI64(5),           // pageSize
		InstCallBuiltin(BuiltinCollPage, 3),
		InstStore(0),
		// Check length = 5
		InstLoad(0),
		InstCallBuiltin(BuiltinCollLen, 1),
		InstStore(1),
		// Check first element = 6
		InstLoad(0),
		InstConstI64(0),
		InstCallBuiltin(BuiltinCollGet, 2),
		InstStore(2),
		// Return (len, first)
		InstLoad(1),
		InstLoad(2),
		InstRet(2),
	)

	prog := &Program{Code: code}
	results, err := Run(prog)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].AsI64() != 5 {
		t.Fatalf("page: expected len 5, got %d", results[0].AsI64())
	}
	if results[1].AsI64() != 6 {
		t.Fatalf("page: expected first element 6, got %d", results[1].AsI64())
	}
}

func TestCollEmpty(t *testing.T) {
	prog := &Program{
		Code: []Inst{
			{Opcode: OpCallBuiltin, Op1: BuiltinCollEmpty, Op2: 0, Typ: TypeCollStr},
			InstCallBuiltin(BuiltinCollLen, 1),
			InstRet(1),
		},
	}
	results, err := Run(prog)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].AsI64() != 0 {
		t.Fatalf("empty: expected len 0, got %d", results[0].AsI64())
	}
}

// --- FOR_RANGE ---

func TestForRangeSum(t *testing.T) {
	// sum([10, 20, 30]) = 60
	prog := &Program{
		Code: []Inst{
			// Build collection
			InstConstI64(10),
			InstConstI64(20),
			InstConstI64(30),
			InstMakeColl(TypeCollI64, 3),
			// accumulator = 0
			InstConstI64(0),
			InstStore(0), // local 0 = accumulator
			// for i, v := range coll { acc += v }
			InstForRange(1, 2), // i→local1, v→local2
			InstLoad(0),
			InstLoad(2),
			InstArith(OpAdd, TypeI64),
			InstStore(0),
			InstEndFor(),
			// return accumulator
			InstLoad(0),
			InstRet(1),
		},
	}
	results, err := Run(prog)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].AsI64() != 60 {
		t.Fatalf("expected 60, got %d", results[0].AsI64())
	}
}

func TestForRangeBreak(t *testing.T) {
	// Find first element > 5 in [1, 3, 7, 9, 2]
	prog := &Program{
		Code: []Inst{
			InstConstI64(1),
			InstConstI64(3),
			InstConstI64(7),
			InstConstI64(9),
			InstConstI64(2),
			InstMakeColl(TypeCollI64, 5),
			InstConstI64(-1),
			InstStore(0), // result = -1
			InstForRange(1, 2),
			InstLoad(2),
			InstConstI64(5),
			InstCmp(OpGt, TypeI64),
			InstIf(),
			InstLoad(2),
			InstStore(0),
			InstBreak(),
			InstEndIf(),
			InstEndFor(),
			InstLoad(0),
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

func TestForRangeEmpty(t *testing.T) {
	// for range over empty collection — body should not execute
	prog := &Program{
		Code: []Inst{
			{Opcode: OpCallBuiltin, Op1: BuiltinCollEmpty, Op2: 0, Typ: TypeCollI64},
			InstConstI64(42),
			InstStore(0),
			InstForRange(1, 2),
			InstConstI64(0),
			InstStore(0), // would set to 0 if body ran
			InstEndFor(),
			InstLoad(0),
			InstRet(1),
		},
	}
	results, err := Run(prog)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].AsI64() != 42 {
		t.Fatalf("expected 42 (body should not run), got %d", results[0].AsI64())
	}
}

func TestForRangeRetInBody(t *testing.T) {
	// Return from inside a for body
	prog := &Program{
		Code: []Inst{
			InstConstI64(10),
			InstConstI64(20),
			InstConstI64(30),
			InstMakeColl(TypeCollI64, 3),
			InstForRange(0, 1),
			InstLoad(1),
			InstConstI64(20),
			InstCmp(OpEq, TypeI64),
			InstIf(),
			InstLoad(0), // return the index
			InstRet(1),
			InstEndIf(),
			InstEndFor(),
			InstConstI64(-1),
			InstRet(1),
		},
	}
	results, err := Run(prog)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].AsI64() != 1 {
		t.Fatalf("expected index 1, got %d", results[0].AsI64())
	}
}

// --- Realistic constraint: name pagination ---

func TestNamePagination(t *testing.T) {
	// Given a collection of names:
	// if len(names) <= pageSize: return (names, empty)
	// else: return (take(names, pageSize), drop(names, pageSize))
	prog := &Program{
		Strings: []string{"Alice", "Bob", "Charlie", "Diana", "Eve",
			"Frank", "Grace", "Heidi", "Ivan", "Judy",
			"Karl", "Laura"},
		Code: []Inst{
			// Build names collection from string table
			{Opcode: OpConstStr, Op1: 0},
			{Opcode: OpConstStr, Op1: 1},
			{Opcode: OpConstStr, Op1: 2},
			{Opcode: OpConstStr, Op1: 3},
			{Opcode: OpConstStr, Op1: 4},
			{Opcode: OpConstStr, Op1: 5},
			{Opcode: OpConstStr, Op1: 6},
			{Opcode: OpConstStr, Op1: 7},
			{Opcode: OpConstStr, Op1: 8},
			{Opcode: OpConstStr, Op1: 9},
			{Opcode: OpConstStr, Op1: 10},
			{Opcode: OpConstStr, Op1: 11},
			InstMakeColl(TypeCollStr, 12),
			InstStore(0), // local 0 = names

			InstConstI64(10),
			InstStore(1), // local 1 = pageSize

			// if len(names) <= pageSize
			InstLoad(0),
			InstCallBuiltin(BuiltinCollLen, 1),
			InstLoad(1),
			InstCmp(OpLe, TypeI64),
			InstIf(),
			// return (names, empty)
			InstLoad(0),
			{Opcode: OpCallBuiltin, Op1: BuiltinCollEmpty, Op2: 0, Typ: TypeCollStr},
			InstRet(2),
			InstElse(),
			// return (take(names, pageSize), drop(names, pageSize))
			InstLoad(0),
			InstLoad(1),
			InstCallBuiltin(BuiltinCollTake, 2),
			InstLoad(0),
			InstLoad(1),
			InstCallBuiltin(BuiltinCollDrop, 2),
			InstRet(2),
			InstEndIf(),
			// unreachable but needed for well-formedness
			InstRet(0),
		},
	}
	results, err := Run(prog)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	// First page: 10 names
	if len(results[0].coll) != 10 {
		t.Fatalf("expected first page len 10, got %d", len(results[0].coll))
	}
	// Remainder: 2 names
	if len(results[1].coll) != 2 {
		t.Fatalf("expected remainder len 2, got %d", len(results[1].coll))
	}
	// Check first of remainder is "Karl"
	if results[1].coll[0].str != "Karl" {
		t.Fatalf("expected remainder[0]=\"Karl\", got %q", results[1].coll[0].str)
	}
}

// --- MakeColl ---

func TestMakeColl(t *testing.T) {
	prog := &Program{
		Strings: []string{"x", "y", "z"},
		Code: []Inst{
			{Opcode: OpConstStr, Op1: 0},
			{Opcode: OpConstStr, Op1: 1},
			{Opcode: OpConstStr, Op1: 2},
			InstMakeColl(TypeCollStr, 3),
			InstStore(0),
			// Verify element 2
			InstLoad(0),
			InstConstI64(2),
			InstCallBuiltin(BuiltinCollGet, 2),
			InstRet(1),
		},
	}
	results, err := Run(prog)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].AsStr() != "z" {
		t.Fatalf("expected \"z\", got %q", results[0].AsStr())
	}
}

// --- BoolToTri conversion ---

func TestBoolToTri(t *testing.T) {
	prog := &Program{
		Code: []Inst{
			InstConstBool(true),
			{Opcode: OpBoolToTri},
			InstRet(1),
		},
	}
	results, err := Run(prog)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].typ != TypeTribool || results[0].i64 != TriboolTrue {
		t.Fatalf("expected tribool(true), got %v", results[0])
	}
}

// --- Float builtins ---

func TestSqrtFloorCeilRound(t *testing.T) {
	// sqrt(16.0) = 4.0
	prog := &Program{
		Code: []Inst{
			InstConstF64(16.0),
			InstCallBuiltin(BuiltinSqrt, 1),
			InstRet(1),
		},
	}
	results, err := Run(prog)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].AsF64() != 4.0 {
		t.Fatalf("sqrt(16): expected 4.0, got %g", results[0].AsF64())
	}

	// floor(3.7) = 3.0
	prog2 := &Program{
		Code: []Inst{
			InstConstF64(3.7),
			InstCallBuiltin(BuiltinFloor, 1),
			InstRet(1),
		},
	}
	results, err = Run(prog2)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].AsF64() != 3.0 {
		t.Fatalf("floor(3.7): expected 3.0, got %g", results[0].AsF64())
	}

	// ceil(3.2) = 4.0
	prog3 := &Program{
		Code: []Inst{
			InstConstF64(3.2),
			InstCallBuiltin(BuiltinCeil, 1),
			InstRet(1),
		},
	}
	results, err = Run(prog3)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].AsF64() != 4.0 {
		t.Fatalf("ceil(3.2): expected 4.0, got %g", results[0].AsF64())
	}

	// round(3.5) = 4.0
	prog4 := &Program{
		Code: []Inst{
			InstConstF64(3.5),
			InstCallBuiltin(BuiltinRound, 1),
			InstRet(1),
		},
	}
	results, err = Run(prog4)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].AsF64() != 4.0 {
		t.Fatalf("round(3.5): expected 4.0, got %g", results[0].AsF64())
	}
}
