package fontcache

import "mazarin/textshape"

// LoadFontIndex parses a CSV font index from raw bytes.
// The CSV format is: family,style,filename (one entry per line).
// Lines starting with '#' are comments.
func LoadFontIndex(data []byte) (*textshape.FontIndex, error) {
	return textshape.ParseFontIndex(data), nil
}
