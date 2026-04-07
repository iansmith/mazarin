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
	"mazzy/mazarin/mancini/impl"
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
	colFKeyDim color.NRGBA // muted f key when off
	colGKey    color.NRGBA
	colGKeyDim color.NRGBA // muted g key when off
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
	colFKeyDim = swapRB(140, 80, 80, 255)
	colGKey = swapRB(70, 120, 190, 255)
	colGKeyDim = swapRB(100, 120, 150, 255)
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
	engine     *RPNEngine                      // RPN calculator engine
	funcGrid   [btnRows][btnCols]*HP15CFunctionButton
	fBtn       *HP15CShiftButton // f shift button
	gBtn       *HP15CShiftButton // g shift button
	enterBtn   *HP15CFunctionButton
	mainCol    *std.ColumnPercentage
	hexDisp    *std.HexDisplay                 // 14-segment LED display
	throb      *Throbber                       // pulsing orange indicator
	radialMenu *std.RadialNOfMChooser          // radial chooser (created on throbber click)
	calcTheme  mancini.Theme                   // saved theme for radial menu creation
	app        *std.AppWindow                  // top-level AppWindow interactor
	appLH      *mancini.LayoutAttributes       // AppWindow layout — source of truth for dimensions
)

func createLayout(pal mancini.Palette, theme mancini.Theme) {
	// Main column with percentage-based layout.
	// [5% top spacer, 18% display, 72% button grid, 5% bottom spacer]
	mainCol = std.NewColumnPercentage("main_col", "AppWindow", pal, 0, 0,
		[]float64{5, 18, 72, 5})

	// 30px spacer above display.
	_ = std.NewSpacer("top_spacer", "main_col", int64(660-2*marginX), 30)

	// 14-segment LED display — width equals btn_row's width (sibling constraint).
	// btn_row is created after this, but the constraint resolves lazily on first Draw.
	hexDisp = std.NewHexDisplay("display", "main_col", "btn_row",
		22, 30, colDispTxt, colDisplay)
	hexDisp.Format = std.FormatDecimal
	hexDisp.HPadding = 6 // match btn_row's HPadding

	// Button row.
	btnRow := std.NewRow("btn_row", "main_col", pal, 0, mancini.AxisMinimum, 6)
	btnRow.SetSpacing(float64(btnGapX))

	// Phase 1: Create all columns (determines column ordering in Row).
	colNames := make([]string, btnCols)
	for col := 0; col < btnCols; col++ {
		colNames[col] = fmt.Sprintf("btn_col_%d", col)
		column := std.NewColumn(colNames[col], "btn_row", pal, 0, mancini.AxisMiddle, 6, 0, false)
		column.SetSpacing(float64(btnGapY))
	}

	// Phase 2: Create buttons row-by-row so children are registered in
	// top-to-bottom order within each column. f and g shift buttons are
	// created at their row 3 positions. Function buttons created before
	// f/g get nil shift refs — we wire them up in phase 3.
	for row := 0; row < btnRows; row++ {
		for col := 0; col < btnCols; col++ {
			k := keyGrid[row][col]
			colName := colNames[col]

			if k == nil {
				// Col 5 rows 2-3: ENTER occupies this space.
				// Row 2: create ENTER (spans rows 2-3).
				// Row 3: no child — ENTER already covers it.
				if col == 5 && row == 2 {
					enterH := int64(btnH*2 + btnGapY)
					enterBtn = NewHP15CFunctionButton(
						"btn_enter", colName, theme,
						int64(btnW), enterH,
						"E N T E R", "", "LSTx",
						fontBtn, fontShift,
						colEnter, colBtnText, colFLabel, colGLabel,
						nil, nil, // shift refs wired in phase 3
						lookupHandler("E N T E R"),
					)
				}
				continue
			}

			if k.label == "f" {
				fBtn = NewHP15CShiftButton("btn_f", colName, theme,
					int64(btnW), int64(btnH), "f", fontBtn,
					colFKey, colFKeyDim, colWhite)
				continue
			}
			if k.label == "g" {
				gBtn = NewHP15CShiftButton("btn_g", colName, theme,
					int64(btnW), int64(btnH), "g", fontBtn,
					colGKey, colGKeyDim, colWhite)
				continue
			}

			name := fmt.Sprintf("btn_%d_%d", row, col)
			fillColor := colBtnFace

			funcGrid[row][col] = NewHP15CFunctionButton(
				name, colName, theme,
				int64(btnW), int64(btnH),
				k.label, k.fLabel, k.gLabel,
				fontBtn, fontShift,
				fillColor, colBtnText, colFLabel, colGLabel,
				nil, nil, // shift refs wired in phase 3
				lookupHandler(k.label),
			)
		}
	}

	// Phase 3: Wire up shift button mutual exclusion and set shift refs
	// on all function buttons now that fBtn/gBtn exist.
	fBtn.SetOther(gBtn)
	gBtn.SetOther(fBtn)
	for row := 0; row < btnRows; row++ {
		for col := 0; col < btnCols; col++ {
			if funcGrid[row][col] != nil {
				funcGrid[row][col].fBtn = fBtn
				funcGrid[row][col].gBtn = gBtn
			}
		}
	}
	if enterBtn != nil {
		enterBtn.fBtn = fBtn
		enterBtn.gBtn = gBtn
	}

	// Bottom row (4th child of main_col): spacer pushes throbber to right.
	_ = std.NewRowPercentage("bottom_row", "main_col", pal, 0, 0,
		[]float64{95, 5})
	_ = std.NewSpacer("bottom_spacer", "bottom_row", 1, 10)
	throb = NewThrobber("throbber", "bottom_row", 10)
}

