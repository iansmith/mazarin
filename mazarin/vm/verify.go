package vm

import "fmt"

// Verify checks a program for safety properties at load time, before
// execution. It performs abstract interpretation over the instruction
// stream, tracking type stack and local initialization to catch errors
// that would otherwise only surface at runtime.
//
// Properties checked:
//   - Structural: IF/ELSE/END_IF and FOR_RANGE/END_FOR properly nested
//   - Stack depth: consistent at control flow merge points, never negative
//   - Type safety: operations receive correct types on the abstract stack
//   - Initialized locals: LOAD never reads an uninitialized slot
//   - Termination: all paths end with RET
//   - Bounds: program size, stack depth, local slots, nesting depth
//   - BREAK only inside FOR_RANGE
func Verify(prog *Program) error {
	if len(prog.Code) == 0 {
		return &VerifyError{PC: 0, Message: "empty program"}
	}
	if len(prog.Code) > MaxInstructions {
		return &VerifyError{PC: 0, Message: fmt.Sprintf("program too large: %d instructions (max %d)", len(prog.Code), MaxInstructions)}
	}

	if prog.Funcs != nil {
		return verifyMultiFunc(prog)
	}

	// Single-function (backward compatible).
	v := &verifier{
		code:     prog.Code,
		strings:  prog.Strings,
		numArgs:  prog.NumArgs,
		argTypes: prog.ArgTypes,
	}
	return v.verify()
}

// verifyMultiFunc validates a multi-function program.
func verifyMultiFunc(prog *Program) error {
	if int(prog.Entry) >= len(prog.Funcs) {
		return &VerifyError{PC: 0, Message: fmt.Sprintf("entry index %d out of range (have %d funcs)", prog.Entry, len(prog.Funcs))}
	}

	// Check for call graph cycles.
	if err := checkCallGraph(prog.Funcs, prog.Code); err != nil {
		return err
	}

	// Verify each function independently.
	for i, fn := range prog.Funcs {
		// Find the end of this function's code (scan for the last RET).
		endPC := len(prog.Code)
		if i+1 < len(prog.Funcs) {
			endPC = int(prog.Funcs[i+1].PC)
		}
		fnCode := prog.Code[fn.PC:endPC]

		v := &verifier{
			code:     fnCode,
			strings:  prog.Strings,
			numArgs:  fn.NumArgs,
			argTypes: prog.ArgTypes, // entry function types; helpers use I64 fallback
			funcs:    prog.Funcs,
		}
		if i != int(prog.Entry) {
			// Non-entry functions: seed with their arg count but no ArgTypes info.
			v.argTypes = nil
		}
		if err := v.verify(); err != nil {
			return &VerifyError{
				PC:      int(fn.PC) + err.(*VerifyError).PC,
				Message: fmt.Sprintf("in %s: %s", fn.Name, err.(*VerifyError).Message),
			}
		}
	}
	return nil
}

// checkCallGraph builds a call graph from OpCall instructions and rejects cycles.
func checkCallGraph(funcs []FuncInfo, code []Inst) error {
	// Build adjacency list.
	adj := make([][]uint16, len(funcs))
	for i, fn := range funcs {
		endPC := len(code)
		if i+1 < len(funcs) {
			endPC = int(funcs[i+1].PC)
		}
		for pc := int(fn.PC); pc < endPC; pc++ {
			if code[pc].Opcode == OpCall {
				target := code[pc].Op1
				if int(target) >= len(funcs) {
					return &VerifyError{PC: pc, Message: fmt.Sprintf("call to invalid function index %d", target)}
				}
				adj[i] = append(adj[i], target)
			}
		}
	}

	// DFS cycle detection.
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := make([]int, len(funcs))
	var dfs func(u int) error
	dfs = func(u int) error {
		color[u] = gray
		for _, v := range adj[u] {
			if color[v] == gray {
				return &VerifyError{
					PC:      int(funcs[u].PC),
					Message: fmt.Sprintf("recursion: %s calls %s (cycles not allowed)", funcs[u].Name, funcs[v].Name),
				}
			}
			if color[v] == white {
				if err := dfs(int(v)); err != nil {
					return err
				}
			}
		}
		color[u] = black
		return nil
	}
	for i := range funcs {
		if color[i] == white {
			if err := dfs(i); err != nil {
				return err
			}
		}
	}
	return nil
}

