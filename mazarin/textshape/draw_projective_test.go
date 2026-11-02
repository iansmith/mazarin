package textshape

import (
	"image"
	"image/color"
	"math"
	"testing"
)

func approxEq(a, b float64) bool { return math.Abs(a-b) <= 1e-6 }

// TestMul3AffineTo3 checks the 3×3 helpers: affineTo3 round-trips an affine
// Matrix, and mul3 implements standard row-major composition (identity is a
// unit, and a known product is correct).
func TestMul3AffineTo3(t *testing.T) {
	m := Matrix{XX: 2, YX: 0.5, XY: -1, YY: 3, X0: 10, Y0: 20}
	h := affineTo3(m)
	// affineTo3 maps (x,y)→(XX*x+XY*y+X0, YX*x+YY*y+Y0); homogeneous row [0 0 1].
	want := [9]float64{2, -1, 10, 0.5, 3, 20, 0, 0, 1}
	if h != want {
		t.Fatalf("affineTo3 = %v, want %v", h, want)
	}
	id := [9]float64{1, 0, 0, 0, 1, 0, 0, 0, 1}
	if got := mul3(h, id); got != h {
		t.Errorf("mul3(h, I) = %v, want %v", got, h)
	}
	if got := mul3(id, h); got != h {
		t.Errorf("mul3(I, h) = %v, want %v", got, h)
	}
	// Standard product check: mul3(A,B)[0][0] = A row0 · B col0.
	a := [9]float64{1, 2, 3, 4, 5, 6, 7, 8, 9}
	b := [9]float64{9, 8, 7, 6, 5, 4, 3, 2, 1}
	got := mul3(a, b)
	if !approxEq(got[0], 1*9+2*6+3*3) || !approxEq(got[8], 7*7+8*4+9*1) {
		t.Errorf("mul3 standard product wrong: got %v", got)
	}
}

// TestProjectiveTransformPointDivide verifies that MultiplyProjectiveMatrix
// installs a homography and that transformPoint applies the perspective
// divide. Uses a homography with a non-trivial bottom row.
func TestProjectiveTransformPointDivide(t *testing.T) {
	dc := NewDrawContext(100, 100, nil)
	// screen_x = (x)/(0.001x+1), screen_y = (y)/(0.001x+1)
	dc.MultiplyProjectiveMatrix(1, 0, 0, 0, 1, 0, 0.001, 0, 1)
	for _, p := range [][2]float64{{0, 0}, {100, 0}, {50, 80}} {
		w := 0.001*p[0] + 1
		wantX, wantY := p[0]/w, p[1]/w
		gotX, gotY := dc.transformPoint(p[0], p[1])
		if !approxEq(gotX, wantX) || !approxEq(gotY, wantY) {
			t.Errorf("transformPoint(%v) = (%v,%v), want (%v,%v)", p, gotX, gotY, wantX, wantY)
		}
	}
}

// TestProjectiveMatchesAffine is the key invariant: a projective multiply with
// an affine homography (bottom row [0 0 1]) must transform points identically
// to the affine MultiplyMatrix — so the projective path is a true
// generalization, not a behavior change for existing (non-perspective) callers.
func TestProjectiveMatchesAffine(t *testing.T) {
	// An affine transform expressed both ways.
	xx, yx, xy, yy, x0, y0 := 1.5, 0.3, -0.7, 2.0, 12.0, -5.0
	affine := NewDrawContext(100, 100, nil)
	affine.MultiplyMatrix(xx, yx, xy, yy, x0, y0)
	proj := NewDrawContext(100, 100, nil)
	// affineTo3 of Matrix{xx,yx,xy,yy,x0,y0} in row-major: [xx,xy,x0, yx,yy,y0, 0,0,1].
	proj.MultiplyProjectiveMatrix(xx, xy, x0, yx, yy, y0, 0, 0, 1)
	for _, p := range [][2]float64{{0, 0}, {37, 11}, {100, 100}, {-4, 60}} {
		ax, ay := affine.transformPoint(p[0], p[1])
		px, py := proj.transformPoint(p[0], p[1])
		if !approxEq(ax, px) || !approxEq(ay, py) {
			t.Errorf("affine vs projective differ at %v: affine=(%v,%v) proj=(%v,%v)", p, ax, ay, px, py)
		}
	}
}

