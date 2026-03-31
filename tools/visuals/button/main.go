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

// ── Button depth ────────────────────────────────────────────────────────────

type ButtonDepth int

const (
	Raised    ButtonDepth = iota
	Flat                  // flush with surface
	Depressed             // pushed in
)

// Same types as mancini.RaisedParams / InsetParams / FlushParams / NeuParams.
// Copied here so this standalone tool doesn't import mancini.

type RaisedParams struct {
	LightOff, LightBlur   float64
	DarkOff, DarkBlur     float64
	DarkAlpha, LightAlpha uint8
}

type InsetParams struct {
	Off                 float64
	DarkBlur, LightBlur float64
}

type FlushParams struct {
	EdgeW     float64
	EdgeAlpha uint8
}

type NeuParams struct {
	Raised RaisedParams
	Flush  FlushParams
	Inset  InsetParams
}

// NeuButtonParams — lighter than NeuBoxParams, comparable to scrollbar thumb.
var NeuButtonParams = NeuParams{
	Raised: RaisedParams{LightOff: 1.5, LightBlur: 3, DarkOff: 4, DarkBlur: 4, DarkAlpha: 100, LightAlpha: 220},
	Flush:  FlushParams{EdgeW: 1.5, EdgeAlpha: 140},
	Inset:  InsetParams{Off: 1.5, DarkBlur: 3, LightBlur: 2},
}

// ── RectangularFace ─────────────────────────────────────────────────────────

// RectangularFace draws content into the drawable interior of a button.
// The coordinates define the area after neumorphic sides have been applied,
// so (x, y, w, h) is smaller than the overall button bounds.
type RectangularFace interface {
	Draw(canvas *image.RGBA, x, y, w, h float64)
}

// ── LabelFace ───────────────────────────────────────────────────────────────

// LabelFace draws a centered text label.
type LabelFace struct {
	Text     string
	FontSize float64
}

func (f *LabelFace) Draw(canvas *image.RGBA, x, y, w, h float64) {
	dc := gg.NewContextForRGBA(canvas)
	fontSize := f.FontSize
	if fontSize == 0 {
		fontSize = 18
	}
	if err := dc.LoadFontFace(fontPath, int64(math.Round(fontSize))); err != nil {
		panic(err)
	}
	dc.SetColor(textCol)
	dc.DrawStringAnchored(f.Text, x+w/2, y+h/2, 0.5, 0.35)
}

// ── IconFace ────────────────────────────────────────────────────────────────

// IconFace draws a simple geometric icon centered in the face.
type IconFace struct {
	Kind  IconKind
	Color color.NRGBA
}

type IconKind int

const (
	IconPlay IconKind = iota
	IconStop
	IconCircle
)

func (f *IconFace) Draw(canvas *image.RGBA, x, y, w, h float64) {
	cx, cy := x+w/2, y+h/2
	size := math.Min(w, h) * 0.35

	dc := gg.NewContextForRGBA(canvas)
	dc.SetColor(f.Color)

	switch f.Kind {
	case IconPlay:
		// Right-pointing triangle.
		dc.MoveTo(cx-size*0.5, cy-size*0.6)
		dc.LineTo(cx+size*0.6, cy)
		dc.LineTo(cx-size*0.5, cy+size*0.6)
		dc.ClosePath()
		dc.Fill()
	case IconStop:
		half := size * 0.45
		dc.DrawRectangle(cx-half, cy-half, half*2, half*2)
		dc.Fill()
	case IconCircle:
		dc.DrawCircle(cx, cy, size*0.45)
		dc.Fill()
	}
}

// ── CheckmarkFace ──────────────────────────────────────────────────────────

// CheckmarkFace draws a checkmark centered in the face area.
type CheckmarkFace struct {
	Color     color.NRGBA
	LineWidth float64
}

func (f *CheckmarkFace) Draw(canvas *image.RGBA, x, y, w, h float64) {
	cx, cy := x+w/2, y+h/2
	size := math.Min(w, h) * 0.35
	lw := f.LineWidth
	if lw == 0 {
		lw = 2.5
	}

	dc := gg.NewContextForRGBA(canvas)
	dc.SetColor(f.Color)
	dc.SetLineWidth(lw)
	dc.SetLineCap(gg.LineCapRound)
	dc.SetLineJoin(gg.LineJoinRound)
	// Short leg down-right, then long leg up-right.
	dc.MoveTo(cx-size*0.55, cy+size*0.05)
	dc.LineTo(cx-size*0.1, cy+size*0.55)
	dc.LineTo(cx+size*0.6, cy-size*0.45)
	dc.Stroke()
}

// ── Checkbox ───────────────────────────────────────────────────────────────

// drawNeuCheckbox draws a square neumorphic checkbox.
// checked==false → depressed (inset), checked==true → raised with checkmark.
func drawNeuCheckbox(canvas *image.RGBA, cx, cy, size float64, checked bool, params NeuParams) {
	x1, y1 := cx-size/2, cy-size/2
	x2, y2 := cx+size/2, cy+size/2

	if checked {
		drawNeuButton(canvas, x1, y1, x2, y2, Raised, params,
			&CheckmarkFace{Color: textCol, LineWidth: 2.5})
	} else {
		drawNeuButton(canvas, x1, y1, x2, y2, Depressed, params, nil)
	}
}

// ── CheckboxWithLabel ──────────────────────────────────────────────────────

type LabelSide int

const (
	LabelTop LabelSide = iota
	LabelRight
	LabelBottom
	LabelLeft
)

