package std

import (
	"image"
	"math"
	"sync"
	"time"

	"github.com/fogleman/gg"
	"golang.org/x/image/font"

	"mazzy/mazarin/attr"
	"mazzy/mazarin/mancini"
)

// GradientTitle renders an animated horizontal gradient title bar into
// an off-screen buffer, for use as [AppWindow]'s TitleDraw callback.
// A background goroutine recomputes the buffer at ~15 fps and notifies
// the attribute system so the event loop redraws.
//
// The gradient has a symmetric peak ([mancini.Palette.SurfaceTint]) that
// oscillates horizontally at 0.05 Hz — one full left→right→left sweep
// in 20 seconds. On each side of the peak the color fades linearly to
// [mancini.Palette.Surface] at the edge.
//
// See also [StripedTitle] for a static Mac OS-style pinstripe title bar.
type GradientTitle struct {
	pal    mancini.Palette
	title  string
	face   font.Face
	radius float64
	swapRB bool

	mu    sync.Mutex
	front *image.RGBA // current completed frame for blitting
	w, h  int         // buffer dimensions (logical pixels)

	// Writing frameAttr triggers attr.OnDirty() in the event loop.
	frameAttr *attr.Attribute[int64]
	frame       int64

	started bool
}

// NewGradientTitle creates a GradientTitle. The font face is resolved once
// at creation time. Call Start or pass to NewAppWindow — the animation
// goroutine launches on the first Draw when the title bar dimensions are known.
func NewGradientTitle(pal mancini.Palette, fonts *mancini.FontConfig, title string, fontSize int64, radius float64) *GradientTitle {
	var face font.Face
	if fonts != nil && fonts.LoadFace != nil {
		face = fonts.LoadFace(true, fontSize)
	}
	return &GradientTitle{
		pal:    pal,
		title:  title,
		face:   face,
		radius: radius,
		swapRB: pal.SwapRB(),
	}
}

// Start launches the animation goroutine for a title bar of size w×h.
// Safe to call multiple times — only the first call starts the goroutine.
func (g *GradientTitle) Start(w, h int) {
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
func (g *GradientTitle) run() {
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
func (g *GradientTitle) renderFrame(elapsed float64) {
	w, h := g.w, g.h
	if w <= 0 || h <= 0 {
		return
	}

	buf := image.NewRGBA(image.Rect(0, 0, w, h))
	dc := gg.NewContextForRGBA(buf)
	dc.SwapRB = g.swapRB

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

	// Title text centered.
	if g.face != nil {
		dc.SetFontFace(g.face)
	}
	dc.SetColor(g.pal.Text())
	dc.DrawStringAnchored(g.title, fw/2, fh/2, 0.5, 0.5)

	g.mu.Lock()
	g.front = buf
	g.mu.Unlock()
}

// TitleDraw is the callback for std.AppWindow's TitleDraw field.
// It blits the current off-screen frame onto the main canvas.
// On the first call it starts the animation goroutine using the provided
// title bar dimensions.
func (g *GradientTitle) TitleDraw(dc mancini.DrawContext, focused bool, x, y, w, h float64) {
	if !focused {
		// Unfocused windows get no gradient — the AppWindow draws
		// plain centered text via its own unfocused fallback.
		return
	}

	// Lazy start: first focused draw tells us the title bar size.
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
}
