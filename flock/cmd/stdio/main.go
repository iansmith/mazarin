// stdio is a userspace priest that owns the serial port soft IRQ and
// renders kernel console output to a dark gray rectangle on the display.
// It loads the Atkinson Hyperlegible Mono font and displays serial
// output as lines of monospaced text.
package main

import (
	_ "embed"
	"fmt"
	"image"
	"image/color"
	"math"
	"runtime"
	"time"
	"unsafe"

	gg "github.com/fogleman/gg"
	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"

	"mazzy/mazarin/serial"
	"mazzy/mazarin/sys"
)

//go:embed AtkinsonHyperlegibleMono-Regular.otf
var fontData []byte

const (
	LineSpacing = 2
	pad         = 120
	textPad     = 8 // inner padding from rect edge to text

	// Neumorphic card dimensions
	cardBorderSide = 16 // left/right/bottom border width
	cardBorderTop  = 40 // title bar height
	shadowSize     = 12 // shadow offset in pixels

	// Glyph cache sizing
	glyphCacheLen = 512 // number of cache slots
	glyphKeyMin   = 3   // minimum word length to cache
	glyphKeyMax   = 12  // maximum word length to cache
)

// glyphCacheEntry holds a pre-rendered word as raw RGBA pixels.
type glyphCacheEntry struct {
	word   string // cached word (empty = unused slot)
	pixW   int    // pixel width = len(word) * charW
	pixH   int    // pixel height = charH
	pixels []byte // RGBA pixel data, pre-allocated to max size
	hits   int    // access count for LFU eviction
}

// glyphCache holds pre-rendered single-character glyph strips and a
// fully-associative word cache built by composing those glyphs.
// Single-char glyphs are the fallback; the word cache avoids repeated
// per-character blits for frequently seen words.
type glyphCache struct {
	glyphs    [95][]byte // printable ASCII 32-126 in nText color
	glyphsErr [95][]byte // printable ASCII 32-126 in nStderr color (dark red)
	entries   [glyphCacheLen]glyphCacheEntry
	charW     int
	charH     int
}

// token represents a segment of a line for cache-aware rendering.
type token struct {
	text      string
	col       int  // starting column index in the line
	cacheable bool // true if alphanumeric word in [glyphKeyMin, glyphKeyMax]
}

