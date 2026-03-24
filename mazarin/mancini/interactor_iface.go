package mancini

import "image/color"

// Interactor is the base interface for all UI elements in the tree.
type Interactor interface {
	X() int64
	Y() int64
	W() int64
	H() int64
	Visible() bool
	DC() DrawContext
}

// ThemedInteractor extends Interactor with theme access.
type ThemedInteractor interface {
	Interactor
	BgColor() color.NRGBA
	FgColor() color.NRGBA
	Font(feature Feature, size int64) *FontConfig
	DefaultFont() *FontConfig
	DefaultSize() int64
}

// Parent is an interactor that has children. Simple parents embed
// impl.Parent for a default DrawChildren that propagates DC and calls
// each child's Draw. Complex parents (like Decorator) use GetChildren
// but handle drawing themselves.
type Parent interface {
	GetChildren() []Interactor
	DrawChildren(self Interactor, x, y, w, h int64)
}

// Decoratable is implemented by types that customize decoration drawing.
// The default Decorator draws a thick black box; NeuBox and NeuCircle
// override Decorate to draw neumorphic shadows instead.
// x, y, w, h are the decorator's authoritative bounds from its parent.
type Decoratable interface {
	Decorate(self Interactor, x, y, w, h int64)
}
