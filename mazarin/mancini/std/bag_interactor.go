// BoxesAndGlueInteractor renders text using Knuth-Plass paragraph layout
// from the boxesandglue library. It receives text via a shared memory
// page whose address and length are provided at construction time.
// The length is driven by an eager constraint, so the BAG redraws
// automatically when the source updates.
//
// This interactor replaces WebInteractor for TeX-quality text rendering
// without an HTML engine. Font metrics and glyph bitmaps come from the
// same GlyphProvider / fontsvc infrastructure used by all other
// interactors.
package std

import (
	"image"
	"time"
	"unsafe"

	"mazzy/mazarin/attr"
	"mazzy/mazarin/mancini"
	"mazzy/mazarin/mancini/impl"
	"mazarin/textshape"

	"github.com/boxesandglue/boxesandglue/backend/bag"
	"github.com/boxesandglue/boxesandglue/backend/node"
)

// BoxesAndGlueInteractor is a leaf interactor that renders justified
// paragraphs using Knuth-Plass line breaking. It has outside-in sizing
// (width and height set by the parent via constraints).
type BoxesAndGlueInteractor struct {
	impl.ThemedInteractor

	// Shared page for text input — written by the source (VersaiEditor).
	textPageAddr uintptr
	textLenAttr  *attr.Attribute[int64] // eager constraint on source's textLen

	fontFamily string // font family name (overrides theme if set)
	fontID     int32
	fontSize   int32

	// Cached layout result. Recomputed when text, dimensions, or font change.
	lines     []bagLine
	layoutW   float64 // width used for last layout
	lastText  string  // text used for last layout
	drawCount int64
	drawTotal int64 // cumulative draw time in microseconds

	// Font metrics cached after first OpenFont.
	ascent  float64 // pixels
	descent float64 // pixels
	leading float64 // extra inter-line space in pixels
}

// bagLine is a single line of justified text with positioned glyphs.
type bagLine struct {
	glyphs []bagGlyph
}

// bagGlyph is a glyph positioned within a line.
type bagGlyph struct {
	x       float64 // X offset in pixels from line left edge
	gid     uint32
	fontID  int32
	cluster string // source text for this glyph
}

// NewBoxesAndGlueInteractor creates a BoxesAndGlueInteractor with
// outside-in sizing. The text page address and length constraint
// are configured via SetTextSource after construction.
func NewBoxesAndGlueInteractor(myName, parent string,
	theme mancini.Theme, fontSize int32) *BoxesAndGlueInteractor {

	if myName == "" {
		myName = mancini.DefaultName("bag")
	}
	lh := mancini.NewLayoutAttributes(myName, parent)

	b := &BoxesAndGlueInteractor{
		fontSize: fontSize,
		fontID:   -1,
		leading:  2.0, // 2px inter-line spacing
	}
	b.ThemedInteractor.Initialize(b, lh, theme)
	return b
}

// NewBoxesAndGlueInteractorWithLayout creates a BoxesAndGlueInteractor
// with pre-built layout attributes (for constrained height, etc.).
func NewBoxesAndGlueInteractorWithLayout(myName string,
	lh *mancini.LayoutAttributes, theme mancini.Theme,
	fontSize int32) *BoxesAndGlueInteractor {

	b := &BoxesAndGlueInteractor{
		fontSize: fontSize,
		fontID:   -1,
		leading:  2.0,
	}
	b.ThemedInteractor.Initialize(b, lh, theme)
	return b
}

// SetTextSource configures the shared page address and the eager
// length constraint. Call this once during setup.
func (b *BoxesAndGlueInteractor) SetTextSource(pageAddr uintptr, lenAttr *attr.Attribute[int64]) {
	b.textPageAddr = pageAddr
	b.textLenAttr = lenAttr
}

// readText reads the current text from the shared page using the
// length from the eager constraint.
func (b *BoxesAndGlueInteractor) readText() string {
	if b.textLenAttr == nil || b.textPageAddr == 0 {
		return ""
	}
	n := b.textLenAttr.Get()
	if n <= 0 {
		return ""
	}
	data := unsafe.Slice((*byte)(unsafe.Pointer(b.textPageAddr)), n)
	return string(data)
}

// SetLeading sets the extra inter-line spacing in pixels (default 2).
func (b *BoxesAndGlueInteractor) SetLeading(px float64) {
	b.leading = px
}

// ensureFont opens the font via DrawContext if not already done.
// SetFontFamily overrides the theme's font family for this interactor.
func (b *BoxesAndGlueInteractor) SetFontFamily(family string) {
	b.fontFamily = family
	b.fontID = -1 // force re-open
}

// SetFontSize changes the font size and forces re-layout on next Draw.
func (b *BoxesAndGlueInteractor) SetFontSize(size int32) {
	if size == b.fontSize {
		return
	}
	b.fontSize = size
	b.fontID = -1 // force re-open
	b.lastText = ""  // force re-layout
	b.FullDamage()
}

