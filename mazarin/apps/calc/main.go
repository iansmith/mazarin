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

//go:linkname nanotime runtime.nanotime
func nanotime() int64

// Window and layout constants.
const (
	// Margins inside the app area.
	marginX = 20

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
	// Superscripts use Unicode superscript letters where possible.
	// U+221A = √, U+00B2 = ², U+02E3 is rare — use "^x" fallback or
	// U+02E3 replaced with common superscript notation.
	keyGrid[0] = [btnCols]*hp15cKey{
		{label: "\u221Ax", fLabel: "A", gLabel: "x\u00B2"},       // √x, x²
		{label: "e^x", fLabel: "B", gLabel: "LN"},                // e^x
		{label: "10^x", fLabel: "C", gLabel: "LOG"},              // 10^x
		{label: "y^x", fLabel: "D", gLabel: "%"},                 // y^x
		{label: "1/x", fLabel: "E", gLabel: "d%"},
		{label: "CHS", fLabel: "MATRIX", gLabel: "ABS"},
		{label: "7", fLabel: "FIX", gLabel: "DEG"},
		{label: "8", fLabel: "SCI", gLabel: "RAD"},
		{label: "9", fLabel: "ENG", gLabel: "GRD"},
		{label: "\u00F7", fLabel: "SOLVE", gLabel: "x<=y"},       // ÷
	}
	// Row 1
	keyGrid[1] = [btnCols]*hp15cKey{
		{label: "SST", fLabel: "LBL", gLabel: "BST"},
		{label: "GTO", fLabel: "HYP", gLabel: "HYP-1"},
		{label: "SIN", fLabel: "DIM", gLabel: "SIN-1"},
		{label: "COS", fLabel: "(i)", gLabel: "COS-1"},
		{label: "TAN", fLabel: "I", gLabel: "TAN-1"},
		{label: "EEX", fLabel: "RESULT", gLabel: "pi"},
		{label: "4", fLabel: "x<>", gLabel: "SF"},
		{label: "5", fLabel: "DSE", gLabel: "CF"},
		{label: "6", fLabel: "ISG", gLabel: "?"},
		{label: "\u00D7", fLabel: "Sxy", gLabel: "x=0"},           // ×
	}
	// Row 2 — ENTER is in its own column (enterCol), not in the grid.
	keyGrid[2] = [btnCols]*hp15cKey{
		{label: "R/S", fLabel: "PSE", gLabel: "P/R"},
		{label: "GSB", fLabel: "SUM", gLabel: "RTN"},
		{label: "Rv", fLabel: "PRGM", gLabel: "R^"},              // R↓ → Rv, R↑ → R^
		{label: "x<>y", fLabel: "REG", gLabel: "RND"},            // x↔y → x<>y
		{label: "<-", fLabel: "PREFIX", gLabel: "CLx"},            // ← → <-
		nil, // col 5 spacers handle this row
		{label: "1", fLabel: "->R", gLabel: "->P"},               // →R, →P
		{label: "2", fLabel: "->H.MS", gLabel: "->H"},            // →H.MS, →H
		{label: "3", fLabel: "->RAD", gLabel: "->DEG"},           // →RAD, →DEG
		{label: "-", fLabel: "Re<>Im", gLabel: "TEST"},
	}
	// Row 3 — col 5 spacers handle this row too.
	keyGrid[3] = [btnCols]*hp15cKey{
		{label: "ON", fLabel: "", gLabel: ""},
		{label: "f", fLabel: "", gLabel: ""},
		{label: "g", fLabel: "", gLabel: ""},
		{label: "STO", fLabel: "FRAC", gLabel: "INT"},
		{label: "RCL", fLabel: "USER", gLabel: "MEM"},
		nil, // col 5 spacers handle this row
		{label: "0", fLabel: "x!", gLabel: "x"},
		{label: "\u00B7", fLabel: "y,r", gLabel: "s"},            // ·
		{label: "E+", fLabel: "L.R.", gLabel: "E-"},
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
	colDisplay = swapRB(25, 35, 20, 255)
	colDispTxt = swapRB(140, 220, 110, 255)
	colBtnFace = pal.SurfaceTint()
	colBtnText = pal.Text()
	colFKey = swapRB(180, 40, 40, 255)
	colGKey = swapRB(70, 120, 190, 255)
	colFLabel = swapRB(180, 40, 40, 255)
	colGLabel = swapRB(80, 130, 200, 255)
	colEnter = pal.SurfaceTint()
	colWhite = swapRB(255, 255, 255, 255)
}

// --- Font IDs ---
var (
	fontBtn     int32
	fontSmall   int32
	fontShift   int32
)

func initFonts(dc mancini.DrawContext) {
	m, err := dc.OpenFont(mfont.DefaultSans, 0, 16)
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
	btnGrid    [btnRows][btnCols]*HP15CButton
	calcShift  ShiftState // shared shift state, synced from engine before draw
	mainCol    *std.ColumnPercentage
	hexDisp    *std.HexDisplay                 // 14-segment LED display
	app        *std.AppWindow                  // top-level AppWindow interactor
	appLH      *mancini.LayoutAttributes       // AppWindow layout — source of truth for dimensions
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
	// Main column with percentage-based layout.
	// [10% top spacer, 15% display, 75% button grid]
	mainCol = std.NewColumnPercentage("main_col", "AppWindow", pal, 0, 0,
		[]float64{5, 18, 77})

	// 30px spacer above display.
	_ = std.NewSpacer("top_spacer", "main_col", int64(660-2*marginX), 30)

	// 14-segment LED display — width equals btn_row's width (sibling constraint).
	// btn_row is created after this, but the constraint resolves lazily on first Draw.
	hexDisp = std.NewHexDisplay("display", "main_col", "btn_row",
		22, 30, colDispTxt, colDisplay)
	hexDisp.Format = std.FormatDecimal

	// Button row.
	btnRow := std.NewRow("btn_row", "main_col", pal, 0, mancini.AxisMinimum, 6)
	btnRow.SetSpacing(float64(btnGapX))

	for col := 0; col < btnCols; col++ {
		colName := fmt.Sprintf("btn_col_%d", col)
		column := std.NewColumn(colName, "btn_row", pal, 0, mancini.AxisMiddle, 6, 0, false)
		column.SetSpacing(float64(btnGapY))

		for row := 0; row < btnRows; row++ {
			k := keyGrid[row][col]
			if k == nil {
				if col == 5 {
					// Fill rows 2-3 in CHS/EEX column with spacers.
					std.NewSpacer(fmt.Sprintf("spacer_%d_%d", row, col),
						colName, int64(btnW), int64(btnH))
				}
				continue
			}
			name := fmt.Sprintf("btn_%d_%d", row, col)
			btnGrid[row][col] = NewHP15CButton(
				name, colName, theme,
				int64(btnW), int64(btnH), k, &calcShift,
				fontBtn, fontShift,
				fillColorForKey(k),
				normalColorForKey(k),
				colFLabel, colGLabel,
			)
		}

		// After col 5 (CHS/EEX), insert the ENTER column.
		if col == 5 {
			enterCol := std.NewColumn("enter_col", "btn_row", pal, 0, mancini.AxisMiddle, 6, 0, false)
			enterCol.SetSpacing(float64(btnGapY))
			// Two spacers at top (aligned with rows 0-1).
			std.NewSpacer("enter_spacer_0", "enter_col", int64(btnW), int64(btnH))
			std.NewSpacer("enter_spacer_1", "enter_col", int64(btnW), int64(btnH))
			// ENTER button spanning rows 2-3.
			enterKey := &hp15cKey{label: "E N T E R", fLabel: "", gLabel: "LSTx"}
			enterH := int64(btnH*2 + btnGapY)
			btnGrid[2][5] = NewHP15CButton(
				"btn_enter", "enter_col", theme,
				int64(btnW), enterH, enterKey, &calcShift,
				fontBtn, fontShift,
				fillColorForKey(enterKey),
				normalColorForKey(enterKey),
				colFLabel, colGLabel,
			)
		}
	}

}

// --- Drawing ---

// syncDisplay updates the 14-segment display and shift state from the engine.
func syncDisplay(engine *RPNEngine) {
	if engine.FShift {
		calcShift = ShiftF
	} else if engine.GShift {
		calcShift = ShiftG
	} else {
		calcShift = ShiftNone
	}
	// Test: display 0xFACEB00C in decimal on the 14-segment display.
	hexDisp.Display(0xFACEB00C)
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
var wmCh = make(chan any, 16)

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
	app = std.NewAppWindow(pal, "HP-15C")
	app.Focused = false
	appLH = app.GetLayout()
	appLH.X.Set(0)
	appLH.Y.Set(0)

	// 6. Key grid.
	initKeyGrid()

	// 7. Position: centered.
	winX := screenW/2 - 660/2
	winY := screenH/2 - 300/2

	// 8. Announce to rachel.
	_ = appLH.Bounds.Get()
	readyAttr := attr.ValueBool(wm.ReadyURI(attr.SID()), true)
	_ = readyAttr
	sys.UartWriteString("[calc] Ready=true\n")

	announceToWM(int32(winX), int32(winY), 660, 210)

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
	bsLen := totalStride * totalH
	bsSlice := unsafe.Slice((*byte)(unsafe.Pointer(uintptr(bsr.BackingStoreAddr))), bsLen)

	// Track initial backing store in the stack.
	app.HandleBackingStoreReady(uintptr(bsr.BackingStoreAddr), bsLen)
	app.SetSize(int64(bsr.AppWidth), int64(bsr.AppHeight))

	// Set AppWindow dimensions from rachel's actual allocation.
	appLH.Width.Set(int64(bsr.AppWidth))
	appLH.Height.Set(int64(bsr.AppHeight))

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
	dc.DrawRectangle(0, 0, float64(bsr.AppWidth), float64(bsr.AppHeight))
	dc.Clip()

	sys.UartWriteString(fmt.Sprintf("[calc] backing store ready: total=%dx%d inset=(%d,%d) app=%dx%d\n",
		totalW, totalH, bsr.LeftInset, bsr.TopInset, bsr.AppWidth, bsr.AppHeight))

	// 11. Open fonts + create button interactors.
	initFonts(dc)
	createLayout(pal, theme)

	// 12. Initial draw.
	engine := NewRPNEngine()
	syncDisplay(engine)
	t0 := nanotime()
	app.SetDC(dc)
	app.Draw(app, 0, 0, appLH.Width.Get(), appLH.Height.Get())
	dt := nanotime() - t0
	sys.UartWriteString(fmt.Sprintf("[calc] initial draw: %dms\n", dt/1_000_000))
	sendBlit()
	sys.UartWriteString("[calc] initial draw complete\n")

	// 13. Event loop.
	for {
		msg := <-wmCh
		switch m := msg.(type) {
		case wm.KeyPress:
			handleKeyPress(engine, m)
		case wm.MousePress:
			// Rachel converts screen→app-local coords before sending.
			lx := int(m.X)
			ly := int(m.Y)
			if row, col, ok := hitTest(lx, ly); ok {
				dispatchButton(engine, row, col)
			}
		case wm.YouHaveFocus, wm.KeyboardFocusGained:
			app.Focused = true
		case wm.YouLostFocus, wm.KeyboardFocusLost:
			app.Focused = false
		case wm.BackingStoreReady:
			// New backing store from rachel (resize start/end).
			newTotalStride := int(m.TotalStride)
			newTotalH := int(m.TotalHeight)
			newBSLen := newTotalStride * newTotalH
			app.HandleBackingStoreReady(uintptr(m.BackingStoreAddr), newBSLen)
			app.SetSize(int64(m.AppWidth), int64(m.AppHeight))
			bsSlice = unsafe.Slice((*byte)(unsafe.Pointer(uintptr(m.BackingStoreAddr))), newBSLen)
			bsImg = &image.RGBA{
				Pix:    bsSlice,
				Stride: newTotalStride,
				Rect:   image.Rect(0, 0, int(m.TotalWidth), newTotalH),
			}
			dc = mancini.NewDrawContextForImage(bsImg, provider)
			initFonts(dc) // re-register fonts with new DC
			dc.Push()
			dc.Translate(float64(m.LeftInset), float64(m.TopInset))
			dc.DrawRectangle(0, 0, float64(m.AppWidth), float64(m.AppHeight))
			dc.Clip()
			appLH.Width.Set(int64(m.AppWidth))
			appLH.Height.Set(int64(m.AppHeight))
			sys.UartWriteString(fmt.Sprintf("[calc] new BS: app=%dx%d total=%dx%d\n",
				m.AppWidth, m.AppHeight, m.TotalWidth, m.TotalHeight))
		case wm.WindowResized:
			// Drain any queued resize messages — only the latest matters.
			// Non-resize messages (focus, keys) are processed inline.
			// If we find a BackingStoreReady, it means the drag ended and
			// supersedes all preceding resizes — break out and let the
			// next loop iteration handle it normally.
			// CRITICAL: rachel must send BackingStoreReady AFTER the drag
			// ends (after clearing drag agent state), never interleaved
			// with WindowResized messages. See dragEndResize() in rachel.
			drained := 0
			gotBSR := false
		drainLoop:
			for {
				select {
				case next := <-wmCh:
					switch n := next.(type) {
					case wm.WindowResized:
						m = n
						drained++
					case wm.BackingStoreReady:
						// Drag ended — push back and let the main
						// loop handle it on the next iteration.
						// Safe: we just drained, so channel has room.
						wmCh <- next
						gotBSR = true
						break drainLoop
					case wm.YouHaveFocus, wm.KeyboardFocusGained:
						app.Focused = true
					case wm.YouLostFocus, wm.KeyboardFocusLost:
						app.Focused = false
					default:
						// Drop other messages during resize drain
						// (keys, mouse events mid-drag are stale).
					}
				default:
					break drainLoop
				}
			}
			if drained > 0 {
				sys.UartWriteString(fmt.Sprintf("[calc] resize coalesced: skipped %d stale\n", drained))
			}
			if gotBSR {
				// BackingStoreReady is on the channel — skip drawing
				// at this intermediate size, the final size is next.
				continue
			}
			// Same buffer, new dimensions only.
			newTotalW := int(m.TotalWidth)
			newTotalH := int(m.TotalHeight)
			newTotalStride := int(m.TotalStride)
			newAppW := int(m.AppWidth)
			newAppH := int(m.AppHeight)
			app.SetSize(int64(newAppW), int64(newAppH))
			bsImg = &image.RGBA{
				Pix:    bsSlice,
				Stride: newTotalStride,
				Rect:   image.Rect(0, 0, newTotalW, newTotalH),
			}
			dc = mancini.NewDrawContextForImage(bsImg, provider)
			initFonts(dc) // re-register fonts with new DC
			dc.Push()
			dc.Translate(float64(m.LeftInset), float64(m.TopInset))
			dc.DrawRectangle(0, 0, float64(newAppW), float64(newAppH))
			dc.Clip()
			appLH.Width.Set(int64(newAppW))
			appLH.Height.Set(int64(newAppH))
			sys.UartWriteString(fmt.Sprintf("[calc] resized: app=%dx%d total=%dx%d\n",
				newAppW, newAppH, newTotalW, newTotalH))
		default:
			continue
		}
		syncDisplay(engine)
		t0 := nanotime()
		app.SetDC(dc)
		app.Draw(app, 0, 0, appLH.Width.Get(), appLH.Height.Get())
		dt := nanotime() - t0
		sys.UartWriteString(fmt.Sprintf("[calc] draw: %dms\n", dt/1_000_000))
		sendBlit()
	}
}
