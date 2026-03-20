package mancini

import (
	"image"
)

// Clip stack for hierarchical clipping — legacy, retained for potential
// future use. The current approach uses ClippedContext (save/restore pixels).
var clipStack []image.Rectangle

// PushClip pushes a clip rectangle (global coords). The effective clip
// is the intersection of this rect with the parent's clip.
func PushClip(x, y, w, h float64) {
	r := image.Rect(int(x), int(y), int(x+w), int(y+h))
	if len(clipStack) > 0 {
		r = r.Intersect(clipStack[len(clipStack)-1])
	}
	clipStack = append(clipStack, r)
}

// PopClip restores the previous clip rectangle.
func PopClip() {
	if len(clipStack) > 0 {
		clipStack = clipStack[:len(clipStack)-1]
	}
}
