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
		"_rectPrefix_", RectPrefix(),
		"_damageRectSuffix_", LayoutDamageRect.Suffix(),
	)
	return BindStrings(prog, bindings...)
}

// BindStringsParent is BindStrings plus automatic binding of the placeholders
// required by the "parent" vgo library. The caller must supply the parent
// interactor's constraint-system name as parentSeg.
func BindStringsParent(prog *vm.Program, parentSeg string, bindings ...string) *vm.Program {
	bindings = append(bindings,
		"_int64Prefix_", Int64Prefix(),
		"_parentSeg_", parentSeg,
		"_xSuffix_", LayoutX.Suffix(),
		"_ySuffix_", LayoutY.Suffix(),
		"_widthSuffix_", LayoutWidth.Suffix(),
		"_heightSuffix_", LayoutHeight.Suffix(),
	)
	return BindStrings(prog, bindings...)
}

// BindStringsSibling is BindStrings plus automatic binding of the placeholders
// required by the "sibling" vgo library. The caller must supply the parent
// interactor's constraint-system name as parentName (the value stored in each
// child's Parent layout attribute).
func BindStringsSibling(prog *vm.Program, parentName string, bindings ...string) *vm.Program {
	bindings = append(bindings,
		"_childPattern_", ChildPattern(),
		"_parentName_", parentName,
		"_int64Prefix_", Int64Prefix(),
		"_boolPrefix_", BoolPrefix(),
		"_xSuffix_", LayoutX.Suffix(),
		"_ySuffix_", LayoutY.Suffix(),
		"_widthSuffix_", LayoutWidth.Suffix(),
		"_heightSuffix_", LayoutHeight.Suffix(),
		"_visSuffix_", VisSuffix,
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

// EqualI64 returns a constraint program that mirrors an int64 source attribute.
// The returned program reads sourceURI and returns its value unchanged.
func EqualI64(sourceURI string) *vm.Program {
	return BindStrings(ProgIdentityI64, "_source_", sourceURI)
}

// EqualBool returns a constraint program that mirrors a bool source attribute.
// The returned program reads sourceURI and returns its value unchanged.
func EqualBool(sourceURI string) *vm.Program {
	return BindStrings(ProgIdentityBool, "_source_", sourceURI)
}

// EqualStr returns a constraint program that mirrors a string source attribute.
// The returned program reads sourceURI and returns its value unchanged.
func EqualStr(sourceURI string) *vm.Program {
	return BindStrings(ProgIdentityStr, "_source_", sourceURI)
}
