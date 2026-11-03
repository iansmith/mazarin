package textshape

import (
	"cmp"
	"image"
	"math"
	"slices"
)

// Edge coverage for path fills and clips.
//
// Anti-aliased coverage is the exact area of each pixel the path covers
// (areaCoverage, an accumulation rasterizer) — the analogue of Skia's
// analytic AA. Non-anti-aliased coverage mirrors Skia's non-AA scan
// converter (SkScan_Path.cpp walk_edges / SkEdge::setLine): every edge is
// sampled at pixel-centre scanlines, and edge positions round half up
// (SkFDot6Round, SkFixedRoundToInt), so pixel (x, y) is painted iff its
// centre (x+½, y+½) satisfies top < y+½ ≤ bottom and left < x+½ ≤ right.

// skRoundToInt rounds half up, as SkScalarRoundToInt / SkFixedRoundToInt.
func skRoundToInt(v float64) int { return int(math.Floor(v + 0.5)) }

// onPixelGrid reports whether a device coordinate lies on a pixel boundary
// (to within float noise from a transform), where analytic coverage and
// whole-pixel rounding agree.
func onPixelGrid(v float64) bool { return math.Abs(v-math.Round(v)) < 1e-9 }

// mulCoverage multiplies two 8-bit coverages (255 is the identity).
func mulCoverage(a, b uint8) uint8 { return uint8((uint32(a)*uint32(b) + 127) / 255) }

// scaleByCoverage scales a 16-bit premultiplied colour by 8-bit coverage.
func scaleByCoverage(r, g, b, a uint32, cov uint8) (uint32, uint32, uint32, uint32) {
	if cov == 255 {
		return r, g, b, a
	}
	k := uint32(cov)
	return r * k / 255, g * k / 255, b * k / 255, a * k / 255
}

// pathCoverage rasterizes the current path into a coverage buffer covering
// bbox (canvas coordinates; the buffer's origin is bbox.Min). antiAlias
// selects analytic area coverage or Skia's non-AA centre sampling.
func (dc *DrawContextImpl) pathCoverage(bbox image.Rectangle, antiAlias bool) *image.Alpha {
	ox, oy := float32(bbox.Min.X), float32(bbox.Min.Y)
	return edgeCoverage(flattenPath(dc.path, ox, oy), bbox.Dx(), bbox.Dy(), antiAlias)
}

// edgeCoverage rasterizes closed polygon edges (buffer coordinates) into a
// w×h coverage buffer: exact area coverage (areaCoverage) when antiAlias,
// else Skia's non-AA centre sampling (scanCentres).
func edgeCoverage(edges []edgeLine, w, h int, antiAlias bool) *image.Alpha {
	cov := image.NewAlpha(image.Rect(0, 0, max(w, 0), max(h, 0)))
	if w <= 0 || h <= 0 {
		return cov
	}
	if antiAlias {
		areaCoverage(cov, edges)
	} else {
		scanCentres(cov, edges)
	}
	return cov
}

