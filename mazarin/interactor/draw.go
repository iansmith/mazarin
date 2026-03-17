package interactor

import (
	"image"
	"image/color"
	"image/draw"
	"unsafe"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"

	"mazzy/mazarin/sys"
	"mazzy/mazarin/vm"
)

// DrawContext holds framebuffer rendering state for an interactor tree.
type DrawContext struct {
	fb      *sys.FramebufferInfo
	im      *image.RGBA
	glyphs  [95][]byte // pre-rendered ASCII 32-126
	charW   int
	charH   int
	ascent  int
	regionX int
	regionY int
	regionW int
	regionH int
}

// NewDrawContext creates a draw context with framebuffer access and pre-rendered glyphs.
// fontData is the embedded OTF font bytes. Region defines the screen area owned by this priest.
func NewDrawContext(fontData []byte, regionX, regionY, regionW, regionH int) *DrawContext {
	fb, err := sys.GetFramebuffer()
	if err != nil {
		panic("interactor: GetFramebuffer failed: " + err.Error())
	}

	// Wrap framebuffer as image.RGBA.
	fbPix := unsafe.Slice((*byte)(unsafe.Pointer(fb.Addr)), int(fb.Pitch)*int(fb.Height))
	im := &image.RGBA{
		Pix:    fbPix,
		Stride: int(fb.Pitch),
		Rect:   image.Rect(0, 0, int(fb.Width), int(fb.Height)),
	}

	// Parse font.
	otFont, err := opentype.Parse(fontData)
	if err != nil {
		panic("interactor: font parse error: " + err.Error())
	}
	face, err := opentype.NewFace(otFont, &opentype.FaceOptions{
		Size:    16.0,
		DPI:     72,
		Hinting: font.HintingFull,
	})
	if err != nil {
		panic("interactor: NewFace error: " + err.Error())
	}
	adv, ok := face.GlyphAdvance('M')
	if !ok {
		panic("interactor: could not measure glyph")
	}
	charW := adv.Ceil()
	metrics := face.Metrics()
	charH := (metrics.Ascent + metrics.Descent).Ceil()
	ascent := metrics.Ascent.Ceil()

	dc := &DrawContext{
		fb:      fb,
		im:      im,
		charW:   charW,
		charH:   charH,
		ascent:  ascent,
		regionX: regionX,
		regionY: regionY,
		regionW: regionW,
		regionH: regionH,
	}

	// Pre-render glyph set.
	dc.renderGlyphs(face)
	return dc
}

// renderGlyphs pre-renders printable ASCII characters (32-126) into pixel buffers.
func (dc *DrawContext) renderGlyphs(face font.Face) {
	textColor := bgraColor(255, 255, 255, 255) // white text (default)
	bgColor := bgraColor(0, 0, 0, 0)           // transparent background

	tmpIm := image.NewRGBA(image.Rect(0, 0, dc.charW, dc.charH))
	rowBytes := dc.charW * 4
	bgU := image.NewUniform(bgColor)

	for ch := byte(32); ch <= 126; ch++ {
		// Clear to background.
		draw.Draw(tmpIm, tmpIm.Bounds(), bgU, image.Point{}, draw.Src)

		// Draw the glyph.
		d := &font.Drawer{
			Dst:  tmpIm,
			Src:  image.NewUniform(textColor),
			Face: face,
			Dot:  fixed.P(0, dc.ascent),
		}
		d.DrawString(string(ch))

		// Copy pixel data.
		buf := make([]byte, dc.charH*rowBytes)
		for y := 0; y < dc.charH; y++ {
			off := tmpIm.PixOffset(0, y)
			copy(buf[y*rowBytes:], tmpIm.Pix[off:off+rowBytes])
		}
		dc.glyphs[ch-32] = buf
	}
}

// Image returns the framebuffer as an *image.RGBA.
func (dc *DrawContext) Image() *image.RGBA {
	return dc.im
}

// DrawTree renders the interactor tree within the given clip rectangle.
func (dc *DrawContext) DrawTree(root *Interactor, clipRect [4]int32) {
	dc.drawNode(root, clipRect)
}

func (dc *DrawContext) drawNode(i *Interactor, clipRect [4]int32) {
	if !i.Visible.Get() {
		return
	}

	bv := i.Bounds.Get()
	bx0, by0, bx1, by1 := bv.AsRectangle()

	// Clip to priest's region.
	rx0, ry0 := int32(dc.regionX), int32(dc.regionY)
	rx1, ry1 := int32(dc.regionX+dc.regionW), int32(dc.regionY+dc.regionH)

	// Intersect with clip rect.
	cx0 := max32(max32(bx0, clipRect[0]), rx0)
	cy0 := max32(max32(by0, clipRect[1]), ry0)
	cx1 := min32(min32(bx1, clipRect[2]), rx1)
	cy1 := min32(min32(by1, clipRect[3]), ry1)

	if cx0 >= cx1 || cy0 >= cy1 {
		return // empty intersection
	}

	switch i.Kind {
	case KindWindow, KindCard:
		dc.fillRect(cx0, cy0, cx1, cy1, i.BgColor.Get())
	case KindLabel:
		// Fill label background with parent's bg color if we have a parent.
		if i.Parent != nil {
			dc.fillRect(cx0, cy0, cx1, cy1, i.Parent.BgColor.Get())
		}
		dc.drawText(i, bx0, by0, cx0, cy0, cx1, cy1)
	}

	// Recurse into children.
	for _, child := range i.Children {
		dc.drawNode(child, clipRect)
	}

	// Update lastPainted.
	dc.updateLastPainted(i)
}

