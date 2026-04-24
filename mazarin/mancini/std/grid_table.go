package std

import (
	"fmt"
	"image"
	"math"

	"mazzy/mazarin/attr"
	"mazzy/mazarin/mancini"
	"mazzy/mazarin/mancini/impl"
	"mazzy/shared/hid"
)

// GridRow is the interface that each row's data must satisfy.
// The grid table calls these methods to populate column labels.
type GridRow interface {
	Sender() string
	Subject() string
	Date() string
	MsgNum() uint32
}

// GridFrame is a simple parent interactor that wraps a [GridTable] together
// with its visual chrome: side-padded margin, raised NeuBox, and column-
// boundary [Divider] markers. The frame's height spans the full area
// including the Divider overhang strips above and below the content, so
// Dividers live within normal child bounds — no clip escaping needed.
//
// Draw order is strictly: grid subtree first (content area only), then
// Dividers last so they always appear on top of the grid content.
//
// Column widths are controlled by the same split-attribute mechanism as
// [GridTable]. Drag a Divider marker to resize columns.
type GridFrame struct {
	impl.Interactor
	impl.Parent

	Pal      mancini.Palette
	Overhang int64

	grid     *GridTable
	dividers []*Divider
}

// NewGridFrame creates a fully-wired grid frame. myName is the GridFrame
// itself; internal names are myName+"_margin", myName+"_box", myName+"_tbl",
// and myName+"_divN".
//
// overhang is the height in pixels of the bezel area above and below the
// grid content where Divider triangle markers are drawn.
//
// splitSrcAttrs are the writable value attributes the Dividers write during
// drag operations. They must correspond 1-to-1 with the non-flex entries in
// splitURIs (same order, skipping any -1 flex columns).
func NewGridFrame(myName, parent string, pal mancini.Palette, theme mancini.Theme,
	overhang int64, fontSizeURI string,
	headers []string, percents []float64, splitURIs []string,
	splitSrcAttrs []*attr.Attribute[int64]) *GridFrame {

	if myName == "" {
		myName = mancini.DefaultName("gridframe")
	}

	lh := mancini.NewLayoutAttributes(myName, parent)

	gf := &GridFrame{
		Pal:      pal,
		Overhang: overhang,
	}
	gf.Interactor.Initialize(gf, lh)
	gf.Parent.Initialize(true, &gf.Interactor)

	gridPad := int64(theme.Style().Pad(mancini.LightWeight))

	// Side-padding margin: no top/bottom overhang — GridFrame owns that.
	gridMargin := NewMarginParent(myName+"_margin", myName,
		0, gridPad, 0, gridPad, 0, "", theme, pal)
	_ = gridMargin

	// NeuBox for visual depth, child of the margin.
	boxLH := mancini.NewLayoutAttributes(myName+"_box", myName+"_margin")
	gridBox := NewNeuBoxStyled(boxLH, theme, mancini.Raised, mancini.LightWeight, 8)
	_ = gridBox

	// GridTable as child of the box (no dividers — GridFrame owns them).
	grid := NewGridTable(myName+"_tbl", myName+"_box", pal, theme, fontSizeURI,
		headers, percents, splitURIs)
	gf.grid = grid

	// Dividers at each column boundary where at least one adjacent column
	// is non-flex. A boundary between two non-flex columns uses the LEFT
	// column's splitAttr (normal drag). A boundary where the LEFT column
	// is flex but the RIGHT is not uses the RIGHT column's splitAttr with
	// inverted drag (dragging right narrows the right column).
	splitMap := buildSplitMap(percents)
	nCols := len(percents)
	onDamage := func() {
		// Damage all leaf labels so the constraint chain sees dirty state
		// and the blit region covers divider bezel strips and column content.
		grid.DamageAll()
	}
	for i := 0; i < nCols-1; i++ {
		si := splitMap[i]
		rightSI := splitMap[i+1]

		var srcIdx int
		var inverted bool
		if si >= 0 {
			srcIdx = si
			inverted = false
		} else if rightSI >= 0 {
			srcIdx = rightSI
			inverted = true
		} else {
			continue
		}
		if srcIdx >= len(splitSrcAttrs) {
			continue
		}

		divName := fmt.Sprintf("%s_div%d", myName, i)
		div := NewDivider(divName, myName, pal, i, splitSrcAttrs[srcIdx])
		div.DragInverted = inverted
		div.OnDamage = onDamage
		gf.dividers = append(gf.dividers, div)
	}

	// Wire pixel-position constraints (ScaleI64 chains) then X-overlap constraints.
	for _, div := range gf.dividers {
		div.SetupPositionConstraints()
	}
	if len(gf.dividers) == 2 {
		gf.dividers[0].OtherSplitAttr = gf.dividers[1].SplitAttr
		gf.dividers[1].OtherSplitAttr = gf.dividers[0].SplitAttr
		gf.dividers[0].SetupXConstraint(gf.dividers[1])
		gf.dividers[1].SetupXConstraint(gf.dividers[0])
	}

	return gf
}

// AddRow appends a data row to the embedded GridTable.
// Returns a func() that damages this row's leaf labels — use it as the
// row's OnLoaded callback to trigger a redraw when async data arrives.
func (gf *GridFrame) AddRow(row GridRow) func() {
	return gf.grid.AddRow(row)
}

