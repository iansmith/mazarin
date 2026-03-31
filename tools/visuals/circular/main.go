package main

import (
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"os"
	"os/exec"

	"github.com/fogleman/gg"
)

// Neumorphic palette — matches mancini DefaultPalette.
var (
	surface    = color.NRGBA{232, 230, 244, 255}
	darkSh     = color.NRGBA{176, 173, 195, 255}
	lightSh    = color.NRGBA{255, 255, 255, 255}
	textCol    = color.NRGBA{78, 72, 112, 255}
	selectedBg = color.NRGBA{200, 100, 255, 255}
)

const fontPath = "/Users/iansmith/louis14/fonts/AtkinsonHyperlegible-Bold.ttf"

// Raised shadow parameters tuned for a 50px annular ring.
var ringRaised = struct {
	LightOff, LightBlur   float64
	DarkOff, DarkBlur     float64
	DarkAlpha, LightAlpha uint8
}{
	LightOff:   4,
	LightBlur:  4,
	DarkOff:    6,
	DarkBlur:   6,
	DarkAlpha:  160,
	LightAlpha: 240,
}

const (
	outerDiameter = 300.0
	innerDiameter = 200.0
	canvasMargin  = 40.0
)

// ── SegmentDecorator interface ─────────────────────────────────────────────────

// SegmentDecorator draws content into a single annular segment.
// The segment is defined by its angular range [thetaStart, thetaEnd] and
// radial range [rInner, rOuter], centered at (cx, cy).
// The bounds are pre-inset to avoid painting over grooves and inset shadows.
type SegmentDecorator interface {
	Draw(canvas *image.RGBA, cx, cy, thetaStart, thetaEnd, rInner, rOuter float64)
}

// ── StringDrawer ────────────────────────────────────────────────────────────

// StringDrawer draws a text label centered in the segment.
type StringDrawer struct {
	Text string
}

func (d *StringDrawer) Draw(canvas *image.RGBA, cx, cy, thetaStart, thetaEnd, rInner, rOuter float64) {
	midAngle := (thetaStart + thetaEnd) / 2
	midR := (rInner + rOuter) / 2
	tx := cx + midR*math.Cos(midAngle)
	ty := cy + midR*math.Sin(midAngle)

	bounds := canvas.Bounds()
	w, h := bounds.Dx(), bounds.Dy()

	labelBuf := image.NewRGBA(image.Rect(0, 0, w, h))
	dc := gg.NewContextForRGBA(labelBuf)
	if err := dc.LoadFontFace(fontPath, 16); err != nil {
		panic(err)
	}
	dc.SetColor(textCol)
	dc.DrawStringAnchored(d.Text, tx, ty, 0.5, 0.35)

	mask := segmentMask(w, h, cx, cy, rOuter, rInner, thetaStart, thetaEnd)
	draw.DrawMask(canvas, bounds, labelBuf, image.Point{}, mask, image.Point{}, draw.Over)
}

// ── ColorDrawer ─────────────────────────────────────────────────────────────

// ColorDrawer fills the segment with a solid color.
type ColorDrawer struct {
	Color color.NRGBA
}

func (d *ColorDrawer) Draw(canvas *image.RGBA, cx, cy, thetaStart, thetaEnd, rInner, rOuter float64) {
	bounds := canvas.Bounds()
	w, h := bounds.Dx(), bounds.Dy()

	colorBuf := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			off := y*colorBuf.Stride + x*4
			colorBuf.Pix[off] = d.Color.R
			colorBuf.Pix[off+1] = d.Color.G
			colorBuf.Pix[off+2] = d.Color.B
			colorBuf.Pix[off+3] = d.Color.A
		}
	}

	mask := segmentMask(w, h, cx, cy, rOuter, rInner, thetaStart, thetaEnd)
	draw.DrawMask(canvas, bounds, colorBuf, image.Point{}, mask, image.Point{}, draw.Over)
}

// ── ShapeDrawer ─────────────────────────────────────────────────────────────

type ShapeKind int

const (
	ShapeCircle ShapeKind = iota
	ShapeSquare
	ShapeDiamond
	ShapeTriangle
)

// ShapeDrawer draws a small filled shape centered in the segment.
type ShapeDrawer struct {
	Kind  ShapeKind
	Color color.NRGBA
}

