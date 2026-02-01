// stdio is a userspace priest that owns the serial port soft IRQ and
// renders kernel console output to a dark gray rectangle on the display.
// It loads the Atkinson Hyperlegible Mono font and displays serial
// output as lines of monospaced text.
package main

import (
	_ "embed"
	"fmt"
	"image"
	"image/color"
	"unsafe"

	gg "github.com/fogleman/gg"
	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"

	"mazzy/mazarin/sys"
	"mazzy/shared/hid"
)

//go:embed AtkinsonHyperlegibleMono-Regular.otf
var fontData []byte

const (
	LineSpacing = 2
	pad         = 8
	textPad     = 8 // inner padding from rect edge to text
)

// console holds the state for the text console display.
type console struct {
	fb       *sys.FramebufferInfo
	dc       *gg.Context
	face     font.Face
	ascent   int
	charW    int
	charH    int
	lineH    int // charH + LineSpacing
	rectX    int
	rectY    int
	rectW    int
	rectH    int // adjusted to be multiple of lineH
	maxLines int
	lines    []string

	// Dirty tracking: min/max line indices that need redraw.
	// -1 means nothing dirty.
	dirtyMin int
	dirtyMax int
}

func (c *console) markDirty(lineIdx int) {
	if c.dirtyMin < 0 || lineIdx < c.dirtyMin {
		c.dirtyMin = lineIdx
	}
	if lineIdx > c.dirtyMax {
		c.dirtyMax = lineIdx
	}
}

func (c *console) clearDirty() {
	c.dirtyMin = -1
	c.dirtyMax = -1
}

func main() {
	sys.RegisterAsyncPreempt()

	fmt.Println("[stdio] Starting console priest")

	// --- Discover input devices, register for serial only ---
	devices, err := sys.QueryInputDevices()
	if err != nil {
		fmt.Printf("[stdio] QueryInputDevices failed: %v\n", err)
		return
	}

	serialSlot := -1
	for i, dev := range devices {
		if dev.DeviceType == hid.DeviceTypeSerial {
			if err := sys.RegisterSoftIRQ(dev.IRQNum, i); err != nil {
				fmt.Printf("[stdio] RegisterSoftIRQ slot %d failed: %v\n", i, err)
				continue
			}
			serialSlot = i
			fmt.Printf("[stdio] Registered serial on slot %d (IRQ %d)\n", i, dev.IRQNum)
		}
	}

	// --- Get framebuffer ---
	fb, err := sys.GetFramebuffer()
	if err != nil {
		fmt.Printf("[stdio] GetFramebuffer failed: %v\n", err)
		return
	}
	fmt.Printf("[stdio] Framebuffer: %dx%d pitch=%d addr=0x%x\n",
		fb.Width, fb.Height, fb.Pitch, fb.Addr)

	// --- Parse font ---
	otFont, err := opentype.Parse(fontData)
	if err != nil {
		fmt.Printf("[stdio] font parse error: %v\n", err)
		return
	}

	const fontSize = 16.0
	face, err := opentype.NewFace(otFont, &opentype.FaceOptions{
		Size:    fontSize,
		DPI:     72,
		Hinting: font.HintingFull,
	})
	if err != nil {
		fmt.Printf("[stdio] NewFace error: %v\n", err)
		return
	}

	adv, ok := face.GlyphAdvance('M')
	if !ok {
		fmt.Println("[stdio] could not measure glyph")
		return
	}
	charW := adv.Ceil()
	metrics := face.Metrics()
	charH := (metrics.Ascent + metrics.Descent).Ceil()
	ascent := metrics.Ascent.Ceil()
	lineH := charH + LineSpacing

	// Compute rect dimensions: top-left quarter minus padding, height rounded down to lineH multiple
	w := int(fb.Width)
	h := int(fb.Height)
	rectX := pad
	rectY := pad
	rectW := w/2 - 2*pad
	rawRectH := h/2 - 2*pad
	maxLines := rawRectH / lineH
	rectH := maxLines * lineH

	fmt.Printf("[stdio] Font: %dx%d px, lineH=%d, rect=%dx%d, maxLines=%d\n",
		charW, charH, lineH, rectW, rectH, maxLines)

	con := &console{
		fb:       fb,
		dc:       gg.NewContext(w, h),
		face:     face,
		ascent:   ascent,
		charW:    charW,
		charH:    charH,
		lineH:    lineH,
		rectX:    rectX,
		rectY:    rectY,
		rectW:    rectW,
		rectH:    rectH,
		maxLines: maxLines,
		lines:    []string{""},
		dirtyMin: -1,
		dirtyMax: -1,
	}

	// Initial draw: copy fb, paint rect, flush
	copyFramebufferToGG(con.dc, fb)
	con.drawBackground()
	flushRegionToFramebuffer(con.dc, fb, con.rectX, con.rectY, con.rectW, con.rectH)
	sys.FlushFramebuffer(uint32(con.rectX), uint32(con.rectY), uint32(con.rectW), uint32(con.rectH))
	fmt.Println("[stdio] Console rectangle rendered")

	// --- Enter serial event loop ---
	if serialSlot >= 0 {
		serialLoop(serialSlot, con)
	}

	select {}
}

