package main

import (
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"os"

	"github.com/fogleman/gg"
)

// Neumorphic palette — matches mancini DefaultPalette.
var (
	surface  = color.NRGBA{232, 230, 244, 255}
	darkSh   = color.NRGBA{176, 173, 195, 255}
	lightSh  = color.NRGBA{255, 255, 255, 255}
	textCol  = color.NRGBA{78, 72, 112, 255}
)

const fontPath = "/Users/iansmith/louis14/fonts/AtkinsonHyperlegible-Regular.ttf"

// Mild inset parameters for a thin scrollbar track.
var trackInset = struct {
	Off                 float64
	DarkBlur, LightBlur float64
	DarkAlpha           uint8
	LightAlpha          uint8
}{
	Off:        1.5,
	DarkBlur:   3,
	LightBlur:  2,
	DarkAlpha:  140,
	LightAlpha: 140,
}

// Mild raised parameters for the thumb.
var thumbRaised = struct {
	LightOff, LightBlur   float64
	DarkOff, DarkBlur     float64
	DarkAlpha, LightAlpha uint8
}{
	LightOff:   1.5,
	LightBlur:  3,
	DarkOff:    4,
	DarkBlur:   4,
	DarkAlpha:  100,
	LightAlpha: 220,
}

// Mild raised parameters for the grip dot and arrows.
var dotRaised = struct {
	LightOff, LightBlur   float64
	DarkOff, DarkBlur     float64
	DarkAlpha, LightAlpha uint8
}{
	LightOff:   1,
	LightBlur:  1.5,
	DarkOff:    2,
	DarkBlur:   2,
	DarkAlpha:  120,
	LightAlpha: 200,
}

const (
	// Default cross-axis thickness of the scrollbar (used when trackWidth < 5).
	defaultTrackThick = 22.5

	minTrackLen  = 160.0 // minimum scrollbar length
	largeThumbAt = 250.0 // length at which we switch to larger thumb

	smallThumbLen = 60.0  // thumb for narrow scrollbars
	largeThumbLen = 100.0 // thumb for wider scrollbars

	margin = 30.0 // canvas margin around the scrollbar
)

func main() {
	// Four scrollbars: horiz+vert at trackWidth=20, horiz+vert at trackWidth=5 (default).
	h20 := drawScrollbar(false, 250, true, true, 20)
	v20 := drawScrollbar(true, 350, false, false, 20)
	h5 := drawScrollbar(false, 250, true, true, 5)
	v5 := drawScrollbar(true, 350, false, false, 5)

	gap := 20

	// Layout: two rows.
	// Row 1: h20 | v20
	// Row 2: h5  | v5
	row1W := h20.Bounds().Dx() + gap + v20.Bounds().Dx()
	row2W := h5.Bounds().Dx() + gap + v5.Bounds().Dx()
	totalW := row1W
	if row2W > totalW {
		totalW = row2W
	}
	row1H := v20.Bounds().Dy()
	if h20.Bounds().Dy() > row1H {
		row1H = h20.Bounds().Dy()
	}
	row2H := v5.Bounds().Dy()
	if h5.Bounds().Dy() > row2H {
		row2H = h5.Bounds().Dy()
	}
	totalH := row1H + gap + row2H

	canvas := image.NewRGBA(image.Rect(0, 0, totalW, totalH))
	for y := 0; y < totalH; y++ {
		for x := 0; x < totalW; x++ {
			off := y*canvas.Stride + x*4
			canvas.Pix[off] = surface.R
			canvas.Pix[off+1] = surface.G
			canvas.Pix[off+2] = surface.B
			canvas.Pix[off+3] = surface.A
		}
	}

	// Row 1: trackWidth=20.
	h20Y := (row1H - h20.Bounds().Dy()) / 2
	draw.Draw(canvas, image.Rect(0, h20Y, h20.Bounds().Dx(), h20Y+h20.Bounds().Dy()), h20, image.Point{}, draw.Over)
	v20X := h20.Bounds().Dx() + gap
	draw.Draw(canvas, image.Rect(v20X, 0, v20X+v20.Bounds().Dx(), v20.Bounds().Dy()), v20, image.Point{}, draw.Over)

	// Row 2: trackWidth=5 (uses default 22.5).
	r2Y := row1H + gap
	h5Y := r2Y + (row2H-h5.Bounds().Dy())/2
	draw.Draw(canvas, image.Rect(0, h5Y, h5.Bounds().Dx(), h5Y+h5.Bounds().Dy()), h5, image.Point{}, draw.Over)
	v5X := h5.Bounds().Dx() + gap
	draw.Draw(canvas, image.Rect(v5X, r2Y, v5X+v5.Bounds().Dx(), r2Y+v5.Bounds().Dy()), v5, image.Point{}, draw.Over)

	outDir := "tools/visuals"
	f, err := os.Create(outDir + "/output.png")
	if err != nil {
		panic(err)
	}
	defer f.Close()
	if err := png.Encode(f, canvas); err != nil {
		panic(err)
	}
}

