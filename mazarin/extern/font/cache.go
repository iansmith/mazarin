package font

import (
	"image"
	"sort"
	"unsafe"

	"golang.org/x/image/font"
	"golang.org/x/image/math/fixed"
)

// buildGlyphCache populates the cache buffer with header, glyph map, and
// glyph data. Returns the number of glyphs rendered. The algorithm and
// binary layout match flock/cmd/fontsvc/main.go exactly.
func buildGlyphCache(cache []byte, face font.Face, metrics font.Metrics,
	fontID uint32, pointSize int32) uint32 {

	// Phase 1: Enumerate all codepoints the font supports.
	type cpEntry struct {
		cp      rune
		advance fixed.Int26_6
	}
	var supported []cpEntry

	// Scan BMP (U+0020 to U+FFFF).
	for cp := rune(0x0020); cp <= 0xFFFF; cp++ {
		adv, ok := face.GlyphAdvance(cp)
		if ok && adv > 0 {
			supported = append(supported, cpEntry{cp: cp, advance: adv})
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

	dot := fixed.Point26_6{}
	for _, s := range supported {
		dr, mask, maskp, advance, ok := face.Glyph(dot, s.cp)
		if !ok {
			continue
		}

		w := uint16(dr.Dx())
		h := uint16(dr.Dy())
		entrySize := glyphTotalSize(w, h)

		// Check if this glyph fits.
		if dataPos+entrySize > cacheSizeBytes {
			break // cache full
		}

		// Write glyphEntry header.
		ge := (*glyphEntry)(unsafe.Pointer(&cache[dataPos]))
		ge.Advance = int32(advance)
		ge.DrMinX = int16(dr.Min.X)
		ge.DrMinY = int16(dr.Min.Y)
		ge.DrMaxX = int16(dr.Max.X)
		ge.DrMaxY = int16(dr.Max.Y)
		ge.Width = w
		ge.Height = h

		// Copy alpha pixels.
		if w > 0 && h > 0 && mask != nil {
			alphaOff := dataPos + glyphEntrySize
			copyAlphaPixels(cache[alphaOff:], mask, maskp, int(w), int(h))
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

	// Write header.
	hdr := (*cacheHeader)(unsafe.Pointer(&cache[0]))
	hdr.Magic = cacheMagic
	hdr.Version = cacheVersion
	hdr.PointSize = pointSize
	hdr.FontID = fontID
	hdr.Height = int32(metrics.Height)
	hdr.Ascent = int32(metrics.Ascent)
	hdr.Descent = int32(metrics.Descent)
	hdr.XHeight = int32(metrics.XHeight)
	hdr.CapHeight = int32(metrics.CapHeight)
	hdr.NumGlyphs = rendered
	hdr.MapOffset = mapOffset
	hdr.DataOffset = dataOffset
	hdr.TotalUsed = dataPos

	return rendered
}

// copyAlphaPixels copies the alpha channel from a mask image into a flat buffer.
func copyAlphaPixels(dst []byte, mask image.Image, maskp image.Point, w, h int) {
	switch m := mask.(type) {
	case *image.Alpha:
		for y := 0; y < h; y++ {
			srcOff := m.PixOffset(maskp.X, maskp.Y+y)
			dstOff := y * w
			if srcOff+w <= len(m.Pix) && dstOff+w <= len(dst) {
				copy(dst[dstOff:dstOff+w], m.Pix[srcOff:srcOff+w])
			}
		}
	default:
		// Fallback: read pixel by pixel.
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				_, _, _, a := mask.At(maskp.X+x, maskp.Y+y).RGBA()
				dst[y*w+x] = byte(a >> 8)
			}
		}
	}
}
