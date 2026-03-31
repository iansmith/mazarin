// Package textshape provides text shaping and layout using HarfBuzz
// via go-text/typesetting. It takes a string, font, direction, and
// script, and produces positioned glyph bitmaps ready for compositing.
//
// textshape is the shared package between darwin (in-process) and
// mazarin (fontsvc IPC) text rendering. It defines the [GlyphProvider]
// interface that abstracts glyph bitmap retrieval, plus types
// ([GlyphInfo], [FontMetrics], [OpenFontRequest]) used by both paths.
package textshape

import goFont "github.com/go-text/typesetting/font"

// OpenFontRequest describes a font to open at a specific size.
type OpenFontRequest struct {
	Path    string // font filename (e.g., "AtkinsonHyperlegible-Regular.ttf")
	Variant int32  // 0=regular, 1=bold
	Size    int32  // point size (e.g., 18)
}

// FontMetrics holds font-level metrics returned by [GlyphProvider.OpenFont].
// All metric values use fixed.Int26_6 encoding (int32, 6 fractional bits;
// divide by 64.0 to get the float value).
type FontMetrics struct {
	FontID    int32
	Height    int32 // line height
	Ascent    int32 // baseline to top
	Descent   int32 // baseline to bottom (positive)
	XHeight   int32 // height of lowercase 'x'
	CapHeight int32 // height of uppercase letters
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

// GlyphProvider abstracts glyph bitmap retrieval and font management.
// [TextLayout] depends on this interface rather than on a concrete font
// client, allowing both in-process rasterization (darwin via
// [DirectGlyphProvider]) and fontsvc IPC (mazarin).
type GlyphProvider interface {
	// OpenFont loads a font and returns its metrics. The returned
	// FontMetrics includes a FontID for use in subsequent calls.
	OpenFont(req OpenFontRequest) (FontMetrics, error)

	// Face returns the go-text Face for the given fontID, needed by
	// HarfBuzz for shaping. Returns nil if not loaded.
	Face(fontID int32) *goFont.Face

	// GlyphByGID returns the metrics and alpha bitmap for a glyph
	// identified by its GID (after HarfBuzz shaping). Returns nil
	// info if the glyph has no renderable outline.
	GlyphByGID(fontID int32, gid uint32) (*GlyphInfo, []byte, error)
}

// Direction specifies the text flow direction.
type Direction int

const (
	LTR Direction = iota // Left-to-right (Latin, Cyrillic, etc.)
	RTL                  // Right-to-left (Arabic, Hebrew)
	TTB                  // Top-to-bottom (CJK vertical)
	BTT                  // Bottom-to-top
)

// Script identifies a writing system (ISO 15924).
// Values match the go-text/typesetting/language constants.
type Script uint32

const (
	ScriptLatin      Script = 0x4c61746e // Latn
	ScriptArabic     Script = 0x41726162 // Arab
	ScriptHan        Script = 0x48616e69 // Hani
	ScriptHangul     Script = 0x48616e67 // Hang
	ScriptCyrillic   Script = 0x4379726c // Cyrl
	ScriptGreek      Script = 0x4772656b // Grek
	ScriptDevanagari Script = 0x44657661 // Deva
	ScriptCommon     Script = 0x5a797979 // Zyyy
)

// ShapingParams describes the input to text shaping.
type ShapingParams struct {
	Text      string
	FontID    int32
	Direction Direction
	Script    Script
	Language  string // BCP 47 ("en", "ar", "zh-Hans"); empty = auto
	// StartPenX is the initial pen position in fixed.Int26_6 units (1/64 pixel).
	// Use the fractional carry from the end of the previous run (prevPenX % 64)
	// so that glyphs are positioned as if this run is a continuation of the prior
	// run. This matches Blink's sub-pixel pen accumulation across inline boxes.
	StartPenX int32
}

// ShapedGlyph is a single glyph produced by shaping.
// All metric values are fixed.Int26_6 (divide by 64 for pixels).
type ShapedGlyph struct {
	GID      uint32
	Cluster  uint32 // byte offset into source text
	XAdvance int32
	YAdvance int32
	XOffset  int32
	YOffset  int32
}

// ShapedRun is the output of the shaper for a single text+font+direction.
type ShapedRun struct {
	FontID    int32
	Direction Direction
	Glyphs    []ShapedGlyph
}

// PositionedGlyph is a glyph with its final pixel position and alpha bitmap.
type PositionedGlyph struct {
	X, Y    int32  // pixel position relative to text origin
	Width   uint16
	Height  uint16
	Alpha   []byte // 8-bit alpha, row-major
	Cluster uint32
	GID     uint32
}

// TextRun is the final output of LayoutText: positioned glyphs ready
// for compositing, plus run-level metrics.
// Metric values are fixed.Int26_6 unless noted.
type TextRun struct {
	Glyphs       []PositionedGlyph
	TotalAdvance int32 // fixed.Int26_6
	Ascent       int32 // fixed.Int26_6, positive
	Descent      int32 // fixed.Int26_6, positive (distance below baseline)
	LineHeight   int32 // fixed.Int26_6
	Direction    Direction
}
