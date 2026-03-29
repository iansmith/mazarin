package impl

import (
	"image/color"

	"mazzy/mazarin/mancini"
)

// ThemedInteractor embeds [Interactor] and adds [mancini.Theme] support.
// It is the standard base type for leaf interactors and controls that
// need palette colors, font resolution, and neumorphic parameters.
//
// # Promoted Methods
//
// From [Interactor]: X, Y, W, H, Visible, DC, SetDC, Owner, Layout,
// GetLayout.
//
// Added by ThemedInteractor: Theme, BgColor, FgColor, Font, DefaultFont,
// DefaultSize, and Draw (background clear).
//
// # Draw as Super Call
//
// ThemedInteractor.Draw clears the background to the theme's surface
// color. Concrete types that want a background clear call it as a
// "super" method before rendering their own content:
//
//	func (l *Label) Draw(self mancini.Interactor, x, y, w, h int64) {
//	    l.ThemedInteractor.Draw(self, x, y, w, h) // clear background
//	    // ... render label text ...
//	}
//
// Many interactors (Button, Checkbox, Scrollbar, etc.) skip the super
// call because [std.NeuBoxWith] fills the background as part of the
// neumorphic shadow pipeline.
//
// # Concrete Types That Embed ThemedInteractor
//
// [std.Button], [std.Checkbox], [std.CheckboxWithLabel], [std.Label],
// [std.ConsoleLabel], [std.SingleLineText], [std.Scrollbar],
// [std.NOfMChooser], [std.RadialNOfMChooser], [std.RadialMenu].
type ThemedInteractor struct {
	Interactor // X(), Y(), W(), H(), DC(), Visible() — all promoted
	theme      mancini.Theme
}

// Init wires the backpointer, layout, and theme. Must be called from
// the concrete type's constructor, passing the concrete type as owner:
//
//	b := &Button{...}
//	b.ThemedInteractor.Init(b, layout, theme)
//
// This calls [Interactor.Init] internally, which registers the
// interactor in the global registry.
func (t *ThemedInteractor) Init(owner mancini.Interactor, layout *mancini.LayoutAttributes, theme mancini.Theme) {
	t.Interactor.Init(owner, layout)
	t.theme = theme
}

func (t *ThemedInteractor) Theme() mancini.Theme   { return t.theme }
func (t *ThemedInteractor) BgColor() color.NRGBA { return t.theme.Palette().Surface() }
func (t *ThemedInteractor) FgColor() color.NRGBA { return t.theme.Palette().Text() }
func (t *ThemedInteractor) Font(feature mancini.Feature, size int64) *mancini.FontConfig {
	return t.theme.Font(feature, size)
}
func (t *ThemedInteractor) DefaultFont() *mancini.FontConfig { return t.theme.DefaultFont() }
func (t *ThemedInteractor) DefaultSize() int64               { return t.theme.DefaultFontSize() }

// Draw clears the interactor's background to the [mancini.Palette]'s
// Surface color. If the background is fully transparent (alpha == 0),
// the fill is skipped.
//
// Concrete types that want a background clear before rendering their
// own content call this as a super method:
//
//	l.ThemedInteractor.Draw(self, x, y, w, h)
//
// Interactors whose neumorphic rendering already fills the background
// (via [std.NeuBoxWith] or similar) skip this call entirely.
func (t *ThemedInteractor) Draw(self mancini.Interactor, x, y, w, h int64) {
	bg := t.theme.Palette().Surface()
	if bg.A == 0 {
		return
	}
	dc := self.DC()
	dc.SetColor(bg)
	dc.FillRectangle(float64(x), float64(y), float64(w), float64(h))
}