func (d *ShapeDrawer) Draw(canvas *image.RGBA, cx, cy, thetaStart, thetaEnd, rInner, rOuter float64) {
	midAngle := (thetaStart + thetaEnd) / 2
	midR := (rInner + rOuter) / 2
	sx := cx + midR*math.Cos(midAngle)
	sy := cy + midR*math.Sin(midAngle)

	shapeSize := (rOuter - rInner) * 0.45
	half := shapeSize / 2

	bounds := canvas.Bounds()
	w, h := bounds.Dx(), bounds.Dy()

	shapeBuf := image.NewRGBA(image.Rect(0, 0, w, h))
	dc := gg.NewContextForRGBA(shapeBuf)
	dc.SetColor(d.Color)

	switch d.Kind {
	case ShapeCircle:
		dc.DrawCircle(sx, sy, half)
		dc.Fill()
	case ShapeSquare:
		dc.DrawRectangle(sx-half, sy-half, shapeSize, shapeSize)
		dc.Fill()
	case ShapeDiamond:
		dc.MoveTo(sx, sy-half)
		dc.LineTo(sx+half, sy)
		dc.LineTo(sx, sy+half)
		dc.LineTo(sx-half, sy)
		dc.ClosePath()
		dc.Fill()
	case ShapeTriangle:
		dc.MoveTo(sx, sy-half)
		dc.LineTo(sx+half, sy+half)
		dc.LineTo(sx-half, sy+half)
		dc.ClosePath()
		dc.Fill()
	}

	mask := segmentMask(w, h, cx, cy, rOuter, rInner, thetaStart, thetaEnd)
	draw.DrawMask(canvas, bounds, shapeBuf, image.Point{}, mask, image.Point{}, draw.Over)
}

// ── Main ────────────────────────────────────────────────────────────────────

func main() {
	shapeColors := []color.NRGBA{
		{220, 50, 50, 255},   // red
		{50, 100, 220, 255},  // blue
		{60, 180, 75, 255},   // green
		{230, 140, 30, 255},  // orange
		{140, 60, 180, 255},  // purple
		{60, 200, 210, 255},  // cyan
		{230, 210, 40, 255},  // yellow
		{230, 130, 170, 255}, // pink
	}
	shapeKinds := []ShapeKind{ShapeCircle, ShapeSquare, ShapeDiamond, ShapeTriangle}

	// Helper: make shape drawers cycling through kinds and colors.
	makeShapes := func(n int) []SegmentDecorator {
		drawers := make([]SegmentDecorator, n)
		for i := range drawers {
			drawers[i] = &ShapeDrawer{
				Kind:  shapeKinds[i%len(shapeKinds)],
				Color: shapeColors[i%len(shapeColors)],
			}
		}
		return drawers
	}

	type example struct {
		drawers  []SegmentDecorator
		selected int
	}

	// Top-left: Ok/Cancel with shapes.
	ex0 := makeShapes(2)

	// Top-right: 10 colors using ColorDrawer.
	colorEntries := []struct {
		c color.NRGBA
	}{
		{color.NRGBA{220, 50, 50, 255}},   // Red
		{color.NRGBA{230, 140, 30, 255}},  // Orange
		{color.NRGBA{230, 210, 40, 255}},  // Yellow
		{color.NRGBA{60, 180, 75, 255}},   // Green
		{color.NRGBA{50, 100, 220, 255}},  // Blue
		{color.NRGBA{140, 60, 180, 255}},  // Purple
		{color.NRGBA{230, 130, 170, 255}}, // Pink
		{color.NRGBA{60, 200, 210, 255}},  // Cyan
		{color.NRGBA{150, 100, 50, 255}},  // Brown
		{color.NRGBA{150, 150, 150, 255}}, // Gray
	}
	ex1 := make([]SegmentDecorator, len(colorEntries))
	for i, ce := range colorEntries {
		ex1[i] = &ColorDrawer{Color: ce.c}
	}

	// Bottom-left: 05/10/25/50 with shapes.
	ex2 := makeShapes(4)

	// Bottom-right: French cities with text.
	cities := []string{"annecy", "montpellier", "marseilles", "aix-en-provence",
		"paris", "nantes", "rennes", "lens"}
	ex3 := make([]SegmentDecorator, len(cities))
	for i, name := range cities {
		ex3[i] = &StringDrawer{Text: name}
	}

	examples := []example{
		{ex0, 0},
		{ex1, 9},
		{ex2, 2},
		{ex3, 3},
	}

	cellSize := int(math.Ceil(outerDiameter + 2*canvasMargin))
	gap := 20
	totalW := cellSize*2 + gap
	totalH := cellSize*2 + gap

	canvas := image.NewRGBA(image.Rect(0, 0, totalW, totalH))
	fillSurface(canvas)

	for i, ex := range examples {
		cell := drawCircularMenu(ex.drawers, ex.selected)
		col := i % 2
		row := i / 2
		ox := col * (cellSize + gap)
		oy := row * (cellSize + gap)
		draw.Draw(canvas, image.Rect(ox, oy, ox+cellSize, oy+cellSize),
			cell, image.Point{}, draw.Over)
	}

	outPath := "tools/visuals/circular/output.png"
	f, err := os.Create(outPath)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	if err := png.Encode(f, canvas); err != nil {
		panic(err)
	}

	exec.Command("open", outPath).Run()
}

