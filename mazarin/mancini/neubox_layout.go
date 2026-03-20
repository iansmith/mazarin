package mancini

import (
	"math"

	"mazzy/mazarin/attr"
	"mazzy/mazarin/interactor"
)

// InitLayout creates layout handles for the NeuBox using inside-out sizing.
// The child discovers its parent via the attribute namespace (same as Column).
// If no child has registered, NeuBox defaults to 20x30 logical pixels + margin.
func (n *NeuBox) InitLayout(parent string) {
	if n.Name == "" {
		n.Name = defaultName("neubox")
	}
	n.margin = math.Ceil(neuMaxPad(n.Params))
	n.Layout = newLayoutHandlesBase(n.Name, parent)
	n.Layout.constraintWidth = true
	n.Layout.constraintHeight = true

	// Margin value attribute for constraint programs to reference.
	marginURI := layoutURI(n.Name, "int64", "Margin")
	attr.ValueI64(marginURI, int64(n.margin))

	// Max size attribute (default 800 logical pixels).
	maxSize := int64(800)
	if n.MaxSize > 0 {
		maxSize = int64(n.MaxSize)
	}
	maxSizeURI := layoutURI(n.Name, "int64", "MaxSize")
	attr.ValueI64(maxSizeURI, maxSize)

	// Build find pattern and URI fragments for constraint binding.
	findPattern := "attr:///shepherd/" + manciniPID + "/str/*/layout/Parent"
	prefix := "attr:///shepherd/" + manciniPID + "/int64/"

	// Width = min(child width + 2*margin, maxSize). Default 20 if no child.
	widthProg := interactor.BindStrings(ProgDecorationWidth,
		findPattern, n.Name, marginURI, prefix, "/layout/Width", maxSizeURI)
	n.Layout.Width = attr.ConstraintI64(
		layoutURI(n.Name, "int64", "Width"), widthProg)

	// Height = min(child height + 2*margin, maxSize). Default 30 if no child.
	heightProg := interactor.BindStrings(ProgDecorationHeight,
		findPattern, n.Name, marginURI, prefix, "/layout/Height", maxSizeURI)
	n.Layout.Height = attr.ConstraintI64(
		layoutURI(n.Name, "int64", "Height"), heightProg)

	// Bounds and BoundsHash derived from X, Y, Width, Height.
	n.Layout.initBounds(n.Name)
}
