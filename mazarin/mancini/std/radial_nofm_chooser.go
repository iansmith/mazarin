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

// RadialNOfMChooser is an arc-shaped N-of-M multi-select interactor. It
// draws segments of an annulus (partial ring). Each segment can be
// independently selected. The center of the arc, angular range, and
// radii are configurable.
//
// RadialNOfMChooser embeds [impl.ThemedInteractor] and uses Light-weight
// [mancini.NeuParams]. Selected segments receive a purple tint overlay
// and [mancini.Inset] shadows, matching the treatment used by
// [NOfMChooser]. When [mancini.NeuParams] is nil, only the filled arc
// and face content are drawn.
//
// Each segment can have a [mancini.FaceDrawer] callback whose content is
// automatically rotated to align with the segment's angular position.
// See also [NOfMChooser] for a linear strip variant and [RadialMenu]
// for a full-circle single-select menu.
type RadialNOfMChooser struct {
	impl.ThemedInteractor

	CX, CY          float64              // center of the arc relative to allocated area
	InnerR, OuterR   float64              // inner and outer radii
	StartDeg, EndDeg float64              // angular range in degrees (0=right, 90=down)
	Faces            []mancini.FaceDrawer // content for each segment (nil entries OK)
	Selected         []bool               // which segments are selected
}

// NewRadialNOfMChooser creates a RadialNOfMChooser wired to the constraint
// system and theme. layout must already be created.
func NewRadialNOfMChooser(layout *mancini.LayoutAttributes, theme mancini.Theme,
	cx, cy, innerR, outerR, startDeg, endDeg float64,
	faces []mancini.FaceDrawer, selected []bool) *RadialNOfMChooser {

	c := &RadialNOfMChooser{
		CX:       cx,
		CY:       cy,
		InnerR:   innerR,
		OuterR:   outerR,
		StartDeg: startDeg,
		EndDeg:   endDeg,
		Faces:    faces,
		Selected: selected,
	}
	c.ThemedInteractor.Init(c, layout, theme)
	return c
}

// NewRadialNOfMChooserNamed creates a RadialNOfMChooser with layout built
// from name + parent strings. Width and height are computed from the arc's
// bounding box so the constraint system can bootstrap.
func NewRadialNOfMChooserNamed(myName, parent string, theme mancini.Theme,
	cx, cy, innerR, outerR, startDeg, endDeg float64,
	faces []mancini.FaceDrawer, selected []bool) *RadialNOfMChooser {

	if myName == "" {
		myName = mancini.DefaultName("radialchooser")
	}
	lh := mancini.NewLayoutAttributes(myName, parent)

	// Compute bounding box of the arc to set intrinsic dimensions.
	startRad := startDeg * math.Pi / 180
	endRad := endDeg * math.Pi / 180
	bw, bh := arcBoundingBox(cx, cy, outerR, startRad, endRad)
	lh.Width.Set(int64(math.Ceil(bw)))
	lh.Height.Set(int64(math.Ceil(bh)))

	return NewRadialNOfMChooser(lh, theme, cx, cy, innerR, outerR,
		startDeg, endDeg, faces, selected)
}

// arcBoundingBox computes the width and height needed to contain an arc
// centered at (cx, cy) with outer radius r spanning [startRad, endRad].
func arcBoundingBox(cx, cy, r, startRad, endRad float64) (w, h float64) {
	// Sample the arc at fine intervals plus check axis crossings.
	minX, minY := cx+r*math.Cos(startRad), cy+r*math.Sin(startRad)
	maxX, maxY := minX, minY

	steps := 64
	for i := 0; i <= steps; i++ {
		a := startRad + (endRad-startRad)*float64(i)/float64(steps)
		px := cx + r*math.Cos(a)
		py := cy + r*math.Sin(a)
		if px < minX {
			minX = px
		}
		if px > maxX {
			maxX = px
		}
		if py < minY {
			minY = py
		}
		if py > maxY {
			maxY = py
		}
	}
	// Include the center point (for groove lines drawn from inner radius).
	if cx < minX {
		minX = cx
	}
	if cx > maxX {
		maxX = cx
	}
	if cy < minY {
		minY = cy
	}
	if cy > maxY {
		maxY = cy
	}

	pad := 20.0 // padding for shadows
	return maxX - minX + 2*pad, maxY - minY + 2*pad
}

