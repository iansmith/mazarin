package font

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"math"
	"os"
	"path/filepath"
	"unsafe"

	goFont "github.com/go-text/typesetting/font"
	ot "github.com/go-text/typesetting/font/opentype"
	"golang.org/x/image/vector"
)

type fontSlot struct {
	inUse   bool
	path    string
	variant int32
	size    int32
	cache   []byte // 2MB, same binary layout as fontsvc
	face    *goFont.Face
	scale   float32 // pointSize / upem
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
	if id := s.findCachedFont(req.Path, req.Variant, req.Size); id >= 0 {
		return s.fontInfoFromCache(id), nil
	}

	// Allocate a slot.
	fontID := s.allocFontID()
	if fontID < 0 {
		return FontInfo{}, errors.New("no free font slots (max 32)")
	}

	// Resolve file path.
	resolved := filepath.Join(s.fontDir, req.Path)

	// Reuse an already-parsed face if same file was loaded at different size.
	face := s.findParsedFace(req.Path, req.Variant)
	if face == nil {
		data, err := os.ReadFile(resolved)
		if err != nil {
			return FontInfo{}, fmt.Errorf("read font file: %w", err)
		}
		var parseErr error
		face, parseErr = goFont.ParseTTF(bytes.NewReader(data))
		if parseErr != nil {
			return FontInfo{}, fmt.Errorf("parse font: %w", parseErr)
		}
	}

	upem := float32(face.Upem())
	scale := float32(req.Size) / upem

	// Allocate cache and build glyph data.
	cache := make([]byte, cacheSizeBytes)
	buildGlyphCache(cache, face, scale, uint32(fontID), req.Size)

	// Store slot.
	s.fonts[fontID] = fontSlot{
		inUse:   true,
		path:    req.Path,
		variant: req.Variant,
		size:    req.Size,
		cache:   cache,
		face:    face,
		scale:   scale,
	}

	return s.fontInfoFromCache(fontID), nil
}

// Face returns the underlying go-text/typesetting Face for the given fontID,
// or nil if not loaded. This is needed by the textshape package to share
// the parsed face with HarfBuzz.
func (s *FontSvcSim) Face(fontID int32) *goFont.Face {
	if fontID < 0 || fontID >= maxFonts || !s.fonts[fontID].inUse {
		return nil
	}
	return s.fonts[fontID].face
}

// RequestGlyph renders a single glyph on-demand (tier-2). Returns the
// glyph metrics and the raw alpha-channel bytes (8-bit, row-major,
// stride = Width). Returns nil info if the glyph is unsupported.
func (s *FontSvcSim) RequestGlyph(fontID int32, codepoint rune) (*GlyphInfo, []byte, error) {
	if fontID < 0 || fontID >= maxFonts || !s.fonts[fontID].inUse {
		return nil, nil, fmt.Errorf("invalid fontID %d", fontID)
	}

	slot := &s.fonts[fontID]
	gid, ok := slot.face.NominalGlyph(codepoint)
	if !ok {
		return nil, nil, nil // unsupported glyph
	}

	info, alpha := renderGlyph(slot.face, goFont.GID(gid), slot.scale)
	if info == nil {
		return nil, nil, nil
	}
	return info, alpha, nil
}

