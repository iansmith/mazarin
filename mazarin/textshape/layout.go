package textshape

import (
	goFont "github.com/go-text/typesetting/font"
)

// openedFont tracks a font that has been opened via TextLayout.
type openedFont struct {
	fontID  int32
	metrics FontMetrics
	face    *goFont.Face
}

// TextLayout is the main orchestrator: it combines shaping (HarfBuzz)
// and rasterization ([GlyphProvider]) to produce positioned glyph bitmaps.
//
// TextLayout depends on [GlyphProvider] for glyph bitmap retrieval,
// allowing both in-process rasterization ([DirectGlyphProvider] for
// darwin) and fontsvc IPC (mazarin's FontClient).
type TextLayout struct {
	shaper   *HarfBuzzShaper
	provider GlyphProvider
	fonts    [maxFonts]*openedFont
}

// NewTextLayout creates a TextLayout that loads fonts from fontDir
// using a [DirectGlyphProvider] for in-process rasterization.
func NewTextLayout(fontDir string) *TextLayout {
	return &TextLayout{
		shaper:   NewHarfBuzzShaper(),
		provider: NewDirectGlyphProvider(fontDir),
	}
}

// NewTextLayoutWithProvider creates a TextLayout using the given
// [GlyphProvider]. Use this when you need a custom provider (e.g.,
// mazarin's FontClient backed by fontsvc IPC).
func NewTextLayoutWithProvider(provider GlyphProvider) *TextLayout {
	return &TextLayout{
		shaper:   NewHarfBuzzShaper(),
		provider: provider,
	}
}

// OpenFont opens a font and registers it with both the shaper and
// the glyph provider. Returns font-level metrics.
func (tl *TextLayout) OpenFont(req OpenFontRequest) (FontMetrics, error) {
	metrics, err := tl.provider.OpenFont(req)
	if err != nil {
		return FontMetrics{}, err
	}

	// Register with shaper if not already done.
	if tl.fonts[metrics.FontID] == nil {
		face := tl.provider.Face(metrics.FontID)
		tl.shaper.RegisterFont(metrics.FontID, face, req.Size)
		tl.fonts[metrics.FontID] = &openedFont{
			fontID:  metrics.FontID,
			metrics: metrics,
			face:    face,
		}
	}

	return metrics, nil
}

// LayoutText shapes the text and rasterizes each glyph, returning
// a TextRun with positioned glyphs ready for compositing.
func (tl *TextLayout) LayoutText(params ShapingParams) (*TextRun, error) {
	run, err := tl.shaper.Shape(params)
	if err != nil {
		return nil, err
	}

	of := tl.fonts[params.FontID]
	if of == nil {
		return nil, err
	}

	glyphs := make([]PositionedGlyph, 0, len(run.Glyphs))
	var penX, penY int32 // fixed.Int26_6 pen position

	for _, sg := range run.Glyphs {
		gi, alpha, err := tl.provider.GlyphByGID(params.FontID, sg.GID)
		if err != nil {
			return nil, err
		}

		if gi != nil && gi.Width > 0 && gi.Height > 0 {
			// Pixel position: pen (in pixels) + glyph draw offset + shaping offset.
			// pen is in fixed.Int26_6, convert to pixels by >> 6.
			px := (penX + sg.XOffset) >> 6
			py := (penY + sg.YOffset) >> 6

			glyphs = append(glyphs, PositionedGlyph{
				X:       px + int32(gi.DrMinX),
				Y:       py + int32(gi.DrMinY),
				Width:   gi.Width,
				Height:  gi.Height,
				Alpha:   alpha,
				Cluster: sg.Cluster,
				GID:     sg.GID,
			})
		}

		penX += sg.XAdvance
		penY += sg.YAdvance
	}

	return &TextRun{
		Glyphs:       glyphs,
		TotalAdvance: penX,
		Ascent:       of.metrics.Ascent,
		Descent:      of.metrics.Descent,
		LineHeight:   of.metrics.Height,
		Direction:    params.Direction,
	}, nil
}

// MeasureText shapes the text and returns the total advance width
// in fixed.Int26_6 units. No rasterization is performed.
func (tl *TextLayout) MeasureText(params ShapingParams) (int32, error) {
	run, err := tl.shaper.Shape(params)
	if err != nil {
		return 0, err
	}

	var total int32
	for _, sg := range run.Glyphs {
		total += sg.XAdvance
	}
	return total, nil
}
