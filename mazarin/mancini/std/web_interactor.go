package std

import (
	"fmt"
	"image"
	"image/color"
	"time"

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
	RenderDC(html []byte, dc mancini.DrawContext, viewportW, viewportH float64)
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

// Draw implements mancini.NewDrawer. Renders the HTML content directly
// into the DrawContext at (x, y) using the WebRenderEngine.
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

	// If there's no HTML content, just clear the area with the surface color.
	if len(w.html) == 0 {
		dc.SetColor(color.NRGBA{R: 232, G: 230, B: 244, A: 255})
		dc.FillRectangle(float64(x), float64(y), fw, fh)
		w.contentDirty = false
		w.ClearDamage()
		return
	}

	if w.engine == nil {
		w.ClearDamage()
		return
	}

	// Render directly into the DrawContext.
	// Push state, translate so (0,0) is our top-left, clip to our bounds.
	dc.Push()
	dc.Translate(float64(x), float64(y))
	dc.DrawRectangle(0, 0, fw, fh)
	dc.Clip()

	t0 := time.Now()
	w.engine.RenderDC(w.html, dc, fw, fh)
	fmt.Printf("[versai:timing] WebInteractor.render: %v (%dx%d, %d bytes html)\n",
		time.Since(t0), int(width), int(height), len(w.html))

	dc.Pop()

	w.contentDirty = false
	w.ClearDamage()
}