func (b *BoxesAndGlueInteractor) ensureFont(dc mancini.DrawContext) {
	if b.fontID >= 0 {
		return
	}
	family := b.fontFamily
	if family == "" {
		family = b.Theme().FontFamily()
	}
	m, err := dc.OpenFont(family, 0, b.fontSize)
	if err != nil {
		return
	}
	b.fontID = m.FontID
	b.ascent = float64(m.Ascent) / 64.0
	b.descent = float64(m.Descent) / 64.0
}

// Draw implements mancini.NewDrawer. Clears background, reads text
// from the shared page, runs layout if needed, and renders each glyph.
func (b *BoxesAndGlueInteractor) Draw(self mancini.Interactor,
	x, y, w, h int64, damage image.Rectangle) {

	t0 := time.Now()
	defer func() {
		b.drawTotal += time.Since(t0).Microseconds()
	}()

	if !self.Visible() {
		return
	}
	if !b.Damaged(damage) {
		return
	}
	dc := self.DC()
	if dc == nil {
		return
	}
	b.ensureFont(dc)

	fw, fh := float64(w), float64(h)
	fx, fy := float64(x), float64(y)

	// Clear background.
	pal := b.Theme().Palette()
	dc.SetColor(pal.Base())
	dc.FillRectangle(fx, fy, fw, fh)

	// Read current text from shared page.
	text := b.readText()

	b.drawCount++

	if b.fontID < 0 || len(text) == 0 {
		b.lines = nil
		b.lastText = ""
		b.SnapshotDamage()
		b.ClearDamage()
		return
	}

	// Re-layout if text or width changed.
	if text != b.lastText || b.layoutW != fw {
		b.layout(dc, fw, text)
		b.lastText = text
		b.layoutW = fw
	}

	// Render lines, auto-scrolled so the last line is always visible.
	tc := pal.Text()
	dc.SetColor(tc)
	lineHeight := b.ascent + b.descent + b.leading
	pad := 4.0 // small padding from edges

	// Compute how many lines fit and the starting offset.
	visibleLines := int((fh - 2*pad) / lineHeight)
	if visibleLines < 1 {
		visibleLines = 1
	}
	startLine := 0
	if len(b.lines) > visibleLines {
		startLine = len(b.lines) - visibleLines
	}

	for i := startLine; i < len(b.lines); i++ {
		line := b.lines[i]
		baseline := fy + pad + b.ascent + float64(i-startLine)*lineHeight
		if baseline+b.descent > fy+fh {
			break // clipped
		}
		for _, g := range line.glyphs {
			if g.cluster != "" && g.cluster != " " {
				dc.DrawText(g.cluster, g.fontID, fx+pad+g.x, baseline)
			}
		}
	}

	b.ClearDamage()
}

// layout runs text shaping and Knuth-Plass line breaking.
func (b *BoxesAndGlueInteractor) layout(dc mancini.DrawContext, widthPx float64, text string) {
	b.lines = nil

	tl := dc.TextLayout()
	if tl == nil {
		return
	}

	pad := 4.0
	availW := widthPx - 2*pad
	if availW <= 0 {
		return
	}

	metrics := dc.GetFontMetrics(b.fontID)

	// Shape the full text to get per-glyph data.
	run, err := tl.ShapeText(textshape.ShapingParams{
		Text:   text,
		FontID: b.fontID,
	})
	if err != nil {
		return
	}

	// Build boxesandglue node list from shaped glyphs.
	runes := []rune(text)
	nodeList := buildNodeList(runes, run, b.fontID, tl, metrics)
	if nodeList == nil {
		return
	}

	// Add finishing penalty + glue (required by Linebreak).
	last := nodeList
	for last.Next() != nil {
		last = last.Next()
	}
	// Infinite penalty to prevent break, then forced break.
	finishPenalty := node.NewPenalty()
	finishPenalty.Penalty = 10000
	last.SetNext(finishPenalty)
	finishPenalty.SetPrev(last)

	finishGlue := node.NewGlue()
	finishGlue.Width = 0
	finishGlue.Stretch = bag.Factor // infinite stretch
	finishGlue.StretchOrder = node.StretchFil
	finishPenalty.SetNext(finishGlue)
	finishGlue.SetPrev(finishPenalty)

	forcedBreak := node.NewPenalty()
	forcedBreak.Penalty = -10000 // forced break
	finishGlue.SetNext(forcedBreak)
	forcedBreak.SetPrev(finishGlue)

	// Run Knuth-Plass.
	hsize := pixelsToSP(availW)
	settings := node.NewLinebreakSettings()
	settings.HSize = hsize

	vlist, _ := node.Linebreak(nodeList, settings)
	if vlist == nil {
		return
	}

	// Walk VList → HLists → glyphs, extracting pixel positions.
	b.lines = walkVList(vlist, b.fontID)
}

