package std

import (
	"image/color"

	"mazzy/mazarin/mancini"
	"mazzy/mazarin/mancini/theme"
)

// NewDefaultTheme creates a Theme with the standard neumorphic purple palette.
func NewDefaultTheme(family string, defaultSize int64, resolve mancini.FontResolver) mancini.Theme {
	return theme.NewDefaultTheme(family, defaultSize, resolve)
}

// NewDefaultThemeColors creates a Theme with custom foreground and background
// colors. Shadow colors are derived from the defaults; only the surface and
// text colors are overridden.
func NewDefaultThemeColors(fg, bg color.NRGBA, family string, defaultSize int64, resolve mancini.FontResolver) mancini.Theme {
	pal := theme.NewDefaultPaletteWithColors(bg, fg)
	neu := theme.NewDefaultNeumorphicParams()
	return theme.NewTheme(pal, neu, family, defaultSize, resolve)
}
