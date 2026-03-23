package interactor

import (
	"mazzy/mazarin/attr"
	"mazzy/mazarin/vm/flat"
)

// NewCard creates a card interactor centered in its parent with padding.
func NewCard(id string, parent *Interactor, padding int64, bgColor int64) *Interactor {
	i := &Interactor{
		ID:         id,
		Kind:       KindCard,
		Parent:     parent,
		shepherdName: pName,
		padding:    padding,
	}

	// Value attrs for layout and visual state.
	i.makeValueAttrs(bgColor, 0)

	// Padding stored as value attr for deref-based parameterization.
	paddingAttr := attr.ValueI64(i.uri("int64", "padding"), padding)

	// Width = parent.width - 2*padding (outside-in layout).
	i.Width = attr.ConstraintI64(i.uri("int64", "width"),
		BindStrings(ProgOutsideInDim, "_parentDim_", parent.uri("int64", "width"), "_padding_", paddingAttr.URI()))
	i.Height = attr.ConstraintI64(i.uri("int64", "height"),
		BindStrings(ProgOutsideInDim, "_parentDim_", parent.uri("int64", "height"), "_padding_", paddingAttr.URI()))

	// UpperLeft = centered in parent.
	i.UpperLeft = attr.ConstraintComposite(i.uri("point2d", "upperLeft"),
		flat.TypePoint2D,
		BindStrings(ProgCenterInParent,
			"_parentUL_", parent.uri("point2d", "upperLeft"),
			"_parentWidth_", parent.uri("int64", "width"),
			"_parentHeight_", parent.uri("int64", "height"),
			"_myWidth_", i.uri("int64", "width"),
			"_myHeight_", i.uri("int64", "height")))

	// Bounds from UL + W + H.
	i.Bounds = attr.ConstraintComposite(i.uri("rect", "bounds"),
		flat.TypeRectangle,
		BindStrings(ProgBoundsFromUlwh,
			"_upperLeft_", i.uri("point2d", "upperLeft"),
			"_width_", i.uri("int64", "width"),
			"_height_", i.uri("int64", "height")))

	// Visible = always true.
	i.Visible = attr.ConstraintBool(i.uri("bool", "visible"), ProgConstantTrue)

	// Content = always empty.
	i.Content = attr.ConstraintStr(i.uri("str", "content"), ProgConstantEmptyStr)

	// DamageRect: created by FinalizeDamage() after children are added.

	parent.Children = append(parent.Children, i)
	register(i)
	return i
}