// Draw implements mancini.NewDrawer. It renders the radial arc chooser:
// an annular arc shape with flush edge outlines, radial groove separators
// between segments, selected-segment tint+inset treatment, and per-segment
// face content.
func (c *RadialNOfMChooser) Draw(self mancini.Interactor, x, y, w, h int64) {
	if !self.Visible() {
		return
	}
	dc := self.DC()
	if dc == nil {
		return
	}
	canvas := dc.Image().(*image.RGBA)

	pal := c.Theme().Palette()
	var params *mancini.NeuParams
	if neu := c.Theme().Neumorphic(); neu != nil {
		params = neu.Light()
	}

	n := len(c.Faces)
	if n == 0 {
		return
	}

	fx, fy := float64(x), float64(y)
	cx := fx + c.CX
	cy := fy + c.CY

	startRad := c.StartDeg * math.Pi / 180
	endRad := c.EndDeg * math.Pi / 180
	totalAngle := endRad - startRad
	segAngle := totalAngle / float64(n)

	rOuter := c.OuterR
	rInner := c.InnerR

	// ── 1. Draw the overall arc shape filled with surface color ──────
	radialDrawFilledArc(canvas, pal, cx, cy, rOuter, rInner, startRad, endRad)

	// ── 2–4. Neumorphic effects (flush edge, grooves, selection) ─────
	if params != nil {
		radialDrawFlushEdge(canvas, pal, cx, cy, rOuter, rInner, startRad, endRad, params.Flush)
		radialDrawGrooves(canvas, pal, cx, cy, rOuter, rInner, startRad, segAngle, n)

		for i := 0; i < n; i++ {
			if i >= len(c.Selected) || !c.Selected[i] {
				continue
			}
			segStart := startRad + float64(i)*segAngle
			segEnd := segStart + segAngle
			radialApplySelection(canvas, pal, cx, cy, rOuter, rInner, segStart, segEnd, params.Inset)
		}
	}

	// ── 5. Draw face content for each segment ────────────────────────
	for i := 0; i < n; i++ {
		if c.Faces[i] == nil {
			continue
		}
		segStart := startRad + float64(i)*segAngle
		segEnd := segStart + segAngle
		radialDrawFace(dc, canvas, pal, cx, cy, rOuter, rInner, segStart, segEnd, c.Faces[i])
	}
}

// ── Arc path tracing ────────────────────────────────────────────────────────

// arcPathSteps is the number of line segments used to approximate each arc.
const arcPathSteps = 64

// traceArcPath traces an annular arc path on a gg.Context: outer arc forward
// from startAngle to endAngle, then inner arc reversed, forming a closed
// annular segment shape.
func traceArcPath(ctx *gg.Context, cx, cy, rOuter, rInner, startAngle, endAngle float64) {
	// Outer arc forward.
	for i := 0; i <= arcPathSteps; i++ {
		a := startAngle + (endAngle-startAngle)*float64(i)/float64(arcPathSteps)
		px := cx + rOuter*math.Cos(a)
		py := cy + rOuter*math.Sin(a)
		if i == 0 {
			ctx.MoveTo(px, py)
		} else {
			ctx.LineTo(px, py)
		}
	}
	// Inner arc reversed.
	for i := arcPathSteps; i >= 0; i-- {
		a := startAngle + (endAngle-startAngle)*float64(i)/float64(arcPathSteps)
		ctx.LineTo(cx+rInner*math.Cos(a), cy+rInner*math.Sin(a))
	}
	ctx.ClosePath()
}

// ── Filled arc shape ────────────────────────────────────────────────────────

// radialDrawFilledArc fills the annular arc shape with the palette surface color.
func radialDrawFilledArc(canvas *image.RGBA, pal mancini.Palette,
	cx, cy, rOuter, rInner, startAngle, endAngle float64) {

	rgba := image.NewRGBA(canvas.Bounds())
	ctx := gg.NewContextForRGBA(rgba)
	ctx.SwapRB = pal.SwapRB()
	traceArcPath(ctx, cx, cy, rOuter, rInner, startAngle, endAngle)
	ctx.SetColor(pal.Surface())
	ctx.Fill()
	draw.Draw(canvas, canvas.Bounds(), rgba, image.Point{}, draw.Over)
}

// ── Flush edge ──────────────────────────────────────────────────────────────

