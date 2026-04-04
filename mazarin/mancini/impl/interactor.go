package impl

import (
	"fmt"

	"mazzy/mazarin/mancini"
)

// Interactor is the root base type for all UI elements in the Mancini
// toolkit. Concrete interactor types embed this struct (directly or via
// [ThemedInteractor]) to inherit position, size, visibility, and drawing
// context accessors.
//
// # Promoted Methods
//
// Embedding Interactor promotes: X, Y, W, H, Visible, DC, SetDC,
// Owner, Layout, and GetLayout.
//
// # Backpointer
//
// Interactor stores a backpointer ("owner") to the outermost concrete
// type. This enables virtual dispatch in the Draw protocol: when a
// parent calls child.Draw(child, ...), the child parameter is the
// concrete type, so self.DC() and self.Visible() resolve correctly.
// See [Initialize] for how the backpointer is established.
//
// # Embedding Hierarchy
//
// Concrete types embed one of:
//
//   - Interactor directly — for non-themed leaves ([std.Clock], [std.Spacer])
//     and containers ([std.Column], [std.Row])
//   - [ThemedInteractor] — for themed controls ([std.Button], [std.Label],
//     [std.Checkbox], [std.Scrollbar], etc.)
//   - [Decorator] — for single-child wrappers ([std.NeuBox], [std.NeuCircle],
//     [std.AppWindow], [std.FreeFloatingWindow])
type Interactor struct {
	owner  mancini.Interactor
	layout *mancini.LayoutAttributes
	dc     mancini.DrawContext
}

// Initialize wires the backpointer and layout attributes. Must be called
// from the concrete type's constructor, passing the concrete type as owner:
//
//	b := &Button{...}
//	b.Interactor.Initialize(b, layout)   // b is the backpointer
//
// Initialize also registers the interactor in the global registry (see
// [mancini.RegisterInteractor]) keyed by the layout's constraint-system
// name, enabling child discovery by [Parent.GetChildren].
//
// Initialize does NOT set up damage tracking. Leaf interactors must call
// [FullDamage] themselves (e.g. via [ThemedInteractor.Initialize]).
// Parent interactors get damage from [Parent.Initialize], which installs
// a constraint that unions children's damage rectangles.
//
// For themed interactors, call [ThemedInteractor.Initialize] instead,
// which calls this method internally.
func (i *Interactor) Initialize(owner mancini.Interactor, layout *mancini.LayoutAttributes) {
	i.owner = owner
	i.layout = layout
	if layout != nil {
		mancini.RegisterInteractor(layout.Name(), owner)
	}
}

// FullDamage marks this interactor as needing a complete repaint.
// It sets the DamageRect to the interactor's full Bounds. Called
// automatically by Initialize to ensure the first draw paints
// everything; also available for input handlers that need to force
// a full repaint (e.g., a clock face on tick).
func (i *Interactor) FullDamage() {
	lh := i.layout
	if lh == nil {
		return
	}
	lh.FullDamage()
}

func (i *Interactor) X() int64       { return i.layout.X.Get() }
func (i *Interactor) Y() int64       { return i.layout.Y.Get() }
func (i *Interactor) W() int64       { return i.layout.Width.Get() }
func (i *Interactor) H() int64       { return i.layout.Height.Get() }
func (i *Interactor) Visible() bool  { return i.layout.Visible.Get() }
func (i *Interactor) DC() mancini.DrawContext { return i.dc }

// ScreenCoordConvertTo converts interactor-local coordinates to screen
// coordinates. (0,0) returns the screen position of this interactor's
// top-left corner.
func (i *Interactor) ScreenCoordConvertTo(localX, localY int64) (int64, int64) {
	ox, oy := mancini.ScreenOrigin()
	return localX + i.layout.X.Get() + ox, localY + i.layout.Y.Get() + oy
}

// ScreenCoordConvertFrom converts screen-absolute coordinates to this
// interactor's local coordinate frame.
func (i *Interactor) ScreenCoordConvertFrom(screenX, screenY int64) (int64, int64) {
	ox, oy := mancini.ScreenOrigin()
	return screenX - i.layout.X.Get() - ox, screenY - i.layout.Y.Get() - oy
}

// SetDC sets the [mancini.DrawContext] for this interactor. Called by
// parent interactors during the draw pass to propagate the drawing
// surface down the tree before calling Draw.
func (i *Interactor) SetDC(dc mancini.DrawContext) { i.dc = dc }

// Owner returns the backpointer to the outermost concrete type.
// Used internally by [Decorator.DecorateIfNeeded] to perform virtual
// dispatch on the [mancini.Decoratable] interface.
func (i *Interactor) Owner() mancini.Interactor { return i.owner }

// Layout returns the underlying [mancini.LayoutAttributes] for direct
// constraint access. Callers should prefer GetLayout for interface
// compatibility.
func (i *Interactor) Layout() *mancini.LayoutAttributes { return i.layout }

// GetLayout satisfies the [mancini.Layouter] interface, returning the
// [mancini.LayoutAttributes] that publish this interactor's position,
// size, and visibility in the constraint network.
func (i *Interactor) GetLayout() *mancini.LayoutAttributes { return i.layout }

// Pick performs hit testing in this interactor's local coordinate frame.
// It checks bounds, recurses into children (last-to-first for z-order),
// and appends this interactor if hit. Returns front-to-back order
// (deepest children first).
//
// Concrete types (e.g., Scroller) may override Pick to apply coordinate
// transforms before recursing into children.
func (i *Interactor) Pick(localX, localY int64) []mancini.Interactor {
	lh := i.layout
	if lh == nil {
		return nil
	}
	iw, ih := lh.Width.Get(), lh.Height.Get()
	if localX < 0 || localX >= iw || localY < 0 || localY >= ih {
		if mancini.PickDebugEnabled() {
			fmt.Printf("[pick] %s size=(%dx%d) MISS for local=(%d,%d)\n",
				lh.Name(), iw, ih, localX, localY)
		}
		return nil
	}
	if !i.owner.Visible() {
		return nil
	}

	var result []mancini.Interactor

	// Recurse into children (last-to-first for front-to-back z-order).
	ix, iy := lh.X.Get(), lh.Y.Get()
	if p, ok := i.owner.(mancini.Parent); ok {
		children := p.GetChildren()
		for j := len(children) - 1; j >= 0; j-- {
			child := children[j]
			cl, cok := child.(mancini.Layouter)
			if !cok {
				continue
			}
			clh := cl.GetLayout()
			if clh == nil {
				continue
			}
			childRelX := clh.X.Get() - ix
			childRelY := clh.Y.Get() - iy
			childLocalX := localX - childRelX
			childLocalY := localY - childRelY
			if picker, ok := child.(mancini.Picker); ok {
				result = append(result, picker.Pick(childLocalX, childLocalY)...)
			}
		}
	}

	// DetailedHit for fine-grained testing (e.g., circular regions).
	if dh, ok := i.owner.(mancini.DetailedHit); ok {
		if !dh.DetailedHit(localX, localY) {
			if mancini.PickDebugEnabled() {
				fmt.Printf("[pick] %s DetailedHit REJECTED local=(%d,%d)\n",
					lh.Name(), localX, localY)
			}
			return result
		}
	}

	result = append(result, i.owner)
	return result
}

