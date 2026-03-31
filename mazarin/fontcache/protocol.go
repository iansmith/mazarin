// Package fontcache provides client-side font cache access over shared memory.
// fontsvc.maz is the server; shepherds use FontCache to open fonts and access
// pre-rendered glyph data via shared pages.
package fontcache

import "mazzy/mazarin/sys"

// MaxFonts is the maximum number of concurrently open fonts (IDs 0–31).
const MaxFonts = 32

// FontSvcInjector is the interface used for cross-module injection into
// fontsvc.maz. Interface type assertions work across .maz module boundaries
// (via itabsinit), whereas concrete struct pointer assertions do not.
type FontSvcInjector interface {
	GetRachelChannel() chan<- sys.MailboxNotification
}

// FontSvcInit implements FontSvcInjector. The host (rachel) creates this
// and passes it to fontsvc.maz's MazarinShepherd.
type FontSvcInit struct {
	RachelCh chan<- sys.MailboxNotification
}

// GetRachelChannel implements FontSvcInjector.
func (f *FontSvcInit) GetRachelChannel() chan<- sys.MailboxNotification {
	return f.RachelCh
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
