package textshape

import (
	"image"
	"image/color"
	"math"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/math/f64"
	"golang.org/x/image/vector"
)

// pathOp identifies a stored path command.
type pathOp int

const (
	pathMoveTo    pathOp = iota
	pathLineTo
	pathQuadTo
	pathCubicTo
	pathClose
)

// pathSeg is a single stored path command with up to 3 (x,y) pairs.
type pathSeg struct {
	op   pathOp
	args [6]float64 // x1,y1, x2,y2, x3,y3
}

// graphicsState holds all saveable/restorable drawing state (no image buffer).
type graphicsState struct {
	matrix        Matrix
	color         color.Color
	fillPattern   Pattern
	strokePattern Pattern
	lineWidth     float64
	lineCap       LineCap
	lineJoin      LineJoin
	miterLimit    float64 // SVG/CSS stroke-miterlimit; ratio cap for LineJoinMiter. Default 4.
	fillRule      FillRule
	dashes        []float64
	clipMask      *image.Alpha    // canvas-sized hard-edged coverage mask for non-rectangular clips; nil when the clip is rect-only
	clipRect      image.Rectangle // axis-aligned clip rectangle (also the bounding box of clipMask when one is set)
	hasClipRect   bool
}

// groupEntry holds the image buffer saved by PushGroup.
type groupEntry struct {
	im *image.RGBA
}

// DrawContextImpl is the concrete implementation of [DrawContext].
type DrawContextImpl struct {
	width  int
	height int
	im     *image.RGBA

	// path state
	path       []pathSeg
	hasCurrent bool
	startX     float64
	startY     float64
	currentX   float64
	currentY   float64

	// graphics state
	gs graphicsState

	// stacks
	gsStack    []graphicsState
	groupStack []groupEntry

	// text
	tl      *HarfBuzzTextLayout
	metrics [MaxFonts]*FontMetrics
}

// NewDrawContext creates a DrawContextImpl rendering onto a new width×height
// RGBA image, using provider for glyph bitmaps.
func NewDrawContext(width, height int, provider GlyphProvider) *DrawContextImpl {
	dc := &DrawContextImpl{
		width:  width,
		height: height,
		im:     image.NewRGBA(image.Rect(0, 0, width, height)),
		tl:     NewTextLayoutWithProvider(provider),
	}
	dc.gs = graphicsState{
		matrix:        IdentityMatrix(),
		color:         color.Transparent,
		fillPattern:   NewSolidPattern(color.White),
		strokePattern: NewSolidPattern(color.Black),
		lineWidth:     1,
		miterLimit:    4,
		fillRule:      FillRuleWinding,
	}
	return dc
}

// NewDrawContextForImage creates a DrawContextImpl that renders onto an
// existing RGBA image, using provider for glyph bitmaps.
func NewDrawContextForImage(target *image.RGBA, provider GlyphProvider) *DrawContextImpl {
	b := target.Bounds()
	dc := &DrawContextImpl{
		width:  b.Dx(),
		height: b.Dy(),
		im:     target,
		tl:     NewTextLayoutWithProvider(provider),
	}
	dc.gs = graphicsState{
		matrix:        IdentityMatrix(),
		color:         color.Transparent,
		fillPattern:   NewSolidPattern(color.White),
		strokePattern: NewSolidPattern(color.Black),
		lineWidth:     1,
		miterLimit:    4,
		fillRule:      FillRuleWinding,
	}
	return dc
}

// Clear fills the entire canvas with the current color (set via SetColor).
func (dc *DrawContextImpl) Clear() {
	xdraw.Draw(dc.im, dc.im.Bounds(), image.NewUniform(dc.gs.color), image.Point{}, xdraw.Src)
}

// --- Canvas ---

func (dc *DrawContextImpl) Image() image.Image { return dc.im }
func (dc *DrawContextImpl) Width() int          { return dc.width }
func (dc *DrawContextImpl) Height() int         { return dc.height }

// NewChildContext creates an off-screen DrawContext rendering onto the
// given target image, sharing the same text layout engine (and thus the
// same GlyphProvider and open fonts).
func (dc *DrawContextImpl) NewChildContext(target *image.RGBA) DrawContext {
	b := target.Bounds()
	child := &DrawContextImpl{
		width:   b.Dx(),
		height:  b.Dy(),
		im:      target,
		tl:      dc.tl,
		metrics: dc.metrics,
	}
	child.gs = graphicsState{
		matrix:        IdentityMatrix(),
		color:         color.Transparent,
		fillPattern:   NewSolidPattern(color.White),
		strokePattern: NewSolidPattern(color.Black),
		lineWidth:     1,
		miterLimit:    4,
		fillRule:      FillRuleWinding,
	}
	return child
}

// --- Graphics state ---

func (dc *DrawContextImpl) SetColor(c color.Color) {
	dc.gs.color = c
	dc.gs.fillPattern = NewSolidPattern(c)
	dc.gs.strokePattern = NewSolidPattern(c)
}

func (dc *DrawContextImpl) SetFillStyle(p Pattern) {
	if sp, ok := p.(*solidPattern); ok {
		dc.gs.color = sp.c
	}
	dc.gs.fillPattern = p
}

func (dc *DrawContextImpl) SetStrokeStyle(p Pattern) {
	dc.gs.strokePattern = p
}

func (dc *DrawContextImpl) SetLineWidth(w float64)       { dc.gs.lineWidth = w }
func (dc *DrawContextImpl) SetLineCap(lc LineCap)        { dc.gs.lineCap = lc }
func (dc *DrawContextImpl) SetLineJoin(lj LineJoin)      { dc.gs.lineJoin = lj }
func (dc *DrawContextImpl) SetMiterLimit(limit float64)  { dc.gs.miterLimit = limit }
func (dc *DrawContextImpl) SetFillRule(fr FillRule)      { dc.gs.fillRule = fr }
func (dc *DrawContextImpl) SetDash(dashes ...float64)    { dc.gs.dashes = append([]float64(nil), dashes...) }

// --- Transform stack ---

func (dc *DrawContextImpl) Push() {
	saved := dc.gs
	saved.dashes = append([]float64(nil), dc.gs.dashes...)
	dc.gsStack = append(dc.gsStack, saved)
}

func (dc *DrawContextImpl) Pop() {
	n := len(dc.gsStack)
	if n == 0 {
		return
	}
	dc.gs = dc.gsStack[n-1]
	dc.gsStack = dc.gsStack[:n-1]
}

// Clip sets the clip region to the current path. An axis-aligned
// rectangle is stored as a fast clipRect; any other path (circle,
// ellipse, polygon, rounded rect, arbitrary curves) is rasterized into
// a canvas-sized alpha coverage buffer — the clipMask — mirroring
// Blink's GraphicsContext::ClipPath, which delegates to Skia's
// clipPath. If a clip is already active, the new clip is intersected
// with it (rect∩rect, rect∩mask, mask∩mask all supported).
func (dc *DrawContextImpl) Clip() {
	// Clear the path on every exit path — including panics or early returns —
	// so a malformed clip request from one caller (e.g. a renderer panicking
	// mid-Clip on a rounded path) doesn't leave residual segments that would
	// then poison the next caller's DrawRectangle+Clip pair.
	defer dc.ClearPath()

	if len(dc.path) == 0 {
		return
	}
	if rect, ok := dc.pathIsAxisAlignedRect(); ok {
		// Fast path: axis-aligned rectangle. Intersect with whatever clip
		// is already active.
		r := image.Rect(int(rect[0]), int(rect[1]), int(rect[2]), int(rect[3]))
		if dc.gs.clipMask != nil {
			// rect ∩ mask: zero the mask outside the rect, keep it a mask.
			// Clone first — the existing mask may be shared with a graphics
			// state still on the Push/Pop stack.
			m := cloneAlpha(dc.gs.clipMask)
			dc.gs.clipMask = intersectMaskWithRect(m, r)
			// Tighten the bounding clipRect too.
			if dc.gs.hasClipRect {
				dc.gs.clipRect = dc.gs.clipRect.Intersect(r)
			} else {
				dc.gs.clipRect = r
				dc.gs.hasClipRect = true
			}
			return
		}
		if dc.gs.hasClipRect {
			r = r.Intersect(dc.gs.clipRect)
		}
		dc.gs.clipRect = r
		dc.gs.hasClipRect = true
		return
	}

	// Non-rectangular path: rasterize it into a canvas-sized alpha coverage
	// buffer. This is the clip-as-mask fallback — the exact analogue of
	// Skia's clipPath for a non-rect path.
	mask := dc.rasterizeClipMask()
	if mask == nil {
		return
	}
	// Intersect with any existing clip. `mask` is freshly allocated, so
	// mutating it in place is safe even if the parent's mask is shared.
	if dc.gs.clipMask != nil {
		intersectMasks(mask, dc.gs.clipMask)
	}
	if dc.gs.hasClipRect {
		mask = intersectMaskWithRect(mask, dc.gs.clipRect)
	}
	// Maintain clipRect as the mask's bounding box so the existing
	// rasterizer-size clamps in fillWithPattern / FillRectangle stay tight.
	bbox := dc.pathBoundingBox()
	if dc.gs.hasClipRect {
		bbox = bbox.Intersect(dc.gs.clipRect)
	}
	dc.gs.clipMask = mask
	dc.gs.clipRect = bbox
	dc.gs.hasClipRect = true
}