// --- Drawing ---

// syncDisplay updates the 14-segment display from the engine.
func syncDisplay() {
	// Test: display 0xFACEB00C in decimal on the 14-segment display.
	hexDisp.Display(0xFACEB00C)
}

// --- Radial Menu ---

// toggleRadialMenu creates or toggles visibility of the radial chooser.
func toggleRadialMenu() {
	if radialMenu != nil {
		// Already created — toggle visibility.
		lh := radialMenu.GetLayout()
		if lh != nil && lh.Visible != nil {
			vis := lh.Visible.Get()
			lh.Visible.Set(!vis)
		}
		return
	}

	// First time: create the radial menu.
	throbLH := throb.GetLayout()
	cx := float64(throbLH.X.Get()) + float64(throbLH.Width.Get())/2
	cy := float64(throbLH.Y.Get()) + float64(throbLH.Height.Get())/2

	// Font config for the face labels.
	theme := calcTheme
	fc := &mancini.FontConfig{
		FontRegular:  mfont.DefaultSans,
		FontBold:     mfont.DefaultSans,
		ShapedFontID: fontSmall,
	}

	// Create three faces: "Integer", "Hex", "Binary".
	labels := []string{"Integer", "Hex", "Binary"}
	faces := make([]mancini.LatinTextFace, len(labels))
	for i, label := range labels {
		f := impl.NewLatinTextFace(fc, false, 11, mancini.TextAlignmentParams{})
		f.SetText(label)
		faces[i] = f
	}

	selected := make([]bool, len(labels))
	selected[0] = true // Integer selected by default

	radialMenu = std.NewRadialNOfMChooserNamed(
		"radial_menu", "AppWindow", theme,
		cx, cy, 20, 40, 225, 315,
		faces, selected,
	)
	sys.UartWriteString("[calc] radial menu created\n")
}

// --- Input Handling ---

// pressResult describes what hitTestAndPress found.
type pressResult int

const (
	pressNone    pressResult = iota // no button hit
	pressFunc                        // function button pressed
	pressShiftF                      // f shift button pressed
	pressShiftG                      // g shift button pressed
	pressThrobber                    // throbber clicked — toggle radial menu
)

