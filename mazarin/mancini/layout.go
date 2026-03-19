//go:build linux

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

// Init initializes the mancini layout system with the shepherd's namespace name.
// Must be called after attr.Init().
func Init(name string) {
	manciniPID = name
}

// LayoutHandles holds constraint system handles for an interactor's layout attributes.
type LayoutHandles struct {
	X, Y, Width, Height, Visible *attr.Handle[int64]
	Bounds                       *attr.Handle[vm.Value] // Rectangle2D: (x, y, x+w, y+h)
	BoundsHash                   *attr.Handle[int64]    // hash of X,Y,W,H for fast change detection
	Parent                       *attr.Handle[string]
	SpacingHandle                *attr.Handle[int64] // inter-child spacing (containers only)
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
		X:       attr.ValueI64(layoutURI(name, "int64", "x"), 0),
		Y:       attr.ValueI64(layoutURI(name, "int64", "y"), 0),
		Visible: attr.ValueI64(layoutURI(name, "int64", "visible"), 1),
		Parent:  attr.ValueStr(layoutURI(name, "str", "parent"), parent),
	}
}

// newLayoutHandles creates all layout handles as Value handles,
// plus a Bounds constraint derived from X, Y, Width, Height.
func newLayoutHandles(name, parent string) *LayoutHandles {
	lh := newLayoutHandlesBase(name, parent)
	lh.Width = attr.ValueI64(layoutURI(name, "int64", "width"), 0)
	lh.Height = attr.ValueI64(layoutURI(name, "int64", "height"), 0)
	lh.initBounds(name)
	return lh
}

// initBounds creates the Bounds constraint: rect(X, Y, X+Width, Y+Height),
// plus a BoundsHash constraint for fast layout-change detection.
func (lh *LayoutHandles) initBounds(name string) {
	prog := interactor.BindStrings(ProgBoundsFromXywh,
		lh.X.URI(), lh.Y.URI(), lh.Width.URI(), lh.Height.URI())
	lh.Bounds = attr.ConstraintComposite(
		layoutURI(name, "rect", "bounds"),
		flat.TypeRectangle, prog)

	hashProg := interactor.BindStrings(ProgBoundsHash,
		lh.X.URI(), lh.Y.URI(), lh.Width.URI(), lh.Height.URI())
	lh.BoundsHash = attr.ConstraintI64(layoutURI(name, "int64", "boundsHash"), hashProg)
}

// setVisibleHandle sets the Visible handle value.
func setVisibleHandle(lh *LayoutHandles, v int64) {
	if lh != nil && lh.Visible != nil {
		lh.Visible.Set(v)
	}
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

// InitLayout methods for each widget type.

func (w *AppWindow) InitLayout(parent string) {
	if w.Name == "" {
		w.Name = defaultName("appwindow")
	}
	t := w.Theme
	w.Layout = newLayoutHandlesBase(w.Name, parent)
	w.Layout.constraintWidth = true
	w.Layout.constraintHeight = true

	// Horizontal margin: tbMargin on each side.
	hMargin := int64(t.Px(8))
	hMarginURI := layoutURI(w.Name, "int64", "hMargin")
	attr.ValueI64(hMarginURI, hMargin)

	// Vertical margin: (tbMargin + tbH + gap + tbMargin) / 2
	// so that child + 2*vMargin = child + full vertical chrome.
	tbH := t.Px(26) // default title bar height
	if s, ok := w.TitleBar.(Sizer); ok {
		tbH = s.PreferredHeight()
	}
	vOverhead := t.Px(8) + tbH + t.Px(6) + t.Px(8)
	vMargin := int64(vOverhead / 2)
	vMarginURI := layoutURI(w.Name, "int64", "vMargin")
	attr.ValueI64(vMarginURI, vMargin)

	// Max size (800 logical pixels).
	maxSizeURI := layoutURI(w.Name, "int64", "maxSize")
	attr.ValueI64(maxSizeURI, 800)

	findPattern := "attr:///shepherd/" + manciniPID + "/str/*/layout/parent"
	prefix := "attr:///shepherd/" + manciniPID + "/int64/"

	widthProg := interactor.BindStrings(ProgDecorationWidth,
		findPattern, w.Name, hMarginURI, prefix, "/layout/width", maxSizeURI)
	w.Layout.Width = attr.ConstraintI64(
		layoutURI(w.Name, "int64", "width"), widthProg)

	heightProg := interactor.BindStrings(ProgDecorationHeight,
		findPattern, w.Name, vMarginURI, prefix, "/layout/height", maxSizeURI)
	w.Layout.Height = attr.ConstraintI64(
		layoutURI(w.Name, "int64", "height"), heightProg)

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
}

func (s *Spacer) InitLayout(parent string) {
	if s.Name == "" {
		s.Name = defaultName("spacer")
	}
	s.Layout = newLayoutHandles(s.Name, parent)
}
