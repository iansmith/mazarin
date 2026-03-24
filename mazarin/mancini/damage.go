package mancini

import (
	"mazzy/mazarin/attr"
	"mazzy/mazarin/vm"
	"mazzy/mazarin/vm/flat"
)

// DamageHandles holds "last painted" mirror attributes and the computed
// damage rectangle for an interactor. The damage rectangle is the union
// of current and last-painted bounds when any tracked property changes.
type DamageHandles struct {
	DamageRect    *attr.Handle[vm.Value] // constraint: damaged region (empty if no change)
	LPBounds      *attr.Handle[vm.Value] // value: last-painted bounds (set after painting)
	LPVisible     *attr.Handle[bool]     // value: last-painted visibility
	LPBoundsHash  *attr.Handle[int64]    // value: last-painted bounds hash
	LPBgColor     *attr.Handle[int64]    // value: last-painted background color
	LPFgColor     *attr.Handle[int64]    // value: last-painted foreground color
	LPContentHash *attr.Handle[int64]    // value: last-painted content hash
}

// emptyRect returns a vm.Value for an empty rectangle (0,0,0,0).
func emptyRect() vm.Value {
	return vm.RectangleVal(0, 0, 0, 0)
}

// InitLeafDamage creates damage tracking for a leaf interactor (no children).
// bgColorURI and fgColorURI point to the interactor's current color attributes.
// contentHashURI points to a content hash attribute (use "" for non-text interactors;
// the placeholder will remain unbound and the deref will return 0 — matching the LP default).
func (lh *LayoutHandles) InitLeafDamage(bgColorURI, fgColorURI, contentHashURI string) {
	myName := lh.name
	d := &DamageHandles{}

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
func (lh *LayoutHandles) InitParentDamage(bgColorURI, fgColorURI, childDamageURI string) {
	myName := lh.name
	d := &DamageHandles{}

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

// SnapshotDamage copies current visual state into last-painted handles.
// Called by the draw loop after painting an interactor.
func (lh *LayoutHandles) SnapshotDamage() {
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

// SnapshotDamageColors copies current color values into last-painted handles.
// bgColor and fgColor are the current values to snapshot.
func (lh *LayoutHandles) SnapshotDamageColors(bgColor, fgColor int64) {
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

// SnapshotDamageContentHash copies a content hash into the last-painted handle.
func (lh *LayoutHandles) SnapshotDamageContentHash(hash int64) {
	if lh == nil || lh.Damage == nil {
		return
	}
	if lh.Damage.LPContentHash != nil {
		lh.Damage.LPContentHash.Set(hash)
	}
}
