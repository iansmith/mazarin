package mancini

import (
	"image/color"
	"math"
	"time"

)

// MovadoFace draws a minimalist clock inspired by the Movado Museum Watch:
// clean face, single dot marker at 12 o'clock.
type MovadoFace struct {
	HandColor color.NRGBA    // hand and dot color (zero = black)
	FillColor color.NRGBA    // face fill color (zero = theme Surface)
	Loc       *time.Location // timezone
}

// Location returns the timezone. Falls back to UTC if nil.
func (f *MovadoFace) Location() *time.Location {
	if f.Loc == nil {
		return time.UTC
	}
	return f.Loc
}

// FaceName returns the short display name "Movado".
func (f *MovadoFace) FaceName() string { return "Movado" }

// DrawFace renders the Movado-style clock face.
func (f *MovadoFace) DrawFace(dc DrawContext, _ *FontConfig, pal Palette, cx, cy, radius float64, hour, minute, second, millis int) {
	// Face fill — use palette surface if FillColor is zero.
	face := f.FillColor
	if face == (color.NRGBA{}) {
		face = pal.Surface()
	}
	dc.SetColor(face)
	dc.DrawCircle(cx, cy, radius)
	dc.Fill()

	hand := f.HandColor
	if hand == (color.NRGBA{}) {
		hand = color.NRGBA{20, 20, 20, 255}
	}

	// Single dot at 12 o'clock — the Movado signature. Medium sized.
	dotR := math.Max(3, radius*0.07)
	dc.SetColor(hand)
	dc.DrawCircle(cx, cy-radius+dotR+radius*0.10, dotR)
	dc.Fill()

	// Fractional seconds for smooth cascading motion.
	fracSec := float64(second) + float64(millis)/1000.0
	fracMin := float64(minute) + fracSec/60.0
	fracHour := float64(hour%12) + fracMin/60.0

	// Hour hand (thick).
	hourAngle := fracHour*math.Pi/6 - math.Pi/2
	hourLen := radius * 0.45
	hourW := math.Max(2, radius*0.08)
	DrawHand(dc, cx, cy, hourAngle, hourLen, hourW, hand)

	// Minute hand (medium).
	minAngle := fracMin*math.Pi/30 - math.Pi/2
	minLen := radius * 0.68
	minW := math.Max(1.5, radius*0.05)
	DrawHand(dc, cx, cy, minAngle, minLen, minW, hand)

	// Second hand (red accent).
	secAngle := fracSec*math.Pi/30 - math.Pi/2
	secLen := radius * 0.75
	secW := math.Max(1, radius*0.02)
	secColor := color.NRGBA{200, 60, 60, 255}
	DrawHand(dc, cx, cy, secAngle, secLen, secW, secColor)

	// Center dot.
	dc.SetColor(hand)
	dc.DrawCircle(cx, cy, math.Max(2, radius*0.05))
	dc.Fill()
}
