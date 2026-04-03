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

	// Title bar: GradientTitle (animated gradient, bold, 22pt).
	gt := std.NewGradientTitle(pal, fonts, "World Clocks", 22, 8)
	app := std.NewAppWindow(nil, pal, *mctheme.NewDefaultNeumorphicParams().Heavy(), fonts, "World Clocks", 26, 850, gt.TitleDraw)
	app.Focused = false // wait for rachel to grant focus

	// Row: parent = "AppWindow" (std.AppWindow's fixed constraint name).
	row := std.NewRow("main_row", "AppWindow", pal, 0, mancini.AxisMinimum, 1)
	row.SetSpacing(25)

	for i, city := range cities {
		colName := city.id + "_col"
		circleName := city.id + "_circle"
		loc := city.loc

		// Build all face styles for this city's timezone.
		baseFaces := []mancini.ClockFace{
			&mancini.ClassicFace{HandColor: textColor, Loc: loc},
			&mancini.RomanFace{HandColor: textColor, Loc: loc},
			&mancini.MovadoFace{Loc: loc},
			&mancini.DigitFace{HandColor: textColor, Loc: loc},
			&mancini.MetricFace{HandColor: textColor, Loc: loc},
			&mancini.PolarFace{HandColor: textColor, Loc: loc},
		}
		start := i % len(baseFaces)
		rotated := make([]mancini.ClockFace, len(baseFaces))
		for j := range baseFaces {
			rotated[j] = baseFaces[(start+j)%len(baseFaces)]
		}

		// Column: parent = "main_row". Created first so its name is registered.
		col := std.NewColumn(colName, "main_row", pal, 0, mancini.AxisMiddle, 1, false)
		col.SetSpacing(15)

		// Children created in display order — sequence numbers give deterministic ordering.
		_ = std.NewLabelNamedBold(city.id+"_name", colName, theme, city.name, 18)

		circle := std.NewNeuCircleNamed(circleName, colName, pal, mancini.Raised, *mctheme.NewDefaultNeumorphicParams().Light())
		_ = circle

		clockWidget := std.NewClock(city.id+"_clock", circleName, pal, fonts, 70, utcFunc, rotated)

		_ = std.NewLabelNamedColor(city.id+"_tz", colName, subtitleTheme, city.tzLabel, 18, subtitleColor)

		faceNameLabel := std.NewLabelNamedColor(city.id+"_facename", colName, subtitleTheme, rotated[0].FaceName(), faceNameFontSize, subtitleColor)
		faceNameLabel.TextFunc = func() string {
			return clockWidget.FaceNameAttr.Get()
		}

		spacer := std.NewSpacer(city.id+"_facespc", colName, 0, faceNameH)

		// Wire face label/spacer layout handles to clock for visibility toggling.
		clockWidget.FaceLabelLayout = faceNameLabel.GetLayout()
		clockWidget.FaceSpacerLayout = spacer.GetLayout()

		// Steady state: face name label visible, spacer hidden.
		spacer.GetLayout().Visible.Set(false)

		_ = i
	}

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

	// Skip sizing draw for now — use constraint-computed width
	// (AppWindow sets 850 + margins) and a reasonable height.
	winW := int(appLH.Width.Get())
	winH := int(appLH.Height.Get())
	sys.UartWriteDirectString("[clocks] constraint size w=" + strconv.Itoa(winW) + " h=" + strconv.Itoa(winH) + "\n")
	if winW < 100 {
		winW = 850
	}
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

	// 13. Main loop: wake on dirty, draw to backing store, send blit to rachel.
	var drawCount int64
	var lastPrint int64
	for {
		attr.WaitDirty()
		sys.AttrIncrementI64(eagerSlot)

		sec := timeSec.Get()
		_ = timeNanos.Get()

		// Print current time every 10 seconds via fmt.Printf (goes through
		// write delegate → linux console, exercising the full IPC chain).
		if sec-lastPrint >= 10 {
			t := time.Unix(sec, 0).UTC()
			fmt.Printf("current time: %v\n", t)
			lastPrint = sec
		}

		// Draw to backing store at local (0,0) — translate+clip handle offset.
		app.Draw(app, 0, 0, int64(winW), int64(winH))
		sendBlit()
		drawCount++
		if drawCount%10 == 0 {
			sys.UartWriteDirectString(fmt.Sprintf("[clocks] draws=%d\n", drawCount))
		}
	}
}