// SelectedAttr returns the attribute that holds the msgNum of the primary
// selected row (-1 = none). Constrain any viewer component to this URI.
func (gf *GridFrame) SelectedAttr() *attr.Attribute[int64] {
	return gf.grid.SelectedAttr
}

// SelectedSetAttr returns the attribute holding the full multi-selection set
// as []int64 msgNums. Sentinel: [math.MaxInt64] when count > 256.
func (gf *GridFrame) SelectedSetAttr() *attr.Attribute[[]int64] {
	return gf.grid.SelectedSetAttr
}

// SelectedSetCountAttr returns the attribute holding the true count of
// selected rows (regardless of whether the sentinel is active).
func (gf *GridFrame) SelectedSetCountAttr() *attr.Attribute[int64] {
	return gf.grid.SelectedSetCountAttr
}

// SelectedSetPagesAttr returns the attribute holding ceil(count/512):
// the number of 4096-byte pages needed to transfer the full selection via IPC.
func (gf *GridFrame) SelectedSetPagesAttr() *attr.Attribute[int64] {
	return gf.grid.SelectedSetPagesAttr
}

// RefreshSelected re-publishes the selected row's current MsgNum and the
// full selected set. Call this after ShiftMsgNum is applied to any selected row.
func (gf *GridFrame) RefreshSelected() {
	gf.grid.RefreshSelected()
}

// DamageAll marks every label in the grid as needing repaint.
// Call this after batch-adding rows to force the first full draw.
func (gf *GridFrame) DamageAll() {
	gf.grid.DamageAll()
}

// SetTotalRows sets the total collection size for scroll clamping.
func (gf *GridFrame) SetTotalRows(n int64) { gf.grid.SetTotalRows(n) }

// SetRowFactory enables virtual scroll mode with the given row factory.
func (gf *GridFrame) SetRowFactory(f func() GridRow) { gf.grid.SetRowFactory(f) }

// ScrollBy moves the virtual scroll offset by delta rows.
func (gf *GridFrame) ScrollBy(delta int64) { gf.grid.ScrollBy(delta) }

// MoveSelection moves the primary selection by delta rows, scrolling to keep it visible.
func (gf *GridFrame) MoveSelection(delta int64) { gf.grid.MoveSelection(delta) }

// FirstVisibleMsgNumAttr returns the attribute holding the first visible msgNum.
func (gf *GridFrame) FirstVisibleMsgNumAttr() *attr.Attribute[int64] {
	return gf.grid.FirstVisibleMsgNumAttr
}

// LastVisibleMsgNumAttr returns the attribute holding the last visible msgNum.
func (gf *GridFrame) LastVisibleMsgNumAttr() *attr.Attribute[int64] {
	return gf.grid.LastVisibleMsgNumAttr
}

// VisibleRowCountAttr returns the attribute holding the visible row count.
func (gf *GridFrame) VisibleRowCountAttr() *attr.Attribute[int64] {
	return gf.grid.VisibleRowCountAttr
}

// Draw renders the frame. Grid subtree is drawn first in the content area
// (y+Overhang to y+h-Overhang). Bezel strips are cleared to Surface color.
// Dividers are drawn last so they appear on top of everything.
func (gf *GridFrame) Draw(self mancini.Interactor, x, y, w, h int64, damage image.Rectangle) {
	if !gf.Damaged(damage) {
		return
	}
	dc := self.DC()
	if dc == nil {
		return
	}

	oh := gf.Overhang
	contentY := y + oh
	contentH := h - 2*oh
	if contentH < 0 {
		contentH = 0
	}

	// Draw grid subtree (margin → box → table) in the content area.
	for _, child := range gf.GetChildren() {
		if _, isDivider := child.(*Divider); isDivider {
			continue
		}
		if l, ok := child.(mancini.Layouter); ok {
			clh := l.GetLayout()
			if clh != nil {
				clh.X.Set(x)
				clh.Y.Set(contentY)
				if !clh.Width.IsConstraint() {
					clh.Width.Set(w)
				}
				if !clh.Height.IsConstraint() {
					clh.Height.Set(contentH)
				}
			}
		}
		if cs, ok := child.(interface{ SetDC(mancini.DrawContext) }); ok {
			cs.SetDC(dc)
		}
		if d, ok := child.(mancini.NewDrawer); ok {
			d.Draw(child, x, contentY, w, contentH, damage)
		}
	}

	// Clear bezel strips to Surface color. Push/ResetClip bypasses the
	// AppWindow PushDamageClip, which may be smaller than the full frame
	// (content-only damage). Without the reset, FillRectangle is clipped to
	// the content area and the NeuBox border pixel at (y+oh - 0.5) survives.
	// +1 extends the clear to cover the border's anti-aliased bleed pixel.
	// Extend clear by dangle so triangle tips that protrude into the content
	// area (NeuBox margin) are also erased when a divider moves.
	dangle := int64(0)
	if len(gf.dividers) > 0 {
		dangle = gf.dividers[0].Dangle
	}
	clearH := oh + dangle + 1
	dc.Push()
	dc.ResetClip()
	dc.SetColor(gf.Pal.Surface())
	dc.FillRectangle(float64(x), float64(y), float64(w), float64(clearH))
	dc.FillRectangle(float64(x), float64(y+h-clearH+1), float64(w), float64(clearH))
	dc.Pop()

	// Draw Dividers last — on top of grid content, spanning full frame height.
	// Use the GridTable's actual x and w (set by the subtree draw above) so
	// the divider aligns with the column boundary inside the NeuBox padding,
	// not with the outer GridFrame edge.
	//
	// Two-pass: set all raw positions first so paired-divider X constraints
	// (which cross-read RawXAttr / RightXAttr) evaluate correctly, then draw.
	gridX := gf.grid.X()
	gridW := gf.grid.W()

	// Pass 1: update parent geometry attrs (drives ScaleI64 position constraints)
	// and set layout size. RawXAttr and RightXAttr are now constraint outputs —
	// no imperative position computation needed here.
	for _, div := range gf.dividers {
		hw := div.TriHalfW
		div.ParentXAttr.Set(gridX)
		div.ParentWidthAttr.Set(gridW)
		divLH := div.GetLayout()
		if !divLH.X.IsConstraint() {
			divLH.X.Set(div.RawXAttr.Get()) // single-divider fallback
		}
		divLH.Y.Set(y)
		divLH.Width.Set(hw * 2)
		divLH.Height.Set(h)
	}

	// Pass 2: draw using the (possibly constrained) X.
	for _, div := range gf.dividers {
		divLH := div.GetLayout()
		actualDivX := divLH.X.Get()
		divW := divLH.Width.Get()
		div.SetDC(dc)
		div.Draw(div, actualDivX, y, divW, h, damage)
	}

	gf.Interactor.SnapshotDamage()
}