// drawBackground paints the dark gray rectangle.
func (c *console) drawBackground() {
	c.dc.SetColor(color.RGBA{R: 50, G: 50, B: 50, A: 255})
	c.dc.DrawRectangle(float64(c.rectX), float64(c.rectY), float64(c.rectW), float64(c.rectH))
	c.dc.Fill()
}

// redrawLine repaints one line in the gg context (no flush).
func (c *console) redrawLine(lineIdx int) {
	if lineIdx < 0 || lineIdx >= c.maxLines || lineIdx >= len(c.lines) {
		return
	}

	lineY := c.rectY + lineIdx*c.lineH

	// Clear this line's background
	c.dc.SetColor(color.RGBA{R: 50, G: 50, B: 50, A: 255})
	c.dc.DrawRectangle(float64(c.rectX), float64(lineY), float64(c.rectW), float64(c.lineH))
	c.dc.Fill()

	// Draw the text
	text := c.lines[lineIdx]
	if len(text) > 0 {
		c.dc.SetFontFace(c.face)
		c.dc.SetColor(color.RGBA{R: 200, G: 200, B: 200, A: 255})
		textX := float64(c.rectX + textPad)
		textY := float64(lineY) + float64(c.ascent)
		c.dc.DrawString(text, textX, textY)
	}
}

// flushDirty redraws all dirty lines into the gg context, copies the
// affected pixel region to the framebuffer, and tells the kernel to
// update the display. Resets dirty state afterwards.
func (c *console) flushDirty() {
	if c.dirtyMin < 0 {
		return
	}

	// Redraw each dirty line into the gg backbuffer
	for i := c.dirtyMin; i <= c.dirtyMax; i++ {
		c.redrawLine(i)
	}

	// Compute the pixel region covering all dirty lines
	y0 := c.rectY + c.dirtyMin*c.lineH
	y1 := c.rectY + (c.dirtyMax+1)*c.lineH
	regionH := y1 - y0

	// Copy only the dirty region from gg to framebuffer
	flushRegionToFramebuffer(c.dc, c.fb, c.rectX, y0, c.rectW, regionH)

	// Tell the kernel to push that region to the display
	sys.FlushFramebuffer(uint32(c.rectX), uint32(y0), uint32(c.rectW), uint32(regionH))

	c.clearDirty()
}

// handleChar processes a single character from serial input.
// It updates lines and dirty tracking but does NOT flush.
func (c *console) handleChar(ch byte) {
	if ch == '\r' {
		return
	}
	if ch == '\n' {
		if len(c.lines) < c.maxLines {
			c.lines = append(c.lines, "")
		} else {
			// Scroll: drop first line, append empty line at end
			copy(c.lines, c.lines[1:])
			c.lines[c.maxLines-1] = ""
			// All lines need redraw after scroll
			c.markDirty(0)
			c.markDirty(c.maxLines - 1)
		}
		return
	}
	lineIdx := len(c.lines) - 1
	if lineIdx < 0 || lineIdx >= c.maxLines {
		return
	}
	c.lines[lineIdx] += string(ch)
	c.markDirty(lineIdx)
}

