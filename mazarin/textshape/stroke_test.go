package textshape

import (
	"math"
	"testing"
)

// newStrokeTestDC builds a stroke-only DrawContext. nil GlyphProvider is fine
// because strokeExpand never touches the text layout.
func newStrokeTestDC(lineJoin LineJoin) *DrawContextImpl {
	dc := NewDrawContext(100, 100, nil)
	dc.gs.lineWidth = 2
	dc.gs.lineCap = LineCapButt // no cap geometry — keeps shape counts pure
	dc.gs.lineJoin = lineJoin
	return dc
}

// countShapes counts MoveTo ops — each starts a closed sub-polygon.
func countShapes(segs []pathSeg) int {
	n := 0
	for _, s := range segs {
		if s.op == pathMoveTo {
			n++
		}
	}
	return n
}

// closedRectPath builds the path ops directly (skipping DrawRectangle so the
// current transform doesn't apply).
func closedRectPath(x, y, w, h float64) []pathSeg {
	return []pathSeg{
		{op: pathMoveTo, args: [6]float64{x, y}},
		{op: pathLineTo, args: [6]float64{x + w, y}},
		{op: pathLineTo, args: [6]float64{x + w, y + h}},
		{op: pathLineTo, args: [6]float64{x, y + h}},
		{op: pathClose},
	}
}

func TestStrokeExpand_ClosedRectMiter(t *testing.T) {
	dc := newStrokeTestDC(LineJoinMiter)
	dc.path = closedRectPath(10, 10, 40, 40)

	expanded := dc.strokeExpand()

	// 4 sides + close-line = 4 lines → 4 quads; 4 corners × 2 miter triangles = 12 shapes.
	if got := countShapes(expanded); got != 12 {
		t.Errorf("closed rect with miter: got %d shapes, want 12", got)
	}

	// Top-right corner pivot is (50, 10); with hw=1 the miter point sits
	// at (51, 9) — outward by hw on both axes for a 90° corner.
	hw := dc.gs.lineWidth / 2
	wantMx, wantMy := 50.0+hw, 10.0-hw
	foundMiter := false
	for _, s := range expanded {
		if s.op == pathLineTo && near(s.args[0], wantMx) && near(s.args[1], wantMy) {
			foundMiter = true
			break
		}
	}
	if !foundMiter {
		t.Errorf("expected miter point (%g, %g) in expanded geometry", wantMx, wantMy)
	}
}

func TestStrokeExpand_MiterLimitFallback(t *testing.T) {
	// At a 90° corner sin(halfAngle) = √2/2 ≈ 0.707, so any miterLimit < √2
	// forces fallback.
	dc := newStrokeTestDC(LineJoinMiter)
	dc.gs.miterLimit = 1.0
	dc.path = closedRectPath(10, 10, 40, 40)

	expanded := dc.strokeExpand()

	// Each corner emits 1 bevel triangle instead of 2 miter triangles → 4 quads + 4 = 8.
	if got := countShapes(expanded); got != 8 {
		t.Errorf("closed rect with miterLimit=1: got %d shapes, want 8", got)
	}

	// Bevel triangles use only A1/P/A2 vertices, all inside the rect's
	// hw-expanded bounds [9, 51]. A miter point would land outside (e.g. 51, 9).
	for _, s := range expanded {
		if s.op != pathLineTo {
			continue
		}
		x, y := s.args[0], s.args[1]
		if x > 51 || x < 9 || y > 51 || y < 9 {
			t.Errorf("found out-of-rect vertex (%g, %g) — fallback should produce no miter point", x, y)
		}
	}
}

func TestStrokeExpand_RoundWraparound(t *testing.T) {
	// Round join is 8 fan triangles. 4 quads + 4 wrapped corners × 8 = 36.
	// Without the wraparound fix, only 3 corners get joins (28 shapes).
	dc := newStrokeTestDC(LineJoinRound)
	dc.path = closedRectPath(10, 10, 40, 40)

	if got := countShapes(dc.strokeExpand()); got != 36 {
		t.Errorf("closed rect with round joins: got %d shapes, want 36", got)
	}
}

// Regression guard: open paths must not emit a wraparound join.
func TestStrokeExpand_OpenPathNoWraparound(t *testing.T) {
	dc := newStrokeTestDC(LineJoinMiter)
	dc.path = []pathSeg{
		{op: pathMoveTo, args: [6]float64{10, 10}},
		{op: pathLineTo, args: [6]float64{50, 10}},
		{op: pathLineTo, args: [6]float64{50, 50}},
		{op: pathLineTo, args: [6]float64{10, 50}},
	}

	// 3 quads + 2 inter-segment miters × 2 triangles = 7.
	if got := countShapes(dc.strokeExpand()); got != 7 {
		t.Errorf("open path with miter: got %d shapes, want 7", got)
	}
}

// MAZ-30 acceptance criterion: an angle θ < 2·asin(1/miterLimit) between
// segment directions degrades to bevel. With miterLimit=2, cutoff is θ ≈ 60°;
// 50° is below.
func TestStrokeExpand_MiterFallbackThreshold(t *testing.T) {
	dc := newStrokeTestDC(LineJoinMiter)
	dc.gs.miterLimit = 2.0

	// Two segments meeting at the origin with a 50° interior angle.
	const thetaDeg = 50.0
	angle := math.Pi - thetaDeg*math.Pi/180
	dc.path = []pathSeg{
		{op: pathMoveTo, args: [6]float64{-10, 0}},
		{op: pathLineTo, args: [6]float64{0, 0}},
		{op: pathLineTo, args: [6]float64{10 * math.Cos(angle), 10 * math.Sin(angle)}},
	}

	// Fallback → 2 quads + 1 bevel triangle = 3. Accepted miter would be 4.
	if got := countShapes(dc.strokeExpand()); got != 3 {
		t.Errorf("acute corner (θ=%.0f°, miterLimit=2): got %d shapes, want 3 (fallback to bevel)", thetaDeg, got)
	}
}

func near(a, b float64) bool {
	return math.Abs(a-b) < 1e-6
}
