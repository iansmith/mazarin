package std

import (
	"image"

	"mazzy/mazarin/mancini"
	"mazzy/mazarin/mancini/impl"
)

// Button is a neumorphic push-button interactor. It renders a rounded
// rectangle with shadows controlled by Depth, and optional face content
// (text, icon, etc.) drawn by the Face callback.
//
// Button embeds [impl.ThemedInteractor] for layout wiring, theme access,
// and the backpointer pattern. Rendering is delegated to [NeuBoxWith],
// which handles all three depth states ([mancini.Raised], [mancini.Flush],
// [mancini.Inset]). When the theme's [mancini.NeumorphicParams.Light]
// returns nil, the button falls back to a flat filled rectangle.
//
// # Depth Transitions
//
// Callers toggle Depth on mouse-down/up to animate press feedback. Use
// [mancini.NeuDepth.MouseDown] for the standard transition:
// Raised→Inset on press, Inset→Flush on release.
//
// # Construction
//
// Use [NewButton] with a pre-built [mancini.LayoutAttributes], or
// [NewButtonNamed] for automatic layout creation from name+parent strings.
// Both pass the Button itself as the backpointer via
// [impl.ThemedInteractor.Init].
type Button struct {
	impl.ThemedInteractor

	Depth    mancini.NeuDepth // visual state: Raised, Flush, or Inset
	Radius   float64          // corner radius in pixels (default 8.0)
	Face     mancini.Face     // optional content drawn on top of the button face
	Disabled bool              // when true, renders dimmed and ignores input
}

// NewButton creates a Button wired to the constraint system and theme.
// layout must already be created (e.g., via [mancini.NewLayoutAttributes]).
func NewButton(layout *mancini.LayoutAttributes, theme mancini.Theme,
	depth mancini.NeuDepth) *Button {

	b := &Button{
		Depth:  depth,
		Radius: 8.0,
	}
	b.ThemedInteractor.Initialize(b, layout, theme)
	return b
}

// NewButtonNamed creates a Button with layout built from name + parent
// strings. Width and height are published as intrinsic dimensions so
// the constraint system can bootstrap. If myName is empty, a unique
// name is generated via [mancini.DefaultName].
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

// Draw implements [mancini.NewDrawer]. It renders a neumorphic rounded
// rectangle via [NeuBoxWith] at the given bounds, filled with the
// [mancini.Palette]'s Surface color and decorated with shadows according
// to Depth. If Face is non-nil, it is called to draw content (text,
// icon, etc.) on top of the box.
func (b *Button) Draw(self mancini.Interactor, x, y, w, h int64, damage image.Rectangle) {
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
	b.Theme().Style().DrawBox(pal, dc, b.Depth, mancini.LightWeight,
		fx, fy, fx+fw, fy+fh, b.Radius, pal.Surface())
	if b.Face != nil {
		b.Face.DrawFace(dc, fx, fy, fw, fh)
	}
	if b.Disabled {
		ApplyDisabledOverlay(pal, dc, fx, fy, fx+fw, fy+fh, b.Radius)
	}
}
