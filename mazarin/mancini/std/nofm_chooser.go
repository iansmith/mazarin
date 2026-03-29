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

// NOfMChooser is a strip-based N-of-M chooser interactor. It displays N
// vertical strips side by side, each with a flat top edge and rounded
// bottom corners. Any combination of strips can be selected. Selected
// strips receive a purple tint overlay and inset shadows matching the
// neumorphic inset treatment.
type NOfMChooser struct {
	impl.ThemedInteractor

	StripW   float64              // width of each strip
	StripH   float64              // height of each strip
	Faces    []mancini.FaceDrawer // content drawing for each strip (nil entries OK)
	Selected []bool               // which strips are selected
	CornerR  float64              // corner radius for rounded bottom (default 8.0)
}

// selectedBg is the tint color for selected strips.
var selectedBg = color.NRGBA{200, 100, 255, 255}

// NewNOfMChooser creates an NOfMChooser wired to the constraint system
// and theme. layout must already be created (e.g. via
// mancini.NewLayoutAttributes).
func NewNOfMChooser(layout *mancini.LayoutAttributes, theme mancini.Theme,
	stripW, stripH float64,
	faces []mancini.FaceDrawer, selected []bool) *NOfMChooser {

	c := &NOfMChooser{
		StripW:   stripW,
		StripH:   stripH,
		Faces:    faces,
		Selected: selected,
		CornerR:  8.0,
	}
	c.ThemedInteractor.Init(c, layout, theme)
	return c
}

// NewNOfMChooserNamed creates an NOfMChooser with layout built from
// name + parent strings. Width is set to len(faces)*stripW and height
// to stripH so the constraint system can bootstrap.
func NewNOfMChooserNamed(myName, parent string, theme mancini.Theme,
	stripW, stripH float64,
	faces []mancini.FaceDrawer, selected []bool) *NOfMChooser {

	if myName == "" {
		myName = mancini.DefaultName("nofmchooser")
	}
	lh := mancini.NewLayoutAttributes(myName, parent)
	lh.Width.Set(int64(float64(len(faces)) * stripW))
	lh.Height.Set(int64(stripH))
	return NewNOfMChooser(lh, theme, stripW, stripH, faces, selected)
}

// Draw implements mancini.NewDrawer. It renders the strip chooser:
// an overall shape with flat top and rounded bottom, flush edge
// outlines, vertical groove separators between strips, selected-strip
// tint+inset treatment, and per-strip face content.
func (c *NOfMChooser) Draw(self mancini.Interactor, x, y, w, h int64) {
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

	totalW := float64(n) * c.StripW
	fx, fy := float64(x), float64(y)
	fw, fh := float64(w), float64(h)

	// Center the strips horizontally within the allocated bounds.
	startX := fx + (fw-totalW)/2
	stripH := c.StripH
	if stripH > fh {
		stripH = fh
	}
	r := c.CornerR

	// ── 1. Draw overall outline shape ────────────────────────────────
	drawFlatTopRoundedBottom(dc, pal, startX, fy, totalW, stripH, r)

	// ── 2–4. Neumorphic effects (flush edge, grooves, selection) ─────
	if params != nil {
		drawFlatTopFlushEdge(canvas, pal, startX, fy, totalW, stripH, r, params.Flush)

		for i := 1; i < n; i++ {
			gx := startX + float64(i)*c.StripW
			neuInset(pal, dc, gx-1, fy+2, gx+2, fy+stripH-r-2, 1, pal.Surface(), mancini.GrooveParams)
		}

		for i := 0; i < n; i++ {
			if i >= len(c.Selected) || !c.Selected[i] {
				continue
			}
			sx := startX + float64(i)*c.StripW
			applyStripSelection(canvas, pal, dc, sx, fy, c.StripW, stripH, r, i, n, params.Inset)
		}
	}

	// ── 5. Draw face content for each strip ──────────────────────────
	for i := 0; i < n; i++ {
		if c.Faces[i] == nil {
			continue
		}
		sx := startX + float64(i)*c.StripW
		c.Faces[i](dc, sx, fy, c.StripW, stripH)
	}
}

// drawFlatTopRoundedBottom fills a path with flat top and rounded bottom
// corners using the palette surface color.
func drawFlatTopRoundedBottom(dc mancini.DrawContext, pal mancini.Palette,
	x, y, w, h, r float64) {

	canvas := dc.Image().(*image.RGBA)
	rgba := image.NewRGBA(canvas.Bounds())
	ctx := gg.NewContextForRGBA(rgba)
	ctx.SwapRB = pal.SwapRB()

	flatTopRoundedBottomPath(ctx, x, y, w, h, r)
	ctx.SetColor(pal.Surface())
	ctx.Fill()

	draw.Draw(canvas, canvas.Bounds(), rgba, image.Point{}, draw.Over)
}

