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
	"mazzy/mazarin/sys"
)

//go:embed AtkinsonHyperlegibleMono-Regular.otf
var fontData []byte

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
	content *attr.Handle[string]
}

func main() {
	sys.UartWriteString("[uitest] main() entered\n")

	// 1. Initialize constraint system.
	attr.Init()
	interactor.Init("uitest")
	mancini.Init("uitest")
	sys.UartWriteString("[uitest] attr + interactor init done\n")

	// 2. Parse embedded font and build a Theme.
	otFont, err := opentype.Parse(fontData)
	if err != nil {
		sys.UartWriteString("[uitest] font parse error: " + err.Error() + "\n")
		return
	}
	fontLoader := func(bold bool, size float64) font.Face {
		face, err := opentype.NewFace(otFont, &opentype.FaceOptions{
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

	// 3. Define three cities with their timezones.
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
		uri := "attr:///priest/uitest/str/" + cities[i].name + "_clock/content"
		cities[i].content = attr.ValueStr(uri, "00:00:00")
	}

	// 4. Build drawer tree: AppWindow → Row → 3 Columns → 3 Labels each.
	textColor := color.NRGBA{0, 0, 0, 255}
	subtitleColor := color.NRGBA{78, 72, 112, 255}

	var columns []mancini.Drawer
	for _, city := range cities {
		colName := city.name + "_col"
		content := city.content // capture for closure

		cityLabel := &mancini.Label{
			Theme:    theme,
			Name:     city.name + "_name",
			Text:     city.name,
			FontSize: 16,
			Color:    textColor,
			Bold:     true,
		}

		clockLabel := &mancini.Label{
			Theme:    theme,
			Name:     city.name + "_clock",
			TextFunc: func() string { return content.Get() },
			FontSize: 24,
			Color:    textColor,
		}

		tzLabel := &mancini.Label{
			Theme:    theme,
			Name:     city.name + "_tz",
			Text:     city.tzLabel,
			FontSize: 12,
			Color:    subtitleColor,
		}

		col := &mancini.Column{
			Theme:    theme,
			Name:     colName,
			Spacing:  4,
			Children: []mancini.Drawer{cityLabel, clockLabel, tzLabel},
		}

		// InitLayout bottom-up: leaves first, then container.
		cityLabel.InitLayout(colName)
		clockLabel.InitLayout(colName)
		tzLabel.InitLayout(colName)
		col.InitLayout("main_row")

		columns = append(columns, col)
	}

	row := &mancini.Row{
		Theme:    theme,
		Name:     "main_row",
		Spacing:  12,
		Children: columns,
	}
	row.InitLayout("clock_window")

	app := &mancini.AppWindow{
		Theme:    theme,
		Name:     "clock_window",
		Title:    "World Clocks",
		Focused:  true,
		TitleBar: mancini.StripedTitleFace(theme, "World Clocks", theme.Px(10), theme.Px(8)),
		Content:  row,
	}
	app.InitLayout("")

	// 5. Create draw context.
	dc := interactor.NewDrawContext(fontData, regionX, regionY, regionW, regionH)
	fbImage := dc.Image()
	sys.UartWriteString("[uitest] draw context created\n")

	// 6. Initial sizing draw at a small default size to publish children's dimensions
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

	// 7. Time tracking via constraint system — shared UTC source.
	// Depend on utc_nanos (updated at time_update_hertz frequency) so we get
	// woken at the configured rate, not just once per second.
	timeProg := interactor.BindStrings(interactor.ProgIdentityI64,
		"attr:///kernel/int64/time/utc_seconds")
	timeSec := attr.ConstraintI64("attr:///priest/uitest/int64/time_sec", timeProg)
	timeSec.Get()

	nanosProg := interactor.BindStrings(interactor.ProgIdentityI64,
		"attr:///kernel/int64/time/utc_nanos")
	timeNanos := attr.ConstraintI64("attr:///priest/uitest/int64/time_nanos", nanosProg)
	timeNanos.SetEager(true)
	_ = timeNanos.Get()

	// 8. Instrumentation counters.
	eagerHandle := attr.ValueI64("attr:///priest/uitest/int64/stats/eagerUpdates", 0)
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

	// 9. Main loop: wake on dirty, update clocks only when second changes, redraw.
	loopCount := 0
	var lastSec int64
	for {
		attr.WaitDirty()
		sys.AttrIncrementI64(eagerSlot)
		loopCount++

		sec := timeSec.Get()
		if sec == lastSec {
			// Second hasn't changed — no visual update needed.
			continue
		}
		lastSec = sec

		for i := range cities {
			t := time.Unix(int64(sec), 0).In(cities[i].loc)
			cities[i].content.Set(t.Format("15:04:05"))
		}

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
