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
	desktopBG     color.NRGBA
	disabledAlpha float64

	// Extended color roles.
	base            color.NRGBA
	baseText        color.NRGBA
	midlight        color.NRGBA
	mid             color.NRGBA
	shadow          color.NRGBA
	brightText      color.NRGBA
	alternateBase   color.NRGBA
	toolTipBase     color.NRGBA
	toolTipText     color.NRGBA
	link            color.NRGBA
	placeholderText color.NRGBA
	accent          color.NRGBA
	windowText      color.NRGBA
	cursorColor     color.NRGBA
	cursorTextColor color.NRGBA
	ansiColors      [16]color.NRGBA

	swapRB bool
}

// ── Original accessors ──────────────────────────────────────────────────

func (p *DefaultPalette) Surface() color.NRGBA      { return p.surface }
func (p *DefaultPalette) SurfaceTint() color.NRGBA   { return p.surfaceTint }
func (p *DefaultPalette) DarkShadow() color.NRGBA    { return p.darkShadow }
func (p *DefaultPalette) LightShadow() color.NRGBA   { return p.lightShadow }
func (p *DefaultPalette) Text() color.NRGBA          { return p.text }
func (p *DefaultPalette) Icon() color.NRGBA          { return p.icon }
func (p *DefaultPalette) Highlight() color.NRGBA     { return p.highlight }
func (p *DefaultPalette) HighlightText() color.NRGBA { return p.highlightText }
func (p *DefaultPalette) DisabledAlpha() float64     { return p.disabledAlpha }
func (p *DefaultPalette) DesktopBG() color.NRGBA     { return p.desktopBG }

// ── Extended accessors ──────────────────────────────────────────────────

func (p *DefaultPalette) Base() color.NRGBA            { return p.base }
func (p *DefaultPalette) BaseText() color.NRGBA         { return p.baseText }
func (p *DefaultPalette) Midlight() color.NRGBA         { return p.midlight }
func (p *DefaultPalette) Mid() color.NRGBA              { return p.mid }
func (p *DefaultPalette) Shadow() color.NRGBA           { return p.shadow }
func (p *DefaultPalette) BrightText() color.NRGBA       { return p.brightText }
func (p *DefaultPalette) AlternateBase() color.NRGBA    { return p.alternateBase }
func (p *DefaultPalette) ToolTipBase() color.NRGBA      { return p.toolTipBase }
func (p *DefaultPalette) ToolTipText() color.NRGBA      { return p.toolTipText }
func (p *DefaultPalette) Link() color.NRGBA             { return p.link }
func (p *DefaultPalette) PlaceholderText() color.NRGBA  { return p.placeholderText }
func (p *DefaultPalette) Accent() color.NRGBA           { return p.accent }
func (p *DefaultPalette) WindowText() color.NRGBA       { return p.windowText }
func (p *DefaultPalette) CursorColor() color.NRGBA      { return p.cursorColor }
func (p *DefaultPalette) CursorTextColor() color.NRGBA  { return p.cursorTextColor }

func (p *DefaultPalette) AnsiColor(index int) color.NRGBA {
	if index < 0 {
		index = 0
	}
	if index > 15 {
		index = 15
	}
	return p.ansiColors[index]
}

// SetDesktopBG sets the desktop background color. This color is carried
// by the palette (not derived from Surface) so the compositor and
// decoration renderer can reference it without a separate global.
// If the palette uses RB-swapped colors, pass the logical (R,G,B) color;
// the swap is applied automatically.
func (p *DefaultPalette) SetDesktopBG(c color.NRGBA) {
	if p.swapRB {
		p.desktopBG = mancini.SwapRB(c)
	} else {
		p.desktopBG = c
	}
}

// midColor returns the channel-wise midpoint of two colors.
func midColor(a, b color.NRGBA) color.NRGBA {
	return color.NRGBA{
		R: uint8((uint16(a.R) + uint16(b.R)) / 2),
		G: uint8((uint16(a.G) + uint16(b.G)) / 2),
		B: uint8((uint16(a.B) + uint16(b.B)) / 2),
		A: 255,
	}
}

