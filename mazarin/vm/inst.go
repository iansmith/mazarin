package vm

import (
	"math"
)

// Instruction encoding: 128 bits per instruction.
//
//   ┌──────────┬───────┬──────────┬──────────┬──────────┬──────────────────┐
//   │ opcode:8 │ typ:8 │ op1:16   │ op2:16   │ flags:16 │ immediate:64     │
//   └──────────┴───────┴──────────┴──────────┴──────────┴──────────────────┘
//
// opcode: the operation
// typ:    value type tag (for typed operations like ADD, CMP)
// op1:    local slot, branch target, builtin ID, function index
// op2:    secondary operand (arg count, etc.)
// flags:  reserved for verifier annotations
// imm:    inline constant (int64 or float64 bits)

// Opcodes.
const (
	// Constants — push to stack.
	OpConstI64  uint8 = 0x01 // push imm as int64
	OpConstF64  uint8 = 0x02 // push imm as float64 (bit pattern)
	OpConstBool uint8 = 0x03 // push imm as bool (0 or 1)
	OpConstTri  uint8 = 0x04 // push imm as tribool (0, 1, or 2)
	OpConstStr  uint8 = 0x05 // push string from string table at index op1

	// Locals.
	OpLoad  uint8 = 0x10 // push local[op1]
	OpStore uint8 = 0x11 // pop and store to local[op1]

	// Arithmetic — pop two (or one for NEG/ABS), push result.
	// typ determines I64 or F64.
	OpAdd uint8 = 0x20
	OpSub uint8 = 0x21
	OpMul uint8 = 0x22
	OpDiv uint8 = 0x23
	OpMod uint8 = 0x24 // I64 only
	OpNeg uint8 = 0x25 // unary
	OpAbs uint8 = 0x26 // unary

	// Comparison — pop two of type typ, push Bool.
	OpEq  uint8 = 0x30
	OpNeq uint8 = 0x31
	OpLt  uint8 = 0x32
	OpGt  uint8 = 0x33
	OpLe  uint8 = 0x34
	OpGe  uint8 = 0x35

	// Boolean.
	OpAnd uint8 = 0x40
	OpOr  uint8 = 0x41
	OpNot uint8 = 0x42 // unary

	// Tribool.
	OpAnd3  uint8 = 0x48
	OpOr3   uint8 = 0x49
	OpNot3  uint8 = 0x4A // unary
	OpKnown uint8 = 0x4B // tribool → bool (true if not unknown)

	// Conversions.
	OpI64ToF64 uint8 = 0x50
	OpF64ToI64 uint8 = 0x51
	OpBoolToTri uint8 = 0x52

	// Structured control flow.
	OpIf    uint8 = 0x60 // pop bool; if false, skip to matching ELSE or END_IF
	OpElse  uint8 = 0x61 // end of then-branch, skip to END_IF
	OpEndIf uint8 = 0x62

	// Bounded iteration.
	// FOR_RANGE: pop collection; iterate elements.
	// op1 = local slot for index (i), op2 = local slot for element value (v).
	// Body runs once per element, then jumps back to the top of the loop.
	// Bounded by collection length — no unbounded loops.
	OpForRange uint8 = 0x68
	OpEndFor   uint8 = 0x69
	OpBreak    uint8 = 0x6A // exit innermost FOR_RANGE

	// Collection literal.
	// Pop op2 elements from stack, push a collection of type typ.
	OpMakeColl uint8 = 0x6C

	// Return.
	OpRet uint8 = 0x70 // pop op2 values as return values, halt

	// Built-in function call.
	OpCallBuiltin uint8 = 0x80 // op1=builtin_id, op2=arg_count

	// Function call (within same program).
	OpCall uint8 = 0x81 // op1=func_index, op2=arg_count
)

// Inst is a single 128-bit instruction.
type Inst struct {
	Opcode uint8
	Typ    uint8
	Op1    uint16
	Op2    uint16
	Flags  uint16
	Imm    uint64
}

// Convenience constructors for hand-assembling bytecode (testing).

func InstConstI64(v int64) Inst {
	return Inst{Opcode: OpConstI64, Imm: uint64(v)}
}

func InstConstF64(v float64) Inst {
	return Inst{Opcode: OpConstF64, Imm: math.Float64bits(v)}
}

func InstConstBool(v bool) Inst {
	var imm uint64
	if v {
		imm = 1
	}
	return Inst{Opcode: OpConstBool, Imm: imm}
}

func InstLoad(slot uint16) Inst {
	return Inst{Opcode: OpLoad, Op1: slot}
}

func InstStore(slot uint16) Inst {
	return Inst{Opcode: OpStore, Op1: slot}
}

func InstArith(op uint8, typ uint8) Inst {
	return Inst{Opcode: op, Typ: typ}
}

func InstCmp(op uint8, typ uint8) Inst {
	return Inst{Opcode: op, Typ: typ}
}

func InstIf() Inst    { return Inst{Opcode: OpIf} }
func InstElse() Inst  { return Inst{Opcode: OpElse} }
func InstEndIf() Inst { return Inst{Opcode: OpEndIf} }

func InstForRange(indexSlot, valueSlot uint16) Inst {
	return Inst{Opcode: OpForRange, Op1: indexSlot, Op2: valueSlot}
}
func InstEndFor() Inst { return Inst{Opcode: OpEndFor} }
func InstBreak() Inst  { return Inst{Opcode: OpBreak} }

func InstMakeColl(typ uint8, count uint16) Inst {
	return Inst{Opcode: OpMakeColl, Typ: typ, Op2: count}
}

func InstCallBuiltin(id, argc uint16) Inst {
	return Inst{Opcode: OpCallBuiltin, Op1: id, Op2: argc}
}

func InstRet(count uint16) Inst {
	return Inst{Opcode: OpRet, Op2: count}
}
