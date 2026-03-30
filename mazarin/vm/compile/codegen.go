package compile

import (
	"go/ast"
	"go/token"
	"strconv"

	"mazzy/mazarin/vm"
)

// builtinFuncs maps Go function names to VM builtin IDs and expected arg counts.
var builtinFuncs = map[string]struct {
	id   uint16
	argc uint16
}{
	"min":          {vm.BuiltinMin, 2},
	"max":          {vm.BuiltinMax, 2},
	"clamp":        {vm.BuiltinClamp, 3},
	"minf":         {vm.BuiltinMinF, 2},
	"maxf":         {vm.BuiltinMaxF, 2},
	"clampf":       {vm.BuiltinClampF, 3},
	"sqrt":         {vm.BuiltinSqrt, 1},
	"floor":        {vm.BuiltinFloor, 1},
	"ceil":         {vm.BuiltinCeil, 1},
	"round":        {vm.BuiltinRound, 1},
	"strLen":      {vm.BuiltinStrLen, 1},
	"strConcat":   {vm.BuiltinStrConcat, 2},
	"strContains": {vm.BuiltinStrContains, 2},
	"strSubstr":   {vm.BuiltinStrSubstr, 3},
	"strPrefix":   {vm.BuiltinStrPrefix, 2},
	"strSuffix":   {vm.BuiltinStrSuffix, 2},
	"strUpper":    {vm.BuiltinStrUpper, 1},
	"strLower":    {vm.BuiltinStrLower, 1},
	"collLen":     {vm.BuiltinCollLen, 1},
	"collGet":     {vm.BuiltinCollGet, 2},
	"collTake":    {vm.BuiltinCollTake, 2},
	"collDrop":    {vm.BuiltinCollDrop, 2},
	"collSort":    {vm.BuiltinCollSort, 1},
	"collConcat":  {vm.BuiltinCollConcat, 2},
	"collPage":    {vm.BuiltinCollPage, 3},
	"collEmpty":   {vm.BuiltinCollEmpty, 0},
	"collPush":    {vm.BuiltinCollPush, 2},
	"len":         {vm.BuiltinCollLen, 1}, // Go's len() maps to collLen

	// Geometry builtins.
	"rect":           {vm.BuiltinRect, 4},
	"rectUnion":     {vm.BuiltinRectUnion, 2},
	"rectIntersect": {vm.BuiltinRectIntersect, 2},
	"rectOverlaps":  {vm.BuiltinRectOverlaps, 2},
	"rectContains":  {vm.BuiltinRectContains, 2},
	"rectEmpty":     {vm.BuiltinRectEmpty, 1},
	"rectArea":      {vm.BuiltinRectArea, 1},
	"rectWidth":     {vm.BuiltinRectWidth, 1},
	"rectHeight":    {vm.BuiltinRectHeight, 1},
	"point2d":        {vm.BuiltinPoint2D, 2},
	"point2dX":      {vm.BuiltinPoint2DX, 1},
	"point2dY":      {vm.BuiltinPoint2DY, 1},

	// Service discovery / deref builtins.
	"find":           {vm.BuiltinFind, 1},
	"findWhere":     {vm.BuiltinFindWhere, 2},
	"derefI64":      {vm.BuiltinDerefI64, 1},
	"derefStr":      {vm.BuiltinDerefStr, 1},
	"derefBool":     {vm.BuiltinDerefBool, 1},
	"derefF64":      {vm.BuiltinDerefF64, 1},
	"derefRect":     {vm.BuiltinDerefRect, 1},
	"derefPoint2d":  {vm.BuiltinDerefPoint2D, 1},
	"derefTribool":  {vm.BuiltinDerefTribool, 1},
	"exists":         {vm.BuiltinExists, 1},
	"uriSegment":       {vm.BuiltinURISegment, 2},
	"isUnknown":        {vm.BuiltinIsUnknown, 1},
	"incrementAtomic":  {vm.BuiltinIncrementAtomic, 1},
}

