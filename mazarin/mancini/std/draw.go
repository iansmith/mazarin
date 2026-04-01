package std

import (
	"image"
	"image/color"
	"image/draw"
	"math"
	"sync/atomic"
	_ "unsafe"

	"github.com/fogleman/gg"

	"mazzy/mazarin/mancini"
)

// ── Draw performance instrumentation ────────────────────────────────────────

//go:linkname nanotime runtime.nanotime
func nanotime() int64

// DrawPerfStats accumulates timing data during a draw pass.
// All times in nanoseconds. Call ResetDrawPerf before a draw,
// then PrintDrawPerf after.
type DrawPerfStats struct {
	AllocNs    atomic.Int64 // image.NewRGBA / NewNRGBA / NewAlpha
	AllocCount atomic.Int64
	AllocBytes atomic.Int64
	GGDrawNs   atomic.Int64 // gg context create + rasterize + fill
	GGCount    atomic.Int64
	ConvertNs  atomic.Int64 // rgbaToNRGBA
	BlurNs     atomic.Int64 // gaussianBlurNRGBA
	BlurCount  atomic.Int64
	ComposeNs  atomic.Int64 // draw.Draw compositing
	FaceNs     atomic.Int64 // dc.DrawRoundedRect/Circle + Fill (face)

	// Tree-level timing
	AppDecorateNs atomic.Int64 // AppWindow.DecorateIfNeeded
	AppClipNs     atomic.Int64 // WithClip + Flush
	AppChildNs    atomic.Int64 // child.Draw inside AppWindow
	RowChildNs    [8]atomic.Int64 // per-child draw time in Row (up to 8)
	RowChildCount atomic.Int64
	ColChildNs    atomic.Int64 // total Column child draw time
	NeuDecorNs    atomic.Int64 // NeuCircle.DecorateIfNeeded
	NeuChildNs    atomic.Int64 // NeuCircle child draw (Clock)
	ClockFaceNs   atomic.Int64 // Clock.DrawFace
	LabelNs       atomic.Int64 // Label.Draw total
	LabelCount    atomic.Int64
}

var drawPerf DrawPerfStats

func ResetDrawPerf() {
	drawPerf.AllocNs.Store(0)
	drawPerf.AllocCount.Store(0)
	drawPerf.AllocBytes.Store(0)
	drawPerf.GGDrawNs.Store(0)
	drawPerf.GGCount.Store(0)
	drawPerf.ConvertNs.Store(0)
	drawPerf.BlurNs.Store(0)
	drawPerf.BlurCount.Store(0)
	drawPerf.ComposeNs.Store(0)
	drawPerf.FaceNs.Store(0)
	drawPerf.AppDecorateNs.Store(0)
	drawPerf.AppClipNs.Store(0)
	drawPerf.AppChildNs.Store(0)
	for i := range drawPerf.RowChildNs {
		drawPerf.RowChildNs[i].Store(0)
	}
	drawPerf.RowChildCount.Store(0)
	drawPerf.ColChildNs.Store(0)
	drawPerf.NeuDecorNs.Store(0)
	drawPerf.NeuChildNs.Store(0)
	drawPerf.ClockFaceNs.Store(0)
	drawPerf.LabelNs.Store(0)
	drawPerf.LabelCount.Store(0)
}

func GetDrawPerf() *DrawPerfStats {
	return &drawPerf
}

// ── Gaussian blur (separable, non-premultiplied) ─────────────────────────────

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
	ta := nanotime()
	dst := image.NewNRGBA(b)
	drawPerf.AllocNs.Add(nanotime() - ta)
	drawPerf.AllocCount.Add(1)
	drawPerf.AllocBytes.Add(int64(w * h * 4))
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

	ta := nanotime()
	tmp := image.NewNRGBA(b)
	drawPerf.AllocNs.Add(nanotime() - ta)
	drawPerf.AllocCount.Add(1)
	drawPerf.AllocBytes.Add(int64(w * h * 4))
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

	ta2 := nanotime()
	dst := image.NewNRGBA(b)
	drawPerf.AllocNs.Add(nanotime() - ta2)
	drawPerf.AllocCount.Add(1)
	drawPerf.AllocBytes.Add(int64(w * h * 4))
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

