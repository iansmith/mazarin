package mancini

import (
	"image"
	"image/color"
	"math"

	"github.com/fogleman/gg"
)

// Clock is a leaf interactor that draws an analog clock face with hands.
// It receives the current local time via TimeFunc and renders hour, minute,
// and second hands plus 12 hour markers within its allocated area.
// Every Draw call clears the face and redraws everything — this is the
// simplest way to erase moved hands, and at ~300µs per frame it's free.
type Clock struct {
	Theme    *Theme
	Name     string
	Size     float64                                    // preferred side length (logical pixels)
	TimeFunc func() (hour, minute, second, millis int)  // returns local time components
	Color    color.NRGBA                                // hand/marker color
	Face     color.NRGBA                                // face fill (zero = theme Surface)
	Layout   *LayoutHandles
}

func (c *Clock) GetLayout() *LayoutHandles { return c.Layout }

// PreferredWidth returns the clock's preferred side length.
func (c *Clock) PreferredWidth() float64 {
	s := c.Size
	if s <= 0 {
		s = 50
	}
	return s
}

// PreferredHeight returns the clock's preferred side length.
func (c *Clock) PreferredHeight() float64 { return c.PreferredWidth() }

// Draw implements the Drawer interface.
func (c *Clock) Draw(canvas *image.RGBA, x, y, w, h float64) {
	pw := c.PreferredWidth()
	publishLayout(c.Layout, x, y, pw, pw)

	dc := gg.NewContextForRGBA(canvas)

	cx := x + w/2
	cy := y + h/2
	rad := math.Min(w, h) / 2

	// Clear the clock face — erases previous hands.
	face := c.Face
	if face == (color.NRGBA{}) && c.Theme != nil {
		face = c.Theme.Pal.Surface
	}
	if c.Theme != nil {
		face = c.Theme.C(face)
	}
	dc.SetColor(face)
	dc.DrawCircle(cx, cy, rad)
	dc.Fill()

	col := c.Color
	if col == (color.NRGBA{}) {
		col = color.NRGBA{0, 0, 0, 255}
	}
	if c.Theme != nil {
		col = c.Theme.C(col)
	}

	// Hour markers: 12 dots at the perimeter — outer edge flush with face edge.
	dotR := math.Max(2, rad*0.07)
	markerRad := rad - dotR
	for i := 0; i < 12; i++ {
		angle := float64(i)*math.Pi/6 - math.Pi/2
		mx := cx + markerRad*math.Cos(angle)
		my := cy + markerRad*math.Sin(angle)
		dc.SetColor(col)
		dc.DrawCircle(mx, my, dotR)
		dc.Fill()
	}

	// Get time.
	hour, minute, second, millis := 0, 0, 0, 0
	if c.TimeFunc != nil {
		hour, minute, second, millis = c.TimeFunc()
	}

	// Convert to fractional seconds for smooth cascading motion.
	// All hands move continuously — no snapping to discrete positions.
	fracSec := float64(second) + float64(millis)/1000.0 // 0..59.999
	fracMin := float64(minute) + fracSec/60.0           // 0..59.999
	fracHour := float64(hour%12) + fracMin/60.0          // 0..11.999

	// Hour hand (drawn first — underneath minute and second).
	hourAngle := fracHour * math.Pi / 6 - math.Pi/2
	hourLen := rad * 0.48
	hourW := math.Max(2, rad*0.08)
	drawHand(dc, cx, cy, hourAngle, hourLen, hourW, col)

	// Minute hand.
	minAngle := fracMin * math.Pi / 30 - math.Pi/2
	minLen := rad * 0.70
	minW := math.Max(1.5, rad*0.05)
	drawHand(dc, cx, cy, minAngle, minLen, minW, col)

	// Second hand.
	secAngle := fracSec * math.Pi / 30 - math.Pi/2
	secLen := rad * 0.78
	secW := math.Max(1, rad*0.02)
	secColor := color.NRGBA{200, 60, 60, 255}
	if c.Theme != nil {
		secColor = c.Theme.C(secColor)
	}
	drawHand(dc, cx, cy, secAngle, secLen, secW, secColor)

	// Center dot.
	dc.SetColor(col)
	dc.DrawCircle(cx, cy, math.Max(2, rad*0.06))
	dc.Fill()
}

// drawHand draws a single clock hand as a line from center.
func drawHand(dc *gg.Context, cx, cy, angle, length, width float64, col color.NRGBA) {
	ex := cx + length*math.Cos(angle)
	ey := cy + length*math.Sin(angle)
	dc.SetColor(col)
	dc.SetLineWidth(width)
	dc.SetLineCap(gg.LineCapRound)
	dc.DrawLine(cx, cy, ex, ey)
	dc.Stroke()
}
