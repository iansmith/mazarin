// versai is the primary text editor for mazzy. It occupies the right half
// of the screen at full height and receives focus on launch.
//
// The core component is a Unit: MultiLineText + Scrollbar + VerticalLine + BoxesAndGlueInteractor.
package main

import (
	"fmt"
	"image"
	"time"
	"unsafe"

	"golang.org/x/image/font"

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
	wmCh      = make(chan any, 128)
)


func startUringDispatcher(fc *fontcache.FontCache) {
	d := uring.NewDispatcher()
	d.OnFunc(ipc.ProtoShepherdNotify, wm.DecodeShepherdNotify, func(v any) {
		wmCh <- v
	})
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
	unit := NewUnit("AppWindow", theme, pal)
	fmt.Printf("[versai:timing] NewUnit: %v\n", time.Since(tUnit))

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

	var redrawCount int64
	redraw := func(reason string) {
		redrawCount++
		fmt.Printf("[redraw #%d] %s\n", redrawCount, reason)
		app.Draw(app, 0, 0, int64(winW), int64(winH))
		app.SendBlit()
	}

	var clickTimer <-chan time.Time

	for {
		// Drain any pending dirty events before blocking.
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
			needsRedraw := true
			switch m := msg.(type) {
			case wm.KeyboardFocusGained:
				app.Focus()
				unit.Editor.Focused = true
			case wm.KeyboardFocusLost:
				app.Unfocus()
				unit.Editor.Focused = false
			case wm.YouHaveFocus:
				app.Focus()
				unit.Editor.Focused = true
			case wm.YouLostFocus:
				app.Unfocus()
				unit.Editor.Focused = false
			case wm.MouseFocusGained, wm.MouseFocusLost:
				needsRedraw = false
			case wm.MouseRelease:
				disp.DispatchWM(msg)
				clickTimer = time.After(clickAgent.ClickTimeout + 10*time.Millisecond)
			case wm.KeyPress:
				disp.DispatchWM(msg)
				fmt.Printf("[versai] key char=%c\n", rune(m.Char))
			default:
				disp.DispatchWM(msg)
			}
			if clickAgent.CheckTimer() {
				redraw("click-timer")
			} else if needsRedraw {
				redraw("wm-event")
			}

		case <-clickTimer:
			clickTimer = nil
			if clickAgent.CheckTimer() {
				redraw("click-expire")
			}

		case <-dirtyCh:
			redraw("dirtyCh")

		case <-time.After(500 * time.Millisecond):
		}
	}
}
