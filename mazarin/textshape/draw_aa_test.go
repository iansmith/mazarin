package textshape

import (
	"image"
	"image/color"
	"testing"
)

// Anti-aliased fill and clip coverage (MAZ-218). Skia — and therefore
// Blink's GraphicsContext, whose paint flags default to anti-aliased —
// rasterizes a path with fractional edge coverage and composites the
// source over the destination scaled by that coverage. Clips with
// fractional coverage combine multiplicatively.

// halfCoveredTriangle adds the right triangle (0,0)-(10,0)-(0,10) to dc's
// path. Its hypotenuse x+y=10 passes through the centre of pixel (4,5),
// splitting it in half.
func halfCoveredTriangle(dc *DrawContextImpl, dx, dy float64) {
	dc.MoveTo(dx, dy)
	dc.LineTo(dx+10, dy)
	dc.LineTo(dx, dy+10)
	dc.ClosePath()
}

func alphaAt(im image.Image, x, y int) uint8 {
	return im.(*image.RGBA).RGBAAt(x, y).A
}

func wantNear(t *testing.T, what string, got, want, tol int) {
	t.Helper()
	if got < want-tol || got > want+tol {
		t.Errorf("%s = %d, want %d±%d", what, got, want, tol)
	}
}

func TestFillAntialiasedEdgeCoverage(t *testing.T) {
	dc := NewDrawContext(20, 20, nil)
	dc.SetColor(color.White)
	halfCoveredTriangle(dc, 0, 0)
	dc.Fill()
	wantNear(t, "alpha at half-covered edge pixel (4,5)", int(alphaAt(dc.Image(), 4, 5)), 128, 8)
	if a := alphaAt(dc.Image(), 1, 1); a != 255 {
		t.Errorf("interior pixel alpha = %d, want 255", a)
	}
	if a := alphaAt(dc.Image(), 8, 8); a != 0 {
		t.Errorf("exterior pixel alpha = %d, want 0", a)
	}
}

func TestFillCoverageBlendsOver(t *testing.T) {
	dc := NewDrawContext(20, 20, nil)
	dc.SetColor(color.RGBA{0, 0, 255, 255})
	dc.Clear()
	dc.SetColor(color.RGBA{255, 0, 0, 255})
	halfCoveredTriangle(dc, 0, 0)
	dc.Fill()
	c := dc.Image().(*image.RGBA).RGBAAt(4, 5)
	wantNear(t, "R", int(c.R), 128, 8)
	wantNear(t, "B", int(c.B), 127, 8)
	if c.A != 255 {
		t.Errorf("A = %d, want 255", c.A)
	}
}

func TestSetShouldAntialiasFalseIsHardEdged(t *testing.T) {
	dc := NewDrawContext(20, 20, nil)
	dc.SetShouldAntialias(false)
	if dc.ShouldAntialias() {
		t.Fatal("ShouldAntialias() = true after SetShouldAntialias(false)")
	}
	dc.SetColor(color.White)
	halfCoveredTriangle(dc, 0, 0)
	dc.Fill()
	im := dc.Image().(*image.RGBA)
	for y := 0; y < 20; y++ {
		for x := 0; x < 20; x++ {
			if a := im.RGBAAt(x, y).A; a != 0 && a != 255 {
				t.Fatalf("non-AA fill produced fractional alpha %d at (%d,%d)", a, x, y)
			}
		}
	}
}

func TestShouldAntialiasIsSavedState(t *testing.T) {
	dc := NewDrawContext(4, 4, nil)
	if !dc.ShouldAntialias() {
		t.Fatal("anti-aliasing must default to on")
	}
	dc.Push()
	dc.SetShouldAntialias(false)
	dc.Pop()
	if !dc.ShouldAntialias() {
		t.Error("Pop did not restore ShouldAntialias")
	}
}

func TestClipMaskFractionalCoverage(t *testing.T) {
	dc := NewDrawContext(20, 20, nil)
	halfCoveredTriangle(dc, 0, 0)
	dc.Clip()
	dc.SetColor(color.White)
	dc.DrawRectangle(0, 0, 20, 20)
	dc.Fill()
	wantNear(t, "fill alpha through half-covered clip pixel", int(alphaAt(dc.Image(), 4, 5)), 128, 8)
}

func TestFillRectangleThroughFractionalClip(t *testing.T) {
	dc := NewDrawContext(20, 20, nil)
	halfCoveredTriangle(dc, 0, 0)
	dc.Clip()
	dc.SetFillStyle(NewSolidPattern(color.White))
	dc.FillRectangle(0, 0, 20, 20)
	wantNear(t, "FillRectangle alpha through half-covered clip pixel", int(alphaAt(dc.Image(), 4, 5)), 128, 8)
}