// fillRect fills a rectangle with a solid ARGB color (packed as int64).
func (dc *DrawContext) fillRect(x0, y0, x1, y1 int32, argb int64) {
	r := uint8((argb >> 16) & 0xFF)
	g := uint8((argb >> 8) & 0xFF)
	b := uint8(argb & 0xFF)
	a := uint8((argb >> 24) & 0xFF)
	bgra := bgraColor(r, g, b, a)
	pixel := uint32(bgra.B) | uint32(bgra.G)<<8 | uint32(bgra.R)<<16 | uint32(bgra.A)<<24

	for y := int(y0); y < int(y1); y++ {
		off := dc.im.PixOffset(int(x0), y)
		for x := int(x0); x < int(x1); x++ {
			*(*uint32)(unsafe.Pointer(&dc.im.Pix[off])) = pixel
			off += 4
		}
	}
}

// drawText renders label text content using pre-rendered glyphs.
func (dc *DrawContext) drawText(i *Interactor, bx0, by0, cx0, cy0, cx1, cy1 int32) {
	content := i.Content.Get()
	if len(content) == 0 {
		return
	}

	textArgb := i.TextColor.Get()
	tr := uint8((textArgb >> 16) & 0xFF)
	tg := uint8((textArgb >> 8) & 0xFF)
	tb := uint8(textArgb & 0xFF)
	ta := uint8((textArgb >> 24) & 0xFF)

	rowBytes := dc.charW * 4
	for ci, ch := range []byte(content) {
		px := int(bx0) + ci*dc.charW
		py := int(by0)

		if ch < 32 || ch > 126 {
			continue
		}

		glyphBuf := dc.glyphs[ch-32]
		if glyphBuf == nil {
			continue
		}

		// Blit glyph with color tinting.
		for gy := 0; gy < dc.charH; gy++ {
			dy := py + gy
			if int32(dy) < cy0 || int32(dy) >= cy1 {
				continue
			}
			for gx := 0; gx < dc.charW; gx++ {
				dx := px + gx
				if int32(dx) < cx0 || int32(dx) >= cx1 {
					continue
				}
				// Read glyph alpha from the pre-rendered white-on-transparent glyph.
				goff := gy*rowBytes + gx*4
				ga := glyphBuf[goff+3] // alpha channel
				if ga == 0 {
					continue
				}
				off := dc.im.PixOffset(dx, dy)
				// Blend: output = glyph_alpha * text_color + (1-glyph_alpha) * bg
				if ga == 255 {
					// GPU is BGRA.
					dc.im.Pix[off+0] = tb
					dc.im.Pix[off+1] = tg
					dc.im.Pix[off+2] = tr
					dc.im.Pix[off+3] = ta
				} else {
					inv := 255 - uint16(ga)
					al := uint16(ga)
					dc.im.Pix[off+0] = uint8((al*uint16(tb) + inv*uint16(dc.im.Pix[off+0])) / 255)
					dc.im.Pix[off+1] = uint8((al*uint16(tg) + inv*uint16(dc.im.Pix[off+1])) / 255)
					dc.im.Pix[off+2] = uint8((al*uint16(tr) + inv*uint16(dc.im.Pix[off+2])) / 255)
					dc.im.Pix[off+3] = ta
				}
			}
		}
	}
}

// updateLastPainted sets all LP* values to current state.
func (dc *DrawContext) updateLastPainted(i *Interactor) {
	i.LPBounds.Set(i.Bounds.Get())
	i.LPVisible.Set(i.Visible.Get())
	i.LPContent.Set(i.Content.Get())
	i.LPBgColor.Set(i.BgColor.Get())
	i.LPTextColor.Set(i.TextColor.Get())
}

// Flush sends the damaged region to the GPU.
func (dc *DrawContext) Flush(x0, y0, x1, y1 int32) {
	w := x1 - x0
	h := y1 - y0
	if w <= 0 || h <= 0 {
		return
	}
	_ = sys.FlushFramebuffer(uint32(x0), uint32(y0), uint32(w), uint32(h))
}

// FlushRegion sends the entire priest region to the GPU.
func (dc *DrawContext) FlushRegion() {
	_ = sys.FlushFramebuffer(uint32(dc.regionX), uint32(dc.regionY),
		uint32(dc.regionW), uint32(dc.regionH))
}

// bgraColor creates a color.RGBA with R and B swapped for GPU's BGRA format.
func bgraColor(r, g, b, a uint8) color.RGBA {
	return color.RGBA{R: b, G: g, B: r, A: a}
}

func max32(a, b int32) int32 {
	if a > b {
		return a
	}
	return b
}

func min32(a, b int32) int32 {
	if a < b {
		return a
	}
	return b
}

// RectIsEmpty returns true if the rectangle has zero area.
func RectIsEmpty(v vm.Value) bool {
	x0, y0, x1, y1 := v.AsRectangle()
	return x0 >= x1 || y0 >= y1
}
