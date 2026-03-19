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
	"mazzy/shared/hid"
	"mazzy/shared/wm"
	"os"
	"unsafe"
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
	name    string
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

// mailboxRecvLoop receives notifications from rachel (e.g., YouHaveFocus).
func mailboxRecvLoop() {
	for {
		notif, err := sys.MailboxRecv()
		if err != nil {
			sys.UartWriteString("[uitest:mailbox] recv error\n")
			continue
		}
		if notif.Code == wm.ShepherdNotify {
			rb := ringbuf.Open(uintptr(notif.RingAddr))
			var raw [wm.SizeWMMessage]byte
			for rb.Pop(unsafe.Pointer(&raw[0])) {
				msgType := *(*int64)(unsafe.Pointer(&raw[0]))
				switch msgType {
				case wm.MsgYouHaveFocus:
					sys.UartWriteString("[uitest:mailbox] received YouHaveFocus!\n")
				case wm.MsgYouLostFocus:
					sys.UartWriteString("[uitest:mailbox] received YouLostFocus\n")
				default:
					sys.UartWriteString(fmt.Sprintf("[uitest:mailbox] unknown msg type %d\n", msgType))
				}
			}
		}
	}
}

func main() {
	sys.UartWriteString("[uitest] main() entered\n")

	// 1. Initialize constraint system.
	attr.Init()
	interactor.Init("uitest")
	mancini.Init("uitest")
	sys.UartWriteString("[uitest] attr + interactor init done\n")

	// Find rachel (window manager) and send AppStart so she can grant us focus.
	rachelSID := findShepherdByName("rachel")
	if rachelSID < 0 {
		sys.UartWriteString("[uitest] WARNING: rachel not found, requesting focus directly\n")
		sys.SetInputFocus(0, hid.InputClassKeyboard)
		sys.SetInputFocus(0, hid.InputClassMouseClick)
	} else {
		myPID := os.Getpid()
		sys.UartWriteString(fmt.Sprintf("[uitest] found rachel SID=%d, my SID=%d\n", rachelSID, myPID))

		// Create ring buffer targeting rachel
		rb, err := ringbuf.New(rachelSID, 0, wm.SizeWMMessage, wm.DefaultSlotCount)
		if err != nil {
			sys.UartWriteString("[uitest] ring buffer creation failed: " + err.Error() + "\n")
		} else {
			// Push AppStart message
			var msg wm.AppStartMsg
			msg.Type = wm.MsgAppStart
			msg.SID = int64(myPID)
			rb.Push(unsafe.Pointer(&msg))

			// Notify rachel
			if err := sys.MailboxSend(rachelSID, wm.WMNotify, rb.Addr()); err != nil {
				sys.UartWriteString("[uitest] MailboxSend failed: " + err.Error() + "\n")
			} else {
				sys.UartWriteString("[uitest] sent AppStart to rachel\n")
			}
		}

		// Start mailbox receiver to get focus notifications from rachel.
		go mailboxRecvLoop()
	}

	// 2. Parse embedded fonts and build a Theme.
	otFont, err := opentype.Parse(fontData)
	if err != nil {
		sys.UartWriteString("[uitest] font parse error: " + err.Error() + "\n")
		return
	}
	otFontBold, err := opentype.Parse(boldFontData)
	if err != nil {
		sys.UartWriteString("[uitest] bold font parse error: " + err.Error() + "\n")
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
		{name: "Atlanta", tz: "America/New_York", tzLabel: "US/New_York"},
		{name: "London", tz: "Europe/London", tzLabel: "GB/London"},
		{name: "Paris", tz: "Europe/Paris", tzLabel: "FR/Paris"},
		{name: "Tokyo", tz: "Asia/Tokyo", tzLabel: "JP/Tokyo"},
	}
	for i := range cities {
		loc, err := time.LoadLocation(cities[i].tz)
		if err != nil {
			sys.UartWriteString("[uitest] tz " + cities[i].tz + " failed: " + err.Error() + "\n")
			loc = time.UTC
		}
		cities[i].loc = loc
	}

	// 4. Time tracking via constraint system — needed by Clock widgets.
	timeProg := interactor.BindStrings(interactor.ProgIdentityI64,
		"attr:///kernel/int64/time/utc_seconds")
	timeSec := attr.ConstraintI64("attr:///shepherd/uitest/int64/time_sec", timeProg)
	timeSec.Get()

	nanosProg := interactor.BindStrings(interactor.ProgIdentityI64,
		"attr:///kernel/int64/time/utc_nanos")
	timeNanos := attr.ConstraintI64("attr:///shepherd/uitest/int64/time_nanos", nanosProg)
	timeNanos.SetEager(true)
	_ = timeNanos.Get()

	// 5. Build drawer tree: AppWindow → Row → 4 Columns → [Label, NeuCircle→Clock, Label].
	textColor := color.NRGBA{0, 0, 0, 255}
	subtitleColor := color.NRGBA{78, 72, 112, 255}

	var columns []mancini.Drawer
	for _, city := range cities {
		colName := city.name + "_col"
		circleName := city.name + "_circle"
		loc := city.loc // capture for closure

		cityLabel := &mancini.Label{
			Theme:    theme,
			Name:     city.name + "_name",
			Text:     city.name,
			FontSize: 18,
			Color:    textColor,
			Bold:     true,
		}

		clockFace := &mancini.Clock{
			Theme: theme,
			Name:  city.name + "_clock",
			Size:  50,
			Color: textColor,
			TimeFunc: func() (int, int, int, int) {
				sec := timeSec.Get()
				nanos := timeNanos.Get()
				millis := int(nanos / 1_000_000)
				t := time.Unix(sec, 0).In(loc)
				return t.Hour(), t.Minute(), t.Second(), millis
			},
		}

		circle := &mancini.NeuCircle{
			Theme:  theme,
			Name:   circleName,
			Depth:  mancini.Raised,
			Params: mancini.ButtonParams,
			Child:  clockFace,
		}

		tzLabel := &mancini.Label{
			Theme:    theme,
			Name:     city.name + "_tz",
			Text:     city.tzLabel,
			FontSize: 18,
			Color:    subtitleColor,
		}

		col := &mancini.Column{
			Theme:    theme,
			Name:     colName,
			Children: []mancini.Drawer{cityLabel, circle, tzLabel},
		}

		// InitLayout bottom-up: leaves first, then decorator, then container.
		cityLabel.InitLayout(colName)
		clockFace.InitLayout(circleName)
		circle.InitLayout(colName)
		tzLabel.InitLayout(colName)
		col.InitLayout("main_row")
		col.SetSpacing(15)

		columns = append(columns, col)
	}

	row := &mancini.Row{
		Theme:    theme,
		Name:     "main_row",
		Children: columns,
	}
	row.InitLayout("clock_window")
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
		Name:     "clock_window",
		Title:    "World Clocks",
		Focused:  true,
		TitleBar: titleBar,
		Content:  row,
	}
	app.InitLayout("")

	// 6. Create draw context.
	dc := interactor.NewDrawContext(fontData, regionX, regionY, regionW, regionH)
	fbImage := dc.Image()
	sys.UartWriteString("[uitest] draw context created\n")

	// 7. Initial sizing draw at a small default size to publish children's dimensions
	// without spending forever computing neumorphic shadows at full-region size.
	initW, initH := 600.0, 200.0
	initX := float64(regionX) + (float64(regionW)-initW)/2
	initY := float64(regionY) + (float64(regionH)-initH)/2
	sys.UartWriteString("[uitest] sizing draw...\n")
	app.Draw(fbImage, initX, initY, initW, initH)

	// Debug: check intrinsic sizes after sizing draw.
	sys.UartWriteString(fmt.Sprintf("[uitest] row intrinsic: w=%d h=%d\n",
		row.Layout.Width.Get(), row.Layout.Height.Get()))
	sys.UartWriteString(fmt.Sprintf("[uitest] row preferred: w=%.0f h=%.0f\n",
		row.PreferredWidth(), row.PreferredHeight()))

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
	sys.UartWriteString(fmt.Sprintf("[uitest] constraint size: %.0fx%.0f\n", winW, winH))

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
	app.Draw(fbImage, winX, winY, winW, winH)
	dc.FlushRegion()
	sys.UartWriteString("[uitest] initial draw done, entering loop\n")

	// 8. Instrumentation counters.
	eagerHandle := attr.ValueI64("attr:///shepherd/uitest/int64/stats/eagerUpdates", 0)
	eagerSlot := eagerHandle.Slot()
	var drawCount atomic.Int64

	// Periodic stats printer (~every 10 seconds).
	go func() {
		_ = eagerSlot // used for increment in main loop
		for {
			time.Sleep(10 * time.Second)
			eager := eagerHandle.Get()
			draws := drawCount.Load()
			sys.UartWriteString(fmt.Sprintf("[uitest-stats] eagerUpdates=%d draws=%d\n",
				eager, draws))
		}
	}()

	// 9. Input event listeners — log to UART so we can see focus routing works.
	go func() {
		var buf hid.SoftIRQReturn
		for {
			n, err := sys.WaitInputEvent(hid.InputClassKeyboard, &buf)
			if err != nil {
				continue
			}
			for i := 0; i < n; i++ {
				ev := buf.Events[i]
				if ev.Type == 1 && ev.Value == 1 { // EV_KEY press only
					sys.UartWriteString(fmt.Sprintf("[uitest:kbd] code=%d\n", ev.Code))
				}
			}
		}
	}()
	go func() {
		var buf hid.SoftIRQReturn
		for {
			n, err := sys.WaitInputEvent(hid.InputClassMouseClick, &buf)
			if err != nil {
				continue
			}
			for i := 0; i < n; i++ {
				ev := buf.Events[i]
				if ev.Type == 1 { // EV_KEY (button)
					action := "press"
					if ev.Value == 0 {
						action = "release"
					}
					sys.UartWriteString(fmt.Sprintf("[uitest:click] btn=%d %s\n", ev.Code, action))
				}
			}
		}
	}()

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
		app.Draw(fbImage, winX, winY, winW, winH)
		drawDur := time.Since(t0)
		drawCount.Add(1)

		t1 := time.Now()
		dc.Flush(int32(winX), int32(winY), int32(winX+winW), int32(winY+winH))
		flushDur := time.Since(t1)

		if loopCount <= 10 || loopCount%10 == 0 {
			sys.UartWriteString(fmt.Sprintf("[uitest] loop=%d draw=%v flush=%v pos=(%.0f,%.0f) sz=(%.0f,%.0f)\n",
				loopCount, drawDur, flushDur, winX, winY, winW, winH))
		}
	}
}