// TestProjectiveSaveRestore confirms the projective state is part of the
// graphics-state snapshot: Save/Restore must push and pop both proj and
// hasProj.
func TestProjectiveSaveRestore(t *testing.T) {
	dc := NewDrawContext(100, 100, nil)
	dc.Push()
	dc.MultiplyProjectiveMatrix(1, 0, 0, 0, 1, 0, 0.002, 0, 1)
	if !dc.gs.hasProj {
		t.Fatal("expected hasProj after MultiplyProjectiveMatrix")
	}
	dc.Pop()
	if dc.gs.hasProj {
		t.Error("Restore did not pop projective state")
	}
	// After restore, transform is identity-affine again.
	if gx, gy := dc.transformPoint(40, 60); !approxEq(gx, 40) || !approxEq(gy, 60) {
		t.Errorf("post-Restore transformPoint = (%v,%v), want (40,60)", gx, gy)
	}
}

// TestProjectiveTranslateMaintained checks that affine ops issued while in
// projective mode compose into the homography (so the bracketing
// Translate(o)…Translate(-o) used by callers behaves correctly).
func TestProjectiveTranslateMaintained(t *testing.T) {
	dc := NewDrawContext(100, 100, nil)
	dc.MultiplyProjectiveMatrix(2, 0, 0, 0, 2, 0, 0, 0, 1) // scale 2× (affine homography)
	dc.Translate(10, 5)                                    // applied to local point first
	// Effective map: scale2 ∘ translate(10,5): (x,y) → (2(x+10), 2(y+5)).
	gx, gy := dc.transformPoint(3, 4)
	if !approxEq(gx, 2*(3+10)) || !approxEq(gy, 2*(4+5)) {
		t.Errorf("projective Translate compose = (%v,%v), want (26,18)", gx, gy)
	}
}

// nearPlaneSquareHomography has W = 1 - 0.02x, so W ≤ 0 for x ≥ 50: the
// local square [10,90]² straddles the camera near-plane. Its W > 0 part
// x ∈ [10,50) projects to X = x/W, Y = y/W.
var nearPlaneSquareHomography = [9]float64{1, 0, 0, 0, 1, 0, -0.02, 0, 1}

// nearPlaneSquareInside reports whether device pixel (px,py)'s centre maps
// (inverse of nearPlaneSquareHomography: x = X/(1+0.02X), y = Y(1-0.02x))
// to a W > 0 point inside the local square [10,90]².
func nearPlaneSquareInside(px, py int) bool {
	X, Y := float64(px)+0.5, float64(py)+0.5
	x := X / (1 + 0.02*X)
	w := 1 - 0.02*x
	y := Y * w
	return w > 0 && x >= 10 && x <= 90 && y >= 10 && y <= 90
}

// checkNearPlaneSquare asserts the painted pixels are exactly the W > 0
// part of the square, skipping a 1px band around its edges (hard-edged
// rasterizer coverage at the boundary is not what is under test).
func checkNearPlaneSquare(t *testing.T, dc *DrawContextImpl) {
	t.Helper()
	painted := 0
	for py := 0; py < 100; py++ {
		for px := 0; px < 100; px++ {
			in := nearPlaneSquareInside(px, py)
			edge := false
			for _, d := range [][2]int{{-1, 0}, {1, 0}, {0, -1}, {0, 1}} {
				if nearPlaneSquareInside(px+d[0], py+d[1]) != in {
					edge = true
				}
			}
			if edge {
				continue
			}
			a := dc.im.RGBAAt(px, py).A
			if in && a == 0 {
				t.Fatalf("pixel (%d,%d) is in the W>0 part of the square but not painted", px, py)
			}
			if !in && a != 0 {
				t.Fatalf("pixel (%d,%d) is outside the W>0 part of the square but painted", px, py)
			}
			if in {
				painted++
			}
		}
	}
	if painted < 1000 {
		t.Fatalf("only %d interior pixels painted; homography misconfigured", painted)
	}
}

