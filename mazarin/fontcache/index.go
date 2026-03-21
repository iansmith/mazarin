package fontcache

import (
	"mazzy/mazarin/sys"
	"strings"
	"unsafe"
)

// FontIndexEntry maps a (family, style) pair to a filename on disk.
type FontIndexEntry struct {
	Family   string
	Style    string
	Filename string
}

// FontIndex holds the parsed font index from fonts.csv.
type FontIndex struct {
	entries []FontIndexEntry
}

// Resolve looks up a (family, style) pair and returns the full path
// (e.g., "/fonts/AtkinsonHyperlegible-Bold.ttf"), or "" if not found.
// The lookup is case-insensitive for family and style.
func (idx *FontIndex) Resolve(family, style string) string {
	for i := range idx.entries {
		if strings.EqualFold(idx.entries[i].Family, family) &&
			strings.EqualFold(idx.entries[i].Style, style) {
			return "/fonts/" + idx.entries[i].Filename
		}
	}
	return ""
}

// LoadFontIndex reads and parses a CSV font index from the filesystem.
// The CSV format is: family,style,filename (one entry per line).
// Lines starting with '#' are comments.
func LoadFontIndex(path string) (*FontIndex, error) {
	result, loadErr := sys.LoadFile(path)
	if loadErr != nil {
		return nil, loadErr
	}
	data := unsafe.Slice((*byte)(unsafe.Pointer(uintptr(result.StartVA))), result.BytesRead)
	return parseFontIndex(data), nil
}

// parseFontIndex parses CSV data into a FontIndex.
func parseFontIndex(data []byte) *FontIndex {
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
