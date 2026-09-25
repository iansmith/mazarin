// slides is a standalone shepherd that shows a sequence of full-screen
// slides, embedded at build time from slides/ and ordered by file stem.
// Each slide is an image, optionally paired (by file stem) with an
// HTML+CSS fragment drawn over it:
//
//	slides/01.png + slides/01.html
//	slides/02.jpg                  (image only)
//
// louis14 renders each slide's page into an offscreen image in one pass:
// a white canvas, the image as a centered CSS background (scaled down to
// fit when it is larger than the window, never scaled up), then the HTML
// over it. The image is blitted straight onto the backing store's
// DrawContext. The window is fixed at slightly smaller than the screen
// and never scrolls; HTML that overflows the window height is clipped.
//
// Left/Right arrows move between slides (wrapping). Rendered pages are
// cached; the idle loop pre-renders the slides not yet shown.
package main

import (
	"bytes"
	"embed"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	_ "image/jpeg"
	_ "image/png"
	"path"
	"sort"
	"strings"
	"time"
	"unsafe"

	louis14resource "louis14/pkg/resource"

	"mazzy/mazarin/attr"
	"mazzy/mazarin/fontcache"
	"mazzy/mazarin/mancini"
	"mazzy/mazarin/mancini/std"
	mctheme "mazzy/mazarin/mancini/theme"
	"mazzy/mazarin/mazhost"
	"mazzy/mazarin/sys"
	"mazzy/mazarin/uring"
	"mazzy/shared/ipc"
	"mazzy/shared/wm"
)

// screenMargin is how many pixels the window gives up on each axis
// relative to the full screen (split evenly between the two sides).
const screenMargin = 12

//go:embed slides
var slideFS embed.FS

// slide is one image + HTML pair.
type slide struct {
	name    string
	dataURI string // image bytes as a data: URI
	imgW    int
	imgH    int
	html    string
}

var wmCh = make(chan any, 128)

func init() { mazhost.PinEntry(MazarinMain, nil) }

// MazarinMain is the .maz plugin entry point.
func MazarinMain() { main() }

func startUringDispatcher(fc *fontcache.FontCache) {
	d := uring.NewDispatcher()
	d.OnFunc(ipc.ProtoShepherdNotify, wm.DecodeShepherdNotify, func(v any) {
		wmCh <- v
	})
	d.On(ipc.ProtoFontResponse, wm.DecodeFontResponse, fc.ReplyCh)
	d.OnDeath(func(deadSID int16) {
		fmt.Printf("[slides] shepherd %d died\n", deadSID)
	})
	d.Start()
}

// loadSlides makes one slide per image in slides/, sorted by stem, with
// the .html of the same stem as its overlay if there is one. An .html
// with no image is logged and skipped.
func loadSlides() []slide {
	entries, err := slideFS.ReadDir("slides")
	if err != nil {
		panic(fmt.Sprintf("[slides] FATAL: read embedded slides: %v", err))
	}
	images := map[string]string{} // stem -> file name
	htmls := map[string]string{}
	for _, e := range entries {
		name := e.Name()
		ext := strings.ToLower(path.Ext(name))
		stem := strings.TrimSuffix(name, path.Ext(name))
		switch ext {
		case ".png", ".jpg", ".jpeg":
			images[stem] = name
		case ".html":
			htmls[stem] = name
		}
	}

	var stems []string
	for stem := range images {
		stems = append(stems, stem)
	}
	for stem, name := range htmls {
		if _, ok := images[stem]; !ok {
			fmt.Printf("[slides] skipping %s: no image\n", name)
		}
	}
	sort.Strings(stems)

	var out []slide
	for _, stem := range stems {
		imgBytes, err := slideFS.ReadFile("slides/" + images[stem])
		if err != nil {
			fmt.Printf("[slides] skipping %s: %v\n", stem, err)
			continue
		}
		cfg, _, err := image.DecodeConfig(bytes.NewReader(imgBytes))
		if err != nil {
			fmt.Printf("[slides] skipping %s: decode config: %v\n", stem, err)
			continue
		}
		var htmlBytes []byte
		if name, ok := htmls[stem]; ok {
			if htmlBytes, err = slideFS.ReadFile("slides/" + name); err != nil {
				fmt.Printf("[slides] skipping %s: %v\n", stem, err)
				continue
			}
		}
		mime := "image/png"
		if ext := strings.ToLower(path.Ext(images[stem])); ext == ".jpg" || ext == ".jpeg" {
			mime = "image/jpeg"
		}
		out = append(out, slide{
			name:    stem,
			dataURI: "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(imgBytes),
			imgW:    cfg.Width,
			imgH:    cfg.Height,
			html:    string(htmlBytes),
		})
	}
	return out
}

