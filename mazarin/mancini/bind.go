package mancini

import (
	"fmt"
	"sync/atomic"

	"mazzy/mazarin/attr"
	"mazzy/mazarin/vm"
)

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

// GreaterI64Bool returns a constraint program that evaluates `a > b`,
// where a and b are int64 source attributes. Used by scrollable
// containers (GridFrame, ConsoleFrame) to drive scrollbar visibility:
// `total > visible → scrollNeeded`.
func GreaterI64Bool(aURI, bURI string) *vm.Program {
	return BindStrings(ProgGreaterI64Bool, "_a_", aURI, "_b_", bURI)
}

// ThumbFracPermille returns a constraint program that computes the
// scrollbar thumb fraction in permille (0..1000) from a `visible`
// count and `total` count. Both URIs hold int64 attributes in the
// SAME UNITS (rows for row-based scrollers, pixels for pixel-based).
// Edge cases: total ≤ 0 or visible ≥ total → 1000 (full track);
// visible ≤ 0 → 0. Output binds to Scrollbar.ThumbFracPermilleAttr.
func ThumbFracPermille(visibleURI, totalURI string) *vm.Program {
	return BindStrings(ProgThumbFracPermille, "_visible_", visibleURI, "_total_", totalURI)
}

// NonnegSubI64 returns a constraint program that computes `max(a - b, 0)`,
// where a and b are int64 source attributes. Used for scrollbar Max
// values (`max(total - visible, 0)`) to ensure a sensible range even
// when visible momentarily exceeds total during a layout transition.
func NonnegSubI64(aURI, bURI string) *vm.Program {
	return BindStrings(ProgNonnegSub, "_a_", aURI, "_b_", bURI)
}

// EqualStr returns a constraint program that mirrors a string source attribute.
// The returned program reads sourceURI and returns its value unchanged.
func EqualStr(sourceURI string) *vm.Program {
	return BindStrings(ProgIdentityStr, "_source_", sourceURI)
}

// SubI64 returns a constraint program that computes aURI - bURI.
func SubI64(aURI, bURI string) *vm.Program {
	return BindStrings(ProgSubDeref, "_a_", aURI, "_b_", bURI)
}

// AddI64 returns a constraint program that computes aURI + bURI.
func AddI64(aURI, bURI string) *vm.Program {
	return AddSubI64(aURI, bURI, constI64(0))
}

// ChildAfterI64 returns a constraint program for the Y of a child stacked
// directly below the previous child with a 1-pixel gap:
//
//	result = prevYURI + prevHeightURI + 1
//
// Used by stacking layouts (e.g. ColumnEdgeToEdge) to keep each child's
// Y live when its predecessor's height changes.
func ChildAfterI64(prevYURI, prevHeightURI string) *vm.Program {
	// AddSubI64 computes a + b - c, so c = -1 yields the +1 gap.
	return AddSubI64(prevYURI, prevHeightURI, constI64(-1))
}

// AddSubI64 returns a constraint program that computes aURI + bURI - cURI.
func AddSubI64(aURI, bURI, cURI string) *vm.Program {
	return BindStrings(ProgAddSubDeref, "_a_", aURI, "_b_", bURI, "_c_", cURI)
}

// ScaleI64 returns a constraint program that computes
// deref(sourceURI) * deref(numeratorURI) / deref(denominatorURI).
// Division by zero returns 0.
func ScaleI64(sourceURI, numeratorURI, denominatorURI string) *vm.Program {
	return BindStrings(ProgScaleDeref,
		"_source_", sourceURI,
		"_numerator_", numeratorURI,
		"_denominator_", denominatorURI)
}

// MaxI64 returns a constraint program that computes max(sourceURI, floor).
func MaxI64(sourceURI string, floor int64) *vm.Program {
	floorURI := constI64(floor)
	return BindStrings(ProgMaxDeref, "_source_", sourceURI, "_floor_", floorURI)
}

// MinI64 returns a constraint program that computes min(sourceURI, ceiling).
func MinI64(sourceURI string, ceiling int64) *vm.Program {
	ceilingURI := constI64(ceiling)
	return BindStrings(ProgMinDeref, "_source_", sourceURI, "_ceiling_", ceilingURI)
}

// BetweenI64 returns a constraint program that clamps sourceURI to [lo, hi].
func BetweenI64(sourceURI string, lo, hi int64) *vm.Program {
	loURI := constI64(lo)
	hiURI := constI64(hi)
	return BindStrings(ProgBetweenDeref, "_source_", sourceURI, "_min_", loURI, "_max_", hiURI)
}

// bindConstSeq generates unique URIs for internal constant attributes.
var bindConstSeq atomic.Uint64

// constI64 creates an internal value attribute with the given constant
// and returns its URI. Used by MaxI64, MinI64, BetweenI64 so callers
// can pass plain int64 values instead of creating attributes manually.
func constI64(v int64) string {
	id := bindConstSeq.Add(1)
	uri := fmt.Sprintf("attr:///shepherd/%s/int64/bind_const_%d", manciniSID, id)
	attr.ValueI64(uri, v)
	return uri
}
