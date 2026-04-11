package std

import (
	"image"
	"math"
	"sync"
	"time"

	"github.com/fogleman/gg"

	"mazzy/mazarin/attr"
	"mazzy/mazarin/mancini"
	"mazzy/mazarin/mancini/impl"
)

// GradientTitleBar renders an animated horizontal gradient title bar into
// an off-screen buffer. A background goroutine recomputes the buffer at
// ~15 fps and notifies the attribute system so the event loop redraws.
//
// The gradient has a symmetric peak ([mancini.Palette.SurfaceTint]) that
// oscillates horizontally at 0.05 Hz — one full left→right→left sweep
// in 20 seconds. On each side of the peak the color fades linearly to
// [mancini.Palette.Surface] at the edge.
//
// When inactive, plain centered text is drawn (no gradient animation).
//
// See also [StripedTitleBar] for a static Mac OS-style pinstripe title bar.
type GradientTitleBar struct {
	pal      mancini.Palette
	title    string
	textFace mancini.LatinTextFace
	radius   float64

	mu    sync.Mutex
	front *image.RGBA // current completed frame for blitting
	w, h  int         // buffer dimensions (logical pixels)

	// Writing frameAttr triggers attr.OnEager() in the event loop.
	frameAttr *attr.Attribute[int64]
	frame     int64

	started bool
}

var _ mancini.TitleBar = (*GradientTitleBar)(nil)

// NewGradientTitleBar creates a GradientTitleBar. The font face is resolved once
// at creation time. The animation goroutine launches on the first active Draw
// when the title bar dimensions are known.
func NewGradientTitleBar(pal mancini.Palette, fonts *mancini.FontConfig, title string, fontSize int64, radius float64) *GradientTitleBar {
	textFace := impl.NewLatinTextFace(fonts, true, fontSize, mancini.TextAlignmentParams{})
	textFace.SetText(title)
	return &GradientTitleBar{
		pal:      pal,
		title:    title,
		textFace: textFace,
		radius:   radius,
	}
}

// Start launches the animation goroutine for a title bar of size w×h.
// Safe to call multiple times — only the first call starts the goroutine.
func (g *GradientTitleBar) Start(w, h int) {
	if g.started {
		return
	}
	g.started = true
	g.w = w
	g.h = h
	g.frameAttr = attr.ValueI64(attr.ShepherdURI("int64", "gradient/frame"), 0)

	// Render first frame synchronously so there is a buffer before the
	// first Draw call.
	g.renderFrame(0)

	go g.run()
}

const (
	gradientFPS  = 15
	gradientFreq = 0.05 // Hz — period = 20s for full left→right→left sweep
)

// run is the animation goroutine. It renders frames and notifies damage.
func (g *GradientTitleBar) run() {
	start := time.Now()
	frameInterval := time.Second / gradientFPS

	for {
		elapsed := time.Since(start).Seconds()
		g.renderFrame(elapsed)

		// Notify damage — wakes the event loop.
		g.frame++
		g.frameAttr.Set(g.frame)

		// Sleep to maintain frame rate.
		nextWake := start.Add(time.Duration(g.frame) * frameInterval)
		if d := time.Until(nextWake); d > 0 {
			time.Sleep(d)
		}
	}
}

// renderFrame computes one gradient frame at the given elapsed time and
// stores it as the front buffer.
func (g *GradientTitleBar) renderFrame(elapsed float64) {
	w, h := g.w, g.h
	if w <= 0 || h <= 0 {
		return
	}

	buf := image.NewRGBA(image.Rect(0, 0, w, h))
	dc := gg.NewContextForRGBA(buf)

	fw, fh := float64(w), float64(h)

	// peak ∈ [0, 1]: horizontal position of the gradient center.
	// Cosine sweep: left(0) → right(1) → left(0) in 1/freq seconds.
	peak := (1 - math.Cos(2*math.Pi*gradientFreq*elapsed)) / 2

	// Symmetric gradient: Surface → SurfaceTint at peak → Surface.
	grad := gg.NewLinearGradient(0, fh/2, fw, fh/2)
	grad.AddColorStop(0, g.pal.Surface())
	grad.AddColorStop(peak, g.pal.SurfaceTint())
	grad.AddColorStop(1, g.pal.Surface())
	dc.SetFillStyle(grad)
	dc.DrawRoundedRectangle(0, 0, fw, fh, g.radius)
	dc.Fill()

	g.mu.Lock()
	g.front = buf
	g.mu.Unlock()
}

// DrawTitleBar implements mancini.TitleBar.
func (g *GradientTitleBar) DrawTitleBar(dc mancini.DrawContext, title string, state mancini.WindowState, _ mancini.WindowType, x, y, w, h float64) {
	g.textFace.SetText(title)

	if state == mancini.Inactive {
		// Inactive: plain centered text only.
		dc.SetColor(g.pal.Text())
		g.textFace.DrawFace(dc, x, y, w, h)
		return
	}

	// Lazy start: first active draw tells us the title bar size.
	if !g.started {
		g.Start(int(w), int(h))
	}

	g.mu.Lock()
	buf := g.front
	g.mu.Unlock()

	if buf == nil {
		return
	}

	// Fast pixel blit from off-screen buffer to main canvas.
	canvas, ok := dc.Image().(*image.RGBA)
	if !ok {
		return
	}
	bw := buf.Bounds().Dx()
	bh := buf.Bounds().Dy()
	dstX, dstY := int(x), int(y)
	cb := canvas.Bounds()

	for row := 0; row < bh; row++ {
		dy := dstY + row
		if dy < cb.Min.Y || dy >= cb.Max.Y {
			continue
		}
		srcOff := buf.PixOffset(0, row)
		dstOff := canvas.PixOffset(dstX, dy)
		copyW := bw
		if dstX+copyW > cb.Max.X {
			copyW = cb.Max.X - dstX
		}
		if dstX < cb.Min.X {
			continue
		}
		copy(canvas.Pix[dstOff:dstOff+copyW*4], buf.Pix[srcOff:srcOff+copyW*4])
	}

	// Draw title text on top of the gradient using the real DrawContext.
	if g.textFace != nil {
		dc.SetColor(g.pal.Text())
		g.textFace.DrawFace(dc, x, y, w, h)
	}
}