// radialDrawFlushEdge draws a flush-style dark+light edge around the arc shape.
func radialDrawFlushEdge(canvas *image.RGBA, pal mancini.Palette,
	cx, cy, rOuter, rInner, startAngle, endAngle float64, p mancini.FlushParams) {

	bounds := canvas.Bounds()
	w, h := bounds.Dx(), bounds.Dy()

	darkSh := pal.DarkShadow()
	lightSh := pal.LightShadow()

	// Dark edge.
	darkEdge := image.NewRGBA(image.Rect(0, 0, w, h))
	ddc := gg.NewContextForRGBA(darkEdge)
	ddc.SwapRB = pal.SwapRB()
	ddc.SetColor(color.NRGBA{darkSh.R, darkSh.G, darkSh.B, p.EdgeAlpha})
	ddc.SetLineWidth(p.EdgeW)
	traceArcPath(ddc, cx, cy, rOuter, rInner, startAngle, endAngle)
	ddc.Stroke()
	draw.Draw(canvas, bounds, darkEdge, image.Point{}, draw.Over)

	// Light edge (offset +1, +1, masked to shape).
	lightEdge := image.NewRGBA(image.Rect(0, 0, w, h))
	ldc := gg.NewContextForRGBA(lightEdge)
	ldc.SwapRB = pal.SwapRB()
	ldc.SetColor(color.NRGBA{lightSh.R, lightSh.G, lightSh.B, p.EdgeAlpha})
	ldc.SetLineWidth(p.EdgeW)
	traceArcPath(ldc, cx+1, cy+1, rOuter, rInner, startAngle, endAngle)
	ldc.Stroke()

	// Mask to the arc shape so the light edge doesn't leak outside.
	mask := arcSegmentMask(w, h, cx, cy, rOuter, rInner, startAngle, endAngle)
	draw.DrawMask(canvas, bounds, lightEdge, image.Point{}, mask, image.Point{}, draw.Over)
}

// ── Groove separators ───────────────────────────────────────────────────────

// radialDrawGrooves draws radial groove lines between segments, masked to
// the overall arc shape.
func radialDrawGrooves(canvas *image.RGBA, pal mancini.Palette,
	cx, cy, rOuter, rInner, startAngle, segAngle float64, n int) {

	bounds := canvas.Bounds()
	w, h := bounds.Dx(), bounds.Dy()

	// Mask to overall arc shape.
	endAngle := startAngle + segAngle*float64(n)
	mask := arcSegmentMask(w, h, cx, cy, rOuter, rInner, startAngle, endAngle)

	darkSh := pal.DarkShadow()
	lightSh := pal.LightShadow()

	grooveOff := 0.8
	lineWidth := 1.5

	// Dark groove lines.
	darkBuf := image.NewRGBA(image.Rect(0, 0, w, h))
	ddc := gg.NewContextForRGBA(darkBuf)
	ddc.SwapRB = pal.SwapRB()
	ddc.SetColor(color.NRGBA{darkSh.R, darkSh.G, darkSh.B, 150})
	ddc.SetLineWidth(lineWidth)
	for i := 1; i < n; i++ {
		angle := startAngle + float64(i)*segAngle
		cos, sin := math.Cos(angle), math.Sin(angle)
		ddc.DrawLine(
			cx+rInner*cos-grooveOff, cy+rInner*sin-grooveOff,
			cx+rOuter*cos-grooveOff, cy+rOuter*sin-grooveOff,
		)
		ddc.Stroke()
	}
	darkNRGBA := rgbaToNRGBA(darkBuf)
	darkNRGBA = gaussianBlurNRGBA(darkNRGBA, 1.5)
	draw.DrawMask(canvas, bounds, darkNRGBA, image.Point{}, mask, image.Point{}, draw.Over)

	// Light groove lines.
	lightBuf := image.NewRGBA(image.Rect(0, 0, w, h))
	ldc := gg.NewContextForRGBA(lightBuf)
	ldc.SwapRB = pal.SwapRB()
	ldc.SetColor(color.NRGBA{lightSh.R, lightSh.G, lightSh.B, 150})
	ldc.SetLineWidth(lineWidth)
	for i := 1; i < n; i++ {
		angle := startAngle + float64(i)*segAngle
		cos, sin := math.Cos(angle), math.Sin(angle)
		ldc.DrawLine(
			cx+rInner*cos+grooveOff, cy+rInner*sin+grooveOff,
			cx+rOuter*cos+grooveOff, cy+rOuter*sin+grooveOff,
		)
		ldc.Stroke()
	}
	lightNRGBA := rgbaToNRGBA(lightBuf)
	lightNRGBA = gaussianBlurNRGBA(lightNRGBA, 1.0)
	draw.DrawMask(canvas, bounds, lightNRGBA, image.Point{}, mask, image.Point{}, draw.Over)
}

// ── Selected segment treatment ──────────────────────────────────────────────

