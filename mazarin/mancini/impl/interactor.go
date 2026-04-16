package impl

import (
	"fmt"
	"image"
	"image/color"

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

// ReInitializeOwner replaces the backpointer and re-registers with the
// new owner. Used when a subclass wraps an already-initialized interactor
// and needs dispatch to resolve to the subclass's methods.
func (i *Interactor) ReInitializeOwner(newOwner mancini.Interactor) {
	i.owner = newOwner
	if i.layout != nil {
		mancini.RegisterInteractor(i.layout.Name(), newOwner)
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

// ClearDamage resets this interactor's damage rectangle to empty.
// For leaves (value DamageRect), this directly clears the value.
// For parents (constraint DamageRect), this is a no-op — the
// constraint re-evaluates to empty when all children clear.
func (i *Interactor) ClearDamage() {
	lh := i.layout
	if lh == nil {
		return
	}
	lh.ClearDamage()
}

// SnapshotDamage copies current Bounds, Visible, and BoundsHash into
// "last-painted" mirror attributes. The parent damage constraint
// compares current state to LP state — when they match, ownDamage
// stays empty and child damage passes through untouched. Call this
// after drawing an interactor so the next evaluation sees no change.
func (i *Interactor) SnapshotDamage() {
	lh := i.layout
	if lh == nil {
		return
	}
	lh.SnapshotDamage()
}

// BoundsRect returns the interactor's bounds as an image.Rectangle
// computed from layout X, Y, Width, Height. Returns empty rect if
// no layout is available.
func (i *Interactor) BoundsRect() image.Rectangle {
	lh := i.layout
	if lh == nil {
		return image.Rectangle{}
	}
	x, y := int(lh.X.Get()), int(lh.Y.Get())
	return image.Rect(x, y, x+int(lh.Width.Get()), y+int(lh.Height.Get()))
}

// Damaged intersects the damage rectangle with this interactor's bounds
// (from layout X, Y, Width, Height). Returns true if the intersection
// is non-empty — meaning this interactor overlaps the damaged region
// and should repaint.
func (i *Interactor) Damaged(damage image.Rectangle) bool {
	lh := i.layout
	if lh == nil {
		return true // no layout → always draw (safe default)
	}
	return !damage.Intersect(i.BoundsRect()).Empty()
}

// UnionDamage returns the union of prev (the interactor's bounds before
// a size/position change) and the interactor's current bounds. Use this
// when a child moves or resizes: save BoundsRect() before the change,
// perform the change, then call UnionDamage(saved) to get the damage
// rect that covers both old and new positions.
func (i *Interactor) UnionDamage(prev image.Rectangle) image.Rectangle {
	return prev.Union(i.BoundsRect())
}

// DrawSelfOpaque intersects the damage rectangle with this interactor's
// bounds and fills the intersection with the given color. Designed for
// interactors that always show a painted background (themed controls,
// opaque containers).
func (i *Interactor) DrawSelfOpaque(damage image.Rectangle, c color.NRGBA) {
	if c.A == 0 {
		return
	}
	r := damage.Intersect(i.BoundsRect())
	if r.Empty() {
		return
	}
	dc := i.dc
	if dc == nil {
		return
	}
	dc.SetColor(c)
	dc.FillRectangle(float64(r.Min.X), float64(r.Min.Y),
		float64(r.Dx()), float64(r.Dy()))
}

func (i *Interactor) X() int64       { return i.layout.X.Get() }
func (i *Interactor) Y() int64       { return i.layout.Y.Get() }
func (i *Interactor) W() int64       { return i.layout.Width.Get() }
func (i *Interactor) H() int64       { return i.layout.Height.Get() }
func (i *Interactor) Visible() bool  { return i.layout.Visible.Get() }
func (i *Interactor) SetVisible(v bool) { i.layout.Visible.Set(v) }
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
// Returns front-to-back order (deepest children first).
//
// The parent walks its children last-to-first (top z-order first). For
// each child the parent pre-filters against that child's bounds in the
// parent's coordinate frame: if the click does not overlap the child
// there is no reason to recurse into it. Invisible children are also
// skipped. When a child does overlap, the parent transforms the click
// into the child's local frame (subtracting the child's x,y within the
// parent) before recursing.
//
// A child's bounds may legitimately extend outside the parent's own
// bounds (e.g. a GridTable divider whose marker overhangs above and
// below the grid). The parent still recurses into such children based
// on the child's own bounds — it does NOT pre-filter by its own bounds.
//
// Self is appended to the result only if the click is within this
// interactor's local bounds and DetailedHit (if implemented) returns
// true.
//
// Concrete types (e.g., Scroller) may override Pick to apply additional
// coordinate transforms before recursing into children.
func (i *Interactor) Pick(localX, localY int64) []mancini.Interactor {
	lh := i.layout
	if lh == nil {
		return nil
	}
	if !i.owner.Visible() {
		return nil
	}

	iw, ih := lh.Width.Get(), lh.Height.Get()
	ix, iy := lh.X.Get(), lh.Y.Get()

	var result []mancini.Interactor

	// Recurse into children (last-to-first for front-to-back z-order).
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

			// Child bounds in our (parent) local frame.
			childRelX := clh.X.Get() - ix
			childRelY := clh.Y.Get() - iy
			cw, ch := clh.Width.Get(), clh.Height.Get()

			// Pre-filter: skip if the click does not fall inside the
			// child's bounds (child bounds may extend outside our own).
			if localX < childRelX || localX >= childRelX+cw ||
				localY < childRelY || localY >= childRelY+ch {
				continue
			}
			// Skip invisible children — they cannot be pick targets.
			if !child.Visible() {
				continue
			}

			// Transform to the child's local frame and recurse.
			childLocalX := localX - childRelX
			childLocalY := localY - childRelY
			if picker, ok := child.(mancini.Picker); ok {
				result = append(result, picker.Pick(childLocalX, childLocalY)...)
			}
		}
	}

	// Add self only if the click is within our own local bounds and,
	// for interactors with a shape-based hit region, DetailedHit passes.
	if localX < 0 || localX >= iw || localY < 0 || localY >= ih {
		return result
	}
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

