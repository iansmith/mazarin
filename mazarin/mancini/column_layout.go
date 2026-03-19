//go:build linux

package mancini

import (
	"mazzy/mazarin/attr"
	"mazzy/mazarin/interactor"
)

// InitLayout creates layout handles for the Column, including constraint handles
// for HEIGHT and WIDTH that reactively compute from children's dimensions.
func (c *Column) InitLayout(parent string) {
	if c.Name == "" {
		c.Name = defaultName("column")
	}
	c.Layout = newLayoutHandlesBase(c.Name, parent)
	c.Layout.constraintWidth = true
	c.Layout.constraintHeight = true

	// Inter-child spacing attribute. Set via Column.SetSpacing() after InitLayout.
	spacingURI := layoutURI(c.Name, "int64", "Spacing")
	c.Layout.SpacingHandle = attr.ValueI64(spacingURI, 0)

	// Build find pattern and URI fragments for constraint binding.
	findPattern := "attr:///shepherd/" + manciniPID + "/str/*/layout/Parent"
	prefix := "attr:///shepherd/" + manciniPID + "/int64/"

	// Column HEIGHT is a constraint: sum of children heights + spacing.
	heightProg := interactor.BindStrings(interactor.ProgColumnHeight,
		findPattern, spacingURI, c.Name, prefix, "/layout/Height")
	heightURI := layoutURI(c.Name, "int64", "Height")
	c.Layout.Height = attr.ConstraintI64(heightURI, heightProg)

	// Column WIDTH is a constraint: max of children widths.
	widthProg := interactor.BindStrings(interactor.ProgColumnWidth,
		findPattern, c.Name, prefix, "/layout/Width")
	widthURI := layoutURI(c.Name, "int64", "Width")
	c.Layout.Width = attr.ConstraintI64(widthURI, widthProg)

	// Bounds derived from X, Y, Width, Height.
	c.Layout.initBounds(c.Name)
}