func isAlnum(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

// newGlyphCache pre-renders all printable ASCII characters into single-char
// pixel strips using DrawString on a temporary gg context, then pre-allocates
// all word cache entry buffers. No font rendering happens after init.
func newGlyphCache(charW, charH, ascent int, face font.Face) *glyphCache {
	gc := &glyphCache{charW: charW, charH: charH}

	renderGlyphSet(&gc.glyphs, charW, charH, ascent, face, nText, nContent)
	renderGlyphSet(&gc.glyphsErr, charW, charH, ascent, face, nStderr, nContent)

	// Pre-allocate word cache entry buffers
	maxBuf := glyphKeyMax * charW * charH * 4
	for i := range gc.entries {
		gc.entries[i].pixels = make([]byte, maxBuf)
	}
	return gc
}

// renderGlyphSet pre-renders all printable ASCII characters (32-126) into
// pixel strips with the given text color on the given background color.
func renderGlyphSet(dst *[95][]byte, charW, charH, ascent int, face font.Face, textColor, bgColor color.RGBA) {
	tmpDc := gg.NewContext(charW, charH)
	tmpDc.SetFontFace(face)
	tmpIm := tmpDc.Image().(*image.RGBA)
	rowBytes := charW * 4

	for ch := byte(32); ch <= 126; ch++ {
		tmpDc.SetColor(bgColor)
		tmpDc.DrawRectangle(0, 0, float64(charW), float64(charH))
		tmpDc.Fill()

		tmpDc.SetFontFace(face)
		tmpDc.SetColor(textColor)
		tmpDc.DrawString(string(ch), 0, float64(ascent))

		buf := make([]byte, charW*charH*4)
		for y := 0; y < charH; y++ {
			srcOff := y * tmpIm.Stride
			dstOff := y * rowBytes
			copy(buf[dstOff:dstOff+rowBytes], tmpIm.Pix[srcOff:srcOff+rowBytes])
		}
		dst[ch-32] = buf
	}
}

// lookup scans for word in the cache. On hit, increments hits and returns the entry.
func (gc *glyphCache) lookup(word string) (*glyphCacheEntry, bool) {
	for i := range gc.entries {
		if gc.entries[i].word == word {
			gc.entries[i].hits++
			return &gc.entries[i], true
		}
	}
	return nil, false
}

// compose evicts the least-frequently-used cache entry and builds the word's
// pixel strip by horizontally concatenating single-char glyphs from the
// glyph buffer. No font rendering involved.
func (gc *glyphCache) compose(word string) *glyphCacheEntry {
	minIdx := 0
	minHits := gc.entries[0].hits
	minLen := len(gc.entries[0].word)
	for i := 1; i < glyphCacheLen; i++ {
		h := gc.entries[i].hits
		if h < minHits || (h == minHits && len(gc.entries[i].word) < minLen) {
			minHits = h
			minLen = len(gc.entries[i].word)
			minIdx = i
			if minHits == 0 {
				break
			}
		}
	}

	e := &gc.entries[minIdx]
	e.word = word
	e.pixW = len(word) * gc.charW
	e.pixH = gc.charH
	e.hits = 1

	charBytes := gc.charW * 4
	for i := 0; i < len(word); i++ {
		ch := word[i]
		if ch < 32 || ch > 126 {
			continue
		}
		glyph := gc.glyphs[ch-32]
		dstCol := i * charBytes
		for y := 0; y < gc.charH; y++ {
			srcOff := y * charBytes
			dstOff := y*e.pixW*4 + dstCol
			copy(e.pixels[dstOff:dstOff+charBytes], glyph[srcOff:srcOff+charBytes])
		}
	}
	return e
}

// blit copies a multi-char cache entry's pixels into the gg image.
func (gc *glyphCache) blit(e *glyphCacheEntry, im *image.RGBA, px, py int) {
	for y := 0; y < e.pixH; y++ {
		srcOff := y * e.pixW * 4
		dstOff := (py+y)*im.Stride + px*4
		copy(im.Pix[dstOff:dstOff+e.pixW*4], e.pixels[srcOff:srcOff+e.pixW*4])
	}
}

// blitChar copies a single pre-rendered character glyph into the gg image.
func (gc *glyphCache) blitChar(ch byte, im *image.RGBA, px, py int) {
	gc.blitCharFrom(ch, gc.glyphs[:], im, px, py)
}

// blitCharFrom copies a glyph from the specified glyph set into the gg image.
// Used to select between normal (glyphs) and stderr (glyphsErr) rendering.
func (gc *glyphCache) blitCharFrom(ch byte, glyphs [][]byte, im *image.RGBA, px, py int) {
	if ch < 32 || ch > 126 {
		return
	}
	glyph := glyphs[ch-32]
	charBytes := gc.charW * 4
	for y := 0; y < gc.charH; y++ {
		srcOff := y * charBytes
		dstOff := (py+y)*im.Stride + px*4
		copy(im.Pix[dstOff:dstOff+charBytes], glyph[srcOff:srcOff+charBytes])
	}
}

// console holds the state for the text console display.
type console struct {
	fb       *sys.FramebufferInfo
	dc       *gg.Context
	face     font.Face
	ascent   int
	charW    int
	charH    int
	lineH    int // charH + LineSpacing
	rectX    int // content area (dark gray)
	rectY    int
	rectW    int
	rectH    int // adjusted to be multiple of lineH
	cardX    int // card outer bounds
	cardY    int
	cardW    int
	cardH    int
	maxLines int
	maxCols  int // max characters per line
	lines    [][]serial.SerialByte

	// Glyph cache for repeated word rendering
	gc     *glyphCache
	tokBuf []token

	// Dirty tracking: min/max line indices that need redraw.
	// -1 means nothing dirty.
	dirtyMin int
	dirtyMax int

	// Last fd seen — used to force a newline when fd changes mid-line
	// so stdout and stderr appear on separate lines.
	lastFd byte
}

func (c *console) markDirty(lineIdx int) {
	if c.dirtyMin < 0 || lineIdx < c.dirtyMin {
		c.dirtyMin = lineIdx
	}
	if lineIdx > c.dirtyMax {
		c.dirtyMax = lineIdx
	}
}

func (c *console) clearDirty() {
	c.dirtyMin = -1
	c.dirtyMax = -1
}

// tokenizeLine splits a line into alternating word/non-word segments.
// Words are contiguous [a-zA-Z0-9]; cacheable if length in [glyphKeyMin, glyphKeyMax].
func (c *console) tokenizeLine(text string) []token {
	c.tokBuf = c.tokBuf[:0]
	i := 0
	for i < len(text) {
		if isAlnum(text[i]) {
			start := i
			for i < len(text) && isAlnum(text[i]) {
				i++
			}
			word := text[start:i]
			cacheable := len(word) >= glyphKeyMin && len(word) <= glyphKeyMax
			c.tokBuf = append(c.tokBuf, token{text: word, col: start, cacheable: cacheable})
		} else {
			start := i
			for i < len(text) && !isAlnum(text[i]) {
				i++
			}
			c.tokBuf = append(c.tokBuf, token{text: text[start:i], col: start, cacheable: false})
		}
	}
	return c.tokBuf
}

func main() {
	sys.RegisterAsyncPreempt()

	fmt.Println("[stdio] Starting console priest")

	// --- Get serial channel ---
	serialCh, err := serial.Chars()
	if err != nil {
		fmt.Printf("[stdio] serial.Chars failed: %v\n", err)
		return
	}

	// --- Get framebuffer ---
	fb, err := sys.GetFramebuffer()
	if err != nil {
		fmt.Printf("[stdio] GetFramebuffer failed: %v\n", err)
		return
	}
	fmt.Printf("[stdio] Framebuffer: %dx%d pitch=%d addr=0x%x\n",
		fb.Width, fb.Height, fb.Pitch, fb.Addr)

	// --- Parse font ---
	otFont, err := opentype.Parse(fontData)
	if err != nil {
		fmt.Printf("[stdio] font parse error: %v\n", err)
		return
	}

	const fontSize = 16.0
	face, err := opentype.NewFace(otFont, &opentype.FaceOptions{
		Size:    fontSize,
		DPI:     72,
		Hinting: font.HintingFull,
	})
	if err != nil {
		fmt.Printf("[stdio] NewFace error: %v\n", err)
		return
	}

	adv, ok := face.GlyphAdvance('M')
	if !ok {
		fmt.Println("[stdio] could not measure glyph")
		return
	}
	charW := adv.Ceil()
	metrics := face.Metrics()
	charH := (metrics.Ascent + metrics.Descent).Ceil()
	ascent := metrics.Ascent.Ceil()
	lineH := charH + LineSpacing

	// Compute content area from desired text dimensions
	w := int(fb.Width)
	h := int(fb.Height)
	_, _ = w, h
	maxLines := 35
	maxCols := 120
	rectW := maxCols*charW + 2*textPad
	rectH := maxLines * lineH
	rectX := pad
	rectY := pad

	// Card wraps outside the content area
	cardX := rectX - cardBorderSide
	cardY := rectY - cardBorderTop
	cardW := rectW + 2*cardBorderSide
	cardH := cardBorderTop + rectH + cardBorderSide

	fmt.Printf("[stdio] Font: %dx%d px, lineH=%d, rect=%dx%d, maxLines=%d\n",
		charW, charH, lineH, rectW, rectH, maxLines)

	t0 := time.Now()
	dc := gg.NewContext(w, h)
	fmt.Printf("[stdio] gg.NewContext: %v\n", time.Since(t0))

	t0 = time.Now()
	gc := newGlyphCache(charW, charH, ascent, face)
	fmt.Printf("[stdio] newGlyphCache: %v\n", time.Since(t0))

	sys.RawWrite(2, 'Q') // breadcrumb: before console struct

	con := &console{
		fb:       fb,
		dc:       dc,
		face:     face,
		ascent:   ascent,
		charW:    charW,
		charH:    charH,
		lineH:    lineH,
		rectX:    rectX,
		rectY:    rectY,
		rectW:    rectW,
		rectH:    rectH,
		cardX:    cardX,
		cardY:    cardY,
		cardW:    cardW,
		cardH:    cardH,
		maxLines: maxLines,
		maxCols:  maxCols,
		lines:    [][]serial.SerialByte{nil},
		gc:       gc,
		tokBuf:   make([]token, 0, 64),
		dirtyMin: -1,
		dirtyMax: -1,
	}

	sys.RawWrite(2, '2') // breadcrumb: after console struct, before fill

	// Initial draw
	t0 = time.Now()
	if runtime.GOARCH == "riscv64" {
		// RISC-V: write directly to the MMIO framebuffer. This avoids
		// demand-paging ~1200 pages of the gg backbuffer (each page
		// fault costs ~50ms on QEMU TCG). The framebuffer is already
		// mapped by the kernel — MMIO writes hit device memory directly.
		pitch := int(fb.Pitch)
		fbH := int(fb.Height)
		dstU32 := unsafe.Slice((*uint32)(unsafe.Pointer(fb.Addr)), (pitch*fbH)/4)
		dstStride32 := pitch / 4
		bgr := uint32(nContent.B) | uint32(nContent.G)<<8 | uint32(nContent.R)<<16 | 0xFF000000
		for y := rectY; y < rectY+rectH; y++ {
			for x := rectX; x < rectX+rectW; x++ {
				dstU32[y*dstStride32+x] = bgr
			}
		}
		fmt.Printf("[stdio] fillContentArea(direct): %v\n", time.Since(t0))

		t0 = time.Now()
		sys.FlushFramebuffer(uint32(rectX), uint32(rectY), uint32(rectW), uint32(rectH))
		fmt.Printf("[stdio] FlushFramebuffer: %v\n", time.Since(t0))
	} else {
		copyFramebufferToGG(con.dc, fb)
		fmt.Printf("[stdio] copyFramebufferToGG: %v\n", time.Since(t0))

		t0 = time.Now()
		con.drawCard()
		fmt.Printf("[stdio] drawCard: %v\n", time.Since(t0))

		t0 = time.Now()
		con.drawContentArea()
		fmt.Printf("[stdio] drawContentArea: %v\n", time.Since(t0))

		// Flush card region plus shadow margin
		margin := 40 + 4
		flushX := cardX - margin
		flushY := cardY - margin
		flushW := cardW + 2*margin
		flushH := cardH + 2*margin
		if flushX < 0 {
			flushX = 0
		}
		if flushY < 0 {
			flushY = 0
		}

		t0 = time.Now()
		flushRegionToFramebuffer(con.dc, fb, flushX, flushY, flushW, flushH)
		fmt.Printf("[stdio] flushRegionToFB: %v\n", time.Since(t0))

		t0 = time.Now()
		sys.FlushFramebuffer(uint32(flushX), uint32(flushY), uint32(flushW), uint32(flushH))
		fmt.Printf("[stdio] FlushFramebuffer: %v\n", time.Since(t0))
	}

	fmt.Println("[stdio] Console rectangle rendered")

	// --- Enter serial event loop ---
	for sb := range serialCh {
		con.handleSerialByte(sb)
		// Drain any additional buffered chars before flushing
		done := false
		for !done {
			select {
			case sb = <-serialCh:
				con.handleSerialByte(sb)
			default:
				done = true
			}
		}
		con.flushDirty()
	}
}

// Neumorphic palette — light gray surface, same color for bg and elements.
var (
	nBase    = color.RGBA{224, 224, 230, 255} // light warm gray surface
	nContent = color.RGBA{40, 42, 48, 255}    // content well (dark neutral gray)
	nTitle   = color.RGBA{100, 100, 110, 255} // title text
	nText    = color.RGBA{200, 205, 215, 255} // content text (stdout)
	nStderr  = color.RGBA{200, 80, 80, 255}   // stderr text (dark red)
)

// fillRect fills a rectangle in the gg image with a solid color using direct
// pixel writes. No floating-point math. Used on RISC-V where gg's rasterizer
// is too slow under QEMU TCG.
func fillRect(im *image.RGBA, x, y, w, h int, c color.RGBA) {
	bounds := im.Bounds()
	if x < bounds.Min.X {
		w -= bounds.Min.X - x
		x = bounds.Min.X
	}
	if y < bounds.Min.Y {
		h -= bounds.Min.Y - y
		y = bounds.Min.Y
	}
	if x+w > bounds.Max.X {
		w = bounds.Max.X - x
	}
	if y+h > bounds.Max.Y {
		h = bounds.Max.Y - y
	}
	if w <= 0 || h <= 0 {
		return
	}
	r, g, b, a := c.R, c.G, c.B, c.A
	for py := y; py < y+h; py++ {
		off := py*im.Stride + x*4
		for px := 0; px < w; px++ {
			im.Pix[off+0] = r
			im.Pix[off+1] = g
			im.Pix[off+2] = b
			im.Pix[off+3] = a
			off += 4
		}
	}
}

// gaussLUT is a precomputed lookup table for exp(-d²/(2σ²)) * amplitude.
// Indexed by dist in 0.1px increments up to 4σ.
type gaussLUT struct {
	table []float64
	step  float64 // distance per table entry (0.1)
	max   float64 // max distance in table
}

func newGaussLUT(sigma, amplitude float64) *gaussLUT {
	step := 0.1
	maxDist := sigma * 4
	n := int(maxDist/step) + 2
	table := make([]float64, n)
	twoSigma2 := 2 * sigma * sigma
	for i := range table {
		d := float64(i) * step
		table[i] = amplitude * math.Exp(-(d*d)/twoSigma2)
	}
	return &gaussLUT{table: table, step: step, max: maxDist}
}

func (g *gaussLUT) lookup(dist float64) float64 {
	if dist <= 0 || dist >= g.max {
		return 0
	}
	idx := dist / g.step
	i := int(idx)
	if i >= len(g.table)-1 {
		return 0
	}
	// Linear interpolation between table entries
	frac := idx - float64(i)
	return g.table[i]*(1-frac) + g.table[i+1]*frac
}

// roundedRectSDFWithGrad returns both the signed distance and the
// analytical gradient direction (gx, gy) for a rounded rectangle.
// This avoids 3 SDF evaluations per pixel (was using central differences).
func roundedRectSDFWithGrad(px, py, rx, ry, rw, rh, r float64) (dist, gx, gy float64) {
	cx := rx + rw/2
	cy := ry + rh/2
	// Fold into first quadrant
	dx := math.Abs(px-cx) - rw/2 + r
	dy := math.Abs(py-cy) - rh/2 + r

	if dx > 0 && dy > 0 {
		// Corner region
		glen := math.Sqrt(dx*dx + dy*dy)
		dist = glen - r
		if glen < 1e-6 {
			return dist, 0, 0
		}
		// Gradient in folded space
		gx = dx / glen
		gy = dy / glen
	} else if dx > dy {
		// Horizontal edge
		dist = dx - r
		gx = 1
		gy = 0
	} else {
		// Vertical edge
		dist = dy - r
		gx = 0
		gy = 1
	}

	// Unfold gradient: flip sign based on which quadrant the point is in
	if px < cx {
		gx = -gx
	}
	if py < cy {
		gy = -gy
	}
	return
}

// Light direction constant: (-1,-1)/sqrt(2)
const lightInv = 1.0 / 1.4142135623730951

// sdfShadow computes the neumorphic shadow delta for a single pixel
// relative to a rounded rectangle using a precomputed gaussian LUT
// and analytical gradient.
func sdfShadowLUT(px, py, rx, ry, rw, rh, radius float64, lut *gaussLUT) float64 {
	dist, gx, gy := roundedRectSDFWithGrad(px, py, rx, ry, rw, rh, radius)
	if dist <= 0 {
		return 0
	}

	gauss := lut.lookup(dist)
	if gauss == 0 {
		return 0
	}

	dot := gx*(-lightInv) + gy*(-lightInv)
	return gauss * dot
}

// circleSdfShadowLUT is the same but for a circle, with LUT.
func circleSdfShadowLUT(px, py, cx, cy, r float64, lut *gaussLUT) float64 {
	dx := px - cx
	dy := py - cy
	glen := math.Sqrt(dx*dx + dy*dy)
	dist := glen - r
	if dist <= 0 {
		return 0
	}

	gauss := lut.lookup(dist)
	if gauss == 0 {
		return 0
	}

	if glen < 1e-6 {
		return 0
	}
	dot := (dx/glen)*(-lightInv) + (dy/glen)*(-lightInv)
	return gauss * dot
}

// roundedRectSDF returns the signed distance from point (px,py) to a
// rounded rectangle defined by top-left (rx,ry), size (rw,rh), corner radius r.
// Negative = inside, positive = outside.
func roundedRectSDF(px, py, rx, ry, rw, rh, r float64) float64 {
	// Transform to center-relative coordinates
	cx := rx + rw/2
	cy := ry + rh/2
	dx := math.Abs(px-cx) - rw/2 + r
	dy := math.Abs(py-cy) - rh/2 + r
	outside := math.Sqrt(math.Max(dx, 0)*math.Max(dx, 0)+math.Max(dy, 0)*math.Max(dy, 0)) - r
	inside := math.Min(math.Max(dx, dy), 0) - r
	if outside > 0 {
		return outside
	}
	return inside
}

// drawCard paints the neumorphic card frame using per-pixel SDF shadows.
// No blur pass needed — shadow intensity is computed analytically from
// the signed distance field with gaussian falloff and directional lighting.
func (c *console) drawCard() {
	if runtime.GOARCH == "riscv64" {
		return // skip gg rasterizer; background fill provides card frame
	}
	cx := float64(c.cardX)
	cy := float64(c.cardY)
	cw := float64(c.cardW)
	ch := float64(c.cardH)
	radius := 12.0

	im := c.dc.Image().(*image.RGBA)
	bounds := im.Bounds()

	// SDF shadow: skip on RISC-V where QEMU TCG FP emulation is too slow
	// (~1M pixels * sqrt+exp per pixel = minutes on RV64 TCG).
	if runtime.GOARCH != "riscv64" {
		sigma := 10.0
		amplitude := 15.0
		cardLUT := newGaussLUT(sigma, amplitude)
		// Shadow region: 4*sigma beyond card edges
		margin := int(math.Ceil(sigma * 4))
		sx := c.cardX - margin
		sy := c.cardY - margin
		ex := c.cardX + c.cardW + margin
		ey := c.cardY + c.cardH + margin
		if sx < bounds.Min.X { sx = bounds.Min.X }
		if sy < bounds.Min.Y { sy = bounds.Min.Y }
		if ex > bounds.Max.X { ex = bounds.Max.X }
		if ey > bounds.Max.Y { ey = bounds.Max.Y }

		// Per-pixel SDF shadow for the card
		t0 := time.Now()
		bgR := float64(nBase.R)
		bgG := float64(nBase.G)
		bgB := float64(nBase.B)
		for py := sy; py < ey; py++ {
			for px := sx; px < ex; px++ {
				delta := sdfShadowLUT(float64(px)+0.5, float64(py)+0.5,
					cx, cy, cw, ch, radius, cardLUT)
				if delta == 0 {
					continue
				}
				off := py*im.Stride + px*4
				r := bgR + delta
				g := bgG + delta
				b := bgB + delta
				if r < 0 { r = 0 } else if r > 255 { r = 255 }
				if g < 0 { g = 0 } else if g > 255 { g = 255 }
				if b < 0 { b = 0 } else if b > 255 { b = 255 }
				im.Pix[off+0] = uint8(r)
				im.Pix[off+1] = uint8(g)
				im.Pix[off+2] = uint8(b)
				im.Pix[off+3] = 255
			}
		}

		fmt.Printf("[stdio] Card shadow: %v\n", time.Since(t0))
	}

	// Card body on top
	c.dc.SetColor(nBase)
	c.dc.DrawRoundedRectangle(cx, cy, cw, ch, radius)
	c.dc.Fill()

	// Title text
	c.dc.SetFontFace(c.face)
	c.dc.SetColor(nTitle)
	titleY := cy + float64(cardBorderTop)/2 + float64(c.ascent)/2
	c.dc.DrawString("Serial Console", cx+float64(cardBorderSide)+float64(textPad), titleY)

	// Neumorphic button in upper-right of title bar (SDF shadow)
	btnR := float64(cardBorderTop-8) / 2
	btnCx := cx + cw - float64(cardBorderSide) - float64(textPad) - btnR
	btnCy := cy + float64(cardBorderTop)/2

	if runtime.GOARCH != "riscv64" {
		btnSigma := 5.0
		btnAmp := 12.0
		btnLUT := newGaussLUT(btnSigma, btnAmp)

		// Per-pixel SDF shadow for the button circle
		t1 := time.Now()
		bMargin := int(math.Ceil(btnSigma * 4))
		bsx := int(btnCx-btnR) - bMargin
		bsy := int(btnCy-btnR) - bMargin
		bex := int(btnCx+btnR) + bMargin
		bey := int(btnCy+btnR) + bMargin
		if bsx < bounds.Min.X { bsx = bounds.Min.X }
		if bsy < bounds.Min.Y { bsy = bounds.Min.Y }
		if bex > bounds.Max.X { bex = bounds.Max.X }
		if bey > bounds.Max.Y { bey = bounds.Max.Y }

		for py := bsy; py < bey; py++ {
			for px := bsx; px < bex; px++ {
				delta := circleSdfShadowLUT(float64(px)+0.5, float64(py)+0.5,
					btnCx, btnCy, btnR, btnLUT)
				if delta == 0 {
					continue
				}
				off := py*im.Stride + px*4
				// Read current pixel (card body is already drawn)
				cr := float64(im.Pix[off+0]) + delta
				cg := float64(im.Pix[off+1]) + delta
				cb := float64(im.Pix[off+2]) + delta
				if cr < 0 { cr = 0 } else if cr > 255 { cr = 255 }
				if cg < 0 { cg = 0 } else if cg > 255 { cg = 255 }
				if cb < 0 { cb = 0 } else if cb > 255 { cb = 255 }
				im.Pix[off+0] = uint8(cr)
				im.Pix[off+1] = uint8(cg)
				im.Pix[off+2] = uint8(cb)
			}
		}

		fmt.Printf("[stdio] Button shadow: %v\n", time.Since(t1))
	}

	// Button body
	c.dc.SetColor(nBase)
	c.dc.DrawCircle(btnCx, btnCy, btnR)
	c.dc.Fill()

	// Button label — small dot
	c.dc.SetColor(nTitle)
	c.dc.DrawCircle(btnCx, btnCy, 3)
	c.dc.Fill()
}

// drawContentArea fills the content well with the darker content color.
func (c *console) drawContentArea() {
	if runtime.GOARCH == "riscv64" {
		im := c.dc.Image().(*image.RGBA)
		fillRect(im, c.rectX, c.rectY, c.rectW, c.rectH, nContent)
		return
	}
	c.dc.SetColor(nContent)
	c.dc.DrawRectangle(float64(c.rectX), float64(c.rectY), float64(c.rectW), float64(c.rectH))
	c.dc.Fill()
}

// redrawLine repaints one line in the gg context (no flush).
// All rendering is pixel copies from pre-rendered glyph buffers —
// no font rendering (DrawString) happens here.
func (c *console) redrawLine(lineIdx int) {
	if lineIdx < 0 || lineIdx >= c.maxLines || lineIdx >= len(c.lines) {
		return
	}

	lineY := c.rectY + lineIdx*c.lineH
	im := c.dc.Image().(*image.RGBA)

	// Clear this line's background
	if runtime.GOARCH == "riscv64" {
		fillRect(im, c.rectX, lineY, c.rectW, c.lineH, nContent)
	} else {
		c.dc.SetColor(nContent)
		c.dc.DrawRectangle(float64(c.rectX), float64(lineY), float64(c.rectW), float64(c.lineH))
		c.dc.Fill()
	}

	line := c.lines[lineIdx]
	if len(line) == 0 {
		return
	}
	baseX := c.rectX + textPad

	// Check if any stderr content in this line
	hasStderr := false
	for _, sb := range line {
		if sb.Fd == 2 {
			hasStderr = true
			break
		}
	}

	if hasStderr {
		// Mixed color: per-char rendering with color selection
		for i, sb := range line {
			px := baseX + i*c.charW
			if sb.Fd == 2 {
				c.gc.blitCharFrom(sb.B, c.gc.glyphsErr[:], im, px, lineY)
			} else {
				c.gc.blitCharFrom(sb.B, c.gc.glyphs[:], im, px, lineY)
			}
		}
	} else {
		// All normal color: use word cache for performance
		text := c.lineText(lineIdx)
		tokens := c.tokenizeLine(text)
		for _, tok := range tokens {
			pixX := baseX + tok.col*c.charW

			if tok.cacheable {
				if entry, hit := c.gc.lookup(tok.text); hit {
					c.gc.blit(entry, im, pixX, lineY)
				} else {
					entry := c.gc.compose(tok.text)
					c.gc.blit(entry, im, pixX, lineY)
				}
			} else {
				for i := 0; i < len(tok.text); i++ {
					c.gc.blitChar(tok.text[i], im, pixX+i*c.charW, lineY)
				}
			}
		}
	}
}

// lineText extracts the text content from a line of SerialBytes as a string.
func (c *console) lineText(lineIdx int) string {
	line := c.lines[lineIdx]
	buf := make([]byte, len(line))
	for i, sb := range line {
		buf[i] = sb.B
	}
	return string(buf)
}


// flushDirty redraws all dirty lines into the gg context, copies the
// affected pixel region to the framebuffer, and tells the kernel to
// update the display. Resets dirty state afterwards.
func (c *console) flushDirty() {
	if c.dirtyMin < 0 {
		return
	}

	// Redraw each dirty line into the gg backbuffer
	for i := c.dirtyMin; i <= c.dirtyMax; i++ {
		c.redrawLine(i)
	}

	// Compute the pixel region covering all dirty lines
	y0 := c.rectY + c.dirtyMin*c.lineH
	y1 := c.rectY + (c.dirtyMax+1)*c.lineH
	regionH := y1 - y0

	// Copy only the dirty region from gg to framebuffer
	flushRegionToFramebuffer(c.dc, c.fb, c.rectX, y0, c.rectW, regionH)

	// Tell the kernel to push that region to the display
	sys.FlushFramebuffer(uint32(c.rectX), uint32(y0), uint32(c.rectW), uint32(regionH))

	c.clearDirty()
}

// handleSerialByte processes a single SerialByte from the ring.
// It updates lines and dirty tracking but does NOT flush.
func (c *console) handleSerialByte(sb serial.SerialByte) {
	if sb.B == '\r' {
		return
	}
	if sb.B == '\n' {
		if len(c.lines) < c.maxLines {
			c.lines = append(c.lines, nil)
		} else {
			// Before scrolling, flush any dirty lines to ensure pixels match lines[]
			// This is the "lock" - we can't scroll until current state is rendered
			if c.dirtyMin >= 0 {
				c.flushDirty()
			}
			// Now scroll with consistent state
			c.scroll()
		}
		return
	}
	lineIdx := len(c.lines) - 1
	if lineIdx < 0 || lineIdx >= c.maxLines {
		return
	}
	// Force a newline when fd changes mid-line so stdout and stderr
	// appear on separate lines instead of interleaving with mixed colors.
	if c.lastFd != 0 && sb.Fd != c.lastFd && len(c.lines[lineIdx]) > 0 {
		c.lastFd = sb.Fd
		c.handleSerialByte(serial.SerialByte{Fd: sb.Fd, B: '\n'})
		lineIdx = len(c.lines) - 1
		if lineIdx < 0 || lineIdx >= c.maxLines {
			return
		}
	}
	c.lastFd = sb.Fd
	// Clamp line length to prevent overflow panic in text rendering
	if len(c.lines[lineIdx]) >= c.maxCols {
		return
	}
	c.lines[lineIdx] = append(c.lines[lineIdx], sb)
	c.markDirty(lineIdx)
}

// scroll shifts lines and pixels, then flushes the entire content area.
// REQUIRES: no dirty lines (caller must flush first)
func (c *console) scroll() {
	// Shift line content
	copy(c.lines, c.lines[1:])
	c.lines[c.maxLines-1] = nil

	// Shift pixels in gg buffer
	im, ok := c.dc.Image().(*image.RGBA)
	if !ok {
		return
	}

	srcStartY := c.rectY + c.lineH
	dstStartY := c.rectY
	copyH := (c.maxLines - 1) * c.lineH
	contentStartX := c.rectX * 4
	contentBytes := c.rectW * 4

	for y := 0; y < copyH; y++ {
		srcRowStart := (srcStartY+y)*im.Stride + contentStartX
		dstRowStart := (dstStartY+y)*im.Stride + contentStartX
		copy(im.Pix[dstRowStart:dstRowStart+contentBytes],
			im.Pix[srcRowStart:srcRowStart+contentBytes])
	}

	c.redrawLine(c.maxLines - 1)
	flushRegionToFramebuffer(c.dc, c.fb, c.rectX, c.rectY, c.rectW, c.rectH)
	sys.FlushFramebuffer(uint32(c.rectX), uint32(c.rectY), uint32(c.rectW), uint32(c.rectH))
}

func fixedToInt(f fixed.Int26_6) int {
	return f.Ceil()
}

// copyFramebufferToGG reads the BGRA framebuffer into the gg RGBA backbuffer.
func copyFramebufferToGG(dc *gg.Context, fb *sys.FramebufferInfo) {
	im, ok := dc.Image().(*image.RGBA)
	if !ok {
		return
	}

	width := int(fb.Width)
	height := int(fb.Height)
	pitch := int(fb.Pitch)

	if width > im.Bounds().Dx() {
		width = im.Bounds().Dx()
	}
	if height > im.Bounds().Dy() {
		height = im.Bounds().Dy()
	}

	src := unsafe.Slice((*uint8)(unsafe.Pointer(fb.Addr)), pitch*height)
	dstPix := im.Pix
	dstStride := im.Stride

	for y := 0; y < height; y++ {
		srcRow := src[y*pitch:]
		dstRow := dstPix[y*dstStride:]
		for x := 0; x < width; x++ {
			si := x * 4
			di := x * 4
			b := srcRow[si+0]
			g := srcRow[si+1]
			r := srcRow[si+2]
			dstRow[di+0] = r
			dstRow[di+1] = g
			dstRow[di+2] = b
			dstRow[di+3] = 0xFF
		}
	}
}

// flushRegionToFramebuffer copies a rectangular region from the gg RGBA
// backbuffer to the BGRA framebuffer. Only the specified region is touched.
func flushRegionToFramebuffer(dc *gg.Context, fb *sys.FramebufferInfo, rx, ry, rw, rh int) {
	im, ok := dc.Image().(*image.RGBA)
	if !ok {
		return
	}

	fbW := int(fb.Width)
	fbH := int(fb.Height)
	pitch := int(fb.Pitch)

	// Clamp region to framebuffer and image bounds
	imW := im.Bounds().Dx()
	imH := im.Bounds().Dy()
	if rx < 0 {
		rw += rx
		rx = 0
	}
	if ry < 0 {
		rh += ry
		ry = 0
	}
	if rx+rw > fbW {
		rw = fbW - rx
	}
	if rx+rw > imW {
		rw = imW - rx
	}
	if ry+rh > fbH {
		rh = fbH - ry
	}
	if ry+rh > imH {
		rh = imH - ry
	}
	if rw <= 0 || rh <= 0 {
		return
	}

	// Use 32-bit writes to MMIO framebuffer for better performance
	// (4x fewer device memory transactions vs byte writes).
	dstU32 := unsafe.Slice((*uint32)(unsafe.Pointer(fb.Addr)), (pitch*fbH)/4)
	dstStride32 := pitch / 4
	srcPix := im.Pix
	srcStride := im.Stride

	for y := ry; y < ry+rh; y++ {
		srcRow := srcPix[y*srcStride:]
		for x := rx; x < rx+rw; x++ {
			si := x * 4
			r := srcRow[si+0]
			g := srcRow[si+1]
			b := srcRow[si+2]
			// BGRA format: B in low byte, A in high byte (little-endian)
			dstU32[y*dstStride32+x] = uint32(b) | uint32(g)<<8 | uint32(r)<<16
		}
	}
}

