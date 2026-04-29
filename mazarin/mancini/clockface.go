package mancini

import (
	"image/color"
	"math"
	"mazarin/textshape"
	"time"
)

// ClockFace defines how an analog clock renders and what timezone it displays.
// Implementations control the visual appearance of the face, markers, hands,
// and center. The Clock interactor handles layout and time conversion.
type ClockFace interface {
	// DrawFace renders the complete clock within a circle centered at (cx, cy)
	// with the given radius. Time components are already converted to the
	// face's local timezone by the Clock interactor.
	DrawFace(dc DrawContext, fc *FontConfig, pal Palette, cx, cy, radius float64, hour, minute, second, millis int)

	// Location returns the timezone for this clock face.
	Location() *time.Location

	// FaceName returns a short display name for this face style.
	FaceName() string
}

// DrawHand draws a single clock hand as a line from center.
// Exported as a convenience for ClockFace implementations.
func DrawHand(dc DrawContext, cx, cy, angle, length, width float64, col color.NRGBA) {
	if length < 0.5 || width < 0.5 {
		return // degenerate hand — avoid rasterizer divide-by-zero
	}
	ex := cx + length*math.Cos(angle)
	ey := cy + length*math.Sin(angle)
	dc.SetColor(col)
	dc.SetLineWidth(width)
	dc.SetLineCap(textshape.LineCapRound)
	dc.DrawLine(cx, cy, ex, ey)
	dc.Stroke()
}

// ClassicFace draws a traditional analog clock with dot hour markers,
// three hands (hour, minute, second), and a center dot.
type ClassicFace struct {
	HandColor color.NRGBA    // hand and marker color (zero = black)
	FillColor color.NRGBA    // face fill color (zero = theme Surface)
	Loc       *time.Location // timezone
}

// Location returns the timezone. Falls back to UTC if nil.
func (f *ClassicFace) Location() *time.Location {
	if f.Loc == nil {
		return time.UTC
	}
	return f.Loc
}

// FaceName returns the short display name "Classic".
func (f *ClassicFace) FaceName() string { return "Classic" }

// DrawFace renders the classic clock face.
func (f *ClassicFace) DrawFace(dc DrawContext, fc *FontConfig, pal Palette, cx, cy, radius float64, hour, minute, second, millis int) {
	// Clear the clock face.
	face := f.FillColor
	if face == (color.NRGBA{}) {
		face = pal.Surface()
	}
	dc.SetColor(face)
	dc.DrawCircle(cx, cy, radius)
	dc.Fill()

	col := f.HandColor
	if col == (color.NRGBA{}) {
		col = color.NRGBA{0, 0, 0, 255}
	}

	// Hour markers: 12 dots at the perimeter — outer edge flush with face edge.
	dotR := math.Max(2, radius*0.07)
	markerRad := radius - dotR
	for i := 0; i < 12; i++ {
		angle := float64(i)*math.Pi/6 - math.Pi/2
		mx := cx + markerRad*math.Cos(angle)
		my := cy + markerRad*math.Sin(angle)
		dc.SetColor(col)
		dc.DrawCircle(mx, my, dotR)
		dc.Fill()
	}

	// Fractional seconds for smooth cascading motion.
	fracSec := float64(second) + float64(millis)/1000.0
	fracMin := float64(minute) + fracSec/60.0
	fracHour := float64(hour%12) + fracMin/60.0

	// Hour hand (drawn first — underneath minute and second).
	hourAngle := fracHour*math.Pi/6 - math.Pi/2
	hourLen := radius * 0.48
	hourW := math.Max(2, radius*0.08)
	DrawHand(dc, cx, cy, hourAngle, hourLen, hourW, col)

	// Minute hand.
	minAngle := fracMin*math.Pi/30 - math.Pi/2
	minLen := radius * 0.70
	minW := math.Max(1.5, radius*0.05)
	DrawHand(dc, cx, cy, minAngle, minLen, minW, col)

	// Second hand.
	secAngle := fracSec*math.Pi/30 - math.Pi/2
	secLen := radius * 0.78
	secW := math.Max(1, radius*0.02)
	secColor := color.NRGBA{200, 60, 60, 255}
	DrawHand(dc, cx, cy, secAngle, secLen, secW, secColor)

	// Center dot.
	dc.SetColor(col)
	dc.DrawCircle(cx, cy, math.Max(2, radius*0.06))
	dc.Fill()
}
