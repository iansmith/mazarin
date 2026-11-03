package textshape

import (
	"image"
	"image/color"
	"math"
	"testing"
)

// Anti-aliasing contract (SkPaint::setAntiAlias / cc::PaintFlags::
// setAntiAlias, SkCanvas::clipPath(path, op, doAntiAlias)):
//   - AA on: an edge pixel is weighted by the area of the pixel the shape
//     covers (analytic coverage).
//   - AA off: Skia's non-AA scan conversion — edges round half up
//     (SkFixedRoundToInt / SkFDot6Round), so a pixel is painted iff its
//     centre c satisfies left < c <= right (and likewise top/bottom).

var aaRed = color.RGBA{R: 255, A: 255}

func alphaAt(dc *DrawContextImpl, x, y int) uint8 { return dc.im.RGBAAt(x, y).A }

// countPartial counts the pixels with 0 < alpha < 255.
func countPartial(dc *DrawContextImpl) int {
	n := 0
	for y := 0; y < dc.height; y++ {
		for x := 0; x < dc.width; x++ {
			if a := alphaAt(dc, x, y); a > 0 && a < 255 {
				n++
			}
		}
	}
	return n
}

// assertRow checks got(x) against want[x] to within tol.
func assertRow(t *testing.T, what string, got func(x int) uint8, want []uint8, tol uint8) {
	t.Helper()
	for x, w := range want {
		if g := got(x); absDiff8(g, w) > tol {
			t.Errorf("%s x=%d: %d, want %d±%d", what, x, g, w, tol)
		}
	}
}

// row0 returns the alpha of row 0 of dc.
func row0(dc *DrawContextImpl) func(x int) uint8 {
	return func(x int) uint8 { return alphaAt(dc, x, 0) }
}

// TestAntiAliasDefaultAndState: AA is on by default (Blink's
// GraphicsContextState), lives in the saved graphics state, and child
// contexts inherit it.
func TestAntiAliasDefaultAndState(t *testing.T) {
	for _, dc := range []*DrawContextImpl{
		NewDrawContext(4, 4, nil),
		NewDrawContextForImage(image.NewRGBA(image.Rect(0, 0, 4, 4)), nil),
	} {
		if !dc.IsAntiAlias() {
			t.Fatal("IsAntiAlias() = false by default; want true (GraphicsContextState)")
		}
	}
	dc := NewDrawContext(4, 4, nil)
	dc.SetAntiAlias(false)
	dc.Push()
	dc.SetAntiAlias(true)
	dc.Pop()
	if dc.IsAntiAlias() {
		t.Fatal("Pop did not restore the anti-alias flag")
	}
	child := dc.NewChildContext(image.NewRGBA(image.Rect(0, 0, 2, 2)))
	if child.(*DrawContextImpl).IsAntiAlias() {
		t.Fatal("NewChildContext did not inherit the anti-alias flag")
	}
}

// TestFillNonAntiAliasSamplesPixelCentres: a non-AA fill paints exactly the
// pixels whose centres the shape contains — not every pixel it touches.
func TestFillNonAntiAliasSamplesPixelCentres(t *testing.T) {
	dc := NewDrawContext(6, 3, nil)
	dc.SetAntiAlias(false)
	dc.SetColor(aaRed)
	// x ∈ [0.6, 2.4): only centre 1.5 is inside.
	dc.DrawRectangle(0.6, 0, 1.8, 3)
	dc.Fill()
	for x, want := range []uint8{0, 255, 0, 0, 0, 0} {
		if got := alphaAt(dc, x, 1); got != want {
			t.Errorf("x=%d: alpha %d, want %d", x, got, want)
		}
	}
	// Edges round half up: x ∈ (3.5, 4.5] holds centre 4.5, not 3.5.
	dc.DrawRectangle(3.5, 0, 1, 3)
	dc.Fill()
	if a3, a4 := alphaAt(dc, 3, 1), alphaAt(dc, 4, 1); a3 != 0 || a4 != 255 {
		t.Errorf("round-half-up span: alpha(3)=%d alpha(4)=%d, want 0, 255", a3, a4)
	}
}

// TestFillAntiAliasFractionalRect: an AA fill weights each edge pixel by its
// covered area.
func TestFillAntiAliasFractionalRect(t *testing.T) {
	dc := NewDrawContext(4, 2, nil)
	dc.SetAntiAlias(true)
	dc.SetColor(aaRed)
	dc.DrawRectangle(0.5, 0, 2, 2) // pixel 0: 50%, 1: 100%, 2: 50%
	dc.Fill()
	assertRow(t, "alpha", row0(dc), []uint8{128, 255, 128, 0}, 1)
}

