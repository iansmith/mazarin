package textshape

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"unsafe"
)

func testFontPath(t *testing.T, name string) string {
	t.Helper()
	return filepath.Join(fontDir(t), name)
}

func loadTestFace(t *testing.T, name string) *DirectGlyphProvider {
	t.Helper()
	p := NewDirectGlyphProvider(fontDir(t))
	_, err := p.OpenFont(OpenFontRequest{Path: name, Size: 18})
	if err != nil {
		t.Fatalf("OpenFont: %v", err)
	}
	return p
}

func TestBuildCacheHeader(t *testing.T) {
	p := loadTestFace(t, testFontLatin)
	df := p.fonts[0]
	if df.cache == nil {
		t.Fatal("cache is nil after OpenFont")
	}

	hdr := (*glyphCacheHeader)(unsafe.Pointer(&df.cache[0]))
	if hdr.Magic != cacheV2Magic {
		t.Errorf("magic = 0x%x, want 0x%x", hdr.Magic, cacheV2Magic)
	}
	if hdr.Version != cacheV2Version {
		t.Errorf("version = %d, want %d", hdr.Version, cacheV2Version)
	}
	if hdr.NumGlyphs == 0 {
		t.Error("NumGlyphs = 0")
	}
	if hdr.NumCPMap == 0 {
		t.Error("NumCPMap = 0")
	}
	if hdr.GIDMapOff == 0 || hdr.GIDMapOff >= hdr.TotalSize {
		t.Errorf("GIDMapOff = %d, TotalSize = %d", hdr.GIDMapOff, hdr.TotalSize)
	}
	if hdr.CPMapOff == 0 || hdr.CPMapOff >= hdr.TotalSize {
		t.Errorf("CPMapOff = %d, TotalSize = %d", hdr.CPMapOff, hdr.TotalSize)
	}
	if hdr.TotalSize%cachePageSize != 0 {
		t.Errorf("TotalSize %d not page-aligned (page=%d)", hdr.TotalSize, cachePageSize)
	}
	if hdr.Ascent <= 0 {
		t.Errorf("Ascent = %d, want > 0", hdr.Ascent)
	}
	t.Logf("Cache: %d glyphs, %d CP entries, %d bytes (%.1f KB)",
		hdr.NumGlyphs, hdr.NumCPMap, hdr.TotalSize, float64(hdr.TotalSize)/1024)
}

func TestGIDLookupMatchesRenderGlyph(t *testing.T) {
	p := loadTestFace(t, testFontLatin)
	df := p.fonts[0]
	face := df.face

	// Test several ASCII characters.
	for _, ch := range "AHello0" {
		gid, ok := face.NominalGlyph(ch)
		if !ok || gid == 0 {
			continue
		}

		// Direct render.
		directInfo, directAlpha := RenderGlyph(face, gid, df.scale)
		if directInfo == nil {
			continue
		}

		// Cache lookup.
		cacheInfo, cacheAlpha := LookupByGID(df.cache, uint32(gid))
		if cacheInfo == nil {
			t.Errorf("GID %d ('%c'): cache miss", gid, ch)
			continue
		}

		if cacheInfo.Advance != directInfo.Advance {
			t.Errorf("GID %d ('%c'): Advance cache=%d direct=%d", gid, ch, cacheInfo.Advance, directInfo.Advance)
		}
		if cacheInfo.Width != directInfo.Width || cacheInfo.Height != directInfo.Height {
			t.Errorf("GID %d ('%c'): size cache=%dx%d direct=%dx%d",
				gid, ch, cacheInfo.Width, cacheInfo.Height, directInfo.Width, directInfo.Height)
		}
		if !bytes.Equal(cacheAlpha, directAlpha) {
			t.Errorf("GID %d ('%c'): alpha bytes differ", gid, ch)
		}
	}
}

func TestCodepointLookup(t *testing.T) {
	p := loadTestFace(t, testFontLatin)
	df := p.fonts[0]
	face := df.face

	gidExpected, ok := face.NominalGlyph('A')
	if !ok {
		t.Fatal("no GID for 'A'")
	}

	gid, info, alpha := lookupByCodepoint(df.cache, uint32('A'))
	if info == nil {
		t.Fatal("codepoint lookup returned nil for 'A'")
	}
	if gid != uint32(gidExpected) {
		t.Errorf("GID = %d, want %d", gid, gidExpected)
	}
	if info.Width == 0 || info.Height == 0 {
		t.Error("'A' has zero dimensions")
	}
	if len(alpha) == 0 {
		t.Error("'A' has empty alpha")
	}
}

func TestCacheMissReturnsNil(t *testing.T) {
	p := loadTestFace(t, testFontLatin)
	df := p.fonts[0]

	info, alpha := LookupByGID(df.cache, 0xDEAD)
	if info != nil {
		t.Error("expected nil for bogus GID")
	}
	if alpha != nil {
		t.Error("expected nil alpha for bogus GID")
	}
}

