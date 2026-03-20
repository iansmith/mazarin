package fontcache

import (
	"image"
	"mazzy/shared/wm"
	"unsafe"

	"golang.org/x/image/font"
	"golang.org/x/image/math/fixed"
)

// sharedFace implements font.Face by reading glyph data from a shared
// memory glyph cache built by fontsvc.maz.
type sharedFace struct {
	fc      *FontCache
	fontID  int32
	header  *CacheHeader
	cache   uintptr // base VA of shared cache in this shepherd's space
	metrics font.Metrics

	// Glyph map: sorted slice of GlyphMapEntry for binary search.
	mapEntries []GlyphMapEntry

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
func newSharedFace(fc *FontCache, reply *wm.OpenFontReplyMsg) *sharedFace {
	cacheBase := uintptr(reply.CacheAddr)
	header := (*CacheHeader)(unsafe.Pointer(cacheBase))

	// Build a slice over the glyph map in shared memory.
	mapPtr := cacheBase + uintptr(header.MapOffset)
	mapEntries := unsafe.Slice((*GlyphMapEntry)(unsafe.Pointer(mapPtr)), header.NumGlyphs)

	f := &sharedFace{
		fc:     fc,
		fontID: reply.FontID,
		header: header,
		cache:  cacheBase,
		metrics: font.Metrics{
			Height:    fixed.Int26_6(reply.Height),
			Ascent:    fixed.Int26_6(reply.Ascent),
			Descent:   fixed.Int26_6(reply.Descent),
			XHeight:   fixed.Int26_6(header.XHeight),
			CapHeight: fixed.Int26_6(header.CapHeight),
		},
		mapEntries:  mapEntries,
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
	// Tier 1: binary search the shared cache map.
	if idx := f.binarySearch(r); idx >= 0 {
		entry := &f.mapEntries[idx]
		ge := (*GlyphEntry)(unsafe.Pointer(f.cache + uintptr(entry.Offset)))
		w := int(ge.Width)
		h := int(ge.Height)
		var alpha []byte
		if w > 0 && h > 0 {
			alphaPtr := f.cache + uintptr(entry.Offset) + GlyphEntrySize
			alpha = unsafe.Slice((*byte)(unsafe.Pointer(alphaPtr)), w*h)
		}
		return ge, alpha
	}

	// Tier 2: check local cache.
	if lg, ok := f.localGlyphs[r]; ok {
		return &lg.entry, lg.alpha
	}

	// Tier 2 miss: request from fontsvc.
	reply := f.fc.requestGlyph(f.fontID, r)
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

// binarySearch finds r in the sorted glyph map. Returns index or -1.
func (f *sharedFace) binarySearch(r rune) int {
	cp := uint32(r)
	lo, hi := 0, len(f.mapEntries)-1
	for lo <= hi {
		mid := lo + (hi-lo)/2
		v := f.mapEntries[mid].Codepoint
		if v == cp {
			return mid
		}
		if v < cp {
			lo = mid + 1
		} else {
			hi = mid - 1
		}
	}
	return -1
}