// TestFillAntiAliasDiagonal: a diagonal edge gets intermediate coverage with
// AA and only 0/255 without; integer-aligned edges are identical either way.
func TestFillAntiAliasDiagonal(t *testing.T) {
	tri := func(aa bool) *DrawContextImpl {
		dc := NewDrawContext(10, 10, nil)
		dc.SetAntiAlias(aa)
		dc.SetColor(aaRed)
		dc.MoveTo(0, 0)
		dc.LineTo(10, 0)
		dc.LineTo(0, 10)
		dc.ClosePath()
		dc.Fill()
		return dc
	}
	aa, hard := tri(true), tri(false)
	if n := countPartial(hard); n != 0 {
		t.Fatalf("non-AA fill has %d partially covered pixels", n)
	}
	if countPartial(aa) == 0 {
		t.Fatal("AA diagonal edge has no partially covered pixels")
	}
	// Pixel (4,4): centre (4.5,4.5) lies on x+y=9 < 10, inside; (5,5) outside.
	if a, b := alphaAt(hard, 4, 4), alphaAt(hard, 5, 5); a != 255 || b != 0 {
		t.Errorf("non-AA diagonal: alpha(4,4)=%d alpha(5,5)=%d, want 255, 0", a, b)
	}
	// Pixel (0,0) is fully inside and (9,9) fully outside in both modes.
	if alphaAt(aa, 0, 0) != 255 || alphaAt(aa, 9, 9) != 0 {
		t.Error("AA interior/exterior pixels are not 255/0")
	}
}

// TestFillAntiAliasEdgeCoverageIsExact: both slanted edges of a trapezoid
// get their exact covered area. In row 67 each edge moves ¼px across one
// pixel, leaving ⅛ of it uncovered: 0.875 coverage on both sides
// (x/image/vector's fixed-point rasterizer gave 0.75 on the right).
func TestFillAntiAliasEdgeCoverageIsExact(t *testing.T) {
	dc := NewDrawContext(103, 70, nil)
	dc.SetColor(aaRed)
	dc.MoveTo(1, 68)
	dc.LineTo(17+2.0/3, 1+1.0/3)
	dc.LineTo(84+1.0/3, 1+1.0/3)
	dc.LineTo(101, 68)
	dc.ClosePath()
	dc.Fill()
	for _, x := range []int{1, 100} {
		if a := alphaAt(dc, x, 67); absDiff8(a, 223) > 1 {
			t.Errorf("edge pixel (%d,67): alpha %d, want 223±1 (7/8 coverage)", x, a)
		}
	}
}

// TestStrokeAntiAlias: strokes honour the flag.
func TestStrokeAntiAlias(t *testing.T) {
	dc := NewDrawContext(20, 20, nil)
	dc.SetAntiAlias(true)
	dc.SetColor(aaRed)
	dc.SetLineWidth(2)
	dc.MoveTo(2, 3)
	dc.LineTo(18, 15)
	dc.Stroke()
	if countPartial(dc) == 0 {
		t.Fatal("AA stroke of a diagonal line has no partially covered pixels")
	}
}

// TestFillRectangleAntiAlias: FillRectangle with fractional device edges is
// weighted by coverage when AA is on; without AA its edges round half up.
func TestFillRectangleAntiAlias(t *testing.T) {
	fill := func(aa bool) *DrawContextImpl {
		dc := NewDrawContext(4, 1, nil)
		dc.SetAntiAlias(aa)
		dc.SetColor(aaRed)
		dc.FillRectangle(0.5, 0, 2, 1)
		return dc
	}
	assertRow(t, "AA alpha", row0(fill(true)), []uint8{128, 255, 128, 0}, 1)
	assertRow(t, "non-AA alpha", row0(fill(false)), []uint8{0, 255, 255, 0}, 0)
}

// TestClipPathAntiAlias: an AA clip weights painted pixels by clip coverage;
// a non-AA clip is centre-sampled.
func TestClipPathAntiAlias(t *testing.T) {
	clipFill := func(aa bool) *DrawContextImpl {
		dc := NewDrawContext(6, 1, nil)
		dc.SetColor(aaRed)
		dc.DrawRectangle(0.5, 0, 2, 1)
		dc.ClipPath(ClipOpIntersect, aa)
		dc.FillRectangle(0, 0, 6, 1)
		return dc
	}
	assertRow(t, "AA clip alpha", row0(clipFill(true)), []uint8{128, 255, 128, 0}, 1)
	// Non-AA rect clip rounds its edges (SkRect::round): [0.5, 2.5) → [1, 3).
	assertRow(t, "non-AA clip alpha", row0(clipFill(false)), []uint8{0, 255, 255, 0}, 0)
}

