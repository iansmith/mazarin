package mancini

import (
	"strconv"
	"sync/atomic"

	"mazzy/mazarin/attr"
	"mazzy/mazarin/vm"
	"mazzy/mazarin/vm/flat"
)

var manciniSID string

// Init initializes the mancini layout system using the shepherd's SID from attr.SID().
// Must be called after attr.Init().
// VisSuffix is the URI suffix for the Visible attribute, used by constraint programs
// to check child visibility.
var VisSuffix = LayoutVisible.Suffix()

func Init() {
	manciniSID = attr.SID()
}

// LayoutAttributes holds constraint system attributes for an interactor's layout.
type LayoutAttributes struct {
	name                         string               // interactor's constraint-system name
	X, Y, Width, Height *attr.Attribute[int64]
	Visible             *attr.Attribute[bool]
	Bounds                       *attr.Attribute[vm.Value] // Rectangle2D: (x, y, x+w, y+h)
	BoundsHash                   *attr.Attribute[int64]    // hash of X,Y,W,H for fast change detection
	Parent                       *attr.Attribute[string]
	SpacingAttr        *attr.Attribute[int64] // inter-child spacing (containers only)
	CrossAlignAttr     *attr.Attribute[int64] // cross-axis alignment (containers only)
	MaxWidthAttr       *attr.Attribute[int64] // max width for overflow clipping (Row only)
	LastChildDrawnAttr *attr.Attribute[int64] // 0-based index of last child to draw (Row only)
	Damage               *DamageAttributes      // damage rectangle tracking (nil = no damage)
}

// Name returns the interactor's constraint-system name.
func (lh *LayoutAttributes) Name() string {
	if lh == nil {
		return ""
	}
	return lh.name
}

var nameCounter uint64

func DefaultName(typeName string) string {
	n := atomic.AddUint64(&nameCounter, 1)
	return typeName + strconv.FormatUint(n, 10)
}

// LayoutURI builds an attribute URI in the mancini namespace.
func LayoutURI(myName string, dt DataType, prop LayoutProp) string {
	return "attr:///shepherd/" + manciniSID + "/" + string(dt) + "/" + myName + "/layout/" + string(prop)
}

// ChildPattern returns the URI pattern for discovering children via their Parent attributes.
func ChildPattern() string {
	return "attr:///shepherd/" + manciniSID + "/str/*/layout/Parent"
}

// Int64Prefix returns the URI prefix for int64 attributes in the mancini namespace.
func Int64Prefix() string {
	return "attr:///shepherd/" + manciniSID + "/int64/"
}

// BoolPrefix returns the URI prefix for bool attributes in the mancini namespace.
func BoolPrefix() string {
	return "attr:///shepherd/" + manciniSID + "/bool/"
}

// NewLayoutAttributesBase creates X, Y, Visible, Parent attributes (no Width/Height).
func NewLayoutAttributesBase(myName, parent string) *LayoutAttributes {
	return &LayoutAttributes{
		name:    myName,
		X:       attr.ValueI64(LayoutURI(myName, DataTypeInt64, LayoutX), 0),
		Y:       attr.ValueI64(LayoutURI(myName, DataTypeInt64, LayoutY), 0),
		Visible: attr.ValueBool(LayoutURI(myName, DataTypeBool, LayoutVisible), true),
		Parent:  attr.ValueStr(LayoutURI(myName, DataTypeStr, LayoutParent), parent),
	}
}

// NewLayoutAttributes creates all layout attributes as Value attributes,
// plus a Bounds constraint derived from X, Y, Width, Height.
func NewLayoutAttributes(myName, parent string) *LayoutAttributes {
	lh := NewLayoutAttributesBase(myName, parent)
	lh.Width = attr.ValueI64(LayoutURI(myName, DataTypeInt64, LayoutWidth), 0)
	lh.Height = attr.ValueI64(LayoutURI(myName, DataTypeInt64, LayoutHeight), 0)
	lh.InitBounds(myName)
	return lh
}

// InitBounds creates the Bounds constraint: rect(X, Y, X+Width, Y+Height),
// plus a BoundsHash constraint for fast layout-change detection.
func (lh *LayoutAttributes) InitBounds(myName string) {
	prog := BindStrings(ProgBoundsFromXywh,
		"_x_", lh.X.URI(), "_y_", lh.Y.URI(), "_width_", lh.Width.URI(), "_height_", lh.Height.URI())
	lh.Bounds = attr.ConstraintComposite(
		LayoutURI(myName, DataTypeRect, LayoutBounds),
		flat.TypeRectangle, prog)

	hashProg := BindStrings(ProgBoundsHash,
		"_x_", lh.X.URI(), "_y_", lh.Y.URI(), "_width_", lh.Width.URI(), "_height_", lh.Height.URI())
	lh.BoundsHash = attr.ConstraintI64(LayoutURI(myName, DataTypeInt64, LayoutBoundsHash), hashProg)
}

// setVisibleAttr sets the Visible attribute value.
func setVisibleAttr(lh *LayoutAttributes, v bool) {
	if lh != nil && lh.Visible != nil {
		lh.Visible.Set(v)
	}
}

// isVisibleAttr returns true if the interactor is visible (default true if no attribute).
func isVisibleAttr(lh *LayoutAttributes) bool {
	if lh == nil || lh.Visible == nil {
		return true
	}
	return lh.Visible.Get()
}

// boundsHashValue returns the current bounds hash, or 0 if unavailable.
func (lh *LayoutAttributes) boundsHashValue() int64 {
	if lh == nil || lh.BoundsHash == nil {
		return 0
	}
	return lh.BoundsHash.Get()
}

