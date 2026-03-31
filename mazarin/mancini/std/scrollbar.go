package std

import (
	"image"
	"image/color"
	"image/draw"
	"math"

	"github.com/fogleman/gg"

	"mazzy/mazarin/mancini"
	"mazzy/mazarin/mancini/impl"
)

// Scrollbar is a neumorphic scrollbar interactor with an inset track,
// a raised thumb with a grip dot, and optional arrow buttons at each end.
//
// Scrollbar embeds [impl.ThemedInteractor] and uses Light-weight
// [mancini.NeuParams] for its neumorphic effects. When the theme's
// [mancini.NeumorphicParams.Light] returns nil, the scrollbar falls back
// to flat rendering: [mancini.Palette.SurfaceTint] for the track,
// [mancini.Palette.Surface] for the thumb, and [mancini.Palette.Icon] for
// arrow triangles.
//
// ThumbFrac controls thumb size as a fraction of the track (0..1,
// representing the proportion of visible content). ThumbPos controls the
// thumb position along available travel (0 = start, 1 = end). All
// dimensions scale proportionally to TrackWidth.
type Scrollbar struct {
	impl.ThemedInteractor

	IsVertical bool    // true = vertical, false = horizontal
	TrackWidth float64 // cross-axis thickness (values < 5 use default 22.5)
	ThumbFrac  float64 // visible fraction 0..1 (determines thumb length)
	ThumbPos   float64 // thumb position 0..1 along available travel
	ShowArrows bool    // draw arrow triangles at each end
}

const defaultScrollbarThick = 22.5

// NewScrollbar creates a Scrollbar wired to the constraint system and theme.
func NewScrollbar(layout *mancini.LayoutAttributes, theme mancini.Theme,
	isVertical bool, trackWidth, thumbFrac, thumbPos float64, showArrows bool) *Scrollbar {

	s := &Scrollbar{
		IsVertical: isVertical,
		TrackWidth: trackWidth,
		ThumbFrac:  thumbFrac,
		ThumbPos:   thumbPos,
		ShowArrows: showArrows,
	}
	s.ThemedInteractor.Initialize(s, layout, theme)
	return s
}

// NewScrollbarNamed creates a Scrollbar with layout built from name + parent
// strings. The main-axis dimension is set to length; the cross-axis is set to
// the resolved track width.
func NewScrollbarNamed(myName, parent string, theme mancini.Theme,
	isVertical bool, length, trackWidth, thumbFrac, thumbPos float64,
	showArrows bool) *Scrollbar {

	if myName == "" {
		myName = mancini.DefaultName("scrollbar")
	}
	lh := mancini.NewLayoutAttributes(myName, parent)
	thick := trackWidth
	if thick < 5 {
		thick = defaultScrollbarThick
	}
	if isVertical {
		lh.Width.Set(int64(math.Ceil(thick)))
		lh.Height.Set(int64(math.Ceil(length)))
	} else {
		lh.Width.Set(int64(math.Ceil(length)))
		lh.Height.Set(int64(math.Ceil(thick)))
	}
	return NewScrollbar(lh, theme, isVertical, trackWidth, thumbFrac, thumbPos, showArrows)
}