// TestClipPathAntiAliasCircle: an AA circle clip produces partial coverage on
// its rim and composes multiplicatively with a second AA clip.
func TestClipPathAntiAliasCircle(t *testing.T) {
	dc := NewDrawContext(20, 20, nil)
	dc.SetColor(aaRed)
	dc.DrawCircle(10, 10, 7.3)
	dc.ClipPath(ClipOpIntersect, true)
	dc.DrawRectangle(0, 0, 10.5, 20)
	dc.ClipPath(ClipOpIntersect, true)
	dc.FillRectangle(0, 0, 20, 20)
	if countPartial(dc) == 0 {
		t.Fatal("AA circle clip produced no partial coverage")
	}
	// Column 10 is half-covered by the rect clip; its interior pixels are 50%.
	if a := alphaAt(dc, 10, 10); absDiff8(a, 128) > 1 {
		t.Errorf("rect∩circle at (10,10): alpha %d, want 128±1", a)
	}
	if a := alphaAt(dc, 11, 10); a != 0 {
		t.Errorf("(11,10) outside the rect clip has alpha %d", a)
	}
}

// TestClipPathDifference: SkClipOp::kDifference removes the path from the
// clip; with AA the removed edge is partial.
func TestClipPathDifference(t *testing.T) {
	dc := NewDrawContext(6, 1, nil)
	dc.SetColor(aaRed)
	dc.DrawRectangle(1.5, 0, 2, 1) // removes 50% of px1, all of px2, 50% of px3
	dc.ClipPath(ClipOpDifference, true)
	dc.FillRectangle(0, 0, 6, 1)
	assertRow(t, "AA difference alpha", row0(dc), []uint8{255, 128, 0, 128, 255, 255}, 1)
	dc2 := NewDrawContext(6, 1, nil)
	dc2.SetColor(aaRed)
	dc2.DrawRectangle(1, 0, 2, 1)
	dc2.ClipPath(ClipOpDifference, false)
	dc2.FillRectangle(0, 0, 6, 1)
	assertRow(t, "non-AA difference alpha", row0(dc2), []uint8{255, 0, 0, 255, 255, 255}, 0)
}

// TestClipLegacyIsNonAntiAliasIntersect: Clip() is ClipPath(Intersect, false).
func TestClipLegacyIsNonAntiAliasIntersect(t *testing.T) {
	a := NewDrawContext(20, 20, nil)
	b := NewDrawContext(20, 20, nil)
	for _, dc := range []*DrawContextImpl{a, b} {
		dc.SetColor(aaRed)
		dc.DrawCircle(10, 10, 6.4)
	}
	a.Clip()
	b.ClipPath(ClipOpIntersect, false)
	for _, dc := range []*DrawContextImpl{a, b} {
		dc.FillRectangle(0, 0, 20, 20)
	}
	for y := 0; y < 20; y++ {
		for x := 0; x < 20; x++ {
			if alphaAt(a, x, y) != alphaAt(b, x, y) {
				t.Fatalf("(%d,%d): Clip()=%d ClipPath(Intersect,false)=%d", x, y, alphaAt(a, x, y), alphaAt(b, x, y))
			}
		}
	}
}

// TestDrawImageAntiAliasRotated: an opaque image drawn under a rotation gets
// coverage-weighted edges with AA and centre-sampled (0/255) edges without.
func TestDrawImageAntiAliasRotated(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 10, 10))
	for i := range src.Pix {
		src.Pix[i] = 255
	}
	draw := func(aa bool) *DrawContextImpl {
		dc := NewDrawContext(30, 30, nil)
		dc.SetAntiAlias(aa)
		dc.RotateAbout(math.Pi/6, 15, 15)
		dc.DrawImageAnchored(src, 15, 15, 0.5, 0.5)
		return dc
	}
	aa, hard := draw(true), draw(false)
	if n := countPartial(hard); n != 0 {
		t.Fatalf("non-AA rotated image has %d partially covered pixels", n)
	}
	if countPartial(aa) == 0 {
		t.Fatal("AA rotated image has no partially covered edge pixels")
	}
	if alphaAt(aa, 15, 15) != 255 {
		t.Errorf("AA rotated image interior alpha %d, want 255", alphaAt(aa, 15, 15))
	}
}

// TestClipCoverageAt: clipCoverageAt (which fill, image and glyph
// compositing multiply by) is 255 without a clip and the AA clip's edge
// coverage with one.
func TestClipCoverageAt(t *testing.T) {
	dc := NewDrawContext(4, 1, nil)
	if c := dc.clipCoverageAt(1, 0); c != 255 {
		t.Fatalf("no clip: coverage %d, want 255", c)
	}
	dc.DrawRectangle(0.5, 0, 2, 1)
	dc.ClipPath(ClipOpIntersect, true)
	assertRow(t, "clip coverage", func(x int) uint8 { return dc.clipCoverageAt(x, 0) }, []uint8{128, 255, 128, 0}, 1)
}
