package impl

import (
	"image/color"

	"mazzy/mazarin/mancini"
)

// ThemedInteractor embeds Interactor and adds theme support.
// Its Draw() clears the background to the theme's BgColor.
// Concrete types call t.ThemedInteractor.Draw(self) as "super"
// before rendering their own content.
type ThemedInteractor struct {
	Interactor // X(), Y(), W(), H(), DC(), Visible() — all promoted
	theme      mancini.Theme
}

// Init wires the back-pointer, layout, and theme. Must be called from
// the concrete type's constructor.
func (t *ThemedInteractor) Init(owner mancini.Interactor, layout *mancini.LayoutHandles, theme mancini.Theme) {
	t.Interactor.Init(owner, layout)
	t.theme = theme
}

func (t *ThemedInteractor) BgColor() color.NRGBA { return t.theme.Bg }
func (t *ThemedInteractor) FgColor() color.NRGBA { return t.theme.Fg }
func (t *ThemedInteractor) Font(feature mancini.Feature, size int64) *mancini.FontConfig {
	return t.theme.Font(feature, size)
}
func (t *ThemedInteractor) DefaultFont() *mancini.FontConfig { return t.theme.DefaultFont() }
func (t *ThemedInteractor) DefaultSize() int64               { return t.theme.DefaultSize() }

// Draw clears the interactor's background to the theme's BgColor.
// Concrete types override Draw and call t.ThemedInteractor.Draw(self)
// first to get the background clear, then render their own content.
func (t *ThemedInteractor) Draw(self mancini.Interactor) {
	dc := self.DC()
	dc.SetColor(t.theme.Bg)
	dc.FillRectangle(float64(self.X()), float64(self.Y()),
		float64(self.W()), float64(self.H()))
}