// Standard 16-color ANSI palette.
var defaultAnsiColors = [16]color.NRGBA{
	{R: 0, G: 0, B: 0, A: 255},       // 0  Black
	{R: 180, G: 50, B: 50, A: 255},    // 1  Red
	{R: 50, G: 150, B: 50, A: 255},    // 2  Green
	{R: 180, G: 150, B: 50, A: 255},   // 3  Yellow
	{R: 60, G: 80, B: 200, A: 255},    // 4  Blue
	{R: 160, G: 60, B: 180, A: 255},   // 5  Magenta
	{R: 50, G: 160, B: 170, A: 255},   // 6  Cyan
	{R: 200, G: 200, B: 200, A: 255},  // 7  White
	{R: 100, G: 100, B: 100, A: 255},  // 8  Bright Black
	{R: 230, G: 80, B: 80, A: 255},    // 9  Bright Red
	{R: 80, G: 200, B: 80, A: 255},    // 10 Bright Green
	{R: 230, G: 200, B: 80, A: 255},   // 11 Bright Yellow
	{R: 100, G: 130, B: 240, A: 255},  // 12 Bright Blue
	{R: 200, G: 100, B: 220, A: 255},  // 13 Bright Magenta
	{R: 80, G: 210, B: 220, A: 255},   // 14 Bright Cyan
	{R: 240, G: 240, B: 240, A: 255},  // 15 Bright White
}

// NewDefaultPalette returns the standard purple neumorphic palette.
func NewDefaultPalette() *DefaultPalette {
	surface := color.NRGBA{R: 232, G: 230, B: 244, A: 255}
	darkShadow := color.NRGBA{R: 176, G: 173, B: 195, A: 255}
	lightShadow := color.NRGBA{R: 255, G: 255, B: 255, A: 255}
	text := color.NRGBA{R: 78, G: 72, B: 112, A: 255}
	highlight := color.NRGBA{R: 200, G: 100, B: 255, A: 255}

	return &DefaultPalette{
		surface:       surface,
		surfaceTint:   color.NRGBA{R: 218, G: 213, B: 237, A: 255},
		darkShadow:    darkShadow,
		lightShadow:   lightShadow,
		text:          text,
		icon:          color.NRGBA{R: 105, G: 99, B: 148, A: 235},
		highlight:     highlight,
		highlightText: color.NRGBA{R: 78, G: 72, B: 112, A: 255},
		disabledAlpha: 0.4,

		// Extended roles derived from the base purple scheme.
		base:            color.NRGBA{R: 248, G: 247, B: 252, A: 255}, // near-white
		baseText:        text,
		midlight:        midColor(surface, lightShadow),
		mid:             midColor(darkShadow, surface),
		shadow:          color.NRGBA{R: 40, G: 38, B: 50, A: 255}, // near-black
		brightText:      color.NRGBA{R: 255, G: 255, B: 255, A: 255},
		alternateBase:   color.NRGBA{R: 240, G: 238, B: 250, A: 255},
		toolTipBase:     color.NRGBA{R: 255, G: 255, B: 220, A: 255},
		toolTipText:     color.NRGBA{R: 40, G: 38, B: 50, A: 255},
		link:            color.NRGBA{R: 60, G: 80, B: 200, A: 255},
		placeholderText: color.NRGBA{R: 140, G: 136, B: 170, A: 200},
		accent:          highlight,
		windowText:      text,
		cursorColor:     text,
		cursorTextColor: color.NRGBA{R: 255, G: 255, B: 255, A: 255},
		ansiColors:      defaultAnsiColors,

		swapRB: false,
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
	// Original roles.
	p.surface = mancini.SwapRB(p.surface)
	p.surfaceTint = mancini.SwapRB(p.surfaceTint)
	p.darkShadow = mancini.SwapRB(p.darkShadow)
	p.lightShadow = mancini.SwapRB(p.lightShadow)
	p.text = mancini.SwapRB(p.text)
	p.icon = mancini.SwapRB(p.icon)
	p.highlight = mancini.SwapRB(p.highlight)
	p.highlightText = mancini.SwapRB(p.highlightText)
	// Extended roles.
	p.base = mancini.SwapRB(p.base)
	p.baseText = mancini.SwapRB(p.baseText)
	p.midlight = mancini.SwapRB(p.midlight)
	p.mid = mancini.SwapRB(p.mid)
	p.shadow = mancini.SwapRB(p.shadow)
	p.brightText = mancini.SwapRB(p.brightText)
	p.alternateBase = mancini.SwapRB(p.alternateBase)
	p.toolTipBase = mancini.SwapRB(p.toolTipBase)
	p.toolTipText = mancini.SwapRB(p.toolTipText)
	p.link = mancini.SwapRB(p.link)
	p.placeholderText = mancini.SwapRB(p.placeholderText)
	p.accent = mancini.SwapRB(p.accent)
	p.windowText = mancini.SwapRB(p.windowText)
	p.cursorColor = mancini.SwapRB(p.cursorColor)
	p.cursorTextColor = mancini.SwapRB(p.cursorTextColor)
	for i := range p.ansiColors {
		p.ansiColors[i] = mancini.SwapRB(p.ansiColors[i])
	}
	return p
}
