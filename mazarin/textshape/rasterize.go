package textshape

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"math"
	"os"
	"path/filepath"

	goFont "github.com/go-text/typesetting/font"
	ot "github.com/go-text/typesetting/font/opentype"
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

// directFont tracks a font loaded by DirectGlyphProvider.
type directFont struct {
	face    *goFont.Face
	scale   float32
	path    string
	variant int32
	size    int32
}

// DirectGlyphProvider implements [GlyphProvider] using in-process
// rasterization via [RenderGlyph]. This is the provider used on darwin
// where fonts are loaded and rasterized directly, without fontsvc IPC.
type DirectGlyphProvider struct {
	fontDir string
	fonts   [maxFonts]*directFont
}

// NewDirectGlyphProvider creates a DirectGlyphProvider that loads font
// files from fontDir.
func NewDirectGlyphProvider(fontDir string) *DirectGlyphProvider {
	return &DirectGlyphProvider{fontDir: fontDir}
}

// OpenFont loads a font at the requested size and returns font-level
// metrics. If the same font/variant/size was already opened, the cached
// result is returned.
func (p *DirectGlyphProvider) OpenFont(req OpenFontRequest) (FontMetrics, error) {
	// Check cache hit.
	if id := p.findCachedFont(req.Path, req.Variant, req.Size); id >= 0 {
		return p.metricsFor(id), nil
	}

	// Allocate a slot.
	fontID := p.allocFontID()
	if fontID < 0 {
		return FontMetrics{}, errors.New("no free font slots (max 32)")
	}

	// Resolve file path.
	resolved := filepath.Join(p.fontDir, req.Path)

	// Reuse an already-parsed face if same file was loaded at different size.
	face := p.findParsedFace(req.Path, req.Variant)
	if face == nil {
		data, err := os.ReadFile(resolved)
		if err != nil {
			return FontMetrics{}, fmt.Errorf("read font file: %w", err)
		}
		var parseErr error
		face, parseErr = goFont.ParseTTF(bytes.NewReader(data))
		if parseErr != nil {
			return FontMetrics{}, fmt.Errorf("parse font: %w", parseErr)
		}
	}

	upem := float32(face.Upem())
	scale := float32(req.Size) / upem

	p.fonts[fontID] = &directFont{
		face:    face,
		scale:   scale,
		path:    req.Path,
		variant: req.Variant,
		size:    req.Size,
	}

	return p.metricsFor(int32(fontID)), nil
}

// Face returns the go-text Face for the given fontID.
func (p *DirectGlyphProvider) Face(fontID int32) *goFont.Face {
	if fontID < 0 || fontID >= maxFonts || p.fonts[fontID] == nil {
		return nil
	}
	return p.fonts[fontID].face
}

// GlyphByGID rasterizes a glyph on-demand by GID.
func (p *DirectGlyphProvider) GlyphByGID(fontID int32, gid uint32) (*GlyphInfo, []byte, error) {
	if fontID < 0 || fontID >= maxFonts || p.fonts[fontID] == nil {
		return nil, nil, fmt.Errorf("invalid fontID %d", fontID)
	}

	df := p.fonts[fontID]
	info, alpha := RenderGlyph(df.face, goFont.GID(gid), df.scale)
	if info == nil {
		return nil, nil, nil
	}
	return info, alpha, nil
}

// --- internal helpers ---

func (p *DirectGlyphProvider) findCachedFont(path string, variant, size int32) int32 {
	for i := int32(0); i < maxFonts; i++ {
		if p.fonts[i] != nil && p.fonts[i].path == path &&
			p.fonts[i].variant == variant && p.fonts[i].size == size {
			return i
		}
	}
	return -1
}

func (p *DirectGlyphProvider) findParsedFace(path string, variant int32) *goFont.Face {
	for i := int32(0); i < maxFonts; i++ {
		if p.fonts[i] != nil && p.fonts[i].path == path &&
			p.fonts[i].variant == variant && p.fonts[i].face != nil {
			return p.fonts[i].face
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
	ext, _ := df.face.FontHExtents()
	return FontMetrics{
		FontID:    fontID,
		Height:    int32((ext.Ascender - ext.Descender + ext.LineGap) * df.scale * 64),
		Ascent:    int32(ext.Ascender * df.scale * 64),
		Descent:   int32(-ext.Descender * df.scale * 64),
		XHeight:   int32(df.face.LineMetric(goFont.XHeight) * df.scale * 64),
		CapHeight: int32(df.face.LineMetric(goFont.CapHeight) * df.scale * 64),
	}
}
