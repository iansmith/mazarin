package textshape

import (
	"image"
	"image/color"
	"math"
	"testing"
)

// Skia clips every draw to the device clip (SkCanvas::onDrawImage2 →
// SkDraw with the raster clip). An image under a non-identity affine
// transform takes the xdraw.BiLinear.Transform path when anti-aliasing is
// off, or when it is on and the image edges land on the pixel grid. A
// rect-only clip (no clip mask) must still bound it.
func TestDrawImageAffineHonoursClipRect(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 10, 10))
	for y := 0; y < 10; y++ {
		for x := 0; x < 10; x++ {
			src.SetRGBA(x, y, color.RGBA{G: 255, A: 255})
		}
	}
	clip := image.Rect(10, 10, 20, 30)

	for _, tc := range []struct {
		name      string
		antiAlias bool
		transform func(dc *DrawContextImpl)
		inside    image.Point
	}{
		{"aa-off scale", false, func(dc *DrawContextImpl) { dc.Scale(2, 2) }, image.Pt(12, 12)},
		{"aa-on on-grid scale", true, func(dc *DrawContextImpl) { dc.Scale(2, 2) }, image.Pt(12, 12)},
		{"aa-off rotate", false, func(dc *DrawContextImpl) {
			dc.Translate(15, 5)
			dc.Rotate(math.Pi / 4)
		}, image.Pt(15, 15)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dc := NewDrawContext(40, 40, nil)
			dc.SetAntiAlias(tc.antiAlias)
			dc.DrawRectangle(float64(clip.Min.X), float64(clip.Min.Y), float64(clip.Dx()), float64(clip.Dy()))
			dc.ClipPath(ClipOpIntersect, false)
			if dc.gs.clipMask != nil {
				t.Fatalf("setup: expected a rect-only clip, got a clip mask")
			}
			tc.transform(dc)
			dc.DrawImage(src, 0, 0)

			if got := dc.im.RGBAAt(tc.inside.X, tc.inside.Y); got.G == 0 {
				t.Errorf("inside clip %v: %v, want green", tc.inside, got)
			}
			for y := 0; y < 40; y++ {
				for x := 0; x < 40; x++ {
					if image.Pt(x, y).In(clip) {
						continue
					}
					if got := dc.im.RGBAAt(x, y); got != (color.RGBA{}) {
						t.Fatalf("outside clip (%d,%d): %v, want untouched", x, y, got)
					}
				}
			}
		})
	}
}