// TestProjectiveNearPlaneClipFill: a straight-edged path with vertices on or
// behind the camera near-plane (W ≤ ε) is clipped against the near-plane in
// homogeneous space before the perspective divide, so its W > 0 part is
// filled — mirroring Skia's SkPathPriv::PerspectiveClip — rather than the
// whole primitive being dropped or mirrored geometry rasterized.
func TestProjectiveNearPlaneClipFill(t *testing.T) {
	h := nearPlaneSquareHomography
	dc := NewDrawContext(100, 100, nil)
	dc.SetColor(color.RGBA{R: 255, A: 255})
	dc.MultiplyProjectiveMatrix(h[0], h[1], h[2], h[3], h[4], h[5], h[6], h[7], h[8])
	dc.MoveTo(10, 10)
	dc.LineTo(90, 10) // x=90 → W = 1-1.8 < 0: behind camera
	dc.LineTo(90, 90)
	dc.LineTo(10, 90)
	dc.ClosePath()
	if !dc.projBehindCamera {
		t.Fatal("expected projBehindCamera set for a path crossing W≤0")
	}
	dc.FillPreserve()
	checkNearPlaneSquare(t, dc)
	// FillPreserve keeps the caller's (unclipped) path and flag.
	if len(dc.path) != 5 || !dc.projBehindCamera {
		t.Errorf("FillPreserve altered the path: %d segs, projBehindCamera=%v", len(dc.path), dc.projBehindCamera)
	}
	dc.Fill()
	if dc.projBehindCamera {
		t.Error("projBehindCamera not reset by ClearPath")
	}
}

// TestProjectiveNearPlaneClipClip: Clip applies the same near-plane clip, so
// a clip path straddling the camera clips to its W > 0 part (here: the
// background-clip border box of css-transforms/perspective-split-by-zero-w).
func TestProjectiveNearPlaneClipClip(t *testing.T) {
	h := nearPlaneSquareHomography
	dc := NewDrawContext(100, 100, nil)
	dc.MultiplyProjectiveMatrix(h[0], h[1], h[2], h[3], h[4], h[5], h[6], h[7], h[8])
	dc.DrawRectangle(10, 10, 80, 80)
	dc.Clip()
	// An opaque image covering local [0,100)², drawn through the clip.
	im := image.NewRGBA(image.Rect(0, 0, 100, 100))
	for i := range im.Pix {
		im.Pix[i] = 255
	}
	dc.DrawImage(im, 0, 0)
	checkNearPlaneSquare(t, dc)
}

// TestProjectiveNearPlaneCurveDrop: a path with curve segments that crosses
// the near-plane is still dropped whole (curves are not near-plane-clipped).
func TestProjectiveNearPlaneCurveDrop(t *testing.T) {
	h := nearPlaneSquareHomography
	dc := NewDrawContext(100, 100, nil)
	dc.SetColor(color.RGBA{R: 255, A: 255})
	dc.MultiplyProjectiveMatrix(h[0], h[1], h[2], h[3], h[4], h[5], h[6], h[7], h[8])
	dc.MoveTo(10, 10)
	dc.CubicTo(40, 0, 70, 0, 90, 10)
	dc.LineTo(90, 90)
	dc.LineTo(10, 90)
	dc.ClosePath()
	dc.Fill()
	if painted := countNonTransparent(dc); painted != 0 {
		t.Errorf("expected 0 painted pixels (curved primitive dropped), got %d", painted)
	}
}

func countNonTransparent(dc *DrawContextImpl) int {
	n := 0
	b := dc.im.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if _, _, _, a := dc.im.At(x, y).RGBA(); a != 0 {
				n++
			}
		}
	}
	return n
}