func fixedToInt(f fixed.Int26_6) int {
	return f.Ceil()
}

func serialLoop(slot int, con *console) {
	fmt.Printf("[stdio] serial loop started on slot %d\n", slot)
	var buf hid.SoftIRQReturn
	for {
		n, err := sys.WaitSoftIRQ(slot, &buf)
		if err != nil {
			fmt.Printf("[stdio:serial] WaitSoftIRQ error: %v\n", err)
			continue
		}
		// Process entire batch of events before flushing
		for i := 0; i < n; i++ {
			con.handleChar(byte(buf.Events[i].Value))
		}
		con.flushDirty()
	}
}

// copyFramebufferToGG reads the BGRA framebuffer into the gg RGBA backbuffer.
func copyFramebufferToGG(dc *gg.Context, fb *sys.FramebufferInfo) {
	im, ok := dc.Image().(*image.RGBA)
	if !ok {
		return
	}

	width := int(fb.Width)
	height := int(fb.Height)
	pitch := int(fb.Pitch)

	if width > im.Bounds().Dx() {
		width = im.Bounds().Dx()
	}
	if height > im.Bounds().Dy() {
		height = im.Bounds().Dy()
	}

	src := unsafe.Slice((*uint8)(unsafe.Pointer(fb.Addr)), pitch*height)
	dstPix := im.Pix
	dstStride := im.Stride

	for y := 0; y < height; y++ {
		srcRow := src[y*pitch:]
		dstRow := dstPix[y*dstStride:]
		for x := 0; x < width; x++ {
			si := x * 4
			di := x * 4
			b := srcRow[si+0]
			g := srcRow[si+1]
			r := srcRow[si+2]
			dstRow[di+0] = r
			dstRow[di+1] = g
			dstRow[di+2] = b
			dstRow[di+3] = 0xFF
		}
	}
}

// flushRegionToFramebuffer copies a rectangular region from the gg RGBA
// backbuffer to the BGRA framebuffer. Only the specified region is touched.
func flushRegionToFramebuffer(dc *gg.Context, fb *sys.FramebufferInfo, rx, ry, rw, rh int) {
	im, ok := dc.Image().(*image.RGBA)
	if !ok {
		return
	}

	fbW := int(fb.Width)
	fbH := int(fb.Height)
	pitch := int(fb.Pitch)

	// Clamp region to framebuffer and image bounds
	imW := im.Bounds().Dx()
	imH := im.Bounds().Dy()
	if rx < 0 {
		rw += rx
		rx = 0
	}
	if ry < 0 {
		rh += ry
		ry = 0
	}
	if rx+rw > fbW {
		rw = fbW - rx
	}
	if rx+rw > imW {
		rw = imW - rx
	}
	if ry+rh > fbH {
		rh = fbH - ry
	}
	if ry+rh > imH {
		rh = imH - ry
	}
	if rw <= 0 || rh <= 0 {
		return
	}

	dst := unsafe.Slice((*uint8)(unsafe.Pointer(fb.Addr)), pitch*fbH)
	srcPix := im.Pix
	srcStride := im.Stride

	for y := ry; y < ry+rh; y++ {
		srcRow := srcPix[y*srcStride:]
		dstRow := dst[y*pitch:]
		for x := rx; x < rx+rw; x++ {
			si := x * 4
			di := x * 4
			r := srcRow[si+0]
			g := srcRow[si+1]
			b := srcRow[si+2]
			dstRow[di+0] = b
			dstRow[di+1] = g
			dstRow[di+2] = r
			dstRow[di+3] = 0x00
		}
	}
}