// GetSpacing returns the current inter-child spacing value, or 0 if unavailable.
func (lh *LayoutAttributes) GetSpacing() float64 {
	if lh == nil || lh.SpacingAttr == nil {
		return 0
	}
	return float64(lh.SpacingAttr.Get())
}

// setLayoutSpacing sets the inter-child spacing value on a LayoutAttributes.
func setLayoutSpacing(lh *LayoutAttributes, v float64) {
	if lh != nil && lh.SpacingAttr != nil {
		lh.SpacingAttr.Set(int64(v))
	}
}

// GetCrossAlign returns the current cross-axis alignment, or AxisMinimum if unavailable.
func (lh *LayoutAttributes) GetCrossAlign() Alignment {
	if lh == nil || lh.CrossAlignAttr == nil {
		return AxisMinimum
	}
	return Alignment(lh.CrossAlignAttr.Get())
}

// setLayoutCrossAlign sets the cross-axis alignment value on a LayoutAttributes.
func setLayoutCrossAlign(lh *LayoutAttributes, v Alignment) {
	if lh != nil && lh.CrossAlignAttr != nil {
		lh.CrossAlignAttr.Set(int64(v))
	}
}

// childLayoutWidth reads a child's width from its constraint layout attributes.
// Returns fallback if the child has no layout or its width is 0.
func childLayoutWidth(d Drawer, fallback float64) float64 {
	if l, ok := d.(Layouter); ok {
		return ChildWidth(l, fallback)
	}
	return fallback
}

// childLayoutHeight reads a child's height from its constraint layout attributes.
// Returns fallback if the child has no layout or its height is 0.
func childLayoutHeight(d Drawer, fallback float64) float64 {
	if l, ok := d.(Layouter); ok {
		return ChildHeight(l, fallback)
	}
	return fallback
}

// ChildWidth reads a Layouter's width from its constraint attributes.
// Returns fallback if the layout is nil or width is 0.
func ChildWidth(l Layouter, fallback float64) float64 {
	lh := l.GetLayout()
	if lh != nil && lh.Width != nil {
		if v := float64(lh.Width.Get()); v > 0 {
			return v
		}
	}
	return fallback
}

// ChildHeight reads a Layouter's height from its constraint attributes.
// Returns fallback if the layout is nil or height is 0.
func ChildHeight(l Layouter, fallback float64) float64 {
	lh := l.GetLayout()
	if lh != nil && lh.Height != nil {
		if v := float64(lh.Height.Get()); v > 0 {
			return v
		}
	}
	return fallback
}

// PublishLayout writes position and size to an interactor's layout attributes.
// Panics if a non-nil attribute is a constraint — constraint values are computed
// by the VM and must not be set imperatively.
func PublishLayout(l *LayoutAttributes, x, y, w, h float64) {
	if l == nil {
		return
	}
	if l.X != nil {
		l.X.Set(int64(x))
	}
	if l.Y != nil {
		l.Y.Set(int64(y))
	}
	if l.Width != nil {
		l.Width.Set(int64(w))
	}
	if l.Height != nil {
		l.Height.Set(int64(h))
	}
}


// NewDecoratorLayout creates layout attributes with inside-out constraint sizing
// for decorator-style parents. The parent interactor is used to extract the
// parent's constraint-system name. hMargin and vMargin are the half-margins
// for width and height respectively: width = child.Width + 2*hMargin,
// height = child.Height + 2*vMargin (both clamped to maxSize).
// For symmetric decorators (equal insets), pass the same value for both.
// If no child is found, defaults from ProgDecorationWidth/Height apply (20/30).
func NewDecoratorLayout(name string, parent Interactor, hMargin, vMargin, maxSize int64) *LayoutAttributes {
	parentName := ""
	if parent != nil {
		if l, ok := parent.(Layouter); ok {
			lh := l.GetLayout()
			if lh != nil {
				parentName = lh.Name()
			}
		}
	}
	return NewDecoratorLayoutByParentName(name, parentName, hMargin, vMargin, maxSize)
}

// NewDecoratorLayoutByParentName is the string-based variant of NewDecoratorLayout.
// Prefer NewDecoratorLayout which takes an Interactor — this version exists for
// cases where only the parent's constraint-system name is available.
func NewDecoratorLayoutByParentName(myName, parentName string, hMargin, vMargin, maxSize int64) *LayoutAttributes {
	lh := NewLayoutAttributesBase(myName, parentName)

	hMarginURI := LayoutURI(myName, DataTypeInt64, LayoutHMargin)
	attr.ValueI64(hMarginURI, hMargin)

	vMarginURI := LayoutURI(myName, DataTypeInt64, LayoutVMargin)
	attr.ValueI64(vMarginURI, vMargin)

	maxSizeURI := LayoutURI(myName, DataTypeInt64, LayoutMaxSize)
	attr.ValueI64(maxSizeURI, maxSize)

	widthProg := BindStringsChildren(ProgDecorationWidth,
		"_myName_", myName, "_margin_", hMarginURI, "_maxSize_", maxSizeURI)
	lh.Width = attr.ConstraintI64(
		LayoutURI(myName, DataTypeInt64, LayoutWidth), widthProg)

	heightProg := BindStringsChildren(ProgDecorationHeight,
		"_myName_", myName, "_margin_", vMarginURI, "_maxSize_", maxSizeURI)
	lh.Height = attr.ConstraintI64(
		LayoutURI(myName, DataTypeInt64, LayoutHeight), heightProg)

	lh.InitBounds(myName)
	return lh
}