// ── Core circular menu rendering ────────────────────────────────────────────

// drawCircularMenu draws a complete circular menu. The base structure
// (raised annulus, selected inset, grooves) is drawn first, then each
// SegmentDecorator is called to render its content.
func drawCircularMenu(drawers []SegmentDecorator, selectedIndex int) *image.RGBA {
	outerR := outerDiameter / 2.0
	innerR := innerDiameter / 2.0
	n := len(drawers)

	size := int(math.Ceil(outerDiameter + 2*canvasMargin))
	canvas := image.NewRGBA(image.Rect(0, 0, size, size))
	fillSurface(canvas)

	cx := float64(size) / 2.0
	cy := float64(size) / 2.0

	// Base structure.
	drawRaisedAnnulus(canvas, cx, cy, outerR, innerR)
	drawInsetSegment(canvas, cx, cy, outerR, innerR, selectedIndex, n)
	drawGrooves(canvas, cx, cy, outerR, innerR, n)

	// Segment content — drawn after base structure.
	// All segments are inset slightly to avoid painting over groove separators.
	// The selected segment is inset further to preserve its depressed shadow.
	segAngle := 2 * math.Pi / float64(n)
	grooveMargin := 3.0 // pixels to keep away from groove lines
	insetMargin := 5.0  // additional pixels for selected segment shadows
	midR := (innerR + outerR) / 2
	grooveAngular := grooveMargin / midR
	insetAngular := insetMargin / midR
	for i, drawer := range drawers {
		thetaStart := -math.Pi/2 + float64(i)*segAngle + grooveAngular
		thetaEnd := -math.Pi/2 + float64(i+1)*segAngle - grooveAngular
		rInner := innerR + grooveMargin
		rOuter := outerR - grooveMargin
		if i == selectedIndex {
			thetaStart += insetAngular
			thetaEnd -= insetAngular
			rInner += insetMargin
			rOuter -= insetMargin
		}
		drawer.Draw(canvas, cx, cy, thetaStart, thetaEnd, rInner, rOuter)
	}

	return canvas
}

func fillSurface(canvas *image.RGBA) {
	b := canvas.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			off := y*canvas.Stride + x*4
			canvas.Pix[off] = surface.R
			canvas.Pix[off+1] = surface.G
			canvas.Pix[off+2] = surface.B
			canvas.Pix[off+3] = surface.A
		}
	}
}

// ── Neumorphic base rendering ───────────────────────────────────────────────

func drawRaisedAnnulus(canvas *image.RGBA, cx, cy, outerR, innerR float64) {
	p := ringRaised

	x1, y1 := cx-outerR, cy-outerR
	x2, y2 := cx+outerR, cy+outerR
	maxOff := math.Max(p.DarkOff, p.LightOff)
	maxBlur := math.Max(p.DarkBlur, p.LightBlur)
	pad := maxOff + math.Ceil(maxBlur*3) + 2
	lw, lh, ox, oy := localRect(canvas, x1, y1, x2, y2, pad)
	lcx, lcy := cx-ox, cy-oy
	dst := image.Rect(int(ox), int(oy), int(ox)+lw, int(oy)+lh)

	dark := annulusShadowLayer(lw, lh,
		lcx+p.DarkOff, lcy+p.DarkOff, outerR, innerR,
		darkSh, p.DarkAlpha, p.DarkBlur)
	draw.Draw(canvas, dst, dark, image.Point{}, draw.Over)

	light := annulusShadowLayer(lw, lh,
		lcx-p.LightOff, lcy-p.LightOff, outerR, innerR,
		lightSh, p.LightAlpha, p.LightBlur)
	draw.Draw(canvas, dst, light, image.Point{}, draw.Over)

	dc := gg.NewContextForRGBA(canvas)
	dc.SetFillRuleEvenOdd()
	dc.SetColor(surface)
	dc.DrawCircle(cx, cy, outerR)
	dc.DrawCircle(cx, cy, innerR)
	dc.Fill()
}

