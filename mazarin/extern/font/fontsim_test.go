package font

import (
	"path/filepath"
	"runtime"
	"testing"
	"unsafe"
)

// fontDir returns the absolute path to the project's fonts/ directory.
func fontDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot determine test file path")
	}
	// file is mazarin/extern/font/fontsim_test.go; fonts/ is at project root
	return filepath.Join(filepath.Dir(file), "..", "..", "..", "fonts")
}

const testFont = "AtkinsonHyperlegible-Regular.ttf"

func TestStructSizes(t *testing.T) {
	if got := unsafe.Sizeof(cacheHeader{}); got != 60 {
		t.Errorf("cacheHeader size = %d, want 60", got)
	}
	if got := unsafe.Sizeof(glyphMapEntry{}); got != 8 {
		t.Errorf("glyphMapEntry size = %d, want 8", got)
	}
	if got := unsafe.Sizeof(glyphEntry{}); got != 16 {
		t.Errorf("glyphEntry size = %d, want 16", got)
	}
}

func TestOpenFont(t *testing.T) {
	client := NewFontClient(fontDir(t))
	info, err := client.OpenFont(OpenFontRequest{
		Path: testFont, Size: 18,
	})
	if err != nil {
		t.Fatalf("OpenFont: %v", err)
	}
	if info.FontID < 0 {
		t.Fatal("OpenFont returned negative FontID")
	}
	if info.Height <= 0 {
		t.Errorf("Height = %d, want > 0", info.Height)
	}
	if info.Ascent <= 0 {
		t.Errorf("Ascent = %d, want > 0", info.Ascent)
	}
	if info.NumGlyphs == 0 {
		t.Error("NumGlyphs = 0, want > 0")
	}
	if info.CacheSize == 0 {
		t.Error("CacheSize = 0, want > 0")
	}
}

func TestGlyphInfoForA(t *testing.T) {
	client := NewFontClient(fontDir(t))
	info, err := client.OpenFont(OpenFontRequest{
		Path: testFont, Size: 18,
	})
	if err != nil {
		t.Fatalf("OpenFont: %v", err)
	}

	gi, err := client.GlyphInfo(info.FontID, 'A')
	if err != nil {
		t.Fatalf("GlyphInfo('A'): %v", err)
	}
	if gi == nil {
		t.Fatal("GlyphInfo('A') returned nil")
	}
	if gi.Width == 0 || gi.Height == 0 {
		t.Errorf("glyph 'A' size = %dx%d, want non-zero", gi.Width, gi.Height)
	}
	if gi.Advance <= 0 {
		t.Errorf("glyph 'A' advance = %d, want > 0", gi.Advance)
	}
}

func TestGlyphAlphaForA(t *testing.T) {
	client := NewFontClient(fontDir(t))
	info, err := client.OpenFont(OpenFontRequest{
		Path: testFont, Size: 18,
	})
	if err != nil {
		t.Fatalf("OpenFont: %v", err)
	}

	gi, alphaErr := client.GlyphInfo(info.FontID, 'A')
	if alphaErr != nil {
		t.Fatalf("GlyphInfo: %v", alphaErr)
	}

	alpha, err := client.GlyphAlpha(info.FontID, 'A')
	if err != nil {
		t.Fatalf("GlyphAlpha('A'): %v", err)
	}
	if alpha == nil {
		t.Fatal("GlyphAlpha('A') returned nil")
	}

	expected := int(gi.Width) * int(gi.Height)
	if len(alpha) != expected {
		t.Errorf("alpha len = %d, want %d (%dx%d)", len(alpha), expected, gi.Width, gi.Height)
	}

	// At least some pixels should be non-zero for 'A'.
	nonZero := 0
	for _, b := range alpha {
		if b != 0 {
			nonZero++
		}
	}
	if nonZero == 0 {
		t.Error("all alpha pixels are zero for 'A'")
	}
}

func TestCacheHit(t *testing.T) {
	client := NewFontClient(fontDir(t))
	req := OpenFontRequest{Path: testFont, Size: 18}

	info1, err := client.OpenFont(req)
	if err != nil {
		t.Fatal(err)
	}
	info2, err := client.OpenFont(req)
	if err != nil {
		t.Fatal(err)
	}
	if info1.FontID != info2.FontID {
		t.Errorf("cache hit: FontID %d != %d", info1.FontID, info2.FontID)
	}
}