// rasterizeClipMask rasterizes the current path into a canvas-sized
// image.Alpha coverage buffer. Coverage is hard-edged — any non-zero
// rasterizer coverage becomes fully opaque — mirroring fillWithPattern,
// which treats any non-zero coverage as fully opaque for sharp CSS pixel
// boundaries. A clip-path: circle()/ellipse() thus aligns exactly with
// the same-geometry border-radius fill it is reftested against.
//
// The path is rasterized into a path-bounds-sized scratch buffer using
// the *exact* same origin offset (the 1px-padded pathBounds() min) that
// fillWithPattern uses, then blitted into the canvas-sized mask. Using
// the identical offset matters: vector.Rasterizer consumes float32
// coordinates, and feeding it path-relative coords (small magnitude,
// full mantissa precision) versus canvas-absolute coords can round
// boundary pixels differently — so the clip mask must rasterize the
// path the same way the matching fill does, or a clip-path will be off
// by a pixel from a same-geometry border-radius fill.
func (dc *DrawContextImpl) rasterizeClipMask() *image.Alpha {
	cb := dc.im.Bounds()
	cw, ch := cb.Dx(), cb.Dy()
	if cw <= 0 || ch <= 0 {
		return nil
	}
	// Path-bounds-sized scratch, matching fillWithPattern's bbox.
	bbox := dc.pathBounds()
	bw, bh := bbox.Dx(), bbox.Dy()
	mask := image.NewAlpha(image.Rect(0, 0, cw, ch))
	if bw <= 0 || bh <= 0 {
		return mask
	}
	r := vector.NewRasterizer(bw, bh)
	ox, oy := float32(bbox.Min.X), float32(bbox.Min.Y)
	for _, seg := range dc.path {
		switch seg.op {
		case pathMoveTo:
			r.MoveTo(float32(seg.args[0])-ox, float32(seg.args[1])-oy)
		case pathLineTo:
			r.LineTo(float32(seg.args[0])-ox, float32(seg.args[1])-oy)
		case pathQuadTo:
			r.QuadTo(float32(seg.args[0])-ox, float32(seg.args[1])-oy,
				float32(seg.args[2])-ox, float32(seg.args[3])-oy)
		case pathCubicTo:
			r.CubeTo(float32(seg.args[0])-ox, float32(seg.args[1])-oy,
				float32(seg.args[2])-ox, float32(seg.args[3])-oy,
				float32(seg.args[4])-ox, float32(seg.args[5])-oy)
		case pathClose:
			r.ClosePath()
		}
	}
	if dc.hasCurrent {
		r.ClosePath()
	}
	scratch := image.NewAlpha(image.Rect(0, 0, bw, bh))
	r.Draw(scratch, scratch.Bounds(), image.Opaque, image.Point{})
	// Hard-edge any non-zero coverage and blit into the canvas-sized mask
	// at the path's bounding-box origin.
	for py := 0; py < bh; py++ {
		cy := bbox.Min.Y + py
		if cy < cb.Min.Y || cy >= cb.Max.Y {
			continue
		}
		for px := 0; px < bw; px++ {
			if scratch.Pix[py*scratch.Stride+px] == 0 {
				continue
			}
			cx := bbox.Min.X + px
			if cx < cb.Min.X || cx >= cb.Max.X {
				continue
			}
			mask.Pix[cy*mask.Stride+cx] = 255
		}
	}
	return mask
}

// cloneAlpha returns a deep copy of an image.Alpha.
func cloneAlpha(src *image.Alpha) *image.Alpha {
	dst := image.NewAlpha(src.Rect)
	copy(dst.Pix, src.Pix)
	return dst
}

// intersectMasks multiplies dst by src in place (logical AND of two
// hard-edged coverage masks). Both must be the same canvas-sized bounds.
func intersectMasks(dst, src *image.Alpha) {
	n := len(dst.Pix)
	if len(src.Pix) < n {
		n = len(src.Pix)
	}
	for i := 0; i < n; i++ {
		if src.Pix[i] == 0 {
			dst.Pix[i] = 0
		}
	}
}

// intersectMaskWithRect zeroes a canvas-sized coverage mask outside the
// given rectangle, returning the same mask.
func intersectMaskWithRect(mask *image.Alpha, r image.Rectangle) *image.Alpha {
	b := mask.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if x < r.Min.X || x >= r.Max.X || y < r.Min.Y || y >= r.Max.Y {
				mask.Pix[y*mask.Stride+x] = 0
			}
		}
	}
	return mask
}

// clipMaskAt returns the clip coverage (0 or 255) at canvas pixel
// (x, y). Returns 255 when no clip mask is active.
func (dc *DrawContextImpl) clipMaskAt(x, y int) uint8 {
	m := dc.gs.clipMask
	if m == nil {
		return 255
	}
	b := m.Bounds()
	if x < b.Min.X || x >= b.Max.X || y < b.Min.Y || y >= b.Max.Y {
		return 0
	}
	return m.Pix[(y-b.Min.Y)*m.Stride+(x-b.Min.X)]
}

// pathBoundingBox returns the integer-pixel bounding rectangle of every point
// in the current path. Used as a fallback for Clip() when the path isn't a
// simple axis-aligned rectangle.
func (dc *DrawContextImpl) pathBoundingBox() image.Rectangle {
	if len(dc.path) == 0 {
		return image.Rectangle{}
	}
	minX, minY := math.Inf(1), math.Inf(1)
	maxX, maxY := math.Inf(-1), math.Inf(-1)
	for _, seg := range dc.path {
		// Each pathSeg holds up to 3 (x,y) pairs in args[0..5]; only the
		// pairs actually written by the op contribute. To keep this simple
		// and correct we walk all 6 floats but treat unset pairs (both 0)
		// as a no-op. That matches MoveTo/LineTo/Quadratic/Cubic which
		// always overwrite the relevant slots before appending.
		pairs := []struct{ x, y float64 }{
			{seg.args[0], seg.args[1]},
			{seg.args[2], seg.args[3]},
			{seg.args[4], seg.args[5]},
		}
		for _, p := range pairs {
			if p.x == 0 && p.y == 0 {
				continue
			}
			if p.x < minX {
				minX = p.x
			}
			if p.x > maxX {
				maxX = p.x
			}
			if p.y < minY {
				minY = p.y
			}
			if p.y > maxY {
				maxY = p.y
			}
		}
	}
	if math.IsInf(minX, 1) {
		return image.Rectangle{}
	}
	return image.Rect(int(math.Floor(minX)), int(math.Floor(minY)),
		int(math.Ceil(maxX)), int(math.Ceil(maxY)))
}

