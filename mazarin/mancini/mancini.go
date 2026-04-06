package mancini

import (
	"image/color"
)

// Drawer draws itself into the given bounds using a [DrawContext].
// This is the legacy interface — new interactors should implement
// [NewDrawer] instead.
type Drawer interface {
	Draw(dc DrawContext, x, y, w, h float64)
}

// NewDrawer is the standard draw protocol for all interactors. The parent
// passes authoritative x, y, w, h — these override any stale layout
// handle values. The self parameter is the backpointer to the concrete
// type, enabling virtual dispatch for DC(), Visible(), and other
// interface methods. See the package documentation for details on the
// backpointer pattern.
//
// All concrete interactors in [mazzy/mazarin/mancini/std] implement this
// interface.
type NewDrawer interface {
	Draw(self Interactor, x, y, w, h int64)
}

// Layouter is implemented by interactors that have [LayoutAttributes].
// All interactors that embed [impl.Interactor] satisfy this interface
// via the promoted GetLayout method.
type Layouter interface {
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
	Surface() color.NRGBA
	SurfaceTint() color.NRGBA
	DarkShadow() color.NRGBA
	LightShadow() color.NRGBA
	Text() color.NRGBA
	Icon() color.NRGBA
	Highlight() color.NRGBA
	HighlightText() color.NRGBA
	DisabledAlpha() float64
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
	Heavy() *NeuParams
	Light() *NeuParams
}

// RaisedParams controls the dark and light outer shadow layers for the
// [Raised] depth state. DarkOff/LightOff set the shadow offset in pixels;
// DarkBlur/LightBlur set the Gaussian blur radius; DarkAlpha/LightAlpha
// set the shadow opacity.
type RaisedParams struct {
	LightOff, LightBlur   float64
	DarkOff, DarkBlur     float64
	DarkAlpha, LightAlpha uint8
}

// InsetParams controls the inner shadow layers for the [Inset] depth
// state. Off is the shadow offset (dark biased upper-left, light biased
// lower-right). DarkBlur and LightBlur set the Gaussian blur radius for
// each shadow layer. Inner shadows are masked to the shape boundary.
type InsetParams struct {
	Off                 float64
	DarkBlur, LightBlur float64
}

// FlushParams controls the thin edge outline for the [Flush] depth
// state. EdgeW is the stroke width; EdgeAlpha is the opacity of both
// the dark and light edge strokes.
type FlushParams struct {
	EdgeW     float64
	EdgeAlpha uint8
}

// NeuParams bundles per-depth drawing parameters for an interactor class.
// Each [NeuDepth] state has its own parameter sub-struct. Interactors
// select which sub-struct to use based on their current depth.
//
// A nil *NeuParams disables all neumorphic rendering. See
// [NeumorphicParams] for details on the nil convention.
type NeuParams struct {
	Raised RaisedParams
	Flush  FlushParams
	Inset  InsetParams
}

// GrooveParams are the default [InsetParams] used for thin inset separator
// lines. Used by [std.NeuGroove] and by [std.NOfMChooser] for inter-strip
// groove separators.
var GrooveParams = InsetParams{Off: 1, DarkBlur: 3, LightBlur: 2}

// Animatable is implemented by interactors that participate in rachel's
// animation protocol. AppWindow dispatches animation messages to
// registered Animatable interactors using their local animation ID.
type Animatable interface {
	AnimationStart(localID uint64, startNanos int64)
	AnimationUpdate(localID uint64, startNanos, endNanos int64, coveredStart, coveredEnd float64, nanosSinceStart int64)
	AnimationFinish(localID uint64, endNanos int64)
}

// SwapRB returns a copy of c with the red and blue channels exchanged.
// Use this when writing directly to a BGR framebuffer with colors that
// did not come from a palette (which pre-swaps its own colors).
func SwapRB(c color.NRGBA) color.NRGBA {
	return color.NRGBA{R: c.B, G: c.G, B: c.R, A: c.A}
}