func TestDrawImageThroughFractionalClip(t *testing.T) {
	dc := NewDrawContext(20, 20, nil)
	src := image.NewRGBA(image.Rect(0, 0, 20, 20))
	for i := range src.Pix {
		src.Pix[i] = 255
	}
	halfCoveredTriangle(dc, 0, 0)
	dc.Clip()
	dc.DrawImage(src, 0, 0)
	wantNear(t, "DrawImage alpha through half-covered clip pixel", int(alphaAt(dc.Image(), 4, 5)), 128, 8)
}

func TestClipIntersectionIsMultiplicative(t *testing.T) {
	dc := NewDrawContext(20, 20, nil)
	halfCoveredTriangle(dc, 0, 0)
	dc.Clip()
	halfCoveredTriangle(dc, 0, 0)
	dc.Clip()
	dc.SetColor(color.White)
	dc.DrawRectangle(0, 0, 20, 20)
	dc.Fill()
	// 0.5 × 0.5 coverage.
	wantNear(t, "alpha through two half-covered clips", int(alphaAt(dc.Image(), 4, 5)), 64, 8)
}

func TestClipOutIsComplementOfClip(t *testing.T) {
	in := NewDrawContext(20, 20, nil)
	halfCoveredTriangle(in, 0, 0)
	in.Clip()
	in.SetColor(color.White)
	in.DrawRectangle(0, 0, 20, 20)
	in.Fill()

	out := NewDrawContext(20, 20, nil)
	halfCoveredTriangle(out, 0, 0)
	out.ClipOut()
	out.SetColor(color.White)
	out.DrawRectangle(0, 0, 20, 20)
	out.Fill()

	if a := alphaAt(out.Image(), 1, 1); a != 0 {
		t.Errorf("ClipOut: inside-path alpha = %d, want 0", a)
	}
	if a := alphaAt(out.Image(), 15, 15); a != 255 {
		t.Errorf("ClipOut: outside-path alpha = %d, want 255", a)
	}
	for y := 0; y < 20; y++ {
		for x := 0; x < 20; x++ {
			sum := int(alphaAt(in.Image(), x, y)) + int(alphaAt(out.Image(), x, y))
			if sum < 253 || sum > 257 {
				t.Fatalf("clip + clip-out coverage at (%d,%d) = %d, want 255", x, y, sum)
			}
		}
	}
}

// offsetCanvas returns a context drawing onto a 100×100 canvas whose
// bounds start at (100,100), like louis14's paintLayerIsolated buffers.
func offsetCanvas() *DrawContextImpl {
	return NewDrawContextForImage(image.NewRGBA(image.Rect(100, 100, 200, 200)), nil)
}

func TestClipMaskOnOffsetCanvas(t *testing.T) {
	dc := offsetCanvas()
	dc.DrawCircle(150, 150, 20)
	dc.Clip()
	dc.SetColor(color.White)
	dc.DrawRectangle(100, 100, 100, 100)
	dc.Fill()
	if a := alphaAt(dc.Image(), 150, 150); a != 255 {
		t.Errorf("circle centre alpha = %d, want 255", a)
	}
	for _, p := range []image.Point{{105, 105}, {150, 125}, {150, 175}, {125, 150}, {175, 150}, {195, 195}} {
		if a := alphaAt(dc.Image(), p.X, p.Y); a != 0 {
			t.Errorf("alpha outside circle at %v = %d, want 0", p, a)
		}
	}
	if a := alphaAt(dc.Image(), 150, 131); a != 255 {
		t.Errorf("alpha just inside circle top = %d, want 255", a)
	}
}

func TestClipMaskThenRectOnOffsetCanvas(t *testing.T) {
	dc := offsetCanvas()
	dc.DrawCircle(150, 150, 40)
	dc.Clip()
	dc.DrawRectangle(150, 100, 50, 100)
	dc.Clip()
	dc.SetColor(color.White)
	dc.DrawRectangle(100, 100, 100, 100)
	dc.Fill()
	if a := alphaAt(dc.Image(), 160, 150); a != 255 {
		t.Errorf("alpha in circle ∩ rect = %d, want 255", a)
	}
	if a := alphaAt(dc.Image(), 140, 150); a != 0 {
		t.Errorf("alpha in circle outside rect = %d, want 0", a)
	}
}

func TestPushGroupOnOffsetCanvas(t *testing.T) {
	dc := offsetCanvas()
	dc.PushGroup()
	dc.SetColor(color.White)
	dc.DrawRectangle(120, 120, 10, 10)
	dc.Fill()
	dc.PopGroup()
	if a := alphaAt(dc.Image(), 125, 125); a != 255 {
		t.Errorf("group content alpha at (125,125) = %d, want 255", a)
	}
	if a := alphaAt(dc.Image(), 145, 145); a != 0 {
		t.Errorf("alpha outside group content = %d, want 0", a)
	}
}
