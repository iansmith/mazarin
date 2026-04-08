// main.go — HP 15C RPN calculator for Mazzy.
package main

import (
	"fmt"
	"image"
	"image/color"
	"math"
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
	fontRadial  int32
)

func initFonts(dc mancini.DrawContext) {
	m, err := dc.OpenFont(mfont.DefaultSans, 0, 16)
	if err != nil {
		sys.UartWriteString(fmt.Sprintf("[calc] OpenFont sans failed: %s\n", err.Error()))
	}
	fontBtn = m.FontID

	m, err = dc.OpenFont(mfont.DefaultSans, 0, 12)
	if err != nil {
		sys.UartWriteString(fmt.Sprintf("[calc] OpenFont small failed: %s\n", err.Error()))
	}
	fontSmall = m.FontID

	m, err = dc.OpenFont(mfont.DefaultSans, 0, 13)
	if err != nil {
		sys.UartWriteString(fmt.Sprintf("[calc] OpenFont shift failed: %s\n", err.Error()))
	}
	fontShift = m.FontID

	m, err = dc.OpenFont(mfont.DefaultSans, 0, 18)
	if err != nil {
		sys.UartWriteString(fmt.Sprintf("[calc] OpenFont radial failed: %s\n", err.Error()))
	}
	fontRadial = m.FontID
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
	radialMenu *std.RadialNOfMChooser          // radial chooser (created on overlay ready)
	calcTheme  mancini.Theme                   // saved theme for radial menu creation
	app        *std.AppWindow                  // top-level AppWindow interactor
	appLH      *mancini.LayoutAttributes       // AppWindow layout — source of truth for dimensions

	// Shared glyph provider for creating DrawContexts.
	glyphProvider *fontcache.FontSvcGlyphProvider

	// mainDC is the app's primary DrawContext. Used to create child DCs
	// (e.g., for overlays) that share the same text layout engine and
	// already-opened fonts.
	mainDC mancini.DrawContext

	// Overlay state for the radial menu popup.
	overlayActive    bool
	overlayID        int32
	overlayDC        mancini.DrawContext
	overlayImg       *image.RGBA
	overlayW, overlayH int32
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

// --- Overlay / Radial Menu ---

// requestOverlay sends OverlayAllocate to rachel for the radial menu popup.
// If an overlay is already active, sends OverlayRelease to dismiss it.
func requestOverlay() {
	if overlayActive {
		// Already showing — dismiss.
		msg := wm.EncodeOverlayRelease(&wm.OverlayRelease{OverlayID: overlayID})
		_ = uring.Send(rachelSID, &msg)
		fmt.Fprintf(os.Stdout, "[calc] sent OverlayRelease id=%d\n", overlayID)
		return
	}

	// Compute screen-space rectangle for the radial menu.
	// Arc center sits at the throbber (bottom-right corner of the calc layout).
	throbLH := throb.GetLayout()
	throbX := throbLH.X.Get()
	throbY := throbLH.Y.Get()
	throbW := throbLH.Width.Get()
	throbH := throbLH.Height.Get()
	// Bottom-right corner of the throbber in app-local coords.
	appCX := float64(throbX) + float64(throbW)
	appCY := float64(throbY) + float64(throbH)

	// Convert app-local to screen coords.
	screenOX, screenOY := mancini.ScreenOrigin()
	screenCX := float64(screenOX) + appCX
	screenCY := float64(screenOY) + appCY

	sys.UartWriteString(fmt.Sprintf("[calc:overlay] throbber layout: X=%d Y=%d W=%d H=%d screenCenter=(%.0f,%.0f)\n",
		throbX, throbY, throbW, throbH, screenCX, screenCY))

	// Radial menu arc: fans lower-right from the bottom-right corner.
	// 3.75° to 86.25°: three 27.5° segments extending the corner outward.
	outerR := 160.0
	pad := 5.0

	// Arc extends rightward and downward from center. The center sits at
	// the top-left of the overlay buffer.
	x1 := int32(screenCX - pad)
	y1 := int32(screenCY - pad)
	x2 := int32(screenCX + outerR + pad)
	y2 := int32(screenCY + outerR + pad)

	fmt.Fprintf(os.Stdout, "[calc] requesting overlay: screen rect (%d,%d)-(%d,%d) center=(%.0f,%.0f)\n",
		x1, y1, x2, y2, screenCX, screenCY)

	msg := wm.EncodeOverlayAllocate(&wm.OverlayAllocate{
		ScreenX1: x1, ScreenY1: y1, ScreenX2: x2, ScreenY2: y2,
	})
	_ = uring.Send(rachelSID, &msg)
}

// handleOverlayReady processes the OverlayReady message from rachel.
// Creates a DrawContext backed by the shared overlay buffer, draws the
// radial menu into it, and sends OverlayBlit to rachel.
func handleOverlayReady(m wm.OverlayReady) {
	overlayActive = true
	overlayID = m.OverlayID
	overlayW = m.Width
	overlayH = m.Height

	// Create image.RGBA backed by the shared overlay buffer.
	stride := int(m.Stride)
	bufLen := stride * int(m.Height)
	ovSlice := unsafe.Slice((*byte)(unsafe.Pointer(uintptr(m.OverlayAddr))), bufLen)
	overlayImg = &image.RGBA{
		Pix:    ovSlice,
		Stride: stride,
		Rect:   image.Rect(0, 0, int(m.Width), int(m.Height)),
	}

	// Create the overlay DC as a child of the main DC so it shares the
	// same text layout engine and already-opened fonts.
	overlayDC = mainDC.NewChildContext(overlayImg)

	// The radial menu center sits at the top-left of the overlay buffer,
	// offset by the padding we added in requestOverlay.
	cx := 5.0 // matches pad in requestOverlay
	cy := 5.0

	// Create faces using the pre-opened fontSmall ID from the main DC.
	// The overlay DC shares the same glyph provider, so the fontID is valid.
	labels := []string{"Integer 123", "Hex 0x7b", "Binary 1111011"}
	faces := make([]mancini.LatinTextFace, len(labels))
	for i, label := range labels {
		f := impl.NewLatinTextFaceWithFontID(fontRadial, mancini.TextAlignmentParams{})
		f.SetText(label)
		faces[i] = f
	}

	selected := make([]bool, len(labels))
	selected[0] = true // Integer selected by default

	// Arc from 3.75° to 86.25°: three 27.5° segments fanning
	// lower-right from the bottom-right corner (0°=right, 90°=down).
	// Inner radius 60 gives a clean gap at the corner; outer 160 for readable text.
	radialMenu = std.NewRadialNOfMChooserNamed(
		"overlay_radial", "", calcTheme,
		cx, cy, 60, 160, 3.75, 86.25,
		faces, selected,
	)

	// Draw the radial menu into the overlay buffer.
	radialMenu.SetDC(overlayDC)

	// Log the radial menu's stored CX/CY and layout dimensions.
	rLH := radialMenu.GetLayout()
	sys.UartWriteString(fmt.Sprintf("[calc:overlay] radial CX=%.1f CY=%.1f innerR=%.1f outerR=%.1f start=%.0f end=%.0f\n",
		radialMenu.CX, radialMenu.CY, radialMenu.InnerR, radialMenu.OuterR,
		radialMenu.StartDeg, radialMenu.EndDeg))
	sys.UartWriteString(fmt.Sprintf("[calc:overlay] radial layout W=%d H=%d\n",
		rLH.Width.Get(), rLH.Height.Get()))

	radialMenu.Draw(radialMenu, 0, 0, int64(m.Width), int64(m.Height))

	fmt.Fprintf(os.Stdout, "[calc] overlay drawn: id=%d %dx%d center=(%.0f,%.0f)\n",
		m.OverlayID, m.Width, m.Height, cx, cy)

	// Tell rachel to composite the overlay onto the framebuffer.
	blit := wm.EncodeOverlayBlit(&wm.OverlayBlit{OverlayID: m.OverlayID})
	_ = uring.Send(rachelSID, &blit)
}

// handleOverlayReleased cleans up overlay state after rachel confirms teardown.
func handleOverlayReleased(m wm.OverlayReleased) {
	fmt.Fprintf(os.Stdout, "[calc] overlay released: id=%d\n", m.OverlayID)
	overlayActive = false
	overlayID = 0
	overlayDC = nil
	overlayImg = nil
	radialMenu = nil
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
			fmt.Fprintf(os.Stdout, "[calc] throbber hit at (%d,%d) bounds=(%d,%d,%d,%d)\n",
				lx, ly, bx, by, bw, bh)
			requestOverlay()
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

// --- Resize Performance Instrumentation ---

// resizePerf accumulates performance data across a single resize operation
// (from first WindowResized to the final BackingStoreReady or first non-resize message).
type resizePerf struct {
	active       bool  // true when we're in a resize operation
	startNanos   int64 // nanotime() at first WindowResized
	draws        int   // number of draw() calls during this resize
	blits        int   // number of sendBlit() calls during this resize
	totalReceived int  // total WindowResized messages received (including coalesced)
	totalDrained  int  // WindowResized messages discarded by coalescing
	totalDroppedBSR int // resize draws skipped because BSR was queued

	// Timing accumulators (nanoseconds).
	drawNanos     int64 // total time in app.Draw()
	drawMin       int64 // min single draw time
	drawMax       int64 // max single draw time
	blitSendNanos int64 // total time in sendBlit() (uring.Send syscall)
	dcCreateNanos int64 // total time creating DrawContext + initFonts
	constraintSetNanos int64 // total time in Width.Set() + Height.Set()
	initFontsNanos int64 // total time in initFonts()

	// Message arrival timing.
	lastMsgNanos  int64 // nanotime() of last WindowResized arrival
	msgGapMin     int64 // min gap between consecutive WindowResized arrivals
	msgGapMax     int64 // max gap between consecutive WindowResized arrivals
	msgGapSum     int64 // sum of gaps (for average)
	msgGapCount   int   // number of gaps measured

	// Pixel count for context.
	lastAppW int
	lastAppH int

	// Constraint system stats (snapshot at start, diff at end).
	constraintEvalsStart int64
	constraintNanosStart int64
}

var rperf resizePerf

func resizePerfStart() {
	rperf = resizePerf{
		active:               true,
		startNanos:           nanotime(),
		lastMsgNanos:         nanotime(),
		drawMin:              math.MaxInt64,
		msgGapMin:            math.MaxInt64,
		constraintEvalsStart: attr.EvalCount.Load(),
		constraintNanosStart: attr.EvalNanos.Load(),
	}
	std.ResetNeuCacheStats()
}

func resizePerfRecordDraw(drawNs, dcNs, fontsNs, blitNs, constraintSetNs int64) {
	if !rperf.active {
		return
	}
	rperf.draws++
	rperf.blits++
	rperf.drawNanos += drawNs
	if drawNs < rperf.drawMin {
		rperf.drawMin = drawNs
	}
	if drawNs > rperf.drawMax {
		rperf.drawMax = drawNs
	}
	rperf.blitSendNanos += blitNs
	rperf.dcCreateNanos += dcNs
	rperf.initFontsNanos += fontsNs
	rperf.constraintSetNanos += constraintSetNs
}

func resizePerfRecordArrival(appW, appH int) {
	if !rperf.active {
		return
	}
	now := nanotime()
	if rperf.msgGapCount > 0 || rperf.totalReceived > 1 {
		gap := now - rperf.lastMsgNanos
		if gap < rperf.msgGapMin {
			rperf.msgGapMin = gap
		}
		if gap > rperf.msgGapMax {
			rperf.msgGapMax = gap
		}
		rperf.msgGapSum += gap
		rperf.msgGapCount++
	}
	rperf.lastMsgNanos = now
	rperf.lastAppW = appW
	rperf.lastAppH = appH
}

func resizePerfRecordCoalesce(drained int, droppedBSR bool) {
	if !rperf.active {
		return
	}
	rperf.totalDrained += drained
	rperf.totalReceived += drained // drained messages were received but not drawn
	if droppedBSR {
		rperf.totalDroppedBSR++
	}
}

func resizePerfEnd() {
	if !rperf.active {
		return
	}
	rperf.active = false
	totalNanos := nanotime() - rperf.startNanos

	// Constraint system deltas.
	evalCount := attr.EvalCount.Load() - rperf.constraintEvalsStart
	evalNanos := attr.EvalNanos.Load() - rperf.constraintNanosStart

	totalMs := totalNanos / 1_000_000
	drawMs := rperf.drawNanos / 1_000_000
	dcMs := rperf.dcCreateNanos / 1_000_000
	fontsMs := rperf.initFontsNanos / 1_000_000
	blitMs := rperf.blitSendNanos / 1_000_000
	csetMs := rperf.constraintSetNanos / 1_000_000
	cevalMs := evalNanos / 1_000_000

	totalMsgs := rperf.totalReceived // includes both drawn and drained
	var coalescePct int64
	if totalMsgs > 0 {
		coalescePct = int64(rperf.totalDrained) * 100 / int64(totalMsgs)
	}

	avgDrawUs := int64(0)
	if rperf.draws > 0 {
		avgDrawUs = rperf.drawNanos / int64(rperf.draws) / 1000
	}
	minDrawUs := rperf.drawMin / 1000
	maxDrawUs := rperf.drawMax / 1000
	if rperf.draws == 0 {
		minDrawUs = 0
	}

	avgEvalUs := int64(0)
	if evalCount > 0 {
		avgEvalUs = evalNanos / evalCount / 1000
	}

	// Message arrival gap stats.
	avgGapUs := int64(0)
	minGapUs := int64(0)
	maxGapUs := int64(0)
	if rperf.msgGapCount > 0 {
		avgGapUs = rperf.msgGapSum / int64(rperf.msgGapCount) / 1000
		minGapUs = rperf.msgGapMin / 1000
		maxGapUs = rperf.msgGapMax / 1000
	}

	fmt.Printf("\n=== RESIZE PERFORMANCE REPORT ===\n")
	fmt.Printf("Total resize time:       %d ms\n", totalMs)
	fmt.Printf("Final size:              %dx%d (%d pixels)\n",
		rperf.lastAppW, rperf.lastAppH, rperf.lastAppW*rperf.lastAppH)
	fmt.Printf("Draws performed:         %d\n", rperf.draws)
	fmt.Printf("  Draw total:            %d ms (avg %d us, min %d us, max %d us)\n",
		drawMs, avgDrawUs, minDrawUs, maxDrawUs)
	fmt.Printf("  DC creation total:     %d ms\n", dcMs)
	fmt.Printf("  initFonts total:       %d ms\n", fontsMs)
	fmt.Printf("  Blit send total:       %d ms (%d blits)\n", blitMs, rperf.blits)
	fmt.Printf("Constraint system:\n")
	fmt.Printf("  Width/Height.Set():    %d ms\n", csetMs)
	fmt.Printf("  Evaluations:           %d (total %d ms, avg %d us/eval)\n",
		evalCount, cevalMs, avgEvalUs)
	drawn := totalMsgs - rperf.totalDrained
	fmt.Printf("Coalescing:\n")
	fmt.Printf("  WindowResized recv'd:  %d\n", totalMsgs)
	fmt.Printf("  Drawn:                 %d\n", drawn)
	fmt.Printf("  Discarded (coalesced): %d (%d%%)\n", rperf.totalDrained, coalescePct)
	fmt.Printf("  Skipped (BSR queued):  %d\n", rperf.totalDroppedBSR)
	if rperf.msgGapCount > 0 {
		fmt.Printf("Message arrival gaps:\n")
		fmt.Printf("  Avg: %d us, Min: %d us, Max: %d us\n", avgGapUs, minGapUs, maxGapUs)
	}
	neuHits, neuMisses := std.NeuCacheStats()
	fmt.Printf("Neu raised cache:\n")
	fmt.Printf("  Hits: %d, Misses: %d\n", neuHits, neuMisses)
	fmt.Printf("=================================\n\n")
}

// --- Message Instrumentation ---

// msgStats tracks message counts by type for periodic reporting.
var msgStats struct {
	total       int64
	keyPress    int64
	mousePress  int64
	focus       int64
	bsReady     int64
	winResized  int64
	overlayRdy  int64
	overlayRel  int64
	overlayIn   int64
	timerTick   int64
	blitSent    int64
	other       int64
	lastDump    int64
}

const msgDumpInterval = 20 // dump every N messages

func msgStatsLog(label string) {
	msgStats.total++
	switch label {
	case "KeyPress":
		msgStats.keyPress++
	case "MousePress":
		msgStats.mousePress++
	case "Focus":
		msgStats.focus++
	case "BackingStoreReady":
		msgStats.bsReady++
	case "WindowResized":
		msgStats.winResized++
	case "OverlayReady":
		msgStats.overlayRdy++
	case "OverlayReleased":
		msgStats.overlayRel++
	case "OverlayInput":
		msgStats.overlayIn++
	case "TimerTick":
		msgStats.timerTick++
	default:
		msgStats.other++
	}
	if msgStats.total-msgStats.lastDump >= msgDumpInterval {
		msgStats.lastDump = msgStats.total
		sys.UartWriteString(fmt.Sprintf(
			"[calc:msg] total=%d key=%d mouse=%d focus=%d bs=%d resize=%d ovRdy=%d ovRel=%d ovIn=%d tick=%d blit=%d other=%d\n",
			msgStats.total, msgStats.keyPress, msgStats.mousePress, msgStats.focus,
			msgStats.bsReady, msgStats.winResized,
			msgStats.overlayRdy, msgStats.overlayRel, msgStats.overlayIn,
			msgStats.timerTick, msgStats.blitSent, msgStats.other))
	}
}

// --- Application Lifecycle ---

var rachelSID int
var wmCh = make(chan any, 16)

func announceToWM(x, y, w, h int32) {
	app.AnnounceToWM(x, y, w, h)
}

func sendBlit() {
	msgStats.blitSent++
	app.SendBlit()
}

func startUringDispatcher(fc *fontcache.FontCache) {
	d := uring.NewDispatcher()
	d.On(ipc.ProtoShepherdNotify, wm.DecodeShepherdNotify, wmCh)
	d.On(ipc.ProtoFontResponse, wm.DecodeFontResponse, fc.ReplyCh)
	d.Start()
}

func main() {
	fmt.Fprintln(os.Stdout, "[calc] main() entered")

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
	fmt.Fprintf(os.Stdout, "[calc] screen: %dx%d\n", screenW, screenH)

	// 5. AppWindow — required by rachel for Bounds/Title attributes.
	app = std.NewAppWindow(pal, "HP-15C")
	app.RachelSID = rachelSID
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
	fmt.Fprintln(os.Stdout, "[calc] Ready=true")

	announceToWM(int32(winX), int32(winY), 660, 210)

	// 9. Wait for backing store.
	fmt.Fprintln(os.Stdout, "[calc] waiting for BackingStoreReady...")
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
	glyphProvider = fontcache.NewFontSvcGlyphProvider(fc)
	provider := glyphProvider
	dc := mancini.NewDrawContextForImage(bsImg, provider)
	mainDC = dc

	leftInset := float64(bsr.LeftInset)
	topInset := float64(bsr.TopInset)
	dc.Push()
	dc.Translate(leftInset, topInset)
	dc.DrawRectangle(0, 0, float64(bsr.AppWidth), float64(bsr.AppHeight))
	dc.Clip()

	fmt.Fprintf(os.Stdout, "[calc] backing store ready: total=%dx%d inset=(%d,%d) app=%dx%d\n",
		totalW, totalH, bsr.LeftInset, bsr.TopInset, bsr.AppWidth, bsr.AppHeight)

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
	fmt.Fprintf(os.Stdout, "[calc] initial draw: %dms\n", dt/1_000_000)
	sendBlit()
	fmt.Fprintln(os.Stdout, "[calc] initial draw complete")

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
			msgStatsLog("TimerTick")
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
			if rperf.active {
				resizePerfEnd()
			}
			msgStatsLog("KeyPress")
			handleKeyPress(m)
		case wm.MousePress:
			if rperf.active {
				resizePerfEnd()
			}
			msgStatsLog("MousePress")
			lx := int(m.X)
			ly := int(m.Y)
			hitTestAndPress(lx, ly)
		case wm.OverlayReady:
			msgStatsLog("OverlayReady")
			sys.UartWriteString(fmt.Sprintf("[calc:overlay] OverlayReady id=%d addr=0x%x %dx%d at (%d,%d)\n",
				m.OverlayID, m.OverlayAddr, m.Width, m.Height, m.ScreenX, m.ScreenY))
			handleOverlayReady(m)
			continue // don't redraw main window
		case wm.OverlayReleased:
			msgStatsLog("OverlayReleased")
			sys.UartWriteString(fmt.Sprintf("[calc:overlay] OverlayReleased id=%d\n", m.OverlayID))
			handleOverlayReleased(m)
			continue // don't redraw main window
		case wm.OverlayInput:
			msgStatsLog("OverlayInput")
			sys.UartWriteString(fmt.Sprintf("[calc:overlay] OverlayInput id=%d kind=%d xy=(%d,%d) btn=%d char=%d action=%d\n",
				m.OverlayID, m.Kind, m.X, m.Y, m.Button, m.Char, m.Action))
			continue // don't redraw main window
		case wm.WindowMoved:
			mancini.SetScreenOrigin(int64(m.AppX), int64(m.AppY))
		case wm.YouHaveFocus, wm.KeyboardFocusGained:
			msgStatsLog("Focus")
			app.Focused = true
		case wm.YouLostFocus, wm.KeyboardFocusLost:
			msgStatsLog("Focus")
			app.Focused = false
		case wm.BackingStoreReady:
			msgStatsLog("BackingStoreReady")
			// If a resize was in progress, this BSR ends it — print report.
			if rperf.active {
				resizePerfEnd()
			}
			// New backing store from rachel (resize start/end).
			newTotalStride := int(m.TotalStride)
			newTotalH := int(m.TotalHeight)
			newBSLen := newTotalStride * newTotalH
			app.HandleBackingStoreReady(uintptr(m.BackingStoreAddr), newBSLen)
			app.SetSize(int64(m.AppWidth), int64(m.AppHeight))
			mancini.SetScreenOrigin(int64(m.AppX), int64(m.AppY))
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
			fmt.Fprintf(os.Stdout, "[calc] new BS: app=%dx%d total=%dx%d\n",
				m.AppWidth, m.AppHeight, m.TotalWidth, m.TotalHeight)
		case wm.WindowResized:
			msgStatsLog("WindowResized")
			// Start resize perf tracking on first WindowResized.
			if !rperf.active {
				resizePerfStart()
			}
			rperf.totalReceived++ // count this message

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
			resizePerfRecordCoalesce(drained, gotBSR)
			if drained > 0 {
				fmt.Fprintf(os.Stdout, "[calc] resize coalesced: skipped %d stale\n", drained)
			}
			if gotBSR {
				// BackingStoreReady is on the channel — skip drawing
				// at this intermediate size, the final size is next.
				continue
			}
			// Same buffer, new dimensions only.
			mancini.SetScreenOrigin(int64(m.AppX), int64(m.AppY))
			newTotalW := int(m.TotalWidth)
			newTotalH := int(m.TotalHeight)
			newTotalStride := int(m.TotalStride)
			newAppW := int(m.AppWidth)
			newAppH := int(m.AppHeight)
			resizePerfRecordArrival(newAppW, newAppH)

			// Time constraint set operations (SetSize calls Width.Set + Height.Set).
			csT0 := nanotime()
			app.SetSize(int64(newAppW), int64(newAppH))
			csNanos := nanotime() - csT0

			bsImg = &image.RGBA{
				Pix:    bsSlice,
				Stride: newTotalStride,
				Rect:   image.Rect(0, 0, newTotalW, newTotalH),
			}

			// Time DC creation (without fonts).
			dcT0 := nanotime()
			dc = mancini.NewDrawContextForImage(bsImg, provider)
			dc.Push()
			dc.Translate(float64(m.LeftInset), float64(m.TopInset))
			dc.DrawRectangle(0, 0, float64(newAppW), float64(newAppH))
			dc.Clip()
			dcNanos := nanotime() - dcT0

			// Time initFonts separately.
			fontsT0 := nanotime()
			initFonts(dc)
			fontsNanos := nanotime() - fontsT0

			fmt.Fprintf(os.Stdout, "[calc] resized: app=%dx%d total=%dx%d\n",
				newAppW, newAppH, newTotalW, newTotalH)

			// Draw + blit with timing.
			syncDisplay()
			drawT0 := nanotime()
			app.SetDC(dc)
			app.Draw(app, 0, 0, appLH.Width.Get(), appLH.Height.Get())
			drawNanos := nanotime() - drawT0

			blitT0 := nanotime()
			sendBlit()
			blitNanos := nanotime() - blitT0

			resizePerfRecordDraw(drawNanos, dcNanos, fontsNanos, blitNanos, csNanos)
			continue // skip the common draw/blit below (we did it inline)
		default:
			msgStatsLog("Other")
			// If a resize was in progress, a non-resize message ends it.
			if rperf.active {
				resizePerfEnd()
			}
			sys.UartWriteString(fmt.Sprintf("[calc:msg] unhandled message type: %T\n", msg))
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
