// main.go — HP 15C RPN calculator for Mazzy.
package main

import (
	"fmt"
	"image"
	"image/color"
	"os"
	"unsafe"

	"mazzy/mazarin/attr"
	"mazzy/mazarin/fontcache"
	"mazzy/mazarin/mancini"
	"mazzy/mazarin/mancini/std"
	mctheme "mazzy/mazarin/mancini/theme"
	"mazzy/mazarin/sys"
	"mazzy/mazarin/uring"
	mfont "mazzy/shared/font"
	"mazzy/shared/ipc"
	"mazzy/shared/wm"

	"golang.org/x/image/font"
)

// Window and layout constants.
const (
	winW = 660
	winH = 300

	// Margins inside the app area.
	marginX = 20

	// Display LCD area.
	dispH = 44
	dispW = winW - 2*marginX

	// Button grid: 10 cols x 4 rows.
	btnCols = 10
	btnRows = 4
	btnW    = 56
	btnH    = 32
	btnGapX = 6
	btnGapY = 4
)

// hp15cKey describes one calculator button.
type hp15cKey struct {
	label   string
	fLabel  string
	gLabel  string
	rowSpan int // 2 for ENTER, 1 for everything else
}

// keyGrid[row][col] — nil means the cell is occupied by a spanning key above.
var keyGrid [btnRows][btnCols]*hp15cKey

func initKeyGrid() {
	// Row 0
	keyGrid[0] = [btnCols]*hp15cKey{
		{label: "\u221Ax", fLabel: "A", gLabel: "x\u00B2"},
		{label: "e\u02E3", fLabel: "B", gLabel: "LN"},
		{label: "10\u02E3", fLabel: "C", gLabel: "LOG"},
		{label: "y\u02E3", fLabel: "D", gLabel: "%"},
		{label: "1/x", fLabel: "E", gLabel: "\u0394%"},
		{label: "CHS", fLabel: "MATRIX", gLabel: "ABS"},
		{label: "7", fLabel: "FIX", gLabel: "DEG"},
		{label: "8", fLabel: "SCI", gLabel: "RAD"},
		{label: "9", fLabel: "ENG", gLabel: "GRD"},
		{label: "\u00F7", fLabel: "SOLVE", gLabel: "x\u2264y"},
	}
	// Row 1
	keyGrid[1] = [btnCols]*hp15cKey{
		{label: "SST", fLabel: "LBL", gLabel: "BST"},
		{label: "GTO", fLabel: "HYP", gLabel: "HYP\u207B\u00B9"},
		{label: "SIN", fLabel: "DIM", gLabel: "SIN\u207B\u00B9"},
		{label: "COS", fLabel: "(i)", gLabel: "COS\u207B\u00B9"},
		{label: "TAN", fLabel: "I", gLabel: "TAN\u207B\u00B9"},
		{label: "EEX", fLabel: "RESULT", gLabel: "\u03C0"},
		{label: "4", fLabel: "x\u21CB", gLabel: "SF"},
		{label: "5", fLabel: "DSE", gLabel: "CF"},
		{label: "6", fLabel: "ISG", gLabel: "?"},
		{label: "\u00D7", fLabel: "\u222Bxy", gLabel: "x=0"},
	}
	// Row 2 — ENTER at col 5 spans into row 3.
	keyGrid[2] = [btnCols]*hp15cKey{
		{label: "R/S", fLabel: "PSE", gLabel: "P/R"},
		{label: "GSB", fLabel: "\u03A3", gLabel: "RTN"},
		{label: "R\u2193", fLabel: "PRGM", gLabel: "R\u2191"},
		{label: "x\u21C6y", fLabel: "REG", gLabel: "RND"},
		{label: "\u2190", fLabel: "PREFIX", gLabel: "CLx"},
		{label: "E N T E R", fLabel: "", gLabel: "LSTx", rowSpan: 2},
		{label: "1", fLabel: "\u2192R", gLabel: "\u2192P"},
		{label: "2", fLabel: "\u2192H.MS", gLabel: "\u2192H"},
		{label: "3", fLabel: "\u2192RAD", gLabel: "\u2192DEG"},
		{label: "\u2212", fLabel: "Re\u21C6Im", gLabel: "TEST"},
	}
	// Row 3 — col 5 is nil (occupied by ENTER above).
	keyGrid[3] = [btnCols]*hp15cKey{
		{label: "ON", fLabel: "", gLabel: ""},
		{label: "f", fLabel: "", gLabel: ""},
		{label: "g", fLabel: "", gLabel: ""},
		{label: "STO", fLabel: "FRAC", gLabel: "INT"},
		{label: "RCL", fLabel: "USER", gLabel: "MEM"},
		nil, // occupied by ENTER
		{label: "0", fLabel: "x!", gLabel: "x\u0304"},
		{label: "\u00B7", fLabel: "y\u0302,r", gLabel: "s"},
		{label: "\u03A3+", fLabel: "L.R.", gLabel: "\u03A3-"},
		{label: "+", fLabel: "Py,x", gLabel: "Cy,x"},
	}

	// Set rowSpan=1 default.
	for r := range keyGrid {
		for c := range keyGrid[r] {
			if keyGrid[r][c] != nil && keyGrid[r][c].rowSpan == 0 {
				keyGrid[r][c].rowSpan = 1
			}
		}
	}
}

