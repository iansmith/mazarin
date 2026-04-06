package fontcache

import (
	"bytes"
	"fmt"
	"mazarin/textshape"

	goFont "github.com/go-text/typesetting/font"
)

// InternalGlyphProvider implements textshape.GlyphProvider for the
// in-process case where fontsvc runs inside the same shepherd (rachel).
// It calls registered callbacks directly — no uring IPC, no SharePages.
// Cache and font data slices are returned directly since they are
// already in the same address space.
type InternalGlyphProvider struct {
	openFontCb   func(family string, variant, size int32) (InternalOpenFontResult, bool)
	glyphByGIDCb func(fontID int32, gid uint32) (InternalGlyphResult, bool)
	fonts        [MaxFonts]*internalFont
}

type internalFont struct {
	face    *goFont.Face
	cache   []byte
	metrics textshape.FontMetrics
}

// NewInternalGlyphProvider creates a GlyphProvider backed by in-process
// fontsvc callbacks.
func NewInternalGlyphProvider(
	openFont func(family string, variant, size int32) (InternalOpenFontResult, bool),
	glyphByGID func(fontID int32, gid uint32) (InternalGlyphResult, bool),
) *InternalGlyphProvider {
	return &InternalGlyphProvider{
		openFontCb:   openFont,
		glyphByGIDCb: glyphByGID,
	}
}

// OpenFont implements textshape.GlyphProvider.
func (p *InternalGlyphProvider) OpenFont(req textshape.OpenFontRequest) (textshape.FontMetrics, error) {
	result, ok := p.openFontCb(req.Path, req.Variant, req.Size)
	if !ok {
		return textshape.FontMetrics{}, fmt.Errorf("internal OpenFont failed for %s", req.Path)
	}

	fontID := result.FontID
	if fontID < 0 || fontID >= MaxFonts {
		return textshape.FontMetrics{}, fmt.Errorf("internal OpenFont: invalid fontID %d", fontID)
	}

	// Parse Face from the font data (already in our address space).
	var face *goFont.Face
	if len(result.FontData) > 0 {
		var parseErr error
		face, parseErr = goFont.ParseTTF(bytes.NewReader(result.FontData))
		if parseErr != nil {
			return textshape.FontMetrics{}, fmt.Errorf("internal ParseTTF: %w", parseErr)
		}
	}

	metrics := textshape.FontMetrics{
		FontID:  fontID,
		Height:  result.Height,
		Ascent:  result.Ascent,
		Descent: result.Descent,
	}

	p.fonts[fontID] = &internalFont{
		face:    face,
		cache:   result.Cache,
		metrics: metrics,
	}

	return metrics, nil
}

// Face implements textshape.GlyphProvider.
func (p *InternalGlyphProvider) Face(fontID int32) *goFont.Face {
	if fontID < 0 || fontID >= MaxFonts || p.fonts[fontID] == nil {
		return nil
	}
	return p.fonts[fontID].face
}

// GlyphByGID implements textshape.GlyphProvider.
func (p *InternalGlyphProvider) GlyphByGID(fontID int32, gid uint32) (*textshape.GlyphInfo, []byte, error) {
	if fontID < 0 || fontID >= MaxFonts || p.fonts[fontID] == nil {
		return nil, nil, fmt.Errorf("invalid fontID %d", fontID)
	}

	ff := p.fonts[fontID]

	// Tier 1: binary search in V2 cache (direct slice, no shared pages).
	if len(ff.cache) > 0 {
		info, alpha := textshape.LookupByGID(ff.cache, gid)
		if info != nil {
			return info, alpha, nil
		}
	}

	// Tier 2: request on-demand rasterization from fontsvc.
	result, ok := p.glyphByGIDCb(fontID, gid)
	if !ok {
		return nil, nil, nil
	}
	if result.Info.Width == 0 && result.Info.Height == 0 {
		return nil, nil, nil
	}

	info := &textshape.GlyphInfo{
		Advance: result.Info.Advance,
		DrMinX:  result.Info.DrMinX,
		DrMinY:  result.Info.DrMinY,
		DrMaxX:  result.Info.DrMaxX,
		DrMaxY:  result.Info.DrMaxY,
		Width:   result.Info.Width,
		Height:  result.Info.Height,
	}
	return info, result.Alpha, nil
}