// fitSize returns the image size to draw in a winW x winH window: native
// size if it fits, otherwise scaled down (aspect preserved) to fit.
func fitSize(imgW, imgH, winW, winH int) (int, int) {
	if imgW <= winW && imgH <= winH {
		return imgW, imgH
	}
	sx := float64(winW) / float64(imgW)
	sy := float64(winH) / float64(imgH)
	s := sx
	if sy < s {
		s = sy
	}
	return int(float64(imgW) * s), int(float64(imgH) * s)
}

// pageHTML wraps a slide's HTML fragment in a document whose single
// window-sized container carries the image as a centered background.
func pageHTML(s slide, winW, winH int) string {
	bgW, bgH := fitSize(s.imgW, s.imgH, winW, winH)
	return fmt.Sprintf(`<html><head><style>
html, body { margin: 0; padding: 0; background: #ffffff; }
.slide { position: relative; overflow: hidden; width: %dpx; height: %dpx;
  background-image: url(%s); background-repeat: no-repeat;
  background-position: center; background-size: %dpx %dpx; }
</style></head><body><div class="slide">%s</div></body></html>`,
		winW, winH, s.dataURI, bgW, bgH, s.html)
}

func main() {
	fmt.Println("[slides] main() entered")

	attr.Init()
	mancini.Init()

	if err := sys.WaitForCoreServices(20); err != nil {
		panic(fmt.Sprintf("[slides] FATAL: core services: %v", err))
	}
	rachelSID := sys.MustGetShepherdByName("rachel")
	fc := fontcache.New(rachelSID)
	startUringDispatcher(fc)

	slides := loadSlides()
	fmt.Printf("[slides] %d slides loaded\n", len(slides))

	screenWAttr := attr.ConstraintI64(attr.ShepherdURI("int64", "screen_w"),
		mancini.BindStrings(mancini.ProgIdentityI64, "_source_", "attr:///kernel/int64/screen/width"))
	screenHAttr := attr.ConstraintI64(attr.ShepherdURI("int64", "screen_h"),
		mancini.BindStrings(mancini.ProgIdentityI64, "_source_", "attr:///kernel/int64/screen/height"))
	screenW := int(screenWAttr.Get())
	screenH := int(screenHAttr.Get())

	pal := mctheme.NewDefaultPaletteSwapRB()
	app := std.NewAppWindow(pal, "Slides")
	app.RachelSID = rachelSID

	winW := screenW - screenMargin
	winH := screenH - screenMargin
	appLH := app.GetLayout()
	appLH.X.Set(0)
	appLH.Y.Set(0)
	appLH.Width.Set(int64(winW))
	appLH.Height.Set(int64(winH))
	_ = appLH.Bounds.Get()
	_ = attr.ValueBool(wm.ReadyURI(attr.SID()), true)

	app.AnnounceToWM(screenMargin/2, screenMargin/2, int32(winW), int32(winH))

	var bsr wm.BackingStoreReady
	for {
		if b, ok := (<-wmCh).(wm.BackingStoreReady); ok {
			bsr = b
			break
		}
	}
	fmt.Printf("[slides] screen=%dx%d requested=%dx%d granted app=%dx%d\n",
		screenW, screenH, winW, winH, bsr.AppWidth, bsr.AppHeight)

	provider := fontcache.NewFontSvcGlyphProvider(fc)
	engine := louis14resource.NewWebEngineWithProvider(provider)
	bsImg := &image.RGBA{}
	var insetX, insetY float64

	// applyBackingStore (re)maps the backing store and resets the DC clip
	// to the app area rachel granted.
	applyBackingStore := func(b wm.BackingStoreReady) {
		if b.BackingStoreAddr != 0 {
			stride := int(b.TotalStride)
			bsImg.Pix = unsafe.Slice((*byte)(unsafe.Pointer(uintptr(b.BackingStoreAddr))),
				stride*int(b.TotalHeight))
			bsImg.Stride = stride
			bsImg.Rect = image.Rect(0, 0, int(b.TotalWidth), int(b.TotalHeight))
			insetX, insetY = float64(b.LeftInset), float64(b.TopInset)
		}
		winW, winH = int(b.AppWidth), int(b.AppHeight)
		appLH.Width.Set(int64(winW))
		appLH.Height.Set(int64(winH))
	}
	applyBackingStore(bsr)
	dc := mancini.NewDrawContextForImage(bsImg, provider)

	// Rendering runs on its own goroutine. louis14 blocks on fontsvc
	// replies, which arrive through the uring dispatcher; the dispatcher
	// in turn blocks when wmCh is full. If the main loop rendered, a burst
	// of mouse/key events during a render would fill wmCh and deadlock the
	// three. The main loop therefore only drains wmCh and blits; the
	// worker owns the engine.
	type job struct{ i, w, h, gen int }
	type result struct {
		i, gen int
		img    *image.RGBA
	}
	jobs := make(chan job, 256)
	results := make(chan result, 256)
	go func() {
		for j := range jobs {
			results <- result{i: j.i, gen: j.gen, img: renderSlide(engine, slides[j.i], j.w, j.h)}
		}
	}()

	// cache holds each slide's rendered page at the current window size;
	// gen discards results rendered for a previous size.
	cache := make([]*image.RGBA, len(slides))
	requested := make([]bool, len(slides))
	gen := 0
	cur := 0

	// requestAll queues every uncached slide, current one first.
	requestAll := func() {
		for k := 0; k < len(slides); k++ {
			i := (cur + k) % len(slides)
			if cache[i] == nil && !requested[i] {
				requested[i] = true
				jobs <- job{i: i, w: winW, h: winH, gen: gen}
			}
		}
	}
	invalidate := func() {
		gen++
		for i := range cache {
			cache[i] = nil
			requested[i] = false
		}
		requestAll()
	}

	// show paints the current slide, or plain white while it renders.
	show := func() {
		dc.Push()
		dc.Translate(insetX, insetY)
		dc.DrawRectangle(0, 0, float64(winW), float64(winH))
		dc.Clip()
		dc.SetColor(color.NRGBA{R: 255, G: 255, B: 255, A: 255})
		dc.FillRectangle(0, 0, float64(winW), float64(winH))
		if len(slides) > 0 && cache[cur] != nil {
			dc.DrawImage(cache[cur], 0, 0)
		}
		dc.Pop()
		app.SendBlit()
	}
	requestAll()
	show()

	for {
		select {
		case r := <-results:
			if r.gen != gen {
				continue
			}
			requested[r.i] = false
			if r.img == nil {
				continue // failure already logged; slide stays white
			}
			cache[r.i] = r.img
			if r.i == cur {
				show()
			}

		case msg := <-wmCh:
			switch m := msg.(type) {
			case wm.BackingStoreReady:
				oldW, oldH := winW, winH
				applyBackingStore(m)
				if winW != oldW || winH != oldH {
					invalidate()
				}
				show()
			case wm.WindowResized:
				if int(m.AppWidth) != winW || int(m.AppHeight) != winH {
					winW, winH = int(m.AppWidth), int(m.AppHeight)
					appLH.Width.Set(int64(winW))
					appLH.Height.Set(int64(winH))
					invalidate()
				}
				show()
			case wm.KeyboardFocusGained, wm.YouHaveFocus:
				app.Focus()
			case wm.KeyboardFocusLost, wm.YouLostFocus:
				app.Unfocus()
			case wm.KeyPress:
				switch {
				case m.Action == wm.ActionEscape:
					// Returning ends MazarinMain; the shepherd host then
					// exits and rachel's death handling removes the window.
					fmt.Println("[slides] Escape pressed, exiting")
					return
				case len(slides) == 0:
				case m.Action == wm.ActionRight:
					cur = (cur + 1) % len(slides)
					show()
				case m.Action == wm.ActionLeft:
					cur = (cur + len(slides) - 1) % len(slides)
					show()
				}
			}
		}
	}
}

// renderSlide renders one slide's page at w x h and converts it to the
// backing store's byte order (see swapRB). Returns nil on failure.
func renderSlide(engine *louis14resource.WebEngine, s slide, w, h int) (img *image.RGBA) {
	defer func() {
		if rec := recover(); rec != nil {
			fmt.Printf("[slides] render %s panic recovered: %v\n", s.name, rec)
			img = nil
		}
	}()
	t0 := time.Now()
	img, err := engine.RenderToImage([]byte(pageHTML(s, w, h)), w)
	if err != nil {
		fmt.Printf("[slides] render %s failed: %v\n", s.name, err)
		return nil
	}
	swapRB(img)
	fmt.Printf("[slides:timing] %s rendered in %v at %dx%d\n",
		s.name, time.Since(t0), img.Bounds().Dx(), img.Bounds().Dy())
	return img
}

// swapRB swaps the red and blue channels in place. louis14 produces RGBA
// pixels, but the shared backing store is BGRA (the palette is likewise
// built with NewDefaultPaletteSwapRB), and DrawImage copies bytes as-is.
func swapRB(img *image.RGBA) {
	p := img.Pix
	for i := 0; i+3 < len(p); i += 4 {
		p[i], p[i+2] = p[i+2], p[i]
	}
}
