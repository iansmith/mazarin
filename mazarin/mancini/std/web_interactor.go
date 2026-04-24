package std

import (
	"fmt"
	"image"
	"image/color"
	"time"

	"mazzy/mazarin/attr"
	"mazzy/mazarin/mancini"
	"mazzy/mazarin/mancini/impl"
)

// WebRenderEngine is the interface between the interactor framework and
// an HTML rendering engine. The concrete implementation lives outside
// mancini (e.g., in louis14's resource package) and is passed to the
// WebInteractor constructor by the shepherd.
type WebRenderEngine interface {
	// RenderDC renders raw HTML bytes (possibly a fragment, not
	// necessarily well-formed) using the provided DrawContext.
	// The DC's translation and clipping define the render viewport.
	// viewportW and viewportH specify the layout dimensions.
	//
	// Returns a non-nil error if the engine could not produce output for
	// this HTML (parser rejected the markup, fetch failed, layout error,
	// etc.). The caller is expected to paint its own placeholder so the
	// user gets visible feedback instead of stale pixels.
	RenderDC(html []byte, dc mancini.DrawContext, viewportW, viewportH float64) error

	// RenderToImage lays the HTML out at the given fixed width and
	// renders into a freshly-allocated image whose height is the natural
	// content height. The caller caches the result and blits a viewport
	// from it on subsequent draws — no re-layout per scroll/redraw.
	//
	// Returns a non-nil error on parser or layout failure; in that case
	// the returned image is nil and the caller should paint a placeholder.
	RenderToImage(html []byte, width int) (*image.RGBA, error)
}

// WebInteractor renders HTML content into its allocated body area as a
// scrollable viewport on a cached, fully-rendered offscreen image.
//
// Lifecycle on each Draw:
//   - If the cached image is missing OR the panel width changed since
//     last render, call engine.RenderToImage(html, width). The result
//     is kept in cachedImage and its natural height is published via
//     ContentHeight (drives the companion scrollbar's Max).
//   - Always blit the cached image at vertical offset -ScrollY into the
//     panel area, clipped so anything past panel bounds is discarded.
//
// ScrollY is an attribute so a partner Scrollbar can drive it via
// constraint (see WebScrollerVertical for the standard wrapper).
//
// The HTML may not be well-formed — it could be a fragment like a
// paragraph or code block. The WebRenderEngine handles that gracefully;
// when it can't, RenderToImage returns an error and Draw paints a
// magenta placeholder so the user gets visible feedback.
type WebInteractor struct {
	impl.Interactor

	engine WebRenderEngine

	// HTML content currently displayed.
	html []byte

	// Cached fully-rendered image. Width matches the panel width at
	// render time; height is the natural content height returned by the
	// engine. Discarded when SetHTML is called or when the panel width
	// changes between Draws.
	cachedImage *image.RGBA
	cachedWidth int

	// Scroll position (pixels from the top of cachedImage). Drives the
	// blit offset. Externally writable so a partner Scrollbar can bind
	// its Value attribute via constraint.
	ScrollY *attr.Attribute[int64]

	// ContentHeight is the natural pixel height of the last rendered
	// image. Published as an attribute so a partner Scrollbar can wire
	// its Max to (ContentHeight - panelHeight).
	ContentHeight *attr.Attribute[int64]

	// Set to true when html or width invalidates the cache; the next
	// Draw re-renders.
	cacheDirty bool
}

// NewWebInteractor creates a WebInteractor with outside-in sizing.
// The engine is used to render HTML bytes into pixels.
func NewWebInteractor(myName, parent string, engine WebRenderEngine) *WebInteractor {
	if myName == "" {
		myName = mancini.DefaultName("web")
	}
	lh := mancini.NewLayoutAttributes(myName, parent)
	return newWebInteractor(myName, lh, engine)
}

// NewWebInteractorWithLayout creates a WebInteractor with pre-built layout
// attributes. Use this when the Width or Height needs to be a constraint
// rather than a plain value attribute (e.g. when wrapped in a scroller
// that constrains both axes).
func NewWebInteractorWithLayout(myName string, lh *mancini.LayoutAttributes, engine WebRenderEngine) *WebInteractor {
	return newWebInteractor(myName, lh, engine)
}

func newWebInteractor(myName string, lh *mancini.LayoutAttributes, engine WebRenderEngine) *WebInteractor {
	scrollY := attr.ValueI64(
		mancini.LayoutURI(myName, mancini.DataTypeInt64, "ScrollY"), 0)
	contentH := attr.ValueI64(
		mancini.LayoutURI(myName, mancini.DataTypeInt64, "ContentHeight"), 0)

	w := &WebInteractor{
		engine:        engine,
		ScrollY:       scrollY,
		ContentHeight: contentH,
		cacheDirty:    true,
	}
	w.Interactor.Initialize(w, lh)
	w.FullDamage()
	return w
}

