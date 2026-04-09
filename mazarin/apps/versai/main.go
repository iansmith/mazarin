// versai is the primary text editor for mazzy. It occupies the right half
// of the screen at full height and receives focus on launch.
//
// The core component is a Unit: MultiLineText + Scrollbar + VerticalLine + WebInteractor.
package main

import (
	"bytes"
	"fmt"
	"image"
	"time"
	"unsafe"

	"github.com/yuin/goldmark"
	"golang.org/x/image/font"

	"louis14/pkg/resource"

	"mazzy/mazarin/attr"
	"mazzy/mazarin/fontcache"
	"mazzy/mazarin/mancini"
	"mazzy/mazarin/mancini/std"
	mctheme "mazzy/mazarin/mancini/theme"
	"mazzy/mazarin/sys"
	"mazzy/mazarin/uring"
	mfont "mazzy/shared/font"
	"mazzy/shared/ipc"
	"mazzy/shared/wm"
)

var (
	rachelSID int
	app       *std.AppWindow
	wmCh      = make(chan any, 4)
)

// defaultCSS is injected before goldmark output so headings, code blocks,
// and other markdown elements render with appropriate styling.
const defaultCSS = `<style>
body { font-family: sans-serif; font-size: 21px; color: #333; margin: 8px; background: #E8E6F4; }
h1 { font-size: 42px; font-weight: bold; margin: 16px 0 8px 0; }
h2 { font-size: 33px; font-weight: bold; margin: 14px 0 6px 0; }
h3 { font-size: 27px; font-weight: bold; margin: 12px 0 4px 0; }
h4 { font-size: 24px; font-weight: bold; margin: 10px 0 4px 0; }
p { margin: 6px 0; }
code { font-family: monospace; background: #eee; padding: 1px 3px; }
pre { background: #eee; padding: 8px; margin: 8px 0; overflow: auto; }
pre code { background: none; padding: 0; }
blockquote { border-left: 3px solid #ccc; margin: 8px 0; padding-left: 8px; color: #666; }
ul, ol { margin: 6px 0; padding-left: 20px; }
li { margin: 2px 0; }
em { font-style: italic; }
strong { font-weight: bold; }
</style>
`

func startUringDispatcher(fc *fontcache.FontCache) {
	d := uring.NewDispatcher()
	d.On(ipc.ProtoShepherdNotify, wm.DecodeShepherdNotify, wmCh)
	d.On(ipc.ProtoFontResponse, wm.DecodeFontResponse, fc.ReplyCh)
	d.Start()
}

