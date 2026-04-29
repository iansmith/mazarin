package std

import (
	"fmt"
	"image"

	"mazzy/mazarin/attr"
	"mazzy/mazarin/mancini"
	"mazzy/mazarin/mancini/impl"
)

// WebScrollerVertical bundles a [WebInteractor] with a vertical
// [Scrollbar] to its right. All constraint wiring is internal:
//
//   - Scrollbar.Value is the source of truth for scroll position.
//   - WebInteractor.ScrollY is constrained to Scrollbar.Value, so a user
//     drag on the thumb scrolls the rendered page.
//   - Scrollbar.Max is constrained to (WebInteractor.ContentHeight -
//     WebInteractor.Height), so the thumb's range tracks how much
//     content is offscreen.
//   - Scrollbar visibility is tied to (ContentHeight > panelHeight) so
//     the bar disappears for short messages that fit fully.
//
// Width and Height of WebScrollerVertical are constrained to the
// caller-supplied parent dimension URIs (mirrors ScrollerVertical).
// The internal WebInteractor occupies the whole area minus trackWidth
// on the right.
//
// This is the standard wrapper for embedding the web component in a
// mancini layout — the mail app uses it for the message body panel.
type WebScrollerVertical struct {
	impl.Interactor
	impl.Parent

	// Web is the inner [WebInteractor] that renders HTML content.
	Web       *WebInteractor
	scrollbar *Scrollbar

	// scrollValue is the scrollbar's Value attribute — the source of
	// truth that drives Web.ScrollY via constraint. Stored separately so
	// SetHTML can reset it without going through the (constraint-typed)
	// Web.ScrollY (which would panic on Set()).
	scrollValue *attr.Attribute[int64]

	trackWidth int64
	pal        mancini.Palette
}

// SetHTML replaces the HTML content of the inner WebInteractor and
// resets the scrollbar to the top so the user always sees the start of
// the new message. Use this instead of Web.SetHTML when the scroller is
// wired up via constraints — directly calling Web.SetHTML works but
// leaves the scrollbar pinned to the previous message's offset.
func (wsv *WebScrollerVertical) SetHTML(html []byte) {
	fmt.Printf("[wsv] SetHTML enter (%d bytes)\n", len(html))
	wsv.Web.SetHTML(html)
	fmt.Printf("[wsv] SetHTML resetting scrollValue\n")
	wsv.scrollValue.Set(0)
	fmt.Printf("[wsv] SetHTML done\n")
}

