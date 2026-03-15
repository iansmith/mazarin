package vm

import (
	"errors"
	"fmt"
	"math"
)

// Limits enforced by the VM. Programs exceeding these are rejected
// by the verifier (not yet implemented) and checked at runtime as
// defense-in-depth.
const (
	MaxInstructions = 100_000 // instruction counter kill limit
	MaxStackDepth   = 256
	MaxLocals       = 64
)

// Program is a verified, ready-to-execute constraint program.
type Program struct {
	Code    []Inst   // instruction stream
	Strings []string // string constant table
	NumArgs uint16   // number of parameters (taken from stack on entry)
}

// ErrHalt is returned when the VM halts abnormally.
type ErrHalt struct {
	PC      int
	Message string
}

func (e *ErrHalt) Error() string {
	return fmt.Sprintf("vm: halt at pc=%d: %s", e.PC, e.Message)
}

// errRet is a sentinel returned by execOne when OpRet is encountered.
var errRet = errors.New("ret")

// Run executes a program with the given arguments and returns the results.
// It enforces the instruction counter limit — if exceeded, the program
// is killed and an error is returned.
func Run(prog *Program, args ...Value) ([]Value, error) {
	vm := machine{
		code:    prog.Code,
		strings: prog.Strings,
		fuel:    MaxInstructions,
	}
	for _, a := range args {
		if err := vm.push(a); err != nil {
			return nil, err
		}
	}
	if err := vm.exec(); err != nil {
		return nil, err
	}
	return vm.stack[:vm.sp], nil
}

type machine struct {
	code    []Inst
	strings []string
	stack   [MaxStackDepth]Value
	locals  [MaxLocals]Value
	sp      int // stack pointer (points to next free slot)
	pc      int // program counter
	fuel    int // instructions remaining
}

func (m *machine) push(v Value) error {
	if m.sp >= MaxStackDepth {
		return &ErrHalt{PC: m.pc, Message: "stack overflow"}
	}
	m.stack[m.sp] = v
	m.sp++
	return nil
}

func (m *machine) pop() (Value, error) {
	if m.sp <= 0 {
		return Value{}, &ErrHalt{PC: m.pc, Message: "stack underflow"}
	}
	m.sp--
	return m.stack[m.sp], nil
}

func (m *machine) haltf(format string, args ...any) error {
	return &ErrHalt{PC: m.pc, Message: fmt.Sprintf(format, args...)}
}

func (m *machine) exec() error {
	for m.pc < len(m.code) {
		if m.fuel <= 0 {
			return &ErrHalt{PC: m.pc, Message: "instruction limit exceeded"}
		}
		m.fuel--
		inst := m.code[m.pc]
		m.pc++

		err := m.execOne(inst)
		if err == errRet {
			return nil
		}
		if err != nil {
			return err
		}
	}
	return &ErrHalt{PC: m.pc, Message: "program ended without RET"}
}

