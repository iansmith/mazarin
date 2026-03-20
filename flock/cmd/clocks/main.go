package main

import (
	_ "embed"
	"fmt"
	"image/color"
	"time"
	_ "time/tzdata"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"

	"sync/atomic"

	"mazzy/mazarin/attr"
	"mazzy/mazarin/interactor"
	"mazzy/mazarin/mancini"
	"mazzy/mazarin/ringbuf"
	"mazzy/mazarin/sys"
	"mazzy/shared/wm"
	"os"
	"unsafe"

	"github.com/fogleman/gg"
)

//go:embed AtkinsonHyperlegibleMono-Regular.otf
var fontData []byte

//go:embed AtkinsonHyperlegibleMono-Bold.otf
var boldFontData []byte

const (
	// Right half of 1728×1117 display.
	regionX = 864
	regionY = 0
	regionW = 864
	regionH = 1117
)

type cityInfo struct {
	name    string // display name
	id      string // safe identifier for constraint names (no spaces)
	tz      string // IANA timezone for time.LoadLocation
	tzLabel string // display label for timezone row
	loc     *time.Location
}

// findShepherdByName looks up a shepherd SID by its launch filename.
// Returns -1 if not found.
func findShepherdByName(name string) int {
	entries, err := sys.ShepherdInfo()
	if err != nil {
		return -1
	}
	target := "/" + name + ".elf"
	for _, e := range entries {
		fn := string(e.Filename[:e.FilenameLen])
		if fn == target {
			return int(e.PID)
		}
	}
	return -1
}

// announceToWM finds rachel, sends AppStart, and starts the mailbox receiver.
// clickTargets are the interactors that can receive mouse press events.
func announceToWM(clickTargets []mancini.Drawer) {
	rachelSID := findShepherdByName("rachel")
	if rachelSID < 0 {
		sys.UartWriteString("[clocks] WARNING: rachel not found\n")
		return
	}

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

	// Start mailbox receiver for focus + mouse events.
	go mailboxRecvLoop(clickTargets)
}