// pathIsAxisAlignedRect returns the bounding rectangle if the current
// path is a simple axis-aligned rectangle (MoveTo + 3 LineTo + Close),
// as produced by DrawRectangle. The returned array is [minX, minY, maxX, maxY].
func (dc *DrawContextImpl) pathIsAxisAlignedRect() ([4]float64, bool) {
	n := len(dc.path)
	// DrawRectangle produces: MoveTo, LineTo, LineTo, LineTo, Close = 5 segments.
	if n != 5 {
		return [4]float64{}, false
	}
	if dc.path[0].op != pathMoveTo ||
		dc.path[1].op != pathLineTo ||
		dc.path[2].op != pathLineTo ||
		dc.path[3].op != pathLineTo ||
		dc.path[4].op != pathClose {
		return [4]float64{}, false
	}

	// Extract the 4 corners.
	x0, y0 := dc.path[0].args[0], dc.path[0].args[1]
	x1, y1 := dc.path[1].args[0], dc.path[1].args[1]
	x2, y2 := dc.path[2].args[0], dc.path[2].args[1]
	x3, y3 := dc.path[3].args[0], dc.path[3].args[1]

	// Check axis-aligned: edges must be horizontal or vertical.
	// DrawRectangle produces: (x,y)→(x+w,y)→(x+w,y+h)→(x,y+h)→close
	// So: edge 0→1 horizontal, 1→2 vertical, 2→3 horizontal, 3→0 vertical.
	if y0 != y1 || x1 != x2 || y2 != y3 || x3 != x0 {
		return [4]float64{}, false
	}

	minX := math.Min(x0, x1)
	maxX := math.Max(x0, x1)
	minY := math.Min(y0, y2)
	maxY := math.Max(y0, y2)
	return [4]float64{minX, minY, maxX, maxY}, true
}

// ResetClip removes the active clip mask.
func (dc *DrawContextImpl) ResetClip() {
	dc.gs.clipMask = nil
	dc.gs.hasClipRect = false
}

func (dc *DrawContextImpl) Translate(x, y float64) {
	dc.gs.matrix = dc.gs.matrix.Translate(x, y)
}

func (dc *DrawContextImpl) Scale(x, y float64) {
	dc.gs.matrix = dc.gs.matrix.Scale(x, y)
}

func (dc *DrawContextImpl) Rotate(angle float64) {
	dc.gs.matrix = dc.gs.matrix.Rotate(angle)
}

func (dc *DrawContextImpl) RotateAbout(angle, x, y float64) {
	dc.Translate(x, y)
	dc.Rotate(angle)
	dc.Translate(-x, -y)
}

func (dc *DrawContextImpl) MultiplyMatrix(xx, yx, xy, yy, x0, y0 float64) {
	m := Matrix{XX: xx, YX: yx, XY: xy, YY: yy, X0: x0, Y0: y0}
	dc.gs.matrix = m.Multiply(dc.gs.matrix)
}

// transformPoint applies the current matrix to a point.
func (dc *DrawContextImpl) transformPoint(x, y float64) (float64, float64) {
	return dc.gs.matrix.TransformPoint(x, y)
}

// TransformPoint applies the current matrix to a point (exported, satisfies DrawContext interface).
func (dc *DrawContextImpl) TransformPoint(x, y float64) (float64, float64) {
	return dc.gs.matrix.TransformPoint(x, y)
}

// --- Path building ---

func (dc *DrawContextImpl) MoveTo(x, y float64) {
	tx, ty := dc.transformPoint(x, y)
	dc.path = append(dc.path, pathSeg{op: pathMoveTo, args: [6]float64{tx, ty}})
	dc.startX, dc.startY = tx, ty
	dc.currentX, dc.currentY = tx, ty
	dc.hasCurrent = true
}

func (dc *DrawContextImpl) LineTo(x, y float64) {
	if !dc.hasCurrent {
		dc.MoveTo(x, y)
		return
	}
	tx, ty := dc.transformPoint(x, y)
	dc.path = append(dc.path, pathSeg{op: pathLineTo, args: [6]float64{tx, ty}})
	dc.currentX, dc.currentY = tx, ty
}

func (dc *DrawContextImpl) QuadraticTo(x1, y1, x2, y2 float64) {
	if !dc.hasCurrent {
		dc.MoveTo(x1, y1)
	}
	tx1, ty1 := dc.transformPoint(x1, y1)
	tx2, ty2 := dc.transformPoint(x2, y2)
	dc.path = append(dc.path, pathSeg{op: pathQuadTo, args: [6]float64{tx1, ty1, tx2, ty2}})
	dc.currentX, dc.currentY = tx2, ty2
}

func (dc *DrawContextImpl) CubicTo(x1, y1, x2, y2, x3, y3 float64) {
	if !dc.hasCurrent {
		dc.MoveTo(x1, y1)
	}
	tx1, ty1 := dc.transformPoint(x1, y1)
	tx2, ty2 := dc.transformPoint(x2, y2)
	tx3, ty3 := dc.transformPoint(x3, y3)
	dc.path = append(dc.path, pathSeg{op: pathCubicTo, args: [6]float64{tx1, ty1, tx2, ty2, tx3, ty3}})
	dc.currentX, dc.currentY = tx3, ty3
}

func (dc *DrawContextImpl) ClosePath() {
	if dc.hasCurrent {
		dc.path = append(dc.path, pathSeg{op: pathClose})
		dc.currentX, dc.currentY = dc.startX, dc.startY
	}
}

func (dc *DrawContextImpl) ClearPath() {
	dc.path = dc.path[:0]
	dc.hasCurrent = false
}

// --- Shape primitives ---

func (dc *DrawContextImpl) DrawRectangle(x, y, w, h float64) {
	dc.MoveTo(x, y)
	dc.LineTo(x+w, y)
	dc.LineTo(x+w, y+h)
	dc.LineTo(x, y+h)
	dc.ClosePath()
}

func (dc *DrawContextImpl) DrawRoundedRectangle(x, y, w, h, r float64) {
	dc.MoveTo(x+r, y)
	dc.LineTo(x+w-r, y)
	dc.drawArc(x+w-r, y+r, r, -math.Pi/2, 0)
	dc.LineTo(x+w, y+h-r)
	dc.drawArc(x+w-r, y+h-r, r, 0, math.Pi/2)
	dc.LineTo(x+r, y+h)
	dc.drawArc(x+r, y+h-r, r, math.Pi/2, math.Pi)
	dc.LineTo(x, y+r)
	dc.drawArc(x+r, y+r, r, math.Pi, 3*math.Pi/2)
	dc.ClosePath()
}

func (dc *DrawContextImpl) drawArc(cx, cy, r, a1, a2 float64) {
	const n = 16
	for i := 0; i < n; i++ {
		t1 := float64(i) / float64(n)
		t2 := float64(i+1) / float64(n)
		aa1 := a1 + (a2-a1)*t1
		aa2 := a1 + (a2-a1)*t2
		x0 := cx + r*math.Cos(aa1)
		y0 := cy + r*math.Sin(aa1)
		xMid := cx + r*math.Cos((aa1+aa2)/2)
		yMid := cy + r*math.Sin((aa1+aa2)/2)
		x2 := cx + r*math.Cos(aa2)
		y2 := cy + r*math.Sin(aa2)
		qcx := 2*xMid - x0/2 - x2/2
		qcy := 2*yMid - y0/2 - y2/2
		if i == 0 {
			if dc.hasCurrent {
				dc.LineTo(x0, y0)
			} else {
				dc.MoveTo(x0, y0)
			}
		}
		dc.QuadraticTo(qcx, qcy, x2, y2)
	}
}

func (dc *DrawContextImpl) DrawCircle(x, y, r float64) {
	dc.drawArc(x, y, r, 0, 2*math.Pi)
	dc.ClosePath()
}

func (dc *DrawContextImpl) DrawLine(x1, y1, x2, y2 float64) {
	dc.MoveTo(x1, y1)
	dc.LineTo(x2, y2)
}

// --- Path rendering ---