// GridTable is a multi-column table built on [ColumnPercentage].
// It stacks a header [RowPercentage] and data [RowPercentage]
// children vertically, each distributing width according to
// constraint-driven column percentages.
//
// Column widths are specified by split URIs — each URI points to
// an int64 attribute holding a column's percentage of the total
// width. Exactly one column may be designated as "flex" (absorbs
// the remainder). The grid reads split values every frame, so
// column proportions update automatically when the underlying
// attributes change.
//
// Font size is driven by an attribute URI (typically from a
// [CornerRadialChooser]). All labels are [DynamicLabel] instances
// that react to font size changes via the constraint network.
//
// Use [NewGridFrame] for a complete grid with Divider markers and
// a NeuBox wrapper. Use NewGridTable directly only when embedding
// the table without the frame chrome.
type GridTable struct {
	ColumnPercentage // structural embedding for Interactor + Parent

	Theme mancini.Theme

	nCols        int                      // number of columns
	flexIdx      int                      // index of the flex (-1) column, or -1 if none
	splitAttrs   []*attr.Attribute[int64] // constraint attrs for each non-flex column
	splitMap     []int                    // splitMap[col] → index into splitAttrs, or -1 for flex
	fontSizeURI  string                   // URI of the font size source attribute

	rows       []GridRow
	rowWidgets []*RowPercentage // parallel to rows; holds the widget for each data row
	headerLabs []*DynamicLabel
	dataLabs   [][]*DynamicLabel
	headerRow  *RowPercentage  // direct reference to header row for virtual draw path

	fontSizeAttr *attr.Attribute[int64]
	lastFontSize int64

	selectedMsgNum int64            // msgNum of primary selected row (-1 = none)
	selectedSet    map[uint32]bool  // set of selected msgNums (always includes selectedMsgNum when ≥0)

	SelectedAttr          *attr.Attribute[int64]   // msgNum of primary selected row (-1 = none)
	SelectedSetAttr       *attr.Attribute[[]int64] // collection of all selected msgNums (sentinel: [MaxInt64] if >256)
	SelectedSetCountAttr  *attr.Attribute[int64]   // true count of selected rows (regardless of sentinel)
	SelectedSetPagesAttr  *attr.Attribute[int64]   // ceil(count/512): pages needed for full IPC transfer

	// Virtual scroll fields — active when rowFactory != nil.
	TotalRows    int64          // total collection size; set via SetTotalRows
	scrollOffset int64          // index of row shown in slot 0
	visibleCount int64          // slots that fully fit; recomputed in Draw
	rowFactory   func() GridRow // creates new slot data objects; nil = legacy AddRow mode
	poolEpoch    int            // incremented on rebuild; ensures unique widget URIs
	slotPool     []GridRow      // fixed pool of virtual slot data objects
	slotWidgets  []*RowPercentage
	slotLabels   [][]*DynamicLabel

	FirstVisibleMsgNumAttr *attr.Attribute[int64]
	LastVisibleMsgNumAttr  *attr.Attribute[int64]
	VisibleRowCountAttr    *attr.Attribute[int64]

	// PadXAttr and PadYAttr drive the interior padding (in pixels) of every
	// label cell. Other parts of the constraint network may swap these to
	// constraint outputs to animate or theme label padding.
	PadXAttr *attr.Attribute[int64]
	PadYAttr *attr.Attribute[int64]
}

// gridURI returns an attribute URI for a grid-published value.
func gridURI(gridName, field string) string {
	return mancini.LayoutURI(gridName, mancini.DataTypeInt64, mancini.LayoutProp("grid/"+field))
}