// mailboxRecvLoop receives notifications from rachel (e.g., YouHaveFocus, MousePress).
// Mouse events are dispatched through the MouseState state machine.
func mailboxRecvLoop(clickTargets []mancini.Drawer) {
	mouse := mancini.NewMouseState(clickTargets)
	for {
		notif, err := sys.MailboxRecv()
		if err != nil {
			sys.UartWriteString("[clocks:mailbox] recv error\n")
			continue
		}
		if notif.Code == wm.ShepherdNotify {
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

	// 2. Parse embedded fonts and build a Theme.
	otFont, err := opentype.Parse(fontData)
	if err != nil {
		sys.UartWriteString("[clocks] font parse error: " + err.Error() + "\n")
		return
	}
	otFontBold, err := opentype.Parse(boldFontData)
	if err != nil {
		sys.UartWriteString("[clocks] bold font parse error: " + err.Error() + "\n")
		return
	}
	fontLoader := func(bold bool, size float64) font.Face {
		f := otFont
		if bold {
			f = otFontBold
		}
		face, err := opentype.NewFace(f, &opentype.FaceOptions{
			Size:    size,
			DPI:     72,
			Hinting: font.HintingFull,
		})
		if err != nil {
			return nil
		}
		return face
	}

	theme := &mancini.Theme{
		Pal:         mancini.DefaultPalette(),
		FontLoader:  fontLoader,
		ScaleFactor: 1.0,
		SwapRB:      true,
	}

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
	timeProg := interactor.BindStrings(interactor.ProgIdentityI64,
		"attr:///kernel/int64/time/utc_seconds")
	timeSec := attr.ConstraintI64(attr.ShepherdURI("int64", "time_sec"), timeProg)
	timeSec.Get()

	nanosProg := interactor.BindStrings(interactor.ProgIdentityI64,
		"attr:///kernel/int64/time/utc_nanos")
	timeNanos := attr.ConstraintI64(attr.ShepherdURI("int64", "time_nanos"), nanosProg)
	timeNanos.SetEager(true)
	_ = timeNanos.Get()

	// 5. Build drawer tree: AppWindow → Row → 4 Columns → [Label, NeuCircle→Clock, Label].
	textColor := color.NRGBA{0, 0, 0, 255}
	subtitleColor := color.NRGBA{78, 72, 112, 255}

	// Shared UTC time source — all clocks read from the same constraint handles.
	utcFunc := func() (int64, int64) {
		return timeSec.Get(), timeNanos.Get()
	}

	// Face name label font size and matching spacer height.
	faceNameFontSize := 14.0
	faceNameH := faceNameFontSize + theme.Px(4) // matches Label.PreferredHeight()

	// clickTargets collects the NeuCircle decorators wrapping each clock.
	// MousePolicy hit-tests against these (larger) bounds, then dispatches
	// to the child Clock via ChildAccessor.
	var clickTargets []mancini.Drawer

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
			Theme:    theme,
			Name:     city.id + "_name",
			Text:     city.name,
			FontSize: 18,
			Color:    textColor,
			Bold:     true,
		}

		clockWidget := &mancini.Clock{
			Theme:   theme,
			Name:    city.id + "_clock",
			Size:    50,
			UTCFunc: utcFunc,
			Faces:   rotated,
			Face:    rotated[0],
		}

		// Face name label — text driven by constraint attribute, updates
		// reactively during press-drag-release face cycling.
		faceNameLabel := &mancini.Label{
			Theme:    theme,
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
			Theme:  theme,
			Name:   circleName,
			Depth:  mancini.Raised,
			Params: mancini.ButtonParams,
			Child:  clockWidget,
		}
		clickTargets = append(clickTargets, circle)

		tzLabel := &mancini.Label{
			Theme:    theme,
			Name:     city.id + "_tz",
			Text:     city.tzLabel,
			FontSize: 18,
			Color:    subtitleColor,
		}

		col := &mancini.Column{
			Theme:      theme,
			Name:       colName,
			CrossAlign: mancini.AxisMiddle,
			Children:   []mancini.Drawer{cityLabel, circle, tzLabel, faceNameLabel, faceNameSpacer},
		}

		// InitLayout bottom-up: leaves first, then decorator, then container.
		cityLabel.InitLayout(colName)
		clockWidget.InitLayout(circleName)
		circle.InitLayout(colName)
		tzLabel.InitLayout(colName)
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
	}

	row := &mancini.Row{
		Theme:             theme,
		Name:              "main_row",
		CrossAlign:        mancini.AxisMinimum,
		Children:          columns,
		ClipChildOverflow: true,
	}
	row.InitLayout("AppWindow")
	row.SetSpacing(20)

	// Title bar: AppTitleBar with a bold centered label.
	titleLabel := &mancini.Label{
		Theme:    theme,
		Name:     "title_label",
		Text:     "World Clocks",
		FontSize: 22,
		Color:    theme.Pal.Text,
		Bold:     true,
	}
	titleBar := &mancini.AppTitleBar{
		Theme: theme,
		Name:  "title_bar",
		Child: titleLabel,
	}
	titleBar.InitLayout("") // also creates child label's layout with Y constraint

	app := &mancini.AppWindow{
		Theme:    theme,
		Name:     "AppWindow",
		Title:    "World Clocks",
		Focused:  true,
		TitleBar: titleBar,
		Content:  row,
	}
	app.InitLayout("")

	// 6. Create draw context.
	drawCtx := interactor.NewDrawContext(fontData, regionX, regionY, regionW, regionH)
	fbImage := drawCtx.Image()

	// Single gg context for the entire draw pass — threaded through the tree.
	ggCtx := gg.NewContextForRGBA(fbImage)
	sys.UartWriteString("[clocks] draw context created\n")

	// 7. Initial sizing draw at a small default size to publish children's dimensions
	// without spending forever computing neumorphic shadows at full-region size.
	initW, initH := 800.0, 250.0
	initX := float64(regionX) + (float64(regionW)-initW)/2
	initY := float64(regionY) + (float64(regionH)-initH)/2
	sys.UartWriteString("[clocks] sizing draw...\n")
	app.Draw(ggCtx, initX, initY, initW, initH)

	// Read constraint-computed size and center the window.
	winW := float64(app.Layout.Width.Get())
	winH := float64(app.Layout.Height.Get())
	if winW < 100 {
		winW = initW // fallback if constraints not yet valid
	}
	if winH < 50 {
		winH = initH
	}
	winX := float64(regionX) + (float64(regionW)-winW)/2
	winY := float64(regionY) + (float64(regionH)-winH)/2
	sys.UartWriteString(fmt.Sprintf("[clocks] constraint size: %.0fx%.0f\n", winW, winH))

	// Clear the region to surface color before final draw (removes sizing draw ghost).
	surfaceColor := theme.Pal.Surface
	if theme.SwapRB {
		surfaceColor = color.NRGBA{R: surfaceColor.B, G: surfaceColor.G, B: surfaceColor.R, A: surfaceColor.A}
	}
	for py := regionY; py < regionY+regionH; py++ {
		for px := regionX; px < regionX+regionW; px++ {
			off := fbImage.PixOffset(px, py)
			fbImage.Pix[off+0] = surfaceColor.R
			fbImage.Pix[off+1] = surfaceColor.G
			fbImage.Pix[off+2] = surfaceColor.B
			fbImage.Pix[off+3] = surfaceColor.A
		}
	}

	// Draw at the centered position with proper dimensions.
	app.Draw(ggCtx, winX, winY, winW, winH)
	drawCtx.FlushRegion()
	sys.UartWriteString(fmt.Sprintf("[clocks] initial draw done (T+%v)\n", time.Since(startTime)))

	// 8. Force Bounds evaluation so the shared page has a valid rectangle,
	// then publish Ready. Rachel gates all interaction on Ready.
	_ = app.Layout.Bounds.Get()
	readyHandle := attr.ValueBool(wm.ReadyURI(attr.SID()), true)
	_ = readyHandle
	sys.UartWriteString(fmt.Sprintf("[clocks] Ready=true, Bounds published (T+%v)\n", time.Since(startTime)))

	// 9. Announce to rachel (window manager).
	// All constraint attributes (including Bounds) are published by now,
	// so rachel can attach her own constraints to track our window.
	announceToWM(clickTargets)

	// 9. Instrumentation counters.
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

	// 9. Mouse events arrive from rachel (WM) via mailbox, not from kernel directly.

	// 10. Main loop: wake on dirty, redraw when second changes.
	// Clock widgets read timeSec directly via their TimeFunc closures.
	loopCount := 0
	for {
		attr.WaitDirty()
		sys.AttrIncrementI64(eagerSlot)
		loopCount++

		_ = timeSec.Get()
		_ = timeNanos.Get()

		winW = float64(app.Layout.Width.Get())
		winH = float64(app.Layout.Height.Get())
		winX = float64(regionX) + (float64(regionW)-winW)/2
		winY = float64(regionY) + (float64(regionH)-winH)/2

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
