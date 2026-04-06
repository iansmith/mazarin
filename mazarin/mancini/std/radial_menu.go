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

// RadialMenu is a full-circle annular menu divided into equal segments.
// One segment can be selected (rendered inset with a tint overlay).
// The ring is drawn with [mancini.Raised] neumorphic shadows, groove
// separators between all segments, and per-segment face content.
//
// RadialMenu embeds [impl.ThemedInteractor] and uses Heavy-weight
// [mancini.NeuParams] (the same weight class as [AppWindow]) because
// the ring shape warrants stronger shadows. When [mancini.NeuParams] is
// nil, only the flat annulus fill and face content are drawn.
//
// Each segment can have a [mancini.LatinTextFace] for text content.
// Face content is positioned at the segment's midpoint and rotated to
// align with the segment's angular position, reusing the same rendering
// code as [RadialNOfMChooser].
//
// See also [NOfMChooser] for a linear strip multi-select and
// [RadialNOfMChooser] for an arc-shaped multi-select.
type RadialMenu struct {
	impl.ThemedInteractor

	OuterR   float64                  // outer radius of the annulus
	InnerR   float64                  // inner radius (center hole)
	Faces    []mancini.LatinTextFace  // text content for each segment (nil entries OK)
	Selected int                      // selected segment index (-1 for none)
	Disabled bool                     // when true, renders dimmed and ignores input
}

// NewRadialMenu creates a RadialMenu wired to the constraint system and theme.
func NewRadialMenu(layout *mancini.LayoutAttributes, theme mancini.Theme,
	outerR, innerR float64, faces []mancini.LatinTextFace, selected int) *RadialMenu {

	m := &RadialMenu{
		OuterR:   outerR,
		InnerR:   innerR,
		Faces:    faces,
		Selected: selected,
	}
	m.ThemedInteractor.Initialize(m, layout, theme)
	return m
}

// NewRadialMenuNamed creates a RadialMenu with layout built from name + parent
// strings. Width and height are set to contain the full ring with shadow padding.
func NewRadialMenuNamed(myName, parent string, theme mancini.Theme,
	outerR, innerR float64, faces []mancini.LatinTextFace, selected int) *RadialMenu {

	if myName == "" {
		myName = mancini.DefaultName("radialmenu")
	}
	lh := mancini.NewLayoutAttributes(myName, parent)
	dim := int64(math.Ceil(outerR*2)) + 40
	lh.Width.Set(dim)
	lh.Height.Set(dim)
	return NewRadialMenu(lh, theme, outerR, innerR, faces, selected)
}

// Draw implements mancini.NewDrawer. It renders the full-circle radial menu:
// a raised annulus base, selected segment inset+tint, groove separators,
// and per-segment face content.
func (m *RadialMenu) Draw(self mancini.Interactor, x, y, w, h int64) {
	if !self.Visible() {
		return
	}
	dc := self.DC()
	if dc == nil {
		return
	}
	canvas := dc.Image().(*image.RGBA)
	pal := m.Theme().Palette()
	var params *mancini.NeuParams
	if neu := m.Theme().Neumorphic(); neu != nil {
		params = neu.Heavy()
	}

	n := len(m.Faces)
	if n == 0 {
		return
	}

	fx, fy := float64(x), float64(y)
	fw, fh := float64(w), float64(h)
	cx := fx + fw/2
	cy := fy + fh/2
	segAngle := 2 * math.Pi / float64(n)

	// 1. Draw base annulus shape.
	if params != nil {
		menuDrawRaisedAnnulus(canvas, dc, pal, cx, cy, m.OuterR, m.InnerR, params.Raised)
	} else {
		menuDrawFlatAnnulus(canvas, pal, cx, cy, m.OuterR, m.InnerR)
	}

	// 2–3. Neumorphic effects (inset segment, grooves).
	if params != nil {
		if m.Selected >= 0 && m.Selected < n {
			startA := -math.Pi/2 + float64(m.Selected)*segAngle
			endA := startA + segAngle
			menuDrawInsetSegment(canvas, pal, cx, cy, m.OuterR, m.InnerR,
				startA, endA, params.Raised, params.Inset)
		}
		menuDrawGrooves(canvas, pal, cx, cy, m.OuterR, m.InnerR, n)
	}

	// 4. Segment face content (reuses radialDrawFace from radial_nofm_chooser.go).
	grooveMargin := 3.0
	insetMargin := 5.0
	if params == nil {
		grooveMargin = 1.0
		insetMargin = 0.0
	}
	midR := (m.InnerR + m.OuterR) / 2
	grooveAngular := grooveMargin / midR
	insetAngular := insetMargin / midR
	for i, face := range m.Faces {
		if face == nil {
			continue
		}
		tStart := -math.Pi/2 + float64(i)*segAngle + grooveAngular
		tEnd := -math.Pi/2 + float64(i+1)*segAngle - grooveAngular
		rIn := m.InnerR + grooveMargin
		rOut := m.OuterR - grooveMargin
		if params != nil && i == m.Selected {
			tStart += insetAngular
			tEnd -= insetAngular
			rIn += insetMargin
			rOut -= insetMargin
		}
		radialDrawContent(dc, canvas, pal, cx, cy, rOut, rIn, tStart, tEnd, face)
	}
	if m.Disabled {
		ApplyDisabledCircleOverlay(pal, dc, cx, cy, m.OuterR)
	}
}

