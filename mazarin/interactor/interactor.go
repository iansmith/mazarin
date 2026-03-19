package interactor

import (
	"mazzy/mazarin/attr"
	"mazzy/mazarin/vm"
	"mazzy/mazarin/vm/flat"
)

// Kind identifies the type of UI element.
type Kind uint8

const (
	KindWindow Kind = iota
	KindCard
	KindLabel
)

// Interactor represents a UI element in the constraint attribute namespace.
type Interactor struct {
	ID       string
	Kind     Kind
	Parent   *Interactor
	Children []*Interactor

	// Layout (constraints — read via Get)
	Width      *attr.Handle[int64]
	Height     *attr.Handle[int64]
	UpperLeft  *attr.Handle[vm.Value] // Point2D
	Bounds     *attr.Handle[vm.Value] // Rectangle
	Visible    *attr.Handle[bool]
	DamageRect *attr.Handle[vm.Value] // Rectangle

	// Visual state
	Content   *attr.Handle[string]
	BgColor   *attr.Handle[int64] // ARGB as int64
	TextColor *attr.Handle[int64] // ARGB as int64

	// Geometry value (Set-able)
	OriginPoint *attr.Handle[vm.Value] // Point2D

	// LastPainted mirrors (value attrs — Set by draw loop after painting)
	LPBounds    *attr.Handle[vm.Value] // Rectangle
	LPVisible   *attr.Handle[bool]
	LPContent   *attr.Handle[string]
	LPBgColor   *attr.Handle[int64]
	LPTextColor *attr.Handle[int64]

	// Private
	priestName string
	padding    int64
}

// Package-level state.
var (
	registry   map[string]*Interactor
	pName      string
	charWidthV int64
	charHeightV int64
	screenW    int64
	screenH    int64
)

// CharWidthURI is the kernel attribute for character width.
const CharWidthURI = "attr:///kernel/int64/screen/charWidth"

// CharHeightURI is the kernel attribute for character height.
const CharHeightURI = "attr:///kernel/int64/screen/charHeight"

// Init initializes the interactor library. Must be called after attr.Init().
func Init(name string) {
	pName = name
	registry = make(map[string]*Interactor)

	charWidthV = readKernelI64(CharWidthURI)
	charHeightV = readKernelI64(CharHeightURI)
	screenW = readKernelI64("attr:///kernel/int64/screen/width")
	screenH = readKernelI64("attr:///kernel/int64/screen/height")
}

// readKernelI64 reads a kernel int64 attribute using a deref constraint.
func readKernelI64(kernelURI string) int64 {
	prog := BindStrings(ProgIdentityI64, kernelURI)
	// Extract last path segment for a clean local URI (e.g. "charWidth" from
	// "attr:///kernel/int64/screen/charWidth").
	short := kernelURI
	for i := len(kernelURI) - 1; i >= 0; i-- {
		if kernelURI[i] == '/' {
			short = kernelURI[i+1:]
			break
		}
	}
	uri := "attr:///priest/" + pName + "/int64/_init/" + short
	h := attr.ConstraintI64(uri, prog)
	return h.Get()
}

func (i *Interactor) uri(typePath, attrName string) string {
	return "attr:///priest/" + i.priestName + "/" + typePath + "/" + i.ID + "/" + attrName
}

func register(i *Interactor) {
	registry[i.ID] = i
}

// BindStrings returns a copy of prog with placeholder strings replaced by the
// given bindings. Pre-compiled programs use placeholder strings ("_0_", "_1_",
// ...) which are replaced with actual URIs at constraint creation time.
// Non-placeholder strings (literals) are preserved at their original indices.
func BindStrings(prog *vm.Program, bindings ...string) *vm.Program {
	p := *prog
	newStrings := make([]string, len(prog.Strings))
	for i, s := range prog.Strings {
		if idx, ok := parsePlaceholder(s); ok && idx < len(bindings) {
			newStrings[i] = bindings[idx]
		} else {
			newStrings[i] = s
		}
	}
	p.Strings = newStrings
	return &p
}

// parsePlaceholder checks if s matches the pattern _N_ (e.g. "_0_", "_12_")
// and returns the numeric index.
func parsePlaceholder(s string) (int, bool) {
	if len(s) < 3 || s[0] != '_' || s[len(s)-1] != '_' {
		return 0, false
	}
	n := 0
	for _, c := range s[1 : len(s)-1] {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
	}
	return n, true
}

// emptyRect returns a vm.Value for an empty rectangle.
func emptyRect() vm.Value {
	return vm.RectangleVal(0, 0, 0, 0)
}

// zeroPoint returns a vm.Value for point (0,0).
func zeroPoint() vm.Value {
	return vm.Point2DVal(0, 0)
}

// makeValueAttrs creates the standard set of value attributes (lastPainted mirrors).
func (i *Interactor) makeValueAttrs(bgColor, textColor int64) {
	i.OriginPoint = attr.ValuePoint2D(i.uri("point2d", "originPoint"), zeroPoint())
	i.LPBounds = attr.ValueRectangle(i.uri("rect", "lpBounds"), emptyRect())
	i.LPVisible = attr.ValueBool(i.uri("bool", "lpVisible"), false)
	i.LPContent = attr.ValueStr(i.uri("str", "lpContent"), "")
	i.LPBgColor = attr.ValueI64(i.uri("int64", "lpBgColor"), 0)
	i.LPTextColor = attr.ValueI64(i.uri("int64", "lpTextColor"), 0)

	// Fixed-value attrs for deref-based parameterization.
	i.BgColor = attr.ValueI64(i.uri("int64", "bgColor"), bgColor)
	i.TextColor = attr.ValueI64(i.uri("int64", "textColor"), textColor)
}

// makeLeafDamage creates the damageRect constraint for leaf interactors.
func (i *Interactor) makeLeafDamage() {
	prog := BindStrings(ProgLeafDamageRect,
		i.uri("rect", "bounds"),
		i.uri("rect", "lpBounds"),
		i.uri("bool", "visible"),
		i.uri("bool", "lpVisible"),
		i.uri("str", "content"),
		i.uri("str", "lpContent"),
		i.uri("int64", "bgColor"),
		i.uri("int64", "lpBgColor"),
		i.uri("int64", "textColor"),
		i.uri("int64", "lpTextColor"),
	)
	i.DamageRect = attr.ConstraintComposite(i.uri("rect", "damageRect"),
		flat.TypeRectangle, prog)
}

// makeParentDamage creates the damageRect constraint for parent interactors.
func (i *Interactor) makeParentDamage(childDamageURI string) {
	prog := BindStrings(ProgParentDamageRect,
		i.uri("rect", "bounds"),
		i.uri("rect", "lpBounds"),
		i.uri("bool", "visible"),
		i.uri("bool", "lpVisible"),
		i.uri("int64", "bgColor"),
		i.uri("int64", "lpBgColor"),
		i.uri("int64", "textColor"),
		i.uri("int64", "lpTextColor"),
		childDamageURI,
	)
	i.DamageRect = attr.ConstraintComposite(i.uri("rect", "damageRect"),
		flat.TypeRectangle, prog)
}
