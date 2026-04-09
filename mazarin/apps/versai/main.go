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

func startUringDispatcher(fc *fontcache.FontCache) {
	d := uring.NewDispatcher()
	d.On(ipc.ProtoShepherdNotify, wm.DecodeShepherdNotify, wmCh)
	d.On(ipc.ProtoFontResponse, wm.DecodeFontResponse, fc.ReplyCh)
	d.Start()
}

func main() {
	fmt.Printf("[versai] main() entered\n")

	// 1. Initialize constraint system.
	// NOTE: waits removed — the kernel now maps constraint pages using
	// explicit L0PA without switching TTBR0, so pages are ready at launch.
	attr.Init()
	mancini.Init()

	// 2. Wait for dependencies.
	if err := sys.WaitForShepherdReady("fs", 10); err != nil {
		panic(fmt.Sprintf("[versai] FATAL: fs: %v", err))
	}
	if err := sys.WaitForShepherdReady("rachel", 10); err != nil {
		panic(fmt.Sprintf("[versai] FATAL: rachel: %v", err))
	}
	if err := sys.WaitForShepherdReady("linux", 10); err != nil {
		panic(fmt.Sprintf("[versai] FATAL: linux: %v", err))
	}
	rachelSID = sys.MustGetShepherdByName("rachel")
	fc := fontcache.New(rachelSID)

	// Start uring dispatcher so font responses are processed.
	startUringDispatcher(fc)

	// 3. Theme and palette.
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
	theme.SetStyle(std.NewNeumorphicStyle(neu.Heavy(), neu.Light()))

	// 4. Read screen dimensions.
	screenWProg := mancini.BindStrings(mancini.ProgIdentityI64,
		"_source_", "attr:///kernel/int64/screen/width")
	screenWAttr := attr.ConstraintI64(attr.ShepherdURI("int64", "screen_w"), screenWProg)
	screenHProg := mancini.BindStrings(mancini.ProgIdentityI64,
		"_source_", "attr:///kernel/int64/screen/height")
	screenHAttr := attr.ConstraintI64(attr.ShepherdURI("int64", "screen_h"), screenHProg)
	screenW := int(screenWAttr.Get())
	screenH := int(screenHAttr.Get())
	fmt.Printf("[versai] screen dimensions: %dx%d\n", screenW, screenH)

	// 5. Create AppWindow.
	app = std.NewAppWindow(pal, "Versai")
	app.RachelSID = rachelSID
	app.Focused = false

	// 6. Create the versai Unit as AppWindow's child.
	provider := fontcache.NewFontSvcGlyphProvider(fc)
	engine := resource.NewWebEngineWithProvider(provider)
	unit := NewUnit("AppWindow", theme, pal, engine)
	unit.Web.SetHTML([]byte(`<div><p>Hello world</p></div>`))

	// Wire up live markdown preview: on each edit, pipe the raw gap-buffer
	// bytes through goldmark and push the resulting HTML to the web column.
	var mdSrc, mdOut bytes.Buffer
	unit.Editor.AfterEdit = func() {
		mdSrc.Reset()
		unit.Editor.WriteTo(&mdSrc)
		mdOut.Reset()
		if err := goldmark.Convert(mdSrc.Bytes(), &mdOut); err != nil {
			return
		}
		unit.Web.SetHTML(mdOut.Bytes())
	}

	// 7. Set initial window size — rachel will override for PlacementRightFull,
	//    but we provide reasonable defaults.
	winW := screenW / 2
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
	_ = appLH.Bounds.Get()
	readyAttr := attr.ValueBool(wm.ReadyURI(attr.SID()), true)
	_ = readyAttr
	fmt.Printf("[versai] Ready=true\n")

	app.AnnounceToWMWithPlacement(0, 0, int32(winW), int32(winH), wm.PlacementRightFull)

	// 10. Wait for BackingStoreReady.
	fmt.Printf("[versai] waiting for BackingStoreReady...\n")
	var bsr wm.BackingStoreReady
	for {
		raw := <-wmCh
		if b, ok := raw.(wm.BackingStoreReady); ok {
			bsr = b
			break
		}
	}

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
	appLH.X.Set(0)
	appLH.Y.Set(0)
	dc.SetColor(pal.Surface())
	dc.FillRectangle(0, 0, float64(winW), float64(winH))
	app.SetDC(dc)
	app.Draw(app, 0, 0, int64(winW), int64(winH))

	// 14. Main loop.
	dirtyCh := attr.OnDirty()

	redraw := func() {
		app.Draw(app, 0, 0, int64(winW), int64(winH))
		app.SendBlit()
	}

	var clickTimer <-chan time.Time

	var inRedraw bool
	safeRedraw := func() {
		if inRedraw {
			return
		}
		inRedraw = true
		redraw()
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
			needsRedraw := true
			switch msg.(type) {
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
				// Mouse focus changes don't affect versai's visual state.
				needsRedraw = false
			case wm.MouseRelease:
				disp.DispatchWM(msg)
				clickTimer = time.After(clickAgent.ClickTimeout + 10*time.Millisecond)
			default:
				disp.DispatchWM(msg)
			}
			if clickAgent.CheckTimer() {
				safeRedraw()
			} else if needsRedraw {
				safeRedraw()
			}

		case <-clickTimer:
			clickTimer = nil
			if clickAgent.CheckTimer() {
				safeRedraw()
			}

		case <-dirtyCh:
			safeRedraw()
		}
	}
}