// areaCoverage fills cov with each pixel's exact covered area under the
// non-zero rule, clamped to 1: every edge deposits its signed area into an
// accumulation buffer, whose running sum is the coverage. This is the
// floating-point algorithm of x/image/vector's Rasterizer
// (raster_floating.go floatingLineTo / floatingAccumulateOpSrc), in
// float64. The Rasterizer itself switches to a fixed-point variant for
// buffers up to 512px, which misplaces edge coverage by up to ⅛ of a
// pixel and so depends on where a shape lands; analytic AA must not.
func areaCoverage(cov *image.Alpha, edges []edgeLine) {
	w, h := cov.Rect.Dx(), cov.Rect.Dy()
	// One flat buffer: deposits clamped to x == w land on the next row's
	// first cell, where the running sum cancels them, as in vector.
	buf := make([]float64, w*h+1)
	clampX := func(x int) int { return min(max(x, 0), w) }
	for _, e := range edges {
		dir := float64(e.winding)
		ax, ay, bx, by := e.x0, e.y0, e.x1, e.y1
		if by-ay <= 1e-6 {
			continue
		}
		dxdy := (bx - ax) / (by - ay)
		// Rows above the buffer only move x: start at the first visible one.
		y0 := max(int(math.Floor(ay)), 0)
		x := ax + (math.Max(float64(y0), ay)-ay)*dxdy
		for y := y0; y < min(int(math.Ceil(by)), h); y++ {
			dy := math.Min(float64(y+1), by) - math.Max(float64(y), ay)
			xNext := x + dy*dxdy
			row := buf[y*w:]
			d := dy * dir
			x0, x1 := min(x, xNext), max(x, xNext)
			x0i, x1i := int(math.Floor(x0)), int(math.Ceil(x1))
			if x1i <= x0i+1 {
				xmf := 0.5*(x+xNext) - float64(x0i)
				row[clampX(x0i)] += d - d*xmf
				row[clampX(x0i+1)] += d * xmf
			} else {
				s := 1 / (x1 - x0)
				x0f := x0 - float64(x0i)
				a0 := 0.5 * s * (1 - x0f) * (1 - x0f)
				x1f := x1 - float64(x1i) + 1
				am := 0.5 * s * x1f * x1f
				row[clampX(x0i)] += d * a0
				if x1i == x0i+2 {
					row[clampX(x0i+1)] += d * (1 - a0 - am)
				} else {
					a1 := s * (1.5 - x0f)
					row[clampX(x0i+1)] += d * (a1 - a0)
					for xi := x0i + 2; xi < x1i-1; xi++ {
						row[clampX(xi)] += d * s
					}
					a2 := a1 + s*float64(x1i-x0i-3)
					row[clampX(x1i-1)] += d * (1 - a2 - am)
				}
				row[clampX(x1i)] += d * am
			}
			x = xNext
		}
	}
	acc := 0.0
	for i := range cov.Pix {
		acc += buf[i]
		cov.Pix[i] = uint8(255.99998 * math.Min(math.Abs(acc), 1))
	}
}

// edgeLine is one flattened, non-horizontal path edge in buffer
// coordinates, stored top to bottom; winding is +1 for a downward edge.
type edgeLine struct {
	x0, y0, x1, y1 float64
	winding        int
}

// flattenPath flattens path (offset by -ox, -oy) into closed polygons'
// edges, subdividing curves as x/image/vector's Rasterizer.QuadTo / CubeTo
// do. Both coverage modes rasterize these edges, so they see the same
// polygon. Every subpath is implicitly closed, as Skia closes fill
// contours.
func flattenPath(path []pathSeg, ox, oy float32) []edgeLine {
	var edges []edgeLine
	var firstX, firstY, penX, penY float32
	open := false
	lineTo := func(x, y float32) {
		if y != penY {
			e := edgeLine{float64(penX), float64(penY), float64(x), float64(y), 1}
			if e.y0 > e.y1 {
				e.x0, e.y0, e.x1, e.y1, e.winding = e.x1, e.y1, e.x0, e.y0, -1
			}
			edges = append(edges, e)
		}
		penX, penY = x, y
	}
	closeContour := func() {
		if open {
			lineTo(firstX, firstY)
		}
		open = false
	}
	for _, seg := range path {
		switch seg.op {
		case pathMoveTo:
			closeContour()
			firstX, firstY = float32(seg.args[0])-ox, float32(seg.args[1])-oy
			penX, penY = firstX, firstY
			open = true
		case pathLineTo:
			lineTo(float32(seg.args[0])-ox, float32(seg.args[1])-oy)
		case pathQuadTo:
			flattenQuad(penX, penY, float32(seg.args[0])-ox, float32(seg.args[1])-oy,
				float32(seg.args[2])-ox, float32(seg.args[3])-oy, lineTo)
		case pathCubicTo:
			flattenCubic(penX, penY, float32(seg.args[0])-ox, float32(seg.args[1])-oy,
				float32(seg.args[2])-ox, float32(seg.args[3])-oy,
				float32(seg.args[4])-ox, float32(seg.args[5])-oy, lineTo)
		case pathClose:
			closeContour()
			penX, penY = firstX, firstY
		}
	}
	closeContour()
	return edges
}

