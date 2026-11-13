package textshape

import (
	"math"
	"testing"
)

// SetDash strokes with Skia's dash rules (SkDashPathEffect / SkDashPath::
// InternalFilter, src/utils/SkDashPath.cpp:309-458 @ skia
// d19e557ac3175dc4f9e52d86dd1476c4dfe06a3e, Chromium a9f50e52's
// skia_revision): with phase 0, an "on" interval starts every on+off along
// each contour while it starts before the contour's end, the last cut at the
// end (MAZ-219).

func dashContext(w, h int, width float64, cp LineCap, lj LineJoin) *DrawContextImpl {
	dc := NewDrawContext(w, h, nil)
	dc.SetLineWidth(width)
	dc.SetLineCap(cp)
	dc.SetLineJoin(lj)
	return dc
}

func wantAlpha(t *testing.T, dc *DrawContextImpl, x, y int, opaque bool, why string) {
	t.Helper()
	if a := alphaAt(dc, x, y); (a == 255) != opaque {
		t.Errorf("alpha at (%d,%d) = %d, want opaque=%v: %s", x, y, a, opaque, why)
	}
}

// A straight line dashed 4 on, 2 off from x 0 to 21 strokes x 0-4, 6-10,
// 12-16 and 18-21, the last dash cut at the line's end.
func TestSetDash_StraightLine(t *testing.T) {
	dc := dashContext(30, 10, 2, LineCapButt, LineJoinMiter)
	dc.SetDash(4, 2)
	dc.MoveTo(0, 5)
	dc.LineTo(21, 5)
	dc.Stroke()
	for _, p := range []struct {
		x      int
		opaque bool
		why    string
	}{
		{2, true, "first dash, x 0-4"},
		{4, false, "first gap, x 4-6"},
		{5, false, "first gap, x 4-6"},
		{7, true, "second dash, x 6-10"},
		{19, true, "last dash, x 18-21"},
		{20, true, "last dash, x 18-21"},
		{21, false, "past the line's end"},
	} {
		wantAlpha(t, dc, p.x, 4, p.opaque, p.why)
	}
}

// A closed 22x10 rectangle (perimeter 64) dashed 6 on, 4 off: the pattern
// runs on around the corners, and the last dash, starting 60 along, joins
// the first across the start corner (5,5) as one stroke, so the mitered
// corner is filled rather than notched by two butt ends.
func TestSetDash_ClosedContourJoinsAcrossStart(t *testing.T) {
	dc := dashContext(40, 25, 2, LineCapButt, LineJoinMiter)
	dc.SetDash(6, 4)
	dc.MoveTo(5, 5)
	dc.LineTo(27, 5)
	dc.LineTo(27, 15)
	dc.LineTo(5, 15)
	dc.ClosePath()
	dc.Stroke()
	wantAlpha(t, dc, 7, 5, true, "first dash along the top, path 0-6 (x 5-11)")
	wantAlpha(t, dc, 13, 5, false, "first gap along the top, path 6-10 (x 11-15)")
	wantAlpha(t, dc, 26, 7, true, "the dash at path 20-26 turns the top-right corner (y 5-9)")
	wantAlpha(t, dc, 5, 7, true, "the last dash, path 60-64, runs up the left side (y 9-5)")
	wantAlpha(t, dc, 4, 4, true, "the last and first dashes join across the start corner (miter)")
}

// Zero-length dashes with round caps are dots: SetDash(0, 6) on a 4px line
// from x 2 to 40 puts dots of radius 2 at x 2, 8, 14, ...
func TestSetDash_ZeroLengthRoundCapDots(t *testing.T) {
	dc := dashContext(50, 10, 4, LineCapRound, LineJoinMiter)
	dc.SetDash(0, 6)
	dc.MoveTo(2, 5)
	dc.LineTo(40, 5)
	dc.Stroke()
	wantAlpha(t, dc, 8, 5, true, "dot centred at x 8")
	wantAlpha(t, dc, 5, 5, false, "between the dots at x 2 and 8")
	wantAlpha(t, dc, 14, 5, true, "dot centred at x 14")
}

// A dashed circle is dashed along its arc length: SetDash(5, 5) leaves about
// half of a radius-10 circle's stroke unpainted.
func TestSetDash_Circle(t *testing.T) {
	dc := dashContext(40, 40, 2, LineCapButt, LineJoinMiter)
	dc.SetDash(5, 5)
	dc.DrawCircle(20, 20, 10)
	dc.Stroke()
	painted := 0
	const samples = 360
	for i := 0; i < samples; i++ {
		a := 2 * math.Pi * float64(i) / samples
		x, y := 20+10*math.Cos(a), 20+10*math.Sin(a)
		if alphaAt(dc, int(math.Floor(x)), int(math.Floor(y))) > 128 {
			painted++
		}
	}
	if f := float64(painted) / samples; f < 0.35 || f > 0.65 {
		t.Errorf("%.0f%% of the circle's stroke painted, want about half", f*100)
	}
}

// SetDash with no intervals, or an invalid pattern (odd count, a negative
// interval, all zeros; SkDashPath::ValidDashPath, :474-487), strokes solid.
func TestSetDash_InvalidPatternStrokesSolid(t *testing.T) {
	for _, dashes := range [][]float64{nil, {4}, {4, -2}, {0, 0}} {
		dc := dashContext(30, 10, 2, LineCapButt, LineJoinMiter)
		dc.SetDash(dashes...)
		dc.MoveTo(0, 5)
		dc.LineTo(21, 5)
		dc.Stroke()
		wantAlpha(t, dc, 5, 4, true, "solid stroke for an invalid dash pattern")
	}
}

// A dash that starts exactly on a vertex still gets its start cap: square
// caps on a 4-on/4-off dash of the polyline (2,10)-(10,10)-(30,10) put the
// second dash at x 10-14, capped out to x 9.
func TestSetDash_DashStartingAtVertexIsCapped(t *testing.T) {
	dc := dashContext(40, 20, 2, LineCapSquare, LineJoinMiter)
	dc.SetDash(4, 4)
	dc.MoveTo(2, 10)
	dc.LineTo(10, 10)
	dc.LineTo(30, 10)
	dc.Stroke()
	wantAlpha(t, dc, 9, 10, true, "second dash's square start cap, x 9-10")
	wantAlpha(t, dc, 7, 10, false, "first gap, x 6-9 after the first dash's cap")
}
