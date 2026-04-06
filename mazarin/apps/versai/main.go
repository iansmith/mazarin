package main

import (
	"fmt"
	"image"
	"os"
	"strconv"
	"unsafe"

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

	"golang.org/x/image/font"
)

var rachelSID int

// wmCh receives typed WM messages from the uring Dispatcher.
var wmCh = make(chan any, 4)

// announceToWM sends AppStart to rachel via uring.
func announceToWM(x, y, w, h int32) {
	msg := wm.EncodeAppStart(&wm.AppStart{
		SID:    int32(os.Getpid()),
		X:      x,
		Y:      y,
		Width:  w,
		Height: h,
	})
	if err := uring.Send(rachelSID, &msg); err != nil {
		sys.UartWriteString("[versai] uring.Send AppStart failed: " + err.Error() + "\n")
		return
	}
	sys.UartWriteString(fmt.Sprintf("[versai] sent AppStart to rachel: %dx%d at (%d,%d)\n", w, h, x, y))
}

// sendBlit tells rachel to copy our backing store to the framebuffer.
func sendBlit() {
	if rachelSID < 0 {
		return
	}
	msg := wm.EncodeBlit(&wm.Blit{SID: int32(os.Getpid())})
	_ = uring.Send(rachelSID, &msg)
}

// startUringDispatcher sets up the uring Dispatcher for WM and font messages.
func startUringDispatcher(fc *fontcache.FontCache) {
	d := uring.NewDispatcher()
	d.On(ipc.ProtoShepherdNotify, wm.DecodeShepherdNotify, wmCh)
	d.On(ipc.ProtoFontResponse, wm.DecodeFontResponse, fc.ReplyCh)
	d.Start()
}