// --- Node building ---

// buildNodeList converts shaped glyphs into a boxesandglue node list.
// Spaces become Glue nodes; everything else becomes Glyph nodes.
func buildNodeList(runes []rune, run textshape.ShapedRun,
	fontID int32, tl textshape.TextLayout,
	metrics textshape.FontMetrics) node.Node {

	if len(run.Glyphs) == 0 {
		return nil
	}

	// Compute interword glue from space metrics.
	spaceAdv, _ := tl.MeasureText(textshape.ShapingParams{
		Text:   " ",
		FontID: fontID,
	})
	spaceWidth := fixed26_6ToSP(spaceAdv)
	spaceStretch := spaceWidth / 2
	spaceShrink := spaceWidth / 3

	ascSP := fixed26_6ToSP(metrics.Ascent)
	descSP := fixed26_6ToSP(metrics.Descent)

	var head, tail node.Node

	for i, sg := range run.Glyphs {
		// Determine the source text for this glyph's cluster.
		clusterStart := int(sg.Cluster)
		clusterEnd := len(runes)
		if i+1 < len(run.Glyphs) {
			clusterEnd = int(run.Glyphs[i+1].Cluster)
		}
		if clusterStart >= len(runes) {
			continue
		}
		if clusterEnd > len(runes) {
			clusterEnd = len(runes)
		}
		clusterText := string(runes[clusterStart:clusterEnd])

		// Space → Glue node.
		if clusterText == " " {
			g := node.NewGlue()
			g.Width = spaceWidth
			g.Stretch = spaceStretch
			g.Shrink = spaceShrink
			appendNode(&head, &tail, g)
			continue
		}

		// Newline → forced break (penalty -10000 + glue).
		if clusterText == "\n" {
			p := node.NewPenalty()
			p.Penalty = -10000
			appendNode(&head, &tail, p)
			continue
		}

		glyph := node.NewGlyph()
		glyph.Codepoint = int(sg.GID)
		glyph.Width = fixed26_6ToSP(sg.XAdvance)
		glyph.Height = ascSP
		glyph.Depth = descSP
		glyph.Components = clusterText
		// glyph.Font intentionally nil — Linebreak never reads it.
		appendNode(&head, &tail, glyph)
	}

	return head
}

func appendNode(head, tail *node.Node, n node.Node) {
	if *head == nil {
		*head = n
		*tail = n
	} else {
		(*tail).SetNext(n)
		n.SetPrev(*tail)
		*tail = n
	}
}

// --- Tree walking ---

// walkVList traverses the boxesandglue VList and extracts pixel
// positions for each glyph on each line.
func walkVList(vl *node.VList, fontID int32) []bagLine {
	var lines []bagLine

	for n := vl.List; n != nil; n = n.Next() {
		hl, ok := node.IsHList(n)
		if !ok {
			continue
		}
		line := walkHList(hl, fontID)
		lines = append(lines, line)
	}

	return lines
}

// walkHList traverses one line and computes glyph X positions,
// applying the glue set ratio for justification.
func walkHList(hl *node.HList, fontID int32) bagLine {
	var line bagLine
	x := 0.0

	for n := hl.List; n != nil; n = n.Next() {
		if g, ok := node.IsGlyph(n); ok {
			line.glyphs = append(line.glyphs, bagGlyph{
				x:       x,
				gid:     uint32(g.Codepoint),
				fontID:  fontID,
				cluster: g.Components,
			})
			x += spToPixels(g.Width)
		} else if g, ok := node.IsGlue(n); ok {
			w := spToPixels(g.Width)
			// Apply glue adjustment for justification.
			if hl.GlueSign == 1 && g.Stretch > 0 { // stretching
				w += hl.GlueSet * spToPixels(g.Stretch)
			} else if hl.GlueSign == 2 && g.Shrink > 0 { // shrinking
				w -= hl.GlueSet * spToPixels(g.Shrink)
			}
			x += w
		} else if k, ok := node.IsKern(n); ok {
			x += spToPixels(k.Kern)
		}
	}

	return line
}

// --- Unit conversion ---

// spPerPixel converts pixels to ScaledPoints at 96 dpi.
// 1 DTP point = 1/72 inch; 1 pixel at 96dpi = 1/96 inch.
// 1 SP = 1/65536 point. So 1 pixel = 65536 * 72 / 96 = 49152 SP.
const spPerPixel = 49152

func pixelsToSP(px float64) bag.ScaledPoint {
	return bag.ScaledPoint(px * float64(spPerPixel))
}

func spToPixels(sp bag.ScaledPoint) float64 {
	return float64(sp) / float64(spPerPixel)
}

// fixed26_6ToSP converts a fixed.Int26_6 value (1/64 pixel) to ScaledPoint.
func fixed26_6ToSP(f int32) bag.ScaledPoint {
	return bag.ScaledPoint(int64(f) * int64(spPerPixel) / 64)
}
