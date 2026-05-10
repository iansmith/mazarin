package fontcache

import (
	"mazarin/textshape"
)

// LoadFontIndex reads and parses a CSV font index from the filesystem.
// The CSV format is: family,style,filename (one entry per line).
// Lines starting with '#' are comments.
// loadFile is the file-read function (e.g., fsclient via injection, or
// sys.LoadFile as fallback).
func LoadFontIndex(path string, loadFile func(string) ([]byte, error)) (*textshape.FontIndex, error) {
	data, loadErr := loadFile(path)
	if loadErr != nil {
		return nil, loadErr
	}
	return textshape.ParseFontIndex(data), nil
}
