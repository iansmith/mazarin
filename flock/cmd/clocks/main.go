package main

import (
	"fmt"
	"image/color"
	"time"
	_ "time/tzdata"

	"sync/atomic"

	"golang.org/x/image/font"

	"mazzy/mazarin/attr"
	mfont "mazzy/shared/font"
	"mazzy/mazarin/fontcache"
	"mazzy/mazarin/mancini"
	"mazzy/mazarin/mancini/std"
	mctheme "mazzy/mazarin/mancini/theme"
	"mazzy/mazarin/ringbuf"
	"mazzy/mazarin/sys"
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

// announceToWM sends AppStart to rachel.
// The mailbox receiver must already be running (started earlier for font loading).
func announceToWM() {
	rachelSID := sys.MustGetShepherdByName("rachel")

	myPID := os.Getpid()
	rb, err := ringbuf.New(rachelSID, 0, wm.SizeWMMessage, wm.DefaultSlotCount)
	if err != nil {
		sys.UartWriteString("[clocks] ring buffer creation failed: " + err.Error() + "\n")
		return
	}

	var msg wm.AppStartMsg
	msg.Type = wm.MsgAppStart
	msg.SID = int64(myPID)
	rb.Push(unsafe.Pointer(&msg))

	if err := sys.MailboxSend(rachelSID, wm.WMNotify, rb.Addr()); err != nil {
		sys.UartWriteString("[clocks] MailboxSend failed: " + err.Error() + "\n")
		return
	}
	sys.UartWriteString("[clocks] sent AppStart to rachel\n")
}

// mailboxRecvLoop receives notifications from rachel (e.g., YouHaveFocus, FontResponse).
func mailboxRecvLoop(fc *fontcache.FontCache) {
	for {
		notif, err := sys.MailboxRecv()
		if err != nil {
			sys.UartWriteString("[clocks:mailbox] recv error\n")
			continue
		}
		switch notif.Code {
		case wm.FontResponse:
			fc.HandleNotification(notif)
		case wm.ShepherdNotify:
			rb := ringbuf.Open(uintptr(notif.RingAddr))
			var raw [wm.SizeWMMessage]byte
			for rb.Pop(unsafe.Pointer(&raw[0])) {
				_ = *(*int64)(unsafe.Pointer(&raw[0]))
			}
		}
	}
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
	rachelSID := sys.MustGetShepherdByName("rachel")
	fc := fontcache.New(rachelSID)

	// Start mailbox receiver early so FontResponse notifications are processed
	// while OpenFace blocks waiting for replies.
	go mailboxRecvLoop(fc)

	fonts := &mancini.FontConfig{
		LoadFace: func(bold bool, size int64) font.Face {
			style := mfont.Regular
			if bold {
				style = mfont.Bold
			}
			return fc.OpenFaceByName(mfont.DefaultMono, style, size)
		},
	}
	pal := mctheme.NewDefaultPaletteSwapRB()

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
		loc, err := time.LoadLocation(cities[i].tz)
		if err != nil {
			sys.UartWriteString("[clocks] tz " + cities[i].tz + " failed: " + err.Error() + "\n")
			loc = time.UTC
		}
		cities[i].loc = loc
	}
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

	// 6. Read kernel screen dimensions for DrawContext sizing.
	screenWProg := mancini.BindStrings(mancini.ProgIdentityI64,
		"_source_", "attr:///kernel/int64/screen/width")
	screenWAttr := attr.ConstraintI64(attr.ShepherdURI("int64", "screen_w"), screenWProg)
	screenHProg := mancini.BindStrings(mancini.ProgIdentityI64,
		"_source_", "attr:///kernel/int64/screen/height")
	screenHAttr := attr.ConstraintI64(attr.ShepherdURI("int64", "screen_h"), screenHProg)
	screenW = int(screenWAttr.Get())
	screenH = int(screenHAttr.Get())
	// 7. Create draw context covering the full screen. Clocks positions itself
	// within rachel's visibleArea via constraints, so it needs full-screen access.
	drawCtx := mancini.NewFramebufferContext()
	fbImage := drawCtx.Image()

	// DrawContext for the entire draw pass — threaded through the tree.
	provider := fontcache.NewFontSvcGlyphProvider(fc)
	dc := mancini.NewDrawContextForImage(fbImage, provider)
	// 8. Initial sizing draw to publish children's dimensions.
	appLH := app.GetLayout()
	initX := float64(screenW)/2 - 400
	initY := float64(screenH)/2 - 125
	appLH.X.Set(int64(initX))
	appLH.Y.Set(int64(initY))
	app.SetDC(dc)
	app.Draw(app, int64(initX), int64(initY), appLH.Width.Get(), appLH.Height.Get())

	// Read constraint-computed size.
	winW := float64(appLH.Width.Get())
	winH := float64(appLH.Height.Get())
	if winW < 100 {
		winW = 800 // fallback if constraints not yet valid
	}
	if winH < 50 {
		winH = 250
	}
	// 9. Force Bounds evaluation so the shared page has a valid rectangle,
	// then publish Ready. Rachel gates all interaction on Ready.
	_ = appLH.Bounds.Get()
	readyAttr := attr.ValueBool(wm.ReadyURI(attr.SID()), true)
	_ = readyAttr
	sys.UartWriteString("[clocks] Ready=true\n")

	// 10. Rachel already confirmed ready (step 2b). Announce to WM.
	announceToWM()

	// Use rachel's SID to read her visibleArea attributes.
	var posXAttr, posYAttr *attr.Attribute[int64]
	if rachelSID >= 0 {
		rachelSIDStr := strconv.Itoa(rachelSID)
		vaXURI := "attr:///shepherd/" + rachelSIDStr + "/int64/visibleArea/x"
		vaYURI := "attr:///shepherd/" + rachelSIDStr + "/int64/visibleArea/y"
		vaWURI := "attr:///shepherd/" + rachelSIDStr + "/int64/visibleArea/w"

		// X = visibleArea.x + visibleArea.w - appWindow.Width (right-align)
		xProg := mancini.BindStrings(mancini.ProgAddSubDeref,
			"_a_", vaXURI, "_b_", vaWURI, "_c_", appLH.Width.URI())
		posXAttr = attr.ConstraintI64(attr.ShepherdURI("int64", "pos/x"), xProg)

		// Y = visibleArea.y (top-align)
		yProg := mancini.BindStrings(mancini.ProgIdentityI64, "_source_", vaYURI)
		posYAttr = attr.ConstraintI64(attr.ShepherdURI("int64", "pos/y"), yProg)

		_ = posXAttr.Get()
		_ = posYAttr.Get()
	} else {
		sys.UartWriteString("[clocks] WARNING: rachel not found, using fallback position\n")
	}

	// Compute initial window position.
	var winX, winY float64
	if posXAttr != nil {
		winX = float64(posXAttr.Get())
		winY = float64(posYAttr.Get())
	} else {
		// Fallback: center on screen.
		winX = float64(screenW)/2 - winW/2
		winY = float64(screenH)/2 - winH/2
	}

	// Clear the window area to surface color before final draw (removes sizing draw ghost).
	clearX0 := winX
	clearY0 := winY
	clearX1 := winX + winW
	clearY1 := winY + winH
	// Also clear the sizing draw ghost which may be at a different position.
	if initX < clearX0 {
		clearX0 = initX
	}
	if initY < clearY0 {
		clearY0 = initY
	}
	if initX+winW > clearX1 {
		clearX1 = initX + winW
	}
	if initY+winH > clearY1 {
		clearY1 = initY + winH
	}
	dc.SetColor(pal.Surface())
	dc.FillRectangle(clearX0, clearY0, clearX1-clearX0, clearY1-clearY0)

	// Draw at the constraint-computed position.
	appLH.X.Set(int64(winX))
	appLH.Y.Set(int64(winY))
	app.Draw(app, int64(winX), int64(winY), int64(winW), int64(winH))
	drawCtx.Flush(int32(winX), int32(winY), int32(winX+winW), int32(winY+winH))
	// 11. Instrumentation counters.
	eagerAttr := attr.ValueI64(attr.ShepherdURI("int64", "stats/eagerUpdates"), 0)
	eagerSlot := eagerAttr.Slot()
	var drawCount atomic.Int64

	// 12. Main loop: wake on dirty, redraw when second changes.
	// Position comes from constraints against rachel's visibleArea.
	for {
		attr.WaitDirty()
		sys.AttrIncrementI64(eagerSlot)

		_ = timeSec.Get()
		_ = timeNanos.Get()

		winW = float64(appLH.Width.Get())
		winH = float64(appLH.Height.Get())
		if posXAttr != nil {
			winX = float64(posXAttr.Get())
			winY = float64(posYAttr.Get())
		} else {
			winX = float64(screenW)/2 - winW/2
			winY = float64(screenH)/2 - winH/2
		}

		appLH.X.Set(int64(winX))
		appLH.Y.Set(int64(winY))
		app.Draw(app, int64(winX), int64(winY), int64(winW), int64(winH))
		drawCount.Add(1)
		drawCtx.Flush(int32(winX), int32(winY), int32(winX+winW), int32(winY+winH))
	}
}