// radialApplySelection draws the selected-segment treatment: purple tint
// overlay and inset shadows, masked to the segment's annular area.
func radialApplySelection(canvas *image.RGBA, pal mancini.Palette,
	cx, cy, rOuter, rInner, startAngle, endAngle float64, ip mancini.InsetParams) {

	bounds := canvas.Bounds()
	w, h := bounds.Dx(), bounds.Dy()

	// Segment mask.
	mask := arcSegmentMask(w, h, cx, cy, rOuter, rInner, startAngle, endAngle)

	// Purple tint fill, masked to segment.
	tintBuf := image.NewRGBA(image.Rect(0, 0, w, h))
	tctx := gg.NewContextForRGBA(tintBuf)
	tctx.SwapRB = pal.SwapRB()
	tctx.SetColor(color.NRGBA{200, 100, 255, 60})
	traceArcPath(tctx, cx, cy, rOuter, rInner, startAngle, endAngle)
	tctx.Fill()
	draw.DrawMask(canvas, bounds, tintBuf, image.Point{}, mask, image.Point{}, draw.Over)

	off := ip.Off
	darkBlur := ip.DarkBlur
	lightBlur := ip.LightBlur

	// Dark inset shadow (offset upper-left, blurred, masked to segment).
	darkSh := arcShadowLayer(w, h, cx-off, cy-off, rOuter, rInner,
		startAngle, endAngle, pal.DarkShadow(), 190, darkBlur, pal.SwapRB())
	draw.DrawMask(canvas, bounds, darkSh, image.Point{}, mask, image.Point{}, draw.Over)

	// Light inset shadow (offset lower-right, blurred, masked to segment).
	lightSh := arcShadowLayer(w, h, cx+off, cy+off, rOuter, rInner,
		startAngle, endAngle, pal.LightShadow(), 190, lightBlur, pal.SwapRB())
	draw.DrawMask(canvas, bounds, lightSh, image.Point{}, mask, image.Point{}, draw.Over)
}

// ── Face content rendering ──────────────────────────────────────────────────

// radialDrawFace renders a FaceDrawer's content positioned at the midpoint
// of the segment's angular and radial range. The content is drawn into a
// temporary buffer, rotated to align with the segment's midpoint angle,
// and composited onto the canvas masked to the segment area.
func radialDrawFace(dc mancini.DrawContext, canvas *image.RGBA, pal mancini.Palette,
	cx, cy, rOuter, rInner, startAngle, endAngle float64, face mancini.FaceDrawer) {

	midAngle := (startAngle + endAngle) / 2
	midR := (rInner + rOuter) / 2
	radialH := rOuter - rInner

	// Arc length at the midpoint radius for the segment.
	arcLen := midR * math.Abs(endAngle-startAngle)

	// Compute the position where the face center should land.
	faceCX := cx + midR*math.Cos(midAngle)
	faceCY := cy + midR*math.Sin(midAngle)

	// Render face content into a temporary buffer centered at origin.
	// The face draws as if it were a horizontal strip: width=arcLen, height=radialH.
	bufW := int(math.Ceil(arcLen)) + 4
	bufH := int(math.Ceil(radialH)) + 4
	if bufW < 4 || bufH < 4 {
		return
	}

	faceBuf := image.NewRGBA(image.Rect(0, 0, bufW, bufH))
	faceCtx := gg.NewContextForRGBA(faceBuf)
	faceCtx.SwapRB = pal.SwapRB()
	face(faceCtx, 2, 2, arcLen, radialH)

	// Rotate the face buffer to align with the segment angle.
	faceNRGBA := rgbaToNRGBA(faceBuf)
	rotAngle := midAngle + math.Pi/2 // rotate from horizontal to radial orientation
	// Flip for segments in the lower half to keep text readable.
	if midAngle > math.Pi/2 && midAngle < 3*math.Pi/2 {
		rotAngle += math.Pi
	}
	rotated := rotateNRGBA(faceNRGBA, rotAngle)

	// Composite rotated face at the segment midpoint, masked to segment.
	bounds := canvas.Bounds()
	cw, ch := bounds.Dx(), bounds.Dy()
	mask := arcSegmentMask(cw, ch, cx, cy, rOuter, rInner, startAngle, endAngle)

	rBounds := rotated.Bounds()
	rw, rh := rBounds.Dx(), rBounds.Dy()
	destX := int(faceCX) - rw/2
	destY := int(faceCY) - rh/2
	destRect := image.Rect(destX, destY, destX+rw, destY+rh)
	draw.DrawMask(canvas, destRect, rotated, image.Point{},
		mask, image.Pt(destX, destY), draw.Over)
}

// ── Segment mask helper ─────────────────────────────────────────────────────

