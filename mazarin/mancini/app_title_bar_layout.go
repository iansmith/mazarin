package mancini

import (
	"mazzy/mazarin/attr"
	"mazzy/mazarin/interactor"
)

// InitLayout creates layout handles for the AppTitleBar and its child Label.
// The child's Y position is a vertical-center constraint computed from the
// title bar's Y, Height, and the child's Height.
func (tb *AppTitleBar) InitLayout(parent string) {
	if tb.Name == "" {
		tb.Name = DefaultName("apptitlebar")
	}
	tb.Layout = newLayoutHandlesBase(tb.Name, parent)
	tb.Layout.constraintHeight = true

	// Width is a value (set by parent via publishLayout during Draw).
	tb.Layout.Width = attr.ValueI64(layoutURI(tb.Name, "int64", "Width"), 0)

	// Initialize child label's layout with Y as a vertical-center constraint.
	if label, ok := tb.Child.(*Label); ok {
		if label.Name == "" {
			label.Name = DefaultName("label")
		}
		childName := label.Name

		// Create child layout handles manually (Y will be a constraint).
		lh := &LayoutHandles{constraintY: true}
		lh.X = attr.ValueI64(layoutURI(childName, "int64", "X"), 0)
		lh.Width = attr.ValueI64(layoutURI(childName, "int64", "Width"), 0)
		lh.Height = attr.ValueI64(layoutURI(childName, "int64", "Height"), 0)
		lh.Visible = attr.ValueI64(layoutURI(childName, "int64", "Visible"), 1)
		lh.Parent = attr.ValueStr(layoutURI(childName, "str", "Parent"), tb.Name)

		// Y = verticalCenter(titleBar.Y, titleBar.Height, label.Height)
		// URIs are resolved at constraint evaluation time, so referencing
		// titleBar Height before its handle is created is fine.
		tbYURI := layoutURI(tb.Name, "int64", "Y")
		tbHeightURI := layoutURI(tb.Name, "int64", "Height")
		childHeightURI := layoutURI(childName, "int64", "Height")

		yProg := interactor.BindStrings(ProgVerticalCenter,
			"_containerY_", tbYURI, "_containerHeight_", tbHeightURI, "_elementHeight_", childHeightURI)
		lh.Y = attr.ConstraintI64(layoutURI(childName, "int64", "Y"), yProg)

		lh.initBounds(childName)
		label.Layout = lh
	}

	// Height = child height + 2*vMargin (decoration pattern).
	vMargin := int64(2) // 2px padding top and bottom
	vMarginURI := layoutURI(tb.Name, "int64", "VMargin")
	attr.ValueI64(vMarginURI, vMargin)

	maxSizeURI := layoutURI(tb.Name, "int64", "MaxSize")
	attr.ValueI64(maxSizeURI, 800)

	findPattern := "attr:///shepherd/" + manciniPID + "/str/*/layout/Parent"
	prefix := "attr:///shepherd/" + manciniPID + "/int64/"

	heightProg := interactor.BindStrings(ProgDecorationHeight,
		"_findPattern_", findPattern, "_myName_", tb.Name, "_margin_", vMarginURI,
		"_int64Prefix_", prefix, "_heightSuffix_", "/layout/Height", "_maxSize_", maxSizeURI, "_visSuffix_", visSuffix)
	tb.Layout.Height = attr.ConstraintI64(
		layoutURI(tb.Name, "int64", "Height"), heightProg)

	tb.Layout.initBounds(tb.Name)
}
