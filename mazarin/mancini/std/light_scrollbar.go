package std

import (
	"fmt"
	"image"
	"image/color"
	"math"

	"mazzy/mazarin/attr"
	"mazzy/mazarin/mancini"
	"mazzy/mazarin/mancini/impl"
)

// LightScrollbar is a minimalist scrollbar with a narrow profile.
// It draws everything itself: a Surface track with a flush groove
// border, two diagonal X-pattern lines on the face, and a rotating
// dark-purple thumb line. All rendering uses Fill (no Stroke calls).
//
// The cross-axis dimension is fixed at [lightScrollbarThick] (10 pixels).
// There are no arrow buttons. Clicking anywhere on the track jumps the
// thumb to that position; dragging moves it.
type LightScrollbar struct {
	impl.ThemedInteractor

	IsVertical bool    // true = vertical, false = horizontal
	ThumbFrac  float64 // visible fraction 0..1 (kept for Scrollbar API compat)
	ThumbPos   float64 // thumb position 0..1 along available travel
	ThumbAngle float64 // thumb rotation in radians (0 = vertical for horiz sb)

	Disabled bool // when true, renders dimmed and ignores input

	// Value/Max: when set, ThumbPos and ThumbFrac are computed from
	// these in Draw. Value is the current scroll position (pixels),
	// Max is the maximum scroll range. Value is the source of truth —
	// the scroller's VirtualY is constrained to it.
	ValueAttr *attr.Attribute[int64]
	MaxAttr   *attr.Attribute[int64]

	// Drag state — active during ClickDraggable interaction.
	dragOrigPos float64 // ThumbPos at drag start
}

const lightScrollbarThick = 10.0 // cross-axis dimension

// NewLightScrollbar creates a LightScrollbar wired to the constraint system.
func NewLightScrollbar(layout *mancini.LayoutAttributes, theme mancini.Theme,
	isVertical bool, thumbFrac, thumbPos float64) *LightScrollbar {

	s := &LightScrollbar{
		IsVertical: isVertical,
		ThumbFrac:  thumbFrac,
		ThumbPos:   thumbPos,
	}
	s.ThemedInteractor.Initialize(s, layout, theme)
	return s
}

// NewLightScrollbarNamed creates a LightScrollbar with layout built from
// name + parent strings. length is the main-axis dimension.
func NewLightScrollbarNamed(myName, parent string, theme mancini.Theme,
	isVertical bool, length, thumbFrac, thumbPos float64) *LightScrollbar {

	if myName == "" {
		myName = mancini.DefaultName("lightscrollbar")
	}
	lh := mancini.NewLayoutAttributes(myName, parent)
	if isVertical {
		lh.Width.Set(int64(lightScrollbarThick))
		lh.Height.Set(int64(length))
	} else {
		lh.Width.Set(int64(length))
		lh.Height.Set(int64(lightScrollbarThick))
	}
	return NewLightScrollbar(lh, theme, isVertical, thumbFrac, thumbPos)
}

// filledLine draws a line as a filled 2px-wide rotated rectangle.
// All rendering via Fill — no Stroke calls.
func filledLine(dc mancini.DrawContext, c color.NRGBA, ax, ay, bx, by, halfW float64) {
	dx := bx - ax
	dy := by - ay
	ln := math.Sqrt(dx*dx + dy*dy)
	if ln < 0.1 {
		return
	}
	// Perpendicular unit vector scaled by halfW.
	px := (-dy / ln) * halfW
	py := (dx / ln) * halfW

	dc.SetColor(c)
	dc.MoveTo(ax-px, ay-py)
	dc.LineTo(bx-px, by-py)
	dc.LineTo(bx+px, by+py)
	dc.LineTo(ax+px, ay+py)
	dc.ClosePath()
	dc.Fill()
}

