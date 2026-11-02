package textshape

import (
	"cmp"
	"image"
	"math"
	"slices"

	"golang.org/x/image/vector"
)

// Edge coverage for path fills and clips.
//
// Anti-aliased coverage is the exact area of each pixel the path covers
// (x/image/vector's accumulation rasterizer) — the analogue of Skia's
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
// w×h coverage buffer: analytic area coverage (x/image/vector's
// accumulation rasterizer, fed the same flattened edges) when antiAlias,
// else Skia's non-AA centre sampling (scanCentres).
func edgeCoverage(edges []edgeLine, w, h int, antiAlias bool) *image.Alpha {
	cov := image.NewAlpha(image.Rect(0, 0, max(w, 0), max(h, 0)))
	if w <= 0 || h <= 0 {
		return cov
	}
	if !antiAlias {
		scanCentres(cov, edges)
		return cov
	}
	r := vector.NewRasterizer(w, h)
	for _, e := range edges {
		// The accumulation rasterizer sums each segment's signed area
		// independently, so edges need not be chained into contours.
		if e.winding > 0 {
			r.MoveTo(float32(e.x0), float32(e.y0))
			r.LineTo(float32(e.x1), float32(e.y1))
		} else {
			r.MoveTo(float32(e.x1), float32(e.y1))
			r.LineTo(float32(e.x0), float32(e.y0))
		}
	}
	r.Draw(cov, cov.Bounds(), image.Opaque, image.Point{})
	return cov
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
