package mancini

import (
	"image/color"
	"math"
	"time"

)

// romanNumerals maps hour position (1-12) to its Roman numeral string.
var romanNumerals = [13]string{
	0: "", 1: "I", 2: "II", 3: "III", 4: "IV", 5: "V",
	6: "VI", 7: "VII", 8: "VIII", 9: "IX", 10: "X",
	11: "XI", 12: "XII",
}

// RomanFace draws an analog clock with Roman numeral hour markers.
type RomanFace struct {
	HandColor color.NRGBA    // hand and numeral color (zero = black)
	FillColor color.NRGBA    // face fill color (zero = theme Surface)
	Loc       *time.Location // timezone
}

// Location returns the timezone. Falls back to UTC if nil.
func (f *RomanFace) Location() *time.Location {
	if f.Loc == nil {
		return time.UTC
	}
	return f.Loc
}

func (f *RomanFace) FaceName() string { return "Roman" }

// DrawFace renders the Roman numeral clock face.
func (f *RomanFace) DrawFace(dc DrawContext, fc *FontConfig, pal Palette, cx, cy, radius float64, hour, minute, second, millis int) {
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

	// Roman numeral markers at each hour position.
	fontSize := int64(math.Max(6, radius*0.18))
	fontID := openFont(fc, dc, Bold, fontSize)
	dc.SetColor(col)
	markerRad := radius * 0.82
	for i := 1; i <= 12; i++ {
		angle := float64(i)*math.Pi/6 - math.Pi/2
		mx := cx + markerRad*math.Cos(angle)
		my := cy + markerRad*math.Sin(angle)
		dc.DrawStringAnchored(romanNumerals[i], fontID, mx, my, 0.5, 0.5)
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