// drawCheckboxWithLabel draws a checkbox with a RectangularFace label on the
// specified side. The origin (ox, oy) is the top-left of the combined bounds.
// labelW and labelH give the space allocated to the label face.
func drawCheckboxWithLabel(canvas *image.RGBA, ox, oy float64, checkSize, labelW, labelH, gap float64, side LabelSide, checked bool, params NeuParams, label RectangularFace) {
	var checkCX, checkCY float64
	var labelX, labelY float64

	switch side {
	case LabelTop:
		// Label above, checkbox below.
		labelX = ox + (checkSize-labelW)/2
		labelY = oy
		checkCX = ox + checkSize/2
		checkCY = oy + labelH + gap + checkSize/2
	case LabelBottom:
		// Checkbox above, label below.
		checkCX = ox + checkSize/2
		checkCY = oy + checkSize/2
		labelX = ox + (checkSize-labelW)/2
		labelY = oy + checkSize + gap
	case LabelLeft:
		// Label left, checkbox right.
		labelX = ox
		labelY = oy + (checkSize-labelH)/2
		checkCX = ox + labelW + gap + checkSize/2
		checkCY = oy + checkSize/2
	case LabelRight:
		// Checkbox left, label right.
		checkCX = ox + checkSize/2
		checkCY = oy + checkSize/2
		labelX = ox + checkSize + gap
		labelY = oy + (checkSize-labelH)/2
	}

	drawNeuCheckbox(canvas, checkCX, checkCY, checkSize, checked, params)
	if label != nil {
		label.Draw(canvas, labelX, labelY, labelW, labelH)
	}
}

// ── Strip ──────────────────────────────────────────────────────────────────

// A strip is a vertical rectangle with a flat top edge and rounded corners
// on the bottom — like a label tab pointing downward. When placed side by
// side the flat top edges form a continuous bar.

const stripCornerR = 10.0

// drawStripPath traces a rectangle that is flat on top and rounded on the
// bottom. The caller must call Fill() or Stroke() afterwards.
func drawStripPath(dc *gg.Context, x1, y1, w, h, r float64) {
	x2 := x1 + w
	y2 := y1 + h
	if r > w/2 {
		r = w / 2
	}
	dc.MoveTo(x1, y1)
	dc.LineTo(x2, y1)
	dc.LineTo(x2, y2-r)
	dc.DrawArc(x2-r, y2-r, r, 0, math.Pi/2)
	dc.LineTo(x1+r, y2)
	dc.DrawArc(x1+r, y2-r, r, math.Pi/2, math.Pi)
	dc.ClosePath()
}

func stripShadowLayer(w, h int, sx1, sy1, sw, sh, r float64, c color.NRGBA, alpha uint8, blur float64) *image.NRGBA {
	rgba := image.NewRGBA(image.Rect(0, 0, w, h))
	dc := gg.NewContextForRGBA(rgba)
	dc.SetColor(color.NRGBA{c.R, c.G, c.B, alpha})
	drawStripPath(dc, sx1, sy1, sw, sh, r)
	dc.Fill()
	nrgba := rgbaToNRGBA(rgba)
	if blur > 0 {
		return gaussianBlurNRGBA(nrgba, blur)
	}
	return nrgba
}

func stripMask(w, h int, sx1, sy1, sw, sh, r float64) *image.Alpha {
	rgba := image.NewRGBA(image.Rect(0, 0, w, h))
	dc := gg.NewContextForRGBA(rgba)
	dc.SetColor(color.White)
	drawStripPath(dc, sx1, sy1, sw, sh, r)
	dc.Fill()
	mask := image.NewAlpha(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			mask.Pix[y*mask.Stride+x] = rgba.Pix[y*rgba.Stride+x*4+3]
		}
	}
	return mask
}

// drawNeuStrip draws a single strip and calls face.Draw on its interior.
func drawNeuStrip(canvas *image.RGBA, x1, y1, w, h float64, depth ButtonDepth, params NeuParams, face RectangularFace) {
	r := stripCornerR

	switch depth {
	case Raised:
		drawRaisedStrip(canvas, x1, y1, w, h, r, params.Raised)
	case Flat:
		drawFlushStrip(canvas, x1, y1, w, h, r, params.Flush)
	case Depressed:
		drawDepressedStrip(canvas, x1, y1, w, h, r, params.Inset)
	}

	var inset float64
	switch depth {
	case Raised:
		inset = params.Raised.DarkOff * 0.5
	case Flat:
		inset = params.Flush.EdgeW + 1
	case Depressed:
		inset = params.Inset.Off + params.Inset.DarkBlur*0.5
	}

	if face != nil {
		face.Draw(canvas,
			x1+inset, y1+inset,
			w-2*inset, h-2*inset)
	}
}

func drawRaisedStrip(canvas *image.RGBA, x1, y1, w, h, r float64, p RaisedParams) {
	maxOff := math.Max(p.DarkOff, p.LightOff)
	maxBlur := math.Max(p.DarkBlur, p.LightBlur)
	pad := maxOff + math.Ceil(maxBlur*3) + 2
	lw, lh, ox, oy := localRect(canvas, x1, y1, x1+w, y1+h, pad)
	lx1, ly1 := x1-ox, y1-oy
	dst := image.Rect(int(ox), int(oy), int(ox)+lw, int(oy)+lh)

	dark := stripShadowLayer(lw, lh,
		lx1+p.DarkOff, ly1+p.DarkOff, w, h, r,
		darkSh, p.DarkAlpha, p.DarkBlur)
	draw.Draw(canvas, dst, dark, image.Point{}, draw.Over)

	light := stripShadowLayer(lw, lh,
		lx1-p.LightOff, ly1-p.LightOff, w, h, r,
		lightSh, p.LightAlpha, p.LightBlur)
	draw.Draw(canvas, dst, light, image.Point{}, draw.Over)

	dc := gg.NewContextForRGBA(canvas)
	dc.SetColor(surface)
	drawStripPath(dc, x1, y1, w, h, r)
	dc.Fill()
}

