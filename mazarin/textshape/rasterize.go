package textshape

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"math"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	goFont "github.com/go-text/typesetting/font"
	ot "github.com/go-text/typesetting/font/opentype"
	ottables "github.com/go-text/typesetting/font/opentype/tables"
	"golang.org/x/image/vector"
)

// RenderGlyph rasterizes a glyph by GID using outline segments from the
// go-text Face, producing an alpha bitmap. scale is pointSize / upem.
// Returns nil info if the glyph has no renderable outline.
func RenderGlyph(face *goFont.Face, gid goFont.GID, scale float32) (*GlyphInfo, []byte) {
	// Get advance width.
	advFontUnits := face.HorizontalAdvance(gid)
	advFixed := int32(advFontUnits * scale * 64) // fixed.Int26_6

	// Get outline. GlyphDataOutline takes uint16 glyph ID.
	outline, ok := face.GlyphDataOutline(uint16(gid))
	if !ok || len(outline.Segments) == 0 {
		// Space or glyph with no outline -- return info with zero dimensions.
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

// tier2Glyph holds a glyph rendered on-demand that didn't fit in
// the tier-1 cache (overflow or ligature GID).
type tier2Glyph struct {
	info  GlyphInfo
	alpha []byte
}

// directFont tracks a font loaded by DirectGlyphProvider.
type directFont struct {
	face     *goFont.Face
	scale    float32
	family   string
	variant  int32
	size     int32
	fontData []byte                 // mmap'd font file (backing face)
	cache    []byte                 // tier-1 glyph cache (V2 format)
	tier2    map[uint32]*tier2Glyph // overflow/ligature glyphs
}

// DirectGlyphProvider implements [GlyphProvider] using in-process
// rasterization via [RenderGlyph]. This is the provider used on darwin
// where fonts are loaded and rasterized directly, without fontsvc IPC.
type DirectGlyphProvider struct {
	fontDir   string
	fontIndex *FontIndex
	fonts     [maxFonts]*directFont
}

// NewDirectGlyphProvider creates a DirectGlyphProvider that loads font
// files from fontDir. If fontDir contains a fonts.csv file, it is parsed
// to resolve (family, variant) pairs to filenames. Without fonts.csv,
// Family values that look like filenames are used directly.
func NewDirectGlyphProvider(fontDir string) *DirectGlyphProvider {
	p := &DirectGlyphProvider{fontDir: fontDir}
	if data, err := os.ReadFile(filepath.Join(fontDir, "fonts.csv")); err == nil {
		p.fontIndex = ParseFontIndex(data)
	}
	return p
}

// OpenFont loads a font at the requested size and returns font-level
// metrics. If the same font/variant/size was already opened, the cached
// result is returned.
func (p *DirectGlyphProvider) OpenFont(req OpenFontRequest) (FontMetrics, error) {
	// Check cache hit.
	if id := p.findCachedFont(req.Family, req.Variant, req.Size); id >= 0 {
		return p.metricsFor(id), nil
	}

	// Allocate a slot.
	fontID := p.allocFontID()
	if fontID < 0 {
		return FontMetrics{}, errors.New("no free font slots (max 32)")
	}

	// Resolve family+variant to a filesystem path.
	resolved := p.resolveFamily(req.Family, req.Variant)

	// Reuse an already-parsed face if same family was loaded at different size.
	existing := p.findExistingFont(req.Family, req.Variant)
	var face *goFont.Face
	var fontData []byte
	if existing != nil {
		face = existing.face
		fontData = existing.fontData
	} else {
		// mmap the font file read-only.
		f, err := os.Open(resolved)
		if err != nil {
			return FontMetrics{}, fmt.Errorf("open font file %s: %w", resolved, err)
		}
		fi, err := f.Stat()
		if err != nil {
			f.Close()
			return FontMetrics{}, fmt.Errorf("stat font file: %w", err)
		}
		fontData, err = syscall.Mmap(int(f.Fd()), 0, int(fi.Size()),
			syscall.PROT_READ, syscall.MAP_PRIVATE)
		f.Close()
		if err != nil {
			return FontMetrics{}, fmt.Errorf("mmap font file: %w", err)
		}
		face, err = goFont.ParseTTF(bytes.NewReader(fontData))
		if err != nil {
			syscall.Munmap(fontData)
			return FontMetrics{}, fmt.Errorf("parse font: %w", err)
		}
	}

	upem := float32(face.Upem())
	scale := float32(req.Size) / upem

	p.fonts[fontID] = &directFont{
		face:     face,
		scale:    scale,
		family:   req.Family,
		variant:  req.Variant,
		size:     req.Size,
		fontData: fontData,
		tier2:    make(map[uint32]*tier2Glyph),
	}

	m := p.metricsFor(int32(fontID))
	p.fonts[fontID].cache = buildGlyphCache(face, scale, uint32(fontID), req.Size, m)

	return m, nil
}

// resolveFamily converts a logical family name + variant to a filesystem path.
// Uses the font index if available. Falls back to treating the family as a
// filename or path (for web fonts and backward compatibility).
func (p *DirectGlyphProvider) resolveFamily(family string, variant int32) string {
	// Try font index first.
	if p.fontIndex != nil {
		if filename := p.fontIndex.ResolveVariant(family, variant); filename != "" {
			return filepath.Join(p.fontDir, filename)
		}
	}
	// Fallback: if it looks like a path or filename, use directly.
	if strings.Contains(family, "/") || strings.Contains(family, ".ttf") ||
		strings.Contains(family, ".otf") {
		if filepath.IsAbs(family) {
			return family
		}
		return filepath.Join(p.fontDir, family)
	}
	// Last resort: try family as-is relative to fontDir.
	return filepath.Join(p.fontDir, family)
}

// Face returns the go-text Face for the given fontID.
func (p *DirectGlyphProvider) Face(fontID int32) *goFont.Face {
	if fontID < 0 || fontID >= maxFonts || p.fonts[fontID] == nil {
		return nil
	}
	return p.fonts[fontID].face
}

// GlyphByGID looks up a glyph by GID using a tiered strategy:
// tier-1 (binary search in pre-rendered cache), tier-2 (overflow map),
// then on-demand rasterization for cache misses.
func (p *DirectGlyphProvider) GlyphByGID(fontID int32, gid uint32) (*GlyphInfo, []byte, error) {
	if fontID < 0 || fontID >= maxFonts || p.fonts[fontID] == nil {
		return nil, nil, fmt.Errorf("invalid fontID %d", fontID)
	}

	df := p.fonts[fontID]

	// Tier 1: binary search in pre-rendered cache.
	if df.cache != nil {
		info, alpha := LookupByGID(df.cache, gid)
		if info != nil {
			return info, alpha, nil
		}
	}

	// Tier 2: check overflow map (previously rendered on-demand).
	if t2, ok := df.tier2[gid]; ok {
		return &t2.info, t2.alpha, nil
	}

	// Tier 2 miss: render on-demand + cache in overflow map.
	info, alpha := RenderGlyph(df.face, goFont.GID(gid), df.scale)
	if info == nil {
		return nil, nil, nil
	}
	df.tier2[gid] = &tier2Glyph{info: *info, alpha: alpha}
	return info, alpha, nil
}

// --- internal helpers ---

func (p *DirectGlyphProvider) findCachedFont(family string, variant, size int32) int32 {
	for i := int32(0); i < maxFonts; i++ {
		if p.fonts[i] != nil && p.fonts[i].family == family &&
			p.fonts[i].variant == variant && p.fonts[i].size == size {
			return i
		}
	}
	return -1
}

func (p *DirectGlyphProvider) findExistingFont(family string, variant int32) *directFont {
	for i := int32(0); i < maxFonts; i++ {
		if p.fonts[i] != nil && p.fonts[i].family == family &&
			p.fonts[i].variant == variant && p.fonts[i].face != nil {
			return p.fonts[i]
		}
	}
	return nil
}

func (p *DirectGlyphProvider) allocFontID() int32 {
	for i := int32(0); i < maxFonts; i++ {
		if p.fonts[i] == nil {
			return i
		}
	}
	return -1
}

func (p *DirectGlyphProvider) metricsFor(fontID int32) FontMetrics {
	df := p.fonts[fontID]
	return computeFontMetricsWithData(df.face, df.scale, fontID, df.fontData)
}

// ComputeFontMetrics extracts font-level metrics from a go-text Face.
// All values are in fixed.Int26_6 format (multiply by 64).
//
// This entry point uses only the go-text Face (no raw font data), so it
// reports whatever Ascender/Descender/LineGap the Face exposes — which is
// hhea metrics unless the font's OS/2 USE_TYPO_METRICS bit is set. Prefer
// computeFontMetricsWithData when raw font bytes are available, so that
// OS/2 sTypo metrics can be used regardless of the USE_TYPO_METRICS bit
// (matching Blink's policy of preferring sTypo for modern fonts).
func ComputeFontMetrics(face *goFont.Face, scale float32, fontID int32) FontMetrics {
	return computeFontMetricsWithData(face, scale, fontID, nil)
}

// computeFontMetricsWithData builds FontMetrics, preferring OS/2 sTypo
// metrics parsed from fontData when they are present. Falls back to the
// Face's FontHExtents (hhea-based for fonts without USE_TYPO_METRICS).
func computeFontMetricsWithData(face *goFont.Face, scale float32, fontID int32, fontData []byte) FontMetrics {
	asc, desc, gap, ok := parseSTypoMetrics(fontData)
	if !ok {
		ext, _ := face.FontHExtents()
		asc, desc, gap = ext.Ascender, ext.Descender, ext.LineGap
	}
	return FontMetrics{
		FontID:    fontID,
		Height:    int32((asc - desc + gap) * scale * 64),
		Ascent:    int32(asc * scale * 64),
		Descent:   int32(-desc * scale * 64),
		XHeight:   int32(face.LineMetric(goFont.XHeight) * scale * 64),
		CapHeight: int32(face.LineMetric(goFont.CapHeight) * scale * 64),
	}
}

// parseSTypoMetrics reads the OS/2 table from fontData and returns the
// sTypoAscender / sTypoDescender / sTypoLineGap values in font units.
// Returns ok=false if the OS/2 table is absent, fails to parse, or has
// an invalid (zero) STypoAscender. Descender is returned as a negative
// value (matching the FontHExtents convention).
func parseSTypoMetrics(fontData []byte) (ascender, descender, lineGap float32, ok bool) {
	if len(fontData) == 0 {
		return 0, 0, 0, false
	}
	loader, err := ot.NewLoader(bytes.NewReader(fontData))
	if err != nil {
		return 0, 0, 0, false
	}
	raw, err := loader.RawTable(ot.MustNewTag("OS/2"))
	if err != nil || len(raw) == 0 {
		return 0, 0, 0, false
	}
	os2, _, err := ottables.ParseOs2(raw)
	if err != nil {
		return 0, 0, 0, false
	}
	// Reject clearly invalid sTypo tables (zero ascender).
	if os2.STypoAscender == 0 {
		return 0, 0, 0, false
	}
	return float32(os2.STypoAscender), float32(os2.STypoDescender), float32(os2.STypoLineGap), true
}