// flatTopRoundedBottomPath traces a path with flat top edge and rounded
// bottom-left and bottom-right corners. The arc at each bottom corner
// is approximated with line segments.
func flatTopRoundedBottomPath(ctx *gg.Context, x, y, w, h, r float64) {
	if r > h/2 {
		r = h / 2
	}
	if r > w/2 {
		r = w / 2
	}

	// Start at top-left, go clockwise.
	ctx.MoveTo(x, y)
	ctx.LineTo(x+w, y)                 // top edge (flat)
	ctx.LineTo(x+w, y+h-r)             // right edge down to bottom-right arc
	arcSegments(ctx, x+w-r, y+h-r, r, 0, math.Pi/2)    // bottom-right corner
	ctx.LineTo(x+r, y+h)               // bottom edge
	arcSegments(ctx, x+r, y+h-r, r, math.Pi/2, math.Pi) // bottom-left corner
	ctx.ClosePath()
}

// arcSegments approximates a circular arc from angle startA to endA
// (in radians, measured clockwise from the +X axis) centered at (cx, cy)
// with radius r. Uses 16 segments per quarter-turn for smooth curves.
func arcSegments(ctx *gg.Context, cx, cy, r, startA, endA float64) {
	steps := 16
	for i := 0; i <= steps; i++ {
		t := float64(i) / float64(steps)
		a := startA + t*(endA-startA)
		px := cx + r*math.Cos(a)
		py := cy + r*math.Sin(a)
		ctx.LineTo(px, py)
	}
}

// drawFlatTopFlushEdge draws a flush-style edge (dark edge + light edge
// with groove effect) around the flat-top-rounded-bottom outline.
func drawFlatTopFlushEdge(canvas *image.RGBA, pal mancini.Palette,
	x, y, w, h, r float64, p mancini.FlushParams) {

	pad := p.EdgeW + 2
	cb := canvas.Bounds()
	ox := math.Floor(x - pad)
	oy := math.Floor(y - pad)
	ex := math.Ceil(x + w + pad)
	ey := math.Ceil(y + h + pad)
	if ox < float64(cb.Min.X) {
		ox = float64(cb.Min.X)
	}
	if oy < float64(cb.Min.Y) {
		oy = float64(cb.Min.Y)
	}
	if ex > float64(cb.Max.X) {
		ex = float64(cb.Max.X)
	}
	if ey > float64(cb.Max.Y) {
		ey = float64(cb.Max.Y)
	}
	lw := int(ex - ox)
	lh := int(ey - oy)
	lx := x - ox
	ly := y - oy
	dst := image.Rect(int(ox), int(oy), int(ox)+lw, int(oy)+lh)

	darkSh := pal.DarkShadow()
	lightSh := pal.LightShadow()

	// Dark edge.
	darkEdge := image.NewRGBA(image.Rect(0, 0, lw, lh))
	ddc := gg.NewContextForRGBA(darkEdge)
	ddc.SwapRB = pal.SwapRB()
	ddc.SetColor(color.NRGBA{darkSh.R, darkSh.G, darkSh.B, p.EdgeAlpha})
	ddc.SetLineWidth(p.EdgeW)
	flatTopRoundedBottomPath(ddc, lx, ly, w, h, r)
	ddc.Stroke()
	draw.Draw(canvas, dst, darkEdge, image.Point{}, draw.Over)

	// Light edge (offset +1, +1, masked to shape).
	lightEdge := image.NewRGBA(image.Rect(0, 0, lw, lh))
	ldc := gg.NewContextForRGBA(lightEdge)
	ldc.SwapRB = pal.SwapRB()
	ldc.SetColor(color.NRGBA{lightSh.R, lightSh.G, lightSh.B, p.EdgeAlpha})
	ldc.SetLineWidth(p.EdgeW)
	flatTopRoundedBottomPath(ldc, lx+1, ly+1, w, h, r)
	ldc.Stroke()

	// Mask to the shape so the light edge doesn't leak outside.
	maskRGBA := image.NewRGBA(image.Rect(0, 0, lw, lh))
	mdc := gg.NewContextForRGBA(maskRGBA)
	mdc.SetColor(color.White)
	flatTopRoundedBottomPath(mdc, lx, ly, w, h, r)
	mdc.Fill()
	mask := image.NewAlpha(image.Rect(0, 0, lw, lh))
	for my := 0; my < lh; my++ {
		for mx := 0; mx < lw; mx++ {
			mask.Pix[my*mask.Stride+mx] = maskRGBA.Pix[my*maskRGBA.Stride+mx*4+3]
		}
	}
	draw.DrawMask(canvas, dst, lightEdge, image.Point{}, mask, image.Point{}, draw.Over)
}

