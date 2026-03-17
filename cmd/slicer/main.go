package main

import (
	"fmt"
	"image"
	"image/png"
	"os"

	"github.com/fogleman/gg"

	"mazzy/mazarin/neu"
)

const (
	fontDir     = "/Users/iansmith/louis14/fonts/"
	fontRegular = fontDir + "AtkinsonHyperlegible-Regular.ttf"
	fontBold    = fontDir + "AtkinsonHyperlegible-Bold.ttf"
)

var theme = &neu.Theme{
	Pal:         neu.DefaultPalette(),
	FontRegular: fontRegular,
	FontBold:    fontBold,
	ScaleFactor: 2.0,
}

func px(n float64) float64 { return theme.Px(n) }

// drawButtonGrid draws a 2×2 grid of raised TextButtons centered in the
// rectangle (ax, ay) to (ax+aw, ay+ah).
func drawButtonGrid(canvas *image.RGBA, ax, ay, aw, ah float64) {
	btnW, btnH := px(75), px(34)
	btnR := px(8)
	btnGap := px(12)
	btnFontSize := px(12)
	labels := []string{"One", "Two", "Three", "Four"}

	gridW := 2*btnW + btnGap
	gridH := 2*btnH + btnGap
	gridX := ax + (aw-gridW)/2
	gridY := ay + (ah-gridH)/2

	for i, lbl := range labels {
		col := i % 2
		row := i / 2
		bx := gridX + float64(col)*(btnW+btnGap)
		by := gridY + float64(row)*(btnH+btnGap)
		neu.NeuBox(theme, canvas, neu.Raised, bx, by, bx+btnW, by+btnH, btnR, theme.Pal.Surface,
			neu.TextFace(theme, lbl, btnFontSize, theme.Pal.Icon, false))
	}
}

// drawFreeFloatingWindow draws a FreeFloatingWindow with title, groove, and
// a 2×2 button grid below the groove.
func drawFreeFloatingWindow(canvas *image.RGBA, wx, wy, winW, winH, winR float64, title string) {
	fw := &neu.FreeFloatingWindow{
		Theme:   theme,
		Title:   title,
		Visible: true,
		Content: neu.FaceDrawer(drawButtonGrid),
	}
	fw.Draw(canvas, wx, wy, winW, winH)
}

// drawAppWindow draws an AppWindow with title bar and a 2×2 button grid.
func drawAppWindow(canvas *image.RGBA, focused bool, wx, wy, winW, winH float64, title string, titleBar neu.Drawer) {
	contentDrawer := neu.FaceDrawer(func(canvas *image.RGBA, x, y, w, h float64) {
		neu.Container(theme, canvas, x, y, x+w, y+h, px(10), true, drawButtonGrid)
	})
	app := &neu.AppWindow{
		Theme:    theme,
		Title:    title,
		Focused:  focused,
		TitleBar: titleBar,
		Content:  contentDrawer,
	}
	app.Draw(canvas, wx, wy, winW, winH)
}

func main() {
	winW, winH := px(200), px(170)
	winGap := px(20)
	marginX := px(20)
	marginTop := px(20)
	labelH := px(22)
	marginBot := px(20)

	// Label demo row
	lblW, lblH := px(120), px(34)
	lblGap := px(16)
	lblRowH := labelH + lblH + px(16) // section label + labels + gap

	totalW := int(3*winW + 2*winGap + 2*marginX)
	totalH := int(marginTop + lblRowH + labelH + winH + marginBot)

	canvas := image.NewRGBA(image.Rect(0, 0, totalW, totalH))
	dc := gg.NewContextForRGBA(canvas)
	dc.SetColor(theme.Pal.Surface)
	dc.DrawRectangle(0, 0, float64(totalW), float64(totalH))
	dc.Fill()

	drawSectionLabel := func(wx, wy, colW float64, label string) {
		dc = gg.NewContextForRGBA(canvas)
		_ = dc.LoadFontFace(fontBold, px(8))
		dc.SetColor(theme.Pal.Text)
		dc.DrawStringAnchored(label, wx+colW/2, wy+labelH/2, 0.5, 0.5)
	}

	// ── Row 1: Labels at three depths ──
	ly := marginTop + labelH
	depths := []neu.NeuDepth{neu.Raised, neu.Flush, neu.Inset}
	depthNames := []string{"Raised", "Flush", "Inset"}
	lblTotalW := 3*lblW + 2*lblGap
	lblStartX := (float64(totalW) - lblTotalW) / 2

	drawSectionLabel(0, marginTop, float64(totalW), "Label")
	for i, d := range depths {
		lx := lblStartX + float64(i)*(lblW+lblGap)
		lbl := &neu.NeuLabel{
			Theme:    theme,
			Depth:    d,
			Text:     depthNames[i],
			FontSize: px(11),
			Color:    theme.Pal.Text,
			Bold:     false,
		}
		lbl.Draw(canvas, lx, ly, lblW, lblH)
	}

	// ── Row 2: Windows ──
	winRowTop := marginTop + lblRowH
	wy := winRowTop + labelH

	// AppWindow (resting)
	wx0 := marginX
	drawSectionLabel(wx0, winRowTop, winW, "AppWindow (resting)")
	drawAppWindow(canvas, false, wx0, wy, winW, winH, "My Application", nil)

	// AppWindow (focused, striped)
	wx1 := marginX + winW + winGap
	drawSectionLabel(wx1, winRowTop, winW, "AppWindow (focused)")
	tbR := px(8)
	drawAppWindow(canvas, true, wx1, wy, winW, winH, "My Application",
		neu.StripedTitleFace(theme, "My Application", px(10), tbR))

	// FreeFloatingWindow
	wx2 := marginX + 2*(winW+winGap)
	drawSectionLabel(wx2, winRowTop, winW, "FreeFloatingWindow")
	drawFreeFloatingWindow(canvas, wx2, wy, winW, winH, px(14), "Extra Window")

	outPath := "cmd/slicer/go_pressed_states.png"
	f, err := os.Create(outPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer f.Close()
	if err := png.Encode(f, canvas); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Saved: %s  (%d×%d)\n", outPath, totalW, totalH)
}
