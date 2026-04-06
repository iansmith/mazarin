package std

import (
	"image/color"

	"mazzy/mazarin/mancini"
	"mazzy/mazarin/mancini/theme"
)

// NewDefaultTheme creates a Theme with the standard neumorphic purple palette.
func NewDefaultTheme(family string, defaultSize int64, resolve mancini.FontResolver) mancini.Theme {
	t := theme.NewDefaultTheme(family, defaultSize, resolve)
	neu := t.Neumorphic()
	t.SetStyle(NewNeumorphicStyle(neu.Heavy(), neu.Light()))
	return t
}

// NewDefaultThemeColors creates a Theme with custom foreground and background
// colors. Shadow colors are derived from the defaults; only the surface and
// text colors are overridden.
func NewDefaultThemeColors(fg, bg color.NRGBA, family string, defaultSize int64, resolve mancini.FontResolver) mancini.Theme {
	pal := theme.NewDefaultPaletteWithColors(bg, fg)
	neu := theme.NewDefaultNeumorphicParams()
	t := theme.NewTheme(pal, neu, family, defaultSize, resolve)
	t.SetStyle(NewNeumorphicStyle(neu.Heavy(), neu.Light()))
	return t
}