// buildSplitMap builds the splitMap array: splitMap[col] is the index
// into the split attributes slice, or -1 for the flex column.
func buildSplitMap(percents []float64) []int {
	splitMap := make([]int, len(percents))
	splitIdx := 0
	for i, p := range percents {
		if p < 0 {
			splitMap[i] = -1
		} else {
			splitMap[i] = splitIdx
			splitIdx++
		}
	}
	return splitMap
}

// NewGridTable creates a grid table with the given column headers and
// constraint-driven column percentages. Each entry in percents is either
// a positive float64 (fixed percentage) paired with a URI in splitURIs,
// or -1 (flex column, absorbs remainder — no URI needed).
//
// splitURIs contains one URI string per non-flex column, in order.
// Each URI must point to an int64 attribute holding the column's
// percentage of the total width.
//
// fontSizeURI should point to an int64 attribute. All labels bind their
// font size to this URI.
//
// For a grid with Divider column-resize markers and a NeuBox wrapper,
// use [NewGridFrame] instead.
func NewGridTable(myName, parent string, pal mancini.Palette,
	theme mancini.Theme, fontSizeURI string,
	headers []string, percents []float64, splitURIs []string) *GridTable {

	if myName == "" {
		myName = mancini.DefaultName("grid")
	}

	lh := mancini.NewLayoutAttributesBase(myName, parent)
	lh.Width = attr.ValueI64(
		mancini.LayoutURI(myName, mancini.DataTypeInt64, mancini.LayoutWidth), 0)
	lh.Height = attr.ValueI64(
		mancini.LayoutURI(myName, mancini.DataTypeInt64, mancini.LayoutHeight), 0)
	lh.InitBounds(myName)

	nCols := len(percents)
	flexIdx := -1
	splitMap := buildSplitMap(percents)
	for i, p := range percents {
		if p < 0 {
			flexIdx = i
		}
	}

	// Constraint attrs bound to the split URIs.
	splitAttrs := make([]*attr.Attribute[int64], len(splitURIs))
	for i, uri := range splitURIs {
		prog := mancini.BindStrings(mancini.ProgIdentityI64, "_source_", uri)
		attrURI := mancini.LayoutURI(myName, mancini.DataTypeInt64,
			mancini.LayoutProp(fmt.Sprintf("split/%d", i)))
		splitAttrs[i] = attr.ConstraintI64(attrURI, prog)
	}

	fsProg := mancini.BindStrings(mancini.ProgIdentityI64, "_source_", fontSizeURI)
	fsAttrURI := mancini.LayoutURI(myName, mancini.DataTypeInt64,
		mancini.LayoutProp("fontSize"))
	fontSizeAttr := attr.ConstraintI64(fsAttrURI, fsProg)
	initialFS := fontSizeAttr.Get()
	if initialFS <= 0 {
		initialFS = 14
	}

	padXURI := mancini.LayoutURI(myName, mancini.DataTypeInt64, mancini.LayoutProp("padX"))
	padYURI := mancini.LayoutURI(myName, mancini.DataTypeInt64, mancini.LayoutProp("padY"))

	gt := &GridTable{
		Theme:                theme,
		nCols:                nCols,
		flexIdx:              flexIdx,
		splitAttrs:           splitAttrs,
		splitMap:             splitMap,
		fontSizeURI:          fontSizeURI,
		fontSizeAttr:         fontSizeAttr,
		lastFontSize:         initialFS,
		selectedMsgNum:       -1,
		selectedSet:          make(map[uint32]bool),
		SelectedAttr:         attr.ValueI64(attr.ShepherdURI("int64", myName+"/selected"), -1),
		SelectedSetAttr:      attr.ValueCollI64(attr.ShepherdURI("int64", myName+"/selectedSet"), nil),
		SelectedSetCountAttr: attr.ValueI64(attr.ShepherdURI("int64", myName+"/selectedSetCount"), 0),
		SelectedSetPagesAttr: attr.ValueI64(attr.ShepherdURI("int64", myName+"/selectedSetPages"), 0),
		FirstVisibleMsgNumAttr: attr.ValueI64(gridURI(myName, "firstVisible"), -1),
		LastVisibleMsgNumAttr:  attr.ValueI64(gridURI(myName, "lastVisible"), -1),
		VisibleRowCountAttr:    attr.ValueI64(gridURI(myName, "visibleCount"), 0),
		PadXAttr:               attr.ValueI64(padXURI, 5),
		PadYAttr:               attr.ValueI64(padYURI, 0),
	}
	gt.ColumnPercentage.Pal = pal
	gt.Interactor.Initialize(gt, lh)
	gt.Parent.Initialize(true, &gt.Interactor)

	initPcts := gt.currentPercents()
	hdrRowName := myName + "_hdr"
	hdrRow := NewRowPercentage(hdrRowName, myName, pal, 0, 0, initPcts)
	hdrRow.ClipChildren = true
	gt.headerRow = hdrRow

	gt.headerLabs = make([]*DynamicLabel, len(headers))
	for i, hdr := range headers {
		name := fmt.Sprintf("%s_hdr_%d", myName, i)
		lab := NewDynamicLabelBold(name, hdrRowName, theme, hdr, fontSizeURI)
		lab.SetPaddingAttrs(padXURI, padYURI)
		gt.headerLabs[i] = lab
	}

	return gt
}