// --- Colors ---

func swapRB(r, g, b, a uint8) color.NRGBA {
	return color.NRGBA{R: b, G: g, B: r, A: a}
}

var (
	colBody    color.NRGBA
	colDisplay color.NRGBA
	colDispTxt color.NRGBA
	colBtnFace color.NRGBA
	colBtnText color.NRGBA
	colFKey    color.NRGBA
	colGKey    color.NRGBA
	colFLabel  color.NRGBA
	colGLabel  color.NRGBA
	colEnter   color.NRGBA
	colWhite   color.NRGBA
)

func initColors(pal mancini.Palette) {
	colBody = pal.Surface()
	colDisplay = swapRB(25, 35, 20, 255)
	colDispTxt = swapRB(140, 220, 110, 255)
	colBtnFace = pal.SurfaceTint()
	colBtnText = pal.Text()
	colFKey = swapRB(200, 155, 50, 255)
	colGKey = swapRB(70, 120, 190, 255)
	colFLabel = swapRB(210, 165, 60, 255)
	colGLabel = swapRB(80, 130, 200, 255)
	colEnter = pal.SurfaceTint()
	colWhite = swapRB(255, 255, 255, 255)
}

// --- Font IDs ---
var (
	fontDisplay int32
	fontBtn     int32
	fontSmall   int32
	fontShift   int32
)

func initFonts(dc mancini.DrawContext) {
	m, err := dc.OpenFont(mfont.DefaultMono, 0, 20)
	if err != nil {
		sys.UartWriteString("[calc] OpenFont mono failed: " + err.Error() + "\n")
	}
	fontDisplay = m.FontID

	m, err = dc.OpenFont(mfont.DefaultSans, 0, 16)
	if err != nil {
		sys.UartWriteString("[calc] OpenFont sans failed: " + err.Error() + "\n")
	}
	fontBtn = m.FontID

	m, err = dc.OpenFont(mfont.DefaultSans, 0, 12)
	if err != nil {
		sys.UartWriteString("[calc] OpenFont small failed: " + err.Error() + "\n")
	}
	fontSmall = m.FontID

	m, err = dc.OpenFont(mfont.DefaultSans, 0, 13)
	if err != nil {
		sys.UartWriteString("[calc] OpenFont shift failed: " + err.Error() + "\n")
	}
	fontShift = m.FontID
}

// --- Button Interactors ---

var (
	btnGrid   [btnRows][btnCols]*HP15CButton
	calcShift ShiftState // shared shift state, synced from engine before draw
	mainCol   *std.Column
	dispSpacer *std.Spacer // placeholder for display drawing
)