func TestDifferentSizes(t *testing.T) {
	client := NewFontClient(fontDir(t))

	info12, err := client.OpenFont(OpenFontRequest{Path: testFont, Size: 12})
	if err != nil {
		t.Fatal(err)
	}
	info24, err := client.OpenFont(OpenFontRequest{Path: testFont, Size: 24})
	if err != nil {
		t.Fatal(err)
	}
	if info12.FontID == info24.FontID {
		t.Error("different sizes should produce different FontIDs")
	}
	if info12.Height == info24.Height {
		t.Error("different sizes should produce different Heights")
	}
}

func TestGlyphMapSorted(t *testing.T) {
	client := NewFontClient(fontDir(t))
	info, err := client.OpenFont(OpenFontRequest{
		Path: testFont, Size: 18,
	})
	if err != nil {
		t.Fatal(err)
	}

	fc := client.caches[info.FontID]
	for i := 1; i < len(fc.mapEntries); i++ {
		if fc.mapEntries[i].Codepoint <= fc.mapEntries[i-1].Codepoint {
			t.Errorf("glyph map not sorted at index %d: %d <= %d",
				i, fc.mapEntries[i].Codepoint, fc.mapEntries[i-1].Codepoint)
			break
		}
	}
}

func TestGlyphAlignment(t *testing.T) {
	client := NewFontClient(fontDir(t))
	info, err := client.OpenFont(OpenFontRequest{
		Path: testFont, Size: 18,
	})
	if err != nil {
		t.Fatal(err)
	}

	fc := client.caches[info.FontID]
	for i, me := range fc.mapEntries {
		if me.Offset%4 != 0 {
			t.Errorf("glyph %d (U+%04X) at offset %d is not 4-byte aligned",
				i, me.Codepoint, me.Offset)
			break
		}
	}
}

func TestTier2TransparentFill(t *testing.T) {
	// NotoSansCJKsc-Regular.otf has ~65K glyphs; only ~6K fit in 2MB.
	// CJK ideographs (U+4E00+) are guaranteed tier-2.
	const cjkFont = "NotoSansCJKsc-Regular.otf"

	client := NewFontClient(fontDir(t))
	info, err := client.OpenFont(OpenFontRequest{Path: cjkFont, Size: 18})
	if err != nil {
		t.Fatalf("OpenFont: %v", err)
	}

	fc := client.caches[info.FontID]
	t.Logf("tier-1 glyphs: %d, cache used: %d bytes", info.NumGlyphs, info.CacheSize)

	// Pick CJK codepoints that are NOT in tier-1.
	testCPs := []rune{'中', '国', '人', '大', '学'}
	for _, cp := range testCPs {
		if binarySearch(fc.mapEntries, cp) >= 0 {
			continue // skip if it happens to be in tier-1
		}

		// First lookup — triggers tier-2 fetch from sim.
		gi, alpha, err := client.Glyph(info.FontID, cp)
		if err != nil {
			t.Fatalf("Glyph(U+%04X): %v", cp, err)
		}
		if gi == nil {
			t.Fatalf("Glyph(U+%04X) returned nil", cp)
		}
		if gi.Width == 0 || gi.Height == 0 {
			t.Errorf("U+%04X: zero-size glyph %dx%d", cp, gi.Width, gi.Height)
		}
		if len(alpha) != int(gi.Width)*int(gi.Height) {
			t.Errorf("U+%04X: alpha len %d != %dx%d", cp, len(alpha), gi.Width, gi.Height)
		}

		// Second lookup — must hit tier-2 cache, same result.
		gi2, alpha2, err := client.Glyph(info.FontID, cp)
		if err != nil {
			t.Fatal(err)
		}
		if gi2.Advance != gi.Advance || gi2.Width != gi.Width {
			t.Errorf("U+%04X: tier-2 cache mismatch", cp)
		}
		if len(alpha2) != len(alpha) {
			t.Errorf("U+%04X: tier-2 alpha len mismatch", cp)
		}
	}
}

func TestGlyphConvenience(t *testing.T) {
	client := NewFontClient(fontDir(t))
	info, err := client.OpenFont(OpenFontRequest{
		Path: testFont, Size: 18,
	})
	if err != nil {
		t.Fatal(err)
	}

	gi, alpha, err := client.Glyph(info.FontID, 'M')
	if err != nil {
		t.Fatalf("Glyph('M'): %v", err)
	}
	if gi == nil {
		t.Fatal("Glyph('M') info is nil")
	}
	if alpha == nil {
		t.Fatal("Glyph('M') alpha is nil")
	}
	if len(alpha) != int(gi.Width)*int(gi.Height) {
		t.Errorf("alpha len %d != %d*%d", len(alpha), gi.Width, gi.Height)
	}
}
