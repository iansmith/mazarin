package mancini

import (
	"image/color"
	"math"
	"time"

)

// MovadoFace draws a minimalist clock inspired by the Movado Museum Watch:
// black face, white hands, single dot marker at 12 o'clock.
type MovadoFace struct {
	Loc *time.Location // timezone
}

// Location returns the timezone. Falls back to UTC if nil.
func (f *MovadoFace) Location() *time.Location {
	if f.Loc == nil {
		return time.UTC
	}
	return f.Loc
}

func (f *MovadoFace) FaceName() string { return "Movado" }

// DrawFace renders the Movado-style clock face.
func (f *MovadoFace) DrawFace(dc DrawContext, theme *Theme, cx, cy, radius float64, hour, minute, second, millis int) {
	// Black face.
	faceColor := color.NRGBA{20, 20, 20, 255}
	if theme != nil {
		faceColor = theme.C(faceColor)
	}
	dc.SetColor(faceColor)
	dc.DrawCircle(cx, cy, radius)
	dc.Fill()

	white := color.NRGBA{230, 230, 230, 255}
	if theme != nil {
		white = theme.C(white)
	}

	// Single dot at 12 o'clock — the Movado signature.
	dotR := math.Max(2, radius*0.09)
	dc.SetColor(white)
	dc.DrawCircle(cx, cy-radius+dotR+radius*0.06, dotR)
	dc.Fill()

	// Fractional seconds for smooth cascading motion.
	fracSec := float64(second) + float64(millis)/1000.0
	fracMin := float64(minute) + fracSec/60.0
	fracHour := float64(hour%12) + fracMin/60.0

	// Hour hand (white, thick).
	hourAngle := fracHour*math.Pi/6 - math.Pi/2
	hourLen := radius * 0.45
	hourW := math.Max(2, radius*0.08)
	DrawHand(dc, cx, cy, hourAngle, hourLen, hourW, white)

	// Minute hand (white, medium).
	minAngle := fracMin*math.Pi/30 - math.Pi/2
	minLen := radius * 0.68
	minW := math.Max(1.5, radius*0.05)
	DrawHand(dc, cx, cy, minAngle, minLen, minW, white)

	// Second hand (red).
	secAngle := fracSec*math.Pi/30 - math.Pi/2
	secLen := radius * 0.75
	secW := math.Max(1, radius*0.02)
	secColor := color.NRGBA{200, 60, 60, 255}
	if theme != nil {
		secColor = theme.C(secColor)
	}
	DrawHand(dc, cx, cy, secAngle, secLen, secW, secColor)

	// Center dot (white).
	dc.SetColor(white)
	dc.DrawCircle(cx, cy, math.Max(2, radius*0.05))
	dc.Fill()
}
