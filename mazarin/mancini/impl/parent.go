package impl

import (
	"mazzy/mazarin/mancini"
)

// dcSetter is satisfied by types that accept a DrawContext (impl.Interactor).
type dcSetter interface {
	SetDC(mancini.DrawContext)
}

// Parent is a mixin for interactors that have children. It discovers
// children via the constraint network: each child's Parent attribute
// names this interactor, and GetChildren uses attr.Find + the registry
// to return them in construction order.
//
// The owning interactor must embed both Interactor and Parent, and
// call Init on Interactor first (which registers it in the registry
// and stores the layout handles).
type Parent struct {
	interactor *Interactor // back-pointer for layout name access
}

// InitParent wires the back-pointer to the embedding Interactor.
// Must be called after Interactor.Init.
func (p *Parent) InitParent(i *Interactor) {
	p.interactor = i
}

// GetChildren discovers children via the constraint network.
// Returns interactors whose Parent attribute matches this interactor's
// constraint-system name, sorted by registration sequence number.
func (p *Parent) GetChildren() []mancini.Interactor {
	if p.interactor == nil || p.interactor.layout == nil {
		return nil
	}
	return mancini.FindChildren(p.interactor.layout.Name())
}

// DrawChildren is the default child-drawing implementation.
// For each child, it propagates the DrawContext from self, then
// calls the child's Draw method if it implements NewDrawer.
// x, y, w, h are the parent's own bounds — children fill the parent
// in this default implementation. Override for custom layout.
func (p *Parent) DrawChildren(self mancini.Interactor, x, y, w, h int64) {
	dc := self.DC()
	for _, child := range p.GetChildren() {
		if cs, ok := child.(dcSetter); ok {
			cs.SetDC(dc)
		}
		if d, ok := child.(mancini.NewDrawer); ok {
			d.Draw(child, x, y, w, h)
		}
	}
}