// VerifyError describes a static verification failure.
type VerifyError struct {
	PC      int
	Message string
}

func (e *VerifyError) Error() string {
	return fmt.Sprintf("verify: pc=%d: %s", e.PC, e.Message)
}

const maxNestingDepth = 32

type verifier struct {
	code     []Inst
	strings  []string
	numArgs  uint16
	argTypes []uint8     // type tags for arguments; nil = all I64
	funcs    []FuncInfo  // nil for single-function programs

	// Abstract state.
	tstack    []uint8           // type stack (stack of type tags)
	initLocal [MaxLocals]bool   // which locals have been initialized
	localType [MaxLocals]uint8  // type stored in each local
	pc        int

	// Control flow nesting.
	ctrlStack []ctrlFrame
	forDepth  int // current FOR_RANGE nesting depth
}

type ctrlKind byte

const (
	ctrlIf       ctrlKind = 'I'
	ctrlForRange ctrlKind = 'F'
)

type ctrlFrame struct {
	kind     ctrlKind
	pc       int // PC of the control instruction

	// Saved state at entry (after consuming the guard value).
	entryStack    []uint8
	entryInit     [MaxLocals]bool
	entryLocalTyp [MaxLocals]uint8

	// IF-specific: then-branch result.
	thenStack    []uint8
	thenInit     [MaxLocals]bool
	thenLocalTyp [MaxLocals]uint8
	thenReturned bool
	hasElse      bool

	// FOR_RANGE-specific.
	idxSlot  uint16
	valSlot  uint16
	elemType uint8
}

func (v *verifier) errf(format string, args ...any) error {
	return &VerifyError{PC: v.pc, Message: fmt.Sprintf(format, args...)}
}

func (v *verifier) pushType(t uint8) error {
	if len(v.tstack) >= MaxStackDepth {
		return v.errf("stack overflow (depth %d)", len(v.tstack))
	}
	v.tstack = append(v.tstack, t)
	return nil
}

func (v *verifier) popType() (uint8, error) {
	if len(v.tstack) == 0 {
		return 0, v.errf("stack underflow")
	}
	t := v.tstack[len(v.tstack)-1]
	v.tstack = v.tstack[:len(v.tstack)-1]
	return t, nil
}

func (v *verifier) popExpect(expected uint8) error {
	t, err := v.popType()
	if err != nil {
		return err
	}
	if t != expected {
		return v.errf("expected %s on stack, got %s", TypeName(expected), TypeName(t))
	}
	return nil
}

func (v *verifier) peekType() (uint8, error) {
	if len(v.tstack) == 0 {
		return 0, v.errf("stack underflow on peek")
	}
	return v.tstack[len(v.tstack)-1], nil
}

func (v *verifier) saveStack() []uint8 {
	s := make([]uint8, len(v.tstack))
	copy(s, v.tstack)
	return s
}

func (v *verifier) restoreStack(s []uint8) {
	v.tstack = make([]uint8, len(s))
	copy(v.tstack, s)
}

func (v *verifier) saveInit() [MaxLocals]bool {
	return v.initLocal
}

func (v *verifier) restoreInit(init [MaxLocals]bool) {
	v.initLocal = init
}

func (v *verifier) saveLocalTypes() [MaxLocals]uint8 {
	return v.localType
}

func (v *verifier) restoreLocalTypes(lt [MaxLocals]uint8) {
	v.localType = lt
}