// SetHTML replaces the HTML content, invalidates the rendered cache,
// resets ScrollY to 0 (only if it's a value attribute — when wrapped
// by a scroller it's a constraint and writing would panic; the wrapper
// is responsible for resetting its source attribute), and marks the
// interactor for redraw.
//
// ContentHeight is left alone here; renderCache will overwrite it on
// the next Draw with the fresh image's natural height.
func (w *WebInteractor) SetHTML(html []byte) {
	w.html = html
	w.cachedImage = nil
	w.cachedWidth = 0
	w.cacheDirty = true
	if !w.ScrollY.IsConstraint() {
		w.ScrollY.Set(0)
	}
	w.FullDamage()
}

// Scroll adjusts ScrollY by dy pixels (positive = scroll down) and
// clamps to [0, ContentHeight - panelHeight]. The clamp is approximate
// — Draw clamps a second time using the actual panel height.
func (w *WebInteractor) Scroll(dy int64) {
	w.ScrollY.Set(w.ScrollY.Get() + dy)
	w.FullDamage()
}

// Draw blits the cached image (re-rendering first if html or width
// changed) at -ScrollY offset, clipped to the panel area. The
// expensive layout/paint pipeline runs only when the cache is dirty;
// every other frame is a single image blit.
func (w *WebInteractor) Draw(self mancini.Interactor, x, y, width, height int64, damage image.Rectangle) {
	if !self.Visible() {
		return
	}
	dc := self.DC()
	if dc == nil {
		return
	}
	fw, fh := float64(width), float64(height)
	if fw <= 0 || fh <= 0 {
		return
	}

	// Re-render when the cache is dirty, missing, or stale relative to
	// the current panel width. Width changes invalidate text wrapping.
	if w.engine != nil && len(w.html) > 0 &&
		(w.cacheDirty || w.cachedImage == nil || w.cachedWidth != int(width)) {
		w.renderCache(int(width))
	}

	// Push translate + clip for the panel area. Everything we draw from
	// here on is at panel-local coords (0, 0) and clipped to (0, 0, fw, fh).
	dc.Push()
	dc.Translate(float64(x), float64(y))
	dc.DrawRectangle(0, 0, fw, fh)
	dc.Clip()

	switch {
	case len(w.html) == 0:
		// No content yet — paint the surface tint as a placeholder.
		dc.SetColor(color.NRGBA{R: 232, G: 230, B: 244, A: 255})
		dc.FillRectangle(0, 0, fw, fh)

	case w.cachedImage == nil:
		// Render failed — bright magenta placeholder. This is the same
		// signal the previous direct-DC path used, so users see a
		// consistent "tried to render but couldn't" indicator.
		dc.SetColor(color.NRGBA{R: 255, G: 0, B: 200, A: 255})
		dc.FillRectangle(0, 0, fw, fh)

	default:
		// Clamp ScrollY to a valid range now that we know panel height.
		maxScroll := int64(w.cachedImage.Bounds().Dy()) - height
		if maxScroll < 0 {
			maxScroll = 0
		}
		sy := w.ScrollY.Get()
		if sy < 0 {
			sy = 0
		}
		if sy > maxScroll {
			sy = maxScroll
		}
		if sy != w.ScrollY.Get() {
			w.ScrollY.Set(sy)
		}

		// Blit the cached image at -sy. The clip we pushed restricts
		// the visible portion to (0,0,fw,fh) in panel-local coords.
		dc.DrawImage(w.cachedImage, 0, int(-sy))
	}

	dc.Pop()

	w.ClearDamage()
}

// renderCache invokes the engine to produce a fresh image for the
// current html at the requested width. Recovers from engine panics so
// one tricky message can't take down the shepherd. Updates
// cachedImage / cachedWidth / ContentHeight and clears cacheDirty.
func (w *WebInteractor) renderCache(width int) {
	w.cacheDirty = false
	w.cachedImage = nil
	w.cachedWidth = width

	t0 := time.Now()
	var img *image.RGBA
	var err error
	func() {
		defer func() {
			if rec := recover(); rec != nil {
				err = fmt.Errorf("panic: %v", rec)
				fmt.Printf("[web] render panic recovered: %v (%d bytes html)\n",
					rec, len(w.html))
			}
		}()
		img, err = w.engine.RenderToImage(w.html, width)
	}()
	elapsed := time.Since(t0)

	if err != nil || img == nil {
		fmt.Printf("[versai:timing] WebInteractor.render: %v failed=%v (width=%d, %d bytes html)\n",
			elapsed, err, width, len(w.html))
		w.ContentHeight.Set(0)
		return
	}
	w.cachedImage = img
	contentH := int64(img.Bounds().Dy())
	w.ContentHeight.Set(contentH)
	fmt.Printf("[versai:timing] WebInteractor.render: %v (%dx%d cached, %d bytes html)\n",
		elapsed, img.Bounds().Dx(), img.Bounds().Dy(), len(w.html))
}
