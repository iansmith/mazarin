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
	fillRule      FillRule
	dashes        []float64
	clipMask      *image.Alpha          // nil means no clipping
	clipRect      image.Rectangle       // bounding box of clip (valid when clipMask != nil)
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
	metrics [maxFonts]*FontMetrics
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

// Clip rasterizes the current path into a clip mask and intersects it with
// any existing clip mask, then clears the path.
func (dc *DrawContextImpl) Clip() {
	if len(dc.path) == 0 {
		return
	}
	r := vector.NewRasterizer(dc.width, dc.height)
	for _, seg := range dc.path {
		switch seg.op {
		case pathMoveTo:
			r.MoveTo(float32(seg.args[0]), float32(seg.args[1]))
		case pathLineTo:
			r.LineTo(float32(seg.args[0]), float32(seg.args[1]))
		case pathQuadTo:
			r.QuadTo(float32(seg.args[0]), float32(seg.args[1]),
				float32(seg.args[2]), float32(seg.args[3]))
		case pathCubicTo:
			r.CubeTo(float32(seg.args[0]), float32(seg.args[1]),
				float32(seg.args[2]), float32(seg.args[3]),
				float32(seg.args[4]), float32(seg.args[5]))
		case pathClose:
			r.ClosePath()
		}
	}
	if dc.hasCurrent {
		r.ClosePath()
	}
	newMask := image.NewAlpha(image.Rect(0, 0, dc.width, dc.height))
	r.Draw(newMask, newMask.Bounds(), image.Opaque, image.Point{})

	// Intersect with existing clip mask if present.
	if dc.gs.clipMask != nil {
		b := newMask.Bounds()
		for py := b.Min.Y; py < b.Max.Y; py++ {
			for px := b.Min.X; px < b.Max.X; px++ {
				a := newMask.AlphaAt(px, py).A
				if a == 0 {
					continue
				}
				existing := dc.gs.clipMask.AlphaAt(px, py).A
				newMask.SetAlpha(px, py, color.Alpha{A: uint8(uint32(a) * uint32(existing) / 255)})
			}
		}
	}
	dc.gs.clipMask = newMask

	// Compute axis-aligned bounding box of non-zero alpha pixels.
	// For common rectangular clips this gives an exact tight rect.
	b := newMask.Bounds()
	minX, minY, maxX, maxY := b.Max.X, b.Max.Y, b.Min.X, b.Min.Y
	for py := b.Min.Y; py < b.Max.Y; py++ {
		for px := b.Min.X; px < b.Max.X; px++ {
			if newMask.AlphaAt(px, py).A > 0 {
				if px < minX {
					minX = px
				}
				if px+1 > maxX {
					maxX = px + 1
				}
				if py < minY {
					minY = py
				}
				if py+1 > maxY {
					maxY = py + 1
				}
			}
		}
	}
	dc.gs.clipRect = image.Rect(minX, minY, maxX, maxY)
	dc.gs.hasClipRect = minX < maxX && minY < maxY

	dc.ClearPath()
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

	// Apply clip mask: zero out coverage outside the clip region.
	if dc.gs.clipMask != nil {
		for py := 0; py < bh; py++ {
			for px := 0; px < bw; px++ {
				ca := alpha.Pix[py*alpha.Stride+px]
				if ca == 0 {
					continue
				}
				cm := dc.gs.clipMask.AlphaAt(px+bbox.Min.X, py+bbox.Min.Y).A
				if cm == 0 {
					alpha.Pix[py*alpha.Stride+px] = 0
				} else if cm < 255 {
					alpha.Pix[py*alpha.Stride+px] = uint8(uint32(ca) * uint32(cm) / 255)
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
	// First flatten path to line segments.
	var lines [][2][2]float64 // each: [start, end]

	var curX, curY float64
	var subStartX, subStartY float64
	hasPos := false

	flattenCubic := func(x0, y0, x1, y1, x2, y2, x3, y3 float64) {
		const steps = 32
		px, py := x0, y0
		for i := 1; i <= steps; i++ {
			t := float64(i) / float64(steps)
			mt := 1 - t
			nx := mt*mt*mt*x0 + 3*mt*mt*t*x1 + 3*mt*t*t*x2 + t*t*t*x3
			ny := mt*mt*mt*y0 + 3*mt*mt*t*y1 + 3*mt*t*t*y2 + t*t*t*y3
			lines = append(lines, [2][2]float64{{px, py}, {nx, ny}})
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
			lines = append(lines, [2][2]float64{{px, py}, {nx, ny}})
			px, py = nx, ny
		}
	}

	for _, seg := range dc.path {
		switch seg.op {
		case pathMoveTo:
			curX, curY = seg.args[0], seg.args[1]
			subStartX, subStartY = curX, curY
			hasPos = true
		case pathLineTo:
			if hasPos {
				lines = append(lines, [2][2]float64{{curX, curY}, {seg.args[0], seg.args[1]}})
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
				lines = append(lines, [2][2]float64{{curX, curY}, {subStartX, subStartY}})
			}
			curX, curY = subStartX, subStartY
		}
	}

	if len(lines) == 0 {
		return nil
	}

	hw := dc.gs.lineWidth / 2

	// Build expanded path segments. Each line segment becomes a quad (4 vertices).
	// We collect all quads as individual MoveTo+LineTo+ClosePath sequences.
	var out []pathSeg

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

		// Add caps at start of first segment and end of last segment.
		if i == 0 {
			addCap(x0, y0, dx, dy, true)
		}
		if i == len(lines)-1 {
			addCap(x1, y1, dx, dy, false)
		}

		// Round joins between adjacent segments.
		if i < len(lines)-1 && dc.gs.lineJoin == LineJoinRound {
			next := lines[i+1]
			nx, ny := next[1][0]-next[0][0], next[1][1]-next[0][1]
			nlen := math.Hypot(nx, ny)
			if nlen > 1e-10 {
				// Join at x1,y1: fill the angle between current end and next start.
				a1 := math.Atan2(dy/length, dx/length) - math.Pi/2
				a2 := math.Atan2(ny/nlen, nx/nlen) - math.Pi/2
				if a2 < a1 {
					a1, a2 = a2, a1
				}
				const steps = 8
				for j := 0; j < steps; j++ {
					aa1 := a1 + (a2-a1)*float64(j)/float64(steps)
					aa2 := a1 + (a2-a1)*float64(j+1)/float64(steps)
					out = append(out,
						pathSeg{op: pathMoveTo, args: [6]float64{x1, y1}},
						pathSeg{op: pathLineTo, args: [6]float64{x1 + hw*math.Cos(aa1), y1 + hw*math.Sin(aa1)}},
						pathSeg{op: pathLineTo, args: [6]float64{x1 + hw*math.Cos(aa2), y1 + hw*math.Sin(aa2)}},
						pathSeg{op: pathClose},
					)
				}
			}
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
	if sp, ok := pat.(*solidPattern); ok {
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
func (dc *DrawContextImpl) OpenFont(path string, variant, size int32) (FontMetrics, error) {
	m, err := dc.tl.OpenFont(OpenFontRequest{
		Path:    path,
		Variant: variant,
		Size:    size,
	})
	if err != nil {
		return FontMetrics{}, err
	}
	if m.FontID >= 0 && int(m.FontID) < len(dc.metrics) {
		cp := m
		dc.metrics[m.FontID] = &cp
	}
	return m, nil
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

				// Apply clip mask.
				if dc.gs.clipMask != nil {
					cm := dc.gs.clipMask.AlphaAt(dstX, dstY).A
					if cm == 0 {
						continue
					}
					mask = uint8(uint32(mask) * uint32(cm) / 255)
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
		dst := image.Rect(int(math.Round(m.X0)), int(math.Round(m.Y0)),
			int(math.Round(m.X0))+s.X, int(math.Round(m.Y0))+s.Y)
		if dc.gs.hasClipRect {
			dst = dst.Intersect(dc.gs.clipRect)
		}
		xdraw.Draw(dc.im, dst, im, im.Bounds().Min, xdraw.Over)
		return
	}

	s2d := f64.Aff3{m.XX, m.XY, m.X0, m.YX, m.YY, m.Y0}
	xdraw.BiLinear.Transform(dc.im, s2d, im, im.Bounds(), xdraw.Over, nil)
}