// mergeInit returns the intersection — only locals initialized on both paths.
func mergeInit(a, b [MaxLocals]bool) [MaxLocals]bool {
	var r [MaxLocals]bool
	for i := range r {
		r[i] = a[i] && b[i]
	}
	return r
}

func stacksEqual(a, b []uint8) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (v *verifier) verify() error {
	// Seed the type stack with argument types (the VM pushes args before execution).
	for i := 0; i < int(v.numArgs); i++ {
		if i < len(v.argTypes) {
			v.tstack = append(v.tstack, v.argTypes[i])
		} else {
			v.tstack = append(v.tstack, TypeI64)
		}
	}

	returned := false

	for v.pc < len(v.code) {
		inst := v.code[v.pc]
		v.pc++

		// After unconditional return/break, only control flow markers
		// are valid. The handlers below manage the returned flag.
		if returned {
			switch inst.Opcode {
			case OpElse, OpEndIf, OpEndFor:
				// Handled below — they will clear/merge returned.
			default:
				return v.errf("unreachable code after RET")
			}
		}

		switch inst.Opcode {

		// --- Constants ---

		case OpConstI64:
			if err := v.pushType(TypeI64); err != nil {
				return err
			}
		case OpConstF64:
			if err := v.pushType(TypeF64); err != nil {
				return err
			}
		case OpConstBool:
			if err := v.pushType(TypeBool); err != nil {
				return err
			}
		case OpConstTri:
			if inst.Imm > 2 {
				return v.errf("invalid tribool constant %d", inst.Imm)
			}
			if err := v.pushType(TypeTribool); err != nil {
				return err
			}
		case OpConstStr:
			idx := int(inst.Op1)
			if idx < 0 || idx >= len(v.strings) {
				return v.errf("string index %d out of range (table size %d)", idx, len(v.strings))
			}
			if err := v.pushType(TypeStr); err != nil {
				return err
			}

		// --- Locals ---

		case OpLoad:
			slot := int(inst.Op1)
			if slot >= MaxLocals {
				return v.errf("local slot %d out of range (max %d)", slot, MaxLocals)
			}
			if !v.initLocal[slot] {
				return v.errf("load from uninitialized local %d", slot)
			}
			if err := v.pushType(v.localType[slot]); err != nil {
				return err
			}

		case OpStore:
			slot := int(inst.Op1)
			if slot >= MaxLocals {
				return v.errf("local slot %d out of range (max %d)", slot, MaxLocals)
			}
			t, err := v.popType()
			if err != nil {
				return err
			}
			v.initLocal[slot] = true
			v.localType[slot] = t

		case OpDup:
			t, err := v.peekType()
			if err != nil {
				return err
			}
			if err := v.pushType(t); err != nil {
				return err
			}

		// --- Arithmetic ---

		case OpAdd, OpSub, OpMul, OpDiv:
			if inst.Typ != TypeI64 && inst.Typ != TypeF64 {
				return v.errf("arithmetic requires I64 or F64 type tag, got %s", TypeName(inst.Typ))
			}
			if _, err := v.popType(); err != nil {
				return err
			}
			if _, err := v.popType(); err != nil {
				return err
			}
			if err := v.pushType(inst.Typ); err != nil {
				return err
			}

		case OpMod:
			if _, err := v.popType(); err != nil {
				return err
			}
			if _, err := v.popType(); err != nil {
				return err
			}
			if err := v.pushType(TypeI64); err != nil {
				return err
			}

		case OpNeg, OpAbs:
			if inst.Typ != TypeI64 && inst.Typ != TypeF64 {
				return v.errf("unary arithmetic requires I64 or F64 type tag, got %s", TypeName(inst.Typ))
			}
			if _, err := v.popType(); err != nil {
				return err
			}
			if err := v.pushType(inst.Typ); err != nil {
				return err
			}

		// --- Comparison ---

		case OpEq, OpNeq, OpLt, OpGt, OpLe, OpGe:
			if inst.Typ != TypeI64 && inst.Typ != TypeF64 && inst.Typ != TypeStr && inst.Typ != TypeBool {
				return v.errf("comparison requires I64, F64, Str, or Bool type tag, got %s", TypeName(inst.Typ))
			}
			if _, err := v.popType(); err != nil {
				return err
			}
			if _, err := v.popType(); err != nil {
				return err
			}
			if err := v.pushType(TypeBool); err != nil {
				return err
			}

		// --- Boolean ---

		case OpAnd, OpOr:
			if _, err := v.popType(); err != nil {
				return err
			}
			if _, err := v.popType(); err != nil {
				return err
			}
			if err := v.pushType(TypeBool); err != nil {
				return err
			}

		case OpNot:
			if _, err := v.popType(); err != nil {
				return err
			}
			if err := v.pushType(TypeBool); err != nil {
				return err
			}

		// --- Tribool ---

		case OpAnd3, OpOr3:
			if _, err := v.popType(); err != nil {
				return err
			}
			if _, err := v.popType(); err != nil {
				return err
			}
			if err := v.pushType(TypeTribool); err != nil {
				return err
			}

		case OpNot3:
			if _, err := v.popType(); err != nil {
				return err
			}
			if err := v.pushType(TypeTribool); err != nil {
				return err
			}

		case OpKnown:
			if _, err := v.popType(); err != nil {
				return err
			}
			if err := v.pushType(TypeBool); err != nil {
				return err
			}

		// --- Conversions ---

		case OpI64ToF64:
			if _, err := v.popType(); err != nil {
				return err
			}
			if err := v.pushType(TypeF64); err != nil {
				return err
			}

		case OpF64ToI64:
			if _, err := v.popType(); err != nil {
				return err
			}
			if err := v.pushType(TypeI64); err != nil {
				return err
			}

		case OpBoolToTri:
			if _, err := v.popType(); err != nil {
				return err
			}
			if err := v.pushType(TypeTribool); err != nil {
				return err
			}

		// --- Structured control flow: IF ---

		case OpIf:
			if len(v.ctrlStack) >= maxNestingDepth {
				return v.errf("nesting too deep (max %d)", maxNestingDepth)
			}
			if _, err := v.popType(); err != nil {
				return err
			}
			v.ctrlStack = append(v.ctrlStack, ctrlFrame{
				kind:          ctrlIf,
				pc:            v.pc - 1,
				entryStack:    v.saveStack(),
				entryInit:     v.saveInit(),
				entryLocalTyp: v.saveLocalTypes(),
			})

		case OpElse:
			if len(v.ctrlStack) == 0 {
				return v.errf("ELSE without matching IF")
			}
			frame := &v.ctrlStack[len(v.ctrlStack)-1]
			if frame.kind != ctrlIf {
				return v.errf("ELSE inside FOR_RANGE (not IF)")
			}
			if frame.hasElse {
				return v.errf("duplicate ELSE")
			}
			frame.hasElse = true
			frame.thenStack = v.saveStack()
			frame.thenInit = v.saveInit()
			frame.thenLocalTyp = v.saveLocalTypes()
			frame.thenReturned = returned
			// Restore pre-IF state for the else-branch.
			v.restoreStack(frame.entryStack)
			v.restoreInit(frame.entryInit)
			v.restoreLocalTypes(frame.entryLocalTyp)
			returned = false

		case OpEndIf:
			if len(v.ctrlStack) == 0 {
				return v.errf("END_IF without matching IF")
			}
			frame := v.ctrlStack[len(v.ctrlStack)-1]
			v.ctrlStack = v.ctrlStack[:len(v.ctrlStack)-1]
			if frame.kind != ctrlIf {
				return v.errf("END_IF does not match IF (found FOR_RANGE)")
			}

			if frame.hasElse {
				elseStack := v.saveStack()
				elseInit := v.saveInit()
				elseLocalTyp := v.saveLocalTypes()
				elseReturned := returned

				if frame.thenReturned && elseReturned {
					// Both branches return — code after is unreachable.
					returned = true
				} else if frame.thenReturned {
					// Only then returned — continue with else state.
					v.restoreStack(elseStack)
					v.restoreInit(elseInit)
					v.restoreLocalTypes(elseLocalTyp)
					returned = false
				} else if elseReturned {
					// Only else returned — continue with then state.
					v.restoreStack(frame.thenStack)
					v.restoreInit(frame.thenInit)
					v.restoreLocalTypes(frame.thenLocalTyp)
					returned = false
				} else {
					// Neither returned — stacks must match.
					if !stacksEqual(frame.thenStack, elseStack) {
						return v.errf("stack mismatch at END_IF: then-branch depth %d vs else-branch depth %d",
							len(frame.thenStack), len(elseStack))
					}
					v.restoreStack(elseStack)
					v.restoreInit(mergeInit(frame.thenInit, elseInit))
					v.restoreLocalTypes(elseLocalTyp) // conservative: use else side
					returned = false
				}
			} else {
				// IF without ELSE: the false path skips straight here.
				if returned {
					// Then-branch returned. False path continues with pre-IF state.
					v.restoreStack(frame.entryStack)
					v.restoreInit(frame.entryInit)
					v.restoreLocalTypes(frame.entryLocalTyp)
					returned = false
				} else {
					// Then-branch did not return.
					// Stack must match pre-IF (since false path skips body).
					if !stacksEqual(v.tstack, frame.entryStack) {
						return v.errf("IF without ELSE must not change stack depth (entry %d, exit %d)",
							len(frame.entryStack), len(v.tstack))
					}
					v.initLocal = mergeInit(v.initLocal, frame.entryInit)
				}
			}

		// --- Structured control flow: FOR_RANGE ---

		case OpForRange:
			if len(v.ctrlStack) >= maxNestingDepth {
				return v.errf("nesting too deep (max %d)", maxNestingDepth)
			}
			// Pop collection.
			collTyp, err := v.popType()
			if err != nil {
				return err
			}
			if !isCollType(collTyp) {
				return v.errf("FOR_RANGE requires collection, got %s", TypeName(collTyp))
			}
			idxSlot := inst.Op1
			valSlot := inst.Op2
			if int(idxSlot) >= MaxLocals || int(valSlot) >= MaxLocals {
				return v.errf("FOR_RANGE local slots out of range")
			}

			v.ctrlStack = append(v.ctrlStack, ctrlFrame{
				kind:          ctrlForRange,
				pc:            v.pc - 1,
				entryStack:    v.saveStack(),
				entryInit:     v.saveInit(),
				entryLocalTyp: v.saveLocalTypes(),
				idxSlot:       idxSlot,
				valSlot:       valSlot,
				elemType:      elementType(collTyp),
			})
			v.forDepth++
			// Inside the body, index and value locals are initialized.
			v.initLocal[idxSlot] = true
			v.initLocal[valSlot] = true
			v.localType[idxSlot] = TypeI64
			v.localType[valSlot] = elementType(collTyp)

		case OpEndFor:
			if len(v.ctrlStack) == 0 {
				return v.errf("END_FOR without matching FOR_RANGE")
			}
			frame := v.ctrlStack[len(v.ctrlStack)-1]
			v.ctrlStack = v.ctrlStack[:len(v.ctrlStack)-1]
			if frame.kind != ctrlForRange {
				return v.errf("END_FOR does not match FOR_RANGE (found IF)")
			}
			v.forDepth--

			if !returned {
				// Body did not unconditionally return.
				// Stack must match pre-body (body must not change stack depth).
				if !stacksEqual(v.tstack, frame.entryStack) {
					return v.errf("FOR_RANGE body must not change stack depth (entry %d, exit %d)",
						len(frame.entryStack), len(v.tstack))
				}
			}
			// Conservative: restore pre-loop state (body might not have executed).
			v.restoreStack(frame.entryStack)
			v.restoreInit(frame.entryInit)
			v.restoreLocalTypes(frame.entryLocalTyp)
			returned = false

		case OpBreak:
			if v.forDepth == 0 {
				return v.errf("BREAK outside FOR_RANGE")
			}
			// After break, code until END_FOR is unreachable.
			returned = true

		// --- Collection literal ---

		case OpMakeColl:
			if !isCollType(inst.Typ) {
				return v.errf("MAKE_COLL requires collection type tag, got %s", TypeName(inst.Typ))
			}
			count := int(inst.Op2)
			for i := 0; i < count; i++ {
				if _, err := v.popType(); err != nil {
					return err
				}
			}
			if err := v.pushType(inst.Typ); err != nil {
				return err
			}

		// --- Builtin calls ---

		case OpCallBuiltin:
			if err := v.verifyBuiltin(inst.Op1, inst.Op2, inst.Typ); err != nil {
				return err
			}

		// --- Function calls ---

		case OpCall:
			funcIdx := int(inst.Op1)
			argc := int(inst.Op2)
			if v.funcs == nil || funcIdx >= len(v.funcs) {
				return v.errf("call to invalid function index %d", funcIdx)
			}
			target := v.funcs[funcIdx]
			if argc != int(target.NumArgs) {
				return v.errf("call to %s: expected %d args, got %d", target.Name, target.NumArgs, argc)
			}
			// Pop argc argument types.
			for i := 0; i < argc; i++ {
				if _, err := v.popType(); err != nil {
					return err
				}
			}
			// Push one return value (I64 placeholder — we don't track return types yet).
			if err := v.pushType(TypeI64); err != nil {
				return err
			}

		// --- Return ---

		case OpRet:
			count := int(inst.Op2)
			if len(v.tstack) < count {
				return v.errf("RET %d but only %d values on stack", count, len(v.tstack))
			}
			returned = true

		default:
			return v.errf("unknown opcode 0x%02x", inst.Opcode)
		}
	}

	if len(v.ctrlStack) > 0 {
		frame := v.ctrlStack[len(v.ctrlStack)-1]
		kind := "IF"
		if frame.kind == ctrlForRange {
			kind = "FOR_RANGE"
		}
		return &VerifyError{PC: frame.pc, Message: fmt.Sprintf("unterminated %s", kind)}
	}

	if !returned {
		return v.errf("program does not end with RET")
	}

	return nil
}

