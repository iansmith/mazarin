package textshape

import "testing"

// DrawCircle starts a new subpath, as SkPath::addCircle starts a new
// contour (src/core/SkPath.cpp @ skia
// d19e557ac3175dc4f9e52d86dd1476c4dfe06a3e, Chromium a9f50e52's
// skia_revision), so several circles filled as one path are separate
// discs: nothing joins one to the next (MAZ-220). Three r=2.5 circles
// centred on x 10.5 reach x 13 at their rightmost points; the column at
// x 13 between them stays empty.
func TestDrawCircle_StartsNewSubpath(t *testing.T) {
	dc := NewDrawContext(20, 40, nil)
	for _, cy := range []float64{5, 15, 25} {
		dc.DrawCircle(10.5, cy, 2.5)
	}
	dc.Fill()
	if a := alphaAt(dc, 10, 15); a != 255 {
		t.Errorf("alpha at (10,15) = %d, want 255: inside the middle disc", a)
	}
	for _, y := range []int{9, 10, 20} {
		if a := alphaAt(dc, 13, y); a != 0 {
			t.Errorf("alpha at (13,%d) = %d, want 0: between the discs", y, a)
		}
	}
}
