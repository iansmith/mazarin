package mancini

import (
	"fmt"
	"image/color"
	"math"
	"time"
)

// PolarFace draws a 24-hour analog clock inspired by the Raketa Polar watch.
// A single hand makes one revolution per day. The dial shows 0 at top,
// 6 at 3 o'clock, 12 at bottom, 18 at 9 o'clock — with dots at all
// other hour positions.
type PolarFace struct {
	HandColor color.NRGBA    // hand and marker color (zero = black)
	FillColor color.NRGBA    // face fill color (zero = theme Surface)
	Loc       *time.Location // timezone
}

// Location returns the timezone. Falls back to UTC if nil.
func (f *PolarFace) Location() *time.Location {
	if f.Loc == nil {
		return time.UTC
	}
	return f.Loc
}

func (f *PolarFace) FaceName() string { return "Polar" }

// DrawFace renders the Raketa Polar clock face.
func (f *PolarFace) DrawFace(dc DrawContext, fc *FontConfig, pal Palette, cx, cy, radius float64, hour, minute, second, millis int) {
	// Fill the clock face.
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

	// 24 hour markers: digits at 0, 6, 12, 18; dots elsewhere.
	// Each hour is 2*pi/24 = pi/12 radians apart, 0 at top (-pi/2).
	fontSize := int64(math.Max(6, radius*0.20))
	dotR := math.Max(2, radius*0.05)
	digitRad := radius * 0.80 // radius for digit placement
	dotRad := radius - dotR   // radius for dot placement (flush with edge)

	fontID := openFont(fc, dc, Bold, fontSize)
	for i := 0; i < 24; i++ {
		posAngle := float64(i)*math.Pi/12 - math.Pi/2

		if i == 0 || i == 6 || i == 12 || i == 18 {
			// Cardinal positions: draw digit.
			dc.SetColor(col)
			mx := cx + digitRad*math.Cos(posAngle)
			my := cy + digitRad*math.Sin(posAngle)
			dc.DrawStringAnchored(fmt.Sprintf("%d", i), fontID, mx, my, 0.5, 0.5)
		} else {
			// Other positions: draw dot.
			mx := cx + dotRad*math.Cos(posAngle)
			my := cy + dotRad*math.Sin(posAngle)
			dc.SetColor(col)
			dc.DrawCircle(mx, my, dotR)
			dc.Fill()
		}
	}

	// Convert conventional 24h time to day fraction for the single hand.
	totalRealSec := float64(hour)*3600 + float64(minute)*60 + float64(second) + float64(millis)/1000.0
	dayFrac := totalRealSec / 86400.0

	// Single hand: one full revolution = 24 hours (one day).
	handAngle := dayFrac*2*math.Pi - math.Pi/2
	handLen := radius * 0.60
	handW := math.Max(2, radius*0.08)
	DrawHand(dc, cx, cy, handAngle, handLen, handW, col)

	// Center dot.
	dc.SetColor(col)
	dc.DrawCircle(cx, cy, math.Max(2, radius*0.06))
	dc.Fill()
}