// compileFn compiles the function body to bytecode.
func (c *compiler) compileFn() error {
	fn := c.fnDecl

	// Allocate locals for parameters. They arrive on the stack
	// in order, so we pop them in reverse and store to locals.
	if fn.Type.Params != nil {
		// First pass: allocate slots.
		var paramNames []string
		for _, field := range fn.Type.Params.List {
			for _, name := range field.Names {
				c.allocLocal(name.Name)
				paramNames = append(paramNames, name.Name)
			}
		}
		// Emit stores in reverse order (stack is LIFO).
		for i := len(paramNames) - 1; i >= 0; i-- {
			slot := c.locals[paramNames[i]]
			c.emit(vm.InstStore(slot))
		}
	}

	// Count return values.
	if fn.Type.Results != nil {
		c.numReturns = 0
		for _, field := range fn.Type.Results.List {
			if len(field.Names) == 0 {
				c.numReturns++
			} else {
				c.numReturns += len(field.Names)
			}
		}
	}

	// Compile body.
	return c.compileBlock(fn.Body)
}

func (c *compiler) compileBlock(block *ast.BlockStmt) error {
	for _, stmt := range block.List {
		if err := c.compileStmt(stmt); err != nil {
			return err
		}
	}
	return nil
}

func (c *compiler) compileStmt(stmt ast.Stmt) error {
	switch s := stmt.(type) {
	case *ast.ReturnStmt:
		for _, expr := range s.Results {
			if err := c.compileExpr(expr); err != nil {
				return err
			}
		}
		c.emit(vm.InstRet(uint16(len(s.Results))))
		return nil

	case *ast.AssignStmt:
		if len(s.Lhs) != len(s.Rhs) {
			return c.errAt(s.Pos(), "multi-value assignment not supported (use single assignments)")
		}
		for i, rhs := range s.Rhs {
			if err := c.compileExpr(rhs); err != nil {
				return err
			}
			name := s.Lhs[i].(*ast.Ident).Name
			slot := c.allocLocal(name)
			c.emit(vm.InstStore(slot))
		}
		return nil

	case *ast.IfStmt:
		return c.compileIf(s)

	case *ast.RangeStmt:
		return c.compileForRange(s)

	case *ast.BranchStmt:
		if s.Tok == token.BREAK {
			c.emit(vm.InstBreak())
			return nil
		}
		if s.Tok == token.CONTINUE {
			c.emit(vm.InstContinue())
			return nil
		}
		return c.errAt(s.Pos(), "unsupported branch statement: %s", s.Tok)

	case *ast.ExprStmt:
		// Expression statements (like function calls) — compile and discard result.
		return c.compileExpr(s.X)

	case *ast.IncDecStmt:
		// i++ → i = i + 1
		name := s.X.(*ast.Ident).Name
		slot, ok := c.lookupLocal(name)
		if !ok {
			return c.errAt(s.Pos(), "undefined variable %s", name)
		}
		c.emit(vm.InstLoad(slot))
		c.emit(vm.InstConstI64(1))
		if s.Tok == token.INC {
			c.emit(vm.InstArith(vm.OpAdd, vm.TypeI64))
		} else {
			c.emit(vm.InstArith(vm.OpSub, vm.TypeI64))
		}
		c.emit(vm.InstStore(slot))
		return nil

	case *ast.BlockStmt:
		return c.compileBlock(s)

	default:
		return c.errAt(stmt.Pos(), "unsupported statement %T", stmt)
	}
}

func (c *compiler) compileIf(s *ast.IfStmt) error {
	if err := c.compileExpr(s.Cond); err != nil {
		return err
	}
	c.emit(vm.InstIf())

	if err := c.compileBlock(s.Body); err != nil {
		return err
	}

	if s.Else != nil {
		c.emit(vm.InstElse())
		switch e := s.Else.(type) {
		case *ast.BlockStmt:
			if err := c.compileBlock(e); err != nil {
				return err
			}
		case *ast.IfStmt:
			// else-if: compile as nested if.
			if err := c.compileIf(e); err != nil {
				return err
			}
		}
	}

	c.emit(vm.InstEndIf())
	return nil
}

func (c *compiler) compileForRange(s *ast.RangeStmt) error {
	// Compile the collection expression.
	if err := c.compileExpr(s.X); err != nil {
		return err
	}

	// Determine index and value local slots.
	var idxSlot, valSlot uint16
	if s.Key != nil {
		idxSlot = c.allocLocal(s.Key.(*ast.Ident).Name)
	} else {
		idxSlot = c.allocLocal("_range_idx")
	}
	if s.Value != nil {
		valSlot = c.allocLocal(s.Value.(*ast.Ident).Name)
	} else {
		valSlot = c.allocLocal("_range_val")
	}

	c.emit(vm.InstForRange(idxSlot, valSlot))
	if err := c.compileBlock(s.Body); err != nil {
		return err
	}
	c.emit(vm.InstEndFor())
	return nil
}

