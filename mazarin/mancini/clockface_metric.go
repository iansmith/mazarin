package mancini

import (
	"fmt"
	"image/color"
	"math"
	"time"
)

// MetricFace draws an analog clock using French Revolutionary / metric time:
// 10 hours per day, 100 minutes per hour, 100 seconds per minute.
// The dial shows digits 0-9 evenly spaced (36 degrees each), with 0 at top.
// All hands move slower than conventional clocks because one revolution of
// the hour hand covers an entire day.
type MetricFace struct {
	HandColor color.NRGBA    // hand and digit color (zero = black)
	FillColor color.NRGBA    // face fill color (zero = theme Surface)
	Loc       *time.Location // timezone (for converting UTC to local midnight)
}

// Location returns the timezone. Falls back to UTC if nil.
func (f *MetricFace) Location() *time.Location {
	if f.Loc == nil {
		return time.UTC
	}
	return f.Loc
}

func (f *MetricFace) FaceName() string { return "Metric" }

// DrawFace renders the metric clock face. The hour/minute/second/millis
// parameters are conventional (24h) local time; this method converts them
// to metric time for display.
func (f *MetricFace) DrawFace(dc DrawContext, fc *FontConfig, pal Palette, cx, cy, radius float64, hour, minute, second, millis int) {
	// Fill the clock face.
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

	// Digit markers: 0-9 evenly spaced, 36 degrees apart, 0 at top.
	fontSize := int64(math.Max(6, radius*0.20))
	loadFont(fc, dc, true, fontSize)
	dc.SetColor(col)
	markerRad := radius * 0.80
	for i := 0; i < 10; i++ {
		// 0 at top (-pi/2), each subsequent digit 36 degrees (pi/5) clockwise.
		posAngle := float64(i)*math.Pi/5 - math.Pi/2
		mx := cx + markerRad*math.Cos(posAngle)
		my := cy + markerRad*math.Sin(posAngle)
		dc.DrawStringAnchored(fmt.Sprintf("%d", i), mx, my, 0.5, 0.5)
	}

	// Convert conventional time to metric time.
	// Metric time: 10 hours/day, 100 minutes/hour, 100 seconds/minute.
	// Each level is: take integer part, subtract, multiply by 100.
	totalRealSec := float64(hour)*3600 + float64(minute)*60 + float64(second) + float64(millis)/1000.0
	dayFrac := totalRealSec / 86400.0

	metricHours := dayFrac * 10                // 0..10 (fractional metric hours)
	metricHour := math.Floor(metricHours)      // 0..9
	metricMins := (metricHours - metricHour) * 100 // 0..100 (fractional metric minutes)
	metricMin := math.Floor(metricMins)        // 0..99
	metricSec := (metricMins - metricMin) * 100 // 0..100 (metric seconds with fractional part)

	// Fractional values cascade upward for smooth hand motion.
	fracSec := metricSec
	fracMin := metricMin + fracSec/100.0
	fracHour := metricHour + fracMin/100.0

	// Hour hand: one full revolution = 10 metric hours (one day).
	hourAngle := fracHour*2*math.Pi/10 - math.Pi/2
	hourLen := radius * 0.45
	hourW := math.Max(2, radius*0.08)
	DrawHand(dc, cx, cy, hourAngle, hourLen, hourW, col)

	// Minute hand: one full revolution = 100 metric minutes.
	minAngle := fracMin*2*math.Pi/100 - math.Pi/2
	minLen := radius * 0.65
	minW := math.Max(1.5, radius*0.05)
	DrawHand(dc, cx, cy, minAngle, minLen, minW, col)

	// Second hand: one full revolution = 100 metric seconds.
	secAngle := fracSec*2*math.Pi/100 - math.Pi/2
	secLen := radius * 0.72
	secW := math.Max(1, radius*0.02)
	secColor := color.NRGBA{200, 60, 60, 255}
	DrawHand(dc, cx, cy, secAngle, secLen, secW, secColor)

	// Center dot.
	dc.SetColor(col)
	dc.DrawCircle(cx, cy, math.Max(2, radius*0.06))
	dc.Fill()
}
