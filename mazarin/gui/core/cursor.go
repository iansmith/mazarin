package core

import (
	"image"
)

// Cursor represents a registered cursor with its image data.
// The zero value is invalid (id == 0) and serves as a "no cursor" sentinel.
type Cursor struct {
	id  int32
	img *image.RGBA
}

// NewCursor creates a Cursor with the given id and image.
func NewCursor(id int32, img *image.RGBA) Cursor {
	return Cursor{id: id, img: img}
}

// ID returns the cursor's unique identifier.
func (c Cursor) ID() int32 { return c.id }

// Image returns the cursor's RGBA image data.
func (c Cursor) Image() *image.RGBA { return c.img }

// IsValid returns true if this cursor has been registered.
func (c Cursor) IsValid() bool { return c.id != 0 }

// CursorImageMap manages a registry of cursor images.
type CursorImageMap interface {
	CursorImageAdd(img image.Image) Cursor
	CursorImageRemove(c Cursor) bool
	CursorImageFromFile(path string) (Cursor, error)
	CursorImageGet(c Cursor) *image.RGBA
}