func annulusShadowLayer(w, h int, cx, cy, outerR, innerR float64, c color.NRGBA, alpha uint8, blur float64) *image.NRGBA {
	rgba := image.NewRGBA(image.Rect(0, 0, w, h))
	dc := gg.NewContextForRGBA(rgba)
	dc.SetFillRuleEvenOdd()
	dc.SetColor(color.NRGBA{c.R, c.G, c.B, alpha})
	dc.DrawCircle(cx, cy, outerR)
	dc.DrawCircle(cx, cy, innerR)
	dc.Fill()
	nrgba := rgbaToNRGBA(rgba)
	if blur > 0 {
		return gaussianBlurNRGBA(nrgba, blur)
	}
	return nrgba
}

func drawGrooves(canvas *image.RGBA, cx, cy, outerR, innerR float64, segments int) {
	bounds := canvas.Bounds()
	w, h := bounds.Dx(), bounds.Dy()

	mask := annulusMask(w, h, cx, cy, outerR, innerR)

	grooveOff := 0.8
	lineWidth := 1.5

	darkBuf := image.NewRGBA(image.Rect(0, 0, w, h))
	ddc := gg.NewContextForRGBA(darkBuf)
	ddc.SetColor(color.NRGBA{darkSh.R, darkSh.G, darkSh.B, 150})
	ddc.SetLineWidth(lineWidth)
	for i := 0; i < segments; i++ {
		angle := -math.Pi/2 + float64(i)*2*math.Pi/float64(segments)
		cos, sin := math.Cos(angle), math.Sin(angle)
		ddc.DrawLine(
			cx+innerR*cos-grooveOff, cy+innerR*sin-grooveOff,
			cx+outerR*cos-grooveOff, cy+outerR*sin-grooveOff,
		)
		ddc.Stroke()
	}
	darkNRGBA := rgbaToNRGBA(darkBuf)
	darkNRGBA = gaussianBlurNRGBA(darkNRGBA, 1.5)
	draw.DrawMask(canvas, bounds, darkNRGBA, image.Point{}, mask, image.Point{}, draw.Over)

	lightBuf := image.NewRGBA(image.Rect(0, 0, w, h))
	ldc := gg.NewContextForRGBA(lightBuf)
	ldc.SetColor(color.NRGBA{lightSh.R, lightSh.G, lightSh.B, 150})
	ldc.SetLineWidth(lineWidth)
	for i := 0; i < segments; i++ {
		angle := -math.Pi/2 + float64(i)*2*math.Pi/float64(segments)
		cos, sin := math.Cos(angle), math.Sin(angle)
		ldc.DrawLine(
			cx+innerR*cos+grooveOff, cy+innerR*sin+grooveOff,
			cx+outerR*cos+grooveOff, cy+outerR*sin+grooveOff,
		)
		ldc.Stroke()
	}
	lightNRGBA := rgbaToNRGBA(lightBuf)
	lightNRGBA = gaussianBlurNRGBA(lightNRGBA, 1.0)
	draw.DrawMask(canvas, bounds, lightNRGBA, image.Point{}, mask, image.Point{}, draw.Over)
}

