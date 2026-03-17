package compile

import (
	"go/ast"
	"go/token"

	"mazzy/mazarin/vm"
)

// checkRestrictions walks the AST and rejects disallowed constructs.
func (c *compiler) checkRestrictions() error {
	fn := c.fnDecl

	// Must not have receiver (no methods).
	if fn.Recv != nil {
		return c.errAt(fn.Pos(), "methods not allowed")
	}

	// Check parameter types.
	if fn.Type.Params != nil {
		for _, field := range fn.Type.Params.List {
			if _, ok := c.resolveTypeExpr(field.Type); !ok {
				return c.errAt(field.Type.Pos(), "unsupported parameter type %s", fieldTypeString(field.Type))
			}
		}
	}

	// Check return types.
	if fn.Type.Results != nil {
		for _, field := range fn.Type.Results.List {
			if _, ok := c.resolveTypeExpr(field.Type); !ok {
				return c.errAt(field.Type.Pos(), "unsupported return type %s", fieldTypeString(field.Type))
			}
		}
	}

	// Walk the body.
	return c.checkStmtList(fn.Body.List)
}

func (c *compiler) checkStmtList(stmts []ast.Stmt) error {
	for _, stmt := range stmts {
		if err := c.checkStmt(stmt); err != nil {
			return err
		}
	}
	return nil
}

func (c *compiler) checkStmt(stmt ast.Stmt) error {
	switch s := stmt.(type) {
	case *ast.ReturnStmt:
		for _, expr := range s.Results {
			if err := c.checkExpr(expr); err != nil {
				return err
			}
		}
		return nil

	case *ast.AssignStmt:
		if s.Tok != token.DEFINE && s.Tok != token.ASSIGN {
			return c.errAt(s.Pos(), "only := and = assignments allowed")
		}
		for _, lhs := range s.Lhs {
			if _, ok := lhs.(*ast.Ident); !ok {
				return c.errAt(lhs.Pos(), "only simple variable assignments allowed")
			}
		}
		for _, rhs := range s.Rhs {
			if err := c.checkExpr(rhs); err != nil {
				return err
			}
		}
		return nil

	case *ast.IfStmt:
		if s.Init != nil {
			return c.errAt(s.Init.Pos(), "if-init statements not allowed")
		}
		if err := c.checkExpr(s.Cond); err != nil {
			return err
		}
		if err := c.checkStmtList(s.Body.List); err != nil {
			return err
		}
		if s.Else != nil {
			switch e := s.Else.(type) {
			case *ast.BlockStmt:
				return c.checkStmtList(e.List)
			case *ast.IfStmt:
				return c.checkStmt(e) // else-if chain
			default:
				return c.errAt(s.Else.Pos(), "unsupported else form")
			}
		}
		return nil

	case *ast.RangeStmt:
		if s.Key != nil {
			if _, ok := s.Key.(*ast.Ident); !ok {
				return c.errAt(s.Key.Pos(), "range key must be a simple variable")
			}
		}
		if s.Value != nil {
			if _, ok := s.Value.(*ast.Ident); !ok {
				return c.errAt(s.Value.Pos(), "range value must be a simple variable")
			}
		}
		if err := c.checkExpr(s.X); err != nil {
			return err
		}
		return c.checkStmtList(s.Body.List)

	case *ast.BranchStmt:
		if s.Tok == token.BREAK {
			return nil
		}
		return c.errAt(s.Pos(), "%s not allowed (only break is permitted)", s.Tok)

	case *ast.ExprStmt:
		return c.checkExpr(s.X)

	case *ast.BlockStmt:
		return c.checkStmtList(s.List)

	// Rejected constructs.
	case *ast.ForStmt:
		return c.errAt(s.Pos(), "for-loops not allowed (use for-range over collections)")
	case *ast.GoStmt:
		return c.errAt(s.Pos(), "goroutines not allowed")
	case *ast.SendStmt:
		return c.errAt(s.Pos(), "channel sends not allowed")
	case *ast.SelectStmt:
		return c.errAt(s.Pos(), "select not allowed")
	case *ast.DeferStmt:
		return c.errAt(s.Pos(), "defer not allowed")
	case *ast.SwitchStmt:
		return c.errAt(s.Pos(), "switch not allowed (use if/else)")
	case *ast.TypeSwitchStmt:
		return c.errAt(s.Pos(), "type switch not allowed")
	case *ast.LabeledStmt:
		return c.errAt(s.Pos(), "labels not allowed")
	case *ast.IncDecStmt:
		// Allow i++ and i-- as sugar.
		if _, ok := s.X.(*ast.Ident); !ok {
			return c.errAt(s.Pos(), "increment/decrement only on simple variables")
		}
		return nil
	default:
		return c.errAt(stmt.Pos(), "unsupported statement type %T", stmt)
	}
}

