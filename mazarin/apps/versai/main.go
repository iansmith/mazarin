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
	// fmt.Printf("[versai] main() entered\n")

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

	// Pre-open BAG fonts at all three chooser sizes so they're cached
	// before the first draw.
	tFonts := time.Now()
	for _, sz := range []int64{10, 16, 20} {
		fc.OpenFaceByName(mfont.LatinModernRoman, mfont.Regular, sz)
	}
	fmt.Printf("[versai:timing] BAG font pre-open: %v\n", time.Since(tFonts))

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
	// fmt.Printf("[versai] screen dimensions: %dx%d\n", screenW, screenH)
	fmt.Printf("[versai:timing] screen dims: %v\n", time.Since(tScreen))

	// 5. Create AppWindow.
	tApp := time.Now()
	app = std.NewAppWindow(pal, "Versai")
	app.RachelSID = rachelSID
	app.Focused = false
	fmt.Printf("[versai:timing] AppWindow create: %v\n", time.Since(tApp))

	// 6. Create interactor hierarchy:
	//   AppWindow → MarginParent(16,6,6,6) → ColumnEdgeToEdge → ScrollerVertical → 4 Units
	tUnit := time.Now()
	provider := fontcache.NewFontSvcGlyphProvider(fc)

	// 7. Set initial window size — request 70% of screen width.
	winW := screenW * 70 / 100
	winH := screenH
	appLH := app.GetLayout()
	appLH.X.Set(0)
	appLH.Y.Set(0)
	appLH.Width.Set(int64(winW))
	appLH.Height.Set(int64(winH))

	appWidthURI := appLH.Width.URI()
	appHeightURI := appLH.Height.URI()

	// fmt.Printf("[versai] creating ColumnEdgeToEdge...\n")
	col := NewColumnEdgeToEdge("versai_col", "AppWindow",
		theme, pal, appWidthURI, appHeightURI,
		20, std.ScrollbarStandard)
	// fmt.Printf("[versai] ColumnEdgeToEdge created\n")

	// Create 4 Units inside the scroller. Each Unit is a MarginParent
	// containing a UnitContainer (VE + BAG). Margins add top(16)
	// + bottom(6) to the container height.
	scrollerParent := col.ScrollerName()
	units := NewUnitList()
	labels := [4]string{"Unit A", "Unit B", "Unit C", "Unit D"}
	for i := range labels {
		unitName := fmt.Sprintf("versai_u%d", i)
		// fmt.Printf("[versai] creating unit %d (%s)...\n", i, labels[i])
		u := NewUnit(unitName, scrollerParent, theme, pal, rachelSID, nil, labels[i])
		// fmt.Printf("[versai] unit %d created\n", i)
		uLH := u.GetLayout()
		// Initial height placeholder; updated at first draw when
		// UnitContainer computes VE height from font metrics.
		uLH.Height.Set(500)
		entry := &UnitEntry{Unit: u}
		entry.Node = units.PushBack(entry)
	}
	// fmt.Printf("[versai] calling LayoutChildren...\n")
	col.LayoutChildren()
	fmt.Printf("[versai:timing] col + 4 margin+units: %v\n", time.Since(tUnit))

	// 8. Publish Ready and announce to rachel with PlacementRightFull.
	tAnnounce := time.Now()
	_ = appLH.Bounds.Get()
	readyAttr := attr.ValueBool(wm.ReadyURI(attr.SID()), true)
	_ = readyAttr
	// fmt.Printf("[versai] Ready=true\n")

	app.AnnounceToWMWithPlacement(0, 0, int32(winW), int32(winH), wm.PlacementRightFull)
	fmt.Printf("[versai:timing] announce: %v\n", time.Since(tAnnounce))

	// 10. Wait for BackingStoreReady.
	tBSR := time.Now()
	// fmt.Printf("[versai] waiting for BackingStoreReady...\n")
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

	// fmt.Printf("[versai] backing store ready: app=%dx%d at (%d,%d)\n",
	// 	winW, winH, bsr.AppX, bsr.AppY)

	// 11. Initialize input dispatch pipeline.
	disp, clickAgent, keyAgent := app.InitInput()
	mancini.SetScreenOrigin(int64(bsr.AppX), int64(bsr.AppY))
	disp.Tag = "versai"
	disp.Debug = false

	// Collect all Units for focus peer wiring.
	var allUnits []*Unit
	for n := units.Front(); n != nil; n = n.Next() {
		allUnits = append(allUnits, n.Value.Unit)
	}

	// Wire in-app focus on each Unit. Children (VE, Throbber) get
	// FocusParent back-references so they can call SetFocusToSelf().
	for n := units.Front(); n != nil; n = n.Next() {
		u := n.Value.Unit
		u.KeyFocusAgent = keyAgent
		u.KeyFocusTarget = u.Editor
		u.FocusPeers = allUnits
		u.Editor.FocusParent = u
		u.Throbber.FocusParent = u
	}

	// Give keyboard focus to the first unit.
	units.Front().Value.Unit.SetFocusToSelf()

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

	// Set mainDC on all throbbers now that the DrawContext exists.
	for n := units.Front(); n != nil; n = n.Next() {
		n.Value.Unit.Throbber.SetMainDC(dc)
	}

	// Track app screen position for overlay coordinate conversion.
	appScreenX := int32(bsr.AppX)
	appScreenY := int32(bsr.AppY)

	// 13. Initial draw.
	tDraw := time.Now()
	appLH.X.Set(0)
	appLH.Y.Set(0)
	dc.SetColor(pal.Surface())
	dc.FillRectangle(0, 0, float64(winW), float64(winH))
	app.SetDC(dc)
	app.Draw(app, 0, 0, int64(winW), int64(winH), image.Rect(0, 0, int(winW), int(winH)))
	app.SendBlit()
	fmt.Printf("[versai:timing] initial draw: %v\n", time.Since(tDraw))
	fmt.Printf("[versai:timing] TOTAL startup: %v\n", time.Since(t0))

	// 14. Main loop.
	eagerCh := attr.OnEager()

	var redrawCount int64
	var drawTotal time.Duration
	// redraw reads the AppWindow's DamageRect and draws only the damaged
	// region. If damage is empty, no drawing occurs.
	redraw := func(reason string) {
		x0, y0, x1, y1 := appLH.GetDamageRect()
		damage := image.Rect(int(x0), int(y0), int(x1), int(y1))
		if damage.Empty() {
			return
		}
		redrawCount++
		t0 := time.Now()
		app.Draw(app, 0, 0, int64(winW), int64(winH), damage)
		dt := time.Since(t0)
		drawTotal += dt
		app.SendBlit()
		// Report average every 50 redraws.
		if redrawCount%50 == 0 {
			avg := drawTotal / time.Duration(redrawCount)
			fmt.Printf("[perf] redraws=%d avg=%v last=%v dmg=(%d,%d)-(%d,%d)\n",
				redrawCount, avg, dt, x0, y0, x1, y1)
		}
	}

	var clickTimer <-chan time.Time
	throbTicker := time.NewTicker(100 * time.Millisecond) // 10Hz throbber animation
	defer throbTicker.Stop()

	for {
		// Drain any pending dirty events before blocking.
		drainDirty:
		for {
			select {
			case <-eagerCh:
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
			case wm.KeyboardFocusLost:
				app.Unfocus()
			case wm.YouHaveFocus:
				app.Focus()
			case wm.YouLostFocus:
				app.Unfocus()
			case wm.MouseFocusGained, wm.MouseFocusLost:
				needsRedraw = false
			case wm.MouseMove:
				// Route to overlay if active.
				for n := units.Front(); n != nil; n = n.Next() {
					if n.Value.Unit.Throbber.HandleMouseMove(m, appScreenX, appScreenY) {
						needsRedraw = false
						break
					}
				}
				if needsRedraw {
					disp.DispatchWM(msg)
				}
			case wm.MouseRelease:
				// Route to overlay if active.
				overlayHandled := false
				for n := units.Front(); n != nil; n = n.Next() {
					if n.Value.Unit.Throbber.HandleMouseRelease(m, appScreenX, appScreenY) {
						overlayHandled = true
						needsRedraw = false
						break
					}
				}
				if !overlayHandled {
					disp.DispatchWM(msg)
					clickTimer = time.After(clickAgent.ClickTimeout + 10*time.Millisecond)
				}
			case wm.OverlayReady:
				for n := units.Front(); n != nil; n = n.Next() {
					if n.Value.Unit.Throbber.OverlayActive() {
						continue // skip throbbers that already have an overlay
					}
					n.Value.Unit.Throbber.HandleOverlayReady(m)
					break
				}
				needsRedraw = false
			case wm.OverlayReleased:
				for n := units.Front(); n != nil; n = n.Next() {
					n.Value.Unit.Throbber.HandleOverlayReleased(m)
				}
				needsRedraw = false
			case wm.WindowMoved:
				appScreenX = m.AppX
				appScreenY = m.AppY
			case wm.KeyPress:
				disp.DispatchWM(msg)
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

		case <-eagerCh:
			redraw("eagerCh")

		case <-throbTicker.C:
			for n := units.Front(); n != nil; n = n.Next() {
				t := n.Value.Unit.Throbber
				t.Tick()
				t.FullDamage()
			}
			redraw("throbber-tick")

		case <-time.After(500 * time.Millisecond):
		}
	}
}