// applyStripSelection draws the selected-strip treatment: a purple tint
// overlay and inset shadows, masked to the strip's column area.
func applyStripSelection(canvas *image.RGBA, pal mancini.Palette,
	dc mancini.DrawContext, sx, sy, sw, sh, cornerR float64,
	stripIdx, stripCount int, ip mancini.InsetParams) {

	// Determine which corners are rounded for this strip.
	// Only the leftmost strip has a rounded bottom-left; only the rightmost
	// has a rounded bottom-right. The top is always flat.
	leftR := 0.0
	rightR := 0.0
	if stripIdx == 0 {
		leftR = cornerR
	}
	if stripIdx == stripCount-1 {
		rightR = cornerR
	}

	darkSh := pal.DarkShadow()
	lightSh := pal.LightShadow()

	// Tint overlay.
	pad := 2.0
	lw, lh, ox, oy := localRect(canvas, sx, sy, sx+sw, sy+sh, pad)
	lsx := sx - ox
	lsy := sy - oy

	overlay := image.NewRGBA(image.Rect(0, 0, lw, lh))
	odc := gg.NewContextForRGBA(overlay)
	odc.SwapRB = pal.SwapRB()
	odc.SetColor(color.NRGBA{selectedBg.R, selectedBg.G, selectedBg.B, 60})
	stripColumnPath(odc, lsx, lsy, sw, sh, leftR, rightR)
	odc.Fill()
	dst := image.Rect(int(ox), int(oy), int(ox)+lw, int(oy)+lh)
	draw.Draw(canvas, dst, overlay, image.Point{}, draw.Over)

	// Inset shadows masked to strip column.
	maxBlur := math.Max(ip.DarkBlur, ip.LightBlur)
	ipad := ip.Off + math.Ceil(maxBlur*3) + 2
	ilw, ilh, iox, ioy := localRect(canvas, sx, sy, sx+sw, sy+sh, ipad)
	ilsx := sx - iox
	ilsy := sy - ioy
	idst := image.Rect(int(iox), int(ioy), int(iox)+ilw, int(ioy)+ilh)

	// Build mask for this strip column.
	maskRGBA := image.NewRGBA(image.Rect(0, 0, ilw, ilh))
	mdc := gg.NewContextForRGBA(maskRGBA)
	mdc.SetColor(color.White)
	stripColumnPath(mdc, ilsx, ilsy, sw, sh, leftR, rightR)
	mdc.Fill()
	mask := image.NewAlpha(image.Rect(0, 0, ilw, ilh))
	for my := 0; my < ilh; my++ {
		for mx := 0; mx < ilw; mx++ {
			mask.Pix[my*mask.Stride+mx] = maskRGBA.Pix[my*maskRGBA.Stride+mx*4+3]
		}
	}

	// Dark shadow (top-left bias).
	darkShLayer := shadowLayer(ilw, ilh,
		ilsx-ip.Off, ilsy-ip.Off, ilsx+sw-ip.Off, ilsy+sh-ip.Off,
		0, darkSh, 190, ip.DarkBlur, pal.SwapRB())
	draw.DrawMask(canvas, idst, darkShLayer, image.Point{}, mask, image.Point{}, draw.Over)

	// Light shadow (bottom-right bias).
	lightShLayer := shadowLayer(ilw, ilh,
		ilsx+ip.Off, ilsy+ip.Off, ilsx+sw+ip.Off, ilsy+sh+ip.Off,
		0, lightSh, 190, ip.LightBlur, pal.SwapRB())
	draw.DrawMask(canvas, idst, lightShLayer, image.Point{}, mask, image.Point{}, draw.Over)
}

// stripColumnPath traces a path for a single strip column. The top edge
// is always flat. leftR and rightR control the bottom-left and
// bottom-right corner radii independently, so only the outermost
// strips of the chooser get rounded corners.
func stripColumnPath(ctx *gg.Context, x, y, w, h, leftR, rightR float64) {
	ctx.MoveTo(x, y)
	ctx.LineTo(x+w, y) // flat top

	// Right side down.
	if rightR > 0 {
		ctx.LineTo(x+w, y+h-rightR)
		arcSegments(ctx, x+w-rightR, y+h-rightR, rightR, 0, math.Pi/2)
	} else {
		ctx.LineTo(x+w, y+h)
	}

	// Bottom edge.
	if leftR > 0 {
		ctx.LineTo(x+leftR, y+h)
		arcSegments(ctx, x+leftR, y+h-leftR, leftR, math.Pi/2, math.Pi)
	} else {
		ctx.LineTo(x, y+h)
	}

	ctx.ClosePath()
}
