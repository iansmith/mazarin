package mancini

import (
	"image"
	"unsafe"

	"mazzy/mazarin/sys"
)

// FramebufferContext wraps the GPU framebuffer as an *image.RGBA and
// provides Flush for sending dirty regions to the GPU. This is the
// minimal bridge between mancini rendering (via *gg.Context) and the
// hardware framebuffer.
type FramebufferContext struct {
	im *image.RGBA
}

// NewFramebufferContext maps the GPU framebuffer and returns a context
// for rendering into it. The returned Image() can be passed to
// gg.NewContextForRGBA for neumorphic rendering.
func NewFramebufferContext() *FramebufferContext {
	fb, err := sys.GetFramebuffer()
	if err != nil {
		panic("mancini: GetFramebuffer failed: " + err.Error())
	}

	fbPix := unsafe.Slice((*byte)(unsafe.Pointer(fb.Addr)), int(fb.Pitch)*int(fb.Height))
	im := &image.RGBA{
		Pix:    fbPix,
		Stride: int(fb.Pitch),
		Rect:   image.Rect(0, 0, int(fb.Width), int(fb.Height)),
	}

	return &FramebufferContext{im: im}
}

// Image returns the framebuffer as an *image.RGBA.
func (fc *FramebufferContext) Image() *image.RGBA {
	return fc.im
}

// Flush sends the given rectangle to the GPU.
func (fc *FramebufferContext) Flush(x0, y0, x1, y1 int32) {
	w := x1 - x0
	h := y1 - y0
	if w <= 0 || h <= 0 {
		return
	}
	_ = sys.FlushFramebuffer(uint32(x0), uint32(y0), uint32(w), uint32(h))
}