// fillColorForKey returns the button fill color based on the key label.
func fillColorForKey(k *hp15cKey) color.NRGBA {
	switch k.label {
	case "f":
		return colFKey
	case "g":
		return colGKey
	case "E N T E R":
		return colEnter
	default:
		return colBtnFace
	}
}

// normalColorForKey returns the text color for a key's primary label.
func normalColorForKey(k *hp15cKey) color.NRGBA {
	if k.label == "f" || k.label == "g" {
		return colWhite
	}
	return colBtnText
}

func createLayout(pal mancini.Palette, theme mancini.Theme) {
	// Main column: top spacer → display spacer → button row.
	mainCol = std.NewColumn("main_col", "AppWindow", pal, 0, mancini.AxisMiddle, 1, false)
	mainCol.SetSpacing(10)

	// 30px spacer above display.
	_ = std.NewSpacer("top_spacer", "main_col", int64(dispW), 30)

	// Display placeholder — we draw LCD content over this position.
	dispSpacer = std.NewSpacer("display", "main_col", int64(dispW), int64(dispH))

	// Button row.
	btnRow := std.NewRow("btn_row", "main_col", pal, int64(winW-2*marginX), mancini.AxisMinimum, 1)
	btnRow.SetSpacing(float64(btnGapX))

	for col := 0; col < btnCols; col++ {
		colName := fmt.Sprintf("btn_col_%d", col)
		column := std.NewColumn(colName, "btn_row", pal, 0, mancini.AxisMiddle, 1, false)
		column.SetSpacing(float64(btnGapY))

		for row := 0; row < btnRows; row++ {
			k := keyGrid[row][col]
			if k == nil {
				continue
			}
			name := fmt.Sprintf("btn_%d_%d", row, col)
			h := int64(btnH)
			if k.rowSpan == 2 {
				h = int64(btnH*2 + btnGapY)
			}

			btnGrid[row][col] = NewHP15CButton(
				name, colName, theme,
				int64(btnW), h, k, &calcShift,
				fontBtn, fontShift,
				fillColorForKey(k),
				normalColorForKey(k),
				colFLabel, colGLabel,
			)
		}
	}
}

// --- Drawing ---

// drawDisplay renders the LCD display area (background + text) at the
// position determined by the display spacer's layout.
func drawDisplay(dc mancini.DrawContext, engine *RPNEngine) {
	lh := dispSpacer.GetLayout()
	dx := float64(lh.X.Get())
	dy := float64(lh.Y.Get())
	dw := float64(dispW)
	dh := float64(dispH)

	dc.SetColor(colDisplay)
	dc.DrawRoundedRectangle(dx, dy, dw, dh, 4)
	dc.Fill()

	// Shift indicator.
	if engine.FShift {
		dc.SetColor(colFLabel)
		dc.DrawStringAnchored("f", fontShift, dx+8, dy+dh/2, 0, 0.5)
	} else if engine.GShift {
		dc.SetColor(colGLabel)
		dc.DrawStringAnchored("g", fontShift, dx+8, dy+dh/2, 0, 0.5)
	}

	// Display value (right-aligned).
	dc.SetColor(colDispTxt)
	dc.DrawStringAnchored(engine.Display(), fontDisplay,
		dx+dw-12, dy+dh/2, 1.0, 0.5)
}

// drawAll renders the entire calculator face.
func drawAll(dc mancini.DrawContext, engine *RPNEngine) {
	// Sync shift state from engine.
	if engine.FShift {
		calcShift = ShiftF
	} else if engine.GShift {
		calcShift = ShiftG
	} else {
		calcShift = ShiftNone
	}

	// Background.
	dc.SetColor(colBody)
	dc.FillRectangle(0, 0, winW, winH)

	// Layout via Column → (spacer, display spacer, button row).
	mainCol.SetDC(dc)
	mainCol.Draw(mainCol, int64(marginX), 0, int64(winW-2*marginX), int64(winH))

	// Display content drawn over the display spacer position.
	drawDisplay(dc, engine)
}

