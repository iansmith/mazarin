// throbber.go — Pulsing orange indicator light for the HP 15C calculator.
//
// Throbber is a small filled circle that smoothly pulses through
// orange brightness levels over a 6-second cycle (3s up, 3s down).
// It is driven by the 10Hz kernel time attribute via attr.OnDirty().
package main

import (
	"image"
	"image/color"

	"mazzy/mazarin/mancini"
	"mazzy/mazarin/mancini/impl"
)

const (
	throbHalfTicks = 20 // 2 seconds × 10Hz = 20 ticks per half-cycle
	throbFullTicks = 2 * throbHalfTicks
)

// Orange endpoints: dimmest and brightest (in swapRB space).
var (
	throbDimR, throbDimG, throbDimB       uint8 = 60, 30, 0
	throbBrightR, throbBrightG, throbBrightB uint8 = 255, 160, 0
)

// Throbber is a small pulsing circle indicator.
type Throbber struct {
	impl.Interactor

	tick int // current tick counter (0..59), incremented at 10Hz
}

// NewThrobber creates a throbber positioned as a child of parent.
func NewThrobber(myName, parent string, size int64) *Throbber {
	lh := mancini.NewLayoutAttributes(myName, parent)
	lh.Width.Set(size)
	lh.Height.Set(size)

	t := &Throbber{}
	t.Interactor.Initialize(t, lh)
	return t
}

// Tick advances the throbber by one 10Hz step. Always returns true
// since the color changes continuously every tick.
func (t *Throbber) Tick() bool {
	t.tick = (t.tick + 1) % throbFullTicks
	return true
}

// fraction returns 0.0..1.0 representing brightness phase.
// Ramps linearly from 0→1 over the first half, then 1→0 over the second.
func (t *Throbber) fraction() float64 {
	if t.tick < throbHalfTicks {
		return float64(t.tick) / float64(throbHalfTicks)
	}
	return float64(throbFullTicks-t.tick) / float64(throbHalfTicks)
}

// color returns the current interpolated orange.
func (t *Throbber) color() color.NRGBA {
	f := t.fraction()
	r := throbDimR + uint8(f*float64(throbBrightR-throbDimR))
	g := throbDimG + uint8(f*float64(throbBrightG-throbDimG))
	b := throbDimB + uint8(f*float64(throbBrightB-throbDimB))
	return swapRB(r, g, b, 255)
}

// Draw renders the throbber as a filled circle at the current brightness.
// The radius scales from 70% at dimmest to 100% at brightest.
func (t *Throbber) Draw(self mancini.Interactor, x, y, w, h int64, damage image.Rectangle) {
	dc := self.DC()
	if dc == nil {
		return
	}

	cx := float64(x) + float64(w)/2
	cy := float64(y) + float64(h)/2
	sz := w
	if h < sz {
		sz = h
	}
	maxRad := float64(sz) / 2
	f := t.fraction()
	rad := maxRad * (0.7 + 0.3*f) // 70% at dim, 100% at bright

	dc.SetColor(t.color())
	dc.DrawCircle(cx, cy, rad)
	dc.Fill()
}
