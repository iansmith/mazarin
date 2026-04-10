package textshape

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// FontIndexEntry maps a (family, style) pair to a font filename on disk.
// DesignSize is the optical design size in points. 0 means scalable
// (single outline for all sizes). Non-zero means this file is the
// optical master designed for that specific point size.
type FontIndexEntry struct {
	Family     string
	Style      string
	Filename   string
	DesignSize int
}

// FontIndex holds a parsed font index (from fonts.csv) that maps
// logical (family, style) pairs to font filenames. Used by both
// DirectGlyphProvider (darwin) and fontsvc (mazarin) to resolve
// OpenFontRequest.Family + Variant to a concrete font file.
type FontIndex struct {
	entries []FontIndexEntry
}

// Resolve looks up a (family, style) pair and returns the font filename
// (e.g., "AtkinsonHyperlegible-Bold.ttf"), or "" if not found.
// The lookup is case-insensitive for both family and style.
// For fonts with optical sizes, returns the first match (use
// ResolveOptical for size-aware resolution).
func (idx *FontIndex) Resolve(family, style string) string {
	for i := range idx.entries {
		if strings.EqualFold(idx.entries[i].Family, family) &&
			strings.EqualFold(idx.entries[i].Style, style) {
			return idx.entries[i].Filename
		}
	}
	return ""
}

// ResolveVariant resolves a family name and variant constant to a filename.
func (idx *FontIndex) ResolveVariant(family string, variant int32) string {
	return idx.Resolve(family, VariantToStyle(variant))
}

// ResolveOptical resolves a (family, style, targetSize) triple to the
// best matching font filename. For scalable fonts (DesignSize==0), this
// behaves identically to Resolve. For optically-sized fonts, it picks
// the entry whose DesignSize is nearest to targetSize.
func (idx *FontIndex) ResolveOptical(family, style string, targetSize int) string {
	bestFile := ""
	bestDist := int(^uint(0) >> 1) // max int
	found := false

	for i := range idx.entries {
		if !strings.EqualFold(idx.entries[i].Family, family) ||
			!strings.EqualFold(idx.entries[i].Style, style) {
			continue
		}
		// Scalable font — return immediately.
		if idx.entries[i].DesignSize == 0 {
			return idx.entries[i].Filename
		}
		// Optical size — pick nearest.
		dist := idx.entries[i].DesignSize - targetSize
		if dist < 0 {
			dist = -dist
		}
		if !found || dist < bestDist {
			bestFile = idx.entries[i].Filename
			bestDist = dist
			found = true
		}
	}
	return bestFile
}

// ResolveVariantOptical resolves a family, variant, and target size
// to the best matching font filename.
func (idx *FontIndex) ResolveVariantOptical(family string, variant int32, targetSize int) string {
	return idx.ResolveOptical(family, VariantToStyle(variant), targetSize)
}

// ParseFontIndex parses CSV data into a FontIndex.
// The CSV format is: family,style,filename,designSize (one entry per line).
// Lines starting with '#' are comments. Empty lines are skipped.
// Malformed lines (wrong number of fields, bad designSize) are skipped
// with a warning printed to stderr.
func ParseFontIndex(data []byte) *FontIndex {
	idx := &FontIndex{}
	lineNum := 0
	start := 0
	for i := 0; i <= len(data); i++ {
		if i == len(data) || data[i] == '\n' {
			line := string(data[start:i])
			start = i + 1
			lineNum++
			// Strip trailing CR.
			if len(line) > 0 && line[len(line)-1] == '\r' {
				line = line[:len(line)-1]
			}
			// Skip empty lines and comments.
			if len(line) == 0 || line[0] == '#' {
				continue
			}
			// Parse: family,style,filename,designSize
			parts := strings.Split(line, ",")
			if len(parts) != 4 {
				fmt.Fprintf(os.Stderr, "[fontindex] line %d: expected 4 fields, got %d: %s\n",
					lineNum, len(parts), line)
				continue
			}
			designSize, err := strconv.Atoi(strings.TrimSpace(parts[3]))
			if err != nil {
				fmt.Fprintf(os.Stderr, "[fontindex] line %d: bad designSize %q: %s\n",
					lineNum, parts[3], line)
				continue
			}

			idx.entries = append(idx.entries, FontIndexEntry{
				Family:     parts[0],
				Style:      parts[1],
				Filename:   parts[2],
				DesignSize: designSize,
			})
		}
	}
	return idx
}

// IsBoldStyle returns true if the style name implies bold weight.
func IsBoldStyle(style string) bool {
	s := strings.ToLower(style)
	return s == "bold" || s == "bolditalic" || s == "semibold" ||
		s == "semibolditalic" || s == "extrabold" || s == "extrabolditalic" ||
		s == "black" || s == "blackitalic"
}
