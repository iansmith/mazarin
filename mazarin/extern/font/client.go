package font

import (
	"fmt"
	"unsafe"
)

// cachedGlyph stores a tier-2 glyph that was fetched on-demand.
type cachedGlyph struct {
	info  GlyphInfo
	alpha []byte // alpha channel pixels (Width * Height bytes)
}

// fontCache holds per-font state for the FontClient.
type fontCache struct {
	info       FontInfo
	raw        []byte           // raw 2MB tier-1 cache
	mapEntries []glyphMapEntry  // parsed from cache, sorted for binary search
	tier2      map[rune]*cachedGlyph
}

// FontClient provides a high-level font API with transparent caching.
// Tier-1 glyphs come from the pre-built 2MB cache. Tier-2 glyphs (that
// didn't fit) are fetched on-demand from the simulator and cached locally
// so subsequent lookups are instant.
type FontClient struct {
	sim    *FontSvcSim
	caches [maxFonts]*fontCache
}

// NewFontClient creates a FontClient that loads fonts from fontDir.
func NewFontClient(fontDir string) *FontClient {
	return &FontClient{
		sim: NewFontSvcSim(fontDir),
	}
}

// OpenFont opens a font at the requested size and returns font-level metrics.
// The tier-1 glyph cache is built immediately. Subsequent calls with the
// same path/variant/size return the cached result.
func (c *FontClient) OpenFont(req OpenFontRequest) (FontInfo, error) {
	info, err := c.sim.OpenFont(req)
	if err != nil {
		return FontInfo{}, err
	}

	// Initialize the per-font cache if not already done.
	if c.caches[info.FontID] == nil {
		raw := c.sim.Cache(info.FontID)
		entries := parseGlyphMap(raw)
		c.caches[info.FontID] = &fontCache{
			info:       info,
			raw:        raw,
			mapEntries: entries,
			tier2:      make(map[rune]*cachedGlyph),
		}
	}

	return info, nil
}

// GlyphInfo returns per-glyph metrics for the given codepoint.
// Looks up tier-1 cache first, then tier-2 (fetching on-demand if needed).
// Returns nil if the glyph is unsupported.
func (c *FontClient) GlyphInfo(fontID int32, codepoint rune) (*GlyphInfo, error) {
	info, _, err := c.lookupGlyph(fontID, codepoint)
	return info, err
}

// GlyphAlpha returns the alpha-channel bitmap for the given codepoint.
// The result is 8-bit grayscale, one byte per pixel, row-major layout
// with stride = GlyphInfo.Width. Returns nil if the glyph is unsupported.
func (c *FontClient) GlyphAlpha(fontID int32, codepoint rune) ([]byte, error) {
	_, alpha, err := c.lookupGlyph(fontID, codepoint)
	return alpha, err
}

// Glyph returns both metrics and alpha bitmap in one call, avoiding a
// double lookup.
func (c *FontClient) Glyph(fontID int32, codepoint rune) (*GlyphInfo, []byte, error) {
	return c.lookupGlyph(fontID, codepoint)
}

// lookupGlyph implements the three-tier lookup: tier-1 binary search,
// tier-2 local cache, tier-2 miss (fetch from sim and cache).
func (c *FontClient) lookupGlyph(fontID int32, codepoint rune) (*GlyphInfo, []byte, error) {
	if fontID < 0 || fontID >= maxFonts || c.caches[fontID] == nil {
		return nil, nil, fmt.Errorf("font %d not opened", fontID)
	}
	fc := c.caches[fontID]

	// Tier-1: binary search the pre-built glyph map.
	if idx := binarySearch(fc.mapEntries, codepoint); idx >= 0 {
		offset := fc.mapEntries[idx].Offset
		ge := (*glyphEntry)(unsafe.Pointer(&fc.raw[offset]))
		info := glyphEntryToInfo(ge)

		var alpha []byte
		if ge.Width > 0 && ge.Height > 0 {
			start := offset + glyphEntrySize
			length := uint32(ge.Width) * uint32(ge.Height)
			alpha = fc.raw[start : start+length]
		}
		return info, alpha, nil
	}

	// Tier-2: check local cache.
	if cg, ok := fc.tier2[codepoint]; ok {
		if cg == nil {
			return nil, nil, nil // previously determined unsupported
		}
		return &cg.info, cg.alpha, nil
	}

	// Tier-2 miss: fetch from simulator and cache.
	info, alpha, err := c.sim.RequestGlyph(fontID, codepoint)
	if err != nil {
		return nil, nil, err
	}
	if info == nil {
		// Cache the miss so we don't re-render.
		fc.tier2[codepoint] = nil
		return nil, nil, nil
	}

	fc.tier2[codepoint] = &cachedGlyph{
		info:  *info,
		alpha: alpha,
	}
	return info, alpha, nil
}

// --- internal helpers ---

// parseGlyphMap reads the glyph map entries from the raw cache bytes.
func parseGlyphMap(cache []byte) []glyphMapEntry {
	hdr := (*cacheHeader)(unsafe.Pointer(&cache[0]))
	n := int(hdr.NumGlyphs)
	if n == 0 {
		return nil
	}
	entries := make([]glyphMapEntry, n)
	for i := 0; i < n; i++ {
		off := hdr.MapOffset + uint32(i)*8
		me := (*glyphMapEntry)(unsafe.Pointer(&cache[off]))
		entries[i] = *me
	}
	return entries
}

// binarySearch finds codepoint in the sorted map entries.
// Returns the index, or -1 if not found.
func binarySearch(entries []glyphMapEntry, cp rune) int {
	target := uint32(cp)
	lo, hi := 0, len(entries)-1
	for lo <= hi {
		mid := lo + (hi-lo)/2
		v := entries[mid].Codepoint
		if v == target {
			return mid
		}
		if v < target {
			lo = mid + 1
		} else {
			hi = mid - 1
		}
	}
	return -1
}

// glyphEntryToInfo converts the internal binary glyphEntry to the exported GlyphInfo.
func glyphEntryToInfo(ge *glyphEntry) *GlyphInfo {
	return &GlyphInfo{
		Advance: ge.Advance,
		DrMinX:  ge.DrMinX,
		DrMinY:  ge.DrMinY,
		DrMaxX:  ge.DrMaxX,
		DrMaxY:  ge.DrMaxY,
		Width:   ge.Width,
		Height:  ge.Height,
	}
}
