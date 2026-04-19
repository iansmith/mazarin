package std

import (
	"fmt"
	"image"

	"mazzy/mazarin/attr"
	"mazzy/mazarin/mancini"
	"mazzy/mazarin/mancini/impl"
)

// GridRow is the interface that each row's data must satisfy.
// The grid table calls these methods to populate column labels.
type GridRow interface {
	Sender() string
	Subject() string
	Date() string
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
		// Damage the full frame so the blit region covers the old divider
		// position in the bezel strips — not just the new position.
		gf.FullDamage()
		for _, lab := range grid.headerLabs {
			if lab != nil {
				lab.FullDamage()
			}
		}
		for _, row := range grid.dataLabs {
			for _, lab := range row {
				if lab != nil {
					lab.FullDamage()
				}
			}
		}
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
func (gf *GridFrame) AddRow(row GridRow) {
	gf.grid.AddRow(row)
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
	headerLabs []*DynamicLabel
	dataLabs   [][]*DynamicLabel

	fontSizeAttr *attr.Attribute[int64]
	lastFontSize int64

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
		Theme:        theme,
		nCols:        nCols,
		flexIdx:      flexIdx,
		splitAttrs:   splitAttrs,
		splitMap:     splitMap,
		fontSizeURI:  fontSizeURI,
		fontSizeAttr: fontSizeAttr,
		lastFontSize: initialFS,
		PadXAttr:     attr.ValueI64(padXURI, 5),
		PadYAttr:     attr.ValueI64(padYURI, 0),
	}
	gt.ColumnPercentage.Pal = pal
	gt.Interactor.Initialize(gt, lh)
	gt.Parent.Initialize(true, &gt.Interactor)

	initPcts := gt.currentPercents()
	hdrRowName := myName + "_hdr"
	hdrRow := NewRowPercentage(hdrRowName, myName, pal, 0, 0, initPcts)
	hdrRow.ClipChildren = true
	_ = hdrRow

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
func (gt *GridTable) AddRow(row GridRow) {
	myName := gt.GetLayout().Name()
	idx := len(gt.rows)
	gt.rows = append(gt.rows, row)

	pcts := gt.currentPercents()
	rowName := fmt.Sprintf("%s_r%d", myName, idx)
	rp := NewRowPercentage(rowName, myName, gt.Pal, 0, 0, pcts)
	rp.ClipChildren = true
	_ = rp

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

	children := gt.GetChildren()
	if len(children) == 0 {
		return
	}

	pcts := gt.currentPercents()

	rh := gt.rowHeight()
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
