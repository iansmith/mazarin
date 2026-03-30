package font

import (
	"sort"
	"unsafe"

	goFont "github.com/go-text/typesetting/font"
)

// buildGlyphCache populates the cache buffer with header, glyph map, and
// glyph data. Returns the number of glyphs rendered. The binary layout
// matches flock/cmd/fontsvc/main.go exactly.
func buildGlyphCache(cache []byte, face *goFont.Face, scale float32,
	fontID uint32, pointSize int32) uint32 {

	// Phase 1: Enumerate all codepoints the font supports.
	type cpEntry struct {
		cp  rune
		gid goFont.GID
		adv float32 // advance in font units
	}
	var supported []cpEntry

	// Scan BMP (U+0020 to U+FFFF).
	for cp := rune(0x0020); cp <= 0xFFFF; cp++ {
		gid, ok := face.NominalGlyph(cp)
		if !ok {
			continue
		}
		adv := face.HorizontalAdvance(gid)
		if adv > 0 {
			supported = append(supported, cpEntry{cp: cp, gid: gid, adv: adv})
		}
	}
	sort.Slice(supported, func(i, j int) bool {
		return supported[i].cp < supported[j].cp
	})

	// Phase 2: Calculate layout.
	// Header(64) + MapEntries(8 each) + GlyphData.
	mapEntrySize := uint32(8)
	maxGlyphs := uint32(len(supported))

	mapOffset := uint32(headerSize)
	dataOffset := mapOffset + maxGlyphs*mapEntrySize

	// Phase 3: Render glyphs, packing into cache.
	dataPos := dataOffset
	var rendered uint32

	// Temporary map entries (written after we know how many fit).
	mapBuf := make([]glyphMapEntry, 0, len(supported))

	for _, s := range supported {
		info, alpha := renderGlyph(face, s.gid, scale)
		if info == nil {
			continue
		}

		w := info.Width
		h := info.Height
		entrySize := glyphTotalSize(w, h)

		// Check if this glyph fits.
		if dataPos+entrySize > cacheSizeBytes {
			break // cache full
		}

		// Write glyphEntry header.
		ge := (*glyphEntry)(unsafe.Pointer(&cache[dataPos]))
		ge.Advance = info.Advance
		ge.DrMinX = info.DrMinX
		ge.DrMinY = info.DrMinY
		ge.DrMaxX = info.DrMaxX
		ge.DrMaxY = info.DrMaxY
		ge.Width = w
		ge.Height = h

		// Copy alpha pixels.
		if w > 0 && h > 0 && len(alpha) > 0 {
			alphaOff := dataPos + glyphEntrySize
			copy(cache[alphaOff:], alpha[:uint32(w)*uint32(h)])
		}

		mapBuf = append(mapBuf, glyphMapEntry{
			Codepoint: uint32(s.cp),
			Offset:    dataPos,
		})

		dataPos += entrySize
		rendered++
	}

	// Now we know the actual glyph count. Recompute layout if map is smaller.
	actualMapSize := uint32(len(mapBuf)) * mapEntrySize
	if actualMapSize < maxGlyphs*mapEntrySize {
		// Shift glyph data forward to close the gap.
		newDataOffset := mapOffset + actualMapSize
		gap := dataOffset - newDataOffset
		if gap > 0 {
			// Shift all glyph data backward.
			copy(cache[newDataOffset:], cache[dataOffset:dataPos])
			dataPos -= gap
			dataOffset = newDataOffset
			// Adjust all offsets in mapBuf.
			for i := range mapBuf {
				mapBuf[i].Offset -= gap
			}
		}
	}

	// Write glyph map.
	for i, entry := range mapBuf {
		me := (*glyphMapEntry)(unsafe.Pointer(&cache[mapOffset+uint32(i)*mapEntrySize]))
		me.Codepoint = entry.Codepoint
		me.Offset = entry.Offset
	}

	// Compute font-level metrics from go-text face.
	ext, _ := face.FontHExtents()
	height := int32((ext.Ascender - ext.Descender + ext.LineGap) * scale * 64)
	ascent := int32(ext.Ascender * scale * 64)
	descent := int32(-ext.Descender * scale * 64) // positive value
	xHeight := int32(face.LineMetric(goFont.XHeight) * scale * 64)
	capHeight := int32(face.LineMetric(goFont.CapHeight) * scale * 64)

	// Write header.
	hdr := (*cacheHeader)(unsafe.Pointer(&cache[0]))
	hdr.Magic = cacheMagic
	hdr.Version = cacheVersion
	hdr.PointSize = pointSize
	hdr.FontID = fontID
	hdr.Height = height
	hdr.Ascent = ascent
	hdr.Descent = descent
	hdr.XHeight = xHeight
	hdr.CapHeight = capHeight
	hdr.NumGlyphs = rendered
	hdr.MapOffset = mapOffset
	hdr.DataOffset = dataOffset
	hdr.TotalUsed = dataPos

	return rendered
}