func drawInsetSegment(canvas *image.RGBA, cx, cy, outerR, innerR float64, segIndex, totalSegs int) {
	segAngle := 2 * math.Pi / float64(totalSegs)
	startAngle := -math.Pi/2 + float64(segIndex)*segAngle
	endAngle := startAngle + segAngle

	bounds := canvas.Bounds()
	w, h := bounds.Dx(), bounds.Dy()

	off := 2.5
	darkBlur := 4.0
	lightBlur := 3.0

	// Erase raised shadows in this segment's annular area.
	p := ringRaised
	shadowPad := math.Max(p.DarkOff, p.LightOff) + math.Ceil(math.Max(p.DarkBlur, p.LightBlur)*3) + 2
	eraseOuterR := outerR + shadowPad
	eraseInnerR := innerR
	angleMargin := 0.02
	dc := gg.NewContextForRGBA(canvas)
	dc.Push()
	dc.MoveTo(cx, cy)
	dc.DrawArc(cx, cy, eraseOuterR+1, startAngle-angleMargin, endAngle+angleMargin)
	dc.ClosePath()
	dc.Clip()
	dc.SetFillRuleEvenOdd()
	dc.SetColor(surface)
	dc.DrawCircle(cx, cy, eraseOuterR)
	dc.DrawCircle(cx, cy, eraseInnerR)
	dc.Fill()
	dc.Pop()

	// Segment mask.
	mask := segmentMask(w, h, cx, cy, outerR, innerR, startAngle, endAngle)

	// Selected tint fill, masked to segment.
	tintBuf := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			poff := y*tintBuf.Stride + x*4
			tintBuf.Pix[poff] = selectedBg.R
			tintBuf.Pix[poff+1] = selectedBg.G
			tintBuf.Pix[poff+2] = selectedBg.B
			tintBuf.Pix[poff+3] = selectedBg.A
		}
	}
	draw.DrawMask(canvas, bounds, tintBuf, image.Point{}, mask, image.Point{}, draw.Over)

	// Dark shadow — segment shape offset upper-left, blurred, masked.
	darkBuf := image.NewRGBA(image.Rect(0, 0, w, h))
	drawFilledSegment(darkBuf, cx-off, cy-off, outerR, innerR, startAngle, endAngle,
		color.NRGBA{darkSh.R, darkSh.G, darkSh.B, 180})
	darkNRGBA := gaussianBlurNRGBA(rgbaToNRGBA(darkBuf), darkBlur)
	draw.DrawMask(canvas, bounds, darkNRGBA, image.Point{}, mask, image.Point{}, draw.Over)

	// Light shadow — segment shape offset lower-right, blurred, masked.
	lightBuf := image.NewRGBA(image.Rect(0, 0, w, h))
	drawFilledSegment(lightBuf, cx+off, cy+off, outerR, innerR, startAngle, endAngle,
		color.NRGBA{lightSh.R, lightSh.G, lightSh.B, 180})
	lightNRGBA := gaussianBlurNRGBA(rgbaToNRGBA(lightBuf), lightBlur)
	draw.DrawMask(canvas, bounds, lightNRGBA, image.Point{}, mask, image.Point{}, draw.Over)
}

// ── Segment geometry helpers ────────────────────────────────────────────────

func annulusMask(w, h int, cx, cy, outerR, innerR float64) *image.Alpha {
	rgba := image.NewRGBA(image.Rect(0, 0, w, h))
	dc := gg.NewContextForRGBA(rgba)
	dc.SetFillRuleEvenOdd()
	dc.SetColor(color.White)
	dc.DrawCircle(cx, cy, outerR)
	dc.DrawCircle(cx, cy, innerR)
	dc.Fill()
	mask := image.NewAlpha(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			mask.Pix[y*mask.Stride+x] = rgba.Pix[y*rgba.Stride+x*4+3]
		}
	}
	return mask
}

func drawFilledSegment(rgba *image.RGBA, cx, cy, outerR, innerR, startAngle, endAngle float64, c color.NRGBA) {
	dc := gg.NewContextForRGBA(rgba)
	dc.MoveTo(cx, cy)
	dc.DrawArc(cx, cy, outerR+2, startAngle, endAngle)
	dc.ClosePath()
	dc.Clip()
	dc.SetFillRuleEvenOdd()
	dc.SetColor(c)
	dc.DrawCircle(cx, cy, outerR)
	dc.DrawCircle(cx, cy, innerR)
	dc.Fill()
}

func segmentMask(w, h int, cx, cy, outerR, innerR, startAngle, endAngle float64) *image.Alpha {
	rgba := image.NewRGBA(image.Rect(0, 0, w, h))
	drawFilledSegment(rgba, cx, cy, outerR, innerR, startAngle, endAngle,
		color.NRGBA{255, 255, 255, 255})
	mask := image.NewAlpha(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			mask.Pix[y*mask.Stride+x] = rgba.Pix[y*rgba.Stride+x*4+3]
		}
	}
	return mask
}

