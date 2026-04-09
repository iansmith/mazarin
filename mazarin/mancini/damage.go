package mancini

import (
	"mazzy/mazarin/attr"
	"mazzy/mazarin/vm"
	"mazzy/mazarin/vm/flat"
)

// DamageAttributes holds "last painted" mirror attributes and the computed
// damage rectangle for an interactor. The damage rectangle is the union
// of current and last-painted bounds when any tracked property changes.
type DamageAttributes struct {
	DamageRect    *attr.Attribute[vm.Value] // constraint: damaged region (empty if no change)
	LPBounds      *attr.Attribute[vm.Value] // value: last-painted bounds (set after painting)
	LPVisible     *attr.Attribute[bool]     // value: last-painted visibility
	LPBoundsHash  *attr.Attribute[int64]    // value: last-painted bounds hash
	LPBgColor     *attr.Attribute[int64]    // value: last-painted background color
	LPFgColor     *attr.Attribute[int64]    // value: last-painted foreground color
	LPContentHash *attr.Attribute[int64]    // value: last-painted content hash
}

// emptyRect returns a vm.Value for an empty rectangle (0,0,0,0).
func emptyRect() vm.Value {
	return vm.RectangleVal(0, 0, 0, 0)
}

// InitLeafDamage creates damage tracking for a leaf interactor (no children).
// bgColorURI and fgColorURI point to the interactor's current color attributes.
// contentHashURI points to a content hash attribute (use "" for non-text interactors;
// the placeholder will remain unbound and the deref will return 0 — matching the LP default).
func (lh *LayoutAttributes) InitLeafDamage(bgColorURI, fgColorURI, contentHashURI string) {
	myName := lh.name
	d := &DamageAttributes{}

	// Last-painted mirrors — value attributes set by the draw loop after painting.
	d.LPBounds = attr.ValueRectangle(LayoutURI(myName, DataTypeRect, LayoutLPBounds), emptyRect())
	d.LPVisible = attr.ValueBool(LayoutURI(myName, DataTypeBool, LayoutLPVisible), false)
	d.LPBoundsHash = attr.ValueI64(LayoutURI(myName, DataTypeInt64, LayoutLPBoundsHash), 0)
	d.LPBgColor = attr.ValueI64(LayoutURI(myName, DataTypeInt64, LayoutLPBgColor), 0)
	d.LPFgColor = attr.ValueI64(LayoutURI(myName, DataTypeInt64, LayoutLPFgColor), 0)
	d.LPContentHash = attr.ValueI64(LayoutURI(myName, DataTypeInt64, LayoutLPContentHash), 0)

	prog := BindStrings(ProgLeafDamageRect,
		"_bounds_", lh.Bounds.URI(),
		"_lpBounds_", d.LPBounds.URI(),
		"_visible_", lh.Visible.URI(),
		"_lpVisible_", d.LPVisible.URI(),
		"_contentHash_", contentHashURI,
		"_lpContentHash_", d.LPContentHash.URI(),
		"_bgColor_", bgColorURI,
		"_lpBgColor_", d.LPBgColor.URI(),
		"_fgColor_", fgColorURI,
		"_lpFgColor_", d.LPFgColor.URI(),
		"_boundsHash_", lh.BoundsHash.URI(),
		"_lpBoundsHash_", d.LPBoundsHash.URI(),
	)
	d.DamageRect = attr.ConstraintComposite(
		LayoutURI(myName, DataTypeRect, LayoutDamageRect), flat.TypeRectangle, prog)

	lh.Damage = d
}

