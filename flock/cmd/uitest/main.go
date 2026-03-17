package main

import (
	_ "embed"
	"fmt"
	"image/color"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"

	"mazzy/mazarin/attr"
	"mazzy/mazarin/interactor"
	"mazzy/mazarin/neu"
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

func main() {
	sys.UartWriteString("[uitest] main() entered\n")

	// 1. Initialize constraint system (needed for WaitDirty / time tracking).
	attr.Init()
	interactor.Init("uitest")
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

	theme := &neu.Theme{
		Pal:         neu.DefaultPalette(),
		FontLoader:  fontLoader,
		ScaleFactor: 1.0,
		SwapRB:      true,
	}

	// 3. Create a change-gated string handle for the clock text.
	// Kernel change-gates string writes: if the formatted string is
	// identical to the current value, no dirty propagation occurs.
	contentHandle := attr.ValueStr("attr:///priest/uitest/str/clock/content", "00:00:00")
	contentHandle.SetEager(true)

	// 4. Build the Drawer tree: AppWindow → NeuLabel (clock).
	winW, winH := 400.0, 200.0
	winX := float64(regionX) + (float64(regionW)-winW)/2
	winY := float64(regionY) + (float64(regionH)-winH)/2

	clockLabel := &neu.NeuLabel{
		Theme:    theme,
		Depth:    neu.Flush,
		TextFunc: func() string { return contentHandle.Get() },
		FontSize: 24,
		Color:    color.NRGBA{0, 0, 0, 255},
		Bold:     false,
	}

	app := &neu.AppWindow{
		Theme:    theme,
		Title:    "Clock",
		Focused:  true,
		TitleBar: neu.StripedTitleFace(theme, "Clock", theme.Px(10), theme.Px(8)),
		Content:  clockLabel,
	}

	// 5. Get framebuffer via DrawContext.
	dc := interactor.NewDrawContext(fontData, regionX, regionY, regionW, regionH)
	fbImage := dc.Image()
	sys.UartWriteString("[uitest] draw context created\n")

	// 6. Initial draw.
	app.Draw(fbImage, winX, winY, winW, winH)
	dc.FlushRegion()
	sys.UartWriteString("[uitest] initial draw done, entering loop\n")

	// 7. Time tracking via constraint system.
	timeProg := interactor.BindStrings(interactor.ProgIdentityI64,
		"attr:///kernel/int64/time/utc_seconds")
	timeSec := attr.ConstraintI64("attr:///priest/uitest/int64/time_sec", timeProg)
	timeSec.SetEager(true)

	// 8. Main loop: wake on dirty, update content handle (change-gated), redraw.
	for {
		attr.WaitDirty()

		sec := timeSec.Get()
		h, m, s := (sec/3600)%24, (sec/60)%60, sec%60
		contentHandle.Set(fmt.Sprintf("%02d:%02d:%02d", h, m, s))

		app.Draw(fbImage, winX, winY, winW, winH)
		dc.Flush(int32(winX), int32(winY), int32(winX+winW), int32(winY+winH))
	}
}
