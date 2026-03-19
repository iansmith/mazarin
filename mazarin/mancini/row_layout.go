//go:build linux

package mancini

import (
	"mazzy/mazarin/attr"
	"mazzy/mazarin/interactor"
)

// InitLayout creates layout handles for the Row, including constraint handles
// for WIDTH and HEIGHT that reactively compute from children's dimensions.
func (r *Row) InitLayout(parent string) {
	if r.Name == "" {
		r.Name = defaultName("row")
	}
	r.Layout = newLayoutHandlesBase(r.Name, parent)
	r.Layout.constraintWidth = true
	r.Layout.constraintHeight = true

	// Inter-child spacing attribute. Set via Row.SetSpacing() after InitLayout.
	spacingURI := layoutURI(r.Name, "int64", "spacing")
	r.Layout.SpacingHandle = attr.ValueI64(spacingURI, 0)

	// Build find pattern and URI fragments for constraint binding.
	findPattern := "attr:///shepherd/" + manciniPID + "/str/*/layout/parent"
	prefix := "attr:///shepherd/" + manciniPID + "/int64/"

	// Row WIDTH is a constraint: sum of children widths + spacing.
	// Bindings: _0_=findPattern, _1_=spacingURI, _2_=r.Name, _3_=prefix, _4_="/layout/width"
	widthProg := interactor.BindStrings(interactor.ProgRowWidth,
		findPattern, spacingURI, r.Name, prefix, "/layout/width")
	widthURI := layoutURI(r.Name, "int64", "width")
	r.Layout.Width = attr.ConstraintI64(widthURI, widthProg)

	// Row HEIGHT is a constraint: max of children heights.
	// Bindings: _0_=findPattern, _1_=r.Name, _2_=prefix, _3_="/layout/height"
	heightProg := interactor.BindStrings(interactor.ProgRowHeight,
		findPattern, r.Name, prefix, "/layout/height")
	heightURI := layoutURI(r.Name, "int64", "height")
	r.Layout.Height = attr.ConstraintI64(heightURI, heightProg)

	// Bounds derived from X, Y, Width, Height.
	r.Layout.initBounds(r.Name)
}
