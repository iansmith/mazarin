package textshape

import "strings"

// FontIndexEntry maps a (family, style) pair to a font filename on disk.
type FontIndexEntry struct {
	Family   string
	Style    string
	Filename string
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

// ParseFontIndex parses CSV data into a FontIndex.
// The CSV format is: family,style,filename (one entry per line).
// Lines starting with '#' are comments. Empty lines are skipped.
func ParseFontIndex(data []byte) *FontIndex {
	idx := &FontIndex{}
	start := 0
	for i := 0; i <= len(data); i++ {
		if i == len(data) || data[i] == '\n' {
			line := string(data[start:i])
			start = i + 1
			// Strip trailing CR.
			if len(line) > 0 && line[len(line)-1] == '\r' {
				line = line[:len(line)-1]
			}
			// Skip empty lines and comments.
			if len(line) == 0 || line[0] == '#' {
				continue
			}
			// Parse: family,style,filename
			c1 := strings.IndexByte(line, ',')
			if c1 < 0 {
				continue
			}
			c2 := strings.IndexByte(line[c1+1:], ',')
			if c2 < 0 {
				continue
			}
			c2 += c1 + 1
			idx.entries = append(idx.entries, FontIndexEntry{
				Family:   line[:c1],
				Style:    line[c1+1 : c2],
				Filename: line[c2+1:],
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
