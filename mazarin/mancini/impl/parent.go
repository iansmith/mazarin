package impl

import (
	"mazzy/mazarin/mancini"
)

// dcSetter is satisfied by types that accept a DrawContext (impl.Interactor).
type dcSetter interface {
	SetDC(mancini.DrawContext)
}

// Parent is a mixin for interactors that have children. It provides
// the default DrawChildren implementation: propagate DC, then call
// each child's Draw. Complex parents (like Decorator) embed Parent
// for GetChildren/AddChild but do their own drawing.
type Parent struct {
	children []mancini.Interactor
}

// AddChild appends a child interactor.
func (p *Parent) AddChild(child mancini.Interactor) {
	p.children = append(p.children, child)
}

// GetChildren returns the child list.
func (p *Parent) GetChildren() []mancini.Interactor {
	return p.children
}

// DrawChildren is the default child-drawing implementation.
// For each child, it propagates the DrawContext from self, then
// calls the child's Draw method if it implements NewDrawer.
// Parents that also implement Parent recurse automatically —
// their Draw calls DrawChildren internally.
func (p *Parent) DrawChildren(self mancini.Interactor) {
	dc := self.DC()
	for _, child := range p.children {
		if cs, ok := child.(dcSetter); ok {
			cs.SetDC(dc)
		}
		if d, ok := child.(mancini.NewDrawer); ok {
			d.Draw(child)
		}
	}
}