// execOne dispatches a single instruction. Returns errRet for OpRet.
func (m *machine) execOne(inst Inst) error {
	switch inst.Opcode {

	// --- Constants ---

	case OpConstI64:
		return m.push(I64(int64(inst.Imm)))

	case OpConstF64:
		return m.push(F64(math.Float64frombits(inst.Imm)))

	case OpConstBool:
		return m.push(Bool(inst.Imm != 0))

	case OpConstTri:
		if inst.Imm > 2 {
			return m.haltf("invalid tribool value %d", inst.Imm)
		}
		return m.push(Tribool(int64(inst.Imm)))

	case OpConstStr:
		idx := int(inst.Op1)
		if idx < 0 || idx >= len(m.strings) {
			return m.haltf("string index %d out of range (table size %d)", idx, len(m.strings))
		}
		return m.push(Str(m.strings[idx]))

	// --- Locals ---

	case OpLoad:
		slot := int(inst.Op1)
		if slot >= MaxLocals {
			return m.haltf("local slot %d out of range", slot)
		}
		return m.push(m.locals[slot])

	case OpStore:
		slot := int(inst.Op1)
		if slot >= MaxLocals {
			return m.haltf("local slot %d out of range", slot)
		}
		v, err := m.pop()
		if err != nil {
			return err
		}
		m.locals[slot] = v
		return nil

	// --- Arithmetic ---

	case OpAdd:
		return m.binArith(inst.Typ, func(a, b int64) int64 { return a + b },
			func(a, b float64) float64 { return a + b })

	case OpSub:
		return m.binArith(inst.Typ, func(a, b int64) int64 { return a - b },
			func(a, b float64) float64 { return a - b })

	case OpMul:
		return m.binArith(inst.Typ, func(a, b int64) int64 { return a * b },
			func(a, b float64) float64 { return a * b })

	case OpDiv:
		return m.binArith(inst.Typ, func(a, b int64) int64 {
			if b == 0 {
				return 0
			}
			return a / b
		}, func(a, b float64) float64 {
			if b == 0 {
				return 0
			}
			return a / b
		})

	case OpMod:
		b, err := m.pop()
		if err != nil {
			return err
		}
		a, err := m.pop()
		if err != nil {
			return err
		}
		if a.typ != TypeI64 || b.typ != TypeI64 {
			return m.haltf("MOD requires int64, got %s and %s", TypeName(a.typ), TypeName(b.typ))
		}
		denom := b.i64
		if denom == 0 {
			denom = 1
		}
		return m.push(I64(a.i64 % denom))

	case OpNeg:
		return m.unaryArith(inst.Typ, func(a int64) int64 { return -a },
			func(a float64) float64 { return -a })

	case OpAbs:
		return m.unaryArith(inst.Typ, func(a int64) int64 {
			if a < 0 {
				return -a
			}
			return a
		}, func(a float64) float64 { return math.Abs(a) })

	// --- Comparison ---

	case OpEq, OpNeq, OpLt, OpGt, OpLe, OpGe:
		return m.cmp(inst.Opcode, inst.Typ)

	// --- Boolean ---

	case OpAnd:
		b, err := m.pop()
		if err != nil {
			return err
		}
		a, err := m.pop()
		if err != nil {
			return err
		}
		return m.push(Bool(a.AsBool() && b.AsBool()))

	case OpOr:
		b, err := m.pop()
		if err != nil {
			return err
		}
		a, err := m.pop()
		if err != nil {
			return err
		}
		return m.push(Bool(a.AsBool() || b.AsBool()))

	case OpNot:
		a, err := m.pop()
		if err != nil {
			return err
		}
		return m.push(Bool(!a.AsBool()))

	// --- Tribool ---

	case OpAnd3:
		b, err := m.pop()
		if err != nil {
			return err
		}
		a, err := m.pop()
		if err != nil {
			return err
		}
		return m.push(Tribool(triAnd(a.i64, b.i64)))

	case OpOr3:
		b, err := m.pop()
		if err != nil {
			return err
		}
		a, err := m.pop()
		if err != nil {
			return err
		}
		return m.push(Tribool(triOr(a.i64, b.i64)))

	case OpNot3:
		a, err := m.pop()
		if err != nil {
			return err
		}
		return m.push(Tribool(triNot(a.i64)))

	case OpKnown:
		a, err := m.pop()
		if err != nil {
			return err
		}
		return m.push(Bool(a.i64 != TriboolUnknown))

	// --- Conversions ---

	case OpI64ToF64:
		a, err := m.pop()
		if err != nil {
			return err
		}
		return m.push(F64(float64(a.i64)))

	case OpF64ToI64:
		a, err := m.pop()
		if err != nil {
			return err
		}
		return m.push(I64(int64(a.f64)))

	case OpBoolToTri:
		a, err := m.pop()
		if err != nil {
			return err
		}
		return m.push(Tribool(a.i64))

	// --- Structured control flow ---

	case OpIf:
		cond, err := m.pop()
		if err != nil {
			return err
		}
		if !cond.AsBool() {
			return m.skipToElseOrEndIf()
		}
		return nil

	case OpElse:
		return m.skipToEndIf()

	case OpEndIf:
		return nil

	// --- Bounded iteration ---

	case OpForRange:
		coll, err := m.pop()
		if err != nil {
			return err
		}
		if !isCollType(coll.typ) {
			return m.haltf("FOR_RANGE requires collection, got %s", TypeName(coll.typ))
		}
		idxSlot := int(inst.Op1)
		valSlot := int(inst.Op2)
		bodyStart := m.pc
		for i, elem := range coll.coll {
			m.locals[idxSlot] = I64(int64(i))
			m.locals[valSlot] = elem
			m.pc = bodyStart
			sig, err := m.execForBody()
			if err != nil {
				return err
			}
			if sig == forSignalBreak {
				break
			}
			if sig == forSignalRet {
				return errRet
			}
		}
		if len(coll.coll) == 0 {
			return m.skipToEndFor()
		}
		return nil

	case OpEndFor:
		return nil

	case OpBreak:
		return m.haltf("BREAK outside FOR_RANGE")

	// --- Collection literal ---

	case OpMakeColl:
		count := int(inst.Op2)
		if count > m.sp {
			return m.haltf("MAKE_COLL %d but only %d values on stack", count, m.sp)
		}
		elems := make([]Value, count)
		for i := count - 1; i >= 0; i-- {
			v, err := m.pop()
			if err != nil {
				return err
			}
			elems[i] = v
		}
		return m.push(Value{typ: inst.Typ, coll: elems})

	// --- Builtin calls ---

	case OpCallBuiltin:
		return m.callBuiltin(inst.Op1, inst.Op2, inst.Typ)

	// --- Return ---

	case OpRet:
		count := int(inst.Op2)
		if count > m.sp {
			return m.haltf("RET %d but only %d values on stack", count, m.sp)
		}
		copy(m.stack[:count], m.stack[m.sp-count:m.sp])
		m.sp = count
		return errRet

	default:
		return m.haltf("unknown opcode 0x%02x", inst.Opcode)
	}
}

