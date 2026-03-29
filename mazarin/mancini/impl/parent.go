package impl

import (
	"mazzy/mazarin/mancini"
)

// dcSetter is satisfied by types that accept a [mancini.DrawContext]
// ([Interactor]).
type dcSetter interface {
	SetDC(mancini.DrawContext)
}

// Parent is a mixin for interactors that have children. It implements
// [mancini.Parent] by discovering children through the constraint
// network: each child's Parent layout attribute names this interactor,
// and GetChildren uses the global interactor registry (see
// [mancini.FindChildren]) to return them in construction order.
//
// # Embedding
//
// Container interactors embed both [Interactor] and Parent:
//
//	type Column struct {
//	    impl.Interactor  // X(), Y(), W(), H(), DC(), ...
//	    impl.Parent      // GetChildren(), DrawChildren()
//	    // ...
//	}
//
// [Decorator] embeds Parent internally, so decorator types do not need
// to embed it separately.
//
// # Initialization
//
// InitParent must be called after [Interactor.Init], because it needs
// the Interactor's layout name to discover children:
//
//	c.Interactor.Init(c, layout)
//	c.Parent.InitParent(&c.Interactor)
//
// # Concrete Types That Use Parent
//
// [std.Column], [std.Row], [std.ColumnOutsideIn] (embed Parent directly).
// [std.NeuBox], [std.NeuCircle], [std.AppWindow], [std.FreeFloatingWindow]
// (via [Decorator]).
type Parent struct {
	interactor *Interactor // back-pointer for layout name access
}

// InitParent wires the back-pointer to the embedding [Interactor].
// Must be called after [Interactor.Init] so the layout name is available
// for child discovery.
func (p *Parent) InitParent(i *Interactor) {
	p.interactor = i
}

// GetChildren discovers children via the constraint network. Returns
// all [mancini.Interactor] instances whose Parent layout attribute
// matches this interactor's constraint-system name, sorted by
// registration sequence number (construction order).
func (p *Parent) GetChildren() []mancini.Interactor {
	if p.interactor == nil || p.interactor.layout == nil {
		return nil
	}
	return mancini.FindChildren(p.interactor.layout.Name())
}

// DrawChildren is the default child-drawing implementation. For each
// child discovered by GetChildren, it propagates the [mancini.DrawContext]
// from self via SetDC, then calls the child's [mancini.NewDrawer.Draw]
// method. Children receive the parent's own bounds (x, y, w, h) — they
// fill the parent in this default implementation.
//
// Container interactors like [std.Column] and [std.Row] override this
// with custom layout logic that computes per-child positions.
// [Decorator] does not use DrawChildren at all — it handles its single
// child directly in [Decorator.Draw].
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
