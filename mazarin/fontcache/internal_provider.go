package fontcache

import (
	"bytes"
	"errors"
	"fmt"
	"mazarin/textshape"
	"sync"

	goFont "github.com/go-text/typesetting/font"
)

// InternalGlyphProvider implements textshape.GlyphProvider for the
// in-process case where fontsvc runs inside the same shepherd (rachel).
// It calls registered callbacks directly — no uring IPC, no SharePages.
// Cache and font data slices are returned directly since they are
// already in the same address space.
//
// Buffer-registered fonts (CSS @font-face, via RegisterBuffer) bypass the
// callbacks and run entirely in this provider — same shape as
// FontSvcGlyphProvider's registered path.
type InternalGlyphProvider struct {
	openFontCb   func(family string, variant, size int32) (InternalOpenFontResult, bool)
	glyphByGIDCb func(fontID int32, gid uint32) (InternalGlyphResult, bool)
	fonts        [MaxFonts]*internalFont
	regMu        sync.Mutex
	registered   map[regKey]*registeredFace
}

type internalFont struct {
	face       *goFont.Face
	cache      []byte
	metrics    textshape.FontMetrics
	registered bool    // true if opened via RegisterBuffer (no callback path)
	scale      float32 // pointSize / upem, for in-process RenderGlyph
	tier2      map[uint32]*tier2Glyph
}

// Compile-time check that InternalGlyphProvider implements textshape.GlyphProvider.
var _ textshape.GlyphProvider = (*InternalGlyphProvider)(nil)

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
	// Check registered buffer (CSS @font-face). Stays entirely in-process —
	// no callback, no fontsvc cache page.
	if reg := p.findRegistered(req.Family, req.Variant); reg != nil {
		return p.openRegistered(req, reg)
	}

	result, ok := p.openFontCb(req.Family, req.Variant, req.Size)
	if !ok {
		return textshape.FontMetrics{}, fmt.Errorf("internal OpenFont failed for %s", req.Family)
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

// RegisterBuffer registers a parsed font buffer under (family, variant).
// Subsequent OpenFont calls for that key serve the face entirely
// in-process — no fontsvc callback, no shared cache pages, just
// textshape.RenderGlyph for tier-2 misses. Mirrors
// FontSvcGlyphProvider.RegisterBuffer / DirectGlyphProvider.RegisterBuffer.
func (p *InternalGlyphProvider) RegisterBuffer(family string, variant int32, data []byte) error {
	if family == "" {
		return errors.New("RegisterBuffer: empty family")
	}
	if len(data) == 0 {
		return errors.New("RegisterBuffer: empty data")
	}
	face, err := goFont.ParseTTF(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("RegisterBuffer: parse font: %w", err)
	}
	p.regMu.Lock()
	defer p.regMu.Unlock()
	if p.registered == nil {
		p.registered = make(map[regKey]*registeredFace)
	}
	p.registered[regKey{family, variant}] = &registeredFace{face: face, fontData: data}
	return nil
}

// findRegistered returns the registered face for (family, variant), or nil.
func (p *InternalGlyphProvider) findRegistered(family string, variant int32) *registeredFace {
	p.regMu.Lock()
	defer p.regMu.Unlock()
	if p.registered == nil {
		return nil
	}
	return p.registered[regKey{family, variant}]
}

// openRegistered opens a font from a previously-registered buffer entirely
// in-process. Allocates a local fontID slot and computes metrics directly.
func (p *InternalGlyphProvider) openRegistered(req textshape.OpenFontRequest, reg *registeredFace) (textshape.FontMetrics, error) {
	fontID := p.allocFontID()
	if fontID < 0 {
		return textshape.FontMetrics{}, errors.New("no free font slots")
	}

	upem := float32(reg.face.Upem())
	scale := float32(req.Size) / upem
	metrics := textshape.ComputeFontMetricsWithData(reg.face, scale, fontID, reg.fontData)

	p.fonts[fontID] = &internalFont{
		face:       reg.face,
		cache:      nil, // no shared cache for registered fonts
		metrics:    metrics,
		registered: true,
		scale:      scale,
		tier2:      make(map[uint32]*tier2Glyph),
	}

	return metrics, nil
}

// allocFontID finds a free FontID slot in the local fonts array.
func (p *InternalGlyphProvider) allocFontID() int32 {
	for i := int32(0); i < MaxFonts; i++ {
		if p.fonts[i] == nil {
			return i
		}
	}
	return -1
}

// ResetOpenedFonts clears the per-FontID opened-font slot table. The
// registered-buffer map is preserved so CSS @font-face fonts remain
// available across resets. Mirrors FontSvcGlyphProvider.ResetOpenedFonts.
func (p *InternalGlyphProvider) ResetOpenedFonts() {
	for i := range p.fonts {
		p.fonts[i] = nil
	}
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

	// Registered (CSS @font-face) fonts: tier-2 overflow + in-process
	// RenderGlyph. No fontsvc callback — fontsvc never saw the buffer.
	if ff.registered {
		if t2, ok := ff.tier2[gid]; ok {
			return &t2.info, t2.alpha, nil
		}
		info, alpha := textshape.RenderGlyph(ff.face, goFont.GID(gid), ff.scale)
		if info == nil {
			return nil, nil, nil
		}
		ff.tier2[gid] = &tier2Glyph{info: *info, alpha: alpha}
		return info, alpha, nil
	}

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
