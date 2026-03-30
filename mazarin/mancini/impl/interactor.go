package impl

import "mazzy/mazarin/mancini"

// Interactor is the base "class" for all UI elements.
// Concrete types embed this to get X(), Y(), W(), H(), Visible(), DC().
type Interactor struct {
	owner  mancini.Interactor
	layout *mancini.LayoutAttributes
	dc     mancini.DrawContext
}

// Initialize wires the back-pointer and layout. Must be called from the
// concrete type's constructor: i.Interactor.Initialize(i, layout).
// Registers the owner in the global interactor registry keyed by
// the layout attribute's constraint-system name, then marks the
// interactor's full bounds as damaged so the first draw paints everything.
func (i *Interactor) Initialize(owner mancini.Interactor, layout *mancini.LayoutAttributes) {
	i.owner = owner
	i.layout = layout
	if layout != nil {
		mancini.RegisterInteractor(layout.Name(), owner)
	}
	i.FullDamage()
}

// FullDamage sets the damage rectangle to the interactor's full bounds,
// ensuring the next draw pass repaints this interactor completely.
func (i *Interactor) FullDamage() {
	if i.layout != nil {
		i.layout.FullDamage()
	}
}

func (i *Interactor) X() int64       { return i.layout.X.Get() }
func (i *Interactor) Y() int64       { return i.layout.Y.Get() }
func (i *Interactor) W() int64       { return i.layout.Width.Get() }
func (i *Interactor) H() int64       { return i.layout.Height.Get() }
func (i *Interactor) Visible() bool  { return i.layout.Visible.Get() }
func (i *Interactor) DC() mancini.DrawContext { return i.dc }

// SetDC sets the DrawContext for this interactor. Called once per draw
// pass before the tree walk begins.
func (i *Interactor) SetDC(dc mancini.DrawContext) { i.dc = dc }

// Owner returns the back-pointer to the embedding concrete type.
func (i *Interactor) Owner() mancini.Interactor { return i.owner }

// Layout returns the underlying LayoutAttributes for constraint access.
func (i *Interactor) Layout() *mancini.LayoutAttributes { return i.layout }

// GetLayout satisfies the mancini.Layouter interface.
func (i *Interactor) GetLayout() *mancini.LayoutAttributes { return i.layout }