func drawFlushStrip(canvas *image.RGBA, x1, y1, w, h, r float64, p FlushParams) {
	dc := gg.NewContextForRGBA(canvas)
	dc.SetColor(surface)
	drawStripPath(dc, x1, y1, w, h, r)
	dc.Fill()

	pad := p.EdgeW + 2
	lw, lh, ox, oy := localRect(canvas, x1, y1, x1+w, y1+h, pad)
	lx1, ly1 := x1-ox, y1-oy
	dst := image.Rect(int(ox), int(oy), int(ox)+lw, int(oy)+lh)

	// Dark edge.
	darkEdge := image.NewRGBA(image.Rect(0, 0, lw, lh))
	ddc := gg.NewContextForRGBA(darkEdge)
	ddc.SetColor(color.NRGBA{darkSh.R, darkSh.G, darkSh.B, p.EdgeAlpha})
	ddc.SetLineWidth(p.EdgeW)
	drawStripPath(ddc, lx1, ly1, w, h, r)
	ddc.Stroke()
	draw.Draw(canvas, dst, darkEdge, image.Point{}, draw.Over)

	// Light edge offset +1.
	lightEdge := image.NewRGBA(image.Rect(0, 0, lw, lh))
	ldc := gg.NewContextForRGBA(lightEdge)
	ldc.SetColor(color.NRGBA{lightSh.R, lightSh.G, lightSh.B, p.EdgeAlpha})
	ldc.SetLineWidth(p.EdgeW)
	drawStripPath(ldc, lx1+1, ly1+1, w, h, r)
	ldc.Stroke()
	mask := stripMask(lw, lh, lx1, ly1, w, h, r)
	draw.DrawMask(canvas, dst, lightEdge, image.Point{}, mask, image.Point{}, draw.Over)
}

func drawDepressedStrip(canvas *image.RGBA, x1, y1, w, h, r float64, p InsetParams) {
	dc := gg.NewContextForRGBA(canvas)
	dc.SetColor(surface)
	drawStripPath(dc, x1, y1, w, h, r)
	dc.Fill()

	maxBlur := math.Max(p.DarkBlur, p.LightBlur)
	pad := p.Off + math.Ceil(maxBlur*3) + 2
	lw, lh, ox, oy := localRect(canvas, x1, y1, x1+w, y1+h, pad)
	lx1, ly1 := x1-ox, y1-oy
	dst := image.Rect(int(ox), int(oy), int(ox)+lw, int(oy)+lh)

	mask := stripMask(lw, lh, lx1, ly1, w, h, r)

	darkBuf := stripShadowLayer(lw, lh,
		lx1-p.Off, ly1-p.Off, w, h, r,
		darkSh, 190, p.DarkBlur)
	draw.DrawMask(canvas, dst, darkBuf, image.Point{}, mask, image.Point{}, draw.Over)

	lightBuf := stripShadowLayer(lw, lh,
		lx1+p.Off, ly1+p.Off, w, h, r,
		lightSh, 190, p.LightBlur)
	draw.DrawMask(canvas, dst, lightBuf, image.Point{}, mask, image.Point{}, draw.Over)
}

// drawSelectedStrip draws a depressed strip with a colored tint overlay,
// matching the radial menu's selected-segment treatment.
func drawSelectedStrip(canvas *image.RGBA, x1, y1, w, h float64, params NeuParams) {
	r := stripCornerR
	p := params.Inset

	// Surface fill.
	dc := gg.NewContextForRGBA(canvas)
	dc.SetColor(surface)
	drawStripPath(dc, x1, y1, w, h, r)
	dc.Fill()

	// Tint overlay masked to strip shape.
	pad := p.Off + math.Ceil(math.Max(p.DarkBlur, p.LightBlur)*3) + 2
	lw, lh, ox, oy := localRect(canvas, x1, y1, x1+w, y1+h, pad)
	lx1, ly1 := x1-ox, y1-oy
	dst := image.Rect(int(ox), int(oy), int(ox)+lw, int(oy)+lh)

	mask := stripMask(lw, lh, lx1, ly1, w, h, r)

	tintBuf := image.NewRGBA(image.Rect(0, 0, lw, lh))
	for ty := 0; ty < lh; ty++ {
		for tx := 0; tx < lw; tx++ {
			off := ty*tintBuf.Stride + tx*4
			tintBuf.Pix[off] = selectedBg.R
			tintBuf.Pix[off+1] = selectedBg.G
			tintBuf.Pix[off+2] = selectedBg.B
			tintBuf.Pix[off+3] = selectedBg.A
		}
	}
	draw.DrawMask(canvas, dst, tintBuf, image.Point{}, mask, image.Point{}, draw.Over)

	// Inset shadows masked to strip shape.
	darkBuf := stripShadowLayer(lw, lh,
		lx1-p.Off, ly1-p.Off, w, h, r,
		darkSh, 180, p.DarkBlur)
	draw.DrawMask(canvas, dst, darkBuf, image.Point{}, mask, image.Point{}, draw.Over)

	lightBuf := stripShadowLayer(lw, lh,
		lx1+p.Off, ly1+p.Off, w, h, r,
		lightSh, 180, p.LightBlur)
	draw.DrawMask(canvas, dst, lightBuf, image.Point{}, mask, image.Point{}, draw.Over)
}

// drawNOfMChooser draws a horizontal sequence of strips. The selected set
// is highlighted with tint + inset shadows; the rest are flat.
func drawNOfMChooser(canvas *image.RGBA, x, y, stripW, stripH float64, faces []RectangularFace, selected []bool, params NeuParams) {
	for i, face := range faces {
		sx := x + float64(i)*stripW
		if selected[i] {
			drawSelectedStrip(canvas, sx, y, stripW, stripH, params)
			if face != nil {
				inset := params.Inset.Off + params.Inset.DarkBlur*0.5
				face.Draw(canvas, sx+inset, y+inset, stripW-2*inset, stripH-2*inset)
			}
		} else {
			drawNeuStrip(canvas, sx, y, stripW, stripH, Flat, params, face)
		}
	}
}

// ── RadialNOfMChooser ──────────────────────────────────────────────────────

// drawArcPath traces a partial annulus outline: outer arc from startAngle to
// endAngle, line to inner radius, inner arc back, close.
func drawArcPath(dc *gg.Context, cx, cy, rOuter, rInner, startAngle, endAngle float64) {
	steps := 64
	for i := 0; i <= steps; i++ {
		t := float64(i) / float64(steps)
		a := startAngle + t*(endAngle-startAngle)
		x, y := cx+rOuter*math.Cos(a), cy+rOuter*math.Sin(a)
		if i == 0 {
			dc.MoveTo(x, y)
		} else {
			dc.LineTo(x, y)
		}
	}
	for i := steps; i >= 0; i-- {
		t := float64(i) / float64(steps)
		a := startAngle + t*(endAngle-startAngle)
		dc.LineTo(cx+rInner*math.Cos(a), cy+rInner*math.Sin(a))
	}
	dc.ClosePath()
}

