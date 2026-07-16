package textshape

import (
	"math"
	"testing"

	goFont "github.com/go-text/typesetting/font"
)

// outlineTopY recomputes the scaled, Y-flipped top edge of a glyph outline the
// same way RenderGlyph does — independently, rather than by calling into it, so
// the assertions built on it aren't tautological — letting a test talk about the
// fractional part of minY without reaching into RenderGlyph's locals.
func outlineTopY(face *goFont.Face, gid goFont.GID, scale float32) (float32, bool) {
	outline, ok := face.GlyphDataOutline(uint16(gid))
	if !ok || len(outline.Segments) == 0 {
		return 0, false
	}
	var minY float32
	first := true
	for _, seg := range outline.Segments {
		for _, pt := range seg.ArgsSlice() {
			y := -pt.Y * scale
			if first || y < minY {
				minY = y
				first = false
			}
		}
	}
	return minY, !first
}

// TestRenderGlyphDrawRectMatchesMask guards the invariant fontcache/face.go
// relies on when it blits: the draw rect it builds from DrMin*/DrMax* is paired
// with a mask whose bounds are image.Rect(0, 0, Width, Height). If the rect and
// the mask disagree on size, image/draw silently clips or shifts the glyph
// rather than failing, so nothing downstream would report it.
func TestRenderGlyphDrawRectMatchesMask(t *testing.T) {
	p := loadTestFace(t, testFontLatin)
	df := p.fonts[0]

	checked := 0
	for _, ch := range "AHIToqgy0" {
		gid, ok := df.face.NominalGlyph(ch)
		if !ok || gid == 0 {
			continue
		}
		info, _ := RenderGlyph(df.face, gid, df.scale)
		if info == nil || info.Width == 0 || info.Height == 0 {
			continue
		}
		if got, want := int(info.DrMaxX)-int(info.DrMinX), int(info.Width); got != want {
			t.Errorf("'%c': DrMaxX-DrMinX = %d, want Width = %d", ch, got, want)
		}
		if got, want := int(info.DrMaxY)-int(info.DrMinY), int(info.Height); got != want {
			t.Errorf("'%c': DrMaxY-DrMinY = %d, want Height = %d", ch, got, want)
		}
		checked++
	}
	if checked == 0 {
		t.Fatal("no glyphs rendered; test proved nothing")
	}
}

// TestRenderGlyphGridFitsTopEdge is the LOU-367 regression guard.
//
// RenderGlyph rasterizes with offY = minY, so the outline's topmost edge maps to
// mask row 0.0 exactly. For a glyph whose top is a flat horizontal edge — the
// stems of 'H' — that makes row 0 fully opaque. Flooring the origin instead (the
// pre-LOU-367 behaviour) starts the edge frac(minY) down into row 0, cutting its
// coverage to ~(1-frac(minY))*255 and spreading the shape across an extra
// antialiased row. That is the seam louis14's css-multicol column-rule reftests
// caught.
//
// The fractional-minY assertion below is load-bearing: if the test font happened
// to put 'H' on an integral top edge, floor and round would agree and this test
// would pass against the very bug it exists to catch.
func TestRenderGlyphGridFitsTopEdge(t *testing.T) {
	p := loadTestFace(t, testFontLatin)
	df := p.fonts[0]

	const ch = 'H'
	gid, ok := df.face.NominalGlyph(ch)
	if !ok || gid == 0 {
		t.Skipf("test font has no glyph for %q", ch)
	}
	minY, ok := outlineTopY(df.face, gid, df.scale)
	if !ok {
		t.Skipf("test font has no outline for %q", ch)
	}
	if minY == float32(math.Trunc(float64(minY))) {
		t.Fatalf("minY = %v is integral, so floor and round agree and this test "+
			"cannot distinguish grid-fitted from floored placement", minY)
	}

	info, alpha := RenderGlyph(df.face, gid, df.scale)
	if info == nil || info.Width == 0 || info.Height == 0 {
		t.Fatalf("%q did not rasterize", ch)
	}

	// Row 0 must contain the flat top edge at full coverage.
	var maxRow0 byte
	for x := 0; x < int(info.Width); x++ {
		if a := alpha[x]; a > maxRow0 {
			maxRow0 = a
		}
	}
	if maxRow0 != 255 {
		t.Errorf("%q: max alpha in mask row 0 = %d, want 255 — the top edge is not "+
			"grid-fitted onto row 0 (minY = %v, DrMinY = %d)",
			ch, maxRow0, minY, info.DrMinY)
	}

	// DrMinY is the rounded top edge: the nearest whole device row to minY.
	if want := int16(math.Round(float64(minY))); info.DrMinY != want {
		t.Errorf("%q: DrMinY = %d, want round(minY) = %d", ch, info.DrMinY, want)
	}
}
