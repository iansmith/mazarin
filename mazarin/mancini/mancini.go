package mancini

import (
	"image"
	"image/color"
)

// Drawer draws itself into the given bounds using a [DrawContext].
// This is the legacy interface — new interactors should implement
// [NewDrawer] instead.
type Drawer interface {
	// Draw paints this interactor into the given bounds.
	Draw(dc DrawContext, x, y, w, h float64)
}

// NewDrawer is the standard draw protocol for all interactors. The parent
// passes authoritative x, y, w, h — these override any stale layout
// handle values. The self parameter is the backpointer to the concrete
// type, enabling virtual dispatch for DC(), Visible(), and other
// interface methods. The damage parameter is the region that needs
// repainting; interactors whose bounds do not intersect it can skip
// drawing. See the package documentation for details on the
// backpointer pattern.
//
// All concrete interactors in [mazzy/mazarin/mancini/std] implement this
// interface.
type NewDrawer interface {
	// Draw paints this interactor into the given bounds, restricted to damage.
	Draw(self Interactor, x, y, w, h int64, damage image.Rectangle)
}

// Layouter is implemented by interactors that have [LayoutAttributes].
// All interactors that embed [impl.Interactor] satisfy this interface
// via the promoted GetLayout method.
type Layouter interface {
	// GetLayout returns this interactor's layout attributes.
	GetLayout() *LayoutAttributes
}

// NeuDepth represents the neumorphic depth of an interactor relative
// to the surface. It controls which shadow treatment is applied by
// [std.NeuBoxWith] and [std.NeuCircleWith].
//
// Interactive controls like [std.Button] use [NeuDepth.MouseDown] to
// animate between states on press/release.
type NeuDepth int

const (
	Raised NeuDepth = iota // Proud of the surface — casts outer shadows.
	Flush                  // Level with the surface — thin edge outline only.
	Inset                  // Recessed into the surface — inner shadows.
)

// String returns the depth's name ("Raised", "Flush", "Inset").
func (d NeuDepth) String() string {
	switch d {
	case Raised:
		return "Raised"
	case Flush:
		return "Flush"
	case Inset:
		return "Inset"
	}
	return "?"
}

// Weight selects between control-weight and window-weight rendering
// parameters. Interactors pass their weight to [SurfaceStyle.DrawBox]
// and [SurfaceStyle.Pad] so the style can apply appropriate shadow
// sizes and padding.
type Weight int

const (
	LightWeight Weight = iota // Controls (buttons, checkboxes, scrollbars)
	HeavyWeight               // Window decorations (title bars, window frames)
)

// MouseDown returns the depth a button transitions to when pressed.
//
//	Raised → Inset  (pushes below surface)
//	Flush  → Inset  (pushes below surface)
//	Inset  → Flush  (pops back to surface level)
func (d NeuDepth) MouseDown() NeuDepth {
	switch d {
	case Raised:
		return Inset
	case Flush:
		return Inset
	case Inset:
		return Flush
	}
	return d
}

// Palette provides the color vocabulary for interactor rendering.
// The default implementation is [theme.DefaultPalette]. Color roles
// follow the Qt QPalette model with additions for terminal and cursor
// colors.
//
// The original 10 roles (Surface through DesktopBG) are retained for
// backward compatibility. New roles added in the theming overhaul are
// listed after DesktopBG.
type Palette interface {
	// ── Original roles ───────────────────────────────────────────

	// Surface is the primary background color for interactors.
	Surface() color.NRGBA
	// SurfaceTint is a subtle variant of Surface for layered backgrounds.
	SurfaceTint() color.NRGBA
	// DarkShadow is the dark side of neumorphic shadows.
	DarkShadow() color.NRGBA
	// LightShadow is the light side of neumorphic shadows.
	LightShadow() color.NRGBA
	// Text is the default foreground color for text.
	Text() color.NRGBA
	// Icon is the foreground color for icons and glyphs.
	Icon() color.NRGBA
	// Highlight is the selection background color.
	Highlight() color.NRGBA
	// HighlightText is the foreground color used on Highlight backgrounds.
	HighlightText() color.NRGBA
	// DisabledAlpha is the alpha multiplier (0..1) applied to disabled controls.
	DisabledAlpha() float64
	// DesktopBG is the root window background color.
	DesktopBG() color.NRGBA

	// ── Extended color roles (Qt QPalette inspired) ──────────────

	// Base is the background for text input fields and list views.
	Base() color.NRGBA
	// BaseText is the foreground for text on Base backgrounds.
	BaseText() color.NRGBA
	// Midlight is the midpoint between Surface and LightShadow.
	Midlight() color.NRGBA
	// Mid is the midpoint between DarkShadow and Surface.
	Mid() color.NRGBA
	// Shadow is a near-black color for hard drop shadows.
	Shadow() color.NRGBA
	// BrightText is high-contrast text for use on dark backgrounds.
	BrightText() color.NRGBA
	// AlternateBase is an alternating row color for lists and tables.
	AlternateBase() color.NRGBA
	// ToolTipBase is the background for tooltip popups.
	ToolTipBase() color.NRGBA
	// ToolTipText is the foreground for tooltip text.
	ToolTipText() color.NRGBA
	// Link is the color for hyperlink text.
	Link() color.NRGBA
	// PlaceholderText is the color for placeholder/hint text in fields.
	PlaceholderText() color.NRGBA
	// Accent is the primary accent color (selection, focus rings).
	Accent() color.NRGBA
	// WindowText is the default text color for window content areas.
	WindowText() color.NRGBA

	// ── Terminal colors ──────────────────────────────────────────

	// AnsiColor returns one of the 16 standard ANSI terminal colors
	// (0-7 normal, 8-15 bright). Index is clamped to [0,15].
	AnsiColor(index int) color.NRGBA

	// ── Cursor colors ────────────────────────────────────────────

	// CursorColor is the text cursor fill/outline color.
	CursorColor() color.NRGBA
	// CursorTextColor is the text color when drawn over the cursor.
	CursorTextColor() color.NRGBA
}

