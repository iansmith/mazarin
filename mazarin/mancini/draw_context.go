package mancini

import (
	"image"

	"mazarin/textshape"
)

// DrawContext is the unified drawing surface for all interactor rendering.
// It is a type alias for [textshape.DrawContext], the canonical definition
// shared between louis14 and the mazarin UI toolkit.
//
// DrawContext is propagated from parent to child during the draw pass
// via SetDC (see [impl.Interactor.SetDC]). All interactors access it
// through self.DC() in their [NewDrawer.Draw] method.
//
// [ClippedContext] wraps a DrawContext to enforce rectangular clipping
// by saving/restoring overflow pixels — used by [std.Column], [std.Row],
// and [std.AppWindow] for child overflow.
type DrawContext = textshape.DrawContext

// NewDrawContextForImage creates a DrawContext that renders onto an existing
// RGBA image using the given GlyphProvider for text rendering. On mazarin,
// the provider is backed by fontsvc IPC; on the host OS, it can be a
// DirectGlyphProvider. Application code receives the DrawContext interface
// and never interacts with the provider directly.
func NewDrawContextForImage(target *image.RGBA, provider textshape.GlyphProvider) DrawContext {
	return textshape.NewDrawContextForImage(target, provider)
}

// ClipEdge indicates which edge of the clip rect has overflow.
type ClipEdge int

const (
	ClipRight  ClipEdge = iota // Row: child overflows to the right
	ClipBottom                 // Column: child overflows downward
)

// ClippedContext wraps a DrawContext and enforces rectangular clipping on
// one edge. It saves overflow pixels before the child draws, then restores
// them in Flush(). Only the overflow side is saved — the child's shadows
// on other sides (within the parent) are left intact.
type ClippedContext struct {
	DrawContext
	canvas   *image.RGBA
	saveRect image.Rectangle
	saved    []uint8
}

// WithClip creates a ClippedContext that will erase overflow drawing past
// the given clip rectangle on the specified edge. pad is the maximum shadow
// spread the child might draw beyond the clip boundary.
//
// For ClipRight (Row): saves the strip to the right of clipX+clipW.
// For ClipBottom (Column): saves the strip below clipY+clipH.
func WithClip(dc DrawContext, clipX, clipY, clipW, clipH, pad float64, edge ClipEdge) *ClippedContext {
	canvas := dc.Image().(*image.RGBA)
	cb := canvas.Bounds()

	clip := image.Rect(int(clipX), int(clipY), int(clipX+clipW), int(clipY+clipH))
	clip = clip.Intersect(cb)

	padI := int(pad) + 2

	// Save region: only the strip on the overflow side.
	var save image.Rectangle
	switch edge {
	case ClipRight:
		save = image.Rect(
			clip.Max.X, clip.Min.Y-padI,
			clip.Max.X+padI, clip.Max.Y+padI,
		)
	case ClipBottom:
		save = image.Rect(
			clip.Min.X-padI, clip.Max.Y,
			clip.Max.X+padI, clip.Max.Y+padI,
		)
	}
	save = save.Intersect(cb)

	// Bulk-copy rows from the save region.
	sw, sh := save.Dx(), save.Dy()
	saved := make([]uint8, sw*sh*4)
	for dy := 0; dy < sh; dy++ {
		si := canvas.PixOffset(save.Min.X, save.Min.Y+dy)
		di := dy * sw * 4
		copy(saved[di:di+sw*4], canvas.Pix[si:si+sw*4])
	}

	return &ClippedContext{
		DrawContext: dc,
		canvas:      canvas,
		saveRect:    save,
		saved:       saved,
	}
}

// Flush restores the overflow pixels saved during WithClip construction.
func (c *ClippedContext) Flush() {
	sw, sh := c.saveRect.Dx(), c.saveRect.Dy()
	for dy := 0; dy < sh; dy++ {
		di := c.canvas.PixOffset(c.saveRect.Min.X, c.saveRect.Min.Y+dy)
		si := dy * sw * 4
		copy(c.canvas.Pix[di:di+sw*4], c.saved[si:si+sw*4])
	}
}

