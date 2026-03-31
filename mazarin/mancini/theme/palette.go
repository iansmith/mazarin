package theme

import (
	"image/color"

	"mazzy/mazarin/mancini"
)

var _ mancini.Palette = (*DefaultPalette)(nil)

// DefaultPalette implements [mancini.Palette] with a neumorphic color
// scheme. The standard palette uses a purple surface tone with
// complementary dark/light shadow colors.
//
// Create with [NewDefaultPalette], [NewDefaultPaletteWithColors] (custom
// surface and text), or [NewDefaultPaletteSwapRB] (for BGR framebuffers).
type DefaultPalette struct {
	surface       color.NRGBA
	surfaceTint   color.NRGBA
	darkShadow    color.NRGBA
	lightShadow   color.NRGBA
	text          color.NRGBA
	icon          color.NRGBA
	highlight     color.NRGBA
	highlightText color.NRGBA
	disabledAlpha float64
	swapRB        bool
}

func (p *DefaultPalette) Surface() color.NRGBA      { return p.surface }
func (p *DefaultPalette) SurfaceTint() color.NRGBA   { return p.surfaceTint }
func (p *DefaultPalette) DarkShadow() color.NRGBA    { return p.darkShadow }
func (p *DefaultPalette) LightShadow() color.NRGBA   { return p.lightShadow }
func (p *DefaultPalette) Text() color.NRGBA          { return p.text }
func (p *DefaultPalette) Icon() color.NRGBA          { return p.icon }
func (p *DefaultPalette) Highlight() color.NRGBA     { return p.highlight }
func (p *DefaultPalette) HighlightText() color.NRGBA { return p.highlightText }
func (p *DefaultPalette) DisabledAlpha() float64     { return p.disabledAlpha }

// NewDefaultPalette returns the standard purple neumorphic palette.
func NewDefaultPalette() *DefaultPalette {
	return &DefaultPalette{
		surface:       color.NRGBA{R: 232, G: 230, B: 244, A: 255},
		surfaceTint:   color.NRGBA{R: 218, G: 213, B: 237, A: 255},
		darkShadow:    color.NRGBA{R: 176, G: 173, B: 195, A: 255},
		lightShadow:   color.NRGBA{R: 255, G: 255, B: 255, A: 255},
		text:          color.NRGBA{R: 78, G: 72, B: 112, A: 255},
		icon:          color.NRGBA{R: 105, G: 99, B: 148, A: 235},
		highlight:     color.NRGBA{R: 200, G: 100, B: 255, A: 255},
		highlightText: color.NRGBA{R: 78, G: 72, B: 112, A: 255},
		disabledAlpha: 0.4,
		swapRB:        false,
	}
}

// NewDefaultPaletteWithColors returns the standard palette with custom
// surface and text colors. Shadow colors and other fields use defaults.
func NewDefaultPaletteWithColors(surface, text color.NRGBA) *DefaultPalette {
	p := NewDefaultPalette()
	p.surface = surface
	p.text = text
	return p
}

// NewDefaultPaletteSwapRB returns the standard palette with red and blue
// channels pre-swapped in every color (for framebuffers that use BGR
// byte order). Callers can use the returned colors directly without
// additional swapping.
func NewDefaultPaletteSwapRB() *DefaultPalette {
	p := NewDefaultPalette()
	p.swapRB = true
	p.surface = mancini.SwapRB(p.surface)
	p.surfaceTint = mancini.SwapRB(p.surfaceTint)
	p.darkShadow = mancini.SwapRB(p.darkShadow)
	p.lightShadow = mancini.SwapRB(p.lightShadow)
	p.text = mancini.SwapRB(p.text)
	p.icon = mancini.SwapRB(p.icon)
	p.highlight = mancini.SwapRB(p.highlight)
	p.highlightText = mancini.SwapRB(p.highlightText)
	return p
}
