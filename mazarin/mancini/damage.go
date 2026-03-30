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
	DamageRect   *attr.Attribute[vm.Value] // constraint: damaged region (empty if no change)
	LPBounds     *attr.Attribute[vm.Value] // value: last-painted bounds (set after painting)
	LPVisible    *attr.Attribute[bool]     // value: last-painted visibility
	LPBoundsHash *attr.Attribute[int64]    // value: last-painted bounds hash
	LPBgColor    *attr.Attribute[int64]    // value: last-painted background color
	LPFgColor    *attr.Attribute[int64]    // value: last-painted foreground color
}

// emptyRect returns a vm.Value for an empty rectangle (0,0,0,0).
func emptyRect() vm.Value {
	return vm.RectangleVal(0, 0, 0, 0)
}

// InitLeafDamage creates damage tracking for a leaf interactor (no children).
// bgColorURI and fgColorURI point to the interactor's current color attributes.
func (lh *LayoutAttributes) InitLeafDamage(bgColorURI, fgColorURI string) {
	myName := lh.name
	d := &DamageAttributes{}

	// Last-painted mirrors — value attributes set by the draw loop after painting.
	d.LPBounds = attr.ValueRectangle(LayoutURI(myName, DataTypeRect, LayoutLPBounds), emptyRect())
	d.LPVisible = attr.ValueBool(LayoutURI(myName, DataTypeBool, LayoutLPVisible), false)
	d.LPBoundsHash = attr.ValueI64(LayoutURI(myName, DataTypeInt64, LayoutLPBoundsHash), 0)
	d.LPBgColor = attr.ValueI64(LayoutURI(myName, DataTypeInt64, LayoutLPBgColor), 0)
	d.LPFgColor = attr.ValueI64(LayoutURI(myName, DataTypeInt64, LayoutLPFgColor), 0)

	prog := BindStrings(ProgLeafDamageRect,
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

// FullDamage sets the damage rectangle to the interactor's full bounds,
// ensuring the next draw pass repaints this interactor completely.
// Safe to call before Damage or Bounds are initialized — creates a
// minimal DamageRect value attribute if needed.
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
	} else {
		lh.Damage.DamageRect.Set(bounds)
	}
}

// InitDefaultParentDamage installs a default parent damage constraint
// that checks BoundsHash change (→ full bounds) or unions visible
// children's DamageRects. This replaces any value-attribute DamageRect
// (from FullDamage) with a constraint-attribute DamageRect.
func InitDefaultParentDamage(lh *LayoutAttributes) {
	if lh == nil {
		return
	}
	myName := lh.name

	// LPBoundsHash mirror for detecting parent bounds changes.
	if lh.Damage == nil {
		lh.Damage = &DamageAttributes{}
	}
	d := lh.Damage
	if d.LPBoundsHash == nil {
		d.LPBoundsHash = attr.ValueI64(LayoutURI(myName, DataTypeInt64, LayoutLPBoundsHash), 0)
	}

	// Skip constraint DamageRect if a value DamageRect already exists at this URI
	// (created by FullDamage). No AttrDelete API yet, so we can't replace it.
	// The value attr will serve as a placeholder until the constraint rework lands.
	if d.DamageRect != nil {
		return
	}

	prog := BindStringsChildren(ProgParentDamageDefault,
		"_boundsHash_", lh.BoundsHash.URI(),
		"_lpBoundsHash_", d.LPBoundsHash.URI(),
		"_bounds_", lh.Bounds.URI(),
		"_myName_", myName,
	)
	d.DamageRect = attr.ConstraintComposite(
		LayoutURI(myName, DataTypeRect, LayoutDamageRect), flat.TypeRectangle, prog)
}
