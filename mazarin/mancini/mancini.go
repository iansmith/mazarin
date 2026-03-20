package mancini

import (
	"image/color"
)

// Drawer draws itself into the given bounds using a DrawContext.
// The context is created once per draw pass at the top level and threaded
// down through the tree. Parents may wrap dc in a ClippedContext before
// calling children to enforce clipping.
type Drawer interface {
	Draw(dc DrawContext, x, y, w, h float64)
}

// Layouter is implemented by interactors that have layout handles.
type Layouter interface {
	GetLayout() *LayoutHandles
}

// FaceDrawer draws content onto the face of a neumorphic box.
// It satisfies the Drawer interface, so any rendering function can serve
// as a leaf node in a Drawer tree.
type FaceDrawer func(dc DrawContext, x, y, w, h float64)

// Draw implements the Drawer interface.
func (f FaceDrawer) Draw(dc DrawContext, x, y, w, h float64) {
	f(dc, x, y, w, h)
}

// NeuDepth represents the neumorphic depth of an interactor relative to the surface.
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

// Palette holds the colors for a neumorphic theme and the framebuffer
// color-channel configuration needed to render them correctly.
type Palette struct {
	Surface     color.NRGBA
	SurfaceTint color.NRGBA
	DarkSh      color.NRGBA
	LightSh     color.NRGBA
	Text        color.NRGBA
	Icon        color.NRGBA
	SwapRB      bool // true for BGRA GPU framebuffers
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

// NeuParams bundles per-depth drawing parameters for an interactor class.
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