// drawScrollbar draws a complete neumorphic scrollbar and returns the canvas.
//   - isVertical: false = horizontal, true = vertical
//   - length: the long-axis size of the scrollbar in pixels
//   - showArrows: draw arrow triangles at each end
//   - showNumbers: draw "10" label near the thumb
//   - trackWidth: cross-axis thickness; values < 5 use defaultTrackThick
func drawScrollbar(isVertical bool, length float64, showArrows, showNumbers bool, trackWidth float64) *image.RGBA {
	// Resolve track thickness.
	thick := trackWidth
	if thick < 5 {
		thick = defaultTrackThick
	}

	// Scale all derived dimensions proportionally to thickness.
	scale := thick / defaultTrackThick
	tR := thick / 2.0                // fully rounded ends
	thumbInset := 4.5 * scale        // gap between track and thumb edges
	tThumbR := 9.0 * scale           // thumb corner radius
	tDotRadius := 3.75 * scale       // grip dot
	tArrowH := 10.5 * scale          // arrow height (cross-axis)
	tArrowW := 7.5 * scale           // arrow base width (main-axis)
	tArrowPad := 12.0 * scale        // arrow inset from track edge
	fontSize := 14.0 * scale         // label font size

	// Canvas dimensions.
	var cw, ch int
	if isVertical {
		cw = int(math.Ceil(thick + 2*margin))
		ch = int(math.Ceil(length + 2*margin))
	} else {
		cw = int(math.Ceil(length + 2*margin))
		ch = int(math.Ceil(thick + 2*margin))
	}
	canvas := image.NewRGBA(image.Rect(0, 0, cw, ch))

	// Fill background with surface color.
	for y := 0; y < ch; y++ {
		for x := 0; x < cw; x++ {
			off := y*canvas.Stride + x*4
			canvas.Pix[off] = surface.R
			canvas.Pix[off+1] = surface.G
			canvas.Pix[off+2] = surface.B
			canvas.Pix[off+3] = surface.A
		}
	}

	// Track bounds (x1,y1)-(x2,y2).
	var tx1, ty1, tx2, ty2 float64
	if isVertical {
		tx1 = (float64(cw) - thick) / 2.0
		ty1 = margin
		tx2 = tx1 + thick
		ty2 = ty1 + length
	} else {
		tx1 = margin
		ty1 = (float64(ch) - thick) / 2.0
		tx2 = tx1 + length
		ty2 = ty1 + thick
	}
	drawInsetTrack(canvas, tx1, ty1, tx2, ty2, tR)

	// Arrows.
	if showArrows {
		if isVertical {
			trackCX := (tx1 + tx2) / 2.0
			drawRaisedArrowVert(canvas, trackCX, ty1+tArrowPad, tArrowW, tArrowH, true)
			drawRaisedArrowVert(canvas, trackCX, ty2-tArrowPad, tArrowW, tArrowH, false)
		} else {
			trackCY := (ty1 + ty2) / 2.0
			drawRaisedArrowHoriz(canvas, tx1+tArrowPad, trackCY, tArrowW, tArrowH, true)
			drawRaisedArrowHoriz(canvas, tx2-tArrowPad, trackCY, tArrowW, tArrowH, false)
		}
	}

	// Thumb size.
	thumbLen := smallThumbLen
	if length >= largeThumbAt {
		thumbLen = largeThumbLen
	}
	thumbThick := thick - thumbInset

	// Thumb bounds, centered in the track.
	var thx1, thy1, thx2, thy2 float64
	if isVertical {
		thx1 = tx1 + (thick-thumbThick)/2.0
		thy1 = ty1 + (length-thumbLen)/2.0
		thx2 = thx1 + thumbThick
		thy2 = thy1 + thumbLen
	} else {
		thx1 = tx1 + (length-thumbLen)/2.0
		thy1 = ty1 + (thick-thumbThick)/2.0
		thx2 = thx1 + thumbLen
		thy2 = thy1 + thumbThick
	}
	drawRaisedThumb(canvas, thx1, thy1, thx2, thy2, tThumbR)

	// Grip dot.
	dotCX := (thx1 + thx2) / 2.0
	dotCY := (thy1 + thy2) / 2.0
	drawRaisedDot(canvas, dotCX, dotCY, tDotRadius)

	// Number label.
	if showNumbers {
		dc := gg.NewContextForRGBA(canvas)
		if err := dc.LoadFontFace(fontPath, int64(math.Round(fontSize))); err != nil {
			panic(err)
		}
		dc.SetColor(textCol)
		if isVertical {
			dc.DrawStringAnchored("10", tx1-6*scale, dotCY, 1.0, 0.35)
		} else {
			dc.DrawStringAnchored("10", dotCX, ty1-4*scale, 0.5, 0)
		}
	}

	return canvas
}

