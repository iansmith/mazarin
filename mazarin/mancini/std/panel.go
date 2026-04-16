package std

import (
	"image"

	"mazzy/mazarin/attr"
	"mazzy/mazarin/mancini"
	"mazzy/mazarin/mancini/impl"
)

// Panel is a minimal container that draws its children at their
// layout-specified positions. It fills its background with the
// palette's Surface color before drawing children.
//
// Unlike [Column] or [Row], Panel does not arrange children
// automatically — children must have their X, Y, Width, and Height
// set externally (via constraints or direct Set calls).
type Panel struct {
	impl.Interactor
	impl.Parent

	Pal mancini.Palette
}

// NewPanel creates a Panel with value-based width and height.
func NewPanel(myName, parent string, pal mancini.Palette, w, h int64) *Panel {
	if myName == "" {
		myName = mancini.DefaultName("panel")
	}

	lh := mancini.NewLayoutAttributesBase(myName, parent)
	lh.Width = attr.ValueI64(
		mancini.LayoutURI(myName, mancini.DataTypeInt64, mancini.LayoutWidth), w)
	lh.Height = attr.ValueI64(
		mancini.LayoutURI(myName, mancini.DataTypeInt64, mancini.LayoutHeight), h)
	lh.InitBounds(myName)

	p := &Panel{Pal: pal}
	p.Interactor.Initialize(p, lh)
	p.Parent.Initialize(true, &p.Interactor)
	return p
}

// Draw fills the panel background and draws each child at its
// layout-specified position.
func (p *Panel) Draw(self mancini.Interactor, x, y, w, h int64, damage image.Rectangle) {
	dc := self.DC()
	if dc == nil {
		return
	}

	dc.SetColor(p.Pal.Surface())
	dc.FillRectangle(float64(x), float64(y), float64(w), float64(h))

	children := p.GetChildren()
	for _, child := range children {
		if !child.Visible() {
			continue
		}
		childL, hasLayout := child.(mancini.Layouter)
		if !hasLayout {
			continue
		}
		clh := childL.GetLayout()
		cx := clh.X.Get()
		cy := clh.Y.Get()
		cw := clh.Width.Get()
		ch := clh.Height.Get()

		if cs, ok := child.(interface{ SetDC(mancini.DrawContext) }); ok {
			cs.SetDC(dc)
		}
		if d, ok := child.(mancini.NewDrawer); ok {
			d.Draw(child, cx, cy, cw, ch, damage)
		}
	}
}
