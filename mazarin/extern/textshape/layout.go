package textshape

import (
	goFont "github.com/go-text/typesetting/font"
	"mazzy/mazarin/extern/font"
)

// openedFont tracks a font that has been opened via TextLayout.
type openedFont struct {
	fontID int32
	info   font.FontInfo
	face   *goFont.Face
}

// TextLayout is the main orchestrator: it combines shaping (HarfBuzz)
// and rasterization (FontClient) to produce positioned glyph bitmaps.
type TextLayout struct {
	shaper  *HarfBuzzShaper
	client  *font.FontClient
	fontDir string
	fonts   [maxFonts]*openedFont
}

// NewTextLayout creates a TextLayout that loads fonts from fontDir.
func NewTextLayout(fontDir string) *TextLayout {
	return &TextLayout{
		shaper:  NewHarfBuzzShaper(),
		client:  font.NewFontClient(fontDir),
		fontDir: fontDir,
	}
}

// Client returns the underlying FontClient.
func (tl *TextLayout) Client() *font.FontClient {
	return tl.client
}

// OpenFont opens a font and registers it with both the shaper and
// the rasterizer. Returns font-level metrics.
func (tl *TextLayout) OpenFont(req font.OpenFontRequest) (font.FontInfo, error) {
	info, err := tl.client.OpenFont(req)
	if err != nil {
		return font.FontInfo{}, err
	}

	// Register with shaper if not already done.
	if tl.fonts[info.FontID] == nil {
		face := tl.client.Face(info.FontID)
		tl.shaper.RegisterFont(info.FontID, face, req.Size)
		tl.fonts[info.FontID] = &openedFont{
			fontID: info.FontID,
			info:   info,
			face:   face,
		}
	}

	return info, nil
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
		gi, alpha, err := tl.client.GlyphByGID(params.FontID, sg.GID)
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
		Ascent:       of.info.Ascent,
		Descent:      of.info.Descent,
		LineHeight:   of.info.Height,
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
