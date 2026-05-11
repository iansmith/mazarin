package fontcache

import (
	"mazarin/textshape"
)

// LoadFontIndex reads and parses a CSV font index via the given file-read
// function. The CSV format is: family,style,filename (one entry per line).
// Lines starting with '#' are comments.
func LoadFontIndex(path string, loadFile func(string) ([]byte, error)) (*textshape.FontIndex, error) {
	data, loadErr := loadFile(path)
	if loadErr != nil {
		return nil, loadErr
	}
	return textshape.ParseFontIndex(data), nil
}
