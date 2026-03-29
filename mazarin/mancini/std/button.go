package std

import (
	"mazzy/mazarin/mancini"
	"mazzy/mazarin/mancini/impl"
)

// Button is a neumorphic push-button interactor. It embeds ThemedInteractor
// for constraint-system wiring and theme support, and delegates rendering
// to NeuBoxWith so it participates in the standard shadow pipeline.
//
// Depth controls the visual state (Raised, Flush, Inset). Callers can
// toggle Depth on mouse-down/up to animate press feedback.
type Button struct {
	impl.ThemedInteractor

	Depth  mancini.NeuDepth
	Radius float64
	Face   mancini.FaceDrawer // optional content drawing callback
}

// NewButton creates a Button wired to the constraint system and theme.
// layout must already be created (e.g. via mancini.NewLayoutAttributes).
func NewButton(layout *mancini.LayoutAttributes, theme mancini.Theme,
	depth mancini.NeuDepth) *Button {

	b := &Button{
		Depth:  depth,
		Radius: 8.0,
	}
	b.ThemedInteractor.Init(b, layout, theme)
	return b
}

// NewButtonNamed creates a Button with layout built from name + parent strings.
// Width and height are published as intrinsic dimensions so the constraint
// system can bootstrap.
func NewButtonNamed(myName, parent string, theme mancini.Theme,
	depth mancini.NeuDepth, width, height int64) *Button {

	if myName == "" {
		myName = mancini.DefaultName("button")
	}
	lh := mancini.NewLayoutAttributes(myName, parent)
	lh.Width.Set(width)
	lh.Height.Set(height)
	return NewButton(lh, theme, depth)
}

// Draw implements mancini.NewDrawer. It renders a neumorphic rounded
// rectangle at the given bounds, filled with the theme palette's surface
// color and decorated with shadows according to Depth. If Face is non-nil
// it is called to draw content (text, icon, etc.) on top of the box.
func (b *Button) Draw(self mancini.Interactor, x, y, w, h int64) {
	if !self.Visible() {
		return
	}
	dc := self.DC()
	if dc == nil {
		return
	}
	fx, fy := float64(x), float64(y)
	fw, fh := float64(w), float64(h)

	pal := b.Theme().Palette()
	var params *mancini.NeuParams
	if neu := b.Theme().Neumorphic(); neu != nil {
		params = neu.Light()
	}

	NeuBoxWith(pal, dc, b.Depth, fx, fy, fx+fw, fy+fh, b.Radius, pal.Surface(), params, b.Face)
}