// pathBounds computes the axis-aligned bounding box of the current path,
// clamped to the canvas. Returns the integer pixel rect with 1px padding.
func (dc *DrawContextImpl) pathBounds() image.Rectangle {
	minX, minY := math.MaxFloat64, math.MaxFloat64
	maxX, maxY := -math.MaxFloat64, -math.MaxFloat64
	expand := func(x, y float64) {
		if x < minX {
			minX = x
		}
		if x > maxX {
			maxX = x
		}
		if y < minY {
			minY = y
		}
		if y > maxY {
			maxY = y
		}
	}
	for _, seg := range dc.path {
		switch seg.op {
		case pathMoveTo, pathLineTo:
			expand(seg.args[0], seg.args[1])
		case pathQuadTo:
			expand(seg.args[0], seg.args[1])
			expand(seg.args[2], seg.args[3])
		case pathCubicTo:
			expand(seg.args[0], seg.args[1])
			expand(seg.args[2], seg.args[3])
			expand(seg.args[4], seg.args[5])
		}
	}
	// 1px padding for anti-aliasing, clamped to canvas.
	x0 := int(math.Floor(minX)) - 1
	y0 := int(math.Floor(minY)) - 1
	x1 := int(math.Ceil(maxX)) + 1
	y1 := int(math.Ceil(maxY)) + 1
	cb := dc.im.Bounds()
	if x0 < cb.Min.X {
		x0 = cb.Min.X
	}
	if y0 < cb.Min.Y {
		y0 = cb.Min.Y
	}
	if x1 > cb.Max.X {
		x1 = cb.Max.X
	}
	if y1 > cb.Max.Y {
		y1 = cb.Max.Y
	}
	return image.Rect(x0, y0, x1, y1)
}

// fillWithPattern rasterizes the current path as a fill onto dc.im using the given pattern.
func (dc *DrawContextImpl) fillWithPattern(pat Pattern) {
	if len(dc.path) == 0 {
		return
	}

	// Compute path bounding box so we only rasterize/composite the affected region.
	bbox := dc.pathBounds()
	bw, bh := bbox.Dx(), bbox.Dy()
	if bw <= 0 || bh <= 0 {
		return
	}
	// Clamp rasterizer size to the effective drawing area — the
	// intersection of the canvas bounds with the active clip rect.
	// This prevents huge rasterizer allocations when a clip restricts
	// the visible area to a small portion of the canvas.
	cb := dc.im.Bounds()
	if dc.gs.hasClipRect {
		cb = cb.Intersect(dc.gs.clipRect)
	}
	// Further intersect with the path bounding box itself.
	bbox = bbox.Intersect(cb)
	bw = bbox.Dx()
	bh = bbox.Dy()
	if bw <= 0 || bh <= 0 {
		return
	}
	ox, oy := float32(bbox.Min.X), float32(bbox.Min.Y)

	r := vector.NewRasterizer(bw, bh)
	// vector.Rasterizer uses non-zero winding by default; even-odd is not
	// directly supported, so we ignore the fill rule distinction here.

	var subStartX, subStartY float32
	for _, seg := range dc.path {
		switch seg.op {
		case pathMoveTo:
			subStartX = float32(seg.args[0]) - ox
			subStartY = float32(seg.args[1]) - oy
			r.MoveTo(subStartX, subStartY)
		case pathLineTo:
			r.LineTo(float32(seg.args[0])-ox, float32(seg.args[1])-oy)
		case pathQuadTo:
			r.QuadTo(float32(seg.args[0])-ox, float32(seg.args[1])-oy,
				float32(seg.args[2])-ox, float32(seg.args[3])-oy)
		case pathCubicTo:
			r.CubeTo(float32(seg.args[0])-ox, float32(seg.args[1])-oy,
				float32(seg.args[2])-ox, float32(seg.args[3])-oy,
				float32(seg.args[4])-ox, float32(seg.args[5])-oy)
		case pathClose:
			r.ClosePath()
		}
	}
	// Close any open subpath.
	if dc.hasCurrent {
		r.ClosePath()
	}

	// Rasterize into a bbox-sized alpha buffer.
	alpha := image.NewAlpha(image.Rect(0, 0, bw, bh))
	r.Draw(alpha, alpha.Bounds(), image.Opaque, image.Point{})

	// Apply clip: zero out coverage outside the clip region. clipRect is
	// the fast axis-aligned case; clipMask is the non-rectangular
	// clip-as-mask case (clip-path: circle()/ellipse()/polygon(), rounded
	// rects, arbitrary curves).
	if dc.gs.hasClipRect || dc.gs.clipMask != nil {
		cr := dc.gs.clipRect
		for py := 0; py < bh; py++ {
			for px := 0; px < bw; px++ {
				canvasX := px + bbox.Min.X
				canvasY := py + bbox.Min.Y
				if dc.gs.hasClipRect &&
					(canvasX < cr.Min.X || canvasX >= cr.Max.X ||
						canvasY < cr.Min.Y || canvasY >= cr.Max.Y) {
					alpha.Pix[py*alpha.Stride+px] = 0
					continue
				}
				if dc.gs.clipMask != nil && dc.clipMaskAt(canvasX, canvasY) == 0 {
					alpha.Pix[py*alpha.Stride+px] = 0
				}
			}
		}
	}

	// Composite only within the bounding box.
	for py := 0; py < bh; py++ {
		for px := 0; px < bw; px++ {
			a := alpha.Pix[py*alpha.Stride+px]
			if a == 0 {
				continue
			}
			canvasX, canvasY := px+bbox.Min.X, py+bbox.Min.Y
			src := pat.ColorAt(canvasX, canvasY)
			sr, sg, sb, sa := src.RGBA()
			// CSS rendering uses sharp pixel boundaries: any non-zero coverage
			// is treated as fully opaque. This prevents anti-aliased seams where
			// adjacent border polygons share a diagonal edge.
			_ = a
			coverage := uint32(255)
			sa = sa * coverage / 255
			sr = sr * coverage / 255
			sg = sg * coverage / 255
			sb = sb * coverage / 255
			// Porter-Duff Over onto dc.im.
			dst := dc.im.RGBAAt(canvasX, canvasY)
			dr, dg, db, da := uint32(dst.R), uint32(dst.G), uint32(dst.B), uint32(dst.A)
			sr8 := sr >> 8
			sg8 := sg >> 8
			sb8 := sb >> 8
			sa8 := sa >> 8
			inv := uint32(255) - sa8
			dc.im.SetRGBA(canvasX, canvasY, color.RGBA{
				R: uint8(sr8 + dr*inv/255),
				G: uint8(sg8 + dg*inv/255),
				B: uint8(sb8 + db*inv/255),
				A: uint8(sa8 + da*inv/255),
			})
		}
	}
}

func (dc *DrawContextImpl) Fill() {
	dc.fillWithPattern(dc.gs.fillPattern)
	dc.ClearPath()
}

func (dc *DrawContextImpl) FillPreserve() {
	dc.fillWithPattern(dc.gs.fillPattern)
}

// --- Stroke ---

