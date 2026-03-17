package neu

import (
	"image"
	"image/color"
	"math"

	"golang.org/x/image/font"
)

// Drawer draws itself into the given bounds on the canvas.
type Drawer interface {
	Draw(canvas *image.RGBA, x, y, w, h float64)
}

// FaceDrawer draws content onto the face of a neumorphic box.
// It satisfies the Drawer interface, so any rendering function can serve
// as a leaf node in a Drawer tree.
type FaceDrawer func(canvas *image.RGBA, x, y, w, h float64)

// Draw implements the Drawer interface.
func (f FaceDrawer) Draw(canvas *image.RGBA, x, y, w, h float64) {
	f(canvas, x, y, w, h)
}

// NeuDepth represents the neumorphic depth of a widget relative to the surface.
type NeuDepth int

const (
	Raised NeuDepth = iota // Proud of the surface — casts outer shadows
	Flush                  // Level with the surface — thin edge outline only
	Inset                  // Recessed into the surface — inner shadows
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

// Palette holds the colors for a neumorphic theme.
type Palette struct {
	Surface     color.NRGBA
	SurfaceTint color.NRGBA
	DarkSh      color.NRGBA
	LightSh     color.NRGBA
	Text        color.NRGBA
	Icon        color.NRGBA
}

// DefaultPalette returns the standard neumorphic purple palette.
func DefaultPalette() Palette {
	return Palette{
		Surface:     color.NRGBA{232, 230, 244, 255},
		SurfaceTint: color.NRGBA{218, 213, 237, 255},
		DarkSh:      color.NRGBA{176, 173, 195, 255},
		LightSh:     color.NRGBA{255, 255, 255, 255},
		Text:        color.NRGBA{78, 72, 112, 255},
		Icon:        color.NRGBA{105, 99, 148, 235},
	}
}

// Theme holds the rendering configuration for a neumorphic UI.
type Theme struct {
	Pal         Palette
	FontRegular string // path to regular-weight font file (slicer)
	FontBold    string // path to bold-weight font file (slicer)
	// FontLoader creates a font.Face at the given size. Used when fonts are
	// embedded (kernel/priest) rather than loaded from file paths. If non-nil,
	// takes precedence over FontRegular/FontBold.
	FontLoader  func(bold bool, size float64) font.Face
	ScaleFactor float64
	SwapRB      bool // true for BGRA GPU framebuffers
}

// Px converts logical pixels to device pixels.
func (t *Theme) Px(logical float64) float64 {
	return math.Round(logical * t.ScaleFactor)
}

// Pxi returns Px as an integer.
func (t *Theme) Pxi(logical float64) int {
	return int(t.Px(logical))
}

// C returns c with R and B swapped when SwapRB is true. Pass all colors
// through C() before handing them to gg so that BGRA framebuffers get
// correct channel ordering.
func (t *Theme) C(c color.NRGBA) color.NRGBA {
	if t.SwapRB {
		return color.NRGBA{R: c.B, G: c.G, B: c.R, A: c.A}
	}
	return c
}

// Neumorphic parameter types.

type RaisedParams struct {
	LightOff, LightBlur   float64
	DarkOff, DarkBlur     float64
	DarkAlpha, LightAlpha uint8
}

type InsetParams struct {
	Off                 float64
	DarkBlur, LightBlur float64
}

type FlushParams struct {
	EdgeW     float64
	EdgeAlpha uint8
}

// NeuParams bundles per-depth drawing parameters for a widget class.
type NeuParams struct {
	Raised RaisedParams
	Flush  FlushParams
	Inset  InsetParams
}

// ButtonParams are lighter shadows for interactive controls.
var ButtonParams = NeuParams{
	Raised: RaisedParams{LightOff: 2, LightBlur: 4, DarkOff: 7, DarkBlur: 7, DarkAlpha: 90, LightAlpha: 250},
	Flush:  FlushParams{EdgeW: 2, EdgeAlpha: 140},
	Inset:  InsetParams{Off: 2, DarkBlur: 5, LightBlur: 3},
}

// WindowParams are heavier shadows for window-level containers.
var WindowParams = NeuParams{
	Raised: RaisedParams{LightOff: 4, LightBlur: 8, DarkOff: 14, DarkBlur: 14, DarkAlpha: 120, LightAlpha: 255},
	Flush:  FlushParams{EdgeW: 2, EdgeAlpha: 140},
	Inset:  InsetParams{Off: 3, DarkBlur: 7, LightBlur: 4},
}

// GrooveParams are used for thin inset separator lines.
var GrooveParams = InsetParams{Off: 1, DarkBlur: 3, LightBlur: 2}
