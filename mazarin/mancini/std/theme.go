package std

import (
	"image/color"

	"mazzy/mazarin/mancini"
)

// NewDefaultTheme creates a Theme with the standard neumorphic purple palette.
func NewDefaultTheme(family string, defaultSize int64, resolve mancini.FontResolver) mancini.Theme {
	return mancini.NewTheme(
		color.NRGBA{232, 230, 244, 255}, // Surface
		color.NRGBA{78, 72, 112, 255},   // Text
		family, defaultSize, resolve,
	)
}

// NewDefaultThemeColors creates a Theme with custom foreground and background.
func NewDefaultThemeColors(fg, bg color.NRGBA, family string, defaultSize int64, resolve mancini.FontResolver) mancini.Theme {
	return mancini.NewTheme(bg, fg, family, defaultSize, resolve)
}