// ── Shadow / mask helpers ────────────────────────────────────────────────────

// shadowLayer renders a colored rounded rectangle into a temporary NRGBA
// buffer, optionally blurred.
func shadowLayer(w, h int, x1, y1, x2, y2, r float64, c color.NRGBA, alpha uint8, blur float64) *image.NRGBA {
	t0 := nanotime()
	rgba := image.NewRGBA(image.Rect(0, 0, w, h))
	t1 := nanotime()
	drawPerf.AllocNs.Add(t1 - t0)
	drawPerf.AllocCount.Add(1)
	drawPerf.AllocBytes.Add(int64(w * h * 4))

	dc := gg.NewContextForRGBA(rgba)
	dc.SetColor(color.NRGBA{c.R, c.G, c.B, alpha})
	dc.DrawRoundedRectangle(x1, y1, x2-x1, y2-y1, r)
	dc.Fill()
	t2 := nanotime()
	drawPerf.GGDrawNs.Add(t2 - t1)
	drawPerf.GGCount.Add(1)

	nrgba := rgbaToNRGBA(rgba)
	t3 := nanotime()
	drawPerf.ConvertNs.Add(t3 - t2)

	if blur > 0 {
		result := gaussianBlurNRGBA(nrgba, blur)
		t4 := nanotime()
		drawPerf.BlurNs.Add(t4 - t3)
		drawPerf.BlurCount.Add(1)
		return result
	}
	return nrgba
}