func arcSegmentMask(w, h int, cx, cy, rOuter, rInner, startAngle, endAngle float64) *image.Alpha {
	rgba := image.NewRGBA(image.Rect(0, 0, w, h))
	dc := gg.NewContextForRGBA(rgba)
	dc.SetColor(color.White)
	drawArcPath(dc, cx, cy, rOuter, rInner, startAngle, endAngle)
	dc.Fill()
	mask := image.NewAlpha(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			mask.Pix[y*mask.Stride+x] = rgba.Pix[y*rgba.Stride+x*4+3]
		}
	}
	return mask
}

func arcShadowLayer(w, h int, cx, cy, rOuter, rInner, startAngle, endAngle float64, c color.NRGBA, alpha uint8, blur float64) *image.NRGBA {
	rgba := image.NewRGBA(image.Rect(0, 0, w, h))
	dc := gg.NewContextForRGBA(rgba)
	dc.SetColor(color.NRGBA{c.R, c.G, c.B, alpha})
	drawArcPath(dc, cx, cy, rOuter, rInner, startAngle, endAngle)
	dc.Fill()
	nrgba := rgbaToNRGBA(rgba)
	if blur > 0 {
		return gaussianBlurNRGBA(nrgba, blur)
	}
	return nrgba
}

// rotateNRGBA rotates an image by angle (radians) around its center using
// bilinear interpolation.
func rotateNRGBA(src *image.NRGBA, angle float64) *image.NRGBA {
	sw, sh := src.Bounds().Dx(), src.Bounds().Dy()
	scx, scy := float64(sw)/2, float64(sh)/2
	absCos, absSin := math.Abs(math.Cos(angle)), math.Abs(math.Sin(angle))
	dw := int(math.Ceil(float64(sw)*absCos+float64(sh)*absSin)) + 2
	dh := int(math.Ceil(float64(sw)*absSin+float64(sh)*absCos)) + 2
	dcx, dcy := float64(dw)/2, float64(dh)/2
	dst := image.NewNRGBA(image.Rect(0, 0, dw, dh))
	cosNeg, sinNeg := math.Cos(-angle), math.Sin(-angle)
	for y := 0; y < dh; y++ {
		for x := 0; x < dw; x++ {
			dx, dy := float64(x)-dcx, float64(y)-dcy
			sx := dx*cosNeg - dy*sinNeg + scx
			sy := dx*sinNeg + dy*cosNeg + scy
			sx0, sy0 := int(math.Floor(sx)), int(math.Floor(sy))
			if sx0 < 0 || sx0+1 >= sw || sy0 < 0 || sy0+1 >= sh {
				continue
			}
			fx, fy := sx-float64(sx0), sy-float64(sy0)
			for c := 0; c < 4; c++ {
				v00 := float64(src.Pix[sy0*src.Stride+sx0*4+c])
				v10 := float64(src.Pix[sy0*src.Stride+(sx0+1)*4+c])
				v01 := float64(src.Pix[(sy0+1)*src.Stride+sx0*4+c])
				v11 := float64(src.Pix[(sy0+1)*src.Stride+(sx0+1)*4+c])
				v := v00*(1-fx)*(1-fy) + v10*fx*(1-fy) + v01*(1-fx)*fy + v11*fx*fy
				dst.Pix[y*dst.Stride+x*4+c] = clampU8(v)
			}
		}
	}
	return dst
}

