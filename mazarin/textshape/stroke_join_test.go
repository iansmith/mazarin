package textshape

import "testing"

// A join fills the outer side of the turn whichever way the path turns:
// Skia's MiterJoiner and RoundJoiner pick the outer side by
// is_clockwise(before, after) (src/core/SkStrokerPriv.cpp:50-56,
// :103-139, :141-240 @ skia d19e557ac3175dc4f9e52d86dd1476c4dfe06a3e).
// A wave of cubics, each point-symmetric about its midpoint, turns one
// way at its troughs and the other at its crests, so its stroke must be
// point-symmetric too: every pixel matches its mirror through the centre
// of the middle cubic (22.5, 15.5).
func TestStrokeJoin_WaveIsPointSymmetric(t *testing.T) {
	for _, tc := range []struct {
		name string
		join LineJoin
	}{{"miter", LineJoinMiter}, {"round", LineJoinRound}, {"bevel", LineJoinBevel}} {
		t.Run(tc.name, func(t *testing.T) {
			dc := NewDrawContext(60, 31, nil)
			dc.SetAntiAlias(true)
			dc.SetLineWidth(3)
			dc.SetLineJoin(tc.join)
			dc.SetMiterLimit(4)
			const y = 15.5
			dc.MoveTo(-15, y)
			for cx := -15.0; cx < 60; cx += 15 {
				dc.CubicTo(cx+7.5, y+10.5, cx+7.5, y-10.5, cx+15, y)
			}
			dc.Stroke()
			for py := 0; py < 31; py++ {
				for px := 8; px < 37; px++ {
					a, m := int(alphaAt(dc, px, py)), int(alphaAt(dc, 44-px, 30-py))
					if a-m > 2 || m-a > 2 {
						t.Errorf("alpha at (%d,%d) = %d, its mirror (%d,%d) = %d", px, py, a, 44-px, 30-py, m)
					}
				}
			}
		})
	}
}