// strokeExpand expands the stored path into a filled polygon representing the stroke.
// It discretizes curves to line segments, then expands each segment into a rectangle.
func (dc *DrawContextImpl) strokeExpand() []pathSeg {
	// Flatten path to line segments and build nextLine in one pass.
	// nextLine[i] is the index of the line that follows line i within the
	// same subpath, or -1 if line i is the last in an open subpath. For a
	// closed subpath, the last line wraps to the subpath's first line —
	// this is the wraparound join.
	var lines [][2][2]float64 // each: [start, end]
	var nextLine []int        // parallel to lines

	var curX, curY float64
	var subStartX, subStartY float64
	subStartLine := 0
	subAlreadyClosed := false // prevents double-finalization at end-of-loop
	hasPos := false

	appendLine := func(ax, ay, bx, by float64) {
		lines = append(lines, [2][2]float64{{ax, ay}, {bx, by}})
		nextLine = append(nextLine, len(lines)) // tentative: i+1; fixed up at subpath end
	}

	finalizeSubpath := func(closed bool) {
		if subAlreadyClosed || len(lines) == subStartLine {
			return
		}
		if closed {
			nextLine[len(lines)-1] = subStartLine
		} else {
			nextLine[len(lines)-1] = -1
		}
		subAlreadyClosed = true
	}

	flattenCubic := func(x0, y0, x1, y1, x2, y2, x3, y3 float64) {
		const steps = 32
		px, py := x0, y0
		for i := 1; i <= steps; i++ {
			t := float64(i) / float64(steps)
			mt := 1 - t
			nx := mt*mt*mt*x0 + 3*mt*mt*t*x1 + 3*mt*t*t*x2 + t*t*t*x3
			ny := mt*mt*mt*y0 + 3*mt*mt*t*y1 + 3*mt*t*t*y2 + t*t*t*y3
			appendLine(px, py, nx, ny)
			px, py = nx, ny
		}
	}

	flattenQuad := func(x0, y0, x1, y1, x2, y2 float64) {
		const steps = 16
		px, py := x0, y0
		for i := 1; i <= steps; i++ {
			t := float64(i) / float64(steps)
			mt := 1 - t
			nx := mt*mt*x0 + 2*mt*t*x1 + t*t*x2
			ny := mt*mt*y0 + 2*mt*t*y1 + t*t*y2
			appendLine(px, py, nx, ny)
			px, py = nx, ny
		}
	}

	for _, seg := range dc.path {
		switch seg.op {
		case pathMoveTo:
			finalizeSubpath(false) // finalize previous subpath as open
			curX, curY = seg.args[0], seg.args[1]
			subStartX, subStartY = curX, curY
			subStartLine = len(lines)
			subAlreadyClosed = false
			hasPos = true
		case pathLineTo:
			if hasPos {
				appendLine(curX, curY, seg.args[0], seg.args[1])
			}
			curX, curY = seg.args[0], seg.args[1]
		case pathQuadTo:
			if hasPos {
				flattenQuad(curX, curY, seg.args[0], seg.args[1], seg.args[2], seg.args[3])
			}
			curX, curY = seg.args[2], seg.args[3]
		case pathCubicTo:
			if hasPos {
				flattenCubic(curX, curY,
					seg.args[0], seg.args[1],
					seg.args[2], seg.args[3],
					seg.args[4], seg.args[5])
			}
			curX, curY = seg.args[4], seg.args[5]
		case pathClose:
			if hasPos {
				appendLine(curX, curY, subStartX, subStartY)
				finalizeSubpath(true)
			}
			curX, curY = subStartX, subStartY
			// Any line-producing op after Close (no intervening MoveTo)
			// starts an implicit subpath at the close point. Without
			// this reset, subAlreadyClosed=true would leave the new
			// subpath's nextLine entry at its tentative value (= len(lines)),
			// which then OOBs when emitJoin dereferences it.
			subStartX, subStartY = curX, curY
			subStartLine = len(lines)
			subAlreadyClosed = false
		}
	}
	finalizeSubpath(false) // finalize trailing subpath if it didn't end with Close

	if len(lines) == 0 {
		return nil
	}

	// prevLine[i] is the index of the line that precedes line i within
	// the same subpath, or -1 if line i is the first in a subpath. Used
	// (along with nextLine) to detect subpath endpoints for cap emission:
	// open-subpath endpoints get caps; closed-subpath lines never do
	// (their nextLine/prevLine always reference another line via wrap).
	prevLine := make([]int, len(lines))
	for i := range prevLine {
		prevLine[i] = -1
	}
	for i, n := range nextLine {
		if n >= 0 && n < len(lines) {
			prevLine[n] = i
		}
	}

	hw := dc.gs.lineWidth / 2
	var out []pathSeg

	addTri := func(a, b, c [2]float64) {
		out = append(out,
			pathSeg{op: pathMoveTo, args: [6]float64{a[0], a[1]}},
			pathSeg{op: pathLineTo, args: [6]float64{b[0], b[1]}},
			pathSeg{op: pathLineTo, args: [6]float64{c[0], c[1]}},
			pathSeg{op: pathClose},
		)
	}

	addQuad := func(p0, p1, p2, p3 [2]float64) {
		out = append(out,
			pathSeg{op: pathMoveTo, args: [6]float64{p0[0], p0[1]}},
			pathSeg{op: pathLineTo, args: [6]float64{p1[0], p1[1]}},
			pathSeg{op: pathLineTo, args: [6]float64{p2[0], p2[1]}},
			pathSeg{op: pathLineTo, args: [6]float64{p3[0], p3[1]}},
			pathSeg{op: pathClose},
		)
	}

	addCap := func(cx, cy, dx, dy float64, start bool) {
		// dx,dy is the segment direction (not normalized yet).
		len_ := math.Hypot(dx, dy)
		if len_ < 1e-10 {
			return
		}
		// Perpendicular unit vector.
		px := -dy / len_
		py := dx / len_

		switch dc.gs.lineCap {
		case LineCapButt:
			// no cap geometry needed — ends of rectangle already cover it
		case LineCapSquare:
			// Extend by hw in the segment direction.
			var ux, uy float64
			if start {
				ux, uy = -dx/len_*hw, -dy/len_*hw
			} else {
				ux, uy = dx/len_*hw, dy/len_*hw
			}
			p0 := [2]float64{cx + px*hw + ux, cy + py*hw + uy}
			p1 := [2]float64{cx - px*hw + ux, cy - py*hw + uy}
			p2 := [2]float64{cx - px*hw, cy - py*hw}
			p3 := [2]float64{cx + px*hw, cy + py*hw}
			addQuad(p0, p1, p2, p3)
		case LineCapRound:
			// Approximate semicircle with 16 triangles.
			const steps = 16
			var baseAngle float64
			if start {
				baseAngle = math.Atan2(dy, dx) + math.Pi
			} else {
				baseAngle = math.Atan2(dy, dx)
			}
			for i := 0; i < steps; i++ {
				a1 := baseAngle + math.Pi*float64(i)/float64(steps)
				a2 := baseAngle + math.Pi*float64(i+1)/float64(steps)
				out = append(out,
					pathSeg{op: pathMoveTo, args: [6]float64{cx, cy}},
					pathSeg{op: pathLineTo, args: [6]float64{cx + hw*math.Cos(a1), cy + hw*math.Sin(a1)}},
					pathSeg{op: pathLineTo, args: [6]float64{cx + hw*math.Cos(a2), cy + hw*math.Sin(a2)}},
					pathSeg{op: pathClose},
				)
			}
		}
	}

	// emitJoin fills the outer corner between lines[i] (incoming) and
	// lines[next] (outgoing). Bevel emits no patch — the existing
	// per-segment quad ends already meet at a chord-like seam.
	emitJoin := func(i, next int) {
		dx := lines[i][1][0] - lines[i][0][0]
		dy := lines[i][1][1] - lines[i][0][1]
		dlen := math.Hypot(dx, dy)
		nx := lines[next][1][0] - lines[next][0][0]
		ny := lines[next][1][1] - lines[next][0][1]
		nlen := math.Hypot(nx, ny)
		if dlen < 1e-10 || nlen < 1e-10 {
			return
		}
		P := [2]float64{lines[i][1][0], lines[i][1][1]}

		switch dc.gs.lineJoin {
		case LineJoinRound:
			// Normalize the sweep to the shortest arc so the ±π branch
			// cut of Atan2 doesn't turn a tiny corner into a ~360° fan.
			a1 := math.Atan2(dy, dx) - math.Pi/2
			a2 := math.Atan2(ny, nx) - math.Pi/2
			delta := a2 - a1
			if delta > math.Pi {
				delta -= 2 * math.Pi
			} else if delta < -math.Pi {
				delta += 2 * math.Pi
			}
			if delta < 0 {
				a1, delta = a1+delta, -delta // start at the other endpoint, sweep forward
			}
			const steps = 8
			for j := 0; j < steps; j++ {
				aa1 := a1 + delta*float64(j)/float64(steps)
				aa2 := a1 + delta*float64(j+1)/float64(steps)
				addTri(
					P,
					[2]float64{P[0] + hw*math.Cos(aa1), P[1] + hw*math.Sin(aa1)},
					[2]float64{P[0] + hw*math.Cos(aa2), P[1] + hw*math.Sin(aa2)},
				)
			}
		case LineJoinMiter:
			// Unit perpendiculars using the +perp convention from the
			// quad (rotates segment direction 90° math-CCW; image-CW
			// since y is down). dot(n1, n2) is invariant under joint
			// sign flip, so compute it from the pp's directly.
			pp1x, pp1y := -dy/dlen, dx/dlen
			pp2x, pp2y := -ny/nlen, nx/nlen
			dotN := pp1x*pp2x + pp1y*pp2y
			if dotN > 1 {
				dotN = 1
			} else if dotN < -1 {
				dotN = -1
			}
			sinHalf := math.Sqrt((1 + dotN) / 2)
			// Outer side: -perp when the cross product is positive
			// (right turn in image coords; raw cross sign suffices).
			sign := 1.0
			if dx*ny > dy*nx {
				sign = -1
			}
			n1x, n1y := sign*pp1x, sign*pp1y
			n2x, n2y := sign*pp2x, sign*pp2y
			A1 := [2]float64{P[0] + n1x*hw, P[1] + n1y*hw}
			A2 := [2]float64{P[0] + n2x*hw, P[1] + n2y*hw}
			// Fallback per SVG/CSS: degrade to bevel when miter length
			// (= hw / sinHalf) exceeds miterLimit · hw.
			if sinHalf*dc.gs.miterLimit < 1 || sinHalf < 1e-6 {
				addTri(A1, P, A2)
				return
			}
			// Miter point M = P + (n1+n2)·hw / (2·sinHalf²). Derivation:
			// bisector unit = (n1+n2)/||n1+n2||, ||n1+n2|| = 2·sinHalf,
			// miter length from pivot = hw / sinHalf.
			mScale := hw / (2 * sinHalf * sinHalf)
			M := [2]float64{P[0] + (n1x+n2x)*mScale, P[1] + (n1y+n2y)*mScale}
			// Kite {A1, P, A2, M} as two triangles sharing the pivot.
			addTri(P, A1, M)
			addTri(P, M, A2)
		}
	}

	for i, line := range lines {
		x0, y0 := line[0][0], line[0][1]
		x1, y1 := line[1][0], line[1][1]
		dx, dy := x1-x0, y1-y0
		length := math.Hypot(dx, dy)
		if length < 1e-10 {
			continue
		}
		// Perpendicular unit vector scaled to half-width.
		px := -dy / length * hw
		py := dx / length * hw

		p0 := [2]float64{x0 + px, y0 + py}
		p1 := [2]float64{x0 - px, y0 - py}
		p2 := [2]float64{x1 - px, y1 - py}
		p3 := [2]float64{x1 + px, y1 + py}
		addQuad(p0, p1, p2, p3)

		// Caps at open-subpath endpoints only. Closed subpaths never
		// trigger a cap because the wrap fills both nextLine and prevLine.
		if prevLine[i] == -1 {
			addCap(x0, y0, dx, dy, true)
		}
		if nextLine[i] == -1 {
			addCap(x1, y1, dx, dy, false)
		}

		if next := nextLine[i]; next >= 0 {
			emitJoin(i, next)
		}
	}

	return out
}

