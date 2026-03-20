package wm

// Font message type discriminators — first int64 in every font message.
// These are disjoint from WM message types (1–6) so fontsvc can distinguish
// font protocol messages from window manager messages on the shared mailbox.
const (
	// MsgOpenFont: shepherd→fontsvc. "Open this font at this size."
	MsgOpenFont int64 = 100
	// MsgOpenFontReply: fontsvc→shepherd. "Here is your font cache."
	MsgOpenFontReply int64 = 101
	// MsgRequestGlyph: shepherd→fontsvc. "Render codepoint X in font Y."
	MsgRequestGlyph int64 = 102
	// MsgGlyphReply: fontsvc→shepherd. "Glyph rendered; copy it now."
	MsgGlyphReply int64 = 103
)

// OpenFontMsg is sent by a shepherd to fontsvc to open a font.
// Layout: 8 + 4 + 4 + 112 = 128 bytes.
type OpenFontMsg struct {
	Type    int64     // MsgOpenFont
	Variant int32     // 0=regular, 1=bold
	Size    float32   // point size (e.g., 18.0)
	Path    [112]byte // null-terminated font path (e.g., "/fonts/Atkinson...")
}

// OpenFontReplyMsg is sent by fontsvc to a shepherd after opening a font.
// Layout: 8+4+4+8+8+4+4+4+84 = 128 bytes.
type OpenFontReplyMsg struct {
	Type      int64  // MsgOpenFontReply
	FontID    int32  // allocated font ID (0–31), or -1 on error
	NumPages  int32  // number of 4KB pages in glyph cache
	CacheAddr uint64 // VA of glyph cache in shepherd's address space
	CacheSize uint64 // total bytes used in cache
	Height    int32  // font.Metrics.Height (fixed.Int26_6)
	Ascent    int32  // font.Metrics.Ascent (fixed.Int26_6)
	Descent   int32  // font.Metrics.Descent (fixed.Int26_6)
	_         [84]byte
}

// RequestGlyphMsg is sent by a shepherd to fontsvc for a tier-2 glyph.
// Layout: 8+4+4+112 = 128 bytes.
type RequestGlyphMsg struct {
	Type      int64 // MsgRequestGlyph
	FontID    int32 // font to render for
	Codepoint int32 // unicode codepoint
	_         [112]byte
}

// GlyphReplyMsg is sent by fontsvc to a shepherd after rendering a tier-2 glyph.
// The shepherd must copy the glyph data from ScratchAddr immediately — the
// scratch buffer is reused for the next request.
// Layout: 8+4+4+8+4+100 = 128 bytes.
type GlyphReplyMsg struct {
	Type       int64  // MsgGlyphReply
	FontID     int32  // font ID
	Codepoint  int32  // unicode codepoint
	ScratchAddr uint64 // VA of GlyphEntry+alpha in shepherd's scratch page
	GlyphSize  uint32 // total bytes (GlyphEntry header + alpha pixels)
	_          [100]byte
}
