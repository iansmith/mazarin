package interactor

import (
	"mazzy/mazarin/attr"
	"mazzy/mazarin/vm/flat"
)

// NewWindow creates a top-level window interactor with fixed width and height.
func NewWindow(id string, width, height int64, bgColor int64) *Interactor {
	i := &Interactor{
		ID:         id,
		Kind:       KindWindow,
		priestName: pName,
	}

	// Value attrs for layout and visual state.
	i.makeValueAttrs(bgColor, 0)

	// Fixed width/height stored as value attrs, read via identity constraint.
	fixedW := attr.ValueI64(i.uri("int64", "fixedWidth"), width)
	fixedH := attr.ValueI64(i.uri("int64", "fixedHeight"), height)
	i.Width = attr.ConstraintI64(i.uri("int64", "width"),
		BindStrings(ProgIdentityI64, fixedW.URI()))
	i.Height = attr.ConstraintI64(i.uri("int64", "height"),
		BindStrings(ProgIdentityI64, fixedH.URI()))

	// UpperLeft reads from originPoint via identity deref.
	i.UpperLeft = attr.ConstraintComposite(i.uri("point2d", "upperLeft"),
		flat.TypePoint2D,
		BindStrings(ProgIdentityPoint2d, i.uri("point2d", "originPoint")))

	// Bounds from UL + W + H.
	i.Bounds = attr.ConstraintComposite(i.uri("rect", "bounds"),
		flat.TypeRectangle,
		BindStrings(ProgBoundsFromUlwh,
			i.uri("point2d", "upperLeft"),
			i.uri("int64", "width"),
			i.uri("int64", "height")))

	// Visible = always true.
	i.Visible = attr.ConstraintBool(i.uri("bool", "visible"), ProgConstantTrue)

	// Content = always empty (windows don't have text).
	i.Content = attr.ConstraintStr(i.uri("str", "content"), ProgConstantEmptyStr)

	// DamageRect: created by FinalizeDamage() after children are added.

	register(i)
	return i
}

// FinalizeDamage creates the damageRect constraint. For parents with children,
// uses parent_damage_rect (union of own changes + child damage). For leaves,
// uses leaf_damage_rect. Must be called after all children are created.
func (i *Interactor) FinalizeDamage() {
	if len(i.Children) > 0 {
		childDmgURI := i.Children[0].uri("rect", "damageRect")
		i.makeParentDamage(childDmgURI)
	} else {
		i.makeLeafDamage()
	}
}
