// Package fontcache provides client-side font cache access over shared memory.
// fontsvc.maz is the server; shepherds use FontCache to open fonts and access
// pre-rendered glyph data via shared pages.
package fontcache

// MaxFonts is the maximum number of concurrently open fonts (IDs 0–31).
const MaxFonts = 32

// FontSvcInjector is the interface used for cross-module injection into
// fontsvc.maz. Interface type assertions work across .maz module boundaries
// (via itabsinit), whereas concrete struct pointer assertions do not.
//
// The injection passes callback registration functions. Rachel decodes uring
// messages in host context (correct type metadata) and calls the registered
// handlers with typed structs. The handlers run in rachel's runtime context
// (not .maz's broken runtime), receiving plain struct values (not interfaces
// requiring cross-module type assertions).
type FontSvcInjector interface {
	RegisterOpenFontHandler(handler func(senderSID int, variant, size int32, path [100]byte))
	RegisterRequestGlyphHandler(handler func(senderSID int, fontID, gid, codepoint int32))
}

// FontSvcInit implements FontSvcInjector. The host (rachel) creates this
// and passes it to fontsvc.maz's MazarinShepherd.
type FontSvcInit struct {
	HandleOpenFont     func(senderSID int, variant, size int32, path [100]byte)
	HandleRequestGlyph func(senderSID int, fontID, gid, codepoint int32)
}

// RegisterOpenFontHandler implements FontSvcInjector.
func (f *FontSvcInit) RegisterOpenFontHandler(handler func(senderSID int, variant, size int32, path [100]byte)) {
	f.HandleOpenFont = handler
}

// RegisterRequestGlyphHandler implements FontSvcInjector.
func (f *FontSvcInit) RegisterRequestGlyphHandler(handler func(senderSID int, fontID, gid, codepoint int32)) {
	f.HandleRequestGlyph = handler
}

// GlyphEntry is the per-glyph header stored in the cache data region,
// immediately followed by Width×Height bytes of alpha-channel pixel data.
// Total entry size = 16 + Width*Height (padded to 4-byte boundary).
type GlyphEntry struct {
	Advance int32  // pen advance width (fixed.Int26_6)
	DrMinX  int16  // draw rect min X offset from pen position (pixels)
	DrMinY  int16  // draw rect min Y offset from pen position (pixels)
	DrMaxX  int16  // draw rect max X offset from pen position
	DrMaxY  int16  // draw rect max Y offset from pen position
	Width   uint16 // alpha image width in pixels
	Height  uint16 // alpha image height in pixels
}

// GlyphEntrySize is the fixed header size before alpha pixel data.
const GlyphEntrySize = 16

// GlyphTotalSize returns the total bytes for a glyph entry (header + pixels),
// padded to 4-byte alignment.
func GlyphTotalSize(width, height uint16) uint32 {
	raw := uint32(GlyphEntrySize) + uint32(width)*uint32(height)
	return (raw + 3) &^ 3
}
