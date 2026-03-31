package textshape

import (
	"sort"
	"unsafe"

	goFont "github.com/go-text/typesetting/font"
)

// --- V2 glyph cache binary format ---
//
// Layout:
//   [Header 64B] [Glyph data ...] [GID map] [Codepoint map]
//
// Maps are at the END because the glyph count is unknown until
// rendering completes.

const (
	cacheV2Magic      = 0x47435632 // "GCV2"
	cacheV2Version    = 2
	MaxCacheSize      = 4 * 1024 * 1024 // 4MB cap per font
	maxCacheSize      = MaxCacheSize
	cachePageSize     = 16384           // 16KB (Apple Silicon page size)
	cacheHeaderSize   = 64
	glyphEntrySize    = 16
	gidMapEntrySize   = 12
	cpMapEntrySize    = 12
)

// glyphCacheHeader is the 64-byte header at offset 0 of a V2 glyph cache.
type glyphCacheHeader struct {
	Magic     uint32
	Version   uint32
	PointSize int32
	FontID    uint32
	NumGlyphs uint32 // unique GIDs rendered in tier-1
	NumCPMap  uint32 // codepoint map entry count
	GIDMapOff uint32 // byte offset to GID map
	CPMapOff  uint32 // byte offset to codepoint map
	TotalSize uint32 // total bytes used (page-aligned)
	Height    int32  // font metrics (fixed.Int26_6)
	Ascent    int32
	Descent   int32
	_         [12]byte // padding to 64
}

// gidMapEntry maps a GID to its glyph data offset. Sorted by GID.
type gidMapEntry struct {
	GID        uint32
	DataOffset uint32
	Codepoint  uint32 // first codepoint mapping to this GID
}

// cpMapEntry maps a codepoint to its GID and data offset. Sorted by Codepoint.
type cpMapEntry struct {
	Codepoint  uint32
	GID        uint32
	DataOffset uint32
}

// glyphEntry is the 16-byte per-glyph header in the cache, followed
// by Width*Height bytes of alpha pixels (4-byte aligned).
type glyphEntry struct {
	Advance int32
	DrMinX  int16
	DrMinY  int16
	DrMaxX  int16
	DrMaxY  int16
	Width   uint16
	Height  uint16
}

// glyphTotalSize returns bytes for a glyph entry (header + pixels),
// padded to 4-byte alignment.
func glyphTotalSize(w, h uint16) uint32 {
	raw := uint32(glyphEntrySize) + uint32(w)*uint32(h)
	return (raw + 3) &^ 3
}

// codepoint-GID pair used during cache building.
type cpGIDPair struct {
	cp  uint32
	gid uint32
}

// buildGlyphCache pre-renders all enumerable glyphs for a font at the
// given scale and packs them into a V2 binary cache. Returns the
// page-aligned cache bytes.
func buildGlyphCache(face *goFont.Face, scale float32, fontID uint32, pointSize int32, metrics FontMetrics) []byte {
	return buildGlyphCacheCore(nil, face, scale, fontID, pointSize, metrics)
}

// BuildGlyphCacheInto is like buildGlyphCache but writes into a pre-allocated
// buffer (e.g., kernel-allocated pages). The buffer must be at least
// MaxCacheSize bytes. Returns the sub-slice actually used (page-aligned).
func BuildGlyphCacheInto(buf []byte, face *goFont.Face, scale float32, fontID uint32, pointSize int32, metrics FontMetrics) []byte {
	return buildGlyphCacheCore(buf, face, scale, fontID, pointSize, metrics)
}