// ── Low-level helpers ───────────────────────────────────────────────────────

func localRect(canvas *image.RGBA, x1, y1, x2, y2, pad float64) (lw, lh int, ox, oy float64) {
	b := canvas.Bounds()
	ox = math.Floor(x1 - pad)
	oy = math.Floor(y1 - pad)
	ex := math.Ceil(x2 + pad)
	ey := math.Ceil(y2 + pad)
	if ox < float64(b.Min.X) {
		ox = float64(b.Min.X)
	}
	if oy < float64(b.Min.Y) {
		oy = float64(b.Min.Y)
	}
	if ex > float64(b.Max.X) {
		ex = float64(b.Max.X)
	}
	if ey > float64(b.Max.Y) {
		ey = float64(b.Max.Y)
	}
	return int(ex - ox), int(ey - oy), ox, oy
}

func clampU8(v float64) uint8 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(math.Round(v))
}

func rgbaToNRGBA(src *image.RGBA) *image.NRGBA {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	dst := image.NewNRGBA(b)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			soff := y*src.Stride + x*4
			doff := y*dst.Stride + x*4
			a := src.Pix[soff+3]
			if a == 0 {
				continue
			}
			if a == 255 {
				dst.Pix[doff] = src.Pix[soff]
				dst.Pix[doff+1] = src.Pix[soff+1]
				dst.Pix[doff+2] = src.Pix[soff+2]
				dst.Pix[doff+3] = 255
				continue
			}
			a32 := uint32(a)
			dst.Pix[doff] = uint8(uint32(src.Pix[soff]) * 255 / a32)
			dst.Pix[doff+1] = uint8(uint32(src.Pix[soff+1]) * 255 / a32)
			dst.Pix[doff+2] = uint8(uint32(src.Pix[soff+2]) * 255 / a32)
			dst.Pix[doff+3] = a
		}
	}
	return dst
}

func gaussianKernel(sigma float64) []float64 {
	radius := int(math.Ceil(sigma * 3))
	if radius < 1 {
		radius = 1
	}
	size := 2*radius + 1
	k := make([]float64, size)
	var sum float64
	for i := range k {
		x := float64(i - radius)
		k[i] = math.Exp(-(x * x) / (2 * sigma * sigma))
		sum += k[i]
	}
	for i := range k {
		k[i] /= sum
	}
	return k
}

func gaussianBlurNRGBA(src *image.NRGBA, sigma float64) *image.NRGBA {
	if sigma <= 0 {
		cp := image.NewNRGBA(src.Bounds())
		copy(cp.Pix, src.Pix)
		return cp
	}
	k := gaussianKernel(sigma)
	rad := len(k) / 2
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()

	tmp := image.NewNRGBA(b)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var rr, gg, bb, aa float64
			for ki, kv := range k {
				sx := x + ki - rad
				if sx < 0 {
					sx = 0
				} else if sx >= w {
					sx = w - 1
				}
				off := y*src.Stride + sx*4
				rr += float64(src.Pix[off]) * kv
				gg += float64(src.Pix[off+1]) * kv
				bb += float64(src.Pix[off+2]) * kv
				aa += float64(src.Pix[off+3]) * kv
			}
			off := y*tmp.Stride + x*4
			tmp.Pix[off] = clampU8(rr)
			tmp.Pix[off+1] = clampU8(gg)
			tmp.Pix[off+2] = clampU8(bb)
			tmp.Pix[off+3] = clampU8(aa)
		}
	}

	dst := image.NewNRGBA(b)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var rr, gg, bb, aa float64
			for ki, kv := range k {
				sy := y + ki - rad
				if sy < 0 {
					sy = 0
				} else if sy >= h {
					sy = h - 1
				}
				off := sy*tmp.Stride + x*4
				rr += float64(tmp.Pix[off]) * kv
				gg += float64(tmp.Pix[off+1]) * kv
				bb += float64(tmp.Pix[off+2]) * kv
				aa += float64(tmp.Pix[off+3]) * kv
			}
			off := y*dst.Stride + x*4
			dst.Pix[off] = clampU8(rr)
			dst.Pix[off+1] = clampU8(gg)
			dst.Pix[off+2] = clampU8(bb)
			dst.Pix[off+3] = clampU8(aa)
		}
	}
	return dst
}