// gradientImage returns a w×h opaque image whose R and G channels encode the
// pixel's x and y (R = k*x, G = k*y), so a bilinear sample at a continuous
// source position is analytically predictable away from the edges.
func gradientImage(w, h int, k uint8) *image.RGBA {
	im := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			im.SetRGBA(x, y, color.RGBA{R: uint8(x) * k, G: uint8(y) * k, B: 40, A: 255})
		}
	}
	return im
}

// invertHomographyPoint solves h·(x,y,1) ∝ (X,Y,1) for the local (x,y) by
// Cramer's rule — an independent oracle for the draw path's inverse mapping.
func invertHomographyPoint(h [9]float64, X, Y float64) (float64, float64) {
	a, b, c := h[0]-X*h[6], h[1]-X*h[7], X*h[8]-h[2]
	d, e, f := h[3]-Y*h[6], h[4]-Y*h[7], Y*h[8]-h[5]
	det := a*e - b*d
	return (c*e - b*f) / det, (a*f - c*d) / det
}

// TestProjectiveDrawImageMatchesAffine: an image drawn under a projective
// transform whose homography is purely affine must match the affine
// (xdraw.BiLinear) path within 1 LSB per channel, including the anchor
// offset and Over compositing onto a non-empty destination.
func TestProjectiveDrawImageMatchesAffine(t *testing.T) {
	im := gradientImage(20, 16, 12)
	// A translucent (premultiplied) row exercises the Over blend.
	for x := 0; x < 20; x++ {
		im.SetRGBA(x, 5, color.RGBA{R: 60, G: 10, B: 90, A: 128})
	}
	xx, yx, xy, yy, x0, y0 := 1.5, 0.3, -0.4, 1.25, 20.25, 10.7
	affine := NewDrawContext(80, 80, nil)
	proj := NewDrawContext(80, 80, nil)
	for _, dc := range []*DrawContextImpl{affine, proj} {
		dc.SetColor(color.RGBA{R: 30, G: 200, B: 30, A: 255})
		dc.Clear()
	}
	affine.MultiplyMatrix(xx, yx, xy, yy, x0, y0)
	proj.MultiplyProjectiveMatrix(xx, xy, x0, yx, yy, y0, 0, 0, 1)
	affine.DrawImageAnchored(im, 5, 7, 0.5, 0.5)
	proj.DrawImageAnchored(im, 5, 7, 0.5, 0.5)

	diffs := 0
	for y := 0; y < 80; y++ {
		for x := 0; x < 80; x++ {
			a, p := affine.im.RGBAAt(x, y), proj.im.RGBAAt(x, y)
			if absDiff8(a.R, p.R) > 1 || absDiff8(a.G, p.G) > 1 || absDiff8(a.B, p.B) > 1 || absDiff8(a.A, p.A) > 1 {
				if diffs < 5 {
					t.Errorf("pixel (%d,%d): affine=%v projective=%v", x, y, a, p)
				}
				diffs++
			}
		}
	}
	if diffs > 0 {
		t.Errorf("%d pixels differ by more than 1 LSB", diffs)
	}
}

