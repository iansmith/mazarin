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
var app *std.AppWindow

// wmCh receives typed WM messages from the uring Dispatcher.
var wmCh = make(chan any, 4)

// announceToWM sends AppStart to rachel via uring.
func announceToWM(x, y, w, h int32) {
	app.AnnounceToWM(x, y, w, h)
}

// sendBlit tells rachel to copy our backing store to the framebuffer.
func sendBlit() {
	app.SendBlit()
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
		sys.UartWriteString("[clocks] loading tz " + cities[i].tz + "...\n")
		loc, err := time.LoadLocation(cities[i].tz)
		if err != nil {
			sys.UartWriteString("[clocks] tz " + cities[i].tz + " FAILED\n")
			loc = time.UTC
		} else {
			sys.UartWriteString("[clocks] tz " + cities[i].tz + " OK\n")
		}
		cities[i].loc = loc
	}
	sys.UartWriteString("[clocks] timezones loaded\n")
	// 4. Time tracking via constraint system — needed by Clock widgets.
	timeProg := mancini.BindStrings(mancini.ProgIdentityI64,
		"_source_", "attr:///kernel/int64/time/utc_seconds")
	timeSec := attr.ConstraintI64(attr.ShepherdURI("int64", "time_sec"), timeProg)
	timeSec.SetEager(true) // fire eagerCh once per second, not per nanosecond
	_ = timeSec.Get()
	nanosProg := mancini.BindStrings(mancini.ProgIdentityI64,
		"_source_", "attr:///kernel/int64/time/utc_nanos")
	timeNanos := attr.ConstraintI64(attr.ShepherdURI("int64", "time_nanos"), nanosProg)
	_ = timeNanos.Get()
	sys.UartWriteString("[clocks] time constraints created\n")
	// 5. Build interactor tree: AppWindow → Row → 6 Columns → [Label, NeuCircle→Clock, Label].
	// All new-system types. Children discover parents via constraint network.
	textColor := color.NRGBA{0, 0, 0, 255}
	subtitleColor := mancini.SwapRB(color.NRGBA{78, 72, 112, 255})

	// Shared UTC time source — reads directly from the kernel shared page
	// via trie lookup + seqlock. Zero SVCs per call.
	const secURI = "attr:///kernel/int64/time/utc_seconds"
	const nanosURI = "attr:///kernel/int64/time/utc_nanos"
	utcFunc := func() (int64, int64) {
		sec, _ := attr.ReadI64(secURI)
		nanos, _ := attr.ReadI64(nanosURI)
		return sec, nanos
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

	app = std.NewAppWindow(pal, "World Clocks")
	app.RachelSID = rachelSID
	app.Focused = false // wait for rachel to grant focus

	// Scroller width/height = AppWindow width/height (viewport matches window).
	appWidthURI := mancini.LayoutURI("AppWindow", mancini.DataTypeInt64, mancini.LayoutWidth)
	appHeightURI := mancini.LayoutURI("AppWindow", mancini.DataTypeInt64, mancini.LayoutHeight)
	scrollerWidth := attr.ConstraintI64(
		mancini.LayoutURI("scroller", mancini.DataTypeInt64, mancini.LayoutWidth),
		mancini.EqualI64(appWidthURI))
	scrollerHeightProg := mancini.BindStrings(mancini.ProgIdentityI64,
		"_source_", appHeightURI)
	scrollerHeight := attr.ConstraintI64(
		std.ScrollerHeightURI("scroller"), scrollerHeightProg)
	scroller := std.NewScroller("scroller", "AppWindow", pal, scrollerWidth, scrollerHeight, nil)

	// Row: inside-out sizing. Natural width = sum of children.
	// Scroller handles horizontal scrolling to show one city at a time.
	nCities := len(cities)
	cityColW := int64(200) // width per city column
	row := std.NewRow("main_row", "scroller", pal, 0, mancini.AxisMinimum, 0)
	row.SetSpacing(0)

	cityCols := make([]*std.ColumnPercentage, len(cities))
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

		// ColumnPercentage: percentage-based vertical layout for each city.
		// Width = cityColW so Row can compute natural total width.
		cityCols[i] = std.NewColumnPercentage(colName, "main_row", pal, cityColW, 0,
			[]float64{10, 48, 16, 16, 10})

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

	_ = row

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

	// Window size: show 1 city at a time, scroll horizontally to see others.
	winW := int(cityColW)
	winH := 210
	virtualW := cityColW * int64(nCities)

	// Set AppWindow dimensions first — scroller width/height are
	// constrained to these, so they pick up the values automatically.
	appLH.Width.Set(int64(winW))
	appLH.Height.Set(int64(winH))

	scroller.SetVirtualSize(virtualW, 0)
	_ = cityColW // snap support removed from Scroller
	sys.UartWriteString(fmt.Sprintf("[clocks] winW=%d winH=%d virtualW=%d\n",
		winW, winH, virtualW))

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
	sys.UartWriteString("[clocks] waiting for BackingStoreReady...\n")
	var bsr wm.BackingStoreReady
	for {
		raw := <-wmCh
		if b, ok := raw.(wm.BackingStoreReady); ok {
			bsr = b
			break
		}
	}

	// Update AppWindow from rachel's actual allocation.
	appLH.Width.Set(int64(bsr.AppWidth))
	appLH.Height.Set(int64(bsr.AppHeight))
	winW = int(bsr.AppWidth)
	winH = int(bsr.AppHeight)

	// Initialize input dispatch pipeline.
	disp, clickAgent, _ := app.InitInput()
	// Rachel converts screen→app-local coords before sending, so OriginX/Y = 0.
	mancini.SetScreenOrigin(int64(bsr.AppX), int64(bsr.AppY))
	disp.Debug = true
	disp.Tag = "clocks"
	sys.UartWriteString(fmt.Sprintf("[clocks] content area on screen: (%d,%d)-(%d,%d) origin=(%d,%d)\n",
		bsr.AppX, bsr.AppY, int32(winW)+bsr.AppX, int32(winH)+bsr.AppY, bsr.AppX, bsr.AppY))

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

	sys.UartWriteString(fmt.Sprintf("[clocks] backing store ready: total=%dx%d inset=(%d,%d) app=%dx%d appXY=(%d,%d)\n",
		totalW, totalH, bsr.LeftInset, bsr.TopInset, winW, winH, bsr.AppX, bsr.AppY))

	// 11. Draw to the backing store at (0,0) — screen position is rachel's concern.
	appLH.X.Set(0)
	appLH.Y.Set(0)
	dc.SetColor(pal.Surface())
	dc.FillRectangle(0, 0, float64(winW), float64(winH))
	app.SetDC(dc)
	app.Draw(app, 0, 0, int64(winW), int64(winH), image.Rect(0, 0, int(winW), int(winH)))

	// Debug: verify interactor tree dimensions after first draw.
	{
		scrollerLH := scroller.GetLayout()
		rowLH := row.GetLayout()
		sys.UartWriteString(fmt.Sprintf("[clocks] tree: app=%dx%d scroller=%dx%d row=%dx%d\n",
			appLH.Width.Get(), appLH.Height.Get(),
			scrollerLH.Width.Get(), scrollerLH.Height.Get(),
			rowLH.Width.Get(), rowLH.Height.Get()))
		for i, col := range cityCols {
			clh := col.GetLayout()
			sys.UartWriteString(fmt.Sprintf("[clocks] col[%d] %s: %dx%d @ x=%d y=%d\n",
				i, clh.Name(), clh.Width.Get(), clh.Height.Get(), clh.X.Get(), clh.Y.Get()))
			if i == 0 {
				// Dump first column's children dimensions.
				children := col.GetChildren()
				for j, child := range children {
					if l, ok := child.(mancini.Layouter); ok {
						ch := l.GetLayout()
						if ch != nil {
							sys.UartWriteString(fmt.Sprintf("[clocks]   child[%d] %s: %dx%d @ y=%d\n",
								j, ch.Name(), ch.Width.Get(), ch.Height.Get(), ch.Y.Get()))
						}
					}
				}
			}
		}
	}

	// 12. Main loop: select on WM messages and dirty attributes.
	eagerCh := attr.OnEager()
	redraw := func() {
		app.Draw(app, 0, 0, int64(winW), int64(winH), image.Rect(0, 0, int(winW), int(winH)))
		sendBlit()
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
			redraw()
		case <-clickTimer:
			clickTimer = nil
			if clickAgent.CheckTimer() {
				redraw()
			}
		case <-eagerCh:
			scroller.MarkContentDirty()
			redraw()
		}
	}
}