// currentPercents reads the split constraint attrs and computes the
// current column percentages. The flex column gets the remainder.
func (gt *GridTable) currentPercents() []float64 {
	pcts := make([]float64, gt.nCols)
	sum := 0.0
	for i := 0; i < gt.nCols; i++ {
		si := gt.splitMap[i]
		if si < 0 {
			continue
		}
		if si < len(gt.splitAttrs) {
			v := float64(gt.splitAttrs[si].Get())
			if v < 0 {
				v = 0
			}
			pcts[i] = v
			sum += v
		}
	}
	if gt.flexIdx >= 0 {
		rem := 100.0 - sum
		if rem < 0 {
			rem = 0
		}
		pcts[gt.flexIdx] = rem
	}
	return pcts
}

// AddRow appends a data row to the grid.
// Returns a func() that calls FullDamage on this row's DynamicLabel leaves,
// propagating through the constraint chain to trigger a redraw. Use it as the
// row's OnLoaded callback. (RowPercentage.FullDamage is a no-op — it is a
// parent with a constraint-based DamageRect, not a settable leaf.)
//
// Labels are pre-positioned at the expected row Y so that FullDamage()
// immediately produces a damage rect in the correct grid area. Without
// this, labels sit at Y=0 and PushDamageClip clips draws to the wrong region.
// RowPercentage.Draw refines the exact column X positions on the next pass.
func (gt *GridTable) AddRow(row GridRow) func() {
	myName := gt.GetLayout().Name()
	idx := len(gt.rows)
	gt.rows = append(gt.rows, row)

	pcts := gt.currentPercents()
	rowName := fmt.Sprintf("%s_r%d", myName, idx)
	rp := NewRowPercentage(rowName, myName, gt.Pal, 0, 0, pcts)
	rp.ClipChildren = true
	rp.grid = gt
	rp.row = row
	gt.rowWidgets = append(gt.rowWidgets, rp)

	cols := []string{row.Sender(), row.Subject(), row.Date()}
	labels := make([]*DynamicLabel, len(pcts))
	for i := range pcts {
		name := fmt.Sprintf("%s_r%d_c%d", myName, idx, i)
		text := ""
		if i < len(cols) {
			text = cols[i]
		}
		lab := NewDynamicLabel(name, rowName, gt.Theme, text, gt.fontSizeURI)
		lab.SetPaddingAttrs(gt.PadXAttr.URI(), gt.PadYAttr.URI())
		labels[i] = lab
	}
	gt.dataLabs = append(gt.dataLabs, labels)

	// Pre-position labels at the expected row Y so FullDamage() emits a
	// damage rect in the correct grid area. The grid must have been laid
	// out at least once (X/Y/W set by ColumnPercentage.Draw) for this to
	// be meaningful; if W=0 the rect is empty and will be corrected on the
	// next unconditional full draw.
	gridY := gt.GetLayout().Y.Get()
	gridX := gt.GetLayout().X.Get()
	gridW := gt.GetLayout().Width.Get()
	rh := gt.rowHeight()
	rowY := gridY + (rh+2+1) + int64(idx)*rh // after header (rh+2) + separator (1)
	for _, lab := range labels {
		if lab == nil {
			continue
		}
		labLH := lab.GetLayout()
		labLH.Y.Set(rowY)
		labLH.X.Set(gridX)
		if !labLH.Width.IsConstraint() {
			labLH.Width.Set(gridW)
		}
		if !labLH.Height.IsConstraint() {
			labLH.Height.Set(rh) // must set height so FullDamage emits non-empty rect
		}
		lab.FullDamage()
	}

	return func() {
		for _, lab := range labels {
			if lab == nil {
				continue
			}
			labLH := lab.GetLayout()
			if !labLH.Height.IsConstraint() && labLH.Height.Get() == 0 {
				labLH.Height.Set(rh)
			}
			lab.FullDamage()
		}
	}
}

// --- Virtual scroll API ---

// SetTotalRows sets the total collection size used for scroll clamping.
func (gt *GridTable) SetTotalRows(n int64) {
	gt.TotalRows = n
	gt.clampScroll()
}

// SetRowFactory sets the factory used to create virtual slot data objects
// and switches the grid into virtual scroll mode. A pool rebuild is deferred
// to the next Draw call when visibleCount is known.
func (gt *GridTable) SetRowFactory(f func() GridRow) {
	gt.rowFactory = f
	gt.visibleCount = 0 // force rebuild on next Draw
}

// ScrollBy moves the scroll offset by delta rows (clamped). Updates all
// slot msgNums and publishes the visibility attrs.
func (gt *GridTable) ScrollBy(delta int64) {
	gt.scrollOffset += delta
	gt.clampScroll()
	gt.applyScrollToSlots()
	gt.publishScrollAttrs()
	gt.DamageAll()
}

// MoveSelection moves the primary selection by delta rows (clamped to [0, TotalRows-1]).
// If nothing is currently selected, the first visible row is used as the starting point.
// Scrolls to keep the new selection within the visible window.
func (gt *GridTable) MoveSelection(delta int64) {
	if gt.TotalRows == 0 {
		return
	}
	cur := gt.selectedMsgNum
	if cur < 0 {
		cur = gt.scrollOffset
	}
	next := cur + delta
	if next < 0 {
		next = 0
	}
	if next >= gt.TotalRows {
		next = gt.TotalRows - 1
	}
	msgNum := uint32(next)
	gt.selectedMsgNum = next
	gt.selectedSet = map[uint32]bool{msgNum: true}
	gt.SelectedAttr.Set(next)
	gt.publishSelectedSet()

	// Scroll to keep new selection visible.
	if next < gt.scrollOffset {
		gt.scrollOffset = next
	} else if gt.visibleCount > 0 && next >= gt.scrollOffset+gt.visibleCount {
		gt.scrollOffset = next - gt.visibleCount + 1
	}
	gt.clampScroll()
	gt.applyScrollToSlots()
	gt.publishScrollAttrs()
	gt.DamageAll()
}