// RequestGlyphByGID renders a single glyph on-demand by GID (tier-2).
// Returns the glyph metrics and the raw alpha-channel bytes.
// Returns nil info if the glyph has no outline.
func (s *FontSvcSim) RequestGlyphByGID(fontID int32, gid uint32) (*GlyphInfo, []byte, error) {
	if fontID < 0 || fontID >= maxFonts || !s.fonts[fontID].inUse {
		return nil, nil, fmt.Errorf("invalid fontID %d", fontID)
	}

	slot := &s.fonts[fontID]
	info, alpha := renderGlyph(slot.face, goFont.GID(gid), slot.scale)
	if info == nil {
		return nil, nil, nil
	}
	return info, alpha, nil
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

func (s *FontSvcSim) findCachedFont(path string, variant, size int32) int32 {
	for i := int32(0); i < maxFonts; i++ {
		if s.fonts[i].inUse && s.fonts[i].path == path &&
			s.fonts[i].variant == variant && s.fonts[i].size == size {
			return i
		}
	}
	return -1
}

func (s *FontSvcSim) findParsedFace(path string, variant int32) *goFont.Face {
	for i := int32(0); i < maxFonts; i++ {
		if s.fonts[i].inUse && s.fonts[i].path == path &&
			s.fonts[i].variant == variant && s.fonts[i].face != nil {
			return s.fonts[i].face
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

// renderGlyph rasterizes a glyph by GID using outline→vector→alpha.
// Returns nil info if the glyph has no renderable outline.
func renderGlyph(face *goFont.Face, gid goFont.GID, scale float32) (*GlyphInfo, []byte) {
	// Get advance width.
	advFontUnits := face.HorizontalAdvance(gid)
	advFixed := int32(advFontUnits * scale * 64) // fixed.Int26_6

	// Get outline. GlyphDataOutline takes uint16 glyph ID.
	outline, ok := face.GlyphDataOutline(uint16(gid))
	if !ok || len(outline.Segments) == 0 {
		// Space or glyph with no outline — return info with zero dimensions.
		return &GlyphInfo{Advance: advFixed}, nil
	}

	// Scale segments from font units to pixels and find bounding box.
	// Font coords: Y up. Screen coords: Y down. Flip Y.
	var minX, minY, maxX, maxY float32
	first := true
	for _, seg := range outline.Segments {
		for _, pt := range seg.ArgsSlice() {
			x := pt.X * scale
			y := -pt.Y * scale
			if first {
				minX, maxX = x, x
				minY, maxY = y, y
				first = false
			} else {
				if x < minX {
					minX = x
				}
				if x > maxX {
					maxX = x
				}
				if y < minY {
					minY = y
				}
				if y > maxY {
					maxY = y
				}
			}
		}
	}

	// Pixel bounds with 1px padding for anti-aliasing.
	pixMinX := int(math.Floor(float64(minX)))
	pixMinY := int(math.Floor(float64(minY)))
	pixMaxX := int(math.Ceil(float64(maxX))) + 1
	pixMaxY := int(math.Ceil(float64(maxY))) + 1

	w := pixMaxX - pixMinX
	h := pixMaxY - pixMinY
	if w <= 0 || h <= 0 {
		return &GlyphInfo{Advance: advFixed}, nil
	}

	// Rasterize outline to alpha bitmap.
	r := vector.NewRasterizer(w, h)
	offX := float32(pixMinX)
	offY := float32(pixMinY)
	for _, seg := range outline.Segments {
		switch seg.Op {
		case ot.SegmentOpMoveTo:
			r.MoveTo(seg.Args[0].X*scale-offX, -seg.Args[0].Y*scale-offY)
		case ot.SegmentOpLineTo:
			r.LineTo(seg.Args[0].X*scale-offX, -seg.Args[0].Y*scale-offY)
		case ot.SegmentOpQuadTo:
			r.QuadTo(
				seg.Args[0].X*scale-offX, -seg.Args[0].Y*scale-offY,
				seg.Args[1].X*scale-offX, -seg.Args[1].Y*scale-offY,
			)
		case ot.SegmentOpCubeTo:
			r.CubeTo(
				seg.Args[0].X*scale-offX, -seg.Args[0].Y*scale-offY,
				seg.Args[1].X*scale-offX, -seg.Args[1].Y*scale-offY,
				seg.Args[2].X*scale-offX, -seg.Args[2].Y*scale-offY,
			)
		}
	}

	alpha := image.NewAlpha(image.Rect(0, 0, w, h))
	r.Draw(alpha, alpha.Bounds(), image.Opaque, image.Point{})

	return &GlyphInfo{
		Advance: advFixed,
		DrMinX:  int16(pixMinX),
		DrMinY:  int16(pixMinY),
		DrMaxX:  int16(pixMaxX),
		DrMaxY:  int16(pixMaxY),
		Width:   uint16(w),
		Height:  uint16(h),
	}, alpha.Pix
}