// InitParentDamage creates damage tracking for a parent interactor.
// childDamageURI is the URI of the first child's DamageRect attribute.
func (lh *LayoutAttributes) InitParentDamage(bgColorURI, fgColorURI, childDamageURI string) {
	myName := lh.name
	d := &DamageAttributes{}

	d.LPBounds = attr.ValueRectangle(LayoutURI(myName, DataTypeRect, LayoutLPBounds), emptyRect())
	d.LPVisible = attr.ValueBool(LayoutURI(myName, DataTypeBool, LayoutLPVisible), false)
	d.LPBoundsHash = attr.ValueI64(LayoutURI(myName, DataTypeInt64, LayoutLPBoundsHash), 0)
	d.LPBgColor = attr.ValueI64(LayoutURI(myName, DataTypeInt64, LayoutLPBgColor), 0)
	d.LPFgColor = attr.ValueI64(LayoutURI(myName, DataTypeInt64, LayoutLPFgColor), 0)
	d.LPContentHash = attr.ValueI64(LayoutURI(myName, DataTypeInt64, LayoutLPContentHash), 0)

	prog := BindStrings(ProgParentDamageRect,
		"_bounds_", lh.Bounds.URI(),
		"_lpBounds_", d.LPBounds.URI(),
		"_visible_", lh.Visible.URI(),
		"_lpVisible_", d.LPVisible.URI(),
		"_bgColor_", bgColorURI,
		"_lpBgColor_", d.LPBgColor.URI(),
		"_fgColor_", fgColorURI,
		"_lpFgColor_", d.LPFgColor.URI(),
		"_boundsHash_", lh.BoundsHash.URI(),
		"_lpBoundsHash_", d.LPBoundsHash.URI(),
		"_childDamage_", childDamageURI,
	)
	d.DamageRect = attr.ConstraintComposite(
		LayoutURI(myName, DataTypeRect, LayoutDamageRect), flat.TypeRectangle, prog)

	lh.Damage = d
}

// SnapshotDamage copies current visual state into last-painted attributes.
// Called by the draw loop after painting an interactor.
func (lh *LayoutAttributes) SnapshotDamage() {
	if lh == nil || lh.Damage == nil {
		return
	}
	d := lh.Damage
	if lh.Bounds != nil && d.LPBounds != nil {
		d.LPBounds.Set(lh.Bounds.Get())
	}
	if lh.Visible != nil && d.LPVisible != nil {
		d.LPVisible.Set(lh.Visible.Get())
	}
	if lh.BoundsHash != nil && d.LPBoundsHash != nil {
		d.LPBoundsHash.Set(lh.BoundsHash.Get())
	}
}

// SnapshotDamageColors copies current color values into last-painted attributes.
// bgColor and fgColor are the current values to snapshot.
func (lh *LayoutAttributes) SnapshotDamageColors(bgColor, fgColor int64) {
	if lh == nil || lh.Damage == nil {
		return
	}
	d := lh.Damage
	if d.LPBgColor != nil {
		d.LPBgColor.Set(bgColor)
	}
	if d.LPFgColor != nil {
		d.LPFgColor.Set(fgColor)
	}
}

// SnapshotDamageContentHash copies a content hash into the last-painted attribute.
func (lh *LayoutAttributes) SnapshotDamageContentHash(hash int64) {
	if lh == nil || lh.Damage == nil {
		return
	}
	if lh.Damage.LPContentHash != nil {
		lh.Damage.LPContentHash.Set(hash)
	}
}

// ClearDamage resets this interactor's damage rectangle to empty (0,0,0,0).
// For value DamageRect attributes (leaves using FullDamage), this directly
// sets the value. For constraint DamageRect attributes (parents using
// InitDefaultParentDamage), this is a no-op — the constraint automatically
// re-evaluates to empty when all children clear their damage.
//
// Called by [impl.ThemedInteractor.Draw] after painting.
func (lh *LayoutAttributes) ClearDamage() {
	if lh == nil || lh.Damage == nil || lh.Damage.DamageRect == nil {
		return
	}
	if lh.Damage.DamageRect.IsConstraint() {
		return
	}
	lh.Damage.DamageRect.Set(emptyRect())
}

// GetDamageRect returns the current damage rectangle as (x0, y0, x1, y1).
// Returns (0,0,0,0) if no damage is pending.
func (lh *LayoutAttributes) GetDamageRect() (x0, y0, x1, y1 int64) {
	if lh == nil || lh.Damage == nil || lh.Damage.DamageRect == nil {
		return 0, 0, 0, 0
	}
	return lh.Damage.DamageRect.Get().AsRectangle()
}

// HasDamage returns true if the damage rectangle is non-empty.
func (lh *LayoutAttributes) HasDamage() bool {
	x0, y0, x1, y1 := lh.GetDamageRect()
	return x0 != x1 || y0 != y1
}

