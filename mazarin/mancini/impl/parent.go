package impl

import (
	"math"

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
// Initialize must be called after [Interactor.Initialize], because it
// needs the Interactor's layout name to discover children:
//
//	c.Interactor.Initialize(c, layout)
//	c.Parent.Initialize(true, &c.Interactor)
//
// # Concrete Types That Use Parent
//
// [std.Column], [std.Row], [std.ColumnOutsideIn] (embed Parent directly).
// [std.NeuBox], [std.NeuCircle], [std.AppWindow], [std.FreeFloatingWindow]
// (via [Decorator]).
type Parent struct {
	interactor *Interactor // back-pointer for layout name access
}

// Initialize wires the back-pointer to the embedding [Interactor].
// Must be called after [Interactor.Initialize] so the layout name is
// available for child discovery. If wantDefaultDamageConstraint is true,
// [mancini.InitDefaultParentDamage] is called on the interactor's layout
// to set up a damage constraint that unions the parent's own damage with
// the first child's damage rectangle.
func (p *Parent) Initialize(wantDefaultDamageConstraint bool, i *Interactor) {
	p.interactor = i
	if wantDefaultDamageConstraint {
		mancini.InitDefaultParentDamage(i.layout)
	}
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

// parentName returns this parent's constraint-system name.
func (p *Parent) parentName() string {
	if p.interactor == nil || p.interactor.layout == nil {
		return ""
	}
	return p.interactor.layout.Name()
}

// assertParent checks that child has no parent declared in the constraint
// system. If it does, panics. Otherwise sets the parent to this parent.
func (p *Parent) assertParent(child mancini.Interactor) {
	l, ok := child.(mancini.Layouter)
	if !ok {
		panic("AddChild: child does not implement Layouter")
	}
	lh := l.GetLayout()
	if lh == nil {
		panic("AddChild: child has nil layout")
	}
	if lh.Parent != nil && lh.Parent.Get() != "" {
		panic("AddChild: child " + lh.Name() + " already has parent " + lh.Parent.Get())
	}
	lh.Parent.Set(p.parentName())
}

// clearParent removes the parent relationship for the given interactor.
func clearParent(child mancini.Interactor) {
	if l, ok := child.(mancini.Layouter); ok {
		if lh := l.GetLayout(); lh != nil && lh.Parent != nil {
			lh.Parent.Set("")
		}
	}
}

// AddChildFirst adds child as the first (lowest sequence number) child
// of this parent. If the child's sequence number is already lower than
// all existing children, it is simply parented. Otherwise its sequence
// number is swapped with the current first child.
//
// Panics if child already has a parent.
func (p *Parent) AddChildFirst(child mancini.Interactor) {
	p.assertParent(child)

	childName := mancini.InteractorName(child)
	if childName == "" {
		return
	}
	childSeq, ok := mancini.RegistrySeq(childName)
	if !ok {
		return
	}

	// Find current first child (lowest seq).
	children := p.GetChildren()
	if len(children) <= 1 {
		// Only child (the one we just parented), nothing to swap.
		return
	}

	var firstChild mancini.Interactor
	var firstName string
	var firstSeq uint64 = math.MaxUint64
	for _, c := range children {
		n := mancini.InteractorName(c)
		if n == childName {
			continue // skip the new child
		}
		if s, ok := mancini.RegistrySeq(n); ok && s < firstSeq {
			firstSeq = s
			firstChild = c
			firstName = n
		}
	}

	if firstChild == nil || childSeq < firstSeq {
		// Already first, nothing to swap.
		return
	}

	// Swap sequence numbers so the new child sorts first.
	mancini.SwapSequence(childName, firstName)
}

// AddChildLast adds child as the last (highest sequence number) child
// of this parent. If the child's sequence number is already higher than
// all existing children, it is simply parented. Otherwise its sequence
// number is swapped with the current last child.
//
// Panics if child already has a parent.
func (p *Parent) AddChildLast(child mancini.Interactor) {
	p.assertParent(child)

	childName := mancini.InteractorName(child)
	if childName == "" {
		return
	}
	childSeq, ok := mancini.RegistrySeq(childName)
	if !ok {
		return
	}

	// Find current last child (highest seq).
	children := p.GetChildren()
	if len(children) <= 1 {
		// Only child, nothing to swap.
		return
	}

	var lastChild mancini.Interactor
	var lastName string
	var lastSeq uint64
	for _, c := range children {
		n := mancini.InteractorName(c)
		if n == childName {
			continue
		}
		if s, ok := mancini.RegistrySeq(n); ok && s > lastSeq {
			lastSeq = s
			lastChild = c
			lastName = n
		}
	}

	if lastChild == nil || childSeq > lastSeq {
		// Already last, nothing to swap.
		return
	}

	// Swap sequence numbers so the new child sorts last.
	mancini.SwapSequence(childName, lastName)
}

// DeleteAllChildren removes all children from this parent by clearing
// each child's Parent attribute. Returns the removed children in
// sequence order. Returns an empty (non-nil) slice if no children exist.
func (p *Parent) DeleteAllChildren() []mancini.Interactor {
	children := p.GetChildren()
	if len(children) == 0 {
		return []mancini.Interactor{}
	}
	for _, c := range children {
		clearParent(c)
	}
	return children
}

// DeleteFirst removes the first child (lowest sequence number) from this
// parent and returns it. Returns nil if the parent has no children.
// The child's Parent attribute is cleared.
func (p *Parent) DeleteFirst() mancini.Interactor {
	children := p.GetChildren()
	if len(children) == 0 {
		return nil
	}

	// Children are already sorted by seq, so first is index 0.
	first := children[0]
	clearParent(first)
	return first
}

// DeleteLast removes the last child (highest sequence number) from this
// parent and returns it. Returns nil if the parent has no children.
// The child's Parent attribute is cleared.
func (p *Parent) DeleteLast() mancini.Interactor {
	children := p.GetChildren()
	if len(children) == 0 {
		return nil
	}

	// Children are already sorted by seq, so last is the final element.
	last := children[len(children)-1]
	clearParent(last)
	return last
}