// arcSegmentMask creates an alpha mask of the annular arc segment shape.
func arcSegmentMask(w, h int, cx, cy, rOuter, rInner, startAngle, endAngle float64) *image.Alpha {
	rgba := image.NewRGBA(image.Rect(0, 0, w, h))
	ctx := gg.NewContextForRGBA(rgba)
	ctx.SetColor(color.White)
	traceArcPath(ctx, cx, cy, rOuter, rInner, startAngle, endAngle)
	ctx.Fill()
	mask := image.NewAlpha(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			mask.Pix[y*mask.Stride+x] = rgba.Pix[y*rgba.Stride+x*4+3]
		}
	}
	return mask
}

// ── Arc shadow layer helper ─────────────────────────────────────────────────

// arcShadowLayer renders a colored annular arc shape into a temporary NRGBA
// buffer, optionally blurred. Used for inset shadow effects.
func arcShadowLayer(w, h int, cx, cy, rOuter, rInner, startAngle, endAngle float64,
	c color.NRGBA, alpha uint8, blur float64, swapRB bool) *image.NRGBA {

	rgba := image.NewRGBA(image.Rect(0, 0, w, h))
	ctx := gg.NewContextForRGBA(rgba)
	ctx.SwapRB = swapRB
	ctx.SetColor(color.NRGBA{c.R, c.G, c.B, alpha})
	traceArcPath(ctx, cx, cy, rOuter, rInner, startAngle, endAngle)
	ctx.Fill()
	nrgba := rgbaToNRGBA(rgba)
	if blur > 0 {
		return gaussianBlurNRGBA(nrgba, blur)
	}
	return nrgba
}

// ── Image rotation helper ───────────────────────────────────────────────────

// rotateNRGBA rotates an NRGBA image by the given angle (radians) around its
// center using bilinear interpolation. The output bounds are expanded to
// contain the full rotated image.
func rotateNRGBA(src *image.NRGBA, angle float64) *image.NRGBA {
	sb := src.Bounds()
	sw, sh := sb.Dx(), sb.Dy()
	scx := float64(sw) / 2
	scy := float64(sh) / 2

	cosA := math.Cos(angle)
	sinA := math.Sin(angle)

	// Compute output bounds by rotating the four corners.
	corners := [4][2]float64{
		{0, 0}, {float64(sw), 0}, {float64(sw), float64(sh)}, {0, float64(sh)},
	}
	var minX, minY, maxX, maxY float64
	for i, c := range corners {
		rx := (c[0]-scx)*cosA - (c[1]-scy)*sinA + scx
		ry := (c[0]-scx)*sinA + (c[1]-scy)*cosA + scy
		if i == 0 {
			minX, maxX = rx, rx
			minY, maxY = ry, ry
		} else {
			if rx < minX {
				minX = rx
			}
			if rx > maxX {
				maxX = rx
			}
			if ry < minY {
				minY = ry
			}
			if ry > maxY {
				maxY = ry
			}
		}
	}

	dw := int(math.Ceil(maxX - minX))
	dh := int(math.Ceil(maxY - minY))
	if dw < 1 {
		dw = 1
	}
	if dh < 1 {
		dh = 1
	}
	dst := image.NewNRGBA(image.Rect(0, 0, dw, dh))
	dcx := float64(dw) / 2
	dcy := float64(dh) / 2

	// Inverse rotation: for each destination pixel, find source pixel.
	cosNeg := math.Cos(-angle)
	sinNeg := math.Sin(-angle)

	for dy := 0; dy < dh; dy++ {
		for dx := 0; dx < dw; dx++ {
			// Map destination pixel to source coordinates.
			rx := float64(dx) - dcx
			ry := float64(dy) - dcy
			sx := rx*cosNeg - ry*sinNeg + scx
			sy := rx*sinNeg + ry*cosNeg + scy

			// Bilinear interpolation.
			x0 := int(math.Floor(sx))
			y0 := int(math.Floor(sy))
			if x0 < 0 || x0 >= sw-1 || y0 < 0 || y0 >= sh-1 {
				continue
			}

			fx := sx - float64(x0)
			fy := sy - float64(y0)

			off00 := y0*src.Stride + x0*4
			off10 := y0*src.Stride + (x0+1)*4
			off01 := (y0+1)*src.Stride + x0*4
			off11 := (y0+1)*src.Stride + (x0+1)*4

			for ch := 0; ch < 4; ch++ {
				v00 := float64(src.Pix[off00+ch])
				v10 := float64(src.Pix[off10+ch])
				v01 := float64(src.Pix[off01+ch])
				v11 := float64(src.Pix[off11+ch])

				v := v00*(1-fx)*(1-fy) + v10*fx*(1-fy) + v01*(1-fx)*fy + v11*fx*fy
				dst.Pix[dy*dst.Stride+dx*4+ch] = clampU8(v)
			}
		}
	}
	return dst
}
