package textshape

import (
	"image"
	"image/color"
	"math"
)

// LineCap specifies how line endpoints are rendered.
type LineCap int

const (
	LineCapRound  LineCap = iota
	LineCapButt
	LineCapSquare
)

// LineJoin specifies how line segments are joined.
type LineJoin int

const (
	LineJoinRound LineJoin = iota
	LineJoinBevel
)

// FillRule specifies the fill algorithm for self-intersecting paths.
type FillRule int

const (
	FillRuleWinding FillRule = iota
	FillRuleEvenOdd
)

// RepeatOp specifies how a surface pattern tiles.
type RepeatOp int

const (
	RepeatBoth RepeatOp = iota
	RepeatX
	RepeatY
	RepeatNone
)

// Pattern provides a color at any pixel coordinate.
type Pattern interface {
	ColorAt(x, y int) color.Color
}

// NewSolidPattern returns a Pattern that returns the same color everywhere.
func NewSolidPattern(c color.Color) Pattern {
	return &solidPattern{c}
}

// NewSurfacePattern returns a Pattern backed by an image with a repeat mode.
func NewSurfacePattern(im image.Image, op RepeatOp) Pattern {
	return &surfacePattern{im: im, op: op}
}

type solidPattern struct{ c color.Color }

func (p *solidPattern) ColorAt(x, y int) color.Color { return p.c }

type surfacePattern struct {
	im image.Image
	op RepeatOp
}

func (p *surfacePattern) ColorAt(x, y int) color.Color {
	b := p.im.Bounds()
	switch p.op {
	case RepeatX:
		if y >= b.Dy() {
			return color.Transparent
		}
	case RepeatY:
		if x >= b.Dx() {
			return color.Transparent
		}
	case RepeatNone:
		if x >= b.Dx() || y >= b.Dy() {
			return color.Transparent
		}
	}
	x = x%b.Dx() + b.Min.X
	y = y%b.Dy() + b.Min.Y
	return p.im.At(x, y)
}

// Matrix is a 2D affine transform: [ XX XY X0 ]
//                                   [ YX YY Y0 ]
type Matrix struct {
	XX, YX, XY, YY, X0, Y0 float64
}

func IdentityMatrix() Matrix {
	return Matrix{XX: 1, YY: 1}
}

func (a Matrix) Multiply(b Matrix) Matrix {
	return Matrix{
		XX: a.XX*b.XX + a.YX*b.XY,
		YX: a.XX*b.YX + a.YX*b.YY,
		XY: a.XY*b.XX + a.YY*b.XY,
		YY: a.XY*b.YX + a.YY*b.YY,
		X0: a.X0*b.XX + a.Y0*b.XY + b.X0,
		Y0: a.X0*b.YX + a.Y0*b.YY + b.Y0,
	}
}

func (a Matrix) TransformPoint(x, y float64) (float64, float64) {
	return a.XX*x + a.XY*y + a.X0, a.YX*x + a.YY*y + a.Y0
}

func (a Matrix) Translate(x, y float64) Matrix {
	return Matrix{XX: 1, YY: 1, X0: x, Y0: y}.Multiply(a)
}

func (a Matrix) Scale(x, y float64) Matrix {
	return Matrix{XX: x, YY: y}.Multiply(a)
}

func (a Matrix) Rotate(angle float64) Matrix {
	c, s := math.Cos(angle), math.Sin(angle)
	return Matrix{XX: c, YX: s, XY: -s, YY: c}.Multiply(a)
}
