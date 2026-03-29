package mancini

import (
	"image/color"
)

// Drawer draws itself into the given bounds using a DrawContext.
// This is the legacy interface — new interactors should implement
// NewDrawer instead. Remove once all interactors are migrated.
type Drawer interface {
	Draw(dc DrawContext, x, y, w, h float64)
}

// NewDrawer draws itself into the given bounds. The parent passes
// authoritative x, y, w, h — these override any stale layout handle values.
// self provides access to the DrawContext and identity for virtual dispatch.
type NewDrawer interface {
	Draw(self Interactor, x, y, w, h int64)
}

// Layouter is implemented by interactors that have layout attributes.
type Layouter interface {
	GetLayout() *LayoutAttributes
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

// Palette provides colors for a neumorphic theme.
type Palette interface {
	Surface() color.NRGBA
	SurfaceTint() color.NRGBA
	DarkShadow() color.NRGBA
	LightShadow() color.NRGBA
	Text() color.NRGBA
	Icon() color.NRGBA
	Highlight() color.NRGBA
	HighlightText() color.NRGBA
	DisabledAlpha() float64
	SwapRB() bool
}

// NeumorphicParams provides heavy and light shadow parameter bundles.
// Either method may return nil to disable neumorphic rendering for
// that weight class; callers must handle nil gracefully.
type NeumorphicParams interface {
	Heavy() *NeuParams
	Light() *NeuParams
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

// GrooveParams are used for thin inset separator lines.
var GrooveParams = InsetParams{Off: 1, DarkBlur: 3, LightBlur: 2}
