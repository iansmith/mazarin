package mancini

import (
	"strconv"
	"sync/atomic"

	"mazzy/mazarin/attr"
	"mazzy/mazarin/interactor"
	"mazzy/mazarin/vm"
	"mazzy/mazarin/vm/flat"
)

var manciniPID string

// Init initializes the mancini layout system using the shepherd's SID from attr.SID().
// Must be called after attr.Init().
const visSuffix = "/layout/Visible"

func Init() {
	manciniPID = attr.SID()
}

// LayoutHandles holds constraint system handles for an interactor's layout attributes.
type LayoutHandles struct {
	name                         string               // interactor's constraint-system name
	X, Y, Width, Height, Visible *attr.Handle[int64]
	Bounds                       *attr.Handle[vm.Value] // Rectangle2D: (x, y, x+w, y+h)
	BoundsHash                   *attr.Handle[int64]    // hash of X,Y,W,H for fast change detection
	Parent                       *attr.Handle[string]
	SpacingHandle                *attr.Handle[int64] // inter-child spacing (containers only)
	CrossAlignHandle             *attr.Handle[int64] // cross-axis alignment (containers only)
	MaxWidthHandle               *attr.Handle[int64] // max width for overflow clipping (Row only)
	LastChildDrawnHandle         *attr.Handle[int64] // 0-based index of last child to draw (Row only)
	constraintWidth              bool                // true if Width is a constraint (don't Set)
	constraintHeight             bool                // true if Height is a constraint (don't Set)
	constraintY                  bool                // true if Y is a constraint (don't Set)
}