func (dc *DrawContextImpl) Stroke() {
	dc.strokeWithPattern(dc.gs.strokePattern)
	dc.ClearPath()
}

func (dc *DrawContextImpl) StrokePreserve() {
	dc.strokeWithPattern(dc.gs.strokePattern)
}

func (dc *DrawContextImpl) strokeWithPattern(pat Pattern) {
	if len(dc.path) == 0 {
		return
	}
	expanded := dc.strokeExpand()
	if len(expanded) == 0 {
		return
	}

	// Temporarily replace path with expanded stroke path and fill it.
	saved := dc.path
	savedHasCurrent := dc.hasCurrent
	dc.path = expanded
	dc.hasCurrent = false
	dc.fillWithPattern(pat)
	dc.path = saved
	dc.hasCurrent = savedHasCurrent
}

// --- Fast-path fill ---

// FillRectangle fills an axis-aligned rectangle by writing pixels directly,
// bypassing the rasterizer. Uses the current fill pattern.
func (dc *DrawContextImpl) FillRectangle(x, y, w, h float64) {
	tx0, ty0 := dc.transformPoint(x, y)
	tx1, ty1 := dc.transformPoint(x+w, y+h)
	if tx0 > tx1 {
		tx0, tx1 = tx1, tx0
	}
	if ty0 > ty1 {
		ty0, ty1 = ty1, ty0
	}
	x0 := int(math.Round(tx0))
	y0 := int(math.Round(ty0))
	x1 := int(math.Round(tx1))
	y1 := int(math.Round(ty1))

	bounds := dc.im.Bounds()
	if dc.gs.hasClipRect {
		bounds = bounds.Intersect(dc.gs.clipRect)
	}
	if x0 < bounds.Min.X {
		x0 = bounds.Min.X
	}
	if y0 < bounds.Min.Y {
		y0 = bounds.Min.Y
	}
	if x1 > bounds.Max.X {
		x1 = bounds.Max.X
	}
	if y1 > bounds.Max.Y {
		y1 = bounds.Max.Y
	}

	pat := dc.gs.fillPattern
	// The xdraw.Draw fast path fills a whole rectangle and cannot honour a
	// per-pixel clip mask; skip it when a non-rectangular clip is active.
	if sp, ok := pat.(*solidPattern); ok && dc.gs.clipMask == nil {
		// Solid color fast path.
		r, g, b, a := sp.c.RGBA()
		// Convert from 0xffff to 0xff pre-multiplied.
		rc := uint8(r >> 8)
		gc := uint8(g >> 8)
		bc := uint8(b >> 8)
		ac := uint8(a >> 8)
		src := color.RGBA{R: rc, G: gc, B: bc, A: ac}
		rect := image.Rect(x0, y0, x1, y1)
		xdraw.Draw(dc.im, rect, image.NewUniform(src), image.Point{}, xdraw.Over)
		return
	}

	for py := y0; py < y1; py++ {
		for px := x0; px < x1; px++ {
			if dc.gs.clipMask != nil && dc.clipMaskAt(px, py) == 0 {
				continue
			}
			c := pat.ColorAt(px-x0, py-y0)
			cr, cg, cb, ca := c.RGBA()
			if ca == 0 {
				continue
			}
			// Porter-Duff Over.
			cr8 := uint8(cr >> 8)
			cg8 := uint8(cg >> 8)
			cb8 := uint8(cb >> 8)
			ca8 := uint8(ca >> 8)
			inv := uint32(255 - ca8)
			dst := dc.im.RGBAAt(px, py)
			dc.im.SetRGBA(px, py, color.RGBA{
				R: uint8(uint32(cr8) + uint32(dst.R)*inv/255),
				G: uint8(uint32(cg8) + uint32(dst.G)*inv/255),
				B: uint8(uint32(cb8) + uint32(dst.B)*inv/255),
				A: uint8(uint32(ca8) + uint32(dst.A)*inv/255),
			})
		}
	}
}

// --- Text ---

// OpenFont loads a font via the internal TextLayout and caches its metrics.
func (dc *DrawContextImpl) OpenFont(family string, variant, size int32) (FontMetrics, error) {
	m, err := dc.tl.OpenFont(OpenFontRequest{
		Family:  family,
		Variant: variant,
		Size:    size,
	})
	if err != nil {
		return FontMetrics{}, err
	}
	dc.cacheFontMetrics(m)
	return m, nil
}

// OpenTemporaryFont forwards to the underlying TextLayout's temp-pool
// open. Caches the returned metrics the same way OpenFont does so
// subsequent GetFontMetrics(fontID) calls work uniformly. Callers must
// pair with CloseTemporaryFont once the render scope ends; see the
// [DrawContext.OpenTemporaryFont] doc comment for permanent-fontID
// no-op semantics.
func (dc *DrawContextImpl) OpenTemporaryFont(family string, variant, size int32) (FontMetrics, error) {
	m, err := dc.tl.OpenTemporaryFont(OpenFontRequest{
		Family:  family,
		Variant: variant,
		Size:    size,
	})
	if err != nil {
		return FontMetrics{}, err
	}
	dc.cacheFontMetrics(m)
	return m, nil
}

