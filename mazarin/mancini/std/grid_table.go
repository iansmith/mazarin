package std

import (
	"fmt"
	"image"

	"mazzy/mazarin/attr"
	"mazzy/mazarin/mancini"
)

// GridRow is the interface that each row's data must satisfy.
// The grid table calls these methods to populate column labels.
type GridRow interface {
	Sender() string
	Subject() string
	Date() string
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
type GridTable struct {
	ColumnPercentage // structural embedding for Interactor + Parent

	Theme mancini.Theme

	nCols          int                      // number of columns
	flexIdx        int                      // index of the flex (-1) column, or -1 if none
	splitAttrs     []*attr.Attribute[int64] // constraint attrs for each non-flex column
	splitSrcAttrs  []*attr.Attribute[int64] // source value attrs (writable) for divider drag
	splitMap       []int                    // splitMap[col] → index into splitAttrs, or -1 for flex
	fontSizeURI    string                   // URI of the font size source attribute

	rows       []GridRow        // data backing
	headerLabs []*DynamicLabel
	dataLabs   [][]*DynamicLabel

	// dividers are column-boundary indicators drawn after all rows.
	// One divider per non-flex column boundary (between adjacent columns).
	dividers []*Divider

	// fontSizeAttr tracks font size for row height calculation.
	fontSizeAttr *attr.Attribute[int64]
	lastFontSize int64
}

// gridURI returns an attribute URI for a grid-published value.
// Uses the mancini layout URI scheme so the kernel accepts it.
func gridURI(gridName, field string) string {
	return mancini.LayoutURI(gridName, mancini.DataTypeInt64, mancini.LayoutProp("grid/"+field))
}

// NewGridTable creates a grid table with the given column headers and
// constraint-driven column percentages. Each entry in percents is
// either a positive float64 (fixed percentage) paired with a URI in
// splitURIs, or -1 (flex column, absorbs remainder — no URI needed).
//
// splitURIs contains one URI string per non-flex column, in order.
// Each URI must point to an int64 attribute holding the column's
// percentage of the total width. The grid binds constraint attributes
// to these URIs so column proportions update automatically.
//
// splitSrcAttrs are the source value attributes corresponding to
// splitURIs. These are passed to Divider children so they can write
// updated percentages during drag operations. The grid's own
// splitAttrs are constraint (read-only) copies.
//
// fontSizeURI should point to an int64 attribute (e.g., from a
// CornerRadialChooser's ValueURI()). All labels bind their font
// size to this URI.
//
// Example for [Sender 35% | Subject flex | Date 10%]:
//
//	NewGridTable(name, parent, pal, theme, fontSizeURI,
//	    []string{"Sender", "Subject", "Date"},
//	    []float64{35, -1, 10},
//	    []string{senderPctURI, datePctURI},
//	    []*attr.Attribute[int64]{senderPct, datePct})
func NewGridTable(myName, parent string, pal mancini.Palette,
	theme mancini.Theme, fontSizeURI string,
	headers []string, percents []float64, splitURIs []string,
	splitSrcAttrs []*attr.Attribute[int64]) *GridTable {

	if myName == "" {
		myName = mancini.DefaultName("grid")
	}

	lh := mancini.NewLayoutAttributesBase(myName, parent)
	lh.Width = attr.ValueI64(
		mancini.LayoutURI(myName, mancini.DataTypeInt64, mancini.LayoutWidth), 0)
	lh.Height = attr.ValueI64(
		mancini.LayoutURI(myName, mancini.DataTypeInt64, mancini.LayoutHeight), 0)
	lh.InitBounds(myName)

	// Build split map: for each column, record its index into splitAttrs
	// or -1 for the flex column.
	nCols := len(percents)
	flexIdx := -1
	splitMap := make([]int, nCols)
	splitIdx := 0
	for i, p := range percents {
		if p < 0 {
			flexIdx = i
			splitMap[i] = -1
		} else {
			splitMap[i] = splitIdx
			splitIdx++
		}
	}

	// Create constraint attrs bound to the split URIs.
	splitAttrs := make([]*attr.Attribute[int64], len(splitURIs))
	for i, uri := range splitURIs {
		prog := mancini.BindStrings(mancini.ProgIdentityI64, "_source_", uri)
		attrURI := mancini.LayoutURI(myName, mancini.DataTypeInt64,
			mancini.LayoutProp(fmt.Sprintf("split/%d", i)))
		splitAttrs[i] = attr.ConstraintI64(attrURI, prog)
	}

	// Grid's own font size binding for row height calculation.
	fsProg := mancini.BindStrings(mancini.ProgIdentityI64,
		"_source_", fontSizeURI)
	fsAttrURI := mancini.LayoutURI(myName, mancini.DataTypeInt64,
		mancini.LayoutProp("fontSize"))
	fontSizeAttr := attr.ConstraintI64(fsAttrURI, fsProg)
	initialFS := fontSizeAttr.Get()
	if initialFS <= 0 {
		initialFS = 14
	}

	gt := &GridTable{
		Theme:         theme,
		nCols:         nCols,
		flexIdx:       flexIdx,
		splitAttrs:    splitAttrs,
		splitSrcAttrs: splitSrcAttrs,
		splitMap:      splitMap,
		fontSizeURI:   fontSizeURI,
		fontSizeAttr:  fontSizeAttr,
		lastFontSize:  initialFS,
	}
	gt.ColumnPercentage.Pal = pal
	gt.Interactor.Initialize(gt, lh)
	gt.Parent.Initialize(true, &gt.Interactor)

	// Create header RowPercentage with initial percentages.
	initPcts := gt.currentPercents()
	hdrRowName := myName + "_hdr"
	hdrRow := NewRowPercentage(hdrRowName, myName, pal, 0, 0, initPcts)
	hdrRow.ClipChildren = true
	_ = hdrRow

	gt.headerLabs = make([]*DynamicLabel, len(headers))
	for i, hdr := range headers {
		name := fmt.Sprintf("%s_hdr_%d", myName, i)
		lab := NewDynamicLabelBold(name, hdrRowName, theme, hdr, fontSizeURI)
		gt.headerLabs[i] = lab
	}

	// Create dividers at each non-flex column boundary.
	// A boundary exists between column i and column i+1 for each
	// non-flex column (the divider controls column i's percentage).
	// Dividers write to the source value attrs (not the constraint copies).
	for i := 0; i < nCols-1; i++ {
		si := splitMap[i]
		if si < 0 {
			continue // flex column — no draggable boundary on its right edge
		}
		if si >= len(splitSrcAttrs) {
			continue
		}
		divName := fmt.Sprintf("%s_div%d", myName, i)
		div := NewDivider(divName, myName, pal, i, splitSrcAttrs[si])
		gt.dividers = append(gt.dividers, div)
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
			continue // flex — filled in below
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

// AddRow appends a data row to the grid and creates a RowPercentage
// child with DynamicLabel interactors for each column.
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
// Column percentages are read from the split constraint attrs each
// frame, so proportions update automatically.
func (gt *GridTable) Draw(self mancini.Interactor, x, y, w, h int64, damage image.Rectangle) {
	dc := self.DC()
	if dc == nil {
		return
	}

	// Clear background.
	dc.SetColor(gt.Pal.Surface())
	dc.FillRectangle(float64(x), float64(y), float64(w), float64(h))

	children := gt.GetChildren()
	if len(children) == 0 {
		return
	}

	// Read current column percentages from constraint attrs.
	pcts := gt.currentPercents()

	rh := gt.rowHeight()
	curY := y
	headerH := rh + 2

	rowIdx := 0
	for _, child := range children {
		// Skip dividers — they are drawn after all rows.
		if _, isDivider := child.(*Divider); isDivider {
			continue
		}

		if curY+rh > y+h {
			break
		}

		isHeader := rowIdx == 0
		rowH := rh
		if isHeader {
			rowH = headerH
		}

		// Background: header gets SurfaceTint, data rows alternate.
		if isHeader {
			dc.SetColor(gt.Pal.SurfaceTint())
			dc.FillRectangle(float64(x), float64(curY), float64(w), float64(rowH))
		} else if (rowIdx-1)%2 == 1 {
			bg := gt.Pal.SurfaceTint()
			bg.A = 80
			dc.SetColor(bg)
			dc.FillRectangle(float64(x), float64(curY), float64(w), float64(rowH))
		}

		// Update the RowPercentage child's column splits.
		if rp, ok := child.(*RowPercentage); ok {
			rp.Percents = pcts
		}

		// Position and draw the RowPercentage child.
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

		// Separator line after header.
		if isHeader {
			dc.SetColor(gt.Pal.Text())
			dc.FillRectangle(float64(x), float64(curY), float64(w), 1)
			curY++
		}
		rowIdx++
	}

	// Draw dividers after all rows, with expanded clip for overhang.
	gt.drawDividers(dc, x, y, w, h, pcts, damage)
}

// drawDividers positions and draws each Divider child. The divider's
// X is set to the column boundary pixel, and its bounds extend above
// and below the grid by Overhang pixels. A Push/Pop pair temporarily
// expands the clip rectangle to allow the overhang drawing.
func (gt *GridTable) drawDividers(dc mancini.DrawContext, x, y, w, h int64,
	pcts []float64, damage image.Rectangle) {

	if len(gt.dividers) == 0 {
		return
	}

	for _, div := range gt.dividers {
		// Compute the pixel X of this column boundary.
		splitX := x
		for col := 0; col <= div.ColIndex; col++ {
			splitX += int64(float64(w) * pcts[col] / 100.0)
		}

		// Cache parent width for drag percentage math.
		div.parentWidth = w

		// Position the divider: centered on splitX, overhang above and below.
		oh := div.Overhang
		hw := div.TriHalfW
		divX := splitX - hw
		divY := y - oh
		divW := hw * 2
		divH := h + oh*2

		lh := div.GetLayout()
		lh.X.Set(divX)
		lh.Y.Set(divY)
		lh.Width.Set(divW)
		lh.Height.Set(divH)

		div.SetDC(dc)

		// Dividers are decorative overlays drawn LAST, on top of
		// everything else — they live partly in the bezel area outside
		// the grid bounds. We must escape any clip established by
		// parents (MarginParent, row clips, etc.) since dc.Clip() only
		// INTERSECTS, never expands. So: Push state, ResetClip to drop
		// the parent clip, then Clip to the divider's own bounds so we
		// don't smear paint elsewhere. Pop restores the parent's clip.
		dc.Push()
		dc.ResetClip()
		dc.DrawRectangle(float64(divX), float64(divY), float64(divW), float64(divH))
		dc.Clip()

		div.Draw(div, divX, divY, divW, divH, damage)

		dc.Pop()
	}
}
