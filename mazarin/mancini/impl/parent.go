package impl

import (
	"image"
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
func (p *Parent) DrawChildren(self mancini.Interactor, x, y, w, h int64, damage image.Rectangle) {
	dc := self.DC()

	// Fast path: damage is entirely within one child — skip self-drawing.
	if child := p.IsRectSingleChild(damage); child != nil {
		if cs, ok := child.(dcSetter); ok {
			cs.SetDC(dc)
		}
		if d, ok := child.(mancini.NewDrawer); ok {
			d.Draw(child, child.X(), child.Y(), child.W(), child.H(), damage)
		}
		return
	}

	// Paint background strips not covered by children.
	if spd, ok := self.(mancini.SimpleParentDraw); ok {
		for _, strip := range p.SmallestDraw(damage) {
			spd.DrawSelf(dc, strip)
		}
	}

	// Draw children that intersect the damage rect.
	for _, child := range p.GetChildren() {
		if cs, ok := child.(dcSetter); ok {
			cs.SetDC(dc)
		}
		if d, ok := child.(mancini.NewDrawer); ok {
			d.Draw(child, x, y, w, h, damage)
		}
	}
}

// DrawSelf is the default no-op implementation for [mancini.SimpleParentDraw].
// Parent interactors that need background clearing (themed parents, etc.)
// should override this method.
func (p *Parent) DrawSelf(dc mancini.DrawContext, rect image.Rectangle) {}

// IsRectSingleChild tests whether any single child of this parent
// completely contains the given rectangle. If so, that child is
// returned — the parent can skip its own drawing and forward Draw
// directly to that child. Returns nil if no single child contains rect
// or if the parent has no children.
func (p *Parent) IsRectSingleChild(rect image.Rectangle) mancini.Interactor {
	for _, child := range p.GetChildren() {
		cl, ok := child.(mancini.Layouter)
		if !ok {
			continue
		}
		clh := cl.GetLayout()
		if clh == nil {
			continue
		}
		cx, cy := int(clh.X.Get()), int(clh.Y.Get())
		cb := image.Rect(cx, cy, cx+int(clh.Width.Get()), cy+int(clh.Height.Get()))
		if rect.In(cb) {
			return child
		}
	}
	return nil
}

// SmallestDraw computes the set of rectangles that cover the damaged
// region (rect) but do NOT overlap with any child. These are the
// "background strips" the parent needs to repaint — gaps between
// children, margins, etc.
//
// The algorithm:
//  1. Compute the minimal bounding rectangle of all children.
//  2. Intersect rect with that bounding box as the starting area.
//  3. Subtract each child's bounds, collecting the remaining strips.
//  4. Add back any parts of rect outside the children bounding box.
func (p *Parent) SmallestDraw(rect image.Rectangle) []image.Rectangle {
	children := p.GetChildren()
	if len(children) == 0 {
		return []image.Rectangle{rect}
	}

	// Compute minimal bounding rect of all children.
	var childrenBBox image.Rectangle
	first := true
	var childRects []image.Rectangle
	for _, child := range children {
		cl, ok := child.(mancini.Layouter)
		if !ok {
			continue
		}
		clh := cl.GetLayout()
		if clh == nil {
			continue
		}
		cx, cy := int(clh.X.Get()), int(clh.Y.Get())
		cr := image.Rect(cx, cy, cx+int(clh.Width.Get()), cy+int(clh.Height.Get()))
		childRects = append(childRects, cr)
		if first {
			childrenBBox = cr
			first = false
		}
		childrenBBox = childrenBBox.Union(cr)
	}

	// Start with the damage rect. Subtract each child.
	rects := []image.Rectangle{rect}
	for _, cr := range childRects {
		var next []image.Rectangle
		for _, r := range rects {
			next = append(next, rectSubtract(r, cr)...)
		}
		rects = next
	}

	// Filter out empty rectangles.
	result := rects[:0]
	for _, r := range rects {
		if !r.Empty() {
			result = append(result, r)
		}
	}
	return result
}

// rectSubtract returns the parts of a that are NOT covered by b.
// Returns up to 4 rectangles (top, bottom, left, right strips).
// If b does not intersect a, returns a unchanged.
func rectSubtract(a, b image.Rectangle) []image.Rectangle {
	isect := a.Intersect(b)
	if isect.Empty() {
		return []image.Rectangle{a}
	}

	var result []image.Rectangle

	// Top strip: above the intersection.
	if isect.Min.Y > a.Min.Y {
		result = append(result, image.Rect(a.Min.X, a.Min.Y, a.Max.X, isect.Min.Y))
	}
	// Bottom strip: below the intersection.
	if isect.Max.Y < a.Max.Y {
		result = append(result, image.Rect(a.Min.X, isect.Max.Y, a.Max.X, a.Max.Y))
	}
	// Left strip: between top and bottom, left of intersection.
	if isect.Min.X > a.Min.X {
		result = append(result, image.Rect(a.Min.X, isect.Min.Y, isect.Min.X, isect.Max.Y))
	}
	// Right strip: between top and bottom, right of intersection.
	if isect.Max.X < a.Max.X {
		result = append(result, image.Rect(isect.Max.X, isect.Min.Y, a.Max.X, isect.Max.Y))
	}

	if len(result) == 0 {
		// b completely covers a — nothing to draw.
		return nil
	}
	return result
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

// DeleteChild removes a specific child from this parent by clearing its
// Parent attribute. Returns the child, or nil if the child was not found
// among this parent's children.
func (p *Parent) DeleteChild(child mancini.Interactor) mancini.Interactor {
	children := p.GetChildren()
	for _, c := range children {
		if c == child {
			clearParent(c)
			return c
		}
	}
	return nil
}

// DeleteIthChild removes the i-th child (0-based, in sequence order)
// from this parent. Returns the removed child, or nil if i is out of range.
func (p *Parent) DeleteIthChild(i int) mancini.Interactor {
	children := p.GetChildren()
	if i < 0 || i >= len(children) {
		return nil
	}
	child := children[i]
	clearParent(child)
	return child
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
