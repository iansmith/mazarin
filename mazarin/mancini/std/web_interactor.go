package std

import (
	"image"

	"mazzy/mazarin/mancini"
	"mazzy/mazarin/mancini/impl"
)

// WebRenderEngine is the interface between the interactor framework and
// an HTML rendering engine. The concrete implementation lives outside
// mancini (e.g., in louis14's resource package) and is passed to the
// WebInteractor constructor by the shepherd.
type WebRenderEngine interface {
	// Render takes raw HTML bytes (possibly a fragment, not necessarily
	// well-formed) and a viewport size, and returns the rendered pixels.
	Render(html []byte, width, height int) *image.RGBA
}

// WebInteractor is an outside-in leaf interactor that renders HTML
// content into its allocated area. It has no children — it receives
// HTML bytes via SetHTML, renders them through a [WebRenderEngine],
// and blits the result onto the DrawContext during Draw.
//
// Width and Height are value attributes set by the parent (outside-in).
// When the HTML content or dimensions change, the off-screen image is
// re-rendered on the next Draw call.
//
// The HTML may not be well-formed — it could be a fragment like a
// paragraph or code block. The WebRenderEngine is responsible for
// handling that gracefully.
type WebInteractor struct {
	impl.Interactor

	engine WebRenderEngine

	// Current HTML content.
	html []byte

	// Cached rendered image and the dimensions it was rendered at.
	cached       *image.RGBA
	cachedW      int
	cachedH      int
	contentDirty bool
}

// NewWebInteractor creates a WebInteractor with outside-in sizing.
// The engine is used to render HTML bytes into pixels.
func NewWebInteractor(myName, parent string, engine WebRenderEngine) *WebInteractor {
	if myName == "" {
		myName = mancini.DefaultName("web")
	}

	lh := mancini.NewLayoutAttributes(myName, parent)

	w := &WebInteractor{
		engine:       engine,
		contentDirty: true,
	}
	w.Interactor.Initialize(w, lh)
	w.FullDamage()
	return w
}

// NewWebInteractorWithLayout creates a WebInteractor with pre-built layout
// attributes. Use this when the Height needs to be a constraint rather
// than a plain value attribute.
func NewWebInteractorWithLayout(myName string, lh *mancini.LayoutAttributes, engine WebRenderEngine) *WebInteractor {
	w := &WebInteractor{
		engine:       engine,
		contentDirty: true,
	}
	w.Interactor.Initialize(w, lh)
	w.FullDamage()
	return w
}

// SetHTML replaces the HTML content and marks it for re-rendering.
func (w *WebInteractor) SetHTML(html []byte) {
	w.html = html
	w.contentDirty = true
	w.FullDamage()
}

// Draw implements mancini.NewDrawer. Renders the HTML content to an
// off-screen image (if dirty or dimensions changed), then blits the
// result onto the DrawContext at (x, y).
func (w *WebInteractor) Draw(self mancini.Interactor, x, y, width, height int64) {
	if !self.Visible() {
		return
	}
	dc := self.DC()
	if dc == nil {
		return
	}

	iw, ih := int(width), int(height)
	if iw <= 0 || ih <= 0 {
		return
	}

	// Re-render if content changed or dimensions changed.
	if w.contentDirty || w.cached == nil || w.cachedW != iw || w.cachedH != ih {
		w.rerender(iw, ih)
	}

	if w.cached != nil {
		dc.DrawImage(w.cached, int(x), int(y))
	}
}

// rerender calls the WebRenderEngine to produce a new off-screen image.
func (w *WebInteractor) rerender(width, height int) {
	if w.engine == nil || len(w.html) == 0 {
		w.cached = nil
		w.cachedW = width
		w.cachedH = height
		w.contentDirty = false
		return
	}

	w.cached = w.engine.Render(w.html, width, height)
	w.cachedW = width
	w.cachedH = height
	w.contentDirty = false
}
