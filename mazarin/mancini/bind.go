package mancini

import "mazzy/mazarin/vm"

// BindStrings returns a copy of prog with placeholder strings replaced by the
// given bindings. Bindings are alternating name-value pairs:
//
//	BindStrings(prog, "_maxWidth_", maxWidthURI, "_spacing_", spacingURI, ...)
//
// Any string in the program's string table that matches a placeholder name
// (underscore-prefixed and suffixed, e.g. "_maxWidth_") is replaced with the
// corresponding value. Non-placeholder strings are preserved.
func BindStrings(prog *vm.Program, bindings ...string) *vm.Program {
	if len(bindings)%2 != 0 {
		panic("BindStrings: odd number of arguments; expected alternating name-value pairs")
	}
	m := make(map[string]string, len(bindings)/2)
	for i := 0; i < len(bindings); i += 2 {
		m[bindings[i]] = bindings[i+1]
	}
	p := *prog
	newStrings := make([]string, len(prog.Strings))
	for i, s := range prog.Strings {
		if isPlaceholder(s) {
			if val, ok := m[s]; ok {
				newStrings[i] = val
			} else {
				newStrings[i] = s
			}
		} else {
			newStrings[i] = s
		}
	}
	p.Strings = newStrings
	return &p
}

// BindStringsChildren is BindStrings plus automatic binding of the
// placeholders required by the "children" vgo library:
// child discovery (_childPattern_), type prefixes (_boolPrefix_,
// _int64Prefix_), and layout property suffixes (_xSuffix_, _ySuffix_,
// _widthSuffix_, _heightSuffix_, _visSuffix_, _boundsHashSuffix_).
func BindStringsChildren(prog *vm.Program, bindings ...string) *vm.Program {
	bindings = append(bindings,
		"_childPattern_", ChildPattern(),
		"_boolPrefix_", BoolPrefix(),
		"_int64Prefix_", Int64Prefix(),
		"_xSuffix_", LayoutX.Suffix(),
		"_ySuffix_", LayoutY.Suffix(),
		"_widthSuffix_", LayoutWidth.Suffix(),
		"_heightSuffix_", LayoutHeight.Suffix(),
		"_visSuffix_", VisSuffix,
		"_boundsHashSuffix_", LayoutBoundsHash.Suffix(),
	)
	return BindStrings(prog, bindings...)
}

// isPlaceholder returns true if s matches _name_ where name is one or more
// non-underscore characters bracketed by underscores (e.g. "_maxWidth_",
// "_childPattern_").
func isPlaceholder(s string) bool {
	if len(s) < 3 || s[0] != '_' || s[len(s)-1] != '_' {
		return false
	}
	for _, c := range s[1 : len(s)-1] {
		if c == '_' {
			return false
		}
	}
	return true
}