// forSignal indicates how a FOR_RANGE body iteration ended.
type forSignal int

const (
	forSignalContinue forSignal = iota
	forSignalBreak
	forSignalRet
)

// execForBody runs one iteration of a FOR_RANGE body. Returns the signal
// indicating whether to continue, break, or propagate a return.
func (m *machine) execForBody() (forSignal, error) {
	for m.pc < len(m.code) {
		if m.fuel <= 0 {
			return 0, &ErrHalt{PC: m.pc, Message: "instruction limit exceeded"}
		}
		inst := m.code[m.pc]

		if inst.Opcode == OpEndFor {
			m.pc++
			return forSignalContinue, nil
		}
		if inst.Opcode == OpBreak {
			m.pc++
			if err := m.skipToEndFor(); err != nil {
				return 0, err
			}
			return forSignalBreak, nil
		}

		m.fuel--
		m.pc++
		err := m.execOne(inst)
		if err == errRet {
			return forSignalRet, nil
		}
		if err != nil {
			return 0, err
		}
	}
	return 0, &ErrHalt{PC: m.pc, Message: "unterminated FOR_RANGE body"}
}

// binArith pops two values, applies the operation, pushes the result.
func (m *machine) binArith(typ uint8, iop func(int64, int64) int64, fop func(float64, float64) float64) error {
	b, err := m.pop()
	if err != nil {
		return err
	}
	a, err := m.pop()
	if err != nil {
		return err
	}
	switch typ {
	case TypeI64:
		if a.typ != TypeI64 || b.typ != TypeI64 {
			return m.haltf("arithmetic requires int64, got %s and %s", TypeName(a.typ), TypeName(b.typ))
		}
		return m.push(I64(iop(a.i64, b.i64)))
	case TypeF64:
		if a.typ != TypeF64 || b.typ != TypeF64 {
			return m.haltf("arithmetic requires float64, got %s and %s", TypeName(a.typ), TypeName(b.typ))
		}
		return m.push(F64(fop(a.f64, b.f64)))
	default:
		return m.haltf("arithmetic on unsupported type %s", TypeName(typ))
	}
}

// unaryArith pops one value, applies the operation, pushes the result.
func (m *machine) unaryArith(typ uint8, iop func(int64) int64, fop func(float64) float64) error {
	a, err := m.pop()
	if err != nil {
		return err
	}
	switch typ {
	case TypeI64:
		if a.typ != TypeI64 {
			return m.haltf("unary arithmetic requires int64, got %s", TypeName(a.typ))
		}
		return m.push(I64(iop(a.i64)))
	case TypeF64:
		if a.typ != TypeF64 {
			return m.haltf("unary arithmetic requires float64, got %s", TypeName(a.typ))
		}
		return m.push(F64(fop(a.f64)))
	default:
		return m.haltf("unary arithmetic on unsupported type %s", TypeName(typ))
	}
}