func main() {
	t0 := time.Now()
	fmt.Printf("[versai] main() entered\n")

	// 1. Initialize constraint system.
	// NOTE: waits removed — the kernel now maps constraint pages using
	// explicit L0PA without switching TTBR0, so pages are ready at launch.
	attr.Init()
	mancini.Init()
	fmt.Printf("[versai:timing] attr+mancini init: %v\n", time.Since(t0))

	// 2. Wait for dependencies.
	tDep := time.Now()
	if err := sys.WaitForShepherdReady("fs", 10); err != nil {
		panic(fmt.Sprintf("[versai] FATAL: fs: %v", err))
	}
	fmt.Printf("[versai:timing] wait fs: %v\n", time.Since(tDep))
	tDep = time.Now()
	if err := sys.WaitForShepherdReady("rachel", 10); err != nil {
		panic(fmt.Sprintf("[versai] FATAL: rachel: %v", err))
	}
	fmt.Printf("[versai:timing] wait rachel: %v\n", time.Since(tDep))
	tDep = time.Now()
	if err := sys.WaitForShepherdReady("linux", 10); err != nil {
		panic(fmt.Sprintf("[versai] FATAL: linux: %v", err))
	}
	fmt.Printf("[versai:timing] wait linux: %v\n", time.Since(tDep))
	rachelSID = sys.MustGetShepherdByName("rachel")
	fc := fontcache.New(rachelSID)

	// Start uring dispatcher so font responses are processed.
	startUringDispatcher(fc)

	// 3. Theme and palette.
	tTheme := time.Now()
	resolver := func(family string, feature mancini.Feature, size int64) font.Face {
		style := mfont.Regular
		if feature == mancini.Bold {
			style = mfont.Bold
		}
		return fc.OpenFaceByName(family, style, size)
	}
	pal := mctheme.NewDefaultPaletteSwapRB()
	neu := mctheme.NewDefaultNeumorphicParams()
	theme := mctheme.NewTheme(pal, neu, mfont.DefaultSans, 14, resolver)
	theme.SetStyle(std.NewFlatStyle(15, 1.0))
	fmt.Printf("[versai:timing] theme setup: %v\n", time.Since(tTheme))

	// 4. Read screen dimensions.
	tScreen := time.Now()
	screenWProg := mancini.BindStrings(mancini.ProgIdentityI64,
		"_source_", "attr:///kernel/int64/screen/width")
	screenWAttr := attr.ConstraintI64(attr.ShepherdURI("int64", "screen_w"), screenWProg)
	screenHProg := mancini.BindStrings(mancini.ProgIdentityI64,
		"_source_", "attr:///kernel/int64/screen/height")
	screenHAttr := attr.ConstraintI64(attr.ShepherdURI("int64", "screen_h"), screenHProg)
	screenW := int(screenWAttr.Get())
	screenH := int(screenHAttr.Get())
	fmt.Printf("[versai] screen dimensions: %dx%d\n", screenW, screenH)
	fmt.Printf("[versai:timing] screen dims: %v\n", time.Since(tScreen))

	// 5. Create AppWindow.
	tApp := time.Now()
	app = std.NewAppWindow(pal, "Versai")
	app.RachelSID = rachelSID
	app.Focused = false
	fmt.Printf("[versai:timing] AppWindow create: %v\n", time.Since(tApp))

	// 6. Create the versai Unit as AppWindow's child.
	tUnit := time.Now()
	provider := fontcache.NewFontSvcGlyphProvider(fc)
	engine := resource.NewWebEngineWithProvider(provider)
	unit := NewUnit("AppWindow", theme, pal, engine)
	unit.Web.SetHTML([]byte(""))
	fmt.Printf("[versai:timing] NewUnit + SetHTML: %v\n", time.Since(tUnit))

	// Background markdown preview: a goroutine polls for text changes.
	// When text changed: render and reset interval to 3s.
	// When text unchanged: shorten interval to 1s (check sooner).
	const (
		PreviewActiveInterval = 500 * time.Millisecond
		PreviewIdleInterval   = 100 * time.Millisecond
	)
	go func() {
		var mdSrc, mdOut bytes.Buffer
		interval := PreviewActiveInterval
		for {
			time.Sleep(interval)
			unit.Editor.Mu.Lock()
			changed := unit.Editor.TextChanged()
			unit.Editor.Mu.Unlock()
			if !changed {
				interval = PreviewIdleInterval
				continue
			}
			interval = PreviewActiveInterval
			tCycle := time.Now()
			mdSrc.Reset()
			unit.Editor.WriteTo(&mdSrc) // WriteTo holds Editor.Mu
			tWrite := time.Since(tCycle)
			mdOut.Reset()
			if err := goldmark.Convert(mdSrc.Bytes(), &mdOut); err != nil {
				continue
			}
			tConvert := time.Since(tCycle)
			// Prepend default CSS so headings/code render properly.
			styled := append([]byte(defaultCSS), mdOut.Bytes()...)
			unit.Web.SetHTML(styled)
			fmt.Printf("[versai:timing] preview cycle: writeTo=%v goldmark=%v setHTML=%v total=%v (src=%d bytes)\n",
				tWrite, tConvert-tWrite, time.Since(tCycle)-tConvert, time.Since(tCycle), mdSrc.Len())
		}
	}()

	// 7. Set initial window size — request 70% of screen width.
	// Rachel respects the requested width for PlacementRightFull.
	winW := screenW * 70 / 100
	winH := screenH
	appLH := app.GetLayout()
	appLH.X.Set(0)
	appLH.Y.Set(0)
	appLH.Width.Set(int64(winW))
	appLH.Height.Set(int64(winH))

	// Set the splitter's dimensions to match.
	splitLH := unit.Split.GetLayout()
	splitLH.Width.Set(int64(winW))
	splitLH.Height.Set(int64(winH))

	// 8. Publish Ready and announce to rachel with PlacementRightFull.
	tAnnounce := time.Now()
	_ = appLH.Bounds.Get()
	readyAttr := attr.ValueBool(wm.ReadyURI(attr.SID()), true)
	_ = readyAttr
	fmt.Printf("[versai] Ready=true\n")

	app.AnnounceToWMWithPlacement(0, 0, int32(winW), int32(winH), wm.PlacementRightFull)
	fmt.Printf("[versai:timing] announce: %v\n", time.Since(tAnnounce))

	// 10. Wait for BackingStoreReady.
	tBSR := time.Now()
	fmt.Printf("[versai] waiting for BackingStoreReady...\n")
	var bsr wm.BackingStoreReady
	for {
		raw := <-wmCh
		if b, ok := raw.(wm.BackingStoreReady); ok {
			bsr = b
			break
		}
	}
	fmt.Printf("[versai:timing] BackingStoreReady wait: %v\n", time.Since(tBSR))

	// Update dimensions from rachel's actual allocation.
	appLH.Width.Set(int64(bsr.AppWidth))
	appLH.Height.Set(int64(bsr.AppHeight))
	winW = int(bsr.AppWidth)
	winH = int(bsr.AppHeight)
	splitLH.Width.Set(int64(winW))
	splitLH.Height.Set(int64(winH))

	fmt.Printf("[versai] backing store ready: app=%dx%d at (%d,%d)\n",
		winW, winH, bsr.AppX, bsr.AppY)

	// 11. Initialize input dispatch pipeline.
	disp, clickAgent, keyAgent := app.InitInput()
	mancini.SetScreenOrigin(int64(bsr.AppX), int64(bsr.AppY))
	disp.Tag = "versai"

	// Give keyboard focus to the editor.
	keyAgent.SetFocus(unit.Editor)

	// 12. Create DrawContext over the shared backing store.
	totalW := int(bsr.TotalWidth)
	totalH := int(bsr.TotalHeight)
	totalStride := int(bsr.TotalStride)
	bsSlice := unsafe.Slice((*byte)(unsafe.Pointer(uintptr(bsr.BackingStoreAddr))), totalStride*totalH)

	bsImg := &image.RGBA{
		Pix:    bsSlice,
		Stride: totalStride,
		Rect:   image.Rect(0, 0, totalW, totalH),
	}
	dc := mancini.NewDrawContextForImage(bsImg, provider)

	// Translate origin so (0,0) is the app area, and clip to app bounds.
	dc.Push()
	dc.Translate(float64(bsr.LeftInset), float64(bsr.TopInset))
	dc.DrawRectangle(0, 0, float64(winW), float64(winH))
	dc.Clip()

	// 13. Initial draw.
	tDraw := time.Now()
	appLH.X.Set(0)
	appLH.Y.Set(0)
	dc.SetColor(pal.Surface())
	dc.FillRectangle(0, 0, float64(winW), float64(winH))
	app.SetDC(dc)
	app.Draw(app, 0, 0, int64(winW), int64(winH))
	fmt.Printf("[versai:timing] initial draw: %v\n", time.Since(tDraw))
	fmt.Printf("[versai:timing] TOTAL startup: %v\n", time.Since(t0))

	// 14. Main loop.
	dirtyCh := attr.OnDirty()

	redraw := func(reason string) {
		tR := time.Now()
		app.Draw(app, 0, 0, int64(winW), int64(winH))
		tBlit := time.Now()
		app.SendBlit()
		fmt.Printf("[versai:timing] redraw(%s): draw=%v blit=%v\n",
			reason, tBlit.Sub(tR), time.Since(tBlit))
	}

	var clickTimer <-chan time.Time

	var inRedraw bool
	safeRedraw := func(reason string) {
		if inRedraw {
			return
		}
		inRedraw = true
		redraw(reason)
		inRedraw = false
	}

	for {
		// Drain any pending dirty events before blocking — avoids queueing
		// multiple redraws when the constraint system fires dirtyCh rapidly
		// (e.g., during LPBounds updates triggered by drawing itself).
	drainDirty:
		for {
			select {
			case <-dirtyCh:
			default:
				break drainDirty
			}
		}

		select {
		case msg := <-wmCh:
			tMsg := time.Now()
			needsRedraw := true
			reason := "wm"
			switch msg.(type) {
			case wm.KeyboardFocusGained:
				app.Focus()
				unit.Editor.Focused = true
				reason = "kb-focus-gained"
			case wm.KeyboardFocusLost:
				app.Unfocus()
				unit.Editor.Focused = false
				reason = "kb-focus-lost"
			case wm.YouHaveFocus:
				app.Focus()
				unit.Editor.Focused = true
				reason = "you-have-focus"
			case wm.YouLostFocus:
				app.Unfocus()
				unit.Editor.Focused = false
				reason = "you-lost-focus"
			case wm.MouseFocusGained, wm.MouseFocusLost:
				// Mouse focus changes don't affect versai's visual state.
				needsRedraw = false
			case wm.MouseRelease:
				disp.DispatchWM(msg)
				clickTimer = time.After(clickAgent.ClickTimeout + 10*time.Millisecond)
				reason = "mouse-release"
			default:
				disp.DispatchWM(msg)
				reason = fmt.Sprintf("wm-%T", msg)
			}
			fmt.Printf("[versai:timing] wm dispatch(%s): %v\n", reason, time.Since(tMsg))
			if clickAgent.CheckTimer() {
				safeRedraw("click-" + reason)
			} else if needsRedraw {
				safeRedraw(reason)
			}

		case <-clickTimer:
			clickTimer = nil
			if clickAgent.CheckTimer() {
				safeRedraw("click-timer")
			}

		case <-dirtyCh:
			safeRedraw("dirty")
		}
	}
}
