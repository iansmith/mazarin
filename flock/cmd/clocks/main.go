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
	"mazzy/mazarin/ringbuf"
	"mazzy/mazarin/sys"
	"mazzy/shared/wm"
	"os"
	"strconv"
	"unsafe"

	"github.com/fogleman/gg"
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
	sys.UartWriteString(fmt.Sprintf("[clocks] found rachel SID=%d, my SID=%d (T+%v)\n", rachelSID, myPID, time.Since(startTime)))

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
	sys.UartWriteString(fmt.Sprintf("[clocks] sent AppStart to rachel (T+%v)\n", time.Since(startTime)))
}

// mailboxRecvLoop receives notifications from rachel (e.g., YouHaveFocus, FontResponse).
func mailboxRecvLoop(fc *fontcache.FontCache) {
	for {
		notif, err := sys.MailboxRecv()
		if err != nil {
			sys.UartWriteString("[clocks:mailbox] recv error\n")
			continue
		}
		sys.UartWriteString(fmt.Sprintf("[clocks:mailbox] notif code=%d from SID=%d\n", notif.Code, notif.SenderSID))
		switch notif.Code {
		case wm.FontResponse:
			sys.UartWriteString("[clocks:mailbox] FontResponse, calling HandleNotification\n")
			fc.HandleNotification(notif)
		case wm.ShepherdNotify:
			rb := ringbuf.Open(uintptr(notif.RingAddr))
			var raw [wm.SizeWMMessage]byte
			for rb.Pop(unsafe.Pointer(&raw[0])) {
				msgType := *(*int64)(unsafe.Pointer(&raw[0]))
				switch msgType {
				case wm.MsgYouHaveFocus:
					sys.UartWriteString(fmt.Sprintf("[clocks:mailbox] received YouHaveFocus! (T+%v)\n", time.Since(startTime)))
				case wm.MsgYouLostFocus:
					sys.UartWriteString("[clocks:mailbox] received YouLostFocus\n")
				default:
					sys.UartWriteString(fmt.Sprintf("[clocks:mailbox] unknown msg type %d\n", msgType))
				}
			}
		}
	}
}

var startTime time.Time