func roundedRectMask(w, h int, x1, y1, x2, y2, r float64) *image.Alpha {
	rgba := image.NewRGBA(image.Rect(0, 0, w, h))
	dc := gg.NewContextForRGBA(rgba)
	dc.SetColor(color.White) // mask: no SwapRB needed (only alpha matters)
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

// ── Neumorphic box rendering ─────────────────────────────────────────────────

// NeuBoxWith draws a neumorphic rounded rectangle using explicit params.
// It is the central rendering function used by [Button], [Checkbox],
// [CheckboxWithLabel], [NeuBox], [AppWindow], [FreeFloatingWindow],
// [SingleLineText], and [NOfMChooser].
//
// The depth parameter selects the shadow treatment ([mancini.Raised],
// [mancini.Flush], or [mancini.Inset]). If p is nil, a flat
// surface-colored rectangle is drawn (no shadows).
func NeuBoxWith(pal mancini.Palette, dc mancini.DrawContext, depth mancini.NeuDepth, x1, y1, x2, y2, r float64, face color.NRGBA, p *mancini.NeuParams) {
	if p != nil {
		switch depth {
		case mancini.Raised:
			neuRaised(pal, dc, x1, y1, x2, y2, r, face, p.Raised)
		case mancini.Flush:
			neuFlush(pal, dc, x1, y1, x2, y2, r, face, p.Flush)
		case mancini.Inset:
			neuInset(pal, dc, x1, y1, x2, y2, r, face, p.Inset)
		}
		if face != pal.Surface() && depth != mancini.Raised {
			canvas := dc.Image().(*image.RGBA)
			applyTintOverlay(pal, dc, canvas, x1, y1, x2, y2, r, face)
		}
	} else {
		dc.SetColor(face)
		dc.DrawRoundedRectangle(x1, y1, x2-x1, y2-y1, r)
		dc.Fill()
	}
}

// localRect computes a padded bounding box around (x1,y1)-(x2,y2) for
// local temp buffers, clamped to canvas bounds. Coordinates are in
// app-local space; dc.TransformPoint maps them to image-space for
// compositing. Returns local buffer dimensions and image-space origin.
func localRect(canvas *image.RGBA, dc mancini.DrawContext, x1, y1, x2, y2, pad float64) (lw, lh int, ox, oy float64) {
	// Transform corners to image-space.
	ix1, iy1 := dc.TransformPoint(x1, y1)
	ix2, iy2 := dc.TransformPoint(x2, y2)
	b := canvas.Bounds()
	ox = math.Floor(ix1 - pad)
	oy = math.Floor(iy1 - pad)
	ex := math.Ceil(ix2 + pad)
	ey := math.Ceil(iy2 + pad)
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

func neuRaised(pal mancini.Palette, dc mancini.DrawContext, x1, y1, x2, y2, r float64, face color.NRGBA, p mancini.RaisedParams) {
	canvas := dc.Image().(*image.RGBA)
	maxOff := math.Max(p.DarkOff, p.LightOff)
	maxBlur := math.Max(p.DarkBlur, p.LightBlur)
	pad := maxOff + math.Ceil(maxBlur*3) + 2
	lw, lh, ox, oy := localRect(canvas, dc, x1, y1, x2, y2, pad)
	ix1, iy1 := dc.TransformPoint(x1, y1)
	ix2, iy2 := dc.TransformPoint(x2, y2)
	lx1, ly1, lx2, ly2 := ix1-ox, iy1-oy, ix2-ox, iy2-oy
	dst := image.Rect(int(ox), int(oy), int(ox)+lw, int(oy)+lh)

	dark := shadowLayer(lw, lh,
		lx1+p.DarkOff, ly1+p.DarkOff, lx2+p.DarkOff, ly2+p.DarkOff,
		r, pal.DarkShadow(), p.DarkAlpha, p.DarkBlur)
	tc0 := nanotime()
	draw.Draw(canvas, dst, dark, image.Point{}, draw.Over)
	drawPerf.ComposeNs.Add(nanotime() - tc0)

	light := shadowLayer(lw, lh,
		lx1-p.LightOff, ly1-p.LightOff, lx2-p.LightOff, ly2-p.LightOff,
		r, pal.LightShadow(), p.LightAlpha, p.LightBlur)
	tc1 := nanotime()
	draw.Draw(canvas, dst, light, image.Point{}, draw.Over)
	drawPerf.ComposeNs.Add(nanotime() - tc1)

	tf0 := nanotime()
	dc.SetColor(face)
	dc.DrawRoundedRectangle(x1, y1, x2-x1, y2-y1, r)
	dc.Fill()
	drawPerf.FaceNs.Add(nanotime() - tf0)
}

func neuInset(pal mancini.Palette, dc mancini.DrawContext, x1, y1, x2, y2, r float64, face color.NRGBA, p mancini.InsetParams) {
	canvas := dc.Image().(*image.RGBA)

	dc.SetColor(face)
	dc.DrawRoundedRectangle(x1, y1, x2-x1, y2-y1, r)
	dc.Fill()

	maxBlur := math.Max(p.DarkBlur, p.LightBlur)
	pad := p.Off + math.Ceil(maxBlur*3) + 2
	lw, lh, ox, oy := localRect(canvas, dc, x1, y1, x2, y2, pad)
	ix1, iy1 := dc.TransformPoint(x1, y1)
	ix2, iy2 := dc.TransformPoint(x2, y2)
	lx1, ly1, lx2, ly2 := ix1-ox, iy1-oy, ix2-ox, iy2-oy
	dst := image.Rect(int(ox), int(oy), int(ox)+lw, int(oy)+lh)

	mask := roundedRectMask(lw, lh, lx1, ly1, lx2, ly2, r)
	type shadowSpec struct {
		ox, oy float64
		alpha  uint8
		blur   float64
		c      color.NRGBA
	}
	shadows := []shadowSpec{
		{-p.Off, -p.Off, 190, p.DarkBlur, pal.DarkShadow()},
		{+p.Off, +p.Off, 190, p.LightBlur, pal.LightShadow()},
	}
	for _, s := range shadows {
		sh := shadowLayer(lw, lh,
			lx1+s.ox, ly1+s.oy, lx2+s.ox, ly2+s.oy,
			r, s.c, s.alpha, s.blur)
		draw.DrawMask(canvas, dst, sh, image.Point{}, mask, image.Point{}, draw.Over)
	}
}

func neuFlush(pal mancini.Palette, dc mancini.DrawContext, x1, y1, x2, y2, r float64, face color.NRGBA, p mancini.FlushParams) {
	canvas := dc.Image().(*image.RGBA)

	dc.SetColor(face)
	dc.DrawRoundedRectangle(x1, y1, x2-x1, y2-y1, r)
	dc.Fill()

	pad := p.EdgeW + 2
	lw, lh, ox, oy := localRect(canvas, dc, x1, y1, x2, y2, pad)
	ix1, iy1 := dc.TransformPoint(x1, y1)
	ix2, iy2 := dc.TransformPoint(x2, y2)
	lx1, ly1, lx2, ly2 := ix1-ox, iy1-oy, ix2-ox, iy2-oy
	dst := image.Rect(int(ox), int(oy), int(ox)+lw, int(oy)+lh)

	darkEdge := image.NewRGBA(image.Rect(0, 0, lw, lh))
	ddc := gg.NewContextForRGBA(darkEdge)
	darkC := pal.DarkShadow()
	ddc.SetColor(color.NRGBA{darkC.R, darkC.G, darkC.B, p.EdgeAlpha})
	ddc.SetLineWidth(p.EdgeW)
	ddc.DrawRoundedRectangle(lx1, ly1, lx2-lx1, ly2-ly1, r)
	ddc.Stroke()
	draw.Draw(canvas, dst, darkEdge, image.Point{}, draw.Over)

	lightEdge := image.NewRGBA(image.Rect(0, 0, lw, lh))
	ldc := gg.NewContextForRGBA(lightEdge)
	lightC := pal.LightShadow()
	ldc.SetColor(color.NRGBA{lightC.R, lightC.G, lightC.B, p.EdgeAlpha})
	ldc.SetLineWidth(p.EdgeW)
	ldc.DrawRoundedRectangle(lx1+1, ly1+1, lx2-lx1, ly2-ly1, r)
	ldc.Stroke()
	mask := roundedRectMask(lw, lh, lx1, ly1, lx2, ly2, r)
	draw.DrawMask(canvas, dst, lightEdge, image.Point{}, mask, image.Point{}, draw.Over)
}

func applyTintOverlay(pal mancini.Palette, dc mancini.DrawContext, canvas *image.RGBA, x1, y1, x2, y2, r float64, tint color.NRGBA) {
	pad := 2.0
	lw, lh, ox, oy := localRect(canvas, dc, x1, y1, x2, y2, pad)
	ix1, iy1 := dc.TransformPoint(x1, y1)
	lx1, ly1 := ix1-ox, iy1-oy

	overlay := image.NewRGBA(image.Rect(0, 0, lw, lh))
	odc := gg.NewContextForRGBA(overlay)
	odc.SetColor(color.NRGBA{tint.R, tint.G, tint.B, 100})
	odc.DrawRoundedRectangle(lx1, ly1, x2-x1, y2-y1, r)
	odc.Fill()
	dst := image.Rect(int(ox), int(oy), int(ox)+lw, int(oy)+lh)
	draw.Draw(canvas, dst, overlay, image.Point{}, draw.Over)
}

// ── Neumorphic circle rendering ──────────────────────────────────────────────

// NeuCircleWith draws a neumorphic circle using explicit params. It is
// the rendering counterpart of [NeuBoxWith] for circular shapes, used
// by [NeuCircle].
//
// If p is nil, a flat surface-colored circle is drawn (no shadows).
func NeuCircleWith(pal mancini.Palette, dc mancini.DrawContext, depth mancini.NeuDepth, cx, cy, rad float64, face color.NRGBA, p *mancini.NeuParams) {
	if p != nil {
		switch depth {
		case mancini.Raised:
			neuCircleRaised(pal, dc, cx, cy, rad, face, p.Raised)
		case mancini.Flush:
			neuCircleFlush(pal, dc, cx, cy, rad, face, p.Flush)
		case mancini.Inset:
			neuCircleInset(pal, dc, cx, cy, rad, face, p.Inset)
		}
		if face != pal.Surface() && depth != mancini.Raised {
			canvas := dc.Image().(*image.RGBA)
			applyCircleTintOverlay(pal, dc, canvas, cx, cy, rad, face)
		}
	} else {
		dc.SetColor(face)
		dc.DrawCircle(cx, cy, rad)
		dc.Fill()
	}
}

func circleShadowLayer(w, h int, cx, cy, rad float64, c color.NRGBA, alpha uint8, blur float64) *image.NRGBA {
	t0 := nanotime()
	rgba := image.NewRGBA(image.Rect(0, 0, w, h))
	t1 := nanotime()
	drawPerf.AllocNs.Add(t1 - t0)
	drawPerf.AllocCount.Add(1)
	drawPerf.AllocBytes.Add(int64(w * h * 4))

	dc := gg.NewContextForRGBA(rgba)
	dc.SetColor(color.NRGBA{c.R, c.G, c.B, alpha})
	dc.DrawCircle(cx, cy, rad)
	dc.Fill()
	t2 := nanotime()
	drawPerf.GGDrawNs.Add(t2 - t1)
	drawPerf.GGCount.Add(1)

	nrgba := rgbaToNRGBA(rgba)
	t3 := nanotime()
	drawPerf.ConvertNs.Add(t3 - t2)

	if blur > 0 {
		result := gaussianBlurNRGBA(nrgba, blur)
		t4 := nanotime()
		drawPerf.BlurNs.Add(t4 - t3)
		drawPerf.BlurCount.Add(1)
		return result
	}
	return nrgba
}

func circleMask(w, h int, cx, cy, rad float64) *image.Alpha {
	rgba := image.NewRGBA(image.Rect(0, 0, w, h))
	dc := gg.NewContextForRGBA(rgba)
	dc.SetColor(color.White) // mask: no SwapRB needed (only alpha matters)
	dc.DrawCircle(cx, cy, rad)
	dc.Fill()
	mask := image.NewAlpha(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			mask.Pix[y*mask.Stride+x] = rgba.Pix[y*rgba.Stride+x*4+3]
		}
	}
	return mask
}

func neuCircleRaised(pal mancini.Palette, dc mancini.DrawContext, cx, cy, rad float64, face color.NRGBA, p mancini.RaisedParams) {
	neuCircleRaisedShadows(pal, dc, cx, cy, rad, p)
	dc.SetColor(face)
	dc.DrawCircle(cx, cy, rad)
	dc.Fill()
}

// neuCircleRaisedShadows draws only the dark and light shadow layers for a
// raised circle. The face fill is NOT drawn — the caller controls face radius
// separately, allowing a visible bevel ring between face and shadow.
func neuCircleRaisedShadows(pal mancini.Palette, dc mancini.DrawContext, cx, cy, rad float64, p mancini.RaisedParams) {
	canvas := dc.Image().(*image.RGBA)
	x1, y1, x2, y2 := cx-rad, cy-rad, cx+rad, cy+rad
	maxOff := math.Max(p.DarkOff, p.LightOff)
	maxBlur := math.Max(p.DarkBlur, p.LightBlur)
	pad := maxOff + math.Ceil(maxBlur*3) + 2
	lw, lh, ox, oy := localRect(canvas, dc, x1, y1, x2, y2, pad)
	icx, icy := dc.TransformPoint(cx, cy)
	lcx, lcy := icx-ox, icy-oy
	dst := image.Rect(int(ox), int(oy), int(ox)+lw, int(oy)+lh)

	dark := circleShadowLayer(lw, lh,
		lcx+p.DarkOff, lcy+p.DarkOff, rad,
		pal.DarkShadow(), p.DarkAlpha, p.DarkBlur)
	tc0 := nanotime()
	draw.Draw(canvas, dst, dark, image.Point{}, draw.Over)
	drawPerf.ComposeNs.Add(nanotime() - tc0)

	light := circleShadowLayer(lw, lh,
		lcx-p.LightOff, lcy-p.LightOff, rad,
		pal.LightShadow(), p.LightAlpha, p.LightBlur)
	tc1 := nanotime()
	draw.Draw(canvas, dst, light, image.Point{}, draw.Over)
	drawPerf.ComposeNs.Add(nanotime() - tc1)
}

func neuCircleInset(pal mancini.Palette, dc mancini.DrawContext, cx, cy, rad float64, face color.NRGBA, p mancini.InsetParams) {
	canvas := dc.Image().(*image.RGBA)

	dc.SetColor(face)
	dc.DrawCircle(cx, cy, rad)
	dc.Fill()

	x1, y1, x2, y2 := cx-rad, cy-rad, cx+rad, cy+rad
	maxBlur := math.Max(p.DarkBlur, p.LightBlur)
	pad := p.Off + math.Ceil(maxBlur*3) + 2
	lw, lh, ox, oy := localRect(canvas, dc, x1, y1, x2, y2, pad)
	icx, icy := dc.TransformPoint(cx, cy)
	lcx, lcy := icx-ox, icy-oy
	dst := image.Rect(int(ox), int(oy), int(ox)+lw, int(oy)+lh)

	mask := circleMask(lw, lh, lcx, lcy, rad)
	type shadowSpec struct {
		ox, oy float64
		alpha  uint8
		blur   float64
		c      color.NRGBA
	}
	shadows := []shadowSpec{
		{-p.Off, -p.Off, 190, p.DarkBlur, pal.DarkShadow()},
		{+p.Off, +p.Off, 190, p.LightBlur, pal.LightShadow()},
	}
	for _, s := range shadows {
		sh := circleShadowLayer(lw, lh,
			lcx+s.ox, lcy+s.oy, rad,
			s.c, s.alpha, s.blur)
		draw.DrawMask(canvas, dst, sh, image.Point{}, mask, image.Point{}, draw.Over)
	}
}

func neuCircleFlush(pal mancini.Palette, dc mancini.DrawContext, cx, cy, rad float64, face color.NRGBA, p mancini.FlushParams) {
	canvas := dc.Image().(*image.RGBA)

	dc.SetColor(face)
	dc.DrawCircle(cx, cy, rad)
	dc.Fill()

	x1, y1, x2, y2 := cx-rad, cy-rad, cx+rad, cy+rad
	pad := p.EdgeW + 2
	lw, lh, ox, oy := localRect(canvas, dc, x1, y1, x2, y2, pad)
	icx, icy := dc.TransformPoint(cx, cy)
	lcx, lcy := icx-ox, icy-oy
	dst := image.Rect(int(ox), int(oy), int(ox)+lw, int(oy)+lh)

	darkEdge := image.NewRGBA(image.Rect(0, 0, lw, lh))
	ddc := gg.NewContextForRGBA(darkEdge)
	darkC := pal.DarkShadow()
	ddc.SetColor(color.NRGBA{darkC.R, darkC.G, darkC.B, p.EdgeAlpha})
	ddc.SetLineWidth(p.EdgeW)
	ddc.DrawCircle(lcx, lcy, rad)
	ddc.Stroke()
	draw.Draw(canvas, dst, darkEdge, image.Point{}, draw.Over)

	lightEdge := image.NewRGBA(image.Rect(0, 0, lw, lh))
	ldc := gg.NewContextForRGBA(lightEdge)
	lightC := pal.LightShadow()
	ldc.SetColor(color.NRGBA{lightC.R, lightC.G, lightC.B, p.EdgeAlpha})
	ldc.SetLineWidth(p.EdgeW)
	ldc.DrawCircle(lcx+1, lcy+1, rad)
	ldc.Stroke()
	mask := circleMask(lw, lh, lcx, lcy, rad)
	draw.DrawMask(canvas, dst, lightEdge, image.Point{}, mask, image.Point{}, draw.Over)
}

func applyCircleTintOverlay(pal mancini.Palette, dc mancini.DrawContext, canvas *image.RGBA, cx, cy, rad float64, tint color.NRGBA) {
	x1, y1, x2, y2 := cx-rad, cy-rad, cx+rad, cy+rad
	pad := 2.0
	lw, lh, ox, oy := localRect(canvas, dc, x1, y1, x2, y2, pad)
	icx, icy := dc.TransformPoint(cx, cy)
	lcx, lcy := icx-ox, icy-oy

	overlay := image.NewRGBA(image.Rect(0, 0, lw, lh))
	odc := gg.NewContextForRGBA(overlay)
	odc.SetColor(color.NRGBA{tint.R, tint.G, tint.B, 100})
	odc.DrawCircle(lcx, lcy, rad)
	odc.Fill()
	dst := image.Rect(int(ox), int(oy), int(ox)+lw, int(oy)+lh)
	draw.Draw(canvas, dst, overlay, image.Point{}, draw.Over)
}

// ── Groove ────────────────────────────────────────────────────────────────────

// NeuGroove draws a thin horizontal inset line (groove separator). Used
// by [FreeFloatingWindow] for the title/content separator and available
// for custom interactor implementations.
func NeuGroove(pal mancini.Palette, dc mancini.DrawContext, x1, y, x2 float64) {
	neuInset(pal, dc, x1, y, x2, y+3, 2, pal.Surface(), mancini.GrooveParams)
}