func buildGlyphCacheCore(buf []byte, face *goFont.Face, scale float32, fontID uint32, pointSize int32, metrics FontMetrics) []byte {
	// Phase 1: Enumerate codepoints → GIDs.
	var cpPairs []cpGIDPair
	gidSeen := make(map[uint32]bool)
	gidFirstCP := make(map[uint32]uint32)

	for cp := rune(0x0020); cp <= 0xFFFF; cp++ {
		gid, ok := face.NominalGlyph(cp)
		if !ok || gid == 0 {
			continue
		}
		if face.HorizontalAdvance(gid) <= 0 {
			continue
		}
		g := uint32(gid)
		cpPairs = append(cpPairs, cpGIDPair{cp: uint32(cp), gid: g})
		if !gidSeen[g] {
			gidSeen[g] = true
			gidFirstCP[g] = uint32(cp)
		}
	}

	// Sort codepoint pairs by codepoint (already in order from scan, but be explicit).
	sort.Slice(cpPairs, func(i, j int) bool {
		return cpPairs[i].cp < cpPairs[j].cp
	})

	// Build sorted unique GID list.
	uniqueGIDs := make([]uint32, 0, len(gidSeen))
	for g := range gidSeen {
		uniqueGIDs = append(uniqueGIDs, g)
	}
	sort.Slice(uniqueGIDs, func(i, j int) bool {
		return uniqueGIDs[i] < uniqueGIDs[j]
	})

	// Phase 2: Compute capacity.
	maxGIDMapSize := uint32(len(uniqueGIDs)) * gidMapEntrySize
	maxCPMapSize := uint32(len(cpPairs)) * cpMapEntrySize
	dataCapacity := uint32(maxCacheSize) - cacheHeaderSize - maxGIDMapSize - maxCPMapSize
	// dataCapacity can underflow if the font has too many glyphs for 4MB
	// of maps alone. In that case we'll render zero glyphs.
	if dataCapacity > maxCacheSize {
		dataCapacity = 0
	}

	// Phase 3: Render glyphs.
	if buf == nil {
		buf = make([]byte, maxCacheSize)
	}
	dataPos := uint32(cacheHeaderSize)
	gidDataOffset := make(map[uint32]uint32)
	var renderedGIDs []uint32

	for _, g := range uniqueGIDs {
		info, alpha := RenderGlyph(face, goFont.GID(g), scale)
		if info == nil {
			continue
		}

		var entrySize uint32
		if info.Width > 0 && info.Height > 0 {
			entrySize = glyphTotalSize(info.Width, info.Height)
		} else {
			entrySize = glyphEntrySize // space-like glyph
		}

		if dataPos+entrySize > cacheHeaderSize+dataCapacity {
			break // cache full
		}

		// Write glyphEntry header.
		ge := (*glyphEntry)(unsafe.Pointer(&buf[dataPos]))
		ge.Advance = info.Advance
		ge.DrMinX = info.DrMinX
		ge.DrMinY = info.DrMinY
		ge.DrMaxX = info.DrMaxX
		ge.DrMaxY = info.DrMaxY
		ge.Width = info.Width
		ge.Height = info.Height

		// Copy alpha pixels after header.
		if info.Width > 0 && info.Height > 0 && len(alpha) > 0 {
			copy(buf[dataPos+glyphEntrySize:], alpha)
		}

		gidDataOffset[g] = dataPos
		renderedGIDs = append(renderedGIDs, g)
		dataPos += entrySize
	}

	// Phase 4: Write maps at end.
	// GID map (sorted by GID — renderedGIDs is already sorted since
	// uniqueGIDs was sorted and we iterated in order).
	gidMapOff := dataPos
	for _, g := range renderedGIDs {
		me := (*gidMapEntry)(unsafe.Pointer(&buf[dataPos]))
		me.GID = g
		me.DataOffset = gidDataOffset[g]
		me.Codepoint = gidFirstCP[g]
		dataPos += gidMapEntrySize
	}

	// Codepoint map (sorted by codepoint, only rendered GIDs).
	cpMapOff := dataPos
	numCPMap := uint32(0)
	for _, pair := range cpPairs {
		off, ok := gidDataOffset[pair.gid]
		if !ok {
			continue // GID not rendered (overflow)
		}
		me := (*cpMapEntry)(unsafe.Pointer(&buf[dataPos]))
		me.Codepoint = pair.cp
		me.GID = pair.gid
		me.DataOffset = off
		dataPos += cpMapEntrySize
		numCPMap++
	}

	// Phase 5: Write header, trim.
	hdr := (*glyphCacheHeader)(unsafe.Pointer(&buf[0]))
	hdr.Magic = cacheV2Magic
	hdr.Version = cacheV2Version
	hdr.PointSize = pointSize
	hdr.FontID = fontID
	hdr.NumGlyphs = uint32(len(renderedGIDs))
	hdr.NumCPMap = numCPMap
	hdr.GIDMapOff = gidMapOff
	hdr.CPMapOff = cpMapOff
	hdr.Height = metrics.Height
	hdr.Ascent = metrics.Ascent
	hdr.Descent = metrics.Descent

	// Page-align total size.
	aligned := ((dataPos + cachePageSize - 1) / cachePageSize) * cachePageSize
	hdr.TotalSize = aligned

	return buf[:aligned]
}

