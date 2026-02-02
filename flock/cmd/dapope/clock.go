package main

import (
	_ "embed"
	"image"
	"mazzy/mazarin/sys"
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
	// Region in framebuffer coordinates
	x, y, w, h int
}

func newClockRenderer(fb *sys.FramebufferInfo) *clockRenderer {
	otFont, err := opentype.Parse(clockFontData)
	if err != nil {
		return nil
	}
	face, err := opentype.NewFace(otFont, &opentype.FaceOptions{
		Size:    clockFontSize,
		DPI:     72,
		Hinting: font.HintingFull,
	})
	if err != nil {
		return nil
	}

	adv, _ := face.GlyphAdvance('M')
	charW := adv.Ceil()
	metrics := face.Metrics()
	ascent := metrics.Ascent.Ceil()

	return &clockRenderer{
		fb:     fb,
		face:   face,
		charW:  charW,
		ascent: ascent,
	}
}

// Update renders the date/time string in the top-right corner.
func (c *clockRenderer) Update(ts sys.TimeSpec) {
	if c == nil || c.face == nil {
		return
	}

	s := formatDateTime(ts.Seconds, ts.Nanoseconds)

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

	// Fill with powder gray background (matches kernel surface color)
	dc.SetRGB(224.0/255.0, 224.0/255.0, 230.0/255.0)
	dc.Clear()

	// Draw text in dark color for contrast
	dc.SetFontFace(c.face)
	dc.SetRGB(0.2, 0.2, 0.25)
	dc.DrawString(s, float64(padX), float64(padY+c.ascent))

	// Blit back to framebuffer
	c.blitToFramebuffer(dc.Image().(*image.RGBA), rx, ry)
	sys.FlushFramebuffer(uint32(rx), uint32(ry), uint32(regionW), uint32(regionH))
}

// blitToFramebuffer copies an RGBA image to the BGRA framebuffer at (dx, dy).
func (c *clockRenderer) blitToFramebuffer(img *image.RGBA, dx, dy int) {
	fbW := int(c.fb.Width)
	fbH := int(c.fb.Height)
	pitch := int(c.fb.Pitch)
	dst := unsafe.Slice((*uint8)(unsafe.Pointer(c.fb.Addr)), pitch*fbH)

	bounds := img.Bounds()
	for y := 0; y < bounds.Dy(); y++ {
		fy := dy + y
		if fy < 0 || fy >= fbH {
			continue
		}
		for x := 0; x < bounds.Dx(); x++ {
			fx := dx + x
			if fx < 0 || fx >= fbW {
				continue
			}
			soff := y*img.Stride + x*4
			doff := fy*pitch + fx*4
			r := img.Pix[soff+0]
			g := img.Pix[soff+1]
			b := img.Pix[soff+2]
			// RGBA -> BGRA
			dst[doff+0] = b
			dst[doff+1] = g
			dst[doff+2] = r
			dst[doff+3] = 0x00
		}
	}
}

// formatDateTime converts Unix seconds to "Sun Feb 1 19:20:00"
func formatDateTime(sec, _ uint64) string {
	days := int64(sec) / 86400
	timeOfDay := int64(sec) % 86400
	if timeOfDay < 0 {
		timeOfDay += 86400
		days--
	}

	hours := timeOfDay / 3600
	minutes := (timeOfDay % 3600) / 60
	seconds := timeOfDay % 60

	// Day of week: Jan 1, 1970 was Thursday (4)
	dow := (days + 4) % 7
	if dow < 0 {
		dow += 7
	}
	dayNames := [7]string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}
	monthNames := [12]string{"Jan", "Feb", "Mar", "Apr", "May", "Jun",
		"Jul", "Aug", "Sep", "Oct", "Nov", "Dec"}

	_, month, day := daysToDate(days)

	// "Sun Feb 1 19:20:00 UTC"
	var buf [24]byte
	i := 0
	copy(buf[i:], dayNames[dow])
	i += 3
	buf[i] = ' '
	i++
	copy(buf[i:], monthNames[month-1])
	i += 3
	buf[i] = ' '
	i++
	if day >= 10 {
		buf[i] = byte('0' + day/10)
		i++
	}
	buf[i] = byte('0' + day%10)
	i++
	buf[i] = ' '
	i++
	i += writeInt2(buf[:], i, int(hours))
	buf[i] = ':'
	i++
	i += writeInt2(buf[:], i, int(minutes))
	buf[i] = ':'
	i++
	i += writeInt2(buf[:], i, int(seconds))
	buf[i] = ' '
	i++
	copy(buf[i:], "UTC")
	i += 3
	return string(buf[:i])
}

func writeInt2(buf []byte, off int, v int) int {
	buf[off] = byte('0' + v/10)
	buf[off+1] = byte('0' + v%10)
	return 2
}

func writeInt4(buf []byte, off int, v int) int {
	buf[off] = byte('0' + v/1000)
	buf[off+1] = byte('0' + (v/100)%10)
	buf[off+2] = byte('0' + (v/10)%10)
	buf[off+3] = byte('0' + v%10)
	return 4
}

// daysToDate converts days since Unix epoch to (year, month, day).
func daysToDate(days int64) (year, month, day int) {
	// Howard Hinnant's civil_from_days
	z := days + 719468
	era := z / 146097
	if z < 0 {
		era = (z - 146096) / 146097
	}
	doe := z - era*146097
	yoe := (doe - doe/1460 + doe/36524 - doe/146096) / 365
	y := yoe + era*400
	doy := doe - (365*yoe + yoe/4 - yoe/100)
	mp := (5*doy + 2) / 153
	d := doy - (153*mp+2)/5 + 1
	var m int64
	if mp < 10 {
		m = mp + 3
	} else {
		m = mp - 9
	}
	if m <= 2 {
		y++
	}
	return int(y), int(m), int(d)
}