// CloseTemporaryFont releases a temporary fontID previously returned by
// OpenTemporaryFont. Forwards to the TextLayout (and through to the
// provider). The metrics cache entry is left in place — permanent-range
// fontIDs may still be reused by other callers, and temp-range entries
// will be overwritten on the next OpenTemporaryFont with the same ID.
func (dc *DrawContextImpl) CloseTemporaryFont(fontID int32) error {
	return dc.tl.CloseTemporaryFont(fontID)
}

// cacheFontMetrics stores metrics in the per-fontID cache so
// GetFontMetrics works without going back through the TextLayout.
// Bounds-checked; out-of-range fontIDs (e.g. temp-pool 0x1000+ once
// the real IPC lands) silently skip cache population — those callers
// will pay a small lookup cost via tl.CachedFontMetrics fallback.
func (dc *DrawContextImpl) cacheFontMetrics(m FontMetrics) {
	if m.FontID >= 0 && int(m.FontID) < len(dc.metrics) {
		cp := m
		dc.metrics[m.FontID] = &cp
	}
}

// RegisterBuffer forwards a font buffer registration to the underlying
// TextLayout / GlyphProvider. Used for CSS @font-face fonts.
func (dc *DrawContextImpl) RegisterBuffer(family string, variant int32, data []byte) error {
	return dc.tl.RegisterBuffer(family, variant, data)
}

// DrawText shapes, rasterizes, and composites text at (x, y) baseline.
func (dc *DrawContextImpl) DrawText(text string, fontID int32, x, y float64) {
	tx, ty := dc.transformPoint(x, y)

	// Decompose tx into integer-pixel origin and fractional sub-pixel carry.
	// Using Floor for the origin keeps the fractional remainder in [0, 1),
	// giving a StartPenX in [0, 63] fixed.Int26_6 units. This matches Blink's
	// sub-pixel pen accumulation: each glyph's position is
	//   floor(tx) + floor((StartPenX + penX + XOffset) / 64)
	// which equals floor(tx + (penX + XOffset) / 64) — pixel-perfect regardless
	// of how the line's text is split across inline runs.
	txFloor := math.Floor(tx)
	fracPenX := int32(math.Round((tx - txFloor) * 64))

	run, err := dc.tl.LayoutText(ShapingParams{
		Text:      text,
		FontID:    fontID,
		Direction: LTR,
		Script:    ScriptLatin,
		StartPenX: fracPenX,
	})
	if err != nil || run == nil {
		return
	}

	// Decompose current color into RGBA components.
	fr, fg, fb, fa := dc.gs.color.RGBA()
	// Values are 0..0xffff; we want 0..255.
	fR := uint8(fr >> 8)
	fG := uint8(fg >> 8)
	fB := uint8(fb >> 8)
	fA := uint8(fa >> 8)

	bounds := dc.im.Bounds()

	for _, g := range run.Glyphs {
		if len(g.Alpha) == 0 {
			continue
		}
		originX := int(txFloor) + int(g.X)
		originY := int(math.Round(ty)) + int(g.Y)
		w := int(g.Width)
		h := int(g.Height)

		for py := 0; py < h; py++ {
			for px := 0; px < w; px++ {
				dstX := originX + px
				dstY := originY + py
				if dstX < bounds.Min.X || dstX >= bounds.Max.X ||
					dstY < bounds.Min.Y || dstY >= bounds.Max.Y {
					continue
				}
				mask := g.Alpha[py*w+px]
				if mask == 0 {
					continue
				}

				// Apply clip rect.
				if dc.gs.hasClipRect {
					if dstX < dc.gs.clipRect.Min.X || dstX >= dc.gs.clipRect.Max.X ||
						dstY < dc.gs.clipRect.Min.Y || dstY >= dc.gs.clipRect.Max.Y {
						continue
					}
				}
				// Apply non-rectangular clip mask.
				if dc.gs.clipMask != nil && dc.clipMaskAt(dstX, dstY) == 0 {
					continue
				}

				// Blend: src = foreground color * (mask/255) * (fA/255)
				// Use Porter-Duff Over.
				coverage := uint32(mask) * uint32(fA) / 255
				if coverage == 0 {
					continue
				}
				sR := uint32(fR) * coverage / 255
				sG := uint32(fG) * coverage / 255
				sB := uint32(fB) * coverage / 255
				sA := coverage

				dst := dc.im.RGBAAt(dstX, dstY)
				inv := uint32(255) - sA
				dc.im.SetRGBA(dstX, dstY, color.RGBA{
					R: uint8(sR + uint32(dst.R)*inv/255),
					G: uint8(sG + uint32(dst.G)*inv/255),
					B: uint8(sB + uint32(dst.B)*inv/255),
					A: uint8(sA + uint32(dst.A)*inv/255),
				})
			}
		}
	}
}

// DrawStringAnchored draws text anchored at (x, y) with anchor point
// (ax, ay). ax controls horizontal alignment: 0=left, 0.5=center, 1=right.
// ay controls vertical alignment: 0=top of ascent, 0.5=center, 1=bottom of descent.
func (dc *DrawContextImpl) DrawStringAnchored(text string, fontID int32, x, y, ax, ay float64) {
	w := dc.MeasureText(text, fontID)
	m := dc.GetFontMetrics(fontID)
	ascent := float64(m.Ascent) / 64.0
	descent := float64(m.Descent) / 64.0
	h := ascent + descent

	// Baseline position: when ay=0, baseline = y + ascent (top-aligned).
	// When ay=1, baseline = y - descent (bottom-aligned).
	bx := x - ax*w
	by := y + ascent - ay*h
	dc.DrawText(text, fontID, bx, by)
}

// MeasureText returns the advance width of text in pixels.
func (dc *DrawContextImpl) MeasureText(text string, fontID int32) float64 {
	adv, err := dc.tl.MeasureText(ShapingParams{
		Text:      text,
		FontID:    fontID,
		Direction: LTR,
		Script:    ScriptLatin,
	})
	if err != nil {
		return 0
	}
	return float64(adv) / 64.0
}

// DrawTextWithFeatures is like DrawText but applies OpenType features during shaping.
func (dc *DrawContextImpl) DrawTextWithFeatures(text string, fontID int32, x, y float64, features []FontFeature) {
	if len(features) == 0 {
		dc.DrawText(text, fontID, x, y)
		return
	}

	tx, ty := dc.transformPoint(x, y)
	txFloor := math.Floor(tx)
	fracPenX := int32(math.Round((tx - txFloor) * 64))

	run, err := dc.tl.LayoutText(ShapingParams{
		Text:      text,
		FontID:    fontID,
		Direction: LTR,
		Script:    ScriptLatin,
		StartPenX: fracPenX,
		Features:  features,
	})
	if err != nil || run == nil {
		return
	}

	fr, fg, fb, fa := dc.gs.color.RGBA()
	fR := uint8(fr >> 8)
	fG := uint8(fg >> 8)
	fB := uint8(fb >> 8)
	fA := uint8(fa >> 8)

	bounds := dc.im.Bounds()

	for _, g := range run.Glyphs {
		if len(g.Alpha) == 0 {
			continue
		}
		originX := int(txFloor) + int(g.X)
		originY := int(math.Round(ty)) + int(g.Y)
		w := int(g.Width)
		h := int(g.Height)

		for py := 0; py < h; py++ {
			for px := 0; px < w; px++ {
				dstX := originX + px
				dstY := originY + py
				if dstX < bounds.Min.X || dstX >= bounds.Max.X ||
					dstY < bounds.Min.Y || dstY >= bounds.Max.Y {
					continue
				}
				mask := g.Alpha[py*w+px]
				if mask == 0 {
					continue
				}

				if dc.gs.hasClipRect {
					if dstX < dc.gs.clipRect.Min.X || dstX >= dc.gs.clipRect.Max.X ||
						dstY < dc.gs.clipRect.Min.Y || dstY >= dc.gs.clipRect.Max.Y {
						continue
					}
				}
				if dc.gs.clipMask != nil && dc.clipMaskAt(dstX, dstY) == 0 {
					continue
				}

				off := (dstY-bounds.Min.Y)*dc.im.Stride + (dstX-bounds.Min.X)*4
				dR := dc.im.Pix[off+0]
				dG := dc.im.Pix[off+1]
				dB := dc.im.Pix[off+2]
				dA := dc.im.Pix[off+3]

				srcA := uint32(fA) * uint32(mask) / 255
				invA := 255 - srcA
				dc.im.Pix[off+0] = uint8((uint32(fR)*srcA + uint32(dR)*invA) / 255)
				dc.im.Pix[off+1] = uint8((uint32(fG)*srcA + uint32(dG)*invA) / 255)
				dc.im.Pix[off+2] = uint8((uint32(fB)*srcA + uint32(dB)*invA) / 255)
				dc.im.Pix[off+3] = uint8((srcA + uint32(dA)*invA/255))
			}
		}
	}
}