// --- Input Handling ---

func hitTest(lx, ly int) (int, int, bool) {
	for row := 0; row < btnRows; row++ {
		for col := 0; col < btnCols; col++ {
			b := btnGrid[row][col]
			if b == nil {
				continue
			}
			lh := b.GetLayout()
			bx := int(lh.X.Get())
			by := int(lh.Y.Get())
			bw := int(lh.Width.Get())
			bh := int(lh.Height.Get())
			if lx >= bx && lx < bx+bw && ly >= by && ly < by+bh {
				return row, col, true
			}
		}
	}
	return 0, 0, false
}

func dispatchButton(engine *RPNEngine, row, col int) {
	k := keyGrid[row][col]
	if k == nil {
		return
	}

	if k.label == "f" {
		engine.SetFShift()
		return
	}
	if k.label == "g" {
		engine.SetGShift()
		return
	}

	if engine.GShift {
		dispatchGShift(engine, row, col)
		return
	}
	if engine.FShift {
		dispatchFShift(engine, row, col)
		return
	}

	switch k.label {
	case "\u221Ax":
		engine.Sqrt()
	case "e\u02E3":
		engine.Exp()
	case "10\u02E3":
		engine.Pow10()
	case "y\u02E3":
		engine.PowYX()
	case "1/x":
		engine.Reciprocal()
	case "CHS":
		engine.CHS()
	case "7":
		engine.Digit(7)
	case "8":
		engine.Digit(8)
	case "9":
		engine.Digit(9)
	case "\u00F7":
		engine.Div()
	case "SIN":
		engine.Sin()
	case "COS":
		engine.Cos()
	case "TAN":
		engine.Tan()
	case "EEX":
		engine.EEX()
	case "4":
		engine.Digit(4)
	case "5":
		engine.Digit(5)
	case "6":
		engine.Digit(6)
	case "\u00D7":
		engine.Mul()
	case "R\u2193":
		engine.RollDown()
	case "x\u21C6y":
		engine.SwapXY()
	case "\u2190":
		engine.Backspace()
	case "E N T E R":
		engine.Enter()
	case "1":
		engine.Digit(1)
	case "2":
		engine.Digit(2)
	case "3":
		engine.Digit(3)
	case "\u2212":
		engine.Sub()
	case "STO":
		engine.StoreTo(0)
	case "RCL":
		engine.RecallFrom(0)
	case "0":
		engine.Digit(0)
	case "\u00B7":
		engine.Dot()
	case "\u03A3+":
		engine.Add()
	case "+":
		engine.Add()
	case "ON":
		engine.ClearX()
		engine.Y = 0
		engine.Z = 0
		engine.T = 0
		engine.LastX = 0
	}
}

func dispatchGShift(engine *RPNEngine, row, col int) {
	k := keyGrid[row][col]
	if k == nil {
		return
	}
	engine.GShift = false
	engine.FShift = false

	switch k.gLabel {
	case "x\u00B2":
		engine.Square()
	case "LN":
		engine.Ln()
	case "LOG":
		engine.Log()
	case "%":
		engine.Percent()
	case "ABS":
		engine.Abs()
	case "SIN\u207B\u00B9":
		engine.Asin()
	case "COS\u207B\u00B9":
		engine.Acos()
	case "TAN\u207B\u00B9":
		engine.Atan()
	case "\u03C0":
		engine.Pi()
	case "R\u2191":
		engine.RollUp()
	case "CLx":
		engine.ClearX()
	case "LSTx":
		engine.RecallLastX()
	case "x!":
		engine.Factorial()
	}
}

func dispatchFShift(engine *RPNEngine, row, col int) {
	k := keyGrid[row][col]
	if k == nil {
		return
	}
	engine.FShift = false
	engine.GShift = false

	switch k.fLabel {
	case "FIX":
		engine.FixDigits = (engine.FixDigits + 1) % 10
	}
}