// ── Annulus-specific neumorphic rendering ───────────────────────────

// annulusShadowLayer renders a colored annulus (ring) shape into a
// temporary NRGBA buffer using even-odd fill, optionally blurred.
func annulusShadowLayer(w, h int, cx, cy, outerR, innerR float64,
	c color.NRGBA, alpha uint8, blur float64) *image.NRGBA {

	rgba := image.NewRGBA(image.Rect(0, 0, w, h))
	dc := gg.NewContextForRGBA(rgba)
	dc.SetFillRuleEvenOdd()
	dc.SetColor(color.NRGBA{c.R, c.G, c.B, alpha})
	dc.DrawCircle(cx, cy, outerR)
	dc.DrawCircle(cx, cy, innerR)
	dc.Fill()
	nrgba := rgbaToNRGBA(rgba)
	if blur > 0 {
		return gaussianBlurNRGBA(nrgba, blur)
	}
	return nrgba
}

// menuDrawFlatAnnulus fills the annulus ring shape with surface color,
// without any neumorphic shadow effects.
func menuDrawFlatAnnulus(canvas *image.RGBA, pal mancini.Palette,
	cx, cy, outerR, innerR float64) {

	fillBuf := image.NewRGBA(canvas.Bounds())
	fc := gg.NewContextForRGBA(fillBuf)
	fc.SetFillRuleEvenOdd()
	fc.SetColor(pal.Surface())
	fc.DrawCircle(cx, cy, outerR)
	fc.DrawCircle(cx, cy, innerR)
	fc.Fill()
	draw.Draw(canvas, canvas.Bounds(), fillBuf, image.Point{}, draw.Over)
}

// menuDrawRaisedAnnulus draws a raised annulus: dark+light shadow layers
// then a surface-filled ring on top.
func menuDrawRaisedAnnulus(canvas *image.RGBA, dc mancini.DrawContext, pal mancini.Palette,
	cx, cy, outerR, innerR float64, p mancini.RaisedParams) {

	x1, y1 := cx-outerR, cy-outerR
	x2, y2 := cx+outerR, cy+outerR
	maxOff := math.Max(p.DarkOff, p.LightOff)
	maxBlur := math.Max(p.DarkBlur, p.LightBlur)
	pad := maxOff + math.Ceil(maxBlur*3) + 2
	lw, lh, ox, oy := localRect(canvas, dc, x1, y1, x2, y2, pad)
	icx, icy := dc.TransformPoint(cx, cy)
	lcx, lcy := icx-ox, icy-oy
	dst := image.Rect(int(ox), int(oy), int(ox)+lw, int(oy)+lh)

	dark := annulusShadowLayer(lw, lh,
		lcx+p.DarkOff, lcy+p.DarkOff, outerR, innerR,
		pal.DarkShadow(), p.DarkAlpha, p.DarkBlur)
	draw.Draw(canvas, dst, dark, image.Point{}, draw.Over)

	light := annulusShadowLayer(lw, lh,
		lcx-p.LightOff, lcy-p.LightOff, outerR, innerR,
		pal.LightShadow(), p.LightAlpha, p.LightBlur)
	draw.Draw(canvas, dst, light, image.Point{}, draw.Over)

	// Fill annulus with surface color.
	fillBuf := image.NewRGBA(canvas.Bounds())
	fc := gg.NewContextForRGBA(fillBuf)
	fc.SetFillRuleEvenOdd()
	fc.SetColor(pal.Surface())
	fc.DrawCircle(cx, cy, outerR)
	fc.DrawCircle(cx, cy, innerR)
	fc.Fill()
	draw.Draw(canvas, canvas.Bounds(), fillBuf, image.Point{}, draw.Over)
}

