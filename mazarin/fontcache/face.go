package fontcache

import (
	"image"
	"mazzy/shared/wm"
	"unsafe"

	"golang.org/x/image/font"
	"golang.org/x/image/math/fixed"
)

// --- V2 cache header (read-only, matches textshape/glyph_cache.go) ---

const v2Magic = 0x47435632 // "GCV2"

// v2Header mirrors textshape.glyphCacheHeader (64 bytes).
type v2Header struct {
	Magic     uint32
	Version   uint32
	PointSize int32
	FontID    uint32
	NumGlyphs uint32
	NumCPMap  uint32
	GIDMapOff uint32
	CPMapOff  uint32
	TotalSize uint32
	Height    int32
	Ascent    int32
	Descent   int32
	_         [12]byte
}

// v2CPMapEntry mirrors textshape.cpMapEntry (12 bytes).
type v2CPMapEntry struct {
	Codepoint  uint32
	GID        uint32
	DataOffset uint32
}

const v2CPMapEntrySize = 12

// sharedFace implements font.Face by reading glyph data from a shared
// memory V2 glyph cache built by fontsvc.maz.
type sharedFace struct {
	fc      *FontCache
	fontID  int32
	header  *v2Header
	cache   uintptr // base VA of shared cache in this shepherd's space
	metrics font.Metrics

	// V2 codepoint map for binary search.
	numCPMap uint32
	cpMapOff uint32

	// Local tier-2 glyph cache — populated on demand.
	localGlyphs map[rune]*localGlyph
}

// localGlyph stores a locally cached copy of a tier-2 glyph
// (copied from fontsvc's scratch page).
type localGlyph struct {
	entry GlyphEntry
	alpha []byte
}

// newSharedFace constructs a sharedFace from an OpenFontReply.
func newSharedFace(fc *FontCache, reply *wm.OpenFontReply) *sharedFace {
	cacheBase := uintptr(reply.CacheAddr)
	header := (*v2Header)(unsafe.Pointer(cacheBase))

	f := &sharedFace{
		fc:       fc,
		fontID:   reply.FontID,
		header:   header,
		cache:    cacheBase,
		numCPMap: header.NumCPMap,
		cpMapOff: header.CPMapOff,
		metrics: font.Metrics{
			Height:  fixed.Int26_6(reply.Height),
			Ascent:  fixed.Int26_6(reply.Ascent),
			Descent: fixed.Int26_6(reply.Descent),
		},
		localGlyphs: make(map[rune]*localGlyph),
	}
	return f
}

// Close releases resources. The shared memory remains mapped.
func (f *sharedFace) Close() error {
	return nil
}

// Metrics returns the font-level metrics.
func (f *sharedFace) Metrics() font.Metrics {
	return f.metrics
}

// Kern returns 0 — shared cache does not store kerning pairs.
func (f *sharedFace) Kern(r0, r1 rune) fixed.Int26_6 {
	return 0
}

// GlyphAdvance returns the advance width for r.
func (f *sharedFace) GlyphAdvance(r rune) (advance fixed.Int26_6, ok bool) {
	e := f.lookupGlyph(r)
	if e == nil {
		return 0, false
	}
	return fixed.Int26_6(e.Advance), true
}

// GlyphBounds returns the bounding box and advance for r.
func (f *sharedFace) GlyphBounds(r rune) (bounds fixed.Rectangle26_6, advance fixed.Int26_6, ok bool) {
	e := f.lookupGlyph(r)
	if e == nil {
		return fixed.Rectangle26_6{}, 0, false
	}
	bounds = fixed.Rectangle26_6{
		Min: fixed.Point26_6{
			X: fixed.I(int(e.DrMinX)),
			Y: fixed.I(int(e.DrMinY)),
		},
		Max: fixed.Point26_6{
			X: fixed.I(int(e.DrMaxX)),
			Y: fixed.I(int(e.DrMaxY)),
		},
	}
	return bounds, fixed.Int26_6(e.Advance), true
}

