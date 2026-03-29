// Package theme provides the default implementations of [mancini.Theme],
// [mancini.Palette], and [mancini.NeumorphicParams].
//
// [DefaultTheme] bundles a palette, neumorphic parameters, and font
// resolution into a single value that satisfies [mancini.Theme].
// [DefaultPalette] provides the standard purple neumorphic color scheme.
// [DefaultNeumorphicParams] provides two shadow parameter sets: heavy
// (window-weight, for [std.AppWindow]) and light (button-weight, for
// controls like [std.Button] and [std.Checkbox]).
//
// Application code typically creates a theme via [NewDefaultTheme] or
// via [std.NewDefaultTheme], which delegates here.
package theme