// ── Scrollbar element rendering ─────────────────────────────────────────────

func drawInsetTrack(canvas *image.RGBA, x1, y1, x2, y2, r float64) {
	p := trackInset

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

	darkShadow := shadowLayer(lw, lh,
		lx1-p.Off, ly1-p.Off, lx2-p.Off, ly2-p.Off,
		r, darkSh, p.DarkAlpha, p.DarkBlur)
	draw.DrawMask(canvas, dst, darkShadow, image.Point{}, mask, image.Point{}, draw.Over)

	lightShadow := shadowLayer(lw, lh,
		lx1+p.Off, ly1+p.Off, lx2+p.Off, ly2+p.Off,
		r, lightSh, p.LightAlpha, p.LightBlur)
	draw.DrawMask(canvas, dst, lightShadow, image.Point{}, mask, image.Point{}, draw.Over)
}

// drawRaisedArrowHoriz draws a horizontal arrow (left or right pointing).
func drawRaisedArrowHoriz(canvas *image.RGBA, tipX, tipY, w, h float64, pointLeft bool) {
	var x0, y0, x1, y1t, x2, y2 float64
	if pointLeft {
		x0, y0 = tipX, tipY
		x1, y1t = tipX+w, tipY-h/2
		x2, y2 = tipX+w, tipY+h/2
	} else {
		x0, y0 = tipX, tipY
		x1, y1t = tipX-w, tipY-h/2
		x2, y2 = tipX-w, tipY+h/2
	}
	drawRaisedTriangle(canvas, x0, y0, x1, y1t, x2, y2)
}

// drawRaisedArrowVert draws a vertical arrow (up or down pointing).
func drawRaisedArrowVert(canvas *image.RGBA, tipX, tipY, w, h float64, pointUp bool) {
	var x0, y0, x1, y1t, x2, y2 float64
	if pointUp {
		x0, y0 = tipX, tipY
		x1, y1t = tipX-h/2, tipY+w
		x2, y2 = tipX+h/2, tipY+w
	} else {
		x0, y0 = tipX, tipY
		x1, y1t = tipX-h/2, tipY-w
		x2, y2 = tipX+h/2, tipY-w
	}
	drawRaisedTriangle(canvas, x0, y0, x1, y1t, x2, y2)
}

