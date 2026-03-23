package interactor

import (
	"mazzy/mazarin/attr"
	"mazzy/mazarin/vm/flat"
)

// NewLabel creates a label interactor inside its parent.
// Content is NOT created here — call BindContent or SetContentValue after.
func NewLabel(id string, parent *Interactor, textColor int64) *Interactor {
	i := &Interactor{
		ID:         id,
		Kind:       KindLabel,
		Parent:     parent,
		shepherdName: pName,
	}

	// Value attrs for layout and visual state.
	i.makeValueAttrs(0, textColor)

	// Width = charWidth * str_len(content). The content attr may not exist
	// yet; deref_str returns "" for non-existent attrs, giving width=0 until
	// BindContent/SetContentValue is called.
	i.Width = attr.ConstraintI64(i.uri("int64", "width"),
		BindStrings(ProgLabelWidth, "_content_", i.uri("str", "content"), "_charWidth_", CharWidthURI))

	// Height = kernel charHeight (identity deref).
	i.Height = attr.ConstraintI64(i.uri("int64", "height"),
		BindStrings(ProgIdentityI64, "_source_", CharHeightURI))

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

	// DamageRect (leaf).
	i.makeLeafDamage()

	parent.Children = append(parent.Children, i)
	register(i)
	return i
}