// Glyph returns the draw rectangle, mask image, and advance for r at dot.
func (f *sharedFace) Glyph(dot fixed.Point26_6, r rune) (
	dr image.Rectangle, mask image.Image, maskp image.Point,
	advance fixed.Int26_6, ok bool,
) {
	e, alpha := f.lookupGlyphWithAlpha(r)
	if e == nil {
		return image.Rectangle{}, nil, image.Point{}, 0, false
	}

	px := dot.X.Floor()
	py := dot.Y.Floor()

	dr = image.Rect(
		px+int(e.DrMinX), py+int(e.DrMinY),
		px+int(e.DrMaxX), py+int(e.DrMaxY),
	)

	w := int(e.Width)
	h := int(e.Height)
	if w == 0 || h == 0 {
		// Space character or zero-size glyph — advance only.
		return dr, nil, image.Point{}, fixed.Int26_6(e.Advance), true
	}

	mask = &image.Alpha{
		Pix:    alpha,
		Stride: w,
		Rect:   image.Rect(0, 0, w, h),
	}
	return dr, mask, image.Point{}, fixed.Int26_6(e.Advance), true
}

// lookupGlyph finds a GlyphEntry for r. Checks tier-1 (shared cache) first,
// then tier-2 (local cache), then requests from fontsvc.
func (f *sharedFace) lookupGlyph(r rune) *GlyphEntry {
	e, _ := f.lookupGlyphWithAlpha(r)
	return e
}

// lookupGlyphWithAlpha returns both the GlyphEntry and alpha pixel slice.
func (f *sharedFace) lookupGlyphWithAlpha(r rune) (*GlyphEntry, []byte) {
	// Tier 1: binary search the V2 codepoint map.
	cp := uint32(r)
	lo, hi := uint32(0), f.numCPMap
	if hi > 0 {
		hi--
		for lo <= hi {
			mid := lo + (hi-lo)/2
			me := (*v2CPMapEntry)(unsafe.Pointer(f.cache + uintptr(f.cpMapOff) + uintptr(mid)*v2CPMapEntrySize))
			if me.Codepoint == cp {
				ge := (*GlyphEntry)(unsafe.Pointer(f.cache + uintptr(me.DataOffset)))
				w := int(ge.Width)
				h := int(ge.Height)
				var alpha []byte
				if w > 0 && h > 0 {
					alphaPtr := f.cache + uintptr(me.DataOffset) + GlyphEntrySize
					alpha = unsafe.Slice((*byte)(unsafe.Pointer(alphaPtr)), w*h)
				}
				return ge, alpha
			}
			if me.Codepoint < cp {
				lo = mid + 1
			} else {
				if mid == 0 {
					break
				}
				hi = mid - 1
			}
		}
	}

	// Tier 2: check local cache.
	if lg, ok := f.localGlyphs[r]; ok {
		return &lg.entry, lg.alpha
	}

	// Tier 2 miss: request from fontsvc by codepoint (legacy font.Face path).
	reply := f.fc.requestGlyphByCodepoint(f.fontID, r)
	if reply == nil || reply.GlyphSize == 0 {
		return nil, nil
	}

	// Copy glyph data from scratch page into local cache.
	scratchPtr := uintptr(reply.ScratchAddr)
	ge := (*GlyphEntry)(unsafe.Pointer(scratchPtr))
	w := int(ge.Width)
	h := int(ge.Height)
	lg := &localGlyph{entry: *ge}
	if w > 0 && h > 0 {
		lg.alpha = make([]byte, w*h)
		srcAlpha := unsafe.Slice((*byte)(unsafe.Pointer(scratchPtr+GlyphEntrySize)), w*h)
		copy(lg.alpha, srcAlpha)
	}
	f.localGlyphs[r] = lg
	return &lg.entry, lg.alpha
}
