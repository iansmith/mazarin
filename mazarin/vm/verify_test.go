package vm

import (
	"strings"
	"testing"
)

func mustVerify(t *testing.T, prog *Program) {
	t.Helper()
	if err := Verify(prog); err != nil {
		t.Fatalf("expected valid program, got: %v", err)
	}
}

func mustReject(t *testing.T, prog *Program, substr string) {
	t.Helper()
	err := Verify(prog)
	if err == nil {
		t.Fatal("expected verification error, got nil")
	}
	if !strings.Contains(err.Error(), substr) {
		t.Fatalf("expected error containing %q, got: %v", substr, err)
	}
}

// --- Valid programs ---

func TestVerifySimpleArithmetic(t *testing.T) {
	mustVerify(t, &Program{
		Code: []Inst{
			InstConstI64(3),
			InstConstI64(7),
			InstArith(OpAdd, TypeI64),
			InstRet(1),
		},
	})
}

func TestVerifyIfElse(t *testing.T) {
	mustVerify(t, &Program{
		Code: []Inst{
			InstConstBool(true),
			InstIf(),
			InstConstI64(42),
			InstElse(),
			InstConstI64(99),
			InstEndIf(),
			InstRet(1),
		},
	})
}

func TestVerifyIfWithoutElse(t *testing.T) {
	// IF without ELSE: body must not change stack depth.
	mustVerify(t, &Program{
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
	})
}

func TestVerifyForRange(t *testing.T) {
	mustVerify(t, &Program{
		Code: []Inst{
			InstConstI64(1),
			InstConstI64(2),
			InstConstI64(3),
			InstMakeColl(TypeCollI64, 3),
			InstConstI64(0),
			InstStore(0),
			InstForRange(1, 2),
			InstLoad(0),
			InstLoad(2),
			InstArith(OpAdd, TypeI64),
			InstStore(0),
			InstEndFor(),
			InstLoad(0),
			InstRet(1),
		},
	})
}

