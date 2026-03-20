package mancini

import (
	"fmt"
	"image/color"
	"math"
	"time"

)

// DigitFace draws an analog clock with Arabic numerals rotated to match
// their angular position around the dial. 12 is upright, 3 is rotated
// 90° clockwise, 6 is upside-down, 9 is rotated 270° clockwise, etc.
type DigitFace struct {
	HandColor color.NRGBA    // hand and digit color (zero = black)
	FillColor color.NRGBA    // face fill color (zero = theme Surface)
	Loc       *time.Location // timezone
}

// Location returns the timezone. Falls back to UTC if nil.
func (f *DigitFace) Location() *time.Location {
	if f.Loc == nil {
		return time.UTC
	}
	return f.Loc
}

func (f *DigitFace) FaceName() string { return "Digital" }

// DrawFace renders the digit clock face with rotated numerals.
func (f *DigitFace) DrawFace(dc DrawContext, fc *FontConfig, pal Palette, cx, cy, radius float64, hour, minute, second, millis int) {
	// Clear the clock face.
	face := f.FillColor
	if face == (color.NRGBA{}) {
		face = pal.Surface
	}
	dc.SetColor(face)
	dc.DrawCircle(cx, cy, radius)
	dc.Fill()

	col := f.HandColor
	if col == (color.NRGBA{}) {
		col = color.NRGBA{0, 0, 0, 255}
	}

	// Rotated digit markers at each hour position.
	// Each digit is rotated by its angular position: 12→0°, 1→30°, 2→60°, ...
	fontSize := math.Max(6, radius*0.20)
	loadFont(fc, dc, true, fontSize)
	dc.SetColor(col)
	markerRad := radius * 0.80
	for i := 1; i <= 12; i++ {
		// Position angle (12 at top, clockwise).
		posAngle := float64(i)*math.Pi/6 - math.Pi/2
		mx := cx + markerRad*math.Cos(posAngle)
		my := cy + markerRad*math.Sin(posAngle)

		// Rotation angle: i=12 → 0, i=3 → π/2, i=6 → π, i=9 → 3π/2.
		rotAngle := float64(i%12) * math.Pi / 6

		dc.Push()
		dc.RotateAbout(rotAngle, mx, my)
		dc.DrawStringAnchored(fmt.Sprintf("%d", i), mx, my, 0.5, 0.5)
		dc.Pop()
	}

	// Fractional seconds for smooth cascading motion.
	fracSec := float64(second) + float64(millis)/1000.0
	fracMin := float64(minute) + fracSec/60.0
	fracHour := float64(hour%12) + fracMin/60.0

	// Hour hand.
	hourAngle := fracHour*math.Pi/6 - math.Pi/2
	hourLen := radius * 0.45
	hourW := math.Max(2, radius*0.08)
	DrawHand(dc, cx, cy, hourAngle, hourLen, hourW, col)

	// Minute hand.
	minAngle := fracMin*math.Pi/30 - math.Pi/2
	minLen := radius * 0.65
	minW := math.Max(1.5, radius*0.05)
	DrawHand(dc, cx, cy, minAngle, minLen, minW, col)

	// Second hand.
	secAngle := fracSec*math.Pi/30 - math.Pi/2
	secLen := radius * 0.72
	secW := math.Max(1, radius*0.02)
	secColor := color.NRGBA{200, 60, 60, 255}
	DrawHand(dc, cx, cy, secAngle, secLen, secW, secColor)

	// Center dot.
	dc.SetColor(col)
	dc.DrawCircle(cx, cy, math.Max(2, radius*0.06))
	dc.Fill()
}