func drawRaisedTriangle(canvas *image.RGBA, x0, y0, x1, y1, x2, y2 float64) {
	p := dotRaised

	bx1 := math.Min(x0, math.Min(x1, x2))
	by1 := math.Min(y0, math.Min(y1, y2))
	bx2 := math.Max(x0, math.Max(x1, x2))
	by2 := math.Max(y0, math.Max(y1, y2))

	maxOff := math.Max(p.DarkOff, p.LightOff)
	maxBlur := math.Max(p.DarkBlur, p.LightBlur)
	pad := maxOff + math.Ceil(maxBlur*3) + 2
	lw, lh, ox, oy := localRect(canvas, bx1, by1, bx2, by2, pad)
	dst := image.Rect(int(ox), int(oy), int(ox)+lw, int(oy)+lh)

	dark := triangleShadowLayer(lw, lh,
		x0-ox+p.DarkOff, y0-oy+p.DarkOff,
		x1-ox+p.DarkOff, y1-oy+p.DarkOff,
		x2-ox+p.DarkOff, y2-oy+p.DarkOff,
		darkSh, p.DarkAlpha, p.DarkBlur)
	draw.Draw(canvas, dst, dark, image.Point{}, draw.Over)

	light := triangleShadowLayer(lw, lh,
		x0-ox-p.LightOff, y0-oy-p.LightOff,
		x1-ox-p.LightOff, y1-oy-p.LightOff,
		x2-ox-p.LightOff, y2-oy-p.LightOff,
		lightSh, p.LightAlpha, p.LightBlur)
	draw.Draw(canvas, dst, light, image.Point{}, draw.Over)

	dc := gg.NewContextForRGBA(canvas)
	dc.SetColor(surface)
	dc.MoveTo(x0, y0)
	dc.LineTo(x1, y1)
	dc.LineTo(x2, y2)
	dc.ClosePath()
	dc.Fill()
}

func drawRaisedThumb(canvas *image.RGBA, x1, y1, x2, y2, r float64) {
	p := thumbRaised

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

func drawRaisedDot(canvas *image.RGBA, cx, cy, rad float64) {
	p := dotRaised

	x1, y1, x2, y2 := cx-rad, cy-rad, cx+rad, cy+rad
	maxOff := math.Max(p.DarkOff, p.LightOff)
	maxBlur := math.Max(p.DarkBlur, p.LightBlur)
	pad := maxOff + math.Ceil(maxBlur*3) + 2
	lw, lh, ox, oy := localRect(canvas, x1, y1, x2, y2, pad)
	lcx, lcy := cx-ox, cy-oy
	dst := image.Rect(int(ox), int(oy), int(ox)+lw, int(oy)+lh)

	dark := circleShadowLayer(lw, lh, lcx+p.DarkOff, lcy+p.DarkOff, rad,
		darkSh, p.DarkAlpha, p.DarkBlur)
	draw.Draw(canvas, dst, dark, image.Point{}, draw.Over)

	light := circleShadowLayer(lw, lh, lcx-p.LightOff, lcy-p.LightOff, rad,
		lightSh, p.LightAlpha, p.LightBlur)
	draw.Draw(canvas, dst, light, image.Point{}, draw.Over)

	dc := gg.NewContextForRGBA(canvas)
	dc.SetColor(surface)
	dc.DrawCircle(cx, cy, rad)
	dc.Fill()
}

// ── Helpers (extracted from mancini/std/draw.go, simplified for PNG output) ──

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

func circleShadowLayer(w, h int, cx, cy, rad float64, c color.NRGBA, alpha uint8, blur float64) *image.NRGBA {
	rgba := image.NewRGBA(image.Rect(0, 0, w, h))
	dc := gg.NewContextForRGBA(rgba)
	dc.SetColor(color.NRGBA{c.R, c.G, c.B, alpha})
	dc.DrawCircle(cx, cy, rad)
	dc.Fill()
	nrgba := rgbaToNRGBA(rgba)
	if blur > 0 {
		return gaussianBlurNRGBA(nrgba, blur)
	}
	return nrgba
}

func triangleShadowLayer(w, h int, x0, y0, x1, y1, x2, y2 float64, c color.NRGBA, alpha uint8, blur float64) *image.NRGBA {
	rgba := image.NewRGBA(image.Rect(0, 0, w, h))
	dc := gg.NewContextForRGBA(rgba)
	dc.SetColor(color.NRGBA{c.R, c.G, c.B, alpha})
	dc.MoveTo(x0, y0)
	dc.LineTo(x1, y1)
	dc.LineTo(x2, y2)
	dc.ClosePath()
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
