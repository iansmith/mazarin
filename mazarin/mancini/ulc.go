package mancini

import "mazzy/mazarin/attr"

// ULC object-reference codes (bits 5–3 of the 16-bit constant).
// They identify which object's attribute is the source for the constraint.
const (
	UlcSelf       = 0 // the element itself
	UlcParent     = 1 // the element's parent
	UlcPrevSib    = 2 // the previous visible sibling
	UlcNextSib    = 3 // the next visible sibling
	UlcFirstChild = 4 // the first visible child  (element is the parent)
	UlcLastChild  = 5 // the last visible child   (element is the parent)
	UlcMinChild   = 6 // visible child with minimum value of the target attribute
	UlcMaxChild   = 7 // visible child with maximum value of the target attribute
)

// ULC function codes (bits 2–0 of the 16-bit constant).
// They identify which attribute of the referenced object to read.
const (
	UlcX      = 0 // x     (left edge)
	UlcY      = 1 // y     (top edge)
	UlcWidth  = 2 // width
	UlcHeight = 3 // height
	UlcRight  = 4 // right  = x + width
	UlcBottom = 5 // bottom = y + height
)

// UlcConst encodes a ULC 16-bit constant from an object-reference code and a
// function code.
//
//	constVal = (objRef << 3) | fnCode
//
// Example:
//
//	UlcConst(UlcFirstChild, UlcWidth)  // width of the first visible child
//	UlcConst(UlcPrevSib,   UlcRight)  // right edge of the previous sibling
func UlcConst(objRef, fnCode int) int64 {
	return int64((objRef << 3) | fnCode)
}

// NewUlcConstraintI64 attaches a ULC constraint to a layout attribute of lh.
// constVal encodes the source object and attribute via UlcConst.
//
// The constraint is kernel-backed: it re-evaluates automatically whenever any
// of the attributes it reads (position, size, visibility of nearby elements)
// change. The only static dependency is the encoded constant itself, which
// never changes.
//
// Typical usage inside an interactor's Initialize method:
//
//	// width = width of first visible child
//	lh.Width = mancini.NewUlcConstraintI64(lh, LayoutWidth,
//	    mancini.UlcConst(mancini.UlcFirstChild, mancini.UlcWidth))
//
//	// x = right edge of previous sibling  (tile horizontally)
//	lh.X = mancini.NewUlcConstraintI64(lh, LayoutX,
//	    mancini.UlcConst(mancini.UlcPrevSib, mancini.UlcRight))
//
//	// height = maximum child height  (row grows to tallest child)
//	lh.Height = mancini.NewUlcConstraintI64(lh, LayoutHeight,
//	    mancini.UlcConst(mancini.UlcMaxChild, mancini.UlcHeight))
func NewUlcConstraintI64(lh *LayoutAttributes, prop LayoutProp, constVal int64) *attr.Attribute[int64] {
	myName := lh.Name()

	// A constant value attribute that carries the encoded ULC constant into the
	// VM program.  Its URI is scoped to this element and property so that
	// multiple ULC constraints on the same element don't collide.
	cvURI := Int64Prefix() + myName + "/ulc/" + string(prop)
	cvAttr := attr.ValueI64(cvURI, constVal)

	// Bind all standard children/sibling placeholders plus the two that are
	// specific to this constraint instance.
	prog := BindStringsChildren(ProgUlcDecode,
		"_mySeg_", myName,
		"_constValURI_", cvAttr.URI(),
	)

	// cvAttr is declared as a static dependency so the kernel registers the
	// edge before the first evaluation.  All other dependencies (the positions
	// and sizes read via derefI64/findWhere inside the VM) are registered
	// dynamically on the first run.
	return attr.ConstraintI64(LayoutURI(myName, DataTypeInt64, prop), prog, cvAttr)
}
