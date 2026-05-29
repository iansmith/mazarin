package textshape

import (
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

// TestProjectiveNearPlaneDrop checks the near-plane safety: a path with a
// vertex on/behind the camera (W ≤ 0) is flagged and dropped at fill, leaving
// the canvas untouched rather than rasterizing mirrored/exploded geometry.
func TestProjectiveNearPlaneDrop(t *testing.T) {
	dc := NewDrawContext(100, 100, nil)
	dc.SetColor(color.RGBA{R: 255, A: 255})
	// Homography with W = 1 - 0.02x → W ≤ 0 for x ≥ 50.
	dc.MultiplyProjectiveMatrix(1, 0, 0, 0, 1, 0, -0.02, 0, 1)
	dc.MoveTo(10, 10)
	dc.LineTo(90, 10) // x=90 → W = 1-1.8 < 0: behind camera
	dc.LineTo(90, 90)
	dc.LineTo(10, 90)
	dc.ClosePath()
	if !dc.projBehindCamera {
		t.Fatal("expected projBehindCamera set for a path crossing W≤0")
	}
	dc.Fill()
	// Canvas must be untouched (fully transparent) — primitive dropped.
	if painted := countNonTransparent(dc); painted != 0 {
		t.Errorf("expected 0 painted pixels (primitive dropped), got %d", painted)
	}
	// And the flag resets after Fill→ClearPath.
	if dc.projBehindCamera {
		t.Error("projBehindCamera not reset by ClearPath")
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