// drawRadialNOfMChooser draws an arc-shaped N-of-M chooser.
// startDeg/endDeg are in degrees (screen coords: 0=right, 90=down).
func drawRadialNOfMChooser(canvas *image.RGBA, cx, cy, rInner, rOuter, startDeg, endDeg float64,
	faces []RectangularFace, selected []bool, params NeuParams) {

	startRad := startDeg * math.Pi / 180
	endRad := endDeg * math.Pi / 180
	n := len(faces)
	segAngle := (endRad - startRad) / float64(n)

	bounds := canvas.Bounds()
	w, h := bounds.Dx(), bounds.Dy()

	// 1. Flush partial annulus background.
	dc := gg.NewContextForRGBA(canvas)
	dc.SetColor(surface)
	drawArcPath(dc, cx, cy, rOuter, rInner, startRad, endRad)
	dc.Fill()

	fp := params.Flush
	darkEdge := image.NewRGBA(image.Rect(0, 0, w, h))
	ddc := gg.NewContextForRGBA(darkEdge)
	ddc.SetColor(color.NRGBA{darkSh.R, darkSh.G, darkSh.B, fp.EdgeAlpha})
	ddc.SetLineWidth(fp.EdgeW)
	drawArcPath(ddc, cx, cy, rOuter, rInner, startRad, endRad)
	ddc.Stroke()
	draw.Draw(canvas, bounds, darkEdge, image.Point{}, draw.Over)

	lightEdge := image.NewRGBA(image.Rect(0, 0, w, h))
	ldc := gg.NewContextForRGBA(lightEdge)
	ldc.SetColor(color.NRGBA{lightSh.R, lightSh.G, lightSh.B, fp.EdgeAlpha})
	ldc.SetLineWidth(fp.EdgeW)
	drawArcPath(ldc, cx+1, cy+1, rOuter, rInner, startRad, endRad)
	ldc.Stroke()
	fullMask := arcSegmentMask(w, h, cx, cy, rOuter, rInner, startRad, endRad)
	draw.DrawMask(canvas, bounds, lightEdge, image.Point{}, fullMask, image.Point{}, draw.Over)

	// 2. Grooves between segments.
	grooveOff := 0.8
	lineWidth := 1.5
	for i := 1; i < n; i++ {
		angle := startRad + float64(i)*segAngle
		cos, sin := math.Cos(angle), math.Sin(angle)

		darkG := image.NewRGBA(image.Rect(0, 0, w, h))
		gdc := gg.NewContextForRGBA(darkG)
		gdc.SetColor(color.NRGBA{darkSh.R, darkSh.G, darkSh.B, 150})
		gdc.SetLineWidth(lineWidth)
		gdc.DrawLine(cx+rInner*cos-grooveOff, cy+rInner*sin-grooveOff,
			cx+rOuter*cos-grooveOff, cy+rOuter*sin-grooveOff)
		gdc.Stroke()
		draw.DrawMask(canvas, bounds, gaussianBlurNRGBA(rgbaToNRGBA(darkG), 1.5),
			image.Point{}, fullMask, image.Point{}, draw.Over)

		lightG := image.NewRGBA(image.Rect(0, 0, w, h))
		lgdc := gg.NewContextForRGBA(lightG)
		lgdc.SetColor(color.NRGBA{lightSh.R, lightSh.G, lightSh.B, 150})
		lgdc.SetLineWidth(lineWidth)
		lgdc.DrawLine(cx+rInner*cos+grooveOff, cy+rInner*sin+grooveOff,
			cx+rOuter*cos+grooveOff, cy+rOuter*sin+grooveOff)
		lgdc.Stroke()
		draw.DrawMask(canvas, bounds, gaussianBlurNRGBA(rgbaToNRGBA(lightG), 1.0),
			image.Point{}, fullMask, image.Point{}, draw.Over)
	}

	// 3. Selected segments: tint + inset shadows.
	ip := params.Inset
	for i, sel := range selected {
		if !sel {
			continue
		}
		ts := startRad + float64(i)*segAngle
		te := ts + segAngle
		mask := arcSegmentMask(w, h, cx, cy, rOuter, rInner, ts, te)

		tintBuf := image.NewRGBA(image.Rect(0, 0, w, h))
		for ty := 0; ty < h; ty++ {
			for tx := 0; tx < w; tx++ {
				off := ty*tintBuf.Stride + tx*4
				tintBuf.Pix[off] = selectedBg.R
				tintBuf.Pix[off+1] = selectedBg.G
				tintBuf.Pix[off+2] = selectedBg.B
				tintBuf.Pix[off+3] = selectedBg.A
			}
		}
		draw.DrawMask(canvas, bounds, tintBuf, image.Point{}, mask, image.Point{}, draw.Over)

		darkBuf := arcShadowLayer(w, h, cx-ip.Off, cy-ip.Off, rOuter, rInner, ts, te,
			darkSh, 180, ip.DarkBlur)
		draw.DrawMask(canvas, bounds, darkBuf, image.Point{}, mask, image.Point{}, draw.Over)
		lightBuf := arcShadowLayer(w, h, cx+ip.Off, cy+ip.Off, rOuter, rInner, ts, te,
			lightSh, 180, ip.LightBlur)
		draw.DrawMask(canvas, bounds, lightBuf, image.Point{}, mask, image.Point{}, draw.Over)
	}

	// 4. Rotated face content.
	grooveMargin := 3.0
	midR := (rInner + rOuter) / 2
	grooveAngular := grooveMargin / midR
	insetMargin := 5.0
	insetAngular := insetMargin / midR
	for i, face := range faces {
		if face == nil {
			continue
		}
		ts := startRad + float64(i)*segAngle + grooveAngular
		te := startRad + float64(i+1)*segAngle - grooveAngular
		rIn := rInner + grooveMargin
		rOut := rOuter - grooveMargin
		if selected[i] {
			ts += insetAngular
			te -= insetAngular
			rIn += insetMargin
			rOut -= insetMargin
		}

		// Text baseline is a radial line at baseAngle, pointing toward center.
		baseAngle := (ts + te) / 2
		midFaceR := (rIn + rOut) / 2
		arcLen := (te - ts) * midFaceR
		radialH := rOut - rIn

		// Render face into temp buffer with the text spanning the radial
		// distance (width = radialH) and tangential space as height.
		pad := 4
		tw := int(math.Ceil(radialH)) + 2*pad
		th := int(math.Ceil(arcLen)) + 2*pad
		if tw < 1 {
			tw = 1
		}
		if th < 1 {
			th = 1
		}
		tmp := image.NewRGBA(image.Rect(0, 0, tw, th))
		face.Draw(tmp, float64(pad), float64(pad), radialH, arcLen)

		// Rotate so text reads outward from center. For angles where the
		// text would be upside-down (90°–270°), flip by adding π.
		rotation := baseAngle
		if baseAngle > math.Pi/2 && baseAngle < 3*math.Pi/2 {
			rotation = baseAngle + math.Pi
		}
		rotated := rotateNRGBA(rgbaToNRGBA(tmp), rotation)

		// Composite at the baseline angle position.
		fx := cx + midFaceR*math.Cos(baseAngle)
		fy := cy + midFaceR*math.Sin(baseAngle)
		rw, rh := rotated.Bounds().Dx(), rotated.Bounds().Dy()
		ox, oy := int(math.Round(fx))-rw/2, int(math.Round(fy))-rh/2
		draw.Draw(canvas, image.Rect(ox, oy, ox+rw, oy+rh), rotated, image.Point{}, draw.Over)
	}
}

// ── Button drawing ──────────────────────────────────────────────────────────

const buttonCornerR = 8.0

// drawNeuButton draws a neumorphic button and calls face.Draw with the
// interior rectangle that remains after the neumorphic sides.
func drawNeuButton(canvas *image.RGBA, x1, y1, x2, y2 float64, depth ButtonDepth, params NeuParams, face RectangularFace) {
	switch depth {
	case Raised:
		drawRaisedButton(canvas, x1, y1, x2, y2, params.Raised)
	case Flat:
		drawFlushButton(canvas, x1, y1, x2, y2, params.Flush)
	case Depressed:
		drawDepressedButton(canvas, x1, y1, x2, y2, params.Inset)
	}

	// Compute the drawable face area. The neumorphic effect eats into
	// the visual edges, so we inset the face rectangle.
	var inset float64
	switch depth {
	case Raised:
		inset = params.Raised.DarkOff * 0.5
	case Flat:
		inset = params.Flush.EdgeW + 1
	case Depressed:
		inset = params.Inset.Off + params.Inset.DarkBlur*0.5
	}

	if face != nil {
		face.Draw(canvas,
			x1+inset, y1+inset,
			(x2-x1)-2*inset, (y2-y1)-2*inset)
	}
}