func (gt *GridTable) clampScroll() {
	max := gt.TotalRows - gt.visibleCount
	if max < 0 {
		max = 0
	}
	if gt.scrollOffset > max {
		gt.scrollOffset = max
	}
	if gt.scrollOffset < 0 {
		gt.scrollOffset = 0
	}
}

// applyScrollToSlots updates the msgNum on each slot data object.
func (gt *GridTable) applyScrollToSlots() {
	for i, row := range gt.slotPool {
		if setter, ok := row.(interface{ SetMsgNum(uint32) }); ok {
			setter.SetMsgNum(uint32(gt.scrollOffset + int64(i)))
		}
	}
}

// buildSlotPool ensures the pool has at least count slot widgets, growing
// it (and creating new attribute URIs) only when the requested count exceeds
// current capacity. Each grow uses a fresh poolEpoch so freshly-allocated
// slots have unique attribute names; existing slots are kept as-is.
//
// Why grow-only: the previous design rebuilt the pool from scratch on every
// visibleCount change and left the old pool's layout attributes (X/Y/W/H
// per slot per column) orphaned in the kernel attribute table. With repeated
// resize/relayout cycles that table overflowed (`AttrCreate failed: cannot
// allocate memory`), eventually killing the shepherd.
//
// The Draw loop only ever indexes the first `visibleCount` slots, so an
// oversized pool is harmless — wasted RAM, but stable.
func (gt *GridTable) buildSlotPool(count int64) {
	existing := int64(len(gt.slotPool))
	if existing >= count {
		// Pool already big enough — reuse. Re-apply scroll so any newly
		// promoted-into-view slots see the current msgNum mapping.
		gt.applyScrollToSlots()
		if gt.selectedMsgNum >= 0 {
			gt.SelectedAttr.Set(gt.selectedMsgNum)
		}
		return
	}

	gt.poolEpoch++
	ep := gt.poolEpoch
	myName := gt.GetLayout().Name()
	pcts := gt.currentPercents()

	// Grow the underlying slices in place; preserve the existing slots.
	newPool := make([]GridRow, count)
	newWidgets := make([]*RowPercentage, count)
	newLabels := make([][]*DynamicLabel, count)
	copy(newPool, gt.slotPool)
	copy(newWidgets, gt.slotWidgets)
	copy(newLabels, gt.slotLabels)
	gt.slotPool = newPool
	gt.slotWidgets = newWidgets
	gt.slotLabels = newLabels

	// Allocate only the new slots [existing, count).
	for i := existing; i < count; i++ {
		row := gt.rowFactory()
		gt.slotPool[i] = row

		rowName := fmt.Sprintf("%s_s%d_%d", myName, ep, i)
		rp := NewRowPercentage(rowName, myName, gt.Pal, 0, 0, pcts)
		rp.ClipChildren = true
		rp.grid = gt
		rp.row = row
		gt.slotWidgets[i] = rp

		labels := make([]*DynamicLabel, gt.nCols)
		for j := 0; j < gt.nCols; j++ {
			labName := fmt.Sprintf("%s_s%d_%d_c%d", myName, ep, i, j)
			lab := NewDynamicLabel(labName, rowName, gt.Theme, "", gt.fontSizeURI)
			lab.SetPaddingAttrs(gt.PadXAttr.URI(), gt.PadYAttr.URI())
			labels[j] = lab
		}
		gt.slotLabels[i] = labels
	}

	gt.applyScrollToSlots()

	// Re-apply selection state for the new pool.
	if gt.selectedMsgNum >= 0 {
		gt.SelectedAttr.Set(gt.selectedMsgNum)
	}
}

// publishScrollAttrs writes the three visibility attrs from the current state.
func (gt *GridTable) publishScrollAttrs() {
	if gt.visibleCount == 0 || gt.TotalRows == 0 {
		gt.FirstVisibleMsgNumAttr.Set(-1)
		gt.LastVisibleMsgNumAttr.Set(-1)
		gt.VisibleRowCountAttr.Set(0)
		return
	}
	first := gt.scrollOffset
	last := gt.scrollOffset + gt.visibleCount - 1
	if last >= gt.TotalRows {
		last = gt.TotalRows - 1
	}
	gt.FirstVisibleMsgNumAttr.Set(first)
	gt.LastVisibleMsgNumAttr.Set(last)
	gt.VisibleRowCountAttr.Set(gt.visibleCount)
}

// headerAreaH returns the pixel height consumed by the header row + separator.
func (gt *GridTable) headerAreaH(rh int64) int64 { return rh + 3 }

// computeVisibleCount returns how many full data rows fit below the header.
func (gt *GridTable) computeVisibleCount(totalH, rh int64) int64 {
	content := totalH - gt.headerAreaH(rh)
	if content < rh {
		return 0
	}
	return content / rh
}