// Draw implements mancini.NewDrawer. It renders the scrollbar: inset track,
// raised thumb, grip dot, and optional arrow buttons.
func (s *Scrollbar) Draw(self mancini.Interactor, x, y, w, h int64) {
	if !self.Visible() {
		return
	}
	dc := self.DC()
	if dc == nil {
		return
	}

	pal := s.Theme().Palette()
	var params *mancini.NeuParams
	if neu := s.Theme().Neumorphic(); neu != nil {
		params = neu.Light()
	}

	fx, fy := float64(x), float64(y)
	fw, fh := float64(w), float64(h)

	thick := s.TrackWidth
	if thick < 5 {
		thick = defaultScrollbarThick
	}
	scale := thick / defaultScrollbarThick
	tR := thick / 2.0
	thumbInset := 4.5 * scale
	tThumbR := 9.0 * scale
	tDotRad := 3.75 * scale
	tArrowH := 10.5 * scale
	tArrowW := 7.5 * scale
	tArrowPad := 12.0 * scale

	// Track bounds.
	var tx1, ty1, tx2, ty2, length float64
	if s.IsVertical {
		tx1 = fx + (fw-thick)/2
		ty1 = fy
		tx2 = tx1 + thick
		ty2 = fy + fh
		length = fh
	} else {
		tx1 = fx
		ty1 = fy + (fh-thick)/2
		tx2 = fx + fw
		ty2 = ty1 + thick
		length = fw
	}

	// 1. Track.
	if params != nil {
		neuInset(pal, dc, tx1, ty1, tx2, ty2, tR, pal.Surface(), params.Inset)
	} else {
		dc.SetColor(pal.SurfaceTint())
		dc.DrawRoundedRectangle(tx1, ty1, tx2-tx1, ty2-ty1, tR)
		dc.Fill()
	}

	// 2. Arrows.
	if s.ShowArrows {
		if s.IsVertical {
			trackCX := (tx1 + tx2) / 2
			if params != nil {
				scrollbarDrawArrow(dc, pal, params.Raised, trackCX, ty1+tArrowPad, tArrowW, tArrowH, true, true)
				scrollbarDrawArrow(dc, pal, params.Raised, trackCX, ty2-tArrowPad, tArrowW, tArrowH, true, false)
			} else {
				scrollbarDrawFlatArrow(dc, pal, trackCX, ty1+tArrowPad, tArrowW, tArrowH, true, true)
				scrollbarDrawFlatArrow(dc, pal, trackCX, ty2-tArrowPad, tArrowW, tArrowH, true, false)
			}
		} else {
			trackCY := (ty1 + ty2) / 2
			if params != nil {
				scrollbarDrawArrow(dc, pal, params.Raised, tx1+tArrowPad, trackCY, tArrowW, tArrowH, false, true)
				scrollbarDrawArrow(dc, pal, params.Raised, tx2-tArrowPad, trackCY, tArrowW, tArrowH, false, false)
			} else {
				scrollbarDrawFlatArrow(dc, pal, tx1+tArrowPad, trackCY, tArrowW, tArrowH, false, true)
				scrollbarDrawFlatArrow(dc, pal, tx2-tArrowPad, trackCY, tArrowW, tArrowH, false, false)
			}
		}
	}

	// 3. Thumb.
	thumbThick := thick - thumbInset
	minThumbLen := 40.0 * scale
	maxThumbLen := length * 0.8
	thumbLen := length * s.ThumbFrac
	if thumbLen < minThumbLen {
		thumbLen = minThumbLen
	}
	if thumbLen > maxThumbLen {
		thumbLen = maxThumbLen
	}

	travel := length - thumbLen
	thumbOffset := travel * s.ThumbPos

	var thx1, thy1, thx2, thy2 float64
	if s.IsVertical {
		thx1 = tx1 + (thick-thumbThick)/2
		thy1 = ty1 + thumbOffset
		thx2 = thx1 + thumbThick
		thy2 = thy1 + thumbLen
	} else {
		thx1 = tx1 + thumbOffset
		thy1 = ty1 + (thick-thumbThick)/2
		thx2 = thx1 + thumbLen
		thy2 = thy1 + thumbThick
	}
	if params != nil {
		neuRaised(pal, dc, thx1, thy1, thx2, thy2, tThumbR, pal.Surface(), params.Raised)
	} else {
		dc.SetColor(pal.Surface())
		dc.DrawRoundedRectangle(thx1, thy1, thx2-thx1, thy2-thy1, tThumbR)
		dc.Fill()
	}

	// 4. Grip dot at thumb center.
	dotCX := (thx1 + thx2) / 2
	dotCY := (thy1 + thy2) / 2
	if params != nil {
		neuCircleRaised(pal, dc, dotCX, dotCY, tDotRad, pal.Surface(), params.Raised)
	} else {
		dc.SetColor(pal.Surface())
		dc.DrawCircle(dotCX, dotCY, tDotRad)
		dc.Fill()
	}
}

// ── Arrow rendering ────────────────────────────────────────────────

// scrollbarDrawFlatArrow draws a flat triangle arrow without neumorphic shadows.
func scrollbarDrawFlatArrow(dc mancini.DrawContext, pal mancini.Palette,
	tipX, tipY, arrowW, arrowH float64,
	isVertical, pointStart bool) {

	var x0, y0, x1, y1, x2, y2 float64
	if isVertical {
		if pointStart {
			x0, y0 = tipX, tipY
			x1, y1 = tipX-arrowH/2, tipY+arrowW
			x2, y2 = tipX+arrowH/2, tipY+arrowW
		} else {
			x0, y0 = tipX, tipY
			x1, y1 = tipX-arrowH/2, tipY-arrowW
			x2, y2 = tipX+arrowH/2, tipY-arrowW
		}
	} else {
		if pointStart {
			x0, y0 = tipX, tipY
			x1, y1 = tipX+arrowW, tipY-arrowH/2
			x2, y2 = tipX+arrowW, tipY+arrowH/2
		} else {
			x0, y0 = tipX, tipY
			x1, y1 = tipX-arrowW, tipY-arrowH/2
			x2, y2 = tipX-arrowW, tipY+arrowH/2
		}
	}
	dc.SetColor(pal.Icon())
	dc.MoveTo(x0, y0)
	dc.LineTo(x1, y1)
	dc.LineTo(x2, y2)
	dc.Fill()
}

