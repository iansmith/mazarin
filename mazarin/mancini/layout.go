package mancini

import (
	"strconv"
	"sync/atomic"

	"mazzy/mazarin/attr"
	"mazzy/mazarin/vm"
	"mazzy/mazarin/vm/flat"
)

var manciniSID string

// VisSuffix is the URI suffix for the Visible attribute, used by constraint programs
// to check child visibility.
var VisSuffix = LayoutVisible.Suffix()

// Init initializes the mancini layout system using the shepherd's SID from attr.SID().
// Must be called after attr.Init().
func Init() {
	manciniSID = attr.SID()
}

// LayoutAttributes holds constraint system attributes for an interactor's layout.
type LayoutAttributes struct {
	name                         string               // interactor's constraint-system name
	X, Y, Width, Height *attr.Attribute[int64] // local position and size
	Visible             *attr.Attribute[bool]  // visibility flag
	Bounds                       *attr.Attribute[vm.Value] // Rectangle2D: (x, y, x+w, y+h)
	BoundsHash                   *attr.Attribute[int64]    // hash of X,Y,W,H for fast change detection
	Parent                       *attr.Attribute[string]   // parent's constraint-system name
	SpacingAttr        *attr.Attribute[int64] // inter-child spacing (containers only)
	CrossAlignAttr     *attr.Attribute[int64] // cross-axis alignment (containers only)
	MaxWidthAttr       *attr.Attribute[int64] // max width for overflow clipping (Row)
	MaxHeightAttr      *attr.Attribute[int64] // max height for overflow clipping (Column)
	LastChildDrawnAttr *attr.Attribute[int64] // 0-based index of last child to draw (Row/Column)
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

// DefaultName returns a unique constraint-system name by suffixing typeName
// with a process-local counter (e.g. "Button1", "Button2").
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

// RectPrefix returns the URI prefix for rect attributes in the mancini namespace.
func RectPrefix() string {
	return "attr:///shepherd/" + manciniSID + "/rect/"
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

// NewVerticalLineLayout creates layout attributes where Height is constrained
// to equal the source int64 attribute at heightSourceURI. Width is a value
// attribute (set by parent). X and Y are value attributes.
func NewVerticalLineLayout(myName, parent, heightSourceURI string) *LayoutAttributes {
	lh := NewLayoutAttributesBase(myName, parent)
	lh.Width = attr.ValueI64(LayoutURI(myName, DataTypeInt64, LayoutWidth), 0)
	heightURI := LayoutURI(myName, DataTypeInt64, LayoutHeight)
	lh.Height = attr.ConstraintI64(heightURI, EqualI64(heightSourceURI))
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

// CleanConstraints deletes all constraint attributes owned by this
// LayoutAttributes in dependency order. Constraints that read from
// other attributes are deleted first, then the leaf value attributes.
// After this call the LayoutAttributes should not be used.
func (lh *LayoutAttributes) CleanConstraints() {
	if lh == nil {
		return
	}

	// Phase 1: Damage constraint (depends on Bounds, LP* values, etc.)
	if lh.Damage != nil {
		deleteAttr(lh.Damage.DamageRect)
		lh.Damage.DamageRect = nil
	}

	// Phase 2: Bounds and BoundsHash (constraints depending on X, Y, W, H).
	deleteAttr(lh.Bounds)
	lh.Bounds = nil
	deleteAttr(lh.BoundsHash)
	lh.BoundsHash = nil

	// Phase 3: Damage LP* value attrs (no dependents now that DamageRect is gone).
	if lh.Damage != nil {
		deleteAttr(lh.Damage.LPBounds)
		deleteAttr(lh.Damage.LPVisible)
		deleteAttr(lh.Damage.LPBoundsHash)
		deleteAttr(lh.Damage.LPBgColor)
		deleteAttr(lh.Damage.LPFgColor)
		deleteAttr(lh.Damage.LPContentHash)
		lh.Damage = nil
	}

	// Phase 4: Optional container attrs.
	deleteAttr(lh.SpacingAttr)
	lh.SpacingAttr = nil
	deleteAttr(lh.CrossAlignAttr)
	lh.CrossAlignAttr = nil
	deleteAttr(lh.MaxWidthAttr)
	lh.MaxWidthAttr = nil
	deleteAttr(lh.MaxHeightAttr)
	lh.MaxHeightAttr = nil
	deleteAttr(lh.LastChildDrawnAttr)
	lh.LastChildDrawnAttr = nil

	// Phase 5: Core value attrs (X, Y, Width, Height, Visible, Parent).
	// Width and Height may be constraints — that's fine, their deps
	// (if any) are external and were already unwired by the caller.
	deleteAttr(lh.Width)
	lh.Width = nil
	deleteAttr(lh.Height)
	lh.Height = nil
	deleteAttr(lh.X)
	lh.X = nil
	deleteAttr(lh.Y)
	lh.Y = nil
	deleteAttr(lh.Visible)
	lh.Visible = nil
	deleteAttr(lh.Parent)
	lh.Parent = nil
}

// deleteAttr is a helper that deletes a typed attribute, ignoring nil.
func deleteAttr[T any](a *attr.Attribute[T]) {
	if a == nil {
		return
	}
	attr.Delete(a)
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


// NewAppWindowLayout creates layout attributes for the root AppWindow interactor.
// Width and Height are value attributes set by rachel via BackingStoreReady /
// WindowResized messages (outside-in sizing). The constraint name is always
// "AppWindow" so that rachel can locate it at a well-known path.
func NewAppWindowLayout() *LayoutAttributes {
	myName := "AppWindow"
	lh := NewLayoutAttributesBase(myName, "")

	// Outside-in: rachel sets these via BackingStoreReady / WindowResized.
	lh.Width = attr.ValueI64(
		LayoutURI(myName, DataTypeInt64, LayoutWidth), 0)
	lh.Height = attr.ValueI64(
		LayoutURI(myName, DataTypeInt64, LayoutHeight), 0)

	lh.InitBounds(myName)
	return lh
}

// NewBridgeLayout creates layout attributes for the topmost child of an AppWindow.
// Width and Height are constraints that clamp AppWindow's outside-in dimensions
// to the given [min, max] range, bridging outside-in sizing (from rachel) to
// inside-out sizing (children below compute their own sizes).
func NewBridgeLayout(myName string, minW, maxW, minH, maxH int64) *LayoutAttributes {
	lh := NewLayoutAttributesBase(myName, "AppWindow")

	appWURI := LayoutURI("AppWindow", DataTypeInt64, LayoutWidth)
	appHURI := LayoutURI("AppWindow", DataTypeInt64, LayoutHeight)

	minWURI := LayoutURI(myName, DataTypeInt64, "MinWidth")
	attr.ValueI64(minWURI, minW)
	maxWURI := LayoutURI(myName, DataTypeInt64, "MaxWidth")
	attr.ValueI64(maxWURI, maxW)
	minHURI := LayoutURI(myName, DataTypeInt64, "MinHeight")
	attr.ValueI64(minHURI, minH)
	maxHURI := LayoutURI(myName, DataTypeInt64, "MaxHeight")
	attr.ValueI64(maxHURI, maxH)

	lh.Width = attr.ConstraintI64(
		LayoutURI(myName, DataTypeInt64, LayoutWidth),
		BindStrings(ProgInoutBridge, "_dim_", appWURI, "_min_", minWURI, "_max_", maxWURI))

	lh.Height = attr.ConstraintI64(
		LayoutURI(myName, DataTypeInt64, LayoutHeight),
		BindStrings(ProgInoutBridge, "_dim_", appHURI, "_min_", minHURI, "_max_", maxHURI))

	lh.InitBounds(myName)
	return lh
}

// NewDecoratorLayout creates layout attributes with inside-out constraint sizing
// for decorator-style parents. The parent interactor is used to extract the
// parent's constraint-system name. hPadding and vPadding are the half-padding
// for width and height respectively: width = child.Width + 2*hPadding,
// height = child.Height + 2*vPadding (both clamped to maxSize).
// For symmetric decorators (equal insets), pass the same value for both.
// If no child is found, defaults from ProgDecorationWidth/Height apply (20/30).
func NewDecoratorLayout(name string, parent Interactor, hPadding, vPadding, maxSize int64) *LayoutAttributes {
	parentName := ""
	if parent != nil {
		if l, ok := parent.(Layouter); ok {
			lh := l.GetLayout()
			if lh != nil {
				parentName = lh.Name()
			}
		}
	}
	return NewDecoratorLayoutByParentName(name, parentName, hPadding, vPadding, maxSize)
}

// NewDecoratorLayoutByParentName is the string-based variant of NewDecoratorLayout.
// Prefer NewDecoratorLayout which takes an Interactor — this version exists for
// cases where only the parent's constraint-system name is available.
func NewDecoratorLayoutByParentName(myName, parentName string, hPadding, vPadding, maxSize int64) *LayoutAttributes {
	lh := NewLayoutAttributesBase(myName, parentName)

	hPaddingURI := LayoutURI(myName, DataTypeInt64, LayoutHPadding)
	attr.ValueI64(hPaddingURI, hPadding)

	vPaddingURI := LayoutURI(myName, DataTypeInt64, LayoutVPadding)
	attr.ValueI64(vPaddingURI, vPadding)

	maxSizeURI := LayoutURI(myName, DataTypeInt64, LayoutMaxSize)
	attr.ValueI64(maxSizeURI, maxSize)

	widthProg := BindStringsChildren(ProgDecorationWidth,
		"_myName_", myName, "_padding_", hPaddingURI, "_maxSize_", maxSizeURI)
	lh.Width = attr.ConstraintI64(
		LayoutURI(myName, DataTypeInt64, LayoutWidth), widthProg)

	heightProg := BindStringsChildren(ProgDecorationHeight,
		"_myName_", myName, "_padding_", vPaddingURI, "_maxSize_", maxSizeURI)
	lh.Height = attr.ConstraintI64(
		LayoutURI(myName, DataTypeInt64, LayoutHeight), heightProg)

	lh.InitBounds(myName)
	return lh
}
