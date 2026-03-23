package mancini

import (
	"mazzy/mazarin/attr"
	"mazzy/mazarin/interactor"
)

// InitLayout creates layout handles for the Row, including constraint handles
// for WIDTH, HEIGHT, and LastChildDrawn that reactively compute from children's dimensions.
func (r *Row) InitLayout(parent string) {
	if r.Name == "" {
		r.Name = DefaultName("row")
	}
	r.Layout = newLayoutHandlesBase(r.Name, parent)
	r.Layout.constraintWidth = true
	r.Layout.constraintHeight = true

	// Inter-child spacing attribute. Set via Row.SetSpacing() after InitLayout.
	spacingURI := layoutURI(r.Name, "int64", "Spacing")
	r.Layout.SpacingHandle = attr.ValueI64(spacingURI, 0)

	// Cross-axis alignment (vertical for Row).
	alignURI := layoutURI(r.Name, "int64", "CrossAlign")
	r.Layout.CrossAlignHandle = attr.ValueI64(alignURI, int64(r.CrossAlign))

	// MaxWidth attribute. Set via Row.MaxWidth field before InitLayout.
	maxW := r.MaxWidth
	if maxW <= 0 {
		maxW = 9999 // effectively uncapped
	}
	maxWidthURI := layoutURI(r.Name, "int64", "MaxWidth")
	r.Layout.MaxWidthHandle = attr.ValueI64(maxWidthURI, maxW)

	// Build find pattern and URI fragments for constraint binding.
	findPattern := "attr:///shepherd/" + manciniPID + "/str/*/layout/Parent"
	prefix := "attr:///shepherd/" + manciniPID + "/int64/"

	// Row WIDTH is a constraint: sum of children widths + spacing, capped at maxWidth.
	widthProg := interactor.BindStrings(interactor.ProgRowWidth,
		"_maxWidth_", maxWidthURI, "_spacing_", spacingURI, "_findPattern_", findPattern,
		"_myName_", r.Name, "_int64Prefix_", prefix, "_widthSuffix_", "/layout/Width", "_visSuffix_", visSuffix)
	widthURI := layoutURI(r.Name, "int64", "Width")
	r.Layout.Width = attr.ConstraintI64(widthURI, widthProg)

	// Row HEIGHT is a constraint: max of visible children heights.
	heightProg := interactor.BindStrings(interactor.ProgRowHeight,
		"_findPattern_", findPattern, "_myName_", r.Name,
		"_int64Prefix_", prefix, "_heightSuffix_", "/layout/Height", "_visSuffix_", visSuffix)
	heightURI := layoutURI(r.Name, "int64", "Height")
	r.Layout.Height = attr.ConstraintI64(heightURI, heightProg)

	// LastChildDrawn is a constraint: 0-based index of the last child to draw (may be clipped).
	lastChildProg := interactor.BindStrings(interactor.ProgRowLastChild,
		"_maxWidth_", maxWidthURI, "_spacing_", spacingURI, "_findPattern_", findPattern,
		"_myName_", r.Name, "_int64Prefix_", prefix, "_widthSuffix_", "/layout/Width", "_visSuffix_", visSuffix)
	lastChildURI := layoutURI(r.Name, "int64", "LastChildDrawn")
	r.Layout.LastChildDrawnHandle = attr.ConstraintI64(lastChildURI, lastChildProg)

	// Bounds derived from X, Y, Width, Height.
	r.Layout.initBounds(r.Name)
}
