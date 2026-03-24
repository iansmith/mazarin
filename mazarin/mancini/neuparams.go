package mancini

import (
	"image/color"
	"math"
)

// NeuMaxPad computes the maximum shadow padding across all depth states.
// Returns the ceiling as an integer — all interactor dimensions are int64.
func NeuMaxPad(p NeuParams) int64 {
	// Raised: external shadows
	rOff := math.Max(p.Raised.DarkOff, p.Raised.LightOff)
	rBlur := math.Max(p.Raised.DarkBlur, p.Raised.LightBlur)
	rPad := rOff + math.Ceil(rBlur*3) + 2

	// Inset: internal shadows (extend beyond box for blur)
	iBlur := math.Max(p.Inset.DarkBlur, p.Inset.LightBlur)
	iPad := p.Inset.Off + math.Ceil(iBlur*3) + 2

	// Flush: edge stroke
	fPad := p.Flush.EdgeW + 2

	return int64(math.Ceil(math.Max(rPad, math.Max(iPad, fPad))))
}

// Alignment controls cross-axis positioning of children within a container.
// For Column (vertical layout), this aligns children horizontally.
// For Row (horizontal layout), this aligns children vertically.
type Alignment int

const (
	AxisMinimum Alignment = 0 // left (Column) or top (Row)
	AxisMiddle  Alignment = 1 // centered
	AxisMaximum Alignment = 2 // right (Column) or bottom (Row)
)

// ErrPink is the background color drawn when an interactor has no children
// (almost always a programming error).
var ErrPink = color.NRGBA{255, 105, 180, 255}
