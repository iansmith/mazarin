// Package fontcache provides client-side font cache access over shared memory.
// fontsvc.maz is the server; shepherds use FontCache to open fonts and access
// pre-rendered glyph data via shared pages.
package fontcache

import (
	"mazzy/mazarin/fsclient"
	"mazzy/shared/wm"
)

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
//
// RegisterInternalOpenFont / RegisterInternalGlyphByGID allow fontsvc to
// pass back direct function callbacks that rachel can call in-process,
// bypassing uring IPC and SharePages. This is necessary because fontsvc
// runs inside rachel (same SID) and SharePages to yourself is rejected
// by the kernel.
//
// The callbacks use plain scalar/slice types (no interfaces) to avoid
// cross-.maz type assertion failures (linker bug / unreachable method).
type FontSvcInjector interface {
	RegisterOpenFontHandler(handler func(senderSID int, variant, size int32, path [100]byte))
	RegisterRequestGlyphHandler(handler func(senderSID int, fontID, gid, codepoint int32))
	RegisterOpenTemporaryFontHandler(handler func(senderSID int, msg wm.OpenTemporaryFont))
	RegisterCloseTemporaryFontHandler(handler func(senderSID int, fontID int32))
	RegisterRegisterFontBufferHandler(handler func(senderSID int, msg wm.RegisterFontBuffer))
	RegisterUnregisterFontBufferHandler(handler func(senderSID int, msg wm.UnregisterFontBuffer))
	// RegisterCleanupHandler is called by fontsvc to register a callback
	// rachel invokes when a shepherd dies. The callback releases every
	// temp slot owned by the dead SID and unregisters any font buffers
	// the dead shepherd had registered. Called from rachel's
	// handleShepherdDeath, in rachel's event-loop goroutine.
	RegisterCleanupHandler(handler func(deadSID int))
	RegisterInternalOpenFont(handler func(family string, variant, size int32) (InternalOpenFontResult, bool))
	RegisterInternalGlyphByGID(handler func(fontID int32, gid uint32) (InternalGlyphResult, bool))

	// GetFSClient returns the host's fs IPC client.
	GetFSClient() fsclient.FSClient
}

// InternalOpenFontResult is the result of an in-process OpenFont call.
type InternalOpenFontResult struct {
	FontID   int32
	Height   int32  // fixed.Int26_6
	Ascent   int32  // fixed.Int26_6
	Descent  int32  // fixed.Int26_6
	Cache    []byte // V2 glyph cache (direct slice, same address space)
	FontData []byte // raw font file bytes (direct slice)
}

// InternalGlyphResult is the result of an in-process GlyphByGID call.
type InternalGlyphResult struct {
	Info  GlyphEntry
	Alpha []byte // alpha bitmap (copy owned by caller)
}

// FontSvcInit implements FontSvcInjector. The host (rachel) creates this
// and passes it to fontsvc.maz's MazarinShepherd.
type FontSvcInit struct {
	HandleOpenFont             func(senderSID int, variant, size int32, path [100]byte)
	HandleRequestGlyph         func(senderSID int, fontID, gid, codepoint int32)
	HandleOpenTemporaryFont    func(senderSID int, msg wm.OpenTemporaryFont)
	HandleCloseTemporaryFont   func(senderSID int, fontID int32)
	HandleRegisterFontBuffer   func(senderSID int, msg wm.RegisterFontBuffer)
	HandleUnregisterFontBuffer func(senderSID int, msg wm.UnregisterFontBuffer)
	CleanupShepherdFonts       func(deadSID int)
	InternalOpenFont           func(family string, variant, size int32) (InternalOpenFontResult, bool)
	InternalGlyphByGID         func(fontID int32, gid uint32) (InternalGlyphResult, bool)

	// FSClient is the fs IPC client (already connected). Fontsvc uses it
	// to load font files from the filesystem instead of the removed
	// sys.LoadFile delegate path.
	FSClient fsclient.FSClient
}

// RegisterOpenFontHandler implements FontSvcInjector.
func (f *FontSvcInit) RegisterOpenFontHandler(handler func(senderSID int, variant, size int32, path [100]byte)) {
	f.HandleOpenFont = handler
}

// RegisterRequestGlyphHandler implements FontSvcInjector.
func (f *FontSvcInit) RegisterRequestGlyphHandler(handler func(senderSID int, fontID, gid, codepoint int32)) {
	f.HandleRequestGlyph = handler
}

// RegisterOpenTemporaryFontHandler implements FontSvcInjector.
func (f *FontSvcInit) RegisterOpenTemporaryFontHandler(handler func(senderSID int, msg wm.OpenTemporaryFont)) {
	f.HandleOpenTemporaryFont = handler
}

// RegisterCloseTemporaryFontHandler implements FontSvcInjector.
func (f *FontSvcInit) RegisterCloseTemporaryFontHandler(handler func(senderSID int, fontID int32)) {
	f.HandleCloseTemporaryFont = handler
}

// RegisterRegisterFontBufferHandler implements FontSvcInjector.
func (f *FontSvcInit) RegisterRegisterFontBufferHandler(handler func(senderSID int, msg wm.RegisterFontBuffer)) {
	f.HandleRegisterFontBuffer = handler
}

// RegisterUnregisterFontBufferHandler implements FontSvcInjector.
func (f *FontSvcInit) RegisterUnregisterFontBufferHandler(handler func(senderSID int, msg wm.UnregisterFontBuffer)) {
	f.HandleUnregisterFontBuffer = handler
}

// RegisterCleanupHandler implements FontSvcInjector.
func (f *FontSvcInit) RegisterCleanupHandler(handler func(deadSID int)) {
	f.CleanupShepherdFonts = handler
}

// RegisterInternalOpenFont implements FontSvcInjector.
func (f *FontSvcInit) RegisterInternalOpenFont(handler func(family string, variant, size int32) (InternalOpenFontResult, bool)) {
	f.InternalOpenFont = handler
}

// RegisterInternalGlyphByGID implements FontSvcInjector.
func (f *FontSvcInit) RegisterInternalGlyphByGID(handler func(fontID int32, gid uint32) (InternalGlyphResult, bool)) {
	f.InternalGlyphByGID = handler
}

// GetFSClient implements FontSvcInjector.
func (f *FontSvcInit) GetFSClient() fsclient.FSClient {
	return f.FSClient
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