// LookupByGID binary searches the GID map and returns the glyph info
// and alpha slice. Returns (nil, nil) on miss.
func LookupByGID(cache []byte, gid uint32) (*GlyphInfo, []byte) {
	hdr := (*glyphCacheHeader)(unsafe.Pointer(&cache[0]))
	if hdr.Magic != cacheV2Magic || hdr.NumGlyphs == 0 {
		return nil, nil
	}

	count := int(hdr.NumGlyphs)
	base := hdr.GIDMapOff

	// Binary search.
	lo, hi := 0, count-1
	for lo <= hi {
		mid := lo + (hi-lo)/2
		me := (*gidMapEntry)(unsafe.Pointer(&cache[base+uint32(mid)*gidMapEntrySize]))
		if me.GID == gid {
			return readGlyphAt(cache, me.DataOffset)
		}
		if me.GID < gid {
			lo = mid + 1
		} else {
			hi = mid - 1
		}
	}
	return nil, nil
}

// lookupByCodepoint binary searches the codepoint map. Returns
// (gid, info, alpha) or (0, nil, nil) on miss.
func lookupByCodepoint(cache []byte, cp uint32) (uint32, *GlyphInfo, []byte) {
	hdr := (*glyphCacheHeader)(unsafe.Pointer(&cache[0]))
	if hdr.Magic != cacheV2Magic || hdr.NumCPMap == 0 {
		return 0, nil, nil
	}

	count := int(hdr.NumCPMap)
	base := hdr.CPMapOff

	lo, hi := 0, count-1
	for lo <= hi {
		mid := lo + (hi-lo)/2
		me := (*cpMapEntry)(unsafe.Pointer(&cache[base+uint32(mid)*cpMapEntrySize]))
		if me.Codepoint == cp {
			info, alpha := readGlyphAt(cache, me.DataOffset)
			return me.GID, info, alpha
		}
		if me.Codepoint < cp {
			lo = mid + 1
		} else {
			hi = mid - 1
		}
	}
	return 0, nil, nil
}

// readGlyphAt reads a glyphEntry at the given offset and returns the
// GlyphInfo and alpha sub-slice (zero-copy into the cache buffer).
func readGlyphAt(cache []byte, offset uint32) (*GlyphInfo, []byte) {
	ge := (*glyphEntry)(unsafe.Pointer(&cache[offset]))
	info := &GlyphInfo{
		Advance: ge.Advance,
		DrMinX:  ge.DrMinX,
		DrMinY:  ge.DrMinY,
		DrMaxX:  ge.DrMaxX,
		DrMaxY:  ge.DrMaxY,
		Width:   ge.Width,
		Height:  ge.Height,
	}
	if ge.Width == 0 || ge.Height == 0 {
		return info, nil
	}
	alphaStart := offset + glyphEntrySize
	alphaEnd := alphaStart + uint32(ge.Width)*uint32(ge.Height)
	return info, cache[alphaStart:alphaEnd]
}