func (c *compiler) checkExpr(expr ast.Expr) error {
	switch e := expr.(type) {
	case *ast.BasicLit:
		return nil
	case *ast.Ident:
		return nil
	case *ast.BinaryExpr:
		if err := c.checkExpr(e.X); err != nil {
			return err
		}
		return c.checkExpr(e.Y)
	case *ast.UnaryExpr:
		if e.Op == token.AND {
			return c.errAt(e.Pos(), "address-of not allowed")
		}
		return c.checkExpr(e.X)
	case *ast.ParenExpr:
		return c.checkExpr(e.X)
	case *ast.CallExpr:
		for _, arg := range e.Args {
			if err := c.checkExpr(arg); err != nil {
				return err
			}
		}
		// Check that the function is a known builtin or intra-program function.
		if ident, ok := e.Fun.(*ast.Ident); ok {
			if _, ok := builtinFuncs[ident.Name]; ok {
				return nil
			}
			// Type conversions: int64(), int32(), float64(), bool().
			if ident.Name == "int64" || ident.Name == "int32" || ident.Name == "float64" || ident.Name == "bool" {
				return nil
			}
			// Intra-program function calls.
			if _, ok := c.funcTable[ident.Name]; ok {
				return nil
			}
			return c.errAt(e.Pos(), "unknown function %q (only builtins and local functions allowed)", ident.Name)
		}
		return c.errAt(e.Pos(), "only direct function calls allowed (no method calls, no closures)")

	case *ast.IndexExpr:
		// collection[index] — allowed.
		if err := c.checkExpr(e.X); err != nil {
			return err
		}
		return c.checkExpr(e.Index)

	// Rejected.
	case *ast.FuncLit:
		return c.errAt(e.Pos(), "closures/anonymous functions not allowed")
	case *ast.CompositeLit:
		return c.errAt(e.Pos(), "composite literals not allowed (use builtin functions)")
	case *ast.SelectorExpr:
		return c.errAt(e.Pos(), "field access/method calls not allowed")
	case *ast.SliceExpr:
		return c.errAt(e.Pos(), "slice expressions not allowed (use take/drop)")
	case *ast.StarExpr:
		return c.errAt(e.Pos(), "pointer dereference not allowed")
	case *ast.TypeAssertExpr:
		return c.errAt(e.Pos(), "type assertions not allowed")

	default:
		return c.errAt(expr.Pos(), "unsupported expression type %T", expr)
	}
}

// resolveTypeExpr maps a type AST node to a VM type tag.
func (c *compiler) resolveTypeExpr(expr ast.Expr) (uint8, bool) {
	switch e := expr.(type) {
	case *ast.Ident:
		switch e.Name {
		case "int64":
			return vm.TypeI64, true
		case "float64":
			return vm.TypeF64, true
		case "bool":
			return vm.TypeBool, true
		case "string":
			return vm.TypeStr, true
		case "Rect":
			return vm.TypeRectangle, true
		case "Point2D":
			return vm.TypePoint2D, true
		}
	case *ast.ArrayType:
		if e.Len != nil {
			return 0, false // arrays not allowed, only slices
		}
		if ident, ok := e.Elt.(*ast.Ident); ok {
			switch ident.Name {
			case "int64":
				return vm.TypeCollI64, true
			case "float64":
				return vm.TypeCollF64, true
			case "bool":
				return vm.TypeCollBool, true
			case "string":
				return vm.TypeCollStr, true
			}
		}
	}
	return 0, false
}

func fieldTypeString(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.ArrayType:
		return "[]" + fieldTypeString(e.Elt)
	default:
		return "<complex type>"
	}
}
