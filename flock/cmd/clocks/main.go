package main

import (
	"fmt"
	"image"
	"image/color"
	"time"
	_ "time/tzdata"

	"golang.org/x/image/font"

	"mazzy/mazarin/attr"
	mfont "mazzy/shared/font"
	"mazzy/mazarin/fontcache"
	"mazzy/mazarin/mancini"
	"mazzy/mazarin/mancini/std"
	mctheme "mazzy/mazarin/mancini/theme"
	"mazzy/mazarin/sys"
	"mazzy/mazarin/uring"
	"mazzy/shared/ipc"
	"mazzy/shared/wm"
	"os"
	"strconv"
	"unsafe"
)

// Screen dimensions — read from kernel constraint attributes at startup.
var screenW, screenH int

type cityInfo struct {
	name    string // display name
	id      string // safe identifier for constraint names (no spaces)
	tz      string // IANA timezone for time.LoadLocation
	tzLabel string // display label for timezone row
	loc     *time.Location
}

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
		sys.UartWriteString("[clocks] uring.Send AppStart failed: " + err.Error() + "\n")
		return
	}
	sys.UartWriteString(fmt.Sprintf("[clocks] sent AppStart to rachel: %dx%d at (%d,%d)\n", w, h, x, y))
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
	sys.UartWriteString("[clocks] main() entered\n")

	// 1. Initialize constraint system.
	attr.Init()

	mancini.Init()

	// 2. Wait for fs (file operations), rachel (window manager + fontsvc), and
	// linux (Write delegate handler). Clocks must not issue any write() syscalls
	// before linux has entered its event loop, or the delegate channel fills up
	// and deadlocks the system.
	if err := sys.WaitForShepherdReady("fs", 10); err != nil {
		panic(fmt.Sprintf("[clocks] FATAL: fs: %v", err))
	}
	if err := sys.WaitForShepherdReady("rachel", 10); err != nil {
		panic(fmt.Sprintf("[clocks] FATAL: rachel: %v", err))
	}
	if err := sys.WaitForShepherdReady("linux", 10); err != nil {
		panic(fmt.Sprintf("[clocks] FATAL: linux: %v", err))
	}
	rachelSID = sys.MustGetShepherdByName("rachel")
	fc := fontcache.New(rachelSID)

	// Start uring dispatcher early so FontResponse notifications are processed
	// while OpenFace blocks waiting for replies.
	startUringDispatcher(fc)

	fonts := &mancini.FontConfig{
		LoadFace: func(bold bool, size int64) font.Face {
			style := mfont.Regular
			if bold {
				style = mfont.Bold
			}
			return fc.OpenFaceByName(mfont.DefaultMono, style, size)
		},
		FontRegular: mfont.DefaultMono,
		FontBold:    mfont.DefaultMono,
	}
	pal := mctheme.NewDefaultPaletteSwapRB()

	sys.UartWriteString("[clocks] fonts configured, building cities\n")

	// 3. Define cities with their timezones.
	cities := []cityInfo{
		{name: "Atlanta", id: "Atlanta", tz: "America/New_York", tzLabel: "US/New_York"},
		{name: "London", id: "London", tz: "Europe/London", tzLabel: "GB/London"},
		{name: "Paris", id: "Paris", tz: "Europe/Paris", tzLabel: "FR/Paris"},
		{name: "Tokyo", id: "Tokyo", tz: "Asia/Tokyo", tzLabel: "JP/Tokyo"},
		{name: "Auckland", id: "Auckland", tz: "Pacific/Auckland", tzLabel: "NZ/Auckland"},
		{name: "Los Angeles", id: "LosAngeles", tz: "America/Los_Angeles", tzLabel: "US/Los_Angeles"},
	}
	for i := range cities {
		sys.UartWriteDirectString("[clocks] loading tz " + cities[i].tz + "...\n")
		loc, err := time.LoadLocation(cities[i].tz)
		if err != nil {
			sys.UartWriteDirectString("[clocks] tz " + cities[i].tz + " FAILED\n")
			loc = time.UTC
		} else {
			sys.UartWriteDirectString("[clocks] tz " + cities[i].tz + " OK\n")
		}
		cities[i].loc = loc
	}
	sys.UartWriteString("[clocks] timezones loaded\n")
	// 4. Time tracking via constraint system — needed by Clock widgets.
	timeProg := mancini.BindStrings(mancini.ProgIdentityI64,
		"_source_", "attr:///kernel/int64/time/utc_seconds")
	timeSec := attr.ConstraintI64(attr.ShepherdURI("int64", "time_sec"), timeProg)
	timeSec.Get()
	nanosProg := mancini.BindStrings(mancini.ProgIdentityI64,
		"_source_", "attr:///kernel/int64/time/utc_nanos")
	timeNanos := attr.ConstraintI64(attr.ShepherdURI("int64", "time_nanos"), nanosProg)
	timeNanos.SetEager(true)
	_ = timeNanos.Get()
	sys.UartWriteString("[clocks] time constraints created\n")
	// 5. Build interactor tree: AppWindow → Row → 6 Columns → [Label, NeuCircle→Clock, Label].
	// All new-system types. Children discover parents via constraint network.
	textColor := color.NRGBA{0, 0, 0, 255}
	subtitleColor := mancini.SwapRB(color.NRGBA{78, 72, 112, 255})

	// Shared UTC time source — all clocks read from the same constraint handles.
	utcFunc := func() (int64, int64) {
		return timeSec.Get(), timeNanos.Get()
	}

	// Face name label font size and matching spacer height.
	faceNameFontSize := int64(14)
	faceNameH := faceNameFontSize + 4

	// Theme for labels — wraps fontcache in the Theme resolver.
	resolver := func(family string, feature mancini.Feature, size int64) font.Face {
		style := mfont.Regular
		switch feature {
		case mancini.Bold:
			style = mfont.Bold
		}
		return fc.OpenFaceByName(family, style, size)
	}
	transparent := color.NRGBA{0, 0, 0, 0}
	theme := mctheme.NewTheme(mctheme.NewDefaultPaletteWithColors(transparent, textColor), mctheme.NewDefaultNeumorphicParams(), mfont.DefaultMono, 18, resolver)
	subtitleTheme := mctheme.NewTheme(mctheme.NewDefaultPaletteWithColors(transparent, subtitleColor), mctheme.NewDefaultNeumorphicParams(), mfont.DefaultMono, 18, resolver)

	app := std.NewAppWindow(pal, "World Clocks",
		"attr:///kernel/int64/screen/width", "attr:///kernel/int64/screen/height")
	app.Focused = false // wait for rachel to grant focus

	// Scroller height = row height (constraint). Build the constraint
	// before the scroller so it's passed to the constructor.
	rowHeightURI := mancini.LayoutURI("main_row", mancini.DataTypeInt64, mancini.LayoutHeight)
	scrollerHeightProg := mancini.BindStrings(mancini.ProgIdentityI64,
		"_source_", rowHeightURI)
	scrollerHeight := attr.ConstraintI64(
		std.ScrollerHeightURI("scroller"), scrollerHeightProg)
	scroller := std.NewScroller("scroller", "AppWindow", pal, scrollerHeight)

	// Row: parent = "scroller" (child of the scroller viewport).
	row := std.NewRow("main_row", "scroller", pal, 0, mancini.AxisMinimum, 1)
	row.SetSpacing(25)

	for i, city := range cities {
		colName := city.id + "_col"
		circleName := city.id + "_circle"
		loc := city.loc

		// Normal faces: single click cycles through these.
		normalFaces := []mancini.ClockFace{
			&mancini.ClassicFace{HandColor: textColor, Loc: loc},
			&mancini.RomanFace{HandColor: textColor, Loc: loc},
			&mancini.MovadoFace{HandColor: textColor, Loc: loc},
			&mancini.DigitFace{HandColor: textColor, Loc: loc},
		}
		// Weird faces: double click cycles through these.
		weirdFaces := []mancini.ClockFace{
			&mancini.MetricFace{HandColor: textColor, Loc: loc},
			&mancini.PolarFace{HandColor: textColor, Loc: loc},
		}
		// Rotate starting normal face per city so each shows a different style.
		start := i % len(normalFaces)

		// Column: parent = "main_row". Created first so its name is registered.
		col := std.NewColumn(colName, "main_row", pal, 0, mancini.AxisMiddle, 1, false)
		col.SetSpacing(4)

		// Children created in display order — sequence numbers give deterministic ordering.
		_ = std.NewLabelNamedBold(city.id+"_name", colName, theme, city.name, 18)

		circle := std.NewNeuCircleNamed(circleName, colName, pal, mancini.Raised, *mctheme.NewDefaultNeumorphicParams().Light())
		_ = circle

		// Rotate normal faces so each city starts on a different one.
		rotNormal := make([]mancini.ClockFace, len(normalFaces))
		for j := range normalFaces {
			rotNormal[j] = normalFaces[(start+j)%len(normalFaces)]
		}
		clockWidget := std.NewClock(city.id+"_clock", circleName, pal, fonts, 70, utcFunc, rotNormal, weirdFaces)

		_ = std.NewLabelNamedColor(city.id+"_tz", colName, subtitleTheme, city.tzLabel, 18, subtitleColor)

		faceNameLabel := std.NewLabelNamedColor(city.id+"_facename", colName, subtitleTheme, rotNormal[0].FaceName(), faceNameFontSize, subtitleColor)
		faceNameLabel.TextFunc = func() string {
			return clockWidget.FaceNameAttr.Get()
		}

		spacer := std.NewSpacer(city.id+"_facespc", colName, 0, faceNameH)

		// Wire face label/spacer layout handles to clock for visibility toggling.
		clockWidget.FaceLabelLayout = faceNameLabel.GetLayout()
		clockWidget.FaceSpacerLayout = spacer.GetLayout()

		// Steady state: face name label visible, spacer hidden.
		spacer.GetLayout().Visible.Set(false)
	}

	rowLH := row.GetLayout()

	sys.UartWriteString("[clocks] interactor tree built\n")
	// 6. Read kernel screen dimensions for DrawContext sizing.
	screenWProg := mancini.BindStrings(mancini.ProgIdentityI64,
		"_source_", "attr:///kernel/int64/screen/width")
	screenWAttr := attr.ConstraintI64(attr.ShepherdURI("int64", "screen_w"), screenWProg)
	screenHProg := mancini.BindStrings(mancini.ProgIdentityI64,
		"_source_", "attr:///kernel/int64/screen/height")
	screenHAttr := attr.ConstraintI64(attr.ShepherdURI("int64", "screen_h"), screenHProg)
	screenW = int(screenWAttr.Get())
	screenH = int(screenHAttr.Get())
	sys.UartWriteString(fmt.Sprintf("[clocks] screen dimensions: %dx%d\n", screenW, screenH))
	// 7. Sizing draw — allocate a screen-sized scratch image and draw at (0,0)
	// to let the constraint system compute actual dimensions. No pixels from
	// this draw ever reach the framebuffer.
	provider := fontcache.NewFontSvcGlyphProvider(fc)

	appLH := app.GetLayout()
	appLH.X.Set(0)
	appLH.Y.Set(0)

	// Scroller width = viewport showing one clock column.
	scrollerLH := scroller.GetLayout()

	rowContentW := rowLH.Width.Get()
	winW := int(rowContentW / int64(len(cities)))
	if winW < 100 {
		winW = 300 // fallback
	}
	scrollerLH.Width.Set(int64(winW))

	// Virtual width = full row content (no vertical scrolling).
	scroller.SetVirtualSize(rowContentW, 0)

	winH := int(appLH.Height.Get())
	sys.UartWriteDirectString(fmt.Sprintf("[clocks] winW=%d winH=%d rowH=%d rowW=%d scrollerH=%d\n",
		winW, winH,
		rowLH.Height.Get(), rowContentW,
		scrollerLH.Height.Get()))
	if winH < 50 {
		winH = 300
	}

	// 8. Compute screen position via rachel's visibleArea constraints.
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

	// 9. Publish Ready, announce to rachel (no backing store — rachel allocates it).
	_ = appLH.Bounds.Get()
	readyAttr := attr.ValueBool(wm.ReadyURI(attr.SID()), true)
	_ = readyAttr
	sys.UartWriteString("[clocks] Ready=true\n")

	announceToWM(int32(winX), int32(winY), int32(winW), int32(winH))

	// 10. Wait for rachel to allocate backing store and share it with us.
	sys.UartWriteDirectString("[clocks] waiting for BackingStoreReady...\n")
	var bsr wm.BackingStoreReady
	for {
		raw := <-wmCh
		if b, ok := raw.(wm.BackingStoreReady); ok {
			bsr = b
			break
		}
	}

	// Initialize input dispatch pipeline.
	disp, clickAgent, _ := app.InitInput()
	disp.OriginX = int64(bsr.AppX)
	disp.OriginY = int64(bsr.AppY)
	mancini.SetScreenOrigin(int64(bsr.AppX), int64(bsr.AppY))
	disp.Debug = true
	disp.Tag = "clocks"

	// Create a []byte slice over the shared backing store.
	totalW := int(bsr.TotalWidth)
	totalH := int(bsr.TotalHeight)
	totalStride := int(bsr.TotalStride)
	bsSlice := unsafe.Slice((*byte)(unsafe.Pointer(uintptr(bsr.BackingStoreAddr))), totalStride*totalH)

	// Create image.RGBA over the full buffer (including border areas).
	bsImg := &image.RGBA{
		Pix:    bsSlice,
		Stride: totalStride,
		Rect:   image.Rect(0, 0, totalW, totalH),
	}
	dc := mancini.NewDrawContextForImage(bsImg, provider)

	// Translate origin so (0,0) is the app area, and clip to app bounds.
	leftInset := float64(bsr.LeftInset)
	topInset := float64(bsr.TopInset)
	dc.Push()
	dc.Translate(leftInset, topInset)
	dc.DrawRectangle(0, 0, float64(winW), float64(winH))
	dc.Clip()

	sys.UartWriteDirectString(fmt.Sprintf("[clocks] backing store ready: total=%dx%d inset=(%d,%d) app=%dx%d\n",
		totalW, totalH, bsr.LeftInset, bsr.TopInset, winW, winH))

	// 11. Draw to the backing store at (0,0) — screen position is rachel's concern.
	appLH.X.Set(0)
	appLH.Y.Set(0)
	dc.SetColor(pal.Surface())
	dc.FillRectangle(0, 0, float64(winW), float64(winH))
	app.SetDC(dc)
	app.Draw(app, 0, 0, int64(winW), int64(winH))

	// 12. Instrumentation counters.
	eagerAttr := attr.ValueI64(attr.ShepherdURI("int64", "stats/eagerUpdates"), 0)
	eagerSlot := eagerAttr.Slot()

	// 13. Main loop: select on WM messages and dirty attributes.
	dirtyCh := attr.OnDirty()
	var drawCount int64
	redraw := func() {
		_ = timeSec.Get()
		_ = timeNanos.Get()

		app.Draw(app, 0, 0, int64(winW), int64(winH))
		sendBlit()
		drawCount++
	}

	// clickTimer fires after ClickAgent's timeout so pending clicks
	// are committed even when no new WM messages arrive.
	var clickTimer <-chan time.Time

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
			case wm.MousePress:
				disp.DispatchWM(msg)
			case wm.MouseRelease:
				disp.DispatchWM(msg)
				// Arm timer to commit pending click after timeout.
				clickTimer = time.After(clickAgent.ClickTimeout + 10*time.Millisecond)
			default:
				disp.DispatchWM(msg)
			}
			if clickAgent.CheckTimer() {
				redraw()
			}
			sys.AttrIncrementI64(eagerSlot)
			redraw()
		case <-clickTimer:
			clickTimer = nil
			if clickAgent.CheckTimer() {
				redraw()
			}
		case <-dirtyCh:
			sys.AttrIncrementI64(eagerSlot)
			scroller.MarkContentDirty()
			redraw()
		}
	}
}

