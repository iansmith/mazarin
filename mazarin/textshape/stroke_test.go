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

// Regression: stray line ops after pathClose (no intervening MoveTo) must
// not OOB on nextLine, and should be treated as an implicit subpath rooted
// at the close point. Caught by CodeRabbit on the initial MAZ-30 PR.
func TestStrokeExpand_StrayLineAfterClose(t *testing.T) {
	dc := newStrokeTestDC(LineJoinMiter)
	dc.path = []pathSeg{
		{op: pathMoveTo, args: [6]float64{10, 10}},
		{op: pathLineTo, args: [6]float64{50, 10}},
		{op: pathClose},                              // appends (50,10→10,10), wraps to first
		{op: pathLineTo, args: [6]float64{30, 30}},   // implicit subpath: (10,10→30,30)
	}

	// Must not panic. Should emit: 3 quads (line0, close-line, stray) +
	// 1 miter join at the wraparound (line0↔close) × 2 triangles. The
	// stray line is alone in its implicit subpath, so no join with it.
	expanded := dc.strokeExpand()
	if got := countShapes(expanded); got != 5 {
		t.Errorf("M L Z L: got %d shapes, want 5 (3 quads + 1 miter × 2 triangles)", got)
	}
}

// Regression: round join must use the shortest arc, not numeric sort of raw
// Atan2 results. Two segments whose directions straddle the ±π branch cut
// would otherwise produce a ~2π fan instead of a tiny corner fan.
func TestStrokeExpand_RoundJoinBranchCut(t *testing.T) {
	dc := newStrokeTestDC(LineJoinRound)
	dc.gs.lineWidth = 0.002
	const eps = 0.01
	dc.path = []pathSeg{
		{op: pathMoveTo, args: [6]float64{1, eps}},  // seg1 into origin: atan2 ≈ -π + ε
		{op: pathLineTo, args: [6]float64{0, 0}},
		{op: pathLineTo, args: [6]float64{-1, eps}}, // seg2 from origin: atan2 ≈ +π - ε
	}

	// Walk shapes by MoveTo boundaries, collect only fan triangles
	// (addTri = M+L+L+Z, 2 LineTos; quad = M+L+L+L+Z, 3 LineTos). Fan
	// triangles have MoveTo at the pivot (origin in this test).
	expanded := dc.strokeExpand()
	var fanVerts [][2]float64
	for i := 0; i < len(expanded); {
		if expanded[i].op != pathMoveTo {
			i++
			continue
		}
		j := i + 1
		for j < len(expanded) && expanded[j].op == pathLineTo {
			j++
		}
		if j-i-1 == 2 && near(expanded[i].args[0], 0) && near(expanded[i].args[1], 0) {
			fanVerts = append(fanVerts, [2]float64{expanded[i+1].args[0], expanded[i+1].args[1]})
			fanVerts = append(fanVerts, [2]float64{expanded[i+2].args[0], expanded[i+2].args[1]})
		}
		i = j + 1 // skip past pathClose
	}
	if len(fanVerts) == 0 {
		t.Fatal("no fan triangles found")
	}
	// Real sweep ~2ε rad → chord on hw=0.001 ≈ 2e-5. A full 2π fan has
	// max chord = diameter = 2·hw = 2e-3. A threshold of hw/4 cleanly separates.
	maxDist := 0.0
	for i := range fanVerts {
		for j := i + 1; j < len(fanVerts); j++ {
			d := math.Hypot(fanVerts[i][0]-fanVerts[j][0], fanVerts[i][1]-fanVerts[j][1])
			if d > maxDist {
				maxDist = d
			}
		}
	}
	if maxDist > dc.gs.lineWidth/4 {
		t.Errorf("round-join fan max chord %.6f > %.6f — branch-cut bug regressed",
			maxDist, dc.gs.lineWidth/4)
	}
}

// Regression: closed subpaths must emit no caps (the wrap fills the seam).
// Before the per-subpath cap fix, LineCapRound on a closed rect would emit
// 32 spurious cap triangles at the closure seam.
func TestStrokeExpand_ClosedRectNoCaps(t *testing.T) {
	dc := newStrokeTestDC(LineJoinMiter)
	dc.gs.lineCap = LineCapRound // would emit 16 fan triangles per cap if buggy
	dc.path = closedRectPath(10, 10, 40, 40)

	// 4 quads + 4 miter joins × 2 triangles = 12. Caps must contribute 0.
	if got := countShapes(dc.strokeExpand()); got != 12 {
		t.Errorf("closed rect with LineCapRound: got %d shapes, want 12 (caps must be skipped on closed subpaths)", got)
	}
}

func near(a, b float64) bool {
	return math.Abs(a-b) < 1e-6
}
