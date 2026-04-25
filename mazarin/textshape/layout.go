package textshape

import (
	goFont "github.com/go-text/typesetting/font"
)

// TextLayout abstracts text shaping and layout. The default
// implementation is [HarfBuzzTextLayout], which combines HarfBuzz
// shaping with a [GlyphProvider] for glyph bitmap retrieval.
//
// Both mazarin (via [DrawContext]) and louis14 (directly) use this
// interface for text measurement and rendering.
type TextLayout interface {
	// OpenFont loads a font and registers it for shaping. Returns
	// font-level metrics including a FontID for subsequent calls.
	OpenFont(req OpenFontRequest) (FontMetrics, error)

	// OpenTemporaryFont opens a font for a single render scope.
	// Forwards to [GlyphProvider.OpenTemporaryFont]; callers must
	// pair with [CloseTemporaryFont] using the returned FontID.
	// CloseTemporaryFont is a no-op for permanent-range fontIDs (the
	// dedupe path inside the provider may return one), so a renderer
	// can defer-close every fontID it opens without inspection.
	OpenTemporaryFont(req OpenFontRequest) (FontMetrics, error)

	// CloseTemporaryFont releases a fontID returned by
	// OpenTemporaryFont. See [GlyphProvider.CloseTemporaryFont].
	CloseTemporaryFont(fontID int32) error

	// LayoutText shapes text and rasterizes each glyph, returning
	// positioned glyphs ready for compositing.
	LayoutText(params ShapingParams) (*TextRun, error)

	// MeasureText shapes text and returns the total advance width
	// in fixed.Int26_6 units. No rasterization is performed.
	MeasureText(params ShapingParams) (int32, error)

	// ShapeText shapes text and returns per-glyph IDs and advances
	// without rasterization. Used by the paragraph layout adapter to
	// build boxesandglue node lists from shaped glyph data.
	ShapeText(params ShapingParams) (ShapedRun, error)

	// CachedFontMetrics returns the FontMetrics for a previously
	// opened font. Returns zero-value if fontID has not been opened.
	CachedFontMetrics(fontID int32) FontMetrics

	// RegisterBuffer registers a parsed font buffer with the underlying
	// [GlyphProvider] under (family, variant). Used for CSS @font-face
	// fonts, which are fetched/decompressed by the caller and shaped
	// in-process. See [GlyphProvider.RegisterBuffer] for details.
	RegisterBuffer(family string, variant int32, data []byte) error
}

// openedFont tracks a font that has been opened via HarfBuzzTextLayout.
type openedFont struct {
	fontID  int32
	metrics FontMetrics
	face    *goFont.Face
}

// HarfBuzzTextLayout implements [TextLayout] using HarfBuzz shaping
// via go-text/typesetting and a [GlyphProvider] for glyph bitmaps.
//
// It supports both in-process rasterization ([DirectGlyphProvider]
// for darwin) and fontsvc IPC (mazarin's FontClient).
type HarfBuzzTextLayout struct {
	shaper   *HarfBuzzShaper
	provider GlyphProvider
	fonts    [maxFonts]*openedFont
	cache    *shapeCache
}

// NewTextLayout creates a [HarfBuzzTextLayout] that loads fonts from
// fontDir using a [DirectGlyphProvider] for in-process rasterization.
func NewTextLayout(fontDir string) *HarfBuzzTextLayout {
	return &HarfBuzzTextLayout{
		shaper:   NewHarfBuzzShaper(),
		provider: NewDirectGlyphProvider(fontDir),
		cache:    newShapeCache(ShapeCacheSize),
	}
}

// NewTextLayoutWithProvider creates a [HarfBuzzTextLayout] using the
// given [GlyphProvider]. Use this when you need a custom provider
// (e.g., mazarin's FontClient backed by fontsvc IPC).
func NewTextLayoutWithProvider(provider GlyphProvider) *HarfBuzzTextLayout {
	return &HarfBuzzTextLayout{
		shaper:   NewHarfBuzzShaper(),
		provider: provider,
		cache:    newShapeCache(ShapeCacheSize),
	}
}

// OpenFont opens a font and registers it with both the shaper and
// the glyph provider. Returns font-level metrics.
func (tl *HarfBuzzTextLayout) OpenFont(req OpenFontRequest) (FontMetrics, error) {
	metrics, err := tl.provider.OpenFont(req)
	if err != nil {
		return FontMetrics{}, err
	}
	tl.registerOpenedFont(metrics, req.Size)
	return metrics, nil
}

