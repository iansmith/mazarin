package mancini

import (
	"mazzy/mazarin/attr"
	"mazzy/mazarin/interactor"
)

// InitLayout creates layout handles for the Column, including constraint handles
// for HEIGHT and WIDTH that reactively compute from children's dimensions.
func (c *Column) InitLayout(parent string) {
	if c.Name == "" {
		c.Name = DefaultName("column")
	}
	c.Layout = newLayoutHandlesBase(c.Name, parent)
	c.Layout.constraintWidth = true
	c.Layout.constraintHeight = true

	// Inter-child spacing attribute. Set via Column.SetSpacing() after InitLayout.
	spacingURI := layoutURI(c.Name, "int64", "Spacing")
	c.Layout.SpacingHandle = attr.ValueI64(spacingURI, 0)

	// Cross-axis alignment (horizontal for Column).
	alignURI := layoutURI(c.Name, "int64", "CrossAlign")
	c.Layout.CrossAlignHandle = attr.ValueI64(alignURI, int64(c.CrossAlign))

	// Build find pattern and URI fragments for constraint binding.
	findPattern := "attr:///shepherd/" + manciniPID + "/str/*/layout/Parent"
	prefix := "attr:///shepherd/" + manciniPID + "/int64/"

	// Column HEIGHT is a constraint: sum of children heights + spacing.
	heightProg := interactor.BindStrings(interactor.ProgColumnHeight,
		"_findPattern_", findPattern, "_spacing_", spacingURI, "_myName_", c.Name,
		"_int64Prefix_", prefix, "_heightSuffix_", "/layout/Height", "_visSuffix_", visSuffix)
	heightURI := layoutURI(c.Name, "int64", "Height")
	c.Layout.Height = attr.ConstraintI64(heightURI, heightProg)

	// Column WIDTH is a constraint: max of children widths.
	widthProg := interactor.BindStrings(interactor.ProgColumnWidth,
		"_findPattern_", findPattern, "_myName_", c.Name,
		"_int64Prefix_", prefix, "_widthSuffix_", "/layout/Width", "_visSuffix_", visSuffix)
	widthURI := layoutURI(c.Name, "int64", "Width")
	c.Layout.Width = attr.ConstraintI64(widthURI, widthProg)

	// Bounds derived from X, Y, Width, Height.
	c.Layout.initBounds(c.Name)
}
