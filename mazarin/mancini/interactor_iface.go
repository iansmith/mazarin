package mancini

import (
	"image"
	"image/color"
)

// Interactor is the base interface for all UI elements in the tree.
// Every concrete interactor type satisfies this interface via struct
// embedding of [impl.Interactor] or [impl.ThemedInteractor].
//
// Position (X, Y) and size (W, H) are read from [LayoutAttributes]
// published in the constraint network. [DrawContext] is set by the
// parent before each draw pass via SetDC.
type Interactor interface {
	X() int64
	Y() int64
	W() int64
	H() int64
	Visible() bool
	DC() DrawContext

	// ScreenCoordConvertTo converts a point in this interactor's local
	// coordinate frame to screen-absolute coordinates. Passing (0,0)
	// returns the screen position of this interactor's top-left corner.
	ScreenCoordConvertTo(localX, localY int64) (int64, int64)

	// ScreenCoordConvertFrom converts screen-absolute coordinates to
	// this interactor's local coordinate frame.
	ScreenCoordConvertFrom(screenX, screenY int64) (int64, int64)
}

// ThemedInteractor extends [Interactor] with access to the [Theme]'s
// colors and fonts. The standard implementation is [impl.ThemedInteractor],
// which most leaf interactors and controls embed.
type ThemedInteractor interface {
	Interactor
	BgColor() color.NRGBA
	FgColor() color.NRGBA
	Font(feature Feature, size int64) *FontConfig
	DefaultFont() *FontConfig
	DefaultSize() int64
}

// Parent is an [Interactor] that has children. [impl.Parent] provides
// a default implementation that discovers children via the constraint
// network (each child's Parent attribute names this interactor) and
// draws them by propagating the [DrawContext] and calling each child's
// [NewDrawer.Draw].
//
// Container interactors ([std.Column], [std.Row]) embed [impl.Parent]
// but override DrawChildren with custom layout logic. Decorator
// interactors ([impl.Decorator]) use GetChildren but handle the single
// child directly in their own Draw method.
type Parent interface {
	GetChildren() []Interactor
	DrawChildren(self Interactor, x, y, w, h int64, damage image.Rectangle)

	// IsRectSingleChild returns the child that completely contains rect,
	// or nil if no single child does. When non-nil, the parent can skip
	// its own drawing and forward Draw directly to that child.
	IsRectSingleChild(rect image.Rectangle) Interactor

	// SmallestDraw returns the parts of rect not covered by any child.
	// These are the background strips the parent must repaint.
	SmallestDraw(rect image.Rectangle) []image.Rectangle
}

// SimpleParentDraw is the draw protocol for parent interactors that
// want damage-aware drawing. DrawChildren orchestrates the draw pass:
// it uses [Parent.IsRectSingleChild] and [Parent.SmallestDraw] to
// minimize work. DrawSelf paints the parent's own content (typically
// background fill) for a single rectangle.
//
// [impl.Parent] provides a default implementation where DrawSelf is a
// no-op. [impl.ThemedInteractor] implements DrawSelf to fill with the
// theme's Surface color. Concrete parent types can override DrawSelf
// for custom background rendering.
type SimpleParentDraw interface {
	DrawChildren(self Interactor, x, y, w, h int64, damage image.Rectangle)
	DrawSelf(dc DrawContext, rect image.Rectangle)
}

// Picker performs hit testing on an interactor and its children.
// Pick receives coordinates in the interactor's LOCAL frame
// (0,0 = top-left of this interactor). It returns all hit interactors
// front-to-back (deepest children first, then ancestors).
//
// The default implementation on [impl.Interactor] handles both leaf
// interactors (simple bounds check) and parents (recursive traversal
// converting coordinates to each child's local frame). Concrete types
// like [std.Scroller] override Pick to apply coordinate transforms
// (e.g., adding a scroll offset before recursing into the child).
type Picker interface {
	Pick(localX, localY int64) []Interactor
}

// FocusClaimer is implemented by interactors that can claim in-app
// keyboard focus. Children call FocusClaimer.SetFocusToSelf() on
// their parent to request focus when clicked or otherwise activated.
type FocusClaimer interface {
	SetFocusToSelf()
}

// Decoratable is implemented by types that customize the visual
// decoration drawn by [impl.Decorator]. The default [impl.Decorator]
// draws a thick black box; [std.NeuBox] and [std.NeuCircle] override
// Decorate to draw neumorphic shadows, and [std.AppWindow] draws both
// shadows and a title bar.
//
// x, y, w, h are the decorator's authoritative bounds from its parent.
type Decoratable interface {
	Decorate(self Interactor, x, y, w, h int64)
}