func (c *compiler) compileExpr(expr ast.Expr) error {
	switch e := expr.(type) {
	case *ast.BasicLit:
		return c.compileLit(e)

	case *ast.Ident:
		if e.Name == "true" {
			c.emit(vm.InstConstBool(true))
			return nil
		}
		if e.Name == "false" {
			c.emit(vm.InstConstBool(false))
			return nil
		}
		slot, ok := c.lookupLocal(e.Name)
		if !ok {
			return c.errAt(e.Pos(), "undefined variable %q", e.Name)
		}
		c.emit(vm.InstLoad(slot))
		return nil

	case *ast.BinaryExpr:
		return c.compileBinary(e)

	case *ast.UnaryExpr:
		return c.compileUnary(e)

	case *ast.ParenExpr:
		return c.compileExpr(e.X)

	case *ast.CallExpr:
		return c.compileCall(e)

	case *ast.IndexExpr:
		// coll[idx] → push coll, push idx, CALL_BUILTIN collGet
		if err := c.compileExpr(e.X); err != nil {
			return err
		}
		if err := c.compileExpr(e.Index); err != nil {
			return err
		}
		c.emit(vm.InstCallBuiltin(vm.BuiltinCollGet, 2))
		return nil

	default:
		return c.errAt(expr.Pos(), "unsupported expression %T", expr)
	}
}

func (c *compiler) compileLit(lit *ast.BasicLit) error {
	switch lit.Kind {
	case token.INT:
		v, err := strconv.ParseInt(lit.Value, 0, 64)
		if err != nil {
			return c.errAt(lit.Pos(), "invalid integer literal %q", lit.Value)
		}
		c.emit(vm.InstConstI64(v))
		return nil

	case token.FLOAT:
		v, err := strconv.ParseFloat(lit.Value, 64)
		if err != nil {
			return c.errAt(lit.Pos(), "invalid float literal %q", lit.Value)
		}
		c.emit(vm.InstConstF64(v))
		return nil

	case token.STRING:
		v, err := strconv.Unquote(lit.Value)
		if err != nil {
			return c.errAt(lit.Pos(), "invalid string literal %q", lit.Value)
		}
		idx := c.addString(v)
		c.emit(vm.Inst{Opcode: vm.OpConstStr, Op1: idx})
		return nil

	default:
		return c.errAt(lit.Pos(), "unsupported literal kind %s", lit.Kind)
	}
}

func (c *compiler) compileBinary(e *ast.BinaryExpr) error {
	if err := c.compileExpr(e.X); err != nil {
		return err
	}
	if err := c.compileExpr(e.Y); err != nil {
		return err
	}

	// Determine operand type from the type checker.
	typ := c.exprVMType(e.X)

	switch e.Op {
	case token.ADD:
		if typ == vm.TypeStr {
			// String concatenation.
			c.emit(vm.InstCallBuiltin(vm.BuiltinStrConcat, 2))
		} else {
			c.emit(vm.InstArith(vm.OpAdd, typ))
		}
	case token.SUB:
		c.emit(vm.InstArith(vm.OpSub, typ))
	case token.MUL:
		c.emit(vm.InstArith(vm.OpMul, typ))
	case token.QUO:
		c.emit(vm.InstArith(vm.OpDiv, typ))
	case token.REM:
		c.emit(vm.InstArith(vm.OpMod, vm.TypeI64))

	case token.EQL:
		c.emit(vm.InstCmp(vm.OpEq, typ))
	case token.NEQ:
		c.emit(vm.InstCmp(vm.OpNeq, typ))
	case token.LSS:
		c.emit(vm.InstCmp(vm.OpLt, typ))
	case token.GTR:
		c.emit(vm.InstCmp(vm.OpGt, typ))
	case token.LEQ:
		c.emit(vm.InstCmp(vm.OpLe, typ))
	case token.GEQ:
		c.emit(vm.InstCmp(vm.OpGe, typ))

	case token.LAND:
		c.emit(vm.Inst{Opcode: vm.OpAnd})
	case token.LOR:
		c.emit(vm.Inst{Opcode: vm.OpOr})

	default:
		return c.errAt(e.Pos(), "unsupported operator %s", e.Op)
	}
	return nil
}