// NewWebScrollerVertical creates a WebScrollerVertical wrapping a fresh
// WebInteractor and a standard vertical Scrollbar. The wrapper's outer
// Width/Height are value-typed, set by the parent layout each frame
// (e.g. ColumnPercentage propagates its child width). trackWidth is the
// pixel width reserved for the scrollbar.
//
// Inside, the WebInteractor's Width is constrained to (outer width -
// trackWidth) and its Height is constrained to outer height, so layout
// changes flow through the constraint network without manual wiring.
func NewWebScrollerVertical(myName, parent string,
	theme mancini.Theme, pal mancini.Palette,
	trackWidth int64,
	engine WebRenderEngine) *WebScrollerVertical {

	if myName == "" {
		myName = mancini.DefaultName("webscroll")
	}

	// Outer layout: value-typed dimensions set by the parent each frame.
	lh := mancini.NewLayoutAttributes(myName, parent)

	wsv := &WebScrollerVertical{
		trackWidth: trackWidth,
		pal:        pal,
	}
	wsv.Interactor.Initialize(wsv, lh)
	wsv.Parent.Initialize(true, &wsv.Interactor)

	// --- Scrollbar (built first so the WebInteractor's ScrollY can
	// constrain to its Value attribute) ---
	sbName := myName + "_sb"
	sbLH := mancini.NewLayoutAttributesBase(sbName, myName)
	sbLH.Width = attr.ValueI64(
		mancini.LayoutURI(sbName, mancini.DataTypeInt64, mancini.LayoutWidth),
		trackWidth)
	sbLH.Height = attr.ConstraintI64(
		mancini.LayoutURI(sbName, mancini.DataTypeInt64, mancini.LayoutHeight),
		mancini.EqualI64(lh.Height.URI()))
	sbLH.InitBounds(sbName)

	valueAttr := attr.ValueI64(
		mancini.LayoutURI(sbName, mancini.DataTypeInt64, "Value"), 0)
	maxAttr := attr.ValueI64(
		mancini.LayoutURI(sbName, mancini.DataTypeInt64, "Max"), 0)

	sb := NewScrollbar(sbLH, theme, true, float64(trackWidth), 1.0, 0.0, false)
	sb.ValueAttr = valueAttr
	sb.MaxAttr = maxAttr
	wsv.scrollbar = sb
	wsv.scrollValue = valueAttr

	// --- WebInteractor (width = wrapper width - trackWidth) ---
	webName := myName + "_web"
	webLH := mancini.NewLayoutAttributesBase(webName, myName)

	trackWidthConst := attr.ValueI64(
		mancini.LayoutURI(myName, mancini.DataTypeInt64, "TrackWidth"),
		trackWidth)
	webLH.Width = attr.ConstraintI64(
		mancini.LayoutURI(webName, mancini.DataTypeInt64, mancini.LayoutWidth),
		mancini.SubI64(lh.Width.URI(), trackWidthConst.URI()))
	webLH.Height = attr.ConstraintI64(
		mancini.LayoutURI(webName, mancini.DataTypeInt64, mancini.LayoutHeight),
		mancini.EqualI64(lh.Height.URI()))
	webLH.InitBounds(webName)

	wsv.Web = NewWebInteractorWithLayout(webName, webLH, engine)

	// Wire ScrollY ← Scrollbar Value, so dragging the thumb updates the
	// rendered viewport. The WebInteractor's published ScrollY attribute
	// is a value-typed slot, so we replace it with a constraint.
	attr.SwapToConstraint(wsv.Web.ScrollY, mancini.EqualI64(valueAttr.URI()))

	// Wire Scrollbar Max ← (ContentHeight - panelHeight). Negative
	// values are pinned to 0 inside the Scrollbar's own clamp, so we
	// don't need a max(0, ...) here.
	attr.SwapToConstraint(maxAttr,
		mancini.SubI64(wsv.Web.ContentHeight.URI(), webLH.Height.URI()))

	return wsv
}

// WebInteractorName returns the constraint-system name of the inner
// WebInteractor — useful when callers want to constrain something
// against its layout attributes.
func (wsv *WebScrollerVertical) WebInteractorName() string {
	return wsv.Web.GetLayout().Name()
}

// Draw positions the WebInteractor on the left and the Scrollbar on the
// right, then delegates to DrawChildren. Mirrors ScrollerVertical.Draw.
func (wsv *WebScrollerVertical) Draw(self mancini.Interactor, x, y, w, h int64, damage image.Rectangle) {
	if !self.Visible() {
		fmt.Printf("[wsv] Draw skip: not visible\n")
		return
	}
	if !wsv.Damaged(damage) {
		fmt.Printf("[wsv] Draw skip: not damaged (incoming damage=%v, my rect=%d,%d %dx%d)\n",
			damage, x, y, w, h)
		return
	}
	fmt.Printf("[wsv] Draw enter x=%d y=%d w=%d h=%d damage=%v\n", x, y, w, h, damage)

	webLH := wsv.Web.GetLayout()
	if webLH != nil {
		webLH.X.Set(x)
		webLH.Y.Set(y)
		if !webLH.Width.IsConstraint() {
			webLH.Width.Set(w - wsv.trackWidth)
		}
		if !webLH.Height.IsConstraint() {
			webLH.Height.Set(h)
		}
	}

	sbLH := wsv.scrollbar.GetLayout()
	if sbLH != nil {
		sbLH.X.Set(x + w - wsv.trackWidth)
		sbLH.Y.Set(y)
	}

	wsv.Parent.DrawChildren(self, x, y, w, h, damage)
	wsv.SnapshotDamage()
}