// MeasureTextWithFeatures is like MeasureText but applies OpenType features during shaping.
func (dc *DrawContextImpl) MeasureTextWithFeatures(text string, fontID int32, features []FontFeature) float64 {
	if len(features) == 0 {
		return dc.MeasureText(text, fontID)
	}
	adv, err := dc.tl.MeasureText(ShapingParams{
		Text:     text,
		FontID:   fontID,
		Direction: LTR,
		Script:   ScriptLatin,
		Features: features,
	})
	if err != nil {
		return 0
	}
	return float64(adv) / 64.0
}

// GetFontMetrics returns the cached metrics for a previously opened font.
func (dc *DrawContextImpl) GetFontMetrics(fontID int32) FontMetrics {
	if fontID >= 0 && int(fontID) < len(dc.metrics) && dc.metrics[fontID] != nil {
		return *dc.metrics[fontID]
	}
	return FontMetrics{}
}

// TextLayout returns the underlying TextLayout for direct access to
// shaping and measurement.
func (dc *DrawContextImpl) TextLayout() TextLayout {
	return dc.tl
}

// --- Compositing groups ---

// PushGroup saves the current image buffer and starts drawing onto a new
// transparent offscreen buffer of the same size.
func (dc *DrawContextImpl) PushGroup() {
	dc.groupStack = append(dc.groupStack, groupEntry{im: dc.im})
	dc.im = image.NewRGBA(image.Rect(0, 0, dc.width, dc.height))
}

// PopGroup composites the offscreen group buffer onto the parent surface
// at full opacity.
func (dc *DrawContextImpl) PopGroup() {
	dc.PopGroupWithAlpha(1.0)
}

// PopGroupWithAlpha composites the offscreen group buffer onto the parent
// surface with the given opacity.
func (dc *DrawContextImpl) PopGroupWithAlpha(opacity float64) {
	n := len(dc.groupStack)
	if n == 0 {
		return
	}
	entry := dc.groupStack[n-1]
	dc.groupStack = dc.groupStack[:n-1]

	group := dc.im

	if opacity < 1.0 {
		bounds := group.Bounds()
		for py := bounds.Min.Y; py < bounds.Max.Y; py++ {
			rowStart := group.PixOffset(bounds.Min.X, py)
			rowEnd := rowStart + (bounds.Max.X-bounds.Min.X)*4
			for i := rowStart; i < rowEnd; i += 4 {
				if group.Pix[i+3] > 0 {
					group.Pix[i] = uint8(float64(group.Pix[i]) * opacity)
					group.Pix[i+1] = uint8(float64(group.Pix[i+1]) * opacity)
					group.Pix[i+2] = uint8(float64(group.Pix[i+2]) * opacity)
					group.Pix[i+3] = uint8(float64(group.Pix[i+3]) * opacity)
				}
			}
		}
	}

	// Restore parent buffer and composite group onto it.
	dc.im = entry.im
	xdraw.Draw(dc.im, dc.im.Bounds(), group, image.Point{}, xdraw.Over)
}

// --- Image drawing ---

// DrawImage draws the image at integer coordinates (x, y).
func (dc *DrawContextImpl) DrawImage(im image.Image, x, y int) {
	dc.DrawImageAnchored(im, x, y, 0, 0)
}

// DrawImageAnchored draws the image, offsetting by -ax*w and -ay*h from (x,y).
// Uses BiLinear transform when the matrix has rotation/scale; plain draw.Draw otherwise.
func (dc *DrawContextImpl) DrawImageAnchored(im image.Image, x, y int, ax, ay float64) {
	s := im.Bounds().Size()
	ox := x - int(ax*float64(s.X))
	oy := y - int(ay*float64(s.Y))

	fx, fy := float64(ox), float64(oy)
	m := dc.gs.matrix.Translate(fx, fy)

	// Check if matrix is identity-like (pure translation).
	isIdentityScale := math.Abs(m.XX-1) < 1e-9 && math.Abs(m.YY-1) < 1e-9 &&
		math.Abs(m.XY) < 1e-9 && math.Abs(m.YX) < 1e-9

	if isIdentityScale {
		// Fast path: plain pixel copy.
		origMin := image.Pt(int(math.Round(m.X0)), int(math.Round(m.Y0)))
		dst := image.Rect(origMin.X, origMin.Y, origMin.X+s.X, origMin.Y+s.Y)
		if dc.gs.hasClipRect {
			dst = dst.Intersect(dc.gs.clipRect)
		}
		// Adjust source point to account for any clipping offset so that
		// the source-to-destination alignment is preserved after clipping.
		sp := image.Pt(
			im.Bounds().Min.X+(dst.Min.X-origMin.X),
			im.Bounds().Min.Y+(dst.Min.Y-origMin.Y),
		)
		if dc.gs.clipMask != nil {
			// Per-pixel masked Porter-Duff Over — the rectangular xdraw.Draw
			// cannot honour a non-rectangular clip.
			for py := dst.Min.Y; py < dst.Max.Y; py++ {
				for px := dst.Min.X; px < dst.Max.X; px++ {
					if dc.clipMaskAt(px, py) == 0 {
						continue
					}
					sr, sg, sb, sa := im.At(sp.X+(px-dst.Min.X), sp.Y+(py-dst.Min.Y)).RGBA()
					if sa == 0 {
						continue
					}
					sr8, sg8, sb8, sa8 := sr>>8, sg>>8, sb>>8, sa>>8
					inv := uint32(255) - sa8
					d := dc.im.RGBAAt(px, py)
					dc.im.SetRGBA(px, py, color.RGBA{
						R: uint8(sr8 + uint32(d.R)*inv/255),
						G: uint8(sg8 + uint32(d.G)*inv/255),
						B: uint8(sb8 + uint32(d.B)*inv/255),
						A: uint8(sa8 + uint32(d.A)*inv/255),
					})
				}
			}
			return
		}
		xdraw.Draw(dc.im, dst, im, sp, xdraw.Over)
		return
	}

	s2d := f64.Aff3{m.XX, m.XY, m.X0, m.YX, m.YY, m.Y0}
	if dc.gs.clipMask != nil {
		// Transform into a scratch buffer, then composite through the clip
		// mask. xdraw.BiLinear.Transform cannot honour a per-pixel clip.
		scratch := image.NewRGBA(dc.im.Bounds())
		xdraw.BiLinear.Transform(scratch, s2d, im, im.Bounds(), xdraw.Over, nil)
		b := dc.im.Bounds()
		for py := b.Min.Y; py < b.Max.Y; py++ {
			for px := b.Min.X; px < b.Max.X; px++ {
				if dc.clipMaskAt(px, py) == 0 {
					continue
				}
				sc := scratch.RGBAAt(px, py)
				if sc.A == 0 {
					continue
				}
				inv := uint32(255) - uint32(sc.A)
				d := dc.im.RGBAAt(px, py)
				dc.im.SetRGBA(px, py, color.RGBA{
					R: uint8(uint32(sc.R) + uint32(d.R)*inv/255),
					G: uint8(uint32(sc.G) + uint32(d.G)*inv/255),
					B: uint8(uint32(sc.B) + uint32(d.B)*inv/255),
					A: uint8(uint32(sc.A) + uint32(d.A)*inv/255),
				})
			}
		}
		return
	}
	xdraw.BiLinear.Transform(dc.im, s2d, im, im.Bounds(), xdraw.Over, nil)
}
