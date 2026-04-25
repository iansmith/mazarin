package textshape

import (
	"image"
	"image/color"
)

// DrawContext is the unified drawing surface for all rendering in both
// louis14 and the mazarin UI toolkit. The concrete implementation is
// [DrawContextImpl], constructed via [NewDrawContext] with a [GlyphProvider]
// that supplies glyph bitmaps either in-process ([DirectGlyphProvider]) or
// via fontsvc IPC (mazarin's FontClient).
//
// Application code and UI components always program to this interface.
// The GlyphProvider is the only point of variation between platforms.
type DrawContext interface {
	// --- Shape primitives (add to current path) ---
	DrawRectangle(x, y, w, h float64)
	DrawRoundedRectangle(x, y, w, h, r float64)
	DrawCircle(x, y, r float64)
	DrawLine(x1, y1, x2, y2 float64)

	// --- Path building ---
	MoveTo(x, y float64)
	LineTo(x, y float64)
	QuadraticTo(x1, y1, x2, y2 float64)
	CubicTo(x1, y1, x2, y2, x3, y3 float64)
	ClosePath()
	ClearPath()

	// --- Path rendering ---
	Fill()
	FillPreserve()
	Stroke()
	StrokePreserve()

	// Fast-path solid rectangle fill (bypasses path rasterizer).
	FillRectangle(x, y, w, h float64)

	// --- Text ---
	// OpenFont loads a font and returns its metrics including a FontID
	// for use in subsequent DrawText/MeasureText/FontMetrics calls.
	// The family parameter is a logical font family name (e.g.,
	// "AtkinsonHyperlegible"), not a filesystem path.
	OpenFont(family string, variant, size int32) (FontMetrics, error)

	// OpenTemporaryFont opens a font scoped to a single render pass.
	// The returned fontID MUST be paired with [CloseTemporaryFont] at
	// the end of the render. The provider may return a fontID from its
	// permanent pool when the requested face is already loaded — in
	// that case CloseTemporaryFont is a no-op. Callers (e.g. louis14's
	// HTML renderer) can therefore defer-close every fontID they open
	// without inspecting the ID. See [GlyphProvider.OpenTemporaryFont].
	OpenTemporaryFont(family string, variant, size int32) (FontMetrics, error)

	// CloseTemporaryFont releases a fontID returned by
	// OpenTemporaryFont. Tolerates fontIDs in the permanent range
	// (returns nil) so callers may close indiscriminately. See
	// [GlyphProvider.CloseTemporaryFont].
	CloseTemporaryFont(fontID int32) error

	// RegisterBuffer registers a parsed font buffer with the underlying
	// [GlyphProvider] under (family, variant). Used for CSS @font-face
	// fonts, which are fetched/decompressed by the caller and shaped
	// in-process. See [GlyphProvider.RegisterBuffer] for details.
	RegisterBuffer(family string, variant int32, data []byte) error

	// DrawText shapes, rasterizes, and composites text at (x, y) baseline.
	DrawText(text string, fontID int32, x, y float64)

	// DrawStringAnchored draws text anchored at (x, y) with anchor point
	// (ax, ay). ax=0 is left-aligned, 0.5 is centered, 1.0 is right-aligned.
	// ay=0 is top of ascent, 0.5 is vertically centered, 1.0 is bottom of descent.
	DrawStringAnchored(text string, fontID int32, x, y, ax, ay float64)

	// MeasureText returns the advance width of text in pixels.
	MeasureText(text string, fontID int32) float64

	// DrawTextWithFeatures is like DrawText but applies OpenType features.
	DrawTextWithFeatures(text string, fontID int32, x, y float64, features []FontFeature)

	// MeasureTextWithFeatures is like MeasureText but applies OpenType features.
	MeasureTextWithFeatures(text string, fontID int32, features []FontFeature) float64

	// FontMetrics returns the cached metrics for a previously opened font.
	GetFontMetrics(fontID int32) FontMetrics

	// TextLayout returns the underlying [TextLayout] for direct access
	// to shaping and measurement. This allows louis14 and other callers
	// to use the layout engine without going through DrawContext's
	// convenience methods.
	TextLayout() TextLayout

	// --- Graphics state ---
	SetColor(c color.Color)
	SetFillStyle(pattern Pattern)
	SetStrokeStyle(pattern Pattern)
	SetLineWidth(lineWidth float64)
	SetLineCap(lineCap LineCap)
	SetLineJoin(lineJoin LineJoin)
	SetFillRule(fillRule FillRule)
	SetDash(dashes ...float64)

	// Clip intersects the current clipping region with the current path,
	// then clears the path. Subsequent drawing is masked to the clipped area.
	Clip()
	// ResetClip removes the clipping region (restores full-canvas clipping).
	ResetClip()

	// --- Transform stack ---
	Push()
	Pop()
	Translate(x, y float64)
	Scale(x, y float64)
	Rotate(angle float64)
	RotateAbout(angle, x, y float64)
	// MultiplyMatrix pre-multiplies the current transform by the given
	// 2D affine matrix (CSS matrix(a,b,c,d,e,f) parameter order).
	MultiplyMatrix(xx, yx, xy, yy, x0, y0 float64)

	// --- Compositing groups ---
	PushGroup()
	PopGroup()
	PopGroupWithAlpha(opacity float64)

	// --- Image drawing ---
	DrawImage(im image.Image, x, y int)
	DrawImageAnchored(im image.Image, x, y int, ax, ay float64)

	// TransformPoint applies the current matrix to a point, returning
	// the transformed (image-space) coordinates. Used by code that needs
	// to composite directly onto dc.Image() at the correct position.
	TransformPoint(x, y float64) (float64, float64)

	// --- Canvas ---
	// Clear fills the entire canvas with the current color (set via SetColor).
	Clear()
	Image() image.Image
	Width() int
	Height() int

	// NewChildContext creates an off-screen DrawContext rendering onto
	// the given image, sharing the same text/font backend. Useful for
	// off-screen rendering that needs text support.
	NewChildContext(target *image.RGBA) DrawContext
}