// scrollbarDrawArrow draws a small raised triangle arrow button.
func scrollbarDrawArrow(dc mancini.DrawContext, pal mancini.Palette,
	p mancini.RaisedParams,
	tipX, tipY, arrowW, arrowH float64,
	isVertical, pointStart bool) {

	canvas := dc.Image().(*image.RGBA)

	var x0, y0, x1, y1, x2, y2 float64
	if isVertical {
		if pointStart { // point up
			x0, y0 = tipX, tipY
			x1, y1 = tipX-arrowH/2, tipY+arrowW
			x2, y2 = tipX+arrowH/2, tipY+arrowW
		} else { // point down
			x0, y0 = tipX, tipY
			x1, y1 = tipX-arrowH/2, tipY-arrowW
			x2, y2 = tipX+arrowH/2, tipY-arrowW
		}
	} else {
		if pointStart { // point left
			x0, y0 = tipX, tipY
			x1, y1 = tipX+arrowW, tipY-arrowH/2
			x2, y2 = tipX+arrowW, tipY+arrowH/2
		} else { // point right
			x0, y0 = tipX, tipY
			x1, y1 = tipX-arrowW, tipY-arrowH/2
			x2, y2 = tipX-arrowW, tipY+arrowH/2
		}
	}

	bx1 := math.Min(x0, math.Min(x1, x2))
	by1 := math.Min(y0, math.Min(y1, y2))
	bx2 := math.Max(x0, math.Max(x1, x2))
	by2 := math.Max(y0, math.Max(y1, y2))

	maxOff := math.Max(p.DarkOff, p.LightOff)
	maxBlur := math.Max(p.DarkBlur, p.LightBlur)
	pad := maxOff + math.Ceil(maxBlur*3) + 2
	lw, lh, ox, oy := localRect(canvas, bx1, by1, bx2, by2, pad)
	dst := image.Rect(int(ox), int(oy), int(ox)+lw, int(oy)+lh)

	dark := triangleShadowLayer(lw, lh,
		x0-ox+p.DarkOff, y0-oy+p.DarkOff,
		x1-ox+p.DarkOff, y1-oy+p.DarkOff,
		x2-ox+p.DarkOff, y2-oy+p.DarkOff,
		pal.DarkShadow(), p.DarkAlpha, p.DarkBlur)
	draw.Draw(canvas, dst, dark, image.Point{}, draw.Over)

	light := triangleShadowLayer(lw, lh,
		x0-ox-p.LightOff, y0-oy-p.LightOff,
		x1-ox-p.LightOff, y1-oy-p.LightOff,
		x2-ox-p.LightOff, y2-oy-p.LightOff,
		pal.LightShadow(), p.LightAlpha, p.LightBlur)
	draw.Draw(canvas, dst, light, image.Point{}, draw.Over)

	// Fill triangle with surface color.
	dc.SetColor(pal.Surface())
	dc.MoveTo(x0, y0)
	dc.LineTo(x1, y1)
	dc.LineTo(x2, y2)
	dc.Fill()
}

// triangleShadowLayer renders a colored triangle into a temporary NRGBA
// buffer, optionally blurred. Used for arrow button shadows.
func triangleShadowLayer(w, h int, x0, y0, x1, y1, x2, y2 float64,
	c color.NRGBA, alpha uint8, blur float64) *image.NRGBA {

	rgba := image.NewRGBA(image.Rect(0, 0, w, h))
	dc := gg.NewContextForRGBA(rgba)
	dc.SetColor(color.NRGBA{c.R, c.G, c.B, alpha})
	dc.MoveTo(x0, y0)
	dc.LineTo(x1, y1)
	dc.LineTo(x2, y2)
	dc.ClosePath()
	dc.Fill()
	nrgba := rgbaToNRGBA(rgba)
	if blur > 0 {
		return gaussianBlurNRGBA(nrgba, blur)
	}
	return nrgba
}