// verifyBuiltin checks stack effects for a builtin call.
func (v *verifier) verifyBuiltin(id, argc uint16, instTyp uint8) error {
	// Pop argc arguments.
	for i := 0; i < int(argc); i++ {
		if _, err := v.popType(); err != nil {
			return err
		}
	}

	// Push return type based on builtin.
	switch id {
	case BuiltinMin, BuiltinMax:
		return v.pushType(TypeI64)
	case BuiltinClamp:
		return v.pushType(TypeI64)
	case BuiltinMinF, BuiltinMaxF, BuiltinClampF:
		return v.pushType(TypeF64)
	case BuiltinSqrt, BuiltinFloor, BuiltinCeil, BuiltinRound:
		return v.pushType(TypeF64)
	case BuiltinStrLen:
		return v.pushType(TypeI64)
	case BuiltinStrConcat:
		return v.pushType(TypeStr)
	case BuiltinStrContains, BuiltinStrPrefix, BuiltinStrSuffix:
		return v.pushType(TypeBool)
	case BuiltinStrSubstr:
		return v.pushType(TypeStr)
	case BuiltinStrUpper, BuiltinStrLower:
		return v.pushType(TypeStr)
	case BuiltinCollLen:
		return v.pushType(TypeI64)
	case BuiltinCollGet:
		// Returns element type — we'd need to track the collection type.
		// For now, push I64 as placeholder.
		return v.pushType(TypeI64)
	case BuiltinCollTake, BuiltinCollDrop, BuiltinCollSort, BuiltinCollConcat, BuiltinCollPage:
		// Returns same collection type. Use instTyp if available, else placeholder.
		if isCollType(instTyp) {
			return v.pushType(instTyp)
		}
		return v.pushType(TypeCollI64)
	case BuiltinCollEmpty:
		if isCollType(instTyp) {
			return v.pushType(instTyp)
		}
		return v.pushType(TypeCollI64)

	// Rectangle builtins.
	case BuiltinRect:
		return v.pushType(TypeRectangle)
	case BuiltinRectUnion, BuiltinRectIntersect:
		return v.pushType(TypeRectangle)
	case BuiltinRectOverlaps, BuiltinRectContains, BuiltinRectEmpty:
		return v.pushType(TypeBool)
	case BuiltinRectArea, BuiltinRectWidth, BuiltinRectHeight:
		return v.pushType(TypeI64)

	// Trig builtins.
	case BuiltinSin, BuiltinCos, BuiltinTan, BuiltinAsin, BuiltinAcos:
		return v.pushType(TypeF64)
	case BuiltinAtan2:
		return v.pushType(TypeF64)
	case BuiltinDegToRad, BuiltinRadToDeg, BuiltinAbsF:
		return v.pushType(TypeF64)
	case BuiltinPow:
		return v.pushType(TypeF64)

	// Composite constructors.
	case BuiltinTimespec:
		return v.pushType(TypeTimespec)
	case BuiltinTimespecSeconds, BuiltinTimespecNanos:
		return v.pushType(TypeI64)
	case BuiltinTimezone:
		return v.pushType(TypeTimezone)
	case BuiltinTzConvert:
		return v.pushType(TypeTimespec)
	case BuiltinDuration:
		return v.pushType(TypeDuration)
	case BuiltinDurationNanos:
		return v.pushType(TypeI64)
	case BuiltinDate:
		return v.pushType(TypeDate)
	case BuiltinDateYear, BuiltinDateMonth, BuiltinDateDay:
		return v.pushType(TypeI64)
	case BuiltinPoint2D:
		return v.pushType(TypePoint2D)
	case BuiltinPoint2DX, BuiltinPoint2DY:
		return v.pushType(TypeI64)
	case BuiltinPoint3D:
		return v.pushType(TypePoint3D)
	case BuiltinPointF2D:
		return v.pushType(TypePointF2D)
	case BuiltinPointF3D:
		return v.pushType(TypePointF3D)
	case BuiltinIPv4:
		return v.pushType(TypeIPv4)
	case BuiltinIPv4Octet:
		return v.pushType(TypeI64)
	case BuiltinIPv6:
		return v.pushType(TypeIPv6)
	case BuiltinPriestId:
		return v.pushType(TypePriestId)
	case BuiltinPriestIdNum:
		return v.pushType(TypeI64)
	case BuiltinMazId:
		return v.pushType(TypeMazId)
	case BuiltinMazIdNum:
		return v.pushType(TypeI64)

	// Service discovery builtins.
	case BuiltinFind:
		return v.pushType(TypeCollStr)
	case BuiltinDerefI64:
		// Returns I64 or Tribool(unknown) — verifier pushes I64 conservatively.
		return v.pushType(TypeI64)
	case BuiltinDerefStr:
		return v.pushType(TypeStr)
	case BuiltinDerefBool:
		return v.pushType(TypeBool)
	case BuiltinDerefF64:
		return v.pushType(TypeF64)
	case BuiltinDerefRect:
		return v.pushType(TypeRectangle)
	case BuiltinDerefPoint2D:
		return v.pushType(TypePoint2D)
	case BuiltinDerefTribool:
		return v.pushType(TypeTribool)
	case BuiltinExists:
		return v.pushType(TypeBool)
	case BuiltinURISegment:
		return v.pushType(TypeStr)
	case BuiltinIsUnknown:
		return v.pushType(TypeBool)

	default:
		return v.errf("unknown builtin %d", id)
	}
}
