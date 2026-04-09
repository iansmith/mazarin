package fontcache

import (
	"mazzy/mazarin/sys"
	"mazarin/textshape"
	"unsafe"
)

// LoadFontIndex reads and parses a CSV font index from the filesystem.
// The CSV format is: family,style,filename (one entry per line).
// Lines starting with '#' are comments.
func LoadFontIndex(path string) (*textshape.FontIndex, error) {
	result, loadErr := sys.LoadFile(path)
	if loadErr != nil {
		return nil, loadErr
	}
	data := unsafe.Slice((*byte)(unsafe.Pointer(uintptr(result.StartVA))), result.BytesRead)
	return textshape.ParseFontIndex(data), nil
}
