package mancini

// Face draws content into a rectangular area. It is the base interface
// for all content rendered on the face of neumorphic shapes, menu
// segments, buttons, and other interactors.
//
// The caller sets color and clipping on the [DrawContext] before calling
// DrawFace. Implementations render content but do not clear the
// background or modify persistent DC state.
type Face interface {
	DrawFace(dc DrawContext, x, y, w, h float64)
}

// HAlign controls horizontal text placement within the draw rectangle.
type HAlign int

const (
	HAlignCenter HAlign = iota // Centered horizontally (default).
	HAlignLeft                 // Flush to the left edge.
	HAlignRight                // Flush to the right edge.
)

// VAlign controls vertical text placement within the draw rectangle.
type VAlign int

const (
	VAlignCenter   VAlign = iota // Centered vertically (default).
	VAlignTop                    // Top of text at top of rectangle.
	VAlignBottom                 // Bottom of text at bottom of rectangle.
	VAlignBaseline               // y parameter is the baseline Y; h is ignored for vertical placement.
)

// TextAlignmentParams controls text alignment for [LatinTextFace] rendering.
// The zero value is center/center, the most common case.
type TextAlignmentParams struct {
	HAlign HAlign
	VAlign VAlign
}

// LatinTextFace extends [Face] for Latin left-to-right text rendering.
// [SetText] updates the text to render; [Text] returns the current text;
// [MeasureText] returns the advance width without rasterization.
//
// The standard implementation is [impl.LatinTextFaceImpl].
type LatinTextFace interface {
	Face
	SetText(text string)
	Text() string
	MeasureText(text string) float64
}
