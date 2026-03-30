package textshape

import (
	"path/filepath"
	"runtime"
	"testing"
)

func fontDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot determine test file path")
	}
	return filepath.Join(filepath.Dir(file), "..", "..", "fonts")
}

const (
	testFontLatin = "AtkinsonHyperlegible-Regular.ttf"
	testFontCJK   = "NotoSansCJKsc-Regular.otf"
)

func TestLayoutLatinHello(t *testing.T) {
	tl := NewTextLayout(fontDir(t))
	info, err := tl.OpenFont(OpenFontRequest{
		Path: testFontLatin, Size: 18,
	})
	if err != nil {
		t.Fatalf("OpenFont: %v", err)
	}

	run, err := tl.LayoutText(ShapingParams{
		Text:      "Hello",
		FontID:    info.FontID,
		Direction: LTR,
		Script:    ScriptLatin,
	})
	if err != nil {
		t.Fatalf("LayoutText: %v", err)
	}

	if len(run.Glyphs) != 5 {
		t.Errorf("expected 5 glyphs, got %d", len(run.Glyphs))
	}
	if run.TotalAdvance <= 0 {
		t.Errorf("TotalAdvance = %d, want > 0", run.TotalAdvance)
	}
	if run.Ascent <= 0 {
		t.Errorf("Ascent = %d, want > 0", run.Ascent)
	}

	// Glyphs should be positioned left-to-right with increasing X.
	for i := 1; i < len(run.Glyphs); i++ {
		if run.Glyphs[i].X <= run.Glyphs[i-1].X {
			t.Errorf("glyph %d X=%d not > glyph %d X=%d",
				i, run.Glyphs[i].X, i-1, run.Glyphs[i-1].X)
		}
	}

	// Each glyph should have non-empty alpha data.
	for i, g := range run.Glyphs {
		if g.Width == 0 || g.Height == 0 {
			t.Errorf("glyph %d has zero dimensions %dx%d", i, g.Width, g.Height)
		}
		if len(g.Alpha) == 0 {
			t.Errorf("glyph %d has empty alpha", i)
		}
	}
}

func TestMeasureText(t *testing.T) {
	tl := NewTextLayout(fontDir(t))
	info, err := tl.OpenFont(OpenFontRequest{
		Path: testFontLatin, Size: 18,
	})
	if err != nil {
		t.Fatal(err)
	}

	adv, err := tl.MeasureText(ShapingParams{
		Text:      "Hello",
		FontID:    info.FontID,
		Direction: LTR,
		Script:    ScriptLatin,
	})
	if err != nil {
		t.Fatal(err)
	}
	if adv <= 0 {
		t.Errorf("MeasureText advance = %d, want > 0", adv)
	}

	// Measure consistency: LayoutText total advance should match.
	run, err := tl.LayoutText(ShapingParams{
		Text:      "Hello",
		FontID:    info.FontID,
		Direction: LTR,
		Script:    ScriptLatin,
	})
	if err != nil {
		t.Fatal(err)
	}
	if run.TotalAdvance != adv {
		t.Errorf("MeasureText=%d != LayoutText.TotalAdvance=%d", adv, run.TotalAdvance)
	}
}

func TestLayoutCJK(t *testing.T) {
	tl := NewTextLayout(fontDir(t))
	info, err := tl.OpenFont(OpenFontRequest{
		Path: testFontCJK, Size: 24,
	})
	if err != nil {
		t.Fatalf("OpenFont: %v", err)
	}

	run, err := tl.LayoutText(ShapingParams{
		Text:      "中国人",
		FontID:    info.FontID,
		Direction: LTR,
		Script:    ScriptHan,
		Language:  "zh-Hans",
	})
	if err != nil {
		t.Fatalf("LayoutText: %v", err)
	}

	if len(run.Glyphs) != 3 {
		t.Errorf("expected 3 glyphs, got %d", len(run.Glyphs))
	}
	if run.TotalAdvance <= 0 {
		t.Errorf("TotalAdvance = %d, want > 0", run.TotalAdvance)
	}

	for i, g := range run.Glyphs {
		if len(g.Alpha) == 0 {
			t.Errorf("CJK glyph %d has empty alpha", i)
		}
	}
}

func TestShaperDirect(t *testing.T) {
	tl := NewTextLayout(fontDir(t))
	info, err := tl.OpenFont(OpenFontRequest{
		Path: testFontLatin, Size: 18,
	})
	if err != nil {
		t.Fatal(err)
	}

	run, err := tl.shaper.Shape(ShapingParams{
		Text:      "fi",
		FontID:    info.FontID,
		Direction: LTR,
		Script:    ScriptLatin,
		Language:  "en",
	})
	if err != nil {
		t.Fatal(err)
	}

	// "fi" may produce 1 glyph (fi ligature) or 2 glyphs depending on font.
	if len(run.Glyphs) == 0 {
		t.Error("shaping 'fi' produced zero glyphs")
	}

	for _, g := range run.Glyphs {
		if g.XAdvance <= 0 {
			t.Errorf("glyph GID=%d has XAdvance=%d, want > 0", g.GID, g.XAdvance)
		}
	}
}