// curveSubdivisions mirrors x/image/vector's subdivision count for a curve
// with squared deviation devsq.
func curveSubdivisions(devsq float32) int {
	if devsq < 0.333 {
		return 1
	}
	const tol = 3
	return 1 + int(math.Sqrt(math.Sqrt(tol*float64(devsq))))
}

func devSquared(ax, ay, bx, by, cx, cy float32) float32 {
	devx := ax - 2*bx + cx
	devy := ay - 2*by + cy
	return devx*devx + devy*devy
}

func lerp32(t, px, py, qx, qy float32) (float32, float32) {
	return px + t*(qx-px), py + t*(qy-py)
}

// flattenQuad mirrors vector.Rasterizer.QuadTo.
func flattenQuad(ax, ay, bx, by, cx, cy float32, lineTo func(x, y float32)) {
	n := curveSubdivisions(devSquared(ax, ay, bx, by, cx, cy))
	t, nInv := float32(0), 1/float32(n)
	for i := 0; i < n-1; i++ {
		t += nInv
		abx, aby := lerp32(t, ax, ay, bx, by)
		bcx, bcy := lerp32(t, bx, by, cx, cy)
		lineTo(lerp32(t, abx, aby, bcx, bcy))
	}
	lineTo(cx, cy)
}

// flattenCubic mirrors vector.Rasterizer.CubeTo.
func flattenCubic(ax, ay, bx, by, cx, cy, dx, dy float32, lineTo func(x, y float32)) {
	devsq := max(devSquared(ax, ay, bx, by, dx, dy), devSquared(ax, ay, cx, cy, dx, dy))
	n := curveSubdivisions(devsq)
	t, nInv := float32(0), 1/float32(n)
	for i := 0; i < n-1; i++ {
		t += nInv
		abx, aby := lerp32(t, ax, ay, bx, by)
		bcx, bcy := lerp32(t, bx, by, cx, cy)
		cdx, cdy := lerp32(t, cx, cy, dx, dy)
		abcx, abcy := lerp32(t, abx, aby, bcx, bcy)
		bcdx, bcdy := lerp32(t, bcx, bcy, cdx, cdy)
		lineTo(lerp32(t, abcx, abcy, bcdx, bcdy))
	}
	lineTo(dx, dy)
}

// scanCentres fills cov (0 or 255) by Skia's non-AA rule under the
// non-zero winding rule (the rule the anti-aliased rasterizer applies).
func scanCentres(cov *image.Alpha, edges []edgeLine) {
	type crossing struct {
		x       float64
		winding int
	}
	bw, bh := cov.Rect.Dx(), cov.Rect.Dy()
	var xs []crossing
	for py := 0; py < bh; py++ {
		yc := float64(py) + 0.5
		xs = xs[:0]
		for _, e := range edges {
			if e.y0 < yc && yc <= e.y1 {
				x := e.x0 + (yc-e.y0)*(e.x1-e.x0)/(e.y1-e.y0)
				xs = append(xs, crossing{x, e.winding})
			}
		}
		if len(xs) < 2 {
			continue
		}
		slices.SortFunc(xs, func(a, b crossing) int { return cmp.Compare(a.x, b.x) })
		row := cov.Pix[py*cov.Stride : py*cov.Stride+bw]
		w := 0
		for i := 0; i < len(xs)-1; i++ {
			w += xs[i].winding
			if w == 0 {
				continue
			}
			l := max(skRoundToInt(xs[i].x), 0)
			r := min(skRoundToInt(xs[i+1].x), bw)
			for px := l; px < r; px++ {
				row[px] = 255
			}
		}
	}
}