// Name returns the interactor's constraint-system name.
func (lh *LayoutHandles) Name() string {
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

func layoutURI(name, typePath, attrName string) string {
	return "attr:///shepherd/" + manciniPID + "/" + typePath + "/" + name + "/layout/" + attrName
}

// newLayoutHandlesBase creates X, Y, Visible, Parent handles (no Width/Height).
func newLayoutHandlesBase(name, parent string) *LayoutHandles {
	return &LayoutHandles{
		name:    name,
		X:       attr.ValueI64(layoutURI(name, "int64", "X"), 0),
		Y:       attr.ValueI64(layoutURI(name, "int64", "Y"), 0),
		Visible: attr.ValueI64(layoutURI(name, "int64", "Visible"), 1),
		Parent:  attr.ValueStr(layoutURI(name, "str", "Parent"), parent),
	}
}

// newLayoutHandles creates all layout handles as Value handles,
// plus a Bounds constraint derived from X, Y, Width, Height.
func newLayoutHandles(name, parent string) *LayoutHandles {
	lh := newLayoutHandlesBase(name, parent)
	lh.Width = attr.ValueI64(layoutURI(name, "int64", "Width"), 0)
	lh.Height = attr.ValueI64(layoutURI(name, "int64", "Height"), 0)
	lh.initBounds(name)
	return lh
}

// initBounds creates the Bounds constraint: rect(X, Y, X+Width, Y+Height),
// plus a BoundsHash constraint for fast layout-change detection.
func (lh *LayoutHandles) initBounds(name string) {
	prog := interactor.BindStrings(ProgBoundsFromXywh,
		"_x_", lh.X.URI(), "_y_", lh.Y.URI(), "_width_", lh.Width.URI(), "_height_", lh.Height.URI())
	lh.Bounds = attr.ConstraintComposite(
		layoutURI(name, "rect", "Bounds"),
		flat.TypeRectangle, prog)

	hashProg := interactor.BindStrings(ProgBoundsHash,
		"_x_", lh.X.URI(), "_y_", lh.Y.URI(), "_width_", lh.Width.URI(), "_height_", lh.Height.URI())
	lh.BoundsHash = attr.ConstraintI64(layoutURI(name, "int64", "BoundsHash"), hashProg)
}

// setVisibleHandle sets the Visible handle value.
func setVisibleHandle(lh *LayoutHandles, v int64) {
	if lh != nil && lh.Visible != nil {
		lh.Visible.Set(v)
	}
}

// isVisibleHandle returns true if the interactor is visible (default true if no handle).
func isVisibleHandle(lh *LayoutHandles) bool {
	if lh == nil || lh.Visible == nil {
		return true
	}
	return lh.Visible.Get() != 0
}

// boundsHashValue returns the current bounds hash, or 0 if unavailable.
func (lh *LayoutHandles) boundsHashValue() int64 {
	if lh == nil || lh.BoundsHash == nil {
		return 0
	}
	return lh.BoundsHash.Get()
}

// GetSpacing returns the current inter-child spacing value, or 0 if unavailable.
func (lh *LayoutHandles) GetSpacing() float64 {
	if lh == nil || lh.SpacingHandle == nil {
		return 0
	}
	return float64(lh.SpacingHandle.Get())
}

// setLayoutSpacing sets the inter-child spacing value on a LayoutHandles.
func setLayoutSpacing(lh *LayoutHandles, v float64) {
	if lh != nil && lh.SpacingHandle != nil {
		lh.SpacingHandle.Set(int64(v))
	}
}

// GetCrossAlign returns the current cross-axis alignment, or AxisMinimum if unavailable.
func (lh *LayoutHandles) GetCrossAlign() Alignment {
	if lh == nil || lh.CrossAlignHandle == nil {
		return AxisMinimum
	}
	return Alignment(lh.CrossAlignHandle.Get())
}

// setLayoutCrossAlign sets the cross-axis alignment value on a LayoutHandles.
func setLayoutCrossAlign(lh *LayoutHandles, v Alignment) {
	if lh != nil && lh.CrossAlignHandle != nil {
		lh.CrossAlignHandle.Set(int64(v))
	}
}

// childLayoutWidth reads a child's width from its constraint layout handles.
// Returns fallback if the child has no layout or its width is 0.
func childLayoutWidth(d Drawer, fallback float64) float64 {
	if l, ok := d.(Layouter); ok {
		lh := l.GetLayout()
		if lh != nil && lh.Width != nil {
			if v := float64(lh.Width.Get()); v > 0 {
				return v
			}
		}
	}
	return fallback
}

// childLayoutHeight reads a child's height from its constraint layout handles.
// Returns fallback if the child has no layout or its height is 0.
func childLayoutHeight(d Drawer, fallback float64) float64 {
	if l, ok := d.(Layouter); ok {
		lh := l.GetLayout()
		if lh != nil && lh.Height != nil {
			if v := float64(lh.Height.Get()); v > 0 {
				return v
			}
		}
	}
	return fallback
}

// publishLayout writes position and size to an interactor's layout handles.
// Skips Width if constraintWidth is true, Height if constraintHeight is true.
func publishLayout(l *LayoutHandles, x, y, w, h float64) {
	if l == nil {
		return
	}
	l.X.Set(int64(x))
	if !l.constraintY {
		l.Y.Set(int64(y))
	}
	if !l.constraintWidth {
		l.Width.Set(int64(w))
	}
	if !l.constraintHeight {
		l.Height.Set(int64(h))
	}
}

// InitLayout methods for each interactor type.

func (w *AppWindow) InitLayout(parent string) {
	// AppWindow always uses "AppWindow" as its constraint name so rachel
	// can locate it at a well-known path: attr:///shepherd/{pid}/rect/AppWindow/Bounds
	w.Name = "AppWindow"
	w.Layout = newLayoutHandlesBase(w.Name, parent)
	w.Layout.constraintWidth = true
	w.Layout.constraintHeight = true

	// Shadow margin: the neumorphic shadow extent outside the NeuBox face.
	// Published bounds include this margin so shadows don't get clipped.
	w.shadowMargin = NeuMaxPad(WindowParams)
	sm := w.shadowMargin

	// Horizontal margin: tbMargin + shadowMargin on each side.
	hMargin := int64(8) + sm
	hMarginURI := layoutURI(w.Name, "int64", "HMargin")
	attr.ValueI64(hMarginURI, hMargin)

	// Vertical margin: (tbMargin + tbH + gap + tbMargin) / 2 + shadowMargin
	// so that child + 2*vMargin = child + full vertical chrome + shadow.
	tbH := 26.0 // default title bar height
	if s, ok := w.TitleBar.(Sizer); ok {
		tbH = s.PreferredHeight()
	}
	vOverhead := 8 + tbH + 6 + 8
	vMargin := int64(vOverhead/2) + sm
	vMarginURI := layoutURI(w.Name, "int64", "VMargin")
	attr.ValueI64(vMarginURI, vMargin)

	// Max size (default 740 logical pixels), including shadow margin.
	maxW := w.MaxWidth
	if maxW <= 0 {
		maxW = 740
	}
	maxW += 2 * sm // maxWidth accounts for shadow on both sides
	maxSizeURI := layoutURI(w.Name, "int64", "MaxSize")
	attr.ValueI64(maxSizeURI, maxW)

	findPattern := "attr:///shepherd/" + manciniPID + "/str/*/layout/Parent"
	prefix := "attr:///shepherd/" + manciniPID + "/int64/"

	widthProg := interactor.BindStrings(ProgDecorationWidth,
		"_findPattern_", findPattern, "_myName_", w.Name, "_margin_", hMarginURI,
		"_int64Prefix_", prefix, "_widthSuffix_", "/layout/Width", "_maxSize_", maxSizeURI, "_visSuffix_", visSuffix)
	w.Layout.Width = attr.ConstraintI64(
		layoutURI(w.Name, "int64", "Width"), widthProg)

	heightProg := interactor.BindStrings(ProgDecorationHeight,
		"_findPattern_", findPattern, "_myName_", w.Name, "_margin_", vMarginURI,
		"_int64Prefix_", prefix, "_heightSuffix_", "/layout/Height", "_maxSize_", maxSizeURI, "_visSuffix_", visSuffix)
	w.Layout.Height = attr.ConstraintI64(
		layoutURI(w.Name, "int64", "Height"), heightProg)

	w.Layout.initBounds(w.Name)
}

func (w *FreeFloatingWindow) InitLayout(parent string) {
	if w.Name == "" {
		w.Name = DefaultName("floater")
	}
	w.Layout = newLayoutHandles(w.Name, parent)
}

func (b *Button) InitLayout(parent string) {
	if b.Name == "" {
		b.Name = DefaultName("button")
	}
	b.Layout = newLayoutHandles(b.Name, parent)
}

func (l *NeuLabel) InitLayout(parent string) {
	if l.Name == "" {
		l.Name = DefaultName("neulabel")
	}
	l.Layout = newLayoutHandles(l.Name, parent)
}

func (l *Label) InitLayout(parent string) {
	if l.Name == "" {
		l.Name = DefaultName("label")
	}
	l.Layout = newLayoutHandles(l.Name, parent)

	// Publish intrinsic dimensions so constraint programs can bootstrap
	// without waiting for a Draw pass.
	if l.Fonts != nil {
		l.Layout.Height.Set(int64(l.FontSize + 4))
		text := l.Text
		if l.TextFunc != nil {
			text = l.TextFunc()
		}
		l.Layout.Width.Set(int64(l.Fonts.MeasureText(text, l.Bold, l.FontSize) + 8))
	}
}

func (s *Spacer) InitLayout(parent string) {
	if s.Name == "" {
		s.Name = DefaultName("spacer")
	}
	s.Layout = newLayoutHandles(s.Name, parent)

	// Publish intrinsic dimensions so constraint programs can bootstrap.
	s.Layout.Width.Set(int64(s.PreferredW))
	s.Layout.Height.Set(int64(s.PreferredH))
}

// NewDecoratorLayout creates layout handles with inside-out constraint sizing
// for decorator-style parents. The parent interactor is used to extract the
// parent's constraint-system name. hMargin and vMargin are the half-margins
// for width and height respectively: width = child.Width + 2*hMargin,
// height = child.Height + 2*vMargin (both clamped to maxSize).
// For symmetric decorators (equal insets), pass the same value for both.
// If no child is found, defaults from ProgDecorationWidth/Height apply (20/30).
func NewDecoratorLayout(name string, parent Interactor, hMargin, vMargin, maxSize int64) *LayoutHandles {
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
func NewDecoratorLayoutByParentName(name, parentName string, hMargin, vMargin, maxSize int64) *LayoutHandles {
	lh := newLayoutHandlesBase(name, parentName)
	lh.constraintWidth = true
	lh.constraintHeight = true

	hMarginURI := layoutURI(name, "int64", "HMargin")
	attr.ValueI64(hMarginURI, hMargin)

	vMarginURI := layoutURI(name, "int64", "VMargin")
	attr.ValueI64(vMarginURI, vMargin)

	maxSizeURI := layoutURI(name, "int64", "MaxSize")
	attr.ValueI64(maxSizeURI, maxSize)

	findPattern := "attr:///shepherd/" + manciniPID + "/str/*/layout/Parent"
	prefix := "attr:///shepherd/" + manciniPID + "/int64/"

	widthProg := interactor.BindStrings(ProgDecorationWidth,
		"_findPattern_", findPattern, "_myName_", name, "_margin_", hMarginURI,
		"_int64Prefix_", prefix, "_widthSuffix_", "/layout/Width", "_maxSize_", maxSizeURI, "_visSuffix_", visSuffix)
	lh.Width = attr.ConstraintI64(
		layoutURI(name, "int64", "Width"), widthProg)

	heightProg := interactor.BindStrings(ProgDecorationHeight,
		"_findPattern_", findPattern, "_myName_", name, "_margin_", vMarginURI,
		"_int64Prefix_", prefix, "_heightSuffix_", "/layout/Height", "_maxSize_", maxSizeURI, "_visSuffix_", visSuffix)
	lh.Height = attr.ConstraintI64(
		layoutURI(name, "int64", "Height"), heightProg)

	lh.initBounds(name)
	return lh
}