// Draw implements mancini.NewDrawer. Renders track, groove border,
// X-pattern face lines, and rotating thumb — all using Fill.
func (s *LightScrollbar) Draw(self mancini.Interactor, x, y, w, h int64, damage image.Rectangle) {
	if !self.Visible() {
		return
	}
	dc := self.DC()
	if dc == nil {
		return
	}

	// Compute ThumbPos and ThumbFrac from Value/Max if available.
	if s.ValueAttr != nil && s.MaxAttr != nil {
		maxVal := s.MaxAttr.Get()
		if maxVal > 0 {
			val := s.ValueAttr.Get()
			if val < 0 {
				val = 0
			}
			if val > maxVal {
				val = maxVal
			}
			var mainAxis float64
			if s.IsVertical {
				mainAxis = float64(h)
			} else {
				mainAxis = float64(w)
			}
			s.ThumbPos = float64(val) / float64(maxVal)
			s.ThumbFrac = mainAxis / (mainAxis + float64(maxVal))
		}
	}

	pal := s.Theme().Palette()
	fx, fy := float64(x), float64(y)
	fw, fh := float64(w), float64(h)

	// Track bounds.
	var tx1, ty1, tx2, ty2, length float64
	if s.IsVertical {
		tx1 = fx + (fw-lightScrollbarThick)/2
		ty1 = fy
		tx2 = tx1 + lightScrollbarThick
		ty2 = fy + fh
		length = fh
	} else {
		tx1 = fx
		ty1 = fy + (fh-lightScrollbarThick)/2
		tx2 = fx + fw
		ty2 = ty1 + lightScrollbarThick
		length = fw
	}
	tR := lightScrollbarThick / 2.0

	// 1. Track outline — plain black rounded rectangle.
	dc.SetColor(color.NRGBA{0, 0, 0, 255})
	dc.DrawRoundedRectangle(tx1, ty1, tx2-tx1, ty2-ty1, tR)
	dc.Fill()
	// Fill interior with surface color.
	dc.SetColor(pal.Surface())
	dc.DrawRoundedRectangle(tx1+1, ty1+1, tx2-tx1-2, ty2-ty1-2, tR-1)
	dc.Fill()

	// Groove border (disabled) — dark shadow top-left, light shadow bottom-right.
	// dc.SetColor(pal.DarkShadow())
	// dc.DrawRoundedRectangle(tx1-1, ty1-1, tx2-tx1+1, ty2-ty1+1, tR)
	// dc.Fill()
	// dc.SetColor(pal.LightShadow())
	// dc.DrawRoundedRectangle(tx1, ty1, tx2-tx1+1, ty2-ty1+1, tR)
	// dc.Fill()
	// dc.SetColor(pal.Surface())
	// dc.DrawRoundedRectangle(tx1, ty1, tx2-tx1, ty2-ty1, tR)
	// dc.Fill()

	// 3. Thumb — rotating dark-purple line at ThumbPos.
	// The thumb line has half-length halfLen; its center must stay
	// at least halfLen inside the track so the ends don't poke out.
	halfLen := (lightScrollbarThick - 2) / 2.0
	margin := halfLen + 1 // 1px border + half the thumb length
	travel := length - 2*margin
	if travel < 0 {
		travel = 0
	}
	thumbOffset := travel * s.ThumbPos

	var cx, cy float64
	if s.IsVertical {
		cx = (tx1 + tx2) / 2
		cy = ty1 + margin + thumbOffset
	} else {
		cx = tx1 + margin + thumbOffset
		cy = (ty1 + ty2) / 2
	}

	cos := math.Cos(s.ThumbAngle)
	sin := math.Sin(s.ThumbAngle)
	ex0 := cx - halfLen*sin
	ey0 := cy - halfLen*cos
	ex1 := cx + halfLen*sin
	ey1 := cy + halfLen*cos

	filledLine(dc, pal.Text(), ex0, ey0, ex1, ey1, 1.0)

	if s.Disabled {
		ApplyDisabledOverlay(pal, dc, tx1, ty1, tx2, ty2, tR)
	}
}

// ── ClickDraggable protocol ──────────────────────────────────────────

// trackGeometry computes the track's main-axis start, margin, and travel distance.
// margin is the distance from the track edge to the first valid thumb center.
func (s *LightScrollbar) trackGeometry(x, y, w, h int64) (trackStart, length, margin, travel float64) {
	if s.IsVertical {
		trackStart = float64(y)
		length = float64(h)
	} else {
		trackStart = float64(x)
		length = float64(w)
	}
	halfLen := (lightScrollbarThick - 2) / 2.0
	margin = halfLen + 1
	travel = length - 2*margin
	if travel < 0 {
		travel = 0
	}
	return
}

// ClickDragStart implements mancini.ClickDraggable.
func (s *LightScrollbar) ClickDragStart(ev *mancini.InputEvent) bool {
	if s.Disabled {
		return false
	}
	sx, sy := s.X(), s.Y()
	sw, sh := s.W(), s.H()
	trackStart, _, margin, travel := s.trackGeometry(sx, sy, sw, sh)

	s.dragOrigPos = s.ThumbPos

	var cursor float64
	if s.IsVertical {
		cursor = float64(ev.Y)
	} else {
		cursor = float64(ev.X)
	}

	if travel > 0 {
		newPos := (cursor - trackStart - margin) / travel
		if newPos < 0 {
			newPos = 0
		}
		if newPos > 1 {
			newPos = 1
		}
		s.ThumbPos = newPos
		if s.ValueAttr != nil && s.MaxAttr != nil {
			maxVal := s.MaxAttr.Get()
			s.ValueAttr.Set(int64(newPos * float64(maxVal)))
		}
		s.FullDamage()
		fmt.Printf("[lightscrollbar] ClickDragStart jumped to %.3f\n", newPos)
	}
	return true
}

// ClickDragMove implements mancini.ClickDraggable.
func (s *LightScrollbar) ClickDragMove(ev *mancini.InputEvent, _ *mancini.InputEvent, outsideBounds bool) bool {
	sx, sy := s.X(), s.Y()
	sw, sh := s.W(), s.H()
	trackStart, _, margin, travel := s.trackGeometry(sx, sy, sw, sh)

	if travel <= 0 {
		return true
	}

	var cursor float64
	if s.IsVertical {
		cursor = float64(ev.Y)
	} else {
		cursor = float64(ev.X)
	}

	newPos := (cursor - trackStart - margin) / travel
	if newPos < 0 {
		newPos = 0
	}
	if newPos > 1 {
		newPos = 1
	}
	s.ThumbPos = newPos
	if s.ValueAttr != nil && s.MaxAttr != nil {
		maxVal := s.MaxAttr.Get()
		s.ValueAttr.Set(int64(newPos * float64(maxVal)))
	}
	s.FullDamage()
	return true
}

// ClickDragEnd implements mancini.ClickDraggable.
func (s *LightScrollbar) ClickDragEnd(ev *mancini.InputEvent, outsideBounds bool) bool {
	fmt.Printf("[lightscrollbar] ClickDragEnd pos=%.3f outside=%v\n", s.ThumbPos, outsideBounds)
	s.FullDamage()
	return true
}