func (c *compiler) compileUnary(e *ast.UnaryExpr) error {
	if err := c.compileExpr(e.X); err != nil {
		return err
	}
	typ := c.exprVMType(e.X)
	switch e.Op {
	case token.SUB: // negation
		c.emit(vm.InstArith(vm.OpNeg, typ))
	case token.NOT:
		c.emit(vm.Inst{Opcode: vm.OpNot})
	default:
		return c.errAt(e.Pos(), "unsupported unary operator %s", e.Op)
	}
	return nil
}

func (c *compiler) compileCall(e *ast.CallExpr) error {
	ident, ok := e.Fun.(*ast.Ident)
	if !ok {
		return c.errAt(e.Pos(), "only direct function calls allowed")
	}

	// Type conversions.
	if ident.Name == "int64" {
		if len(e.Args) != 1 {
			return c.errAt(e.Pos(), "int64() takes exactly 1 argument")
		}
		if err := c.compileExpr(e.Args[0]); err != nil {
			return err
		}
		srcType := c.exprVMType(e.Args[0])
		if srcType == vm.TypeF64 {
			c.emit(vm.Inst{Opcode: vm.OpF64ToI64})
		}
		// int64(int64) and int64(int32) are no-ops (VM only has int64).
		return nil
	}
	if ident.Name == "int32" {
		// int32(x) is a no-op in the VM — screen coordinates fit in int32.
		// The rect() builtin handles the int64→int32 truncation internally.
		if len(e.Args) != 1 {
			return c.errAt(e.Pos(), "int32() takes exactly 1 argument")
		}
		return c.compileExpr(e.Args[0])
	}
	if ident.Name == "float64" {
		if len(e.Args) != 1 {
			return c.errAt(e.Pos(), "float64() takes exactly 1 argument")
		}
		if err := c.compileExpr(e.Args[0]); err != nil {
			return err
		}
		srcType := c.exprVMType(e.Args[0])
		if srcType == vm.TypeI64 {
			c.emit(vm.Inst{Opcode: vm.OpI64ToF64})
		}
		return nil
	}

	// len() needs special handling — it could be string or collection.
	if ident.Name == "len" {
		if len(e.Args) != 1 {
			return c.errAt(e.Pos(), "len() takes exactly 1 argument")
		}
		if err := c.compileExpr(e.Args[0]); err != nil {
			return err
		}
		argType := c.exprVMType(e.Args[0])
		if argType == vm.TypeStr {
			c.emit(vm.InstCallBuiltin(vm.BuiltinStrLen, 1))
		} else {
			c.emit(vm.InstCallBuiltin(vm.BuiltinCollLen, 1))
		}
		return nil
	}

	// Intra-program function call.
	if funcIdx, ok := c.funcTable[ident.Name]; ok {
		// Compile arguments.
		for _, arg := range e.Args {
			if err := c.compileExpr(arg); err != nil {
				return err
			}
		}
		c.emit(vm.InstCall(uint16(funcIdx), uint16(len(e.Args))))
		return nil
	}

	builtin, ok := builtinFuncs[ident.Name]
	if !ok {
		return c.errAt(e.Pos(), "unknown function %q", ident.Name)
	}

	if uint16(len(e.Args)) != builtin.argc {
		return c.errAt(e.Pos(), "%s() takes %d arguments, got %d", ident.Name, builtin.argc, len(e.Args))
	}

	// Compile arguments.
	for _, arg := range e.Args {
		if err := c.compileExpr(arg); err != nil {
			return err
		}
	}

	inst := vm.InstCallBuiltin(builtin.id, builtin.argc)
	// collEmpty needs the Typ field set so the runtime knows which
	// collection type to create (e.g. TypeCollStr vs TypeCollI64).
	if builtin.id == vm.BuiltinCollEmpty {
		inst.Typ = c.exprVMType(e)
	}
	c.emit(inst)
	return nil
}

// exprVMType returns the VM type tag for an expression, using the type checker.
func (c *compiler) exprVMType(expr ast.Expr) uint8 {
	if tv, ok := c.info.Types[expr]; ok {
		if t, ok := goTypeToVMType(tv.Type); ok {
			return t
		}
	}
	// Default to I64 for untyped constants that will become int64.
	return vm.TypeI64
}

