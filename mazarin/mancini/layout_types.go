package mancini

// DataType identifies the type segment in an attribute URI.
type DataType string

const (
	DataTypeInt64 DataType = "int64"
	DataTypeBool  DataType = "bool"
	DataTypeStr   DataType = "str"
	DataTypeRect  DataType = "rect"
)

// LayoutProp identifies a layout property name in an attribute URI.
// The Suffix method returns the "/layout/<prop>" form used in constraint
// program bindings.
type LayoutProp string

const (
	// Core layout properties.
	LayoutX       LayoutProp = "X"
	LayoutY       LayoutProp = "Y"
	LayoutWidth   LayoutProp = "Width"
	LayoutHeight  LayoutProp = "Height"
	LayoutVisible LayoutProp = "Visible"
	LayoutParent  LayoutProp = "Parent"

	// Derived layout properties.
	LayoutBounds     LayoutProp = "Bounds"
	LayoutBoundsHash LayoutProp = "BoundsHash"

	// Container properties (Row, Column, ColumnOutsideIn).
	LayoutSpacing        LayoutProp = "Spacing"
	LayoutCrossAlign     LayoutProp = "CrossAlign"
	LayoutMaxWidth       LayoutProp = "MaxWidth"
	LayoutLastChildDrawn LayoutProp = "LastChildDrawn"
	LayoutMinHeight      LayoutProp = "MinHeight"
	LayoutMaxHeight      LayoutProp = "MaxHeight"

	// Decorator properties (NeuBox, NeuCircle, AppWindow, AppTitleBar).
	LayoutMargin  LayoutProp = "Margin"
	LayoutHMargin LayoutProp = "HMargin"
	LayoutVMargin LayoutProp = "VMargin"
	LayoutMaxSize LayoutProp = "MaxSize"

	// Clock properties.
	LayoutFaceName LayoutProp = "FaceName"

	// Damage rectangle tracking.
	LayoutDamageRect     LayoutProp = "DamageRect"
	LayoutLPBounds       LayoutProp = "LPBounds"
	LayoutLPVisible      LayoutProp = "LPVisible"
	LayoutLPBoundsHash   LayoutProp = "LPBoundsHash"
	LayoutLPBgColor      LayoutProp = "LPBgColor"
	LayoutLPFgColor      LayoutProp = "LPFgColor"
)

// Suffix returns the "/layout/<prop>" form used in constraint program bindings.
func (p LayoutProp) Suffix() string { return "/layout/" + string(p) }