func drawRaisedButton(canvas *image.RGBA, x1, y1, x2, y2 float64, p RaisedParams) {
	r := buttonCornerR

	maxOff := math.Max(p.DarkOff, p.LightOff)
	maxBlur := math.Max(p.DarkBlur, p.LightBlur)
	pad := maxOff + math.Ceil(maxBlur*3) + 2
	lw, lh, ox, oy := localRect(canvas, x1, y1, x2, y2, pad)
	lx1, ly1 := x1-ox, y1-oy
	lx2, ly2 := x2-ox, y2-oy
	dst := image.Rect(int(ox), int(oy), int(ox)+lw, int(oy)+lh)

	dark := shadowLayer(lw, lh,
		lx1+p.DarkOff, ly1+p.DarkOff, lx2+p.DarkOff, ly2+p.DarkOff,
		r, darkSh, p.DarkAlpha, p.DarkBlur)
	draw.Draw(canvas, dst, dark, image.Point{}, draw.Over)

	light := shadowLayer(lw, lh,
		lx1-p.LightOff, ly1-p.LightOff, lx2-p.LightOff, ly2-p.LightOff,
		r, lightSh, p.LightAlpha, p.LightBlur)
	draw.Draw(canvas, dst, light, image.Point{}, draw.Over)

	dc := gg.NewContextForRGBA(canvas)
	dc.SetColor(surface)
	dc.DrawRoundedRectangle(x1, y1, x2-x1, y2-y1, r)
	dc.Fill()
}

func drawFlushButton(canvas *image.RGBA, x1, y1, x2, y2 float64, p FlushParams) {
	r := buttonCornerR

	// Surface fill.
	dc := gg.NewContextForRGBA(canvas)
	dc.SetColor(surface)
	dc.DrawRoundedRectangle(x1, y1, x2-x1, y2-y1, r)
	dc.Fill()

	pad := p.EdgeW + 2
	lw, lh, ox, oy := localRect(canvas, x1, y1, x2, y2, pad)
	lx1, ly1 := x1-ox, y1-oy
	lx2, ly2 := x2-ox, y2-oy
	dst := image.Rect(int(ox), int(oy), int(ox)+lw, int(oy)+lh)

	// Dark edge (top-left bias).
	darkEdge := image.NewRGBA(image.Rect(0, 0, lw, lh))
	ddc := gg.NewContextForRGBA(darkEdge)
	ddc.SetColor(color.NRGBA{darkSh.R, darkSh.G, darkSh.B, p.EdgeAlpha})
	ddc.SetLineWidth(p.EdgeW)
	ddc.DrawRoundedRectangle(lx1, ly1, lx2-lx1, ly2-ly1, r)
	ddc.Stroke()
	draw.Draw(canvas, dst, darkEdge, image.Point{}, draw.Over)

	// Light edge (bottom-right bias, offset +1).
	lightEdge := image.NewRGBA(image.Rect(0, 0, lw, lh))
	ldc := gg.NewContextForRGBA(lightEdge)
	ldc.SetColor(color.NRGBA{lightSh.R, lightSh.G, lightSh.B, p.EdgeAlpha})
	ldc.SetLineWidth(p.EdgeW)
	ldc.DrawRoundedRectangle(lx1+1, ly1+1, lx2-lx1, ly2-ly1, r)
	ldc.Stroke()
	mask := roundedRectMask(lw, lh, lx1, ly1, lx2, ly2, r)
	draw.DrawMask(canvas, dst, lightEdge, image.Point{}, mask, image.Point{}, draw.Over)
}

func drawDepressedButton(canvas *image.RGBA, x1, y1, x2, y2 float64, p InsetParams) {
	r := buttonCornerR

	// Surface fill.
	dc := gg.NewContextForRGBA(canvas)
	dc.SetColor(surface)
	dc.DrawRoundedRectangle(x1, y1, x2-x1, y2-y1, r)
	dc.Fill()

	maxBlur := math.Max(p.DarkBlur, p.LightBlur)
	pad := p.Off + math.Ceil(maxBlur*3) + 2
	lw, lh, ox, oy := localRect(canvas, x1, y1, x2, y2, pad)
	lx1, ly1 := x1-ox, y1-oy
	lx2, ly2 := x2-ox, y2-oy
	dst := image.Rect(int(ox), int(oy), int(ox)+lw, int(oy)+lh)

	mask := roundedRectMask(lw, lh, lx1, ly1, lx2, ly2, r)

	// Dark shadow — upper-left (alpha 190, matching neuInset in draw.go).
	darkBuf := shadowLayer(lw, lh,
		lx1-p.Off, ly1-p.Off, lx2-p.Off, ly2-p.Off,
		r, darkSh, 190, p.DarkBlur)
	draw.DrawMask(canvas, dst, darkBuf, image.Point{}, mask, image.Point{}, draw.Over)

	// Light shadow — lower-right.
	lightBuf := shadowLayer(lw, lh,
		lx1+p.Off, ly1+p.Off, lx2+p.Off, ly2+p.Off,
		r, lightSh, 190, p.LightBlur)
	draw.DrawMask(canvas, dst, lightBuf, image.Point{}, mask, image.Point{}, draw.Over)
}

// ── Main ────────────────────────────────────────────────────────────────────

