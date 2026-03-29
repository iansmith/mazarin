package mancini

import (
	"image"
	"image/color"

	"github.com/fogleman/gg"
	"golang.org/x/image/font"
)

// DrawContext abstracts the drawing surface for all interactor rendering.
// The [github.com/fogleman/gg.Context] type satisfies this interface via
// structural typing.
//
// DrawContext is propagated from parent to child during the draw pass
// via SetDC (see [impl.Interactor.SetDC]). All interactors access it
// through self.DC() in their [NewDrawer.Draw] method.
//
// # Limitations
//
// DrawContext does not expose ClosePath, DrawArc, SetFillRuleEvenOdd,
// or Clip. Interactors that need these features (e.g., [std.RadialMenu]
// for even-odd annulus fills, [std.Scrollbar] for triangle arrows) use
// temporary [github.com/fogleman/gg.Context] buffers and composite via
// [image/draw.Draw] or [image/draw.DrawMask].
//
// [ClippedContext] wraps a DrawContext to enforce rectangular clipping
// by saving/restoring overflow pixels — used by [std.Column], [std.Row],
// and [std.AppWindow] for child overflow.
type DrawContext interface {
	// Shape primitives (add to current path).
	DrawRectangle(x, y, w, h float64)
	DrawRoundedRectangle(x, y, w, h, r float64)
	DrawCircle(x, y, r float64)
	DrawLine(x1, y1, x2, y2 float64)

	// Path building.
	MoveTo(x, y float64)
	LineTo(x, y float64)

	// Path rendering.
	Fill()
	Stroke()

	// Fast-path rectangle fill using current color (bypasses rasterizer).
	FillRectangle(x, y, w, h float64)

	// Text.
	DrawString(s string, x, y float64)
	DrawStringAnchored(s string, x, y, ax, ay float64)
	MeasureString(s string) (float64, float64)

	// Graphics state.
	SetColor(c color.Color)
	SetFillStyle(pattern gg.Pattern)
	SetLineWidth(lineWidth float64)
	SetLineCap(lineCap gg.LineCap)
	SetFontFace(fontFace font.Face)
	LoadFontFace(path string, points int64) error

	// Transform stack and clipping.
	Push()
	Pop()
	Rotate(angle float64)
	RotateAbout(angle, x, y float64)

	// Canvas access.
	Image() image.Image
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

