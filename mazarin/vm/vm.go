package vm

import (
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

// Run executes a program with the given arguments and returns the results.
// It enforces the instruction counter limit — if exceeded, the program
// is killed and an error is returned.
func Run(prog *Program, args ...Value) ([]Value, error) {
	vm := machine{
		code:    prog.Code,
		strings: prog.Strings,
		fuel:    MaxInstructions,
	}
	// Push arguments onto the stack (caller pushes, callee pops to locals
	// or reads from stack). For now, args are just the bottom of the stack.
	for _, a := range args {
		if err := vm.push(a); err != nil {
			return nil, err
		}
	}
	if err := vm.exec(); err != nil {
		return nil, err
	}
	// Collect results — whatever is left on the stack.
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

func (m *machine) peek() (Value, error) {
	if m.sp <= 0 {
		return Value{}, &ErrHalt{PC: m.pc, Message: "stack underflow on peek"}
	}
	return m.stack[m.sp-1], nil
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

		switch inst.Opcode {

		// --- Constants ---

		case OpConstI64:
			if err := m.push(I64(int64(inst.Imm))); err != nil {
				return err
			}

		case OpConstF64:
			if err := m.push(F64(math.Float64frombits(inst.Imm))); err != nil {
				return err
			}

		case OpConstBool:
			if err := m.push(Bool(inst.Imm != 0)); err != nil {
				return err
			}

		case OpConstTri:
			if inst.Imm > 2 {
				return m.haltf("invalid tribool value %d", inst.Imm)
			}
			if err := m.push(Tribool(int64(inst.Imm))); err != nil {
				return err
			}

		case OpConstStr:
			idx := int(inst.Op1)
			if idx < 0 || idx >= len(m.strings) {
				return m.haltf("string index %d out of range (table size %d)", idx, len(m.strings))
			}
			if err := m.push(Str(m.strings[idx])); err != nil {
				return err
			}

		// --- Locals ---

		case OpLoad:
			slot := int(inst.Op1)
			if slot >= MaxLocals {
				return m.haltf("local slot %d out of range", slot)
			}
			if err := m.push(m.locals[slot]); err != nil {
				return err
			}

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

		// --- Arithmetic ---

		case OpAdd:
			if err := m.binArith(inst.Typ, func(a, b int64) int64 { return a + b },
				func(a, b float64) float64 { return a + b }); err != nil {
				return err
			}

		case OpSub:
			if err := m.binArith(inst.Typ, func(a, b int64) int64 { return a - b },
				func(a, b float64) float64 { return a - b }); err != nil {
				return err
			}

		case OpMul:
			if err := m.binArith(inst.Typ, func(a, b int64) int64 { return a * b },
				func(a, b float64) float64 { return a * b }); err != nil {
				return err
			}

		case OpDiv:
			if err := m.binArith(inst.Typ, func(a, b int64) int64 {
				if b == 0 {
					return 0 // safe division by zero, like BPF
				}
				return a / b
			}, func(a, b float64) float64 {
				if b == 0 {
					return 0
				}
				return a / b
			}); err != nil {
				return err
			}

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
				denom = 1 // safe mod-by-zero
			}
			if err := m.push(I64(a.i64 % denom)); err != nil {
				return err
			}

		case OpNeg:
			if err := m.unaryArith(inst.Typ, func(a int64) int64 { return -a },
				func(a float64) float64 { return -a }); err != nil {
				return err
			}

		case OpAbs:
			if err := m.unaryArith(inst.Typ, func(a int64) int64 {
				if a < 0 {
					return -a
				}
				return a
			}, func(a float64) float64 { return math.Abs(a) }); err != nil {
				return err
			}

		// --- Comparison ---

		case OpEq, OpNeq, OpLt, OpGt, OpLe, OpGe:
			if err := m.cmp(inst.Opcode, inst.Typ); err != nil {
				return err
			}

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
			if err := m.push(Bool(a.AsBool() && b.AsBool())); err != nil {
				return err
			}

		case OpOr:
			b, err := m.pop()
			if err != nil {
				return err
			}
			a, err := m.pop()
			if err != nil {
				return err
			}
			if err := m.push(Bool(a.AsBool() || b.AsBool())); err != nil {
				return err
			}

		case OpNot:
			a, err := m.pop()
			if err != nil {
				return err
			}
			if err := m.push(Bool(!a.AsBool())); err != nil {
				return err
			}

		// --- Conversions ---

		case OpI64ToF64:
			a, err := m.pop()
			if err != nil {
				return err
			}
			if err := m.push(F64(float64(a.i64))); err != nil {
				return err
			}

		case OpF64ToI64:
			a, err := m.pop()
			if err != nil {
				return err
			}
			if err := m.push(I64(int64(a.f64))); err != nil {
				return err
			}

		// --- Structured control flow ---

		case OpIf:
			cond, err := m.pop()
			if err != nil {
				return err
			}
			if !cond.AsBool() {
				// Skip to matching ELSE or END_IF.
				if err := m.skipToElseOrEndIf(); err != nil {
					return err
				}
			}

		case OpElse:
			// We executed the then-branch, skip to END_IF.
			if err := m.skipToEndIf(); err != nil {
				return err
			}

		case OpEndIf:
			// No-op, just a marker.

		// --- Return ---

		case OpRet:
			count := int(inst.Op2)
			if count > m.sp {
				return m.haltf("RET %d but only %d values on stack", count, m.sp)
			}
			// Keep only the top `count` values.
			copy(m.stack[:count], m.stack[m.sp-count:m.sp])
			m.sp = count
			return nil // halt successfully

		default:
			return m.haltf("unknown opcode 0x%02x", inst.Opcode)
		}
	}
	// Fell off the end without RET.
	return &ErrHalt{PC: m.pc, Message: "program ended without RET"}
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
				return nil // resume after ELSE
			}
		case OpEndIf:
			depth--
			if depth == 0 {
				return nil // no ELSE branch, resume after END_IF
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
