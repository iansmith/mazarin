package mancini

import (
	"image"
	"image/color"

	"github.com/fogleman/gg"
	"golang.org/x/image/font"
)

// ScaledDrawContext wraps a DrawContext, multiplying all coordinate arguments
// by a display density factor. At density=1.0 this is an identity wrapper —
// bit-identical output. Font sizes are expected to be pre-scaled at face
// creation time, so SetFontFace is passed through unmodified.
type ScaledDrawContext struct {
	DrawContext
	density float64
}

// NewScaledContext wraps dc with coordinate scaling by density.
func NewScaledContext(dc DrawContext, density float64) *ScaledDrawContext {
	return &ScaledDrawContext{DrawContext: dc, density: density}
}

func (s *ScaledDrawContext) DrawRectangle(x, y, w, h float64) {
	d := s.density
	s.DrawContext.DrawRectangle(x*d, y*d, w*d, h*d)
}

func (s *ScaledDrawContext) DrawRoundedRectangle(x, y, w, h, r float64) {
	d := s.density
	s.DrawContext.DrawRoundedRectangle(x*d, y*d, w*d, h*d, r*d)
}

func (s *ScaledDrawContext) DrawCircle(x, y, r float64) {
	d := s.density
	s.DrawContext.DrawCircle(x*d, y*d, r*d)
}

func (s *ScaledDrawContext) DrawLine(x1, y1, x2, y2 float64) {
	d := s.density
	s.DrawContext.DrawLine(x1*d, y1*d, x2*d, y2*d)
}

func (s *ScaledDrawContext) MoveTo(x, y float64) {
	d := s.density
	s.DrawContext.MoveTo(x*d, y*d)
}

func (s *ScaledDrawContext) LineTo(x, y float64) {
	d := s.density
	s.DrawContext.LineTo(x*d, y*d)
}

func (s *ScaledDrawContext) FillRectangle(x, y, w, h float64) {
	d := s.density
	s.DrawContext.FillRectangle(x*d, y*d, w*d, h*d)
}

func (s *ScaledDrawContext) DrawString(str string, x, y float64) {
	d := s.density
	s.DrawContext.DrawString(str, x*d, y*d)
}

func (s *ScaledDrawContext) DrawStringAnchored(str string, x, y, ax, ay float64) {
	d := s.density
	s.DrawContext.DrawStringAnchored(str, x*d, y*d, ax, ay)
}

func (s *ScaledDrawContext) MeasureString(str string) (float64, float64) {
	w, h := s.DrawContext.MeasureString(str)
	d := s.density
	return w / d, h / d
}

func (s *ScaledDrawContext) SetLineWidth(lineWidth float64) {
	s.DrawContext.SetLineWidth(lineWidth * s.density)
}

// SetColor, SetFillStyle, SetLineCap, SetFontFace, LoadFontFace, Push, Pop,
// Rotate, RotateAbout, Fill, Stroke, Image — all pass through unchanged
// via the embedded DrawContext.

// Ensure ScaledDrawContext satisfies DrawContext at compile time.
var _ DrawContext = (*ScaledDrawContext)(nil)

// The following methods are inherited from the embedded DrawContext:
// SetColor, SetFillStyle, SetLineCap, SetFontFace, LoadFontFace,
// Push, Pop, Rotate, RotateAbout, Fill, Stroke, Image.

// We explicitly re-export SetFillStyle so gradients with coordinates work.
func (s *ScaledDrawContext) SetFillStyle(pattern gg.Pattern) {
	s.DrawContext.SetFillStyle(pattern)
}

func (s *ScaledDrawContext) SetColor(c color.Color) {
	s.DrawContext.SetColor(c)
}

func (s *ScaledDrawContext) SetLineCap(lineCap gg.LineCap) {
	s.DrawContext.SetLineCap(lineCap)
}

func (s *ScaledDrawContext) SetFontFace(fontFace font.Face) {
	s.DrawContext.SetFontFace(fontFace)
}

func (s *ScaledDrawContext) LoadFontFace(path string, points float64) error {
	return s.DrawContext.LoadFontFace(path, points)
}

func (s *ScaledDrawContext) Fill() {
	s.DrawContext.Fill()
}

func (s *ScaledDrawContext) Stroke() {
	s.DrawContext.Stroke()
}

func (s *ScaledDrawContext) Push() {
	s.DrawContext.Push()
}

func (s *ScaledDrawContext) Pop() {
	s.DrawContext.Pop()
}

func (s *ScaledDrawContext) Rotate(angle float64) {
	s.DrawContext.Rotate(angle)
}

func (s *ScaledDrawContext) RotateAbout(angle, x, y float64) {
	d := s.density
	s.DrawContext.RotateAbout(angle, x*d, y*d)
}

func (s *ScaledDrawContext) Image() image.Image {
	return s.DrawContext.Image()
}