func main() {
	buttonW := 160.0
	buttonH := 50.0
	margin := 40.0
	gap := 40.0
	labelGap := 8.0

	// Row 1: three buttons.
	row1W := 3*buttonW + 2*gap
	// Row 2: two bare checkboxes.
	checkSize := 28.0
	checkGap := 60.0
	row2W := 2*checkSize + checkGap
	// Row 3: four labeled checkboxes in a 2x2 grid.
	labelFaceW := 100.0 // space for the label face
	labelFaceH := 24.0
	cbLabelGap := 6.0   // gap between checkbox and its label face
	// Cell sizes for the 2x2 grid — enough for the largest arrangement (Top/Bottom).
	cellW := math.Max(labelFaceW, checkSize) + 20
	cellH := checkSize + cbLabelGap + labelFaceH + 10
	cellGap := 40.0

	// Row 4: two NOfMChoosers side by side.
	stripW := 40.0
	stripH := 88.0
	nStrips := 4
	chooserW := float64(nStrips) * stripW
	chooserGap := 50.0
	row4W := 2*chooserW + chooserGap
	row4H := stripH

	// Row 5: RadialNOfMChooser.
	radCellW, radCellH := 260, 200
	radGap := 30.0
	row5W := float64(2*radCellW) + radGap
	row5H := float64(radCellH)

	// Row 6: orange rectangle with radial chooser on bottom-right corner.
	orangeRectW, orangeRectH := 300.0, 300.0
	row6RI, row6RO := 60.0, 140.0
	row6W := orangeRectW + 2*row6RO  // extra space for arcs fanning left and right
	row6H := orangeRectH + 2*row6RO  // extra space for arcs fanning up and down

	contentW := math.Max(row1W, math.Max(row2W, math.Max(2*cellW+cellGap, math.Max(row4W, math.Max(row5W, row6W)))))
	totalW := int(math.Ceil(contentW + 2*margin))

	row1H := buttonH + labelGap + 20
	rowGap := 30.0
	row2H := checkSize + labelGap + 20
	row3H := 2*cellH + cellGap
	totalH := int(math.Ceil(margin + row1H + rowGap + row2H + rowGap + row3H + rowGap + row4H + rowGap + row5H + rowGap + row6H + margin))

	canvas := image.NewRGBA(image.Rect(0, 0, totalW, totalH))
	fillSurface(canvas)

	// ── Row 1: buttons ──
	depths := []ButtonDepth{Raised, Flat, Depressed}
	labels := []string{"Raised", "Flat", "Depressed"}
	faces := []RectangularFace{
		&LabelFace{Text: "OK"},
		&LabelFace{Text: "Cancel"},
		&IconFace{Kind: IconPlay, Color: color.NRGBA{60, 180, 75, 255}},
	}

	btnOriginX := (float64(totalW) - row1W) / 2
	for i, depth := range depths {
		x1 := btnOriginX + float64(i)*(buttonW+gap)
		y1 := margin
		x2 := x1 + buttonW
		y2 := y1 + buttonH

		drawNeuButton(canvas, x1, y1, x2, y2, depth, NeuButtonParams, faces[i])

		dc := gg.NewContextForRGBA(canvas)
		if err := dc.LoadFontFace(fontPath, 14); err != nil {
			panic(err)
		}
		dc.SetColor(textCol)
		dc.DrawStringAnchored(labels[i], (x1+x2)/2, y2+labelGap+12, 0.5, 0.5)
	}

	// ── Row 2: bare checkboxes ──
	row2Y := margin + row1H + rowGap
	checkCY := row2Y + checkSize/2

	checkOriginX := (float64(totalW) - row2W) / 2
	checkLabels := []string{"Unchecked", "Checked"}
	checkStates := []bool{false, true}

	for i, checked := range checkStates {
		cx := checkOriginX + float64(i)*(checkSize+checkGap) + checkSize/2
		drawNeuCheckbox(canvas, cx, checkCY, checkSize, checked, NeuButtonParams)

		dc := gg.NewContextForRGBA(canvas)
		if err := dc.LoadFontFace(fontPath, 14); err != nil {
			panic(err)
		}
		dc.SetColor(textCol)
		dc.DrawStringAnchored(checkLabels[i], cx, checkCY+checkSize/2+labelGap+12, 0.5, 0.5)
	}

	// ── Row 3: labeled checkboxes (2x2 grid) ──
	row3Y := row2Y + row2H + rowGap
	gridW := 2*cellW + cellGap
	gridOriginX := (float64(totalW) - gridW) / 2

	sides := []LabelSide{LabelTop, LabelRight, LabelBottom, LabelLeft}
	sideNames := []string{"Label Top", "Label Right", "Label Bottom", "Label Left"}

	for i, side := range sides {
		col := i % 2
		row := i / 2
		cellX := gridOriginX + float64(col)*(cellW+cellGap)
		cellY := row3Y + float64(row)*(cellH+cellGap)

		// Center the checkbox+label within the cell.
		var comboW, comboH float64
		switch side {
		case LabelTop, LabelBottom:
			comboW = math.Max(checkSize, labelFaceW)
			comboH = checkSize + cbLabelGap + labelFaceH
		case LabelLeft, LabelRight:
			comboW = checkSize + cbLabelGap + labelFaceW
			comboH = math.Max(checkSize, labelFaceH)
		}
		ox := cellX + (cellW-comboW)/2
		oy := cellY + (cellH-comboH)/2

		lbl := &LabelFace{Text: sideNames[i], FontSize: 14}
		drawCheckboxWithLabel(canvas, ox, oy, checkSize, labelFaceW, labelFaceH, cbLabelGap, side, true, NeuButtonParams, lbl)
	}

	// ── Row 4: NOfMChoosers ──
	row4Y := row3Y + row3H + rowGap
	row4OriginX := (float64(totalW) - row4W) / 2

	stripFaces := []RectangularFace{
		&LabelFace{Text: "a", FontSize: 16},
		&LabelFace{Text: "b", FontSize: 16},
		&LabelFace{Text: "c", FontSize: 16},
		&LabelFace{Text: "d", FontSize: 16},
	}

	// Left chooser: only "c" selected.
	drawNOfMChooser(canvas, row4OriginX, row4Y, stripW, stripH,
		stripFaces, []bool{false, false, true, false}, NeuButtonParams)

	// Right chooser: "b" and "c" selected.
	drawNOfMChooser(canvas, row4OriginX+chooserW+chooserGap, row4Y, stripW, stripH,
		stripFaces, []bool{false, true, true, false}, NeuButtonParams)

	// ── Row 5: RadialNOfMChooser ──
	row5Y := row4Y + row4H + rowGap
	row5OriginX := (float64(totalW) - row5W) / 2
	radCX, radCY := float64(radCellW)/2, 25.0
	radRI, radRO := 60.0, 140.0

	radFaces := []RectangularFace{
		&LabelFace{Text: "a", FontSize: 16},
		&LabelFace{Text: "b", FontSize: 16},
		&LabelFace{Text: "c", FontSize: 16},
		&LabelFace{Text: "d", FontSize: 16},
	}

	// Left: only "c" selected.
	cell1 := image.NewRGBA(image.Rect(0, 0, radCellW, radCellH))
	fillSurface(cell1)
	drawRadialNOfMChooser(cell1, radCX, radCY, radRI, radRO, 45, 135,
		radFaces, []bool{false, false, true, false}, NeuButtonParams)
	ox1 := int(math.Round(row5OriginX))
	oy1 := int(math.Round(row5Y))
	draw.Draw(canvas, image.Rect(ox1, oy1, ox1+radCellW, oy1+radCellH),
		cell1, image.Point{}, draw.Over)

	// Right: "No" and "Maybe" selected.
	radFaces2 := []RectangularFace{
		&LabelFace{Text: "Yes", FontSize: 14},
		&LabelFace{Text: "No", FontSize: 14},
		&LabelFace{Text: "Maybe", FontSize: 14},
		&LabelFace{Text: "Never", FontSize: 14},
	}
	cell2 := image.NewRGBA(image.Rect(0, 0, radCellW, radCellH))
	fillSurface(cell2)
	drawRadialNOfMChooser(cell2, radCX, radCY, radRI, radRO, 0, 90,
		radFaces2, []bool{false, true, true, false}, NeuButtonParams)
	ox2 := int(math.Round(row5OriginX)) + radCellW + int(radGap)
	draw.Draw(canvas, image.Rect(ox2, oy1, ox2+radCellW, oy1+radCellH),
		cell2, image.Point{}, draw.Over)

	// ── Row 6: orange rectangle + radial chooser on bottom-right corner ──
	row6Y := row5Y + row5H + rowGap
	row6OriginX := (float64(totalW) - row6W) / 2

	orX := row6OriginX + row6RO
	orY := row6Y + row6RO
	cornerR := 12.0

	// Bottom-left (6 items): draw BEFORE rectangle so rect overlaps Frik/Frak inner parts.
	// 90°–180° extended by 27.5° each side → 62.5°–207.5°
	blFaces := []RectangularFace{
		&LabelFace{Text: "Frik", FontSize: 14},
		&LabelFace{Text: "Yes", FontSize: 14},
		&LabelFace{Text: "No", FontSize: 14},
		&LabelFace{Text: "Maybe", FontSize: 14},
		&LabelFace{Text: "Never", FontSize: 14},
		&LabelFace{Text: "Frak", FontSize: 14},
	}
	drawRadialNOfMChooser(canvas,
		orX+row6RI, orY+orangeRectH-row6RI,
		row6RI, row6RO, 62.5, 207.5,
		blFaces, []bool{false, true, false, false, true, false}, NeuButtonParams)

	// Draw rounded orange rectangle (after bottom-left so it overlaps Frik/Frak).
	dc6 := gg.NewContextForRGBA(canvas)
	dc6.DrawRoundedRectangle(orX, orY, orangeRectW, orangeRectH, cornerR)
	dc6.SetColor(color.NRGBA{255, 165, 50, 255})
	dc6.Fill()


	outPath := "tools/visuals/button/output.png"
	f, err := os.Create(outPath)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	if err := png.Encode(f, canvas); err != nil {
		panic(err)
	}

	exec.Command("open", outPath).Run()

	// ── output2.png: 800x600 orange rectangle with bottom-left chooser ──
	o2RectW, o2RectH := 800.0, 600.0
	o2RI, o2RO := 60.0, 182.0
	o2Margin := o2RO + 20
	o2W := int(math.Ceil(o2RectW + 2*o2Margin))
	o2H := int(math.Ceil(o2RectH + 2*o2Margin))
	canvas2 := image.NewRGBA(image.Rect(0, 0, o2W, o2H))
	fillSurface(canvas2)

	o2X := o2Margin
	o2Y := o2Margin

	// Bottom-left chooser drawn first so rectangle overlaps Frik/Frak.
	o2Faces := []RectangularFace{
		&LabelFace{Text: "Frik", FontSize: 24},
		&LabelFace{Text: "Yes", FontSize: 24},
		&LabelFace{Text: "No", FontSize: 24},
		&LabelFace{Text: "Maybe", FontSize: 24},
		&LabelFace{Text: "Never", FontSize: 24},
		&LabelFace{Text: "Frak", FontSize: 24},
	}
	drawRadialNOfMChooser(canvas2,
		o2X+o2RI, o2Y+o2RectH-o2RI,
		o2RI, o2RO, 62.5, 207.5,
		o2Faces, []bool{false, true, false, false, true, false}, NeuButtonParams)

	// Orange rectangle.
	dc7 := gg.NewContextForRGBA(canvas2)
	dc7.DrawRoundedRectangle(o2X, o2Y, o2RectW, o2RectH, 12)
	dc7.SetColor(color.NRGBA{255, 165, 50, 255})
	dc7.Fill()

	outPath2 := "tools/visuals/button/output2.png"
	f2, err := os.Create(outPath2)
	if err != nil {
		panic(err)
	}
	defer f2.Close()
	if err := png.Encode(f2, canvas2); err != nil {
		panic(err)
	}
	exec.Command("open", outPath2).Run()
}

