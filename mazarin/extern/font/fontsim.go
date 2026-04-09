package font

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

type fontSlot struct {
	inUse   bool
	family  string
	variant int32
	size    int32
	cache   []byte // 2MB, same binary layout as fontsvc
	otFont  *opentype.Font
	face    font.Face
}

// FontSvcSim simulates the fontsvc font service locally on darwin.
// It parses real font files and builds glyph caches with the same
// binary layout that fontsvc.maz produces inside kmazarin.
type FontSvcSim struct {
	fontDir string
	fonts   [maxFonts]fontSlot
}

// NewFontSvcSim creates a font service simulator that loads font files
// from fontDir.
func NewFontSvcSim(fontDir string) *FontSvcSim {
	return &FontSvcSim{fontDir: fontDir}
}

// OpenFont loads a font at the requested size, builds the glyph cache,
// and returns font-level metrics. If the same font/variant/size was
// already opened, the cached result is returned.
func (s *FontSvcSim) OpenFont(req OpenFontRequest) (FontInfo, error) {
	// Check cache hit.
	if id := s.findCachedFont(req.Family, req.Variant, req.Size); id >= 0 {
		return s.fontInfoFromCache(id), nil
	}

	// Allocate a slot.
	fontID := s.allocFontID()
	if fontID < 0 {
		return FontInfo{}, errors.New("no free font slots (max 32)")
	}

	// Resolve file path.
	resolved := filepath.Join(s.fontDir, req.Family)

	// Reuse an already-parsed font if same file was loaded at different size.
	otFont := s.findParsedFont(req.Family, req.Variant)
	if otFont == nil {
		data, err := os.ReadFile(resolved)
		if err != nil {
			return FontInfo{}, fmt.Errorf("read font file: %w", err)
		}
		var parseErr error
		otFont, parseErr = opentype.Parse(data)
		if parseErr != nil {
			return FontInfo{}, fmt.Errorf("parse font: %w", parseErr)
		}
	}

	// Create face at requested size.
	face, err := opentype.NewFace(otFont, &opentype.FaceOptions{
		Size:    float64(req.Size),
		DPI:     72,
		Hinting: font.HintingFull,
	})
	if err != nil {
		return FontInfo{}, fmt.Errorf("create face: %w", err)
	}

	// Allocate cache and build glyph data.
	cache := make([]byte, cacheSizeBytes)
	metrics := face.Metrics()
	buildGlyphCache(cache, face, metrics, uint32(fontID), req.Size)

	// Store slot.
	s.fonts[fontID] = fontSlot{
		inUse:   true,
		family:  req.Family,
		variant: req.Variant,
		size:    req.Size,
		cache:   cache,
		otFont:  otFont,
		face:    face,
	}

	return s.fontInfoFromCache(fontID), nil
}

// RequestGlyph renders a single glyph on-demand (tier-2). Returns the
// glyph metrics and the raw alpha-channel bytes (8-bit, row-major,
// stride = Width). Returns nil info if the glyph is unsupported.
func (s *FontSvcSim) RequestGlyph(fontID int32, codepoint rune) (*GlyphInfo, []byte, error) {
	if fontID < 0 || fontID >= maxFonts || !s.fonts[fontID].inUse {
		return nil, nil, fmt.Errorf("invalid fontID %d", fontID)
	}

	slot := &s.fonts[fontID]
	dot := fixed.Point26_6{}
	dr, mask, maskp, advance, ok := slot.face.Glyph(dot, codepoint)
	if !ok {
		return nil, nil, nil // unsupported glyph
	}

	w := uint16(dr.Dx())
	h := uint16(dr.Dy())
	totalSize := glyphTotalSize(w, h)
	buf := make([]byte, totalSize)

	// Write glyphEntry header.
	ge := (*glyphEntry)(unsafe.Pointer(&buf[0]))
	ge.Advance = int32(advance)
	ge.DrMinX = int16(dr.Min.X)
	ge.DrMinY = int16(dr.Min.Y)
	ge.DrMaxX = int16(dr.Max.X)
	ge.DrMaxY = int16(dr.Max.Y)
	ge.Width = w
	ge.Height = h

	// Copy alpha pixels.
	if w > 0 && h > 0 && mask != nil {
		copyAlphaPixels(buf[glyphEntrySize:], mask, maskp, int(w), int(h))
	}

	info := &GlyphInfo{
		Advance: int32(advance),
		DrMinX:  int16(dr.Min.X),
		DrMinY:  int16(dr.Min.Y),
		DrMaxX:  int16(dr.Max.X),
		DrMaxY:  int16(dr.Max.Y),
		Width:   w,
		Height:  h,
	}
	return info, buf[glyphEntrySize:glyphEntrySize+uint32(w)*uint32(h)], nil
}

// Cache returns the raw 2MB cache bytes for the given fontID, or nil
// if not loaded. The binary layout matches fontsvc exactly: cacheHeader
// at offset 0, glyphMapEntry array at MapOffset, glyph data at DataOffset.
func (s *FontSvcSim) Cache(fontID int32) []byte {
	if fontID < 0 || fontID >= maxFonts || !s.fonts[fontID].inUse {
		return nil
	}
	return s.fonts[fontID].cache
}

// --- internal helpers ---

func (s *FontSvcSim) findCachedFont(family string, variant, size int32) int32 {
	for i := int32(0); i < maxFonts; i++ {
		if s.fonts[i].inUse && s.fonts[i].family == family &&
			s.fonts[i].variant == variant && s.fonts[i].size == size {
			return i
		}
	}
	return -1
}

func (s *FontSvcSim) findParsedFont(family string, variant int32) *opentype.Font {
	for i := int32(0); i < maxFonts; i++ {
		if s.fonts[i].inUse && s.fonts[i].family == family &&
			s.fonts[i].variant == variant && s.fonts[i].otFont != nil {
			return s.fonts[i].otFont
		}
	}
	return nil
}

func (s *FontSvcSim) allocFontID() int32 {
	for i := int32(0); i < maxFonts; i++ {
		if !s.fonts[i].inUse {
			return i
		}
	}
	return -1
}

func (s *FontSvcSim) fontInfoFromCache(fontID int32) FontInfo {
	cache := s.fonts[fontID].cache
	hdr := (*cacheHeader)(unsafe.Pointer(&cache[0]))
	return FontInfo{
		FontID:    fontID,
		Height:    hdr.Height,
		Ascent:    hdr.Ascent,
		Descent:   hdr.Descent,
		XHeight:   hdr.XHeight,
		CapHeight: hdr.CapHeight,
		NumGlyphs: hdr.NumGlyphs,
		CacheSize: hdr.TotalUsed,
	}
}
