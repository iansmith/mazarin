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
func Init() {
	manciniPID = attr.SID()
}

// LayoutHandles holds constraint system handles for an interactor's layout attributes.
type LayoutHandles struct {
	X, Y, Width, Height, Visible *attr.Handle[int64]
	Bounds                       *attr.Handle[vm.Value] // Rectangle2D: (x, y, x+w, y+h)
	BoundsHash                   *attr.Handle[int64]    // hash of X,Y,W,H for fast change detection
	Parent                       *attr.Handle[string]
	SpacingHandle                *attr.Handle[int64] // inter-child spacing (containers only)
	CrossAlignHandle             *attr.Handle[int64] // cross-axis alignment (containers only)
	constraintWidth              bool                // true if Width is a constraint (don't Set)
	constraintHeight             bool                // true if Height is a constraint (don't Set)
	constraintY                  bool                // true if Y is a constraint (don't Set)
}

var nameCounter uint64

func defaultName(typeName string) string {
	n := atomic.AddUint64(&nameCounter, 1)
	return typeName + strconv.FormatUint(n, 10)
}

func layoutURI(name, typePath, attrName string) string {
	return "attr:///shepherd/" + manciniPID + "/" + typePath + "/" + name + "/layout/" + attrName
}

// newLayoutHandlesBase creates X, Y, Visible, Parent handles (no Width/Height).
func newLayoutHandlesBase(name, parent string) *LayoutHandles {
	return &LayoutHandles{
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
		lh.X.URI(), lh.Y.URI(), lh.Width.URI(), lh.Height.URI())
	lh.Bounds = attr.ConstraintComposite(
		layoutURI(name, "rect", "Bounds"),
		flat.TypeRectangle, prog)

	hashProg := interactor.BindStrings(ProgBoundsHash,
		lh.X.URI(), lh.Y.URI(), lh.Width.URI(), lh.Height.URI())
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
	t := w.Theme
	w.Layout = newLayoutHandlesBase(w.Name, parent)
	w.Layout.constraintWidth = true
	w.Layout.constraintHeight = true

	// Horizontal margin: tbMargin on each side.
	hMargin := int64(t.Px(8))
	hMarginURI := layoutURI(w.Name, "int64", "HMargin")
	attr.ValueI64(hMarginURI, hMargin)

	// Vertical margin: (tbMargin + tbH + gap + tbMargin) / 2
	// so that child + 2*vMargin = child + full vertical chrome.
	tbH := t.Px(26) // default title bar height
	if s, ok := w.TitleBar.(Sizer); ok {
		tbH = s.PreferredHeight()
	}
	vOverhead := t.Px(8) + tbH + t.Px(6) + t.Px(8)
	vMargin := int64(vOverhead / 2)
	vMarginURI := layoutURI(w.Name, "int64", "VMargin")
	attr.ValueI64(vMarginURI, vMargin)

	// Max size (740 logical pixels).
	maxSizeURI := layoutURI(w.Name, "int64", "MaxSize")
	attr.ValueI64(maxSizeURI, 740)

	findPattern := "attr:///shepherd/" + manciniPID + "/str/*/layout/Parent"
	prefix := "attr:///shepherd/" + manciniPID + "/int64/"

	widthProg := interactor.BindStrings(ProgDecorationWidth,
		findPattern, w.Name, hMarginURI, prefix, "/layout/Width", maxSizeURI)
	w.Layout.Width = attr.ConstraintI64(
		layoutURI(w.Name, "int64", "Width"), widthProg)

	heightProg := interactor.BindStrings(ProgDecorationHeight,
		findPattern, w.Name, vMarginURI, prefix, "/layout/Height", maxSizeURI)
	w.Layout.Height = attr.ConstraintI64(
		layoutURI(w.Name, "int64", "Height"), heightProg)

	w.Layout.initBounds(w.Name)
}

func (w *FreeFloatingWindow) InitLayout(parent string) {
	if w.Name == "" {
		w.Name = defaultName("floater")
	}
	w.Layout = newLayoutHandles(w.Name, parent)
}

func (b *Button) InitLayout(parent string) {
	if b.Name == "" {
		b.Name = defaultName("button")
	}
	b.Layout = newLayoutHandles(b.Name, parent)
}

func (l *NeuLabel) InitLayout(parent string) {
	if l.Name == "" {
		l.Name = defaultName("neulabel")
	}
	l.Layout = newLayoutHandles(l.Name, parent)
}

func (l *Label) InitLayout(parent string) {
	if l.Name == "" {
		l.Name = defaultName("label")
	}
	l.Layout = newLayoutHandles(l.Name, parent)

	// Publish intrinsic dimensions so constraint programs can bootstrap
	// without waiting for a Draw pass.
	if l.Theme != nil {
		l.Layout.Height.Set(int64(l.FontSize + l.Theme.Px(4)))
		text := l.Text
		if l.TextFunc != nil {
			text = l.TextFunc()
		}
		l.Layout.Width.Set(int64(l.Theme.MeasureText(text, l.Bold, l.FontSize) + l.Theme.Px(8)))
	}
}

func (s *Spacer) InitLayout(parent string) {
	if s.Name == "" {
		s.Name = defaultName("spacer")
	}
	s.Layout = newLayoutHandles(s.Name, parent)

	// Publish intrinsic dimensions so constraint programs can bootstrap.
	s.Layout.Width.Set(int64(s.PreferredW))
	s.Layout.Height.Set(int64(s.PreferredH))
}