// --- End virtual scroll API ---

// DamageAll calls FullDamage on every DynamicLabel leaf in the grid (header
// and all data rows). Use this after dynamically adding rows to trigger a
// full grid repaint — parent interactors like RowPercentage have constraint-
// based DamageRect, so their FullDamage is a no-op; labels are the leaves.
func (gt *GridTable) DamageAll() {
	for _, lab := range gt.headerLabs {
		if lab != nil {
			lab.FullDamage()
		}
	}
	for _, row := range gt.dataLabs {
		for _, lab := range row {
			if lab != nil {
				lab.FullDamage()
			}
		}
	}
	for _, row := range gt.slotLabels {
		for _, lab := range row {
			if lab != nil {
				lab.FullDamage()
			}
		}
	}
}

// setSelected handles a click on row. Normal click sets a new primary selection
// (clears all other selected rows). Shift+click toggles row in/out of the set
// without changing the primary.
func (gt *GridTable) setSelected(row GridRow, ev *mancini.InputEvent) {
	msgNum := row.MsgNum()
	if !hid.Shift(int64(ev.Mods)) {
		// Normal click: set new primary, clear set.
		gt.selectedMsgNum = int64(msgNum)
		gt.selectedSet = map[uint32]bool{msgNum: true}
		gt.SelectedAttr.Set(gt.selectedMsgNum)
		gt.publishSelectedSet()

		if gt.rowFactory != nil {
			// Virtual mode: SelectionState recomputed in Draw.
			gt.DamageAll()
		} else {
			// Legacy mode: update SelectionState imperatively.
			for i, rp := range gt.rowWidgets {
				newState := 0
				if i < len(gt.rows) && gt.rows[i] == row {
					newState = 1
				}
				if rp.SelectionState != newState {
					rp.SelectionState = newState
					gt.damageRow(i)
				}
			}
		}
		return
	}

	// Shift+click: toggle row in/out of selectedSet.
	// Primary (selectedMsgNum) can never be removed from the set.
	if gt.selectedSet == nil {
		gt.selectedSet = make(map[uint32]bool)
		if gt.selectedMsgNum >= 0 {
			gt.selectedSet[uint32(gt.selectedMsgNum)] = true
		}
	}
	if gt.selectedSet[msgNum] && int64(msgNum) != gt.selectedMsgNum {
		delete(gt.selectedSet, msgNum)
	} else if !gt.selectedSet[msgNum] {
		gt.selectedSet[msgNum] = true
	}
	gt.publishSelectedSet()

	if gt.rowFactory != nil {
		gt.DamageAll()
	} else {
		for i, r := range gt.rows {
			if r == row && i < len(gt.rowWidgets) {
				state := 2
				if int64(r.MsgNum()) == gt.selectedMsgNum {
					state = 1
				}
				gt.rowWidgets[i].SelectionState = state
				gt.damageRow(i)
				break
			}
		}
	}
}

// publishSelectedSet writes the current selectedSet to the three set-export attrs.
// Sentinel rule: if count > 256, the collection contains a single math.MaxInt64
// element. SelectedSetCountAttr always reflects the true count.
func (gt *GridTable) publishSelectedSet() {
	count := len(gt.selectedSet)
	gt.SelectedSetCountAttr.Set(int64(count))

	pages := int64(0)
	if count > 0 {
		pages = (int64(count) + 511) / 512
	}
	gt.SelectedSetPagesAttr.Set(pages)

	var msgNums []int64
	switch {
	case count == 0:
		msgNums = nil
	case count > 256:
		msgNums = []int64{math.MaxInt64}
	default:
		msgNums = make([]int64, 0, count)
		for msgNum := range gt.selectedSet {
			msgNums = append(msgNums, int64(msgNum))
		}
	}
	gt.SelectedSetAttr.Set(msgNums)
}

// damageRow calls FullDamage on the labels of data row idx.
func (gt *GridTable) damageRow(idx int) {
	if idx < 0 || idx >= len(gt.dataLabs) {
		return
	}
	for _, lab := range gt.dataLabs[idx] {
		if lab != nil {
			lab.FullDamage()
		}
	}
}

// RefreshSelected re-publishes the selected msgNum to SelectedAttr.
func (gt *GridTable) RefreshSelected() {
	if gt.selectedMsgNum >= 0 {
		gt.SelectedAttr.Set(gt.selectedMsgNum)
	}
	gt.publishSelectedSet()
}

// rowHeight returns the current row height based on the font size attribute.
func (gt *GridTable) rowHeight() int64 {
	fs := gt.fontSizeAttr.Get()
	if fs <= 0 {
		fs = 14
	}
	gt.lastFontSize = fs
	return fs + 6
}