// ChildHasDamage checks whether a child interactor has pending damage.
// Returns true (safe default) if the child has no layout information.
// Used by parent Draw methods to skip undamaged children.
func ChildHasDamage(child Interactor) bool {
	l, ok := child.(Layouter)
	if !ok {
		return true
	}
	lh := l.GetLayout()
	if lh == nil {
		return true
	}
	return lh.HasDamage()
}

// FullDamage sets this interactor's damage rectangle to its full Bounds,
// forcing a complete repaint on the next draw pass. Creates the
// DamageAttributes and DamageRect if they don't exist yet.
//
// Called by [impl.Interactor.Initialize] to ensure the first draw paints
// everything. Also available for input handlers that need to force a
// full repaint (e.g., a clock face on tick).
func (lh *LayoutAttributes) FullDamage() {
	if lh == nil {
		return
	}
	if lh.Damage == nil {
		lh.Damage = &DamageAttributes{}
	}
	bounds := emptyRect()
	if lh.Bounds != nil {
		bounds = lh.Bounds.Get()
	}
	uri := LayoutURI(lh.name, DataTypeRect, LayoutDamageRect)
	if lh.Damage.DamageRect == nil {
		lh.Damage.DamageRect = attr.ValueRectangle(uri, bounds)
	} else if !lh.Damage.DamageRect.IsConstraint() {
		lh.Damage.DamageRect.Set(bounds)
	}
	// If DamageRect is a constraint (parent interactors), skip the Set —
	// the constraint will re-evaluate from its children's damage rects.
}

// InitDefaultParentDamage creates a damage constraint for a parent
// interactor that unions its visible children's damage rectangles. If
// the parent's own BoundsHash has changed, it returns the full bounds
// instead (everything needs repainting when the parent moves or resizes).
//
// Called by [impl.Parent.Initialize] when wantDefaultDamageConstraint is true.
func InitDefaultParentDamage(lh *LayoutAttributes) {
	if lh == nil {
		return
	}
	myName := lh.name
	if lh.Damage == nil {
		lh.Damage = &DamageAttributes{}
	}

	// LPBoundsHash mirror — tracks whether our own bounds changed.
	lpBoundsHashURI := LayoutURI(myName, DataTypeInt64, LayoutLPBoundsHash)
	lh.Damage.LPBoundsHash = attr.ValueI64(lpBoundsHashURI, 0)

	prog := BindStringsChildren(ProgParentDamageDefault,
		"_boundsHash_", lh.BoundsHash.URI(),
		"_lpBoundsHash_", lh.Damage.LPBoundsHash.URI(),
		"_bounds_", lh.Bounds.URI(),
		"_myName_", myName,
	)
	damageURI := LayoutURI(myName, DataTypeRect, LayoutDamageRect)
	lh.Damage.DamageRect = attr.ConstraintComposite(damageURI, flat.TypeRectangle, prog)
}

// PushDamageClip reads this interactor's DamageRect and, if non-empty,
// sets a clip rectangle on the DrawContext to restrict drawing to the
// damaged region. Returns true if a clip was pushed (caller must call
// PopDamageClip to restore). Returns false if no damage or no clip was
// needed (full bounds = no restriction).
//
// Used by parent Draw methods to avoid redrawing the entire window when
// only a small region changed.
func (lh *LayoutAttributes) PushDamageClip(dc DrawContext) bool {
	if dc == nil {
		return false
	}
	x0, y0, x1, y1 := lh.GetDamageRect()
	if x0 == 0 && y0 == 0 && x1 == 0 && y1 == 0 {
		// No damage — nothing to draw at all. But we return false
		// so the caller can skip drawing entirely.
		return false
	}
	// Check if damage covers the full bounds — no clip needed.
	if lh.Bounds != nil {
		bx0, by0, bx1, by1 := lh.Bounds.Get().AsRectangle()
		if x0 <= bx0 && y0 <= by0 && x1 >= bx1 && y1 >= by1 {
			return false
		}
	}
	dc.Push()
	dc.DrawRectangle(float64(x0), float64(y0), float64(x1-x0), float64(y1-y0))
	dc.Clip()
	return true
}

// PopDamageClip restores the DrawContext state saved by PushDamageClip.
func PopDamageClip(dc DrawContext) {
	if dc != nil {
		dc.ResetClip()
		dc.Pop()
	}
}