func handleKeyPress(engine *RPNEngine, kp wm.KeyPress) {
	ch := kp.Char
	if ch >= '0' && ch <= '9' {
		engine.Digit(int(ch - '0'))
		return
	}
	switch ch {
	case '.':
		engine.Dot()
		return
	case '+':
		engine.Add()
		return
	case '-':
		engine.Sub()
		return
	case '*':
		engine.Mul()
		return
	case '/':
		engine.Div()
		return
	case '^':
		engine.PowYX()
		return
	case 'n':
		engine.CHS()
		return
	case 'f':
		engine.SetFShift()
		return
	case 'g':
		engine.SetGShift()
		return
	case 's':
		engine.Sqrt()
		return
	}

	switch kp.Action {
	case wm.ActionEnter:
		engine.Enter()
	case wm.ActionBackspace:
		engine.Backspace()
	case wm.ActionEscape:
		engine.ClearX()
	}
}

// --- Application Lifecycle ---

var rachelSID int
var wmCh = make(chan any, 4)

func announceToWM(x, y, w, h int32) {
	msg := wm.EncodeAppStart(&wm.AppStart{
		SID:    int32(os.Getpid()),
		X:      x,
		Y:      y,
		Width:  w,
		Height: h,
	})
	if err := uring.Send(rachelSID, &msg); err != nil {
		sys.UartWriteString("[calc] uring.Send AppStart failed: " + err.Error() + "\n")
		return
	}
	sys.UartWriteString(fmt.Sprintf("[calc] sent AppStart to rachel: %dx%d at (%d,%d)\n", w, h, x, y))
}

func sendBlit() {
	if rachelSID < 0 {
		return
	}
	msg := wm.EncodeBlit(&wm.Blit{SID: int32(os.Getpid())})
	_ = uring.Send(rachelSID, &msg)
}

func startUringDispatcher(fc *fontcache.FontCache) {
	d := uring.NewDispatcher()
	d.On(ipc.ProtoShepherdNotify, wm.DecodeShepherdNotify, wmCh)
	d.On(ipc.ProtoFontResponse, wm.DecodeFontResponse, fc.ReplyCh)
	d.Start()
}