// NeumorphicParams provides two weight classes of shadow parameters,
// delivered through [Theme.Neumorphic]. The default implementation is
// [theme.DefaultNeumorphicParams].
//
//   - Heavy — window-weight shadows, used by [std.AppWindow] and
//     [std.FreeFloatingWindow].
//   - Light — control-weight shadows, used by [std.Button], [std.Checkbox],
//     [std.Scrollbar], [std.NOfMChooser], and other controls.
//
// Either method may return nil to disable neumorphic rendering for that
// weight class. All interactors in [mazzy/mazarin/mancini/std] handle
// nil gracefully by falling back to flat rendering.
type NeumorphicParams interface {
	// Heavy returns window-weight neumorphic parameters, or nil to disable.
	Heavy() *NeuParams
	// Light returns control-weight neumorphic parameters, or nil to disable.
	Light() *NeuParams
}

// RaisedParams controls the dark and light outer shadow layers for the
// [Raised] depth state. DarkOff/LightOff set the shadow offset in pixels;
// DarkBlur/LightBlur set the Gaussian blur radius; DarkAlpha/LightAlpha
// set the shadow opacity.
type RaisedParams struct {
	// LightOff and LightBlur set the light-side shadow offset and blur.
	LightOff, LightBlur float64
	// DarkOff and DarkBlur set the dark-side shadow offset and blur.
	DarkOff, DarkBlur float64
	// DarkAlpha and LightAlpha set the opacity of each shadow layer.
	DarkAlpha, LightAlpha uint8
}

// InsetParams controls the inner shadow layers for the [Inset] depth
// state. Off is the shadow offset (dark biased upper-left, light biased
// lower-right). DarkBlur and LightBlur set the Gaussian blur radius for
// each shadow layer. Inner shadows are masked to the shape boundary.
type InsetParams struct {
	// Off is the inner shadow offset in pixels.
	Off float64
	// DarkBlur and LightBlur set the Gaussian blur radius for each inner shadow layer.
	DarkBlur, LightBlur float64
}

// FlushParams controls the thin edge outline for the [Flush] depth
// state. EdgeW is the stroke width; EdgeAlpha is the opacity of both
// the dark and light edge strokes.
type FlushParams struct {
	// EdgeW is the stroke width of the edge outline.
	EdgeW float64
	// EdgeAlpha is the opacity of the dark and light edge strokes.
	EdgeAlpha uint8
}

// NeuParams bundles per-depth drawing parameters for an interactor class.
// Each [NeuDepth] state has its own parameter sub-struct. Interactors
// select which sub-struct to use based on their current depth.
//
// A nil *NeuParams disables all neumorphic rendering. See
// [NeumorphicParams] for details on the nil convention.
type NeuParams struct {
	// Raised holds parameters for the [Raised] depth state.
	Raised RaisedParams
	// Flush holds parameters for the [Flush] depth state.
	Flush FlushParams
	// Inset holds parameters for the [Inset] depth state.
	Inset InsetParams
}

// GrooveParams are the default [InsetParams] used for thin inset separator
// lines. Used by [std.NeuGroove] and by [std.NOfMChooser] for inter-strip
// groove separators.
var GrooveParams = InsetParams{Off: 1, DarkBlur: 3, LightBlur: 2}

// Animatable is implemented by interactors that participate in rachel's
// animation protocol. AppWindow dispatches animation messages to
// registered Animatable interactors using their local animation ID.
type Animatable interface {
	// AnimationStart is called when an animation with the given local ID begins.
	AnimationStart(localID uint64, startNanos int64)
	// AnimationUpdate is called on each animation tick with elapsed-time fractions.
	AnimationUpdate(localID uint64, startNanos, endNanos int64, coveredStart, coveredEnd float64, nanosSinceStart int64)
	// AnimationFinish is called when the animation ends.
	AnimationFinish(localID uint64, endNanos int64)
}

// SwapRB returns a copy of c with the red and blue channels exchanged.
// Use this when writing directly to a BGR framebuffer with colors that
// did not come from a palette (which pre-swaps its own colors).
func SwapRB(c color.NRGBA) color.NRGBA {
	return color.NRGBA{R: c.B, G: c.G, B: c.R, A: c.A}
}
