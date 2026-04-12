package std

import (
	"image"

	"mazzy/mazarin/attr"
	"mazzy/mazarin/mancini"
	"mazzy/mazarin/mancini/impl"
)

// ScrollbarStyle selects which scrollbar implementation to use.
type ScrollbarStyle int

const (
	ScrollbarStandard ScrollbarStyle = iota
	ScrollbarLight
)

// ScrollerVertical bundles a Scroller with a vertical Scrollbar to
// its right. All constraint wiring is internal:
//
//   - Scrollbar.Value is the source of truth for scroll position.
//   - Scroller.VirtualY is constrained to Scrollbar.Value.
//   - Scroller.MaxScrollY flows to Scrollbar.Max.
//   - Scrollbar visibility is tied to Scroller.ScrollNeededY.
//
// The Scroller's width is constrained to (parent width - trackWidth).
// The Scroller's height is constrained to the parent's height.
// Children declare the Scroller as their parent via ScrollerName().
type ScrollerVertical struct {
	impl.Interactor
	impl.Parent

	Scroller  *Scroller
	scrollbar mancini.Interactor // standard or light

	// Typed references for value/max wiring — one will be non-nil.
	stdScrollbar   *Scrollbar
	lightScrollbar *LightScrollbar

	trackWidth int64
	pal        mancini.Palette
}

// NewScrollerVertical creates a ScrollerVertical. The caller provides
// width and height constraints for the ScrollerVertical itself (these
// typically come from the parent's dimensions). trackWidth is the
// pixel width reserved for the scrollbar.
func NewScrollerVertical(myName, parent string,
	theme mancini.Theme, pal mancini.Palette,
	parentWidthURI, parentHeightURI string,
	trackWidth int64,
	style ScrollbarStyle) *ScrollerVertical {

	if myName == "" {
		myName = mancini.DefaultName("scrollvert")
	}

	// ScrollerVertical layout: width = parent width, height = parent height.
	lh := mancini.NewLayoutAttributesBase(myName, parent)
	lh.Width = attr.ConstraintI64(
		mancini.LayoutURI(myName, mancini.DataTypeInt64, mancini.LayoutWidth),
		mancini.EqualI64(parentWidthURI))
	lh.Height = attr.ConstraintI64(
		mancini.LayoutURI(myName, mancini.DataTypeInt64, mancini.LayoutHeight),
		mancini.EqualI64(parentHeightURI))
	lh.InitBounds(myName)

	sv := &ScrollerVertical{
		trackWidth: trackWidth,
		pal:        pal,
	}
	sv.Interactor.Initialize(sv, lh)
	sv.Parent.Initialize(true, &sv.Interactor)

	// --- Scrollbar ---
	// Created first so VirtualY can be constrained to its Value.
	sbName := myName + "_sb"
	sbLH := mancini.NewLayoutAttributesBase(sbName, myName)
	sbLH.Width = attr.ValueI64(
		mancini.LayoutURI(sbName, mancini.DataTypeInt64, mancini.LayoutWidth),
		trackWidth)
	sbLH.Height = attr.ConstraintI64(
		mancini.LayoutURI(sbName, mancini.DataTypeInt64, mancini.LayoutHeight),
		mancini.EqualI64(lh.Height.URI()))
	sbLH.InitBounds(sbName)

	// Scrollbar Value: source of truth for scroll position.
	valueAttr := attr.ValueI64(
		mancini.LayoutURI(sbName, mancini.DataTypeInt64, "Value"), 0)

	switch style {
	case ScrollbarLight:
		lsb := NewLightScrollbar(sbLH, theme, true, 1.0, 0.0)
		lsb.ValueAttr = valueAttr
		sv.lightScrollbar = lsb
		sv.scrollbar = lsb
	default:
		ssb := NewScrollbar(sbLH, theme, true, float64(trackWidth), 1.0, 0.0, false)
		ssb.ValueAttr = valueAttr
		sv.stdScrollbar = ssb
		sv.scrollbar = ssb
	}

	// --- Scroller ---
	// Width = ScrollerVertical width - trackWidth.
	scrollName := myName + "_scr"

	// Track width as a value attribute for SubI64.
	trackWidthConst := attr.ValueI64(
		mancini.LayoutURI(myName, mancini.DataTypeInt64, "TrackWidth"),
		trackWidth)

	scrollWidth := attr.ConstraintI64(
		mancini.LayoutURI(scrollName, mancini.DataTypeInt64, mancini.LayoutWidth),
		mancini.SubI64(lh.Width.URI(), trackWidthConst.URI()))
	scrollHeight := attr.ConstraintI64(
		ScrollerHeightURI(scrollName),
		mancini.EqualI64(lh.Height.URI()))

	// VirtualY constrained to scrollbar's Value.
	virtualY := attr.ConstraintI64(
		mancini.LayoutURI(scrollName, mancini.DataTypeInt64, "VirtualY"),
		mancini.EqualI64(valueAttr.URI()))

	sv.Scroller = NewScroller(scrollName, myName, pal, scrollWidth, scrollHeight, virtualY)
	sv.Scroller.ScrollValueY = valueAttr

	// Scrollbar Max constrained to Scroller's MaxScrollY.
	maxAttr := attr.ConstraintI64(
		mancini.LayoutURI(sbName, mancini.DataTypeInt64, "Max"),
		mancini.EqualI64(sv.Scroller.MaxScrollYURI()))

	if sv.stdScrollbar != nil {
		sv.stdScrollbar.MaxAttr = maxAttr
	} else {
		sv.lightScrollbar.MaxAttr = maxAttr
	}

	// Scrollbar visibility constrained to ScrollNeededY.
	// Replace the default value Visible (from NewLayoutAttributesBase) with a constraint.
	scrollNeededURI := sv.Scroller.ScrollNeededY.URI()
	attr.SwapToConstraint(sbLH.Visible,
		mancini.EqualBool(scrollNeededURI))

	return sv
}

// ScrollerName returns the constraint-system name of the internal
// Scroller. Children should use this as their parent name.
func (sv *ScrollerVertical) ScrollerName() string {
	return sv.Scroller.GetLayout().Name()
}

// ScrollerWidthURI returns the URI of the Scroller's Width attribute.
// Children can constrain their width to this.
func (sv *ScrollerVertical) ScrollerWidthURI() string {
	return sv.Scroller.GetLayout().Width.URI()
}

// Draw implements mancini.NewDrawer. Positions Scroller and Scrollbar,
// then delegates to DrawChildren.
func (sv *ScrollerVertical) Draw(self mancini.Interactor, x, y, w, h int64, damage image.Rectangle) {
	if !self.Visible() {
		return
	}
	if !sv.Damaged(damage) {
		return
	}

	// Position scroller.
	scrollLH := sv.Scroller.GetLayout()
	if scrollLH != nil {
		scrollLH.X.Set(x)
		scrollLH.Y.Set(y)
		if !scrollLH.Width.IsConstraint() {
			scrollLH.Width.Set(w - sv.trackWidth)
		}
		if !scrollLH.Height.IsConstraint() {
			scrollLH.Height.Set(h)
		}
	}

	// Position scrollbar to the right.
	sbLH := sv.scrollbar.(mancini.Layouter).GetLayout()
	if sbLH != nil {
		sbLH.X.Set(x + w - sv.trackWidth)
		sbLH.Y.Set(y)
	}

	sv.Parent.DrawChildren(self, x, y, w, h, damage)
	sv.SnapshotDamage()
}
