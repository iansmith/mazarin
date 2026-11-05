package textshape

import (
	"image"
	"image/color"
	"testing"
)

// Skia clips every draw to the device clip (SkCanvas::onDrawImage2 →
// SkDraw with the raster clip). An image drawn under a non-identity affine
// transform with anti-aliasing off takes the xdraw.BiLinear.Transform path,
// and a rect-only clip (no clip mask) must still bound it.
func TestDrawImageAffineHonoursClipRect(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 10, 10))
	for y := 0; y < 10; y++ {
		for x := 0; x < 10; x++ {
			src.SetRGBA(x, y, color.RGBA{G: 255, A: 255})
		}
	}

	dc := NewDrawContext(40, 40, nil)
	dc.SetAntiAlias(false)
	// Whole-pixel rect clip: stays a clipRect with no clipMask.
	dc.DrawRectangle(0, 0, 10, 40)
	dc.ClipPath(ClipOpIntersect, false)
	if dc.gs.clipMask != nil {
		t.Fatalf("setup: expected a rect-only clip, got a clip mask")
	}
	dc.Scale(2, 2) // non-identity affine: the BiLinear path
	dc.DrawImage(src, 0, 0)

	if got := dc.im.RGBAAt(5, 5); got.G != 255 {
		t.Errorf("inside clip (5,5): %v, want green", got)
	}
	if got := dc.im.RGBAAt(15, 5); got.A != 0 {
		t.Errorf("outside clip (15,5): %v, want untouched (transparent)", got)
	}
}
