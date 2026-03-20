package mancini

import (
	"math"

	"mazzy/mazarin/attr"
)

// InitLayout creates layout handles for the NeuCircle.
// Uses value handles (not constraint programs) because circle geometry
// requires Width == Height == diameter, involving sqrt(2) scaling that
// is not expressible with the rectangular decoration constraint programs.
func (n *NeuCircle) InitLayout(parent string) {
	if n.Name == "" {
		n.Name = defaultName("neucircle")
	}
	n.margin = math.Ceil(neuMaxPad(n.Params))
	n.Layout = newLayoutHandles(n.Name, parent)

	// Publish margin as a value attribute for debugging/inspection.
	marginURI := layoutURI(n.Name, "int64", "Margin")
	attr.ValueI64(marginURI, int64(n.margin))

	// Publish initial diameter so parent constraints can bootstrap.
	// Child must be initialized (InitLayout called) before this.
	if diam := n.preferredDiameter(); diam > 0 {
		n.Layout.Width.Set(int64(diam))
		n.Layout.Height.Set(int64(diam))
	}
}