// OpenTemporaryFont forwards to the provider's temp-pool open. Fontsvc-side
// fontIDs returned through the temp pool may have wm.TempFontIDBase (0x1000)
// OR'd in; with the current FontSvcGlyphProvider fallback path that never
// happens (returns permanent-range fontIDs only). The shaper registration
// path here is conservative: it only registers in-range IDs, so when the
// real temp-pool IPC lands we'll either widen maxFonts or move to a sparse
// map keyed by fontID. Both options preserve this method's semantics.
func (tl *HarfBuzzTextLayout) OpenTemporaryFont(req OpenFontRequest) (FontMetrics, error) {
	metrics, err := tl.provider.OpenTemporaryFont(req, nil)
	if err != nil {
		return FontMetrics{}, err
	}
	tl.registerOpenedFont(metrics, req.Size)
	return metrics, nil
}

// CloseTemporaryFont forwards to the provider. No-op for permanent-range
// fontIDs (which is what every fontID returned by the current provider
// stubs is, until the temp-pool IPC lands).
func (tl *HarfBuzzTextLayout) CloseTemporaryFont(fontID int32) error {
	return tl.provider.CloseTemporaryFont(fontID)
}

// registerOpenedFont stages a freshly-returned font with the HarfBuzz
// shaper if it hasn't already been seen at this fontID. Bounded by
// maxFonts; out-of-range IDs (temp-pool with 0x1000 bit, once the real
// IPC lands) skip registration here and would route through whatever
// expanded scheme replaces the fixed-size tl.fonts table.
func (tl *HarfBuzzTextLayout) registerOpenedFont(metrics FontMetrics, size int32) {
	if metrics.FontID < 0 || metrics.FontID >= maxFonts {
		return
	}
	if tl.fonts[metrics.FontID] != nil {
		return
	}
	face := tl.provider.Face(metrics.FontID)
	tl.shaper.RegisterFont(metrics.FontID, face, size)
	tl.fonts[metrics.FontID] = &openedFont{
		fontID:  metrics.FontID,
		metrics: metrics,
		face:    face,
	}
}

// serializeFeatures returns a stable string key for a FontFeature slice.
func serializeFeatures(features []FontFeature) string {
	if len(features) == 0 {
		return ""
	}
	buf := make([]byte, 0, len(features)*8)
	for _, f := range features {
		buf = append(buf, f.Tag[:]...)
		buf = append(buf, byte(f.Value>>24), byte(f.Value>>16), byte(f.Value>>8), byte(f.Value))
	}
	return string(buf)
}

// shapeWithCache shapes params, using the LRU cache to avoid reshaping
// identical runs.
func (tl *HarfBuzzTextLayout) shapeWithCache(params ShapingParams) (ShapedRun, error) {
	key := shapeCacheKey{
		text:      params.Text,
		fontID:    params.FontID,
		direction: params.Direction,
		script:    params.Script,
		language:  params.Language,
		features:  serializeFeatures(params.Features),
	}
	if run, ok := tl.cache.get(key); ok {
		return run, nil
	}
	run, err := tl.shaper.Shape(params)
	if err != nil {
		return ShapedRun{}, err
	}
	tl.cache.put(key, run)
	return run, nil
}

// LayoutText shapes the text and rasterizes each glyph, returning
// a TextRun with positioned glyphs ready for compositing.
func (tl *HarfBuzzTextLayout) LayoutText(params ShapingParams) (*TextRun, error) {
	run, err := tl.shapeWithCache(params)
	if err != nil {
		return nil, err
	}

	of := tl.fonts[params.FontID]
	if of == nil {
		return nil, err
	}

	glyphs := make([]PositionedGlyph, 0, len(run.Glyphs))
	penX, penY := params.StartPenX, int32(0) // fixed.Int26_6 pen position

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

// CachedFontMetrics returns the FontMetrics for a previously opened font.
// Returns zero-value FontMetrics if fontID has not been opened.
func (tl *HarfBuzzTextLayout) CachedFontMetrics(fontID int32) FontMetrics {
	if fontID < 0 || fontID >= maxFonts || tl.fonts[fontID] == nil {
		return FontMetrics{}
	}
	return tl.fonts[fontID].metrics
}

// ShapeText shapes text and returns per-glyph IDs and advances without
// rasterization. This exposes the cached shaping results that MeasureText
// and LayoutText use internally.
func (tl *HarfBuzzTextLayout) ShapeText(params ShapingParams) (ShapedRun, error) {
	return tl.shapeWithCache(params)
}

// RegisterBuffer forwards a font buffer registration to the underlying
// GlyphProvider. After this call, OpenFont(family, variant, size) uses
// the registered buffer instead of resolving via filesystem or fontsvc IPC.
func (tl *HarfBuzzTextLayout) RegisterBuffer(family string, variant int32, data []byte) error {
	return tl.provider.RegisterBuffer(family, variant, data)
}

// MeasureText shapes the text and returns the total advance width
// in fixed.Int26_6 units. No rasterization is performed.
func (tl *HarfBuzzTextLayout) MeasureText(params ShapingParams) (int32, error) {
	run, err := tl.shapeWithCache(params)
	if err != nil {
		return 0, err
	}

	var total int32
	for _, sg := range run.Glyphs {
		total += sg.XAdvance
	}
	return total, nil
}