// TestProjectiveDrawImagePerspective: under a real homography (non-trivial
// bottom row) each device pixel samples the source at the analytically
// inverse-mapped point, and pixels outside the projected quad are untouched.
func TestProjectiveDrawImagePerspective(t *testing.T) {
	const k = 6
	im := gradientImage(40, 40, k)
	h := [9]float64{1, 0.1, 10, 0.05, 1, 10, 0.004, 0.002, 1}
	dc := NewDrawContext(100, 100, nil)
	dc.MultiplyProjectiveMatrix(h[0], h[1], h[2], h[3], h[4], h[5], h[6], h[7], h[8])
	dc.DrawImage(im, 0, 0)

	checked := 0
	for py := 0; py < 100; py += 3 {
		for px := 0; px < 100; px += 3 {
			sx, sy := invertHomographyPoint(h, float64(px)+0.5, float64(py)+0.5)
			got := dc.im.RGBAAt(px, py)
			switch {
			case sx >= 2 && sx <= 38 && sy >= 2 && sy <= 38:
				// Bilinear interpolation of a linear ramp is exact: the
				// sample at continuous source position s is k*(s-0.5).
				wantR, wantG := k*(sx-0.5), k*(sy-0.5)
				if got.A != 255 || math.Abs(float64(got.R)-wantR) > 1.5 || math.Abs(float64(got.G)-wantG) > 1.5 {
					t.Fatalf("pixel (%d,%d) ← source (%.2f,%.2f): got %v, want R≈%.1f G≈%.1f", px, py, sx, sy, got, wantR, wantG)
				}
				checked++
			case sx < -1 || sx > 41 || sy < -1 || sy > 41:
				if got.A != 0 {
					t.Fatalf("pixel (%d,%d) maps outside the image (%.2f,%.2f) but was painted %v", px, py, sx, sy, got)
				}
			}
		}
	}
	if checked < 50 {
		t.Fatalf("only %d interior pixels checked; homography misconfigured", checked)
	}
}

// TestProjectiveDrawImageBehindCamera: device pixels whose inverse-mapped
// source point lies on or behind the camera near-plane (W ≤ ε) are not
// painted — a W<0 point projects mirrored, and drawing it would show
// geometry that is behind the viewer. The W>0 part of the same image is
// still drawn (css-transforms/perspective-split-by-zero-w), and an image
// entirely behind the camera paints nothing.
func TestProjectiveDrawImageBehindCamera(t *testing.T) {
	im := gradientImage(100, 40, 2)
	// X = (150-2x)/W, Y = y/W with W = 1-0.02x: W ≤ 0 for x ≥ 50.
	h := [9]float64{-2, 0, 150, 0, 1, 0, -0.02, 0, 1}
	dc := NewDrawContext(200, 100, nil)
	dc.MultiplyProjectiveMatrix(h[0], h[1], h[2], h[3], h[4], h[5], h[6], h[7], h[8])
	// Anchor vertically centred: local y ∈ [-20, 20).
	dc.DrawImageAnchored(im, 0, 0, 0, 0.5)

	visible, mirrored := 0, 0
	for py := 0; py < 100; py++ {
		for px := 0; px < 200; px++ {
			sx, sy := invertHomographyPoint(h, float64(px)+0.5, float64(py)+0.5)
			if sx < 1 || sx > 99 || sy < -19 || sy > 19 {
				continue
			}
			w := h[6]*sx + h[7]*sy + h[8]
			a := dc.im.RGBAAt(px, py).A
			if w > 0.05 {
				visible++
				if a != 255 {
					t.Fatalf("pixel (%d,%d) is in front of the camera (W=%.3f) but not painted", px, py, w)
				}
			} else if w < -0.05 {
				mirrored++
				if a != 0 {
					t.Fatalf("pixel (%d,%d) maps behind the camera (W=%.3f) but was painted", px, py, w)
				}
			}
		}
	}
	if visible == 0 || mirrored == 0 {
		t.Fatalf("homography misconfigured: visible=%d mirrored=%d", visible, mirrored)
	}

	// Entirely behind the camera (W = -1 everywhere): the mirrored image
	// would land on-canvas at (10..50, 10..50) but must not be drawn.
	all := NewDrawContext(100, 100, nil)
	all.MultiplyProjectiveMatrix(1, 0, 0, 0, 1, 0, 0, 0, -1)
	all.DrawImage(gradientImage(40, 40, 2), -50, -50)
	if n := countNonTransparent(all); n != 0 {
		t.Errorf("image wholly behind the camera painted %d pixels, want 0", n)
	}
}

func absDiff8(a, b uint8) uint8 {
	if a > b {
		return a - b
	}
	return b - a
}