func hitTestAndPress(lx, ly int) pressResult {
	// Check f button.
	if fBtn != nil {
		lh := fBtn.GetLayout()
		bx, by := int(lh.X.Get()), int(lh.Y.Get())
		bw, bh := int(lh.Width.Get()), int(lh.Height.Get())
		if lx >= bx && lx < bx+bw && ly >= by && ly < by+bh {
			fBtn.Press()
			return pressShiftF
		}
	}
	// Check g button.
	if gBtn != nil {
		lh := gBtn.GetLayout()
		bx, by := int(lh.X.Get()), int(lh.Y.Get())
		bw, bh := int(lh.Width.Get()), int(lh.Height.Get())
		if lx >= bx && lx < bx+bw && ly >= by && ly < by+bh {
			gBtn.Press()
			return pressShiftG
		}
	}
	// Check ENTER button.
	if enterBtn != nil {
		lh := enterBtn.GetLayout()
		bx, by := int(lh.X.Get()), int(lh.Y.Get())
		bw, bh := int(lh.Width.Get()), int(lh.Height.Get())
		if lx >= bx && lx < bx+bw && ly >= by && ly < by+bh {
			enterBtn.Press()
			return pressFunc
		}
	}
	// Check throbber (enlarged hit area for small target).
	if throb != nil {
		lh := throb.GetLayout()
		bx, by := int(lh.X.Get()), int(lh.Y.Get())
		bw, bh := int(lh.Width.Get()), int(lh.Height.Get())
		// Expand hit area by 10px on each side for easier clicking.
		pad := 10
		if lx >= bx-pad && lx < bx+bw+pad && ly >= by-pad && ly < by+bh+pad {
			sys.UartWriteString(fmt.Sprintf("[calc] throbber hit at (%d,%d) bounds=(%d,%d,%d,%d)\n",
				lx, ly, bx, by, bw, bh))
			toggleRadialMenu()
			return pressThrobber
		}
	}
	// Check function grid.
	for row := 0; row < btnRows; row++ {
		for col := 0; col < btnCols; col++ {
			b := funcGrid[row][col]
			if b == nil {
				continue
			}
			lh := b.GetLayout()
			bx, by := int(lh.X.Get()), int(lh.Y.Get())
			bw, bh := int(lh.Width.Get()), int(lh.Height.Get())
			if lx >= bx && lx < bx+bw && ly >= by && ly < by+bh {
				b.Press()
				return pressFunc
			}
		}
	}
	return pressNone
}

func handleKeyPress(kp wm.KeyPress) {
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
		if fBtn != nil {
			fBtn.Press()
		}
		return
	case 'g':
		if gBtn != nil {
			gBtn.Press()
		}
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
	calcTheme = theme

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
	engine = NewRPNEngine()
	syncDisplay()
	t0 := nanotime()
	app.SetDC(dc)
	app.Draw(app, 0, 0, appLH.Width.Get(), appLH.Height.Get())
	dt := nanotime() - t0
	sys.UartWriteString(fmt.Sprintf("[calc] initial draw: %dms\n", dt/1_000_000))
	sendBlit()
	sys.UartWriteString("[calc] initial draw complete\n")

	// 13. 10Hz timer for throbber animation.
	nanosProg := mancini.BindStrings(mancini.ProgIdentityI64,
		"_source_", "attr:///kernel/int64/time/utc_nanos")
	timeNanos := attr.ConstraintI64(attr.ShepherdURI("int64", "time_nanos"), nanosProg)
	timeNanos.SetEager(true)
	_ = timeNanos.Get()
	dirtyCh := attr.OnDirty()

	// 14. Event loop.
	for {
		var msg any
		select {
		case msg = <-wmCh:
		case <-dirtyCh:
			if throb != nil && throb.Tick() {
				syncDisplay()
				app.SetDC(dc)
				app.Draw(app, 0, 0, appLH.Width.Get(), appLH.Height.Get())
				sendBlit()
			}
			continue
		}
		switch m := msg.(type) {
		case wm.KeyPress:
			handleKeyPress(m)
		case wm.MousePress:
			// Rachel converts screen→app-local coords before sending.
			lx := int(m.X)
			ly := int(m.Y)
			hitTestAndPress(lx, ly)
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
		syncDisplay()
		t0 := nanotime()
		app.SetDC(dc)
		app.Draw(app, 0, 0, appLH.Width.Get(), appLH.Height.Get())
		dt := nanotime() - t0
		sys.UartWriteString(fmt.Sprintf("[calc] draw: %dms\n", dt/1_000_000))
		sendBlit()
	}
}
