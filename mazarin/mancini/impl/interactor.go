package impl

import "mazzy/mazarin/mancini"

// Interactor is the base "class" for all UI elements.
// Concrete types embed this to get X(), Y(), W(), H(), Visible(), DC().
type Interactor struct {
	owner  mancini.Interactor
	layout *mancini.LayoutHandles
	dc     mancini.DrawContext
}

// Init wires the back-pointer and layout. Must be called from the
// concrete type's constructor: i.Interactor.Init(i, layout).
// Registers the owner in the global interactor registry keyed by
// the layout handle's constraint-system name.
func (i *Interactor) Init(owner mancini.Interactor, layout *mancini.LayoutHandles) {
	i.owner = owner
	i.layout = layout
	if layout != nil {
		mancini.RegisterInteractor(layout.Name(), owner)
	}
}

func (i *Interactor) X() int64       { return i.layout.X.Get() }
func (i *Interactor) Y() int64       { return i.layout.Y.Get() }
func (i *Interactor) W() int64       { return i.layout.Width.Get() }
func (i *Interactor) H() int64       { return i.layout.Height.Get() }
func (i *Interactor) Visible() bool  { return i.layout.Visible.Get() != 0 }
func (i *Interactor) DC() mancini.DrawContext { return i.dc }

// SetDC sets the DrawContext for this interactor. Called once per draw
// pass before the tree walk begins.
func (i *Interactor) SetDC(dc mancini.DrawContext) { i.dc = dc }

// Owner returns the back-pointer to the embedding concrete type.
func (i *Interactor) Owner() mancini.Interactor { return i.owner }

// Layout returns the underlying LayoutHandles for constraint access.
func (i *Interactor) Layout() *mancini.LayoutHandles { return i.layout }

// GetLayout satisfies the mancini.Layouter interface.
func (i *Interactor) GetLayout() *mancini.LayoutHandles { return i.layout }