// cmp pops two values, compares them, pushes a Bool.
func (m *machine) cmp(op uint8, typ uint8) error {
	b, err := m.pop()
	if err != nil {
		return err
	}
	a, err := m.pop()
	if err != nil {
		return err
	}

	var result bool
	switch typ {
	case TypeI64:
		if a.typ != TypeI64 || b.typ != TypeI64 {
			return m.haltf("compare requires int64, got %s and %s", TypeName(a.typ), TypeName(b.typ))
		}
		switch op {
		case OpEq:
			result = a.i64 == b.i64
		case OpNeq:
			result = a.i64 != b.i64
		case OpLt:
			result = a.i64 < b.i64
		case OpGt:
			result = a.i64 > b.i64
		case OpLe:
			result = a.i64 <= b.i64
		case OpGe:
			result = a.i64 >= b.i64
		}
	case TypeF64:
		if a.typ != TypeF64 || b.typ != TypeF64 {
			return m.haltf("compare requires float64, got %s and %s", TypeName(a.typ), TypeName(b.typ))
		}
		switch op {
		case OpEq:
			result = a.f64 == b.f64
		case OpNeq:
			result = a.f64 != b.f64
		case OpLt:
			result = a.f64 < b.f64
		case OpGt:
			result = a.f64 > b.f64
		case OpLe:
			result = a.f64 <= b.f64
		case OpGe:
			result = a.f64 >= b.f64
		}
	case TypeStr:
		if a.typ != TypeStr || b.typ != TypeStr {
			return m.haltf("compare requires string, got %s and %s", TypeName(a.typ), TypeName(b.typ))
		}
		switch op {
		case OpEq:
			result = a.str == b.str
		case OpNeq:
			result = a.str != b.str
		case OpLt:
			result = a.str < b.str
		case OpGt:
			result = a.str > b.str
		case OpLe:
			result = a.str <= b.str
		case OpGe:
			result = a.str >= b.str
		}
	default:
		return m.haltf("compare on unsupported type %s", TypeName(typ))
	}
	return m.push(Bool(result))
}

// skipToElseOrEndIf advances pc past a matching ELSE or END_IF,
// respecting nested IF blocks.
func (m *machine) skipToElseOrEndIf() error {
	depth := 1
	for m.pc < len(m.code) {
		op := m.code[m.pc].Opcode
		m.pc++
		switch op {
		case OpIf:
			depth++
		case OpElse:
			if depth == 1 {
				return nil
			}
		case OpEndIf:
			depth--
			if depth == 0 {
				return nil
			}
		}
	}
	return &ErrHalt{PC: m.pc, Message: "unterminated IF block"}
}

// skipToEndIf advances pc past the matching END_IF, respecting nesting.
func (m *machine) skipToEndIf() error {
	depth := 1
	for m.pc < len(m.code) {
		op := m.code[m.pc].Opcode
		m.pc++
		switch op {
		case OpIf:
			depth++
		case OpEndIf:
			depth--
			if depth == 0 {
				return nil
			}
		}
	}
	return &ErrHalt{PC: m.pc, Message: "unterminated IF block (seeking END_IF)"}
}

// skipToEndFor advances pc past the matching END_FOR, respecting nesting.
func (m *machine) skipToEndFor() error {
	depth := 1
	for m.pc < len(m.code) {
		op := m.code[m.pc].Opcode
		m.pc++
		switch op {
		case OpForRange:
			depth++
		case OpEndFor:
			depth--
			if depth == 0 {
				return nil
			}
		}
	}
	return &ErrHalt{PC: m.pc, Message: "unterminated FOR_RANGE (seeking END_FOR)"}
}

// Kleene three-valued logic (Tribool).
// 0=false, 1=true, 2=unknown.

func triAnd(a, b int64) int64 {
	if a == TriboolFalse || b == TriboolFalse {
		return TriboolFalse
	}
	if a == TriboolUnknown || b == TriboolUnknown {
		return TriboolUnknown
	}
	return TriboolTrue
}

func triOr(a, b int64) int64 {
	if a == TriboolTrue || b == TriboolTrue {
		return TriboolTrue
	}
	if a == TriboolUnknown || b == TriboolUnknown {
		return TriboolUnknown
	}
	return TriboolFalse
}

func triNot(a int64) int64 {
	switch a {
	case TriboolFalse:
		return TriboolTrue
	case TriboolTrue:
		return TriboolFalse
	default:
		return TriboolUnknown
	}
}