// ── Helpers (copied from scrollbar_track.go) ────────────────────────────────

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

func shadowLayer(w, h int, x1, y1, x2, y2, r float64, c color.NRGBA, alpha uint8, blur float64) *image.NRGBA {
	rgba := image.NewRGBA(image.Rect(0, 0, w, h))
	dc := gg.NewContextForRGBA(rgba)
	dc.SetColor(color.NRGBA{c.R, c.G, c.B, alpha})
	dc.DrawRoundedRectangle(x1, y1, x2-x1, y2-y1, r)
	dc.Fill()
	nrgba := rgbaToNRGBA(rgba)
	if blur > 0 {
		return gaussianBlurNRGBA(nrgba, blur)
	}
	return nrgba
}

func roundedRectMask(w, h int, x1, y1, x2, y2, r float64) *image.Alpha {
	rgba := image.NewRGBA(image.Rect(0, 0, w, h))
	dc := gg.NewContextForRGBA(rgba)
	dc.SetColor(color.White)
	dc.DrawRoundedRectangle(x1, y1, x2-x1, y2-y1, r)
	dc.Fill()
	mask := image.NewAlpha(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			mask.Pix[y*mask.Stride+x] = rgba.Pix[y*rgba.Stride+x*4+3]
		}
	}
	return mask
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