// Draw stacks the header row and data rows vertically with fixed row
// heights, drawing alternating backgrounds and a separator line.
// Column percentages are read from the split constraint attrs each frame.
func (gt *GridTable) Draw(self mancini.Interactor, x, y, w, h int64, damage image.Rectangle) {
	dc := self.DC()
	if dc == nil {
		return
	}

	dc.SetColor(gt.Pal.Surface())
	dc.FillRectangle(float64(x), float64(y), float64(w), float64(h))

	rh := gt.rowHeight()

	// --- Virtual scroll path ---
	if gt.rowFactory != nil {
		newVC := gt.computeVisibleCount(h, rh)
		if newVC != gt.visibleCount {
			gt.visibleCount = newVC
			gt.clampScroll()
			gt.buildSlotPool(newVC)
		}

		pcts := gt.currentPercents()
		curY := y
		hdrH := rh + 2

		// Draw header.
		if gt.headerRow != nil {
			dc.SetColor(gt.Pal.SurfaceTint())
			dc.FillRectangle(float64(x), float64(curY), float64(w), float64(hdrH))
			gt.headerRow.Percents = pcts
			hdrLH := gt.headerRow.GetLayout()
			hdrLH.X.Set(x)
			hdrLH.Y.Set(curY)
			if !hdrLH.Width.IsConstraint() {
				hdrLH.Width.Set(w)
			}
			if !hdrLH.Height.IsConstraint() {
				hdrLH.Height.Set(hdrH)
			}
			gt.headerRow.SetDC(dc)
			gt.headerRow.Draw(gt.headerRow, x, curY, w, hdrH, damage)
			curY += hdrH
			dc.SetColor(gt.Pal.Text())
			dc.FillRectangle(float64(x), float64(curY), float64(w), 1)
			curY++
		}

		// Draw data slots.
		drawCount := gt.visibleCount
		if avail := gt.TotalRows - gt.scrollOffset; avail < drawCount {
			drawCount = avail
		}
		if drawCount < 0 {
			drawCount = 0
		}
		for i := int64(0); i < drawCount; i++ {
			rp := gt.slotWidgets[i]
			row := gt.slotPool[i]

			// Sync label text.
			cols := [3]string{row.Sender(), row.Subject(), row.Date()}
			for j, lab := range gt.slotLabels[i] {
				if lab != nil && j < 3 {
					lab.Text = cols[j]
				}
			}

			// Alternating row background.
			if i%2 == 1 {
				bg := gt.Pal.SurfaceTint()
				bg.A = 80
				dc.SetColor(bg)
				dc.FillRectangle(float64(x), float64(curY), float64(w), float64(rh))
			}

			// Selection state.
			msgNum := uint32(gt.scrollOffset + i)
			switch {
			case gt.selectedMsgNum >= 0 && uint32(gt.selectedMsgNum) == msgNum:
				rp.SelectionState = 1
			case gt.selectedSet[msgNum]:
				rp.SelectionState = 2
			default:
				rp.SelectionState = 0
			}

			rp.Percents = pcts
			rpLH := rp.GetLayout()
			rpLH.X.Set(x)
			rpLH.Y.Set(curY)
			if !rpLH.Width.IsConstraint() {
				rpLH.Width.Set(w)
			}
			if !rpLH.Height.IsConstraint() {
				rpLH.Height.Set(rh)
			}
			rp.SetDC(dc)
			rp.Draw(rp, x, curY, w, rh, damage)
			curY += rh
		}

		gt.publishScrollAttrs()
		return
	}
	// --- End virtual scroll path ---

	children := gt.GetChildren()
	if len(children) == 0 {
		return
	}

	pcts := gt.currentPercents()

	curY := y
	headerH := rh + 2

	rowIdx := 0
	for _, child := range children {
		if curY+rh > y+h {
			break
		}

		isHeader := rowIdx == 0
		rowH := rh
		if isHeader {
			rowH = headerH
		}

		if isHeader {
			dc.SetColor(gt.Pal.SurfaceTint())
			dc.FillRectangle(float64(x), float64(curY), float64(w), float64(rowH))
		} else if (rowIdx-1)%2 == 1 {
			bg := gt.Pal.SurfaceTint()
			bg.A = 80
			dc.SetColor(bg)
			dc.FillRectangle(float64(x), float64(curY), float64(w), float64(rowH))
		}

		if rp, ok := child.(*RowPercentage); ok {
			rp.Percents = pcts
		}

		// Sync label text from live row data so async-loaded rows (e.g. MailRow)
		// display their data as soon as it arrives without a separate update step.
		if !isHeader {
			dataIdx := rowIdx - 1
			if dataIdx < len(gt.rows) && dataIdx < len(gt.dataLabs) {
				row := gt.rows[dataIdx]
				cols := [3]string{row.Sender(), row.Subject(), row.Date()}
				for j, lab := range gt.dataLabs[dataIdx] {
					if lab != nil && j < 3 {
						lab.Text = cols[j]
					}
				}
			}
		}

		childL, hasLayout := child.(mancini.Layouter)
		if hasLayout {
			clh := childL.GetLayout()
			if clh != nil {
				clh.X.Set(x)
				clh.Y.Set(curY)
				if !clh.Width.IsConstraint() {
					clh.Width.Set(w)
				}
				if !clh.Height.IsConstraint() {
					clh.Height.Set(rowH)
				}
			}
		}
		if cs, ok := child.(interface{ SetDC(mancini.DrawContext) }); ok {
			cs.SetDC(dc)
		}
		if d, ok := child.(mancini.NewDrawer); ok {
			d.Draw(child, x, curY, w, rowH, damage)
		}

		curY += rowH

		if isHeader {
			dc.SetColor(gt.Pal.Text())
			dc.FillRectangle(float64(x), float64(curY), float64(w), 1)
			curY++
		}
		rowIdx++
	}
}
