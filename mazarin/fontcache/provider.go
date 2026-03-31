package fontcache

import (
	"bytes"
	"fmt"
	"mazarin/textshape"
	"unsafe"

	goFont "github.com/go-text/typesetting/font"
)

// FontSvcGlyphProvider implements textshape.GlyphProvider using IPC to
// fontsvc.maz for font loading and glyph cache access. Cache and font
// file pages are shared read-only via MailboxMapPage.
type FontSvcGlyphProvider struct {
	fc    *FontCache
	fonts [MaxFonts]*fontsvcFont
}

type fontsvcFont struct {
	face     *goFont.Face        // parsed from shared font file pages
	fontData []byte              // shared font file pages (unsafe.Slice)
	cache    []byte              // shared V2 cache pages (unsafe.Slice)
	tier2    map[uint32]*tier2Glyph
	metrics  textshape.FontMetrics
}

type tier2Glyph struct {
	info  textshape.GlyphInfo
	alpha []byte
}

// NewFontSvcGlyphProvider creates a GlyphProvider backed by fontsvc IPC.
func NewFontSvcGlyphProvider(fc *FontCache) *FontSvcGlyphProvider {
	return &FontSvcGlyphProvider{fc: fc}
}

// OpenFont sends an OpenFont request to fontsvc. fontsvc builds a V2 cache,
// shares cache and font file pages, and returns metrics. The provider parses
// a local Face from the shared font file pages for HarfBuzz.
func (p *FontSvcGlyphProvider) OpenFont(req textshape.OpenFontRequest) (textshape.FontMetrics, error) {
	// Check if already opened.
	for i := int32(0); i < MaxFonts; i++ {
		if p.fonts[i] != nil && p.fonts[i].metrics.FontID == i {
			// TODO: check path/variant/size match
			return p.fonts[i].metrics, nil
		}
	}

	bold := req.Variant == 1
	reply, err := p.fc.SendOpenFont(req.Path, bold, int64(req.Size))
	if err != nil {
		return textshape.FontMetrics{}, fmt.Errorf("SendOpenFont: %w", err)
	}
	if reply == nil || reply.FontID < 0 {
		return textshape.FontMetrics{}, fmt.Errorf("fontsvc: OpenFont failed for %s", req.Path)
	}

	fontID := reply.FontID

	// Build slices over shared pages.
	var cache []byte
	if reply.CacheAddr != 0 && reply.CacheSize > 0 {
		cache = unsafe.Slice((*byte)(unsafe.Pointer(uintptr(reply.CacheAddr))), int(reply.CacheSize))
	}

	var fontData []byte
	if reply.FontAddr != 0 && reply.FontSize > 0 {
		fontData = unsafe.Slice((*byte)(unsafe.Pointer(uintptr(reply.FontAddr))), int(reply.FontSize))
	}

	// Parse Face from shared font file pages.
	var face *goFont.Face
	if len(fontData) > 0 {
		var parseErr error
		face, parseErr = goFont.ParseTTF(bytes.NewReader(fontData))
		if parseErr != nil {
			return textshape.FontMetrics{}, fmt.Errorf("ParseTTF from shared pages: %w", parseErr)
		}
	}

	metrics := textshape.FontMetrics{
		FontID:  fontID,
		Height:  reply.Height,
		Ascent:  reply.Ascent,
		Descent: reply.Descent,
	}

	p.fonts[fontID] = &fontsvcFont{
		face:     face,
		fontData: fontData,
		cache:    cache,
		tier2:    make(map[uint32]*tier2Glyph),
		metrics:  metrics,
	}

	return metrics, nil
}

// Face returns the go-text Face for the given fontID, parsed from shared
// font file pages.
func (p *FontSvcGlyphProvider) Face(fontID int32) *goFont.Face {
	if fontID < 0 || fontID >= MaxFonts || p.fonts[fontID] == nil {
		return nil
	}
	return p.fonts[fontID].face
}

// GlyphByGID looks up a glyph by GID using a tiered strategy:
// tier-1 (binary search in shared V2 cache), tier-2 (local overflow map),
// then fontsvc IPC for on-demand rasterization.
func (p *FontSvcGlyphProvider) GlyphByGID(fontID int32, gid uint32) (*textshape.GlyphInfo, []byte, error) {
	if fontID < 0 || fontID >= MaxFonts || p.fonts[fontID] == nil {
		return nil, nil, fmt.Errorf("invalid fontID %d", fontID)
	}

	ff := p.fonts[fontID]

	// Tier 1: binary search in shared V2 cache.
	if len(ff.cache) > 0 {
		info, alpha := textshape.LookupByGID(ff.cache, gid)
		if info != nil {
			return info, alpha, nil
		}
	}

	// Tier 2: check local overflow map.
	if t2, ok := ff.tier2[gid]; ok {
		return &t2.info, t2.alpha, nil
	}

	// Tier 2 miss: request from fontsvc.
	reply := p.fc.RequestGlyphByGID(fontID, gid)
	if reply == nil || reply.GlyphSize == 0 {
		return nil, nil, nil
	}

	// Copy glyph data from scratch page into local cache.
	scratchPtr := uintptr(reply.ScratchAddr)
	ge := (*GlyphEntry)(unsafe.Pointer(scratchPtr))
	info := textshape.GlyphInfo{
		Advance: ge.Advance,
		DrMinX:  ge.DrMinX,
		DrMinY:  ge.DrMinY,
		DrMaxX:  ge.DrMaxX,
		DrMaxY:  ge.DrMaxY,
		Width:   ge.Width,
		Height:  ge.Height,
	}
	var alpha []byte
	w := int(ge.Width)
	h := int(ge.Height)
	if w > 0 && h > 0 {
		alpha = make([]byte, w*h)
		srcAlpha := unsafe.Slice((*byte)(unsafe.Pointer(scratchPtr+GlyphEntrySize)), w*h)
		copy(alpha, srcAlpha)
	}

	ff.tier2[gid] = &tier2Glyph{info: info, alpha: alpha}
	return &ff.tier2[gid].info, alpha, nil
}