func TestVerifyForRangeBreak(t *testing.T) {
	mustVerify(t, &Program{
		Code: []Inst{
			InstConstI64(1),
			InstConstI64(2),
			InstMakeColl(TypeCollI64, 2),
			InstConstI64(-1),
			InstStore(0),
			InstForRange(1, 2),
			InstLoad(2),
			InstConstI64(1),
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
	})
}

func TestVerifyRetFromIfBranch(t *testing.T) {
	// Both branches return — valid.
	mustVerify(t, &Program{
		Code: []Inst{
			InstConstBool(true),
			InstIf(),
			InstConstI64(1),
			InstRet(1),
			InstElse(),
			InstConstI64(2),
			InstRet(1),
			InstEndIf(),
		},
	})
}

func TestVerifyRetFromThenOnly(t *testing.T) {
	// Only then-branch returns, code after END_IF is reachable.
	mustVerify(t, &Program{
		Code: []Inst{
			InstConstI64(0),
			InstStore(0),
			InstConstBool(true),
			InstIf(),
			InstConstI64(99),
			InstRet(1),
			InstEndIf(),
			InstLoad(0),
			InstRet(1),
		},
	})
}

func TestVerifyBuiltins(t *testing.T) {
	mustVerify(t, &Program{
		Strings: []string{"hello", " world"},
		Code: []Inst{
			// min(10, 20)
			InstConstI64(10),
			InstConstI64(20),
			InstCallBuiltin(BuiltinMin, 2),
			InstStore(0),
			// str_concat("hello", " world")
			{Opcode: OpConstStr, Op1: 0},
			{Opcode: OpConstStr, Op1: 1},
			InstCallBuiltin(BuiltinStrConcat, 2),
			InstStore(1),
			// Return min result
			InstLoad(0),
			InstRet(1),
		},
	})
}

func TestVerifyNestedIf(t *testing.T) {
	mustVerify(t, &Program{
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
	})
}

func TestVerifyPagination(t *testing.T) {
	// The realistic pagination test from builtin_test.go
	mustVerify(t, &Program{
		Strings: []string{"Alice", "Bob", "Charlie", "Diana", "Eve",
			"Frank", "Grace", "Heidi", "Ivan", "Judy",
			"Karl", "Laura"},
		Code: []Inst{
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
			InstStore(0),
			InstConstI64(10),
			InstStore(1),
			InstLoad(0),
			InstCallBuiltin(BuiltinCollLen, 1),
			InstLoad(1),
			InstCmp(OpLe, TypeI64),
			InstIf(),
			InstLoad(0),
			{Opcode: OpCallBuiltin, Op1: BuiltinCollEmpty, Op2: 0, Typ: TypeCollStr},
			InstRet(2),
			InstElse(),
			InstLoad(0),
			InstLoad(1),
			InstCallBuiltin(BuiltinCollTake, 2),
			InstLoad(0),
			InstLoad(1),
			InstCallBuiltin(BuiltinCollDrop, 2),
			InstRet(2),
			InstEndIf(),
		},
	})
}

// --- Invalid programs ---

func TestVerifyEmptyProgram(t *testing.T) {
	mustReject(t, &Program{Code: []Inst{}}, "empty program")
}

func TestVerifyNoRet(t *testing.T) {
	mustReject(t, &Program{
		Code: []Inst{
			InstConstI64(42),
		},
	}, "does not end with RET")
}

func TestVerifyStackUnderflow(t *testing.T) {
	mustReject(t, &Program{
		Code: []Inst{
			InstArith(OpAdd, TypeI64), // nothing on stack
			InstRet(1),
		},
	}, "stack underflow")
}

func TestVerifyUninitializedLocal(t *testing.T) {
	mustReject(t, &Program{
		Code: []Inst{
			InstLoad(5), // never stored to
			InstRet(1),
		},
	}, "uninitialized local 5")
}

func TestVerifyLocalOutOfRange(t *testing.T) {
	mustReject(t, &Program{
		Code: []Inst{
			InstConstI64(1),
			InstStore(MaxLocals), // out of range
			InstRet(0),
		},
	}, "out of range")
}

func TestVerifyUnmatchedEndIf(t *testing.T) {
	mustReject(t, &Program{
		Code: []Inst{
			InstEndIf(),
			InstRet(0),
		},
	}, "END_IF without matching IF")
}

func TestVerifyUnmatchedElse(t *testing.T) {
	mustReject(t, &Program{
		Code: []Inst{
			InstElse(),
			InstRet(0),
		},
	}, "ELSE without matching IF")
}

func TestVerifyUnterminatedIf(t *testing.T) {
	mustReject(t, &Program{
		Code: []Inst{
			InstConstBool(true),
			InstIf(),
			InstConstI64(1),
			InstRet(1),
		},
	}, "unterminated IF")
}

func TestVerifyBreakOutsideFor(t *testing.T) {
	mustReject(t, &Program{
		Code: []Inst{
			InstBreak(),
			InstRet(0),
		},
	}, "BREAK outside FOR_RANGE")
}

func TestVerifyUnmatchedEndFor(t *testing.T) {
	mustReject(t, &Program{
		Code: []Inst{
			InstEndFor(),
			InstRet(0),
		},
	}, "END_FOR without matching FOR_RANGE")
}

func TestVerifyIfStackMismatch(t *testing.T) {
	// Then-branch pushes 1 value, else-branch pushes 2.
	mustReject(t, &Program{
		Code: []Inst{
			InstConstBool(true),
			InstIf(),
			InstConstI64(1),
			InstElse(),
			InstConstI64(1),
			InstConstI64(2),
			InstEndIf(),
			InstRet(1),
		},
	}, "stack mismatch at END_IF")
}

func TestVerifyIfWithoutElseChangesStack(t *testing.T) {
	// IF without ELSE where body pushes a value.
	mustReject(t, &Program{
		Code: []Inst{
			InstConstBool(true),
			InstIf(),
			InstConstI64(42),
			InstEndIf(),
			InstRet(1),
		},
	}, "must not change stack depth")
}

func TestVerifyForRangeOnNonCollection(t *testing.T) {
	mustReject(t, &Program{
		Code: []Inst{
			InstConstI64(42),
			InstForRange(0, 1),
			InstEndFor(),
			InstRet(0),
		},
	}, "FOR_RANGE requires collection")
}

func TestVerifyForRangeChangesStack(t *testing.T) {
	// Body pushes without popping.
	mustReject(t, &Program{
		Code: []Inst{
			InstConstI64(1),
			InstMakeColl(TypeCollI64, 1),
			InstForRange(0, 1),
			InstConstI64(99), // pushes but doesn't pop
			InstEndFor(),
			InstRet(0),
		},
	}, "must not change stack depth")
}

func TestVerifyRetTooMany(t *testing.T) {
	mustReject(t, &Program{
		Code: []Inst{
			InstConstI64(1),
			InstRet(5), // only 1 on stack
		},
	}, "RET 5 but only 1")
}

func TestVerifyBadArithType(t *testing.T) {
	mustReject(t, &Program{
		Code: []Inst{
			InstConstI64(1),
			InstConstI64(2),
			InstArith(OpAdd, TypeStr), // string arithmetic not supported
			InstRet(1),
		},
	}, "I64 or F64")
}

func TestVerifyStringIndexOutOfRange(t *testing.T) {
	mustReject(t, &Program{
		Strings: []string{"hello"},
		Code: []Inst{
			{Opcode: OpConstStr, Op1: 5}, // only 1 string
			InstRet(1),
		},
	}, "string index 5 out of range")
}

func TestVerifyBadTriboolConst(t *testing.T) {
	mustReject(t, &Program{
		Code: []Inst{
			{Opcode: OpConstTri, Imm: 7},
			InstRet(1),
		},
	}, "invalid tribool constant")
}

func TestVerifyDuplicateElse(t *testing.T) {
	mustReject(t, &Program{
		Code: []Inst{
			InstConstBool(true),
			InstIf(),
			InstConstI64(1),
			InstElse(),
			InstConstI64(2),
			InstElse(), // duplicate
			InstConstI64(3),
			InstEndIf(),
			InstRet(1),
		},
	}, "duplicate ELSE")
}

func TestVerifyEndIfMismatchesForRange(t *testing.T) {
	mustReject(t, &Program{
		Code: []Inst{
			InstConstI64(1),
			InstMakeColl(TypeCollI64, 1),
			InstForRange(0, 1),
			InstEndIf(), // should be END_FOR
			InstRet(0),
		},
	}, "END_IF does not match IF")
}

func TestVerifyEndForMismatchesIf(t *testing.T) {
	mustReject(t, &Program{
		Code: []Inst{
			InstConstBool(true),
			InstIf(),
			InstEndFor(), // should be END_IF
			InstRet(0),
		},
	}, "END_FOR does not match FOR_RANGE")
}

func TestVerifyUnknownOpcode(t *testing.T) {
	mustReject(t, &Program{
		Code: []Inst{
			{Opcode: 0xFF},
			InstRet(0),
		},
	}, "unknown opcode")
}

func TestVerifyUnknownBuiltin(t *testing.T) {
	mustReject(t, &Program{
		Code: []Inst{
			InstConstI64(1),
			InstCallBuiltin(9999, 1),
			InstRet(1),
		},
	}, "unknown builtin")
}

func TestVerifyMakeCollBadType(t *testing.T) {
	mustReject(t, &Program{
		Code: []Inst{
			InstConstI64(1),
			InstMakeColl(TypeI64, 1), // TypeI64 is not a collection type
			InstRet(1),
		},
	}, "MAKE_COLL requires collection type")
}

// --- Verify that all existing runtime tests also pass verification ---

func TestVerifyAllRuntimePrograms(t *testing.T) {
	// A selection of programs from vm_test.go and builtin_test.go
	// to ensure the verifier accepts them.
	programs := []*Program{
		// Layout constraint
		{
			NumArgs: 3,
			Code: []Inst{
				InstStore(2), InstStore(1), InstStore(0),
				InstLoad(0),
				InstConstI64(2), InstLoad(1), InstArith(OpMul, TypeI64),
				InstArith(OpSub, TypeI64),
				InstConstI64(2), InstLoad(2), InstArith(OpMul, TypeI64),
				InstArith(OpSub, TypeI64),
				InstRet(1),
			},
		},
		// Centering
		{
			NumArgs: 3,
			Code: []Inst{
				InstStore(2), InstStore(1), InstStore(0),
				InstLoad(0), InstLoad(1), InstArith(OpSub, TypeI64),
				InstConstI64(2), InstArith(OpDiv, TypeI64),
				InstLoad(2), InstArith(OpAdd, TypeI64),
				InstRet(1),
			},
		},
		// Conditional clamp
		{
			NumArgs: 1,
			Code: []Inst{
				InstStore(0),
				InstLoad(0), InstConstI64(10), InstCmp(OpLt, TypeI64),
				InstIf(),
				InstLoad(0),
				InstElse(),
				InstConstI64(10),
				InstEndIf(),
				InstRet(1),
			},
		},
	}

	// For programs with args, pre-initialize the locals used for args.
	for i, prog := range programs {
		v := &verifier{
			code:    prog.Code,
			strings: prog.Strings,
		}
		// Simulate arguments being pushed and stored.
		// Programs with args start by storing args to locals,
		// but the verifier needs to see values on the stack first.
		for j := 0; j < int(prog.NumArgs); j++ {
			v.tstack = append(v.tstack, TypeI64)
		}
		if err := v.verify(); err != nil {
			t.Fatalf("program %d failed verification: %v", i, err)
		}
	}
}

// --- Multi-function verification ---

func TestVerifyMultiFuncValid(t *testing.T) {
	// double(x) = x * 2; main(a) = double(a) + 1
	mustVerify(t, &Program{
		Code: []Inst{
			// double: pc=0
			InstStore(0),
			InstLoad(0),
			InstConstI64(2),
			InstArith(OpMul, TypeI64),
			InstRet(1),
			// main: pc=5
			InstStore(1),
			InstLoad(1),
			InstCall(0, 1),
			InstConstI64(1),
			InstArith(OpAdd, TypeI64),
			InstRet(1),
		},
		Funcs: []FuncInfo{
			{Name: "double", PC: 0, NumArgs: 1, NumLocals: 1, LocalBase: 0},
			{Name: "main", PC: 5, NumArgs: 1, NumLocals: 1, LocalBase: 1},
		},
		Entry:    1,
		NumArgs:  1,
		ArgTypes: []uint8{TypeI64},
	})
}

func TestVerifyCallCycleDirectRecursion(t *testing.T) {
	mustReject(t, &Program{
		Code: []Inst{
			// f: calls itself
			InstStore(0),
			InstLoad(0),
			InstCall(0, 1),
			InstRet(1),
		},
		Funcs: []FuncInfo{
			{Name: "f", PC: 0, NumArgs: 1, NumLocals: 1, LocalBase: 0},
		},
		Entry:    0,
		NumArgs:  1,
		ArgTypes: []uint8{TypeI64},
	}, "recursion")
}

func TestVerifyCallCycleMutualRecursion(t *testing.T) {
	mustReject(t, &Program{
		Code: []Inst{
			// f0: calls f1
			InstStore(0),
			InstLoad(0),
			InstCall(1, 1),
			InstRet(1),
			// f1: calls f0
			InstStore(1),
			InstLoad(1),
			InstCall(0, 1),
			InstRet(1),
		},
		Funcs: []FuncInfo{
			{Name: "f0", PC: 0, NumArgs: 1, NumLocals: 1, LocalBase: 0},
			{Name: "f1", PC: 4, NumArgs: 1, NumLocals: 1, LocalBase: 1},
		},
		Entry:    0,
		NumArgs:  1,
		ArgTypes: []uint8{TypeI64},
	}, "recursion")
}

func TestVerifyCallBadFuncIndex(t *testing.T) {
	mustReject(t, &Program{
		Code: []Inst{
			InstConstI64(1),
			InstCall(5, 1), // index 5 doesn't exist
			InstRet(1),
		},
		Funcs: []FuncInfo{
			{Name: "main", PC: 0, NumArgs: 0, NumLocals: 0, LocalBase: 0},
		},
		Entry: 0,
	}, "invalid function index")
}