func main() {
	startTime = time.Now()
	sys.UartWriteString("[clocks] main() entered\n")

	// 1. Initialize constraint system.
	attr.Init()
	
	mancini.Init()
	sys.UartWriteString(fmt.Sprintf("[clocks] attr + interactor init done, SID=%s (T+%v)\n", attr.SID(), time.Since(startTime)))

	// 2. Wait for rachel (fontsvc) and disk (fs.maz) before creating fontcache.
	sys.UartWriteString(fmt.Sprintf("[clocks] waiting for rachel + disk ready... (T+%v)\n", time.Since(startTime)))
	if !sys.WaitForReady("rachel", 10*time.Second) {
		panic("[clocks] FATAL: rachel not ready after 10s")
	}
	if !sys.WaitForReady("disk", 10*time.Second) {
		panic("[clocks] FATAL: disk not ready after 10s")
	}
	sys.UartWriteString(fmt.Sprintf("[clocks] rachel + disk ready (T+%v)\n", time.Since(startTime)))

	rachelSID := sys.MustGetShepherdByName("rachel")
	fc := fontcache.New(rachelSID)
	sys.UartWriteString(fmt.Sprintf("[clocks] fontcache created, rachel SID=%d (T+%v)\n", rachelSID, time.Since(startTime)))

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
	sys.UartWriteString(fmt.Sprintf("[clocks] fonts configured via fontcache (T+%v)\n", time.Since(startTime)))

	pal := mancini.DefaultPalette()
	pal.SwapRB = true // propagated to offscreen gg contexts in draw.go

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
	sys.UartWriteString(fmt.Sprintf("[clocks] %d cities configured (T+%v)\n", len(cities), time.Since(startTime)))

	// 4. Time tracking via constraint system — needed by Clock widgets.
	sys.UartWriteString(fmt.Sprintf("[clocks] setting up time constraints... (T+%v)\n", time.Since(startTime)))
	timeProg := mancini.BindStrings(mancini.ProgIdentityI64,
		"_source_", "attr:///kernel/int64/time/utc_seconds")
	timeSec := attr.ConstraintI64(attr.ShepherdURI("int64", "time_sec"), timeProg)
	timeSec.Get()
	sys.UartWriteString(fmt.Sprintf("[clocks] timeSec constraint ready (T+%v)\n", time.Since(startTime)))

	nanosProg := mancini.BindStrings(mancini.ProgIdentityI64,
		"_source_", "attr:///kernel/int64/time/utc_nanos")
	timeNanos := attr.ConstraintI64(attr.ShepherdURI("int64", "time_nanos"), nanosProg)
	timeNanos.SetEager(true)
	_ = timeNanos.Get()
	sys.UartWriteString(fmt.Sprintf("[clocks] timeNanos constraint ready (T+%v)\n", time.Since(startTime)))

	// 5. Build interactor tree: AppWindow → Row → 6 Columns → [Label, NeuCircle→Clock, Label].
	// All new-system types. Children discover parents via constraint network.
	textColor := color.NRGBA{0, 0, 0, 255}
	subtitleColor := color.NRGBA{78, 72, 112, 255}

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
	theme := mancini.NewTheme(pal.Surface, textColor, mfont.DefaultMono, 18, resolver)
	subtitleTheme := mancini.NewTheme(pal.Surface, subtitleColor, mfont.DefaultMono, 18, resolver)

	// Title bar: GradientTitle (animated gradient, bold, 22pt).
	gt := std.NewGradientTitle(pal, fonts, "World Clocks", 22, 8)
	app := std.NewAppWindow(nil, pal, fonts, "World Clocks", 26, 850, gt.TitleDraw)
	app.Focused = false // wait for rachel to grant focus

	// Row: parent = "AppWindow" (std.AppWindow's fixed constraint name).
	row := std.NewRow("main_row", "AppWindow", pal, 0, mancini.AxisMinimum)
	row.SetSpacing(20)

	sys.UartWriteString(fmt.Sprintf("[clocks] building columns... (T+%v)\n", time.Since(startTime)))
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
		col := std.NewColumn(colName, "main_row", pal, mancini.AxisMiddle, false)
		col.SetSpacing(15)

		// Children created in display order — sequence numbers give deterministic ordering.
		_ = std.NewLabelNamedBold(city.id+"_name", colName, theme, city.name, 18)

		circle := std.NewNeuCircleNamed(circleName, colName, pal, mancini.Raised, mancini.ButtonParams)
		_ = circle

		clockWidget := std.NewClock(city.id+"_clock", circleName, pal, fonts, 34, utcFunc, rotated)

		_ = std.NewLabelNamedColor(city.id+"_tz", colName, subtitleTheme, city.tzLabel, 18, subtitleColor)

		faceNameLabel := std.NewLabelNamedColor(city.id+"_facename", colName, subtitleTheme, rotated[0].FaceName(), faceNameFontSize, subtitleColor)
		faceNameLabel.TextFunc = func() string {
			return clockWidget.FaceNameHandle.Get()
		}

		spacer := std.NewSpacer(city.id+"_facespc", colName, 0, faceNameH)

		// Wire face label/spacer layout handles to clock for visibility toggling.
		clockWidget.FaceLabelLayout = faceNameLabel.GetLayout()
		clockWidget.FaceSpacerLayout = spacer.GetLayout()

		// Steady state: face name label visible, spacer hidden.
		spacer.GetLayout().Visible.Set(false)

		sys.UartWriteString(fmt.Sprintf("[clocks] column %d (%s) built (T+%v)\n", i, city.id, time.Since(startTime)))
	}

	sys.UartWriteString(fmt.Sprintf("[clocks] UI tree built: %d cities (T+%v)\n", len(cities), time.Since(startTime)))

	// 6. Read kernel screen dimensions for DrawContext sizing.
	screenWProg := mancini.BindStrings(mancini.ProgIdentityI64,
		"_source_", "attr:///kernel/int64/screen/width")
	screenWHandle := attr.ConstraintI64(attr.ShepherdURI("int64", "screen_w"), screenWProg)
	screenHProg := mancini.BindStrings(mancini.ProgIdentityI64,
		"_source_", "attr:///kernel/int64/screen/height")
	screenHHandle := attr.ConstraintI64(attr.ShepherdURI("int64", "screen_h"), screenHProg)
	screenW = int(screenWHandle.Get())
	screenH = int(screenHHandle.Get())
	sys.UartWriteString(fmt.Sprintf("[clocks] screen: %dx%d\n", screenW, screenH))

	// 7. Create draw context covering the full screen. Clocks positions itself
	// within rachel's visibleArea via constraints, so it needs full-screen access.
	drawCtx := mancini.NewFramebufferContext()
	fbImage := drawCtx.Image()

	// Single gg context for the entire draw pass — threaded through the tree.
	ggCtx := gg.NewContextForRGBA(fbImage)
	ggCtx.SwapRB = true
	sys.UartWriteString("[clocks] draw context created\n")

	// 8. Initial sizing draw to publish children's dimensions.
	appLH := app.GetLayout()
	initX := float64(screenW)/2 - 400
	initY := float64(screenH)/2 - 125
	sys.UartWriteString("[clocks] sizing draw...\n")
	appLH.X.Set(int64(initX))
	appLH.Y.Set(int64(initY))
	app.SetDC(ggCtx)
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
	sys.UartWriteString(fmt.Sprintf("[clocks] constraint size: %.0fx%.0f\n", winW, winH))

	// 9. Force Bounds evaluation so the shared page has a valid rectangle,
	// then publish Ready. Rachel gates all interaction on Ready.
	_ = appLH.Bounds.Get()
	readyHandle := attr.ValueBool(wm.ReadyURI(attr.SID()), true)
	_ = readyHandle
	sys.UartWriteString(fmt.Sprintf("[clocks] Ready=true, Bounds published (T+%v)\n", time.Since(startTime)))

	// 10. Rachel already confirmed ready (step 2b). Announce to WM.
	announceToWM()

	// Use rachel's SID to read her visibleArea attributes.
	var posXHandle, posYHandle *attr.Handle[int64]
	if rachelSID >= 0 {
		rachelSIDStr := strconv.Itoa(rachelSID)
		vaXURI := "attr:///shepherd/" + rachelSIDStr + "/int64/visibleArea/x"
		vaYURI := "attr:///shepherd/" + rachelSIDStr + "/int64/visibleArea/y"
		vaWURI := "attr:///shepherd/" + rachelSIDStr + "/int64/visibleArea/w"

		// X = visibleArea.x + visibleArea.w - appWindow.Width (right-align)
		xProg := mancini.BindStrings(mancini.ProgAddSubDeref,
			"_a_", vaXURI, "_b_", vaWURI, "_c_", appLH.Width.URI())
		posXHandle = attr.ConstraintI64(attr.ShepherdURI("int64", "pos/x"), xProg)

		// Y = visibleArea.y (top-align)
		yProg := mancini.BindStrings(mancini.ProgIdentityI64, "_source_", vaYURI)
		posYHandle = attr.ConstraintI64(attr.ShepherdURI("int64", "pos/y"), yProg)

		x := posXHandle.Get()
		y := posYHandle.Get()
		sys.UartWriteString(fmt.Sprintf("[clocks] position constraints: x=%d y=%d (from rachel SID %d)\n", x, y, rachelSID))
	} else {
		sys.UartWriteString("[clocks] WARNING: rachel not found, using fallback position\n")
	}

	// Compute initial window position.
	var winX, winY float64
	if posXHandle != nil {
		winX = float64(posXHandle.Get())
		winY = float64(posYHandle.Get())
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
	ggCtx.SetColor(pal.Surface)
	ggCtx.FillRectangle(clearX0, clearY0, clearX1-clearX0, clearY1-clearY0)

	// Draw at the constraint-computed position.
	appLH.X.Set(int64(winX))
	appLH.Y.Set(int64(winY))
	app.Draw(app, int64(winX), int64(winY), int64(winW), int64(winH))
	drawCtx.Flush(int32(winX), int32(winY), int32(winX+winW), int32(winY+winH))
	sys.UartWriteString(fmt.Sprintf("[clocks] initial draw done at (%.0f,%.0f) (T+%v)\n", winX, winY, time.Since(startTime)))

	// 11. Instrumentation counters.
	eagerHandle := attr.ValueI64(attr.ShepherdURI("int64", "stats/eagerUpdates"), 0)
	eagerSlot := eagerHandle.Slot()
	var drawCount atomic.Int64

	// Periodic stats printer (~every 10 seconds).
	go func() {
		_ = eagerSlot // used for increment in main loop
		for {
			time.Sleep(10 * time.Second)
			eager := eagerHandle.Get()
			draws := drawCount.Load()
			sys.UartWriteString(fmt.Sprintf("[clocks-stats] eagerUpdates=%d draws=%d\n",
				eager, draws))
		}
	}()

	// 12. Main loop: wake on dirty, redraw when second changes.
	// Position comes from constraints against rachel's visibleArea.
	loopCount := 0
	for {
		attr.WaitDirty()
		sys.AttrIncrementI64(eagerSlot)
		loopCount++

		_ = timeSec.Get()
		_ = timeNanos.Get()

		winW = float64(appLH.Width.Get())
		winH = float64(appLH.Height.Get())
		if posXHandle != nil {
			winX = float64(posXHandle.Get())
			winY = float64(posYHandle.Get())
		} else {
			winX = float64(screenW)/2 - winW/2
			winY = float64(screenH)/2 - winH/2
		}

		appLH.X.Set(int64(winX))
		appLH.Y.Set(int64(winY))
		t0 := time.Now()
		app.Draw(app, int64(winX), int64(winY), int64(winW), int64(winH))
		drawDur := time.Since(t0)
		drawCount.Add(1)

		t1 := time.Now()
		drawCtx.Flush(int32(winX), int32(winY), int32(winX+winW), int32(winY+winH))
		flushDur := time.Since(t1)

		if loopCount <= 10 || loopCount%10 == 0 {
			sys.UartWriteString(fmt.Sprintf("[clocks] loop=%d draw=%v flush=%v pos=(%.0f,%.0f) sz=(%.0f,%.0f)\n",
				loopCount, drawDur, flushDur, winX, winY, winW, winH))
		}
	}
}