// menuDrawInsetSegment erases the raised shadows in the selected segment's
// pie-wedge, then applies a tint overlay and inset shadows.
func menuDrawInsetSegment(canvas *image.RGBA, pal mancini.Palette,
	cx, cy, outerR, innerR, startAngle, endAngle float64,
	rp mancini.RaisedParams, ip mancini.InsetParams) {

	bounds := canvas.Bounds()
	w, h := bounds.Dx(), bounds.Dy()

	// Erase raised shadows in this segment by overdrawing with surface
	// color, clipped to the segment's pie wedge.
	shadowPad := math.Max(rp.DarkOff, rp.LightOff) +
		math.Ceil(math.Max(rp.DarkBlur, rp.LightBlur)*3) + 2
	eraseOuterR := outerR + shadowPad
	angleMargin := 0.02

	eraseBuf := image.NewRGBA(bounds)
	ec := gg.NewContextForRGBA(eraseBuf)
	ec.MoveTo(cx, cy)
	ec.DrawArc(cx, cy, eraseOuterR+1, startAngle-angleMargin, endAngle+angleMargin)
	ec.ClosePath()
	ec.Clip()
	ec.SetFillRuleEvenOdd()
	ec.SetColor(pal.Surface())
	ec.DrawCircle(cx, cy, eraseOuterR)
	ec.DrawCircle(cx, cy, innerR)
	ec.Fill()
	draw.Draw(canvas, bounds, eraseBuf, image.Point{}, draw.Over)

	// Segment mask.
	mask := arcSegmentMask(w, h, cx, cy, outerR, innerR, startAngle, endAngle)

	// Selected tint fill.
	tintBuf := image.NewRGBA(image.Rect(0, 0, w, h))
	tctx := gg.NewContextForRGBA(tintBuf)
	hi := pal.Highlight()
	tctx.SetColor(color.NRGBA{hi.R, hi.G, hi.B, 60})
	traceArcPath(tctx, cx, cy, outerR, innerR, startAngle, endAngle)
	tctx.Fill()
	draw.DrawMask(canvas, bounds, tintBuf, image.Point{}, mask, image.Point{}, draw.Over)

	// Inset shadows.
	off := ip.Off
	darkSh := arcShadowLayer(w, h, cx-off, cy-off, outerR, innerR,
		startAngle, endAngle, pal.DarkShadow(), 190, ip.DarkBlur)
	draw.DrawMask(canvas, bounds, darkSh, image.Point{}, mask, image.Point{}, draw.Over)

	lightSh := arcShadowLayer(w, h, cx+off, cy+off, outerR, innerR,
		startAngle, endAngle, pal.LightShadow(), 190, ip.LightBlur)
	draw.DrawMask(canvas, bounds, lightSh, image.Point{}, mask, image.Point{}, draw.Over)
}

// menuDrawGrooves draws radial groove lines at all n segment boundaries,
// masked to the annulus shape.
func menuDrawGrooves(canvas *image.RGBA, pal mancini.Palette,
	cx, cy, outerR, innerR float64, n int) {

	bounds := canvas.Bounds()
	w, h := bounds.Dx(), bounds.Dy()

	// Annulus mask.
	maskRGBA := image.NewRGBA(image.Rect(0, 0, w, h))
	mc := gg.NewContextForRGBA(maskRGBA)
	mc.SetFillRuleEvenOdd()
	mc.SetColor(color.White)
	mc.DrawCircle(cx, cy, outerR)
	mc.DrawCircle(cx, cy, innerR)
	mc.Fill()
	mask := image.NewAlpha(image.Rect(0, 0, w, h))
	for my := 0; my < h; my++ {
		for mx := 0; mx < w; mx++ {
			mask.Pix[my*mask.Stride+mx] = maskRGBA.Pix[my*maskRGBA.Stride+mx*4+3]
		}
	}

	darkSh := pal.DarkShadow()
	lightSh := pal.LightShadow()
	grooveOff := 0.8
	lineWidth := 1.5

	// Dark groove lines.
	darkBuf := image.NewRGBA(image.Rect(0, 0, w, h))
	ddc := gg.NewContextForRGBA(darkBuf)
	ddc.SetColor(color.NRGBA{darkSh.R, darkSh.G, darkSh.B, 150})
	ddc.SetLineWidth(lineWidth)
	for i := 0; i < n; i++ {
		angle := -math.Pi/2 + float64(i)*2*math.Pi/float64(n)
		cos, sin := math.Cos(angle), math.Sin(angle)
		ddc.DrawLine(
			cx+innerR*cos-grooveOff, cy+innerR*sin-grooveOff,
			cx+outerR*cos-grooveOff, cy+outerR*sin-grooveOff,
		)
		ddc.Stroke()
	}
	darkNRGBA := rgbaToNRGBA(darkBuf)
	darkNRGBA = gaussianBlurNRGBA(darkNRGBA, 1.5)
	draw.DrawMask(canvas, bounds, darkNRGBA, image.Point{}, mask, image.Point{}, draw.Over)

	// Light groove lines.
	lightBuf := image.NewRGBA(image.Rect(0, 0, w, h))
	ldc := gg.NewContextForRGBA(lightBuf)
	ldc.SetColor(color.NRGBA{lightSh.R, lightSh.G, lightSh.B, 150})
	ldc.SetLineWidth(lineWidth)
	for i := 0; i < n; i++ {
		angle := -math.Pi/2 + float64(i)*2*math.Pi/float64(n)
		cos, sin := math.Cos(angle), math.Sin(angle)
		ldc.DrawLine(
			cx+innerR*cos+grooveOff, cy+innerR*sin+grooveOff,
			cx+outerR*cos+grooveOff, cy+outerR*sin+grooveOff,
		)
		ldc.Stroke()
	}
	lightNRGBA := rgbaToNRGBA(lightBuf)
	lightNRGBA = gaussianBlurNRGBA(lightNRGBA, 1.0)
	draw.DrawMask(canvas, bounds, lightNRGBA, image.Point{}, mask, image.Point{}, draw.Over)
}