func TestSpaceGlyph(t *testing.T) {
	p := loadTestFace(t, testFontLatin)
	df := p.fonts[0]
	face := df.face

	gid, ok := face.NominalGlyph(' ')
	if !ok || gid == 0 {
		t.Skip("font has no space glyph")
	}

	info, alpha := LookupByGID(df.cache, uint32(gid))
	if info == nil {
		t.Fatal("space glyph not in cache")
	}
	if info.Advance <= 0 {
		t.Errorf("space Advance = %d, want > 0", info.Advance)
	}
	if info.Width != 0 || info.Height != 0 {
		t.Errorf("space dimensions = %dx%d, want 0x0", info.Width, info.Height)
	}
	if alpha != nil {
		t.Error("space should have nil alpha")
	}
}

func TestTier2Fallback(t *testing.T) {
	p := loadTestFace(t, testFontLatin)

	// Use GlyphByGID with a bogus GID — should go through tier-2 fallback.
	// RenderGlyph returns a zero-advance GlyphInfo (not nil) for GIDs
	// with no outline. Verify it doesn't panic and gets cached in tier2.
	info, _, err := p.GlyphByGID(0, 0xDEAD)
	if err != nil {
		t.Fatalf("GlyphByGID error: %v", err)
	}
	// Should be in tier2 map now (even if zero-dimension).
	df := p.fonts[0]
	if _, ok := df.tier2[0xDEAD]; !ok {
		t.Error("expected tier2 entry after fallback render")
	}

	// Second call should return from tier2 map, not re-render.
	info2, _, err := p.GlyphByGID(0, 0xDEAD)
	if err != nil {
		t.Fatalf("second GlyphByGID error: %v", err)
	}
	if info2 == nil {
		t.Error("expected non-nil info from tier2")
	}
	_ = info
}

func TestMmapFontFile(t *testing.T) {
	p := loadTestFace(t, testFontLatin)
	df := p.fonts[0]

	if df.fontData == nil {
		t.Fatal("fontData is nil — mmap failed")
	}
	if len(df.fontData) == 0 {
		t.Fatal("fontData is empty")
	}

	// Verify the mmap'd data starts with a TrueType/OpenType signature.
	// TrueType: 00 01 00 00; OpenType: 4F 54 54 4F ("OTTO")
	if len(df.fontData) < 4 {
		t.Fatal("fontData too short")
	}
	sig := df.fontData[:4]
	isTTF := sig[0] == 0 && sig[1] == 1 && sig[2] == 0 && sig[3] == 0
	isOTF := sig[0] == 'O' && sig[1] == 'T' && sig[2] == 'T' && sig[3] == 'O'
	if !isTTF && !isOTF {
		t.Errorf("unexpected font signature: %x", sig)
	}

	// Verify same file loaded at different size shares the mmap.
	_, err := p.OpenFont(OpenFontRequest{Path: testFontLatin, Size: 24})
	if err != nil {
		t.Fatalf("OpenFont size 24: %v", err)
	}
	df2 := p.fonts[1]
	if df2.fontData == nil {
		t.Fatal("second font has nil fontData")
	}
	// Should point to same backing data (shared mmap).
	if &df.fontData[0] != &df2.fontData[0] {
		t.Error("expected shared fontData for same file at different sizes")
	}
}

func TestVariableSizeCache(t *testing.T) {
	// Build cache for a small font at small size — should be much less than 4MB.
	p := loadTestFace(t, testFontLatin)
	df := p.fonts[0]

	hdr := (*glyphCacheHeader)(unsafe.Pointer(&df.cache[0]))
	if hdr.TotalSize >= maxCacheSize {
		t.Errorf("cache size %d should be less than max %d for small Latin font at 18pt",
			hdr.TotalSize, maxCacheSize)
	}
	t.Logf("Latin 18pt cache: %d bytes (%.1f%% of %d max)",
		hdr.TotalSize, float64(hdr.TotalSize)*100/float64(maxCacheSize), maxCacheSize)
}

func TestCacheCJK(t *testing.T) {
	fontPath := filepath.Join(fontDir(t), testFontCJK)
	if _, err := os.Stat(fontPath); err != nil {
		t.Skipf("CJK font not available: %v", err)
	}

	p := NewDirectGlyphProvider(fontDir(t))
	_, err := p.OpenFont(OpenFontRequest{Path: testFontCJK, Size: 24})
	if err != nil {
		t.Fatalf("OpenFont CJK: %v", err)
	}
	df := p.fonts[0]
	hdr := (*glyphCacheHeader)(unsafe.Pointer(&df.cache[0]))
	t.Logf("CJK 24pt cache: %d glyphs, %d bytes (%.1f%% of %d max)",
		hdr.NumGlyphs, hdr.TotalSize,
		float64(hdr.TotalSize)*100/float64(maxCacheSize), maxCacheSize)

	// CJK should use more of the cache.
	if hdr.NumGlyphs == 0 {
		t.Error("no glyphs rendered for CJK font")
	}
}
