package main

import (
	"fmt"
	"image/color"
	"time"
	_ "time/tzdata"

	"sync/atomic"

	"golang.org/x/image/font"

	"mazzy/mazarin/attr"
	"mazzy/mazarin/fontcache"
	"mazzy/mazarin/interactor"
	"mazzy/mazarin/mancini"
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

// announceToWM sends AppStart to rachel and sets click targets on the MouseState.
// The mailbox receiver must already be running (started earlier for font loading).
func announceToWM(mouse *mancini.MouseState, clickTargets []mancini.Drawer) {
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

	// Now that AppStart is sent, rachel may send mouse events.
	// Set the click targets so they can be hit-tested.
	mouse.SetTargets(clickTargets)
}

// mailboxRecvLoopWithMouse receives notifications from rachel (e.g., YouHaveFocus, MousePress).
// Mouse events are dispatched through the MouseState state machine.
// FontResponse notifications are forwarded to the FontCache.
func mailboxRecvLoopWithMouse(mouse *mancini.MouseState, fc *fontcache.FontCache) {
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
				case wm.MsgMousePress:
					msg := (*wm.MousePressMsg)(unsafe.Pointer(&raw[0]))
					sys.UartWriteString(fmt.Sprintf("[clocks:mailbox] MousePress at (%d,%d) btn=%d\n", msg.X, msg.Y, msg.Button))
					mouse.Press(int64(msg.X), int64(msg.Y))
				case wm.MsgMouseMove:
					msg := (*wm.MouseMoveMsg)(unsafe.Pointer(&raw[0]))
					mouse.Move(int64(msg.X), int64(msg.Y))
				case wm.MsgMouseRelease:
					msg := (*wm.MouseReleaseMsg)(unsafe.Pointer(&raw[0]))
					sys.UartWriteString(fmt.Sprintf("[clocks:mailbox] MouseRelease at (%d,%d) btn=%d\n", msg.X, msg.Y, msg.Button))
					mouse.Release(int64(msg.X), int64(msg.Y))
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
	interactor.Init()
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
	// while OpenFace blocks waiting for replies. Mouse targets are set later
	// after the UI tree is built (rachel won't send mouse events until AppStart).
	mouse := mancini.NewMouseState(nil)
	go mailboxRecvLoopWithMouse(mouse, fc)

	fonts := &mancini.FontConfig{
		LoadFace: func(bold bool, size float64) font.Face {
			path := "/fonts/AtkinsonHyperlegibleMono-Regular.otf"
			if bold {
				path = "/fonts/AtkinsonHyperlegibleMono-Bold.otf"
			}
			return fc.OpenFace(path, bold, size)
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
	timeProg := interactor.BindStrings(interactor.ProgIdentityI64,
		"attr:///kernel/int64/time/utc_seconds")
	timeSec := attr.ConstraintI64(attr.ShepherdURI("int64", "time_sec"), timeProg)
	timeSec.Get()
	sys.UartWriteString(fmt.Sprintf("[clocks] timeSec constraint ready (T+%v)\n", time.Since(startTime)))

	nanosProg := interactor.BindStrings(interactor.ProgIdentityI64,
		"attr:///kernel/int64/time/utc_nanos")
	timeNanos := attr.ConstraintI64(attr.ShepherdURI("int64", "time_nanos"), nanosProg)
	timeNanos.SetEager(true)
	_ = timeNanos.Get()
	sys.UartWriteString(fmt.Sprintf("[clocks] timeNanos constraint ready (T+%v)\n", time.Since(startTime)))

	// 5. Build drawer tree: AppWindow → Row → 4 Columns → [Label, NeuCircle→Clock, Label].
	textColor := color.NRGBA{0, 0, 0, 255}
	subtitleColor := color.NRGBA{78, 72, 112, 255}

	// Shared UTC time source — all clocks read from the same constraint handles.
	utcFunc := func() (int64, int64) {
		return timeSec.Get(), timeNanos.Get()
	}

	// Face name label font size and matching spacer height.
	faceNameFontSize := 14.0
	faceNameH := faceNameFontSize + 4 // matches Label.PreferredHeight()

	// clickTargets collects the NeuCircle decorators wrapping each clock.
	// MousePolicy hit-tests against these (larger) bounds, then dispatches
	// to the child Clock via ChildAccessor.
	var clickTargets []mancini.Drawer

	sys.UartWriteString(fmt.Sprintf("[clocks] building columns... (T+%v)\n", time.Since(startTime)))
	var columns []mancini.Drawer
	for i, city := range cities {
		colName := city.id + "_col"
		circleName := city.id + "_circle"
		loc := city.loc

		// Build all four face styles for this city's timezone.
		// Rotate the list so each city starts on a different face style.
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

		cityLabel := &mancini.Label{
			Pal:      pal,
			Fonts:    fonts,
			Name:     city.id + "_name",
			Text:     city.name,
			FontSize: 18,
			Color:    textColor,
			Bold:     true,
		}

		clockWidget := &mancini.Clock{
			Pal:     pal,
			Fonts:   fonts,
			Name:    city.id + "_clock",
			Size:    34,
			UTCFunc: utcFunc,
			Faces:   rotated,
			Face:    rotated[0],
		}

		// Face name label — text driven by constraint attribute, updates
		// reactively during press-drag-release face cycling.
		faceNameLabel := &mancini.Label{
			Pal:      pal,
			Fonts:    fonts,
			Name:     city.id + "_facename",
			Text:     rotated[0].FaceName(), // fallback for InitLayout width measurement
			FontSize: faceNameFontSize,
			Color:    subtitleColor,
		}
		// Matching spacer — visible in steady state (same height as label).
		faceNameSpacer := &mancini.Spacer{
			Name:       city.id + "_facespc",
			PreferredH: faceNameH,
		}

		// Wire face label/spacer to the clock for visibility toggling.
		clockWidget.FaceLabel = faceNameLabel
		clockWidget.FaceSpacer = faceNameSpacer

		circle := &mancini.NeuCircle{
			Pal:    pal,
			Name:   circleName,
			Depth:  mancini.Raised,
			Params: mancini.ButtonParams,
			Child:  clockWidget,
		}
		clickTargets = append(clickTargets, circle)

		tzLabel := &mancini.Label{
			Pal:      pal,
			Fonts:    fonts,
			Name:     city.id + "_tz",
			Text:     city.tzLabel,
			FontSize: 18,
			Color:    subtitleColor,
		}

		col := &mancini.Column{
			Pal:        pal,
			Name:       colName,
			CrossAlign: mancini.AxisMiddle,
			Children:   []mancini.Drawer{cityLabel, circle, tzLabel, faceNameLabel, faceNameSpacer},
		}

		// InitLayout bottom-up: leaves first, then decorator, then container.
		sys.UartWriteString(fmt.Sprintf("[clocks] col %d: cityLabel.InitLayout... (T+%v)\n", i, time.Since(startTime)))
		cityLabel.InitLayout(colName)
		sys.UartWriteString(fmt.Sprintf("[clocks] col %d: cityLabel done, clockWidget... (T+%v)\n", i, time.Since(startTime)))
		clockWidget.InitLayout(circleName)
		sys.UartWriteString(fmt.Sprintf("[clocks] col %d: clockWidget done (T+%v)\n", i, time.Since(startTime)))
		circle.InitLayout(colName)
		sys.UartWriteString(fmt.Sprintf("[clocks] col %d: circle done, tzLabel... (T+%v)\n", i, time.Since(startTime)))
		tzLabel.InitLayout(colName)
		sys.UartWriteString(fmt.Sprintf("[clocks] col %d: tzLabel done, faceNameLabel... (T+%v)\n", i, time.Since(startTime)))
		faceNameLabel.InitLayout(colName) // measures width from static Text field
		faceNameSpacer.InitLayout(colName)
		col.InitLayout("main_row")
		col.SetSpacing(15)

		// Wire face name label to clock's FaceNameHandle AFTER InitLayout.
		// TextFunc reads the constraint attribute reactively during draw.
		faceNameLabel.TextFunc = func() string {
			return clockWidget.FaceNameHandle.Get()
		}

		// Steady state: face name label visible (non-empty text), spacer hidden.
		mancini.SetVisible(faceNameSpacer, false)

		columns = append(columns, col)
		sys.UartWriteString(fmt.Sprintf("[clocks] column %d (%s) built (T+%v)\n", i, city.id, time.Since(startTime)))
	}

	sys.UartWriteString(fmt.Sprintf("[clocks] UI tree built: %d columns (T+%v)\n", len(columns), time.Since(startTime)))

	row := &mancini.Row{
		Pal:               pal,
		Name:              "main_row",
		CrossAlign:        mancini.AxisMinimum,
		Children:          columns,
		ClipChildOverflow: true,
	}
	row.InitLayout("AppWindow")
	row.SetSpacing(20)

	// Title bar: AppTitleBar with a bold centered label.
	titleLabel := &mancini.Label{
		Pal:      pal,
		Fonts:    fonts,
		Name:     "title_label",
		Text:     "World Clocks",
		FontSize: 22,
		Color:    pal.Text,
		Bold:     true,
	}
	titleBar := &mancini.AppTitleBar{
		Pal:   pal,
		Fonts: fonts,
		Name:  "title_bar",
		Child: titleLabel,
	}
	titleBar.InitLayout("") // also creates child label's layout with Y constraint

	app := &mancini.AppWindow{
		Pal:      pal,
		Fonts:    fonts,
		Name:     "AppWindow",
		Title:    "World Clocks",
		Focused:  true,
		TitleBar: titleBar,
		Content:  row,
		MaxWidth: 850,
	}
	app.InitLayout("")

	// 6. Read kernel screen dimensions for DrawContext sizing.
	screenWProg := interactor.BindStrings(interactor.ProgIdentityI64,
		"attr:///kernel/int64/screen/width")
	screenWHandle := attr.ConstraintI64(attr.ShepherdURI("int64", "screen_w"), screenWProg)
	screenHProg := interactor.BindStrings(interactor.ProgIdentityI64,
		"attr:///kernel/int64/screen/height")
	screenHHandle := attr.ConstraintI64(attr.ShepherdURI("int64", "screen_h"), screenHProg)
	screenW = int(screenWHandle.Get())
	screenH = int(screenHHandle.Get())
	sys.UartWriteString(fmt.Sprintf("[clocks] screen: %dx%d\n", screenW, screenH))

	// 7. Create draw context covering the full screen. Clocks positions itself
	// within rachel's visibleArea via constraints, so it needs full-screen access.
	drawCtx := interactor.NewDrawContext(nil, 0, 0, screenW, screenH)
	fbImage := drawCtx.Image()

	// Single gg context for the entire draw pass — threaded through the tree.
	ggCtx := gg.NewContextForRGBA(fbImage)
	ggCtx.SwapRB = true
	sys.UartWriteString("[clocks] draw context created\n")

	// 8. Initial sizing draw at a small default size to publish children's dimensions
	// without spending forever computing neumorphic shadows at full-region size.
	initW, initH := 800.0, 250.0
	initX := float64(screenW)/2 - initW/2
	initY := float64(screenH)/2 - initH/2
	sys.UartWriteString("[clocks] sizing draw...\n")
	app.Draw(ggCtx, initX, initY, initW, initH)

	// Read constraint-computed size.
	winW := float64(app.Layout.Width.Get())
	winH := float64(app.Layout.Height.Get())
	if winW < 100 {
		winW = initW // fallback if constraints not yet valid
	}
	if winH < 50 {
		winH = initH
	}
	sys.UartWriteString(fmt.Sprintf("[clocks] constraint size: %.0fx%.0f\n", winW, winH))

	// 9. Force Bounds evaluation so the shared page has a valid rectangle,
	// then publish Ready. Rachel gates all interaction on Ready.
	_ = app.Layout.Bounds.Get()
	readyHandle := attr.ValueBool(wm.ReadyURI(attr.SID()), true)
	_ = readyHandle
	sys.UartWriteString(fmt.Sprintf("[clocks] Ready=true, Bounds published (T+%v)\n", time.Since(startTime)))

	// 10. Rachel already confirmed ready (step 2b). Announce to WM.
	announceToWM(mouse, clickTargets)

	// Use rachel's SID to read her visibleArea attributes.
	var posXHandle, posYHandle *attr.Handle[int64]
	if rachelSID >= 0 {
		rachelSIDStr := strconv.Itoa(rachelSID)
		vaXURI := "attr:///shepherd/" + rachelSIDStr + "/int64/visibleArea/x"
		vaYURI := "attr:///shepherd/" + rachelSIDStr + "/int64/visibleArea/y"
		vaWURI := "attr:///shepherd/" + rachelSIDStr + "/int64/visibleArea/w"

		// X = visibleArea.x + visibleArea.w - appWindow.Width (right-align)
		xProg := interactor.BindStrings(interactor.ProgAddSubDeref,
			vaXURI, vaWURI, app.Layout.Width.URI())
		posXHandle = attr.ConstraintI64(attr.ShepherdURI("int64", "pos/x"), xProg)

		// Y = visibleArea.y (top-align)
		yProg := interactor.BindStrings(interactor.ProgIdentityI64, vaYURI)
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
	if initX+initW > clearX1 {
		clearX1 = initX + initW
	}
	if initY+initH > clearY1 {
		clearY1 = initY + initH
	}
	ggCtx.SetColor(pal.Surface)
	ggCtx.FillRectangle(clearX0, clearY0, clearX1-clearX0, clearY1-clearY0)

	// Draw at the constraint-computed position.
	app.Draw(ggCtx, winX, winY, winW, winH)
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

		winW = float64(app.Layout.Width.Get())
		winH = float64(app.Layout.Height.Get())
		if posXHandle != nil {
			winX = float64(posXHandle.Get())
			winY = float64(posYHandle.Get())
		} else {
			winX = float64(screenW)/2 - winW/2
			winY = float64(screenH)/2 - winH/2
		}

		t0 := time.Now()
		app.Draw(ggCtx, winX, winY, winW, winH)
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

