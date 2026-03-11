package main

import (
	_ "embed"
	"fmt"
	"image"
	"mazzy/mazarin/sys"
	"os"
	"time"
	_ "time/tzdata"
	"unsafe"

	"github.com/fogleman/gg"
	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
)

//go:embed AtkinsonHyperlegibleMono-Regular.otf
var clockFontData []byte

const (
	clockFontSize = 18.0
	clockMargin   = 20 // pixels from screen edge
)

type clockRenderer struct {
	fb     *sys.FramebufferInfo
	face   font.Face
	charW  int
	ascent int
	loc    *time.Location
	// Region in framebuffer coordinates
	x, y, w, h int
}

func newClockRenderer(fb *sys.FramebufferInfo) *clockRenderer {
	otFont, err := opentype.Parse(clockFontData)
	if err != nil {
		fmt.Printf("[dapope] clock font parse failed: %v\n", err)
		return nil
	}
	face, err := opentype.NewFace(otFont, &opentype.FaceOptions{
		Size:    clockFontSize,
		DPI:     72,
		Hinting: font.HintingFull,
	})
	if err != nil {
		fmt.Printf("[dapope] clock font face failed: %v\n", err)
		return nil
	}

	adv, _ := face.GlyphAdvance('M')
	charW := adv.Ceil()
	metrics := face.Metrics()
	ascent := metrics.Ascent.Ceil()

	loc := time.UTC
	if tz := os.Getenv("TZ"); tz != "" {
		if l, err := time.LoadLocation(tz); err == nil {
			loc = l
			fmt.Printf("[dapope] timezone: %s\n", tz)
		} else {
			fmt.Printf("[dapope] timezone %q failed: %v, using UTC\n", tz, err)
		}
	}

	return &clockRenderer{
		fb:     fb,
		face:   face,
		charW:  charW,
		ascent: ascent,
		loc:    loc,
	}
}

// Update renders the date/time string in the top-right corner.
func (c *clockRenderer) Update(ts sys.TimeSpec) {
	if c == nil || c.face == nil {
		return
	}

	t := time.Unix(int64(ts.Seconds), int64(ts.Nanoseconds)).In(c.loc)
	s := t.Format("Mon Jan 2 15:04:05 MST")

	metrics := c.face.Metrics()
	textH := (metrics.Ascent + metrics.Descent).Ceil()
	textW := len(s) * c.charW

	// Add padding around text
	padX := 8
	padY := 4
	regionW := textW + 2*padX
	regionH := textH + 2*padY

	// Position: top-right
	rx := int(c.fb.Width) - regionW - clockMargin
	ry := clockMargin

	c.x = rx
	c.y = ry
	c.w = regionW
	c.h = regionH

	// Render into a small gg context filled with the background color
	dc := gg.NewContext(regionW, regionH)

	// Fill with powder gray background (matches kernel surface color).
	// GPU uses BGRA format; swap R↔B in SetRGB so gg writes correct bytes.
	// Actual display color: RGB(224, 224, 230)
	dc.SetRGB(230.0/255.0, 224.0/255.0, 224.0/255.0)
	dc.Clear()

	// Draw text in dark color for contrast.
	// Actual display color: RGB(51, 51, 64)
	dc.SetFontFace(c.face)
	dc.SetRGB(0.25, 0.2, 0.2)
	dc.DrawString(s, float64(padX), float64(padY+c.ascent))

	// Blit back to framebuffer
	c.blitToFramebuffer(dc.Image().(*image.RGBA), rx, ry)
	sys.FlushFramebuffer(uint32(rx), uint32(ry), uint32(regionW), uint32(regionH))
}

// blitToFramebuffer copies an image to the BGRA framebuffer at (dx, dy).
// Colors are pre-swapped (R↔B) in SetRGB calls, so raw byte copy is correct.
func (c *clockRenderer) blitToFramebuffer(img *image.RGBA, dx, dy int) {
	fbW := int(c.fb.Width)
	fbH := int(c.fb.Height)
	pitch := int(c.fb.Pitch)
	dst := unsafe.Slice((*uint8)(unsafe.Pointer(c.fb.Addr)), pitch*fbH)

	bounds := img.Bounds()
	imgW := bounds.Dx()
	for y := 0; y < bounds.Dy(); y++ {
		fy := dy + y
		if fy < 0 || fy >= fbH {
			continue
		}
		fx := dx
		copyW := imgW
		if fx < 0 {
			copyW += fx
			fx = 0
		}
		if fx+copyW > fbW {
			copyW = fbW - fx
		}
		if copyW <= 0 {
			continue
		}
		soff := y*img.Stride + (fx-dx)*4
		doff := fy*pitch + fx*4
		copy(dst[doff:doff+copyW*4], img.Pix[soff:soff+copyW*4])
	}
}

