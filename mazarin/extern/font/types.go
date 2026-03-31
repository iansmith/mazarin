// Package font provides a standalone font service simulator that produces
// glyph caches with the same binary layout as mazzy's fontsvc.maz.
// It has zero mazarin dependencies and runs on GOOS=darwin.
package font

// OpenFontRequest describes a font to open at a specific size.
type OpenFontRequest struct {
	Path    string // font filename (e.g., "AtkinsonHyperlegible-Regular.ttf")
	Variant int32  // 0=regular, 1=bold
	Size    int32  // point size (e.g., 18)
}

// FontInfo holds font-level metrics returned by OpenFont.
// All metric values use fixed.Int26_6 encoding (int32, 6 fractional bits;
// divide by 64.0 to get the float value).
type FontInfo struct {
	FontID    int32
	Height    int32  // line height
	Ascent    int32  // baseline to top
	Descent   int32  // baseline to bottom (negative)
	XHeight   int32  // height of lowercase 'x'
	CapHeight int32  // height of uppercase letters
	NumGlyphs uint32 // glyphs in tier-1 cache
	CacheSize uint32 // total bytes used in cache
}

// GlyphInfo holds per-glyph metrics.
type GlyphInfo struct {
	Advance int32  // pen advance width (fixed.Int26_6)
	DrMinX  int16  // draw rect min X offset from pen (pixels)
	DrMinY  int16  // draw rect min Y offset from pen (pixels)
	DrMaxX  int16  // draw rect max X offset from pen (pixels)
	DrMaxY  int16  // draw rect max Y offset from pen (pixels)
	Width   uint16 // alpha image width in pixels
	Height  uint16 // alpha image height in pixels
}

// --- Internal cache binary layout types (match fontsvc exactly) ---

// cacheHeader is the 64-byte* header at the start of each glyph cache.
// *unsafe.Sizeof is 60; fontsvc uses headerSize=64 for offset calculations,
// leaving a 4-byte zero gap before the glyph map.
type cacheHeader struct {
	Magic, Version uint32
	PointSize      int32
	FontID         uint32
	Height         int32
	Ascent         int32
	Descent        int32
	XHeight        int32
	CapHeight      int32
	NumGlyphs      uint32
	MapOffset      uint32
	DataOffset     uint32
	TotalUsed      uint32
	_              [8]byte
}

// glyphMapEntry maps a codepoint to its data offset. Sorted by Codepoint.
type glyphMapEntry struct {
	Codepoint uint32
	Offset    uint32
}

// glyphEntry is the 16-byte per-glyph header, followed by Width*Height
// bytes of alpha-channel pixel data (padded to 4-byte boundary).
type glyphEntry struct {
	Advance int32
	DrMinX  int16
	DrMinY  int16
	DrMaxX  int16
	DrMaxY  int16
	Width   uint16
	Height  uint16
}

const (
	maxFonts       = 32
	cacheSizeBytes = 2 * 1024 * 1024 // 2MB per font
	cacheMagic     = 0x464F4E54      // "FONT"
	cacheVersion   = 1
	glyphEntrySize = 16
	headerSize     = 64 // fontsvc hardcodes 64, not sizeof(cacheHeader)
)

// glyphTotalSize returns total bytes for a glyph entry (header + pixels),
// padded to 4-byte alignment.
func glyphTotalSize(width, height uint16) uint32 {
	raw := uint32(glyphEntrySize) + uint32(width)*uint32(height)
	return (raw + 3) &^ 3
}