func main() {
	sys.UartWriteString("[calc] main() entered\n")

	// 1. Initialize constraint system.
	attr.Init()
	mancini.Init()

	// 2. Wait for required shepherds.
	if err := sys.WaitForShepherdReady("fs", 10); err != nil {
		panic(fmt.Sprintf("[calc] FATAL: fs: %v", err))
	}
	if err := sys.WaitForShepherdReady("rachel", 10); err != nil {
		panic(fmt.Sprintf("[calc] FATAL: rachel: %v", err))
	}
	if err := sys.WaitForShepherdReady("linux", 10); err != nil {
		panic(fmt.Sprintf("[calc] FATAL: linux: %v", err))
	}
	rachelSID = sys.MustGetShepherdByName("rachel")
	fc := fontcache.New(rachelSID)

	startUringDispatcher(fc)

	// 3. Palette, colors, theme.
	pal := mctheme.NewDefaultPaletteSwapRB()
	initColors(pal)

	resolver := func(family string, feature mancini.Feature, size int64) font.Face {
		style := mfont.Regular
		if feature == mancini.Bold {
			style = mfont.Bold
		}
		return fc.OpenFaceByName(family, style, size)
	}
	neu := mctheme.NewDefaultNeumorphicParams()
	theme := mctheme.NewTheme(pal, neu, mfont.DefaultSans, 18, resolver)
	theme.SetStyle(std.NewNeumorphicStyle(neu.Heavy(), neu.Light()))

	// 4. Screen dimensions.
	screenWURI := "attr:///kernel/int64/screen/width"
	screenHURI := "attr:///kernel/int64/screen/height"
	screenWProg := mancini.BindStrings(mancini.ProgIdentityI64,
		"_source_", screenWURI)
	screenWAttr := attr.ConstraintI64(attr.ShepherdURI("int64", "screen_w"), screenWProg)
	screenHProg := mancini.BindStrings(mancini.ProgIdentityI64,
		"_source_", screenHURI)
	screenHAttr := attr.ConstraintI64(attr.ShepherdURI("int64", "screen_h"), screenHProg)
	screenW := int(screenWAttr.Get())
	screenH := int(screenHAttr.Get())
	sys.UartWriteString(fmt.Sprintf("[calc] screen: %dx%d\n", screenW, screenH))

	// 5. AppWindow — required by rachel for Bounds/Title attributes.
	app := std.NewAppWindow(pal, "Calculator", screenWURI, screenHURI)
	app.Focused = false
	appLH := app.GetLayout()
	appLH.X.Set(0)
	appLH.Y.Set(0)

	// 6. Key grid.
	initKeyGrid()

	// 7. Position: centered.
	winX := screenW/2 - winW/2
	winY := screenH/2 - winH/2

	// 8. Announce to rachel.
	_ = appLH.Bounds.Get()
	readyAttr := attr.ValueBool(wm.ReadyURI(attr.SID()), true)
	_ = readyAttr
	sys.UartWriteString("[calc] Ready=true\n")

	announceToWM(int32(winX), int32(winY), int32(winW), int32(winH))

	// 9. Wait for backing store.
	sys.UartWriteString("[calc] waiting for BackingStoreReady...\n")
	var bsr wm.BackingStoreReady
	for {
		raw := <-wmCh
		if b, ok := raw.(wm.BackingStoreReady); ok {
			bsr = b
			break
		}
	}

	// 10. Set up drawing.
	mancini.SetScreenOrigin(int64(bsr.AppX), int64(bsr.AppY))

	totalW := int(bsr.TotalWidth)
	totalH := int(bsr.TotalHeight)
	totalStride := int(bsr.TotalStride)
	bsSlice := unsafe.Slice((*byte)(unsafe.Pointer(uintptr(bsr.BackingStoreAddr))),
		totalStride*totalH)

	bsImg := &image.RGBA{
		Pix:    bsSlice,
		Stride: totalStride,
		Rect:   image.Rect(0, 0, totalW, totalH),
	}
	provider := fontcache.NewFontSvcGlyphProvider(fc)
	dc := mancini.NewDrawContextForImage(bsImg, provider)

	leftInset := float64(bsr.LeftInset)
	topInset := float64(bsr.TopInset)
	dc.Push()
	dc.Translate(leftInset, topInset)
	dc.DrawRectangle(0, 0, float64(winW), float64(winH))
	dc.Clip()

	sys.UartWriteString(fmt.Sprintf("[calc] backing store ready: total=%dx%d inset=(%d,%d)\n",
		totalW, totalH, bsr.LeftInset, bsr.TopInset))

	// 11. Open fonts + create button interactors.
	initFonts(dc)
	createLayout(pal, theme)

	// 12. Initial draw.
	engine := NewRPNEngine()
	drawAll(dc, engine)
	sendBlit()
	sys.UartWriteString("[calc] initial draw complete\n")

	// 13. Event loop.
	appX := int(bsr.AppX)
	appY := int(bsr.AppY)

	for {
		msg := <-wmCh
		switch m := msg.(type) {
		case wm.KeyPress:
			handleKeyPress(engine, m)
		case wm.MousePress:
			lx := int(m.X) - appX
			ly := int(m.Y) - appY
			if row, col, ok := hitTest(lx, ly); ok {
				dispatchButton(engine, row, col)
			}
		case wm.YouHaveFocus, wm.KeyboardFocusGained:
			app.Focused = true
		case wm.YouLostFocus, wm.KeyboardFocusLost:
			app.Focused = false
		default:
			continue
		}
		drawAll(dc, engine)
		sendBlit()
	}
}
