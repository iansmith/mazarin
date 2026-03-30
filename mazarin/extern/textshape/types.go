// Package textshape provides text shaping and layout using HarfBuzz
// via go-text/typesetting. It takes a string, font, direction, and
// script, and produces positioned glyph bitmaps ready for compositing.
package textshape

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
