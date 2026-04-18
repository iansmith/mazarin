package mazdl

import "fmt"

// mazdlError is the error type returned from all mazdl public APIs.
// MVP uses a plain struct rather than mazarin/error's allocation-free
// ErrorCode system because the smoke-test host runs on stock Linux and
// doesn't pull in the rest of mazarin/*. When mazdl is wired into the
// shepherd the error story will switch to *merror.Error — the shape of
// callers (if err != nil) stays the same.
type mazdlError struct {
	op   string // "Open", "Sym", "RegisterHost", ...
	path string // file path or soname context; empty if irrelevant
	name string // symbol name context; empty if irrelevant
	msg  string
}

func (e *mazdlError) Error() string {
	switch {
	case e.path != "" && e.name != "":
		return fmt.Sprintf("mazdl: %s(%s, %s): %s", e.op, e.path, e.name, e.msg)
	case e.path != "":
		return fmt.Sprintf("mazdl: %s(%s): %s", e.op, e.path, e.msg)
	case e.name != "":
		return fmt.Sprintf("mazdl: %s(%s): %s", e.op, e.name, e.msg)
	default:
		return fmt.Sprintf("mazdl: %s: %s", e.op, e.msg)
	}
}

// errorf is the shorthand Open/resolve/etc use to produce an error.
func errorf(op, path, name, format string, args ...any) error {
	return &mazdlError{op: op, path: path, name: name, msg: fmt.Sprintf(format, args...)}
}