func main() {
	sys.UartWriteString("[versai] main() entered\n")

	// 1. Initialize constraint system.
	attr.Init()
	mancini.Init()

	// 2. Wait for required shepherds.
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

	startUringDispatcher(fc)

	// 3. Palette, fonts, theme.
	pal := mctheme.NewDefaultPaletteSwapRB()

	resolver := func(family string, feature mancini.Feature, size int64) font.Face {
		style := mfont.Regular
		if feature == mancini.Bold {
			style = mfont.Bold
		}
		return fc.OpenFaceByName(family, style, size)
	}
	theme := mctheme.NewTheme(pal, mctheme.NewDefaultNeumorphicParams(), mfont.DefaultMono, 18, resolver)

	sys.UartWriteString("[versai] theme configured\n")

	// 4. Screen dimensions.
	screenWProg := mancini.BindStrings(mancini.ProgIdentityI64,
		"_source_", "attr:///kernel/int64/screen/width")
	screenWAttr := attr.ConstraintI64(attr.ShepherdURI("int64", "screen_w"), screenWProg)
	screenHProg := mancini.BindStrings(mancini.ProgIdentityI64,
		"_source_", "attr:///kernel/int64/screen/height")
	screenHAttr := attr.ConstraintI64(attr.ShepherdURI("int64", "screen_h"), screenHProg)
	screenW := int(screenWAttr.Get())
	screenH := int(screenHAttr.Get())
	sys.UartWriteString(fmt.Sprintf("[versai] screen: %dx%d\n", screenW, screenH))

	// 5. Build interactor tree: AppWindow > RowFillLastChild > [Scroller, Scrollbar]
	app := std.NewAppWindow(pal, "Versai",
		"attr:///kernel/int64/screen/width", "attr:///kernel/int64/screen/height")
	app.Focused = false

	// Window dimensions.
	const winW, winH = 800, 600

	// RowFillLastChild: parent = AppWindow. Scroller gets 95%, scrollbar gets rest.
	row := std.NewRowFillLastChild("main_row", "AppWindow", pal, 0)

	// Set row width to window width — RowFillLastChild needs external width.
	rowLH := row.GetLayout()
	rowLH.Width = attr.ValueI64(
		mancini.LayoutURI("main_row", mancini.DataTypeInt64, mancini.LayoutWidth), int64(winW))

	// Scroller: parent = main_row. Height = winH (fixed value attribute).
	scrollerH := attr.ValueI64(std.ScrollerHeightURI("scroller"), int64(winH))
	scroller := std.NewScroller("scroller", "main_row", pal, scrollerH)
	scrollerLH := scroller.GetLayout()
	scrollerLH.Width.Set(int64(float64(winW) * 0.95))

	// Light scrollbar: parent = main_row. Vertical, height = winH, gets remaining width.
	_ = std.NewLightScrollbarNamed("scrollbar", "main_row", theme,
		true, float64(winH), 0.3, 0.0)

	// Finish row layout after children are created.
	row.FinishLayout()

	sys.UartWriteString("[versai] interactor tree built\n")

	// 6. Compute screen position.
	appLH := app.GetLayout()
	appLH.X.Set(0)
	appLH.Y.Set(0)

	var posXAttr, posYAttr *attr.Attribute[int64]
	if rachelSID >= 0 {
		rachelSIDStr := strconv.Itoa(rachelSID)
		vaXURI := "attr:///shepherd/" + rachelSIDStr + "/int64/visibleArea/x"
		vaYURI := "attr:///shepherd/" + rachelSIDStr + "/int64/visibleArea/y"
		vaWURI := "attr:///shepherd/" + rachelSIDStr + "/int64/visibleArea/w"

		xProg := mancini.BindStrings(mancini.ProgAddSubDeref,
			"_a_", vaXURI, "_b_", vaWURI, "_c_", appLH.Width.URI())
		posXAttr = attr.ConstraintI64(attr.ShepherdURI("int64", "pos/x"), xProg)

		yProg := mancini.BindStrings(mancini.ProgIdentityI64, "_source_", vaYURI)
		posYAttr = attr.ConstraintI64(attr.ShepherdURI("int64", "pos/y"), yProg)

		_ = posXAttr.Get()
		_ = posYAttr.Get()
	}

	var winX, winY int
	if posXAttr != nil {
		winX = int(posXAttr.Get())
		winY = int(posYAttr.Get())
	} else {
		winX = screenW/2 - winW/2
		winY = screenH/2 - winH/2
	}

	// 7. Announce to rachel.
	_ = appLH.Bounds.Get()
	readyAttr := attr.ValueBool(wm.ReadyURI(attr.SID()), true)
	_ = readyAttr
	sys.UartWriteString("[versai] Ready=true\n")

	announceToWM(int32(winX), int32(winY), int32(winW), int32(winH))

	// 8. Wait for backing store.
	sys.UartWriteString("[versai] waiting for BackingStoreReady...\n")
	var bsr wm.BackingStoreReady
	for {
		raw := <-wmCh
		if b, ok := raw.(wm.BackingStoreReady); ok {
			bsr = b
			break
		}
	}

	// 9. Set up drawing.
	disp, _, _ := app.InitInput()
	disp.OriginX = int64(bsr.AppX)
	disp.OriginY = int64(bsr.AppY)
	mancini.SetScreenOrigin(int64(bsr.AppX), int64(bsr.AppY))
	disp.Debug = true
	disp.Tag = "versai"

	totalW := int(bsr.TotalWidth)
	totalH := int(bsr.TotalHeight)
	totalStride := int(bsr.TotalStride)
	bsSlice := unsafe.Slice((*byte)(unsafe.Pointer(uintptr(bsr.BackingStoreAddr))), totalStride*totalH)

	bsImg := &image.RGBA{
		Pix:    bsSlice,
		Stride: totalStride,
		Rect:   image.Rect(0, 0, totalW, totalH),
	}
	provider := fontcache.NewFontSvcGlyphProvider(fc)
	dc := mancini.NewDrawContextForImage(bsImg, provider)

	leftInset := float64(bsr.LeftInset)
	topInset := float64(bsr.TopInset)
	dc.Push()
	dc.Translate(leftInset, topInset)
	dc.DrawRectangle(0, 0, float64(winW), float64(winH))
	dc.Clip()

	sys.UartWriteString(fmt.Sprintf("[versai] backing store ready: total=%dx%d inset=(%d,%d) app=%dx%d\n",
		totalW, totalH, bsr.LeftInset, bsr.TopInset, winW, winH))

	// 10. Initial draw.
	appLH.X.Set(0)
	appLH.Y.Set(0)
	dc.SetColor(pal.Surface())
	dc.FillRectangle(0, 0, float64(winW), float64(winH))
	app.SetDC(dc)
	app.Draw(app, 0, 0, int64(winW), int64(winH))
	sendBlit()

	sys.UartWriteString("[versai] initial draw complete\n")

	// 11. Event loop.
	dirtyCh := attr.OnDirty()
	redraw := func() {
		app.Draw(app, 0, 0, int64(winW), int64(winH))
		sendBlit()
	}

	for {
		select {
		case msg := <-wmCh:
			switch msg.(type) {
			case wm.YouHaveFocus:
				app.Focus()
			case wm.YouLostFocus:
				app.Unfocus()
			case wm.KeyboardFocusGained:
				app.Focus()
			case wm.KeyboardFocusLost:
				app.Unfocus()
			case wm.MouseFocusGained, wm.MouseFocusLost:
				// ignored
			case wm.MousePress, wm.MouseRelease:
				disp.DispatchWM(msg)
			default:
				disp.DispatchWM(msg)
			}
			redraw()
		case <-dirtyCh:
			scroller.MarkContentDirty()
			redraw()
		}
	}
}
