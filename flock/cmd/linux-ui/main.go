// linux-ui is a .maz module loaded by the linux shepherd that provides
// the mancini console UI. It receives console output lines via the LinuxIO
// WriteChannel and sends user input via ReadChannel.
//
// From the kernel's perspective, linux-ui IS the linux shepherd (same PID/SID).
package main

import (
	"fmt"
	"image"
	"os"
	"runtime"
	"unsafe"

	"golang.org/x/image/font"

	"mazzy/mazarin/attr"
	"mazzy/mazarin/fontcache"
	"mazzy/mazarin/linuxio"
	"mazzy/mazarin/mancini"
	"mazzy/mazarin/mancini/std"
	mctheme "mazzy/mazarin/mancini/theme"
	"mazzy/mazarin/sys"
	"mazzy/mazarin/uring"
	mfont "mazzy/shared/font"
	"mazzy/shared/wm"
)

const (
	consoleCols = 80
	consoleRows = 12
)

// MazEntryPoint holds a reference to MazarinMain to prevent DCE.
var MazEntryPoint func() = MazarinMain

// MazarinShepherdAddr holds a reference to MazarinShepherd to prevent DCE.
var MazarinShepherdAddr func(interface{}) error = MazarinShepherd

func init() {
	if MazEntryPoint == nil {
		panic("unreachable")
	}
	if MazarinShepherdAddr == nil {
		panic("unreachable")
	}
}

// io holds the injected LinuxIO interface, set by MazarinShepherd.
var io linuxio.LinuxIO

// fc is the font cache, created during MazarinShepherd.
var fc *fontcache.FontCache

// wmDecodedCh carries WM messages decoded in the .maz's type namespace.
var wmDecodedCh chan any

// MazarinShepherd receives the LinuxIO injection from the linux shepherd.
// It creates the four channels and fills the struct so the shepherd can
// read them back after this call returns.
//
//go:noinline
func MazarinShepherd(injected interface{}) error {
	if injected == nil {
		rawPuts("[linux-ui] MazarinShepherd: nil injection\n")
		return nil
	}
	init, ok := injected.(linuxio.LinuxIO)
	if !ok {
		rawPuts("[linux-ui] MazarinShepherd: type assertion to LinuxIO failed\n")
		return nil
	}
	io = init

	// Create FontCache — uses rachelSID for uring IPC to fontsvc.
	fc = fontcache.New(init.GetRachelSID())

	// Create channels via the interface (concrete type assertion doesn't work
	// across .maz boundaries). Both WM and font channels carry raw payload
	// bytes (chan []byte, not chan any) from the shepherd's uring dispatcher.
	// Using []byte channels avoids cross-.maz type assertion failures —
	// even builtin types like []byte have different type descriptors.
	// Bridge goroutines decode payloads into typed messages in the .maz's
	// type namespace.
	wmRawCh := make(chan []byte, 8)
	fontRawCh := make(chan []byte, 8)
	wmDecodedCh = make(chan any, 8)

	init.SetChannels(
		make(chan []byte, 64),
		make(chan linuxio.LineLine, 64),
		wmRawCh,
		fontRawCh,
	)

	// Bridge goroutines: decode raw payloads into typed messages.
	go rawBridge(wmRawCh, wmDecodedCh, wm.DecodeShepherdNotifyFromPayload)
	go rawBridge(fontRawCh, fc.ReplyCh, wm.DecodeFontResponseFromPayload)

	rawPuts("[linux-ui] MazarinShepherd: channels created, injection complete\n")
	return nil
}

// sendBlit tells rachel to copy our backing store to the framebuffer.
func sendBlit(rachelSID int) {
	if rachelSID < 0 {
		return
	}
	msg := wm.EncodeBlit(&wm.Blit{SID: int32(os.Getpid())})
	_ = uring.Send(rachelSID, &msg)
}

// announceToWM sends AppStart to rachel via uring.
func announceToWM(rachelSID int, x, y, w, h int32) {
	if rachelSID < 0 {
		return
	}
	msg := wm.EncodeAppStart(&wm.AppStart{
		SID:    int32(os.Getpid()),
		X:      x,
		Y:      y,
		Width:  w,
		Height: h,
	})
	if err := uring.Send(rachelSID, &msg); err != nil {
		rawPuts("[linux-ui] uring.Send AppStart failed: " + err.Error() + "\n")
		return
	}
}

// MazarinMain is the .maz entry point. It builds the mancini UI and runs
// the event loop, reading from WriteChannel for console output and
// WMChannel for window manager messages.
//
//go:noinline
func MazarinMain() {
	rawPuts("[linux-ui] MazarinMain() entered\n")

	if io == nil {
		rawPuts("[linux-ui] FATAL: io is nil (MazarinShepherd not called?)\n")
		return
	}

	rachelSID := io.GetRachelSID()

	// Initialize constraint system.
	attr.Init()
	mancini.Init()

	// Font config using our own FontCache (created during MazarinShepherd).
	fonts := &mancini.FontConfig{
		LoadFace: func(bold bool, size int64) font.Face {
			style := mfont.Regular
			if bold {
				style = mfont.Bold
			}
			return fc.OpenFaceByName(mfont.DefaultMono, style, size)
		},
		FontRegular: mfont.DefaultMono,
		FontBold:    mfont.DefaultMono,
	}
	pal := mctheme.NewDefaultPaletteSwapRB()

	// Screen size attributes — must exist before AppWindow constraints.
	screenWURI := "attr:///kernel/int64/screen/width"
	screenHURI := "attr:///kernel/int64/screen/height"
	screenWProg := mancini.BindStrings(mancini.ProgIdentityI64,
		"_source_", screenWURI)
	screenWAttr := attr.ConstraintI64(attr.ShepherdURI("int64", "screen_w"), screenWProg)
	screenHProg := mancini.BindStrings(mancini.ProgIdentityI64,
		"_source_", screenHURI)
	screenHAttr := attr.ConstraintI64(attr.ShepherdURI("int64", "screen_h"), screenHProg)

	// Bind TextSize to rachel's published defaultTextFontSize attribute.
	rachelFontSizeURI := fmt.Sprintf("attr:///shepherd/%d/int64/defaultTextFontSize", rachelSID)
	textSizeProg := mancini.BindStrings(mancini.ProgIdentityI64,
		"_source_", rachelFontSizeURI)
	textSizeAttr := attr.ConstraintI64(
		attr.ShepherdURI("int64", "TextSize"), textSizeProg)
	fontSize := textSizeAttr.Get()
	if fontSize <= 0 {
		fontSize = 24
	}

	rawPuts(fmt.Sprintf("[linux-ui] TextSize=%d\n", fontSize))

	// Build UI tree: AppWindow -> Column -> [InputRow, Console].
	resolver := func(family string, feature mancini.Feature, size int64) font.Face {
		style := mfont.Regular
		if feature == mancini.Bold {
			style = mfont.Bold
		}
		return fc.OpenFaceByName(family, style, size)
	}
	neu := mctheme.NewDefaultNeumorphicParams()
	theme := mctheme.NewTheme(pal, neu, mfont.DefaultMono, fontSize, resolver)
	theme.SetStyle(std.NewNeumorphicStyle(neu.Heavy(), neu.Light()))

	// AppWindow must be created before the bridge so its Width/Height URIs exist.
	app := std.NewAppWindow(pal, "Linux Console")
	app.Focused = false // wait for rachel to grant focus

	// Column: width/height mirror AppWindow's dimensions.
	colLH := mancini.NewLayoutAttributesBase("column", "AppWindow")
	appWURI := mancini.LayoutURI("AppWindow", mancini.DataTypeInt64, mancini.LayoutWidth)
	appHURI := mancini.LayoutURI("AppWindow", mancini.DataTypeInt64, mancini.LayoutHeight)
	colLH.Width = attr.ConstraintI64(
		mancini.LayoutURI("column", mancini.DataTypeInt64, mancini.LayoutWidth),
		mancini.EqualI64(appWURI))
	colLH.Height = attr.ConstraintI64(
		mancini.LayoutURI("column", mancini.DataTypeInt64, mancini.LayoutHeight),
		mancini.EqualI64(appHURI))
	colLH.InitBounds("column")
	col := std.NewColumnWithLayout(colLH, pal, mancini.AxisMinimum, 0, 1, false)
	_ = col

	// Console first so its width attribute exists for the input constraint.
	console := std.NewConsole("console", "column", pal, fonts, fontSize,
		consoleCols, consoleRows)

	// Input row: label + text field side by side, width matches console.
	const inputRowSpacing = int64(4)
	const inputRowHPadding = int64(1)
	inputRow := std.NewRow("inputRow", "column", pal, 0, mancini.AxisMiddle, inputRowHPadding)
	inputRow.SetSpacing(float64(inputRowSpacing))

	inputLabel := std.NewLabelNamed("inputLabel", "inputRow", theme,
		"Stdio Input", fontSize)
	inputLabel.Transparent = true

	// Input field — width = consoleWidth - labelWidth - (spacing + 2*hPadding).
	consoleWidthURI := mancini.LayoutURI("console", mancini.DataTypeInt64, mancini.LayoutWidth)
	labelWidthURI := mancini.LayoutURI("inputLabel", mancini.DataTypeInt64, mancini.LayoutWidth)
	fixedOffsetURI := attr.ShepherdURI("int64", "inputFixedOffset")
	attr.ValueI64(fixedOffsetURI, inputRowSpacing+2*inputRowHPadding)

	inputLH := mancini.NewLayoutAttributesBase("input", "inputRow")
	inputLH.Width = attr.ConstraintI64(
		mancini.LayoutURI("input", mancini.DataTypeInt64, mancini.LayoutWidth),
		mancini.BindStrings(mancini.ProgSubTwoDeref,
			"_a_", consoleWidthURI,
			"_b_", labelWidthURI,
			"_c_", fixedOffsetURI))
	inputLH.Height = attr.ValueI64(
		mancini.LayoutURI("input", mancini.DataTypeInt64, mancini.LayoutHeight),
		fontSize+16)
	inputLH.InitBounds("input")
	input := std.NewSingleLineText(inputLH, theme, "", fontSize)
	input.Hint = "linux stdin text goes here..."
	_ = inputLabel

	// Wire up stdin: on Enter, send the text (+ \n) to the shepherd's ReadChannel.
	readCh := io.ReadChannel()
	input.OnSubmit = func(text string) {
		line := []byte(text + "\n")
		select {
		case readCh <- line:
		default:
			rawPuts("[linux-ui] ReadChannel full, dropping input line\n")
		}
	}

	// Swap sequence so inputRow appears above console in the Column.
	mancini.SwapSequence("inputRow", "console")

	app.RachelSID = rachelSID
	input.AppWindow = app

	// Initialize input dispatch pipeline and set keyboard focus to input field.
	_, _, keyAgent := app.InitInput()
	keyAgent.SetFocus(input)
	input.Focused = true
	screenW := int(screenWAttr.Get())
	screenH := int(screenHAttr.Get())

	appLH := app.GetLayout()
	appLH.X.Set(0)
	appLH.Y.Set(0)

	// Preferred size for AppStart — rachel may adjust.
	winW := 800
	winH := 400

	// Force Bounds evaluation and publish Ready for rachel.
	_ = appLH.Bounds.Get()
	readyAttr := attr.ValueBool(wm.ReadyURI(attr.SID()), true)
	_ = readyAttr

	// Announce to rachel with our desired position and size.
	initX := screenW/2 - winW/2
	initY := screenH/2 - winH/2
	announceToWM(rachelSID, int32(initX), int32(initY), int32(winW), int32(winH))

	// Wait for rachel to allocate backing store and share it with us.
	rawPuts("[linux-ui] waiting for BackingStoreReady...\n")
	wmCh := wmDecodedCh
	var bsr wm.BackingStoreReady
	for {
		raw := <-wmCh
		if b, ok := raw.(wm.BackingStoreReady); ok {
			bsr = b
			break
		}
	}

	// Use actual dimensions from rachel's response.
	winW = int(bsr.AppWidth)
	winH = int(bsr.AppHeight)
	if winW < 100 {
		winW = 800
	}
	if winH < 50 {
		winH = 400
	}
	// Set AppWindow dimensions so bridge constraints can read them.
	appLH.Width.Set(int64(winW))
	appLH.Height.Set(int64(winH))

	totalW := int(bsr.TotalWidth)
	totalH := int(bsr.TotalHeight)
	totalStride := int(bsr.TotalStride)
	bsSlice := unsafe.Slice((*byte)(unsafe.Pointer(uintptr(bsr.BackingStoreAddr))), totalStride*totalH)

	bsImg := &image.RGBA{
		Pix:    bsSlice,
		Stride: totalStride,
		Rect:   image.Rect(0, 0, totalW, totalH),
	}

	// Create DrawContext using our own GlyphProvider.
	provider := fontcache.NewFontSvcGlyphProvider(fc)
	dc := mancini.NewDrawContextForImage(bsImg, provider)

	leftInset := float64(bsr.LeftInset)
	topInset := float64(bsr.TopInset)
	dc.Push()
	dc.Translate(leftInset, topInset)
	dc.DrawRectangle(0, 0, float64(winW), float64(winH))
	dc.Clip()

	rawPuts(fmt.Sprintf("[linux-ui] backing store ready: total=%dx%d inset=(%d,%d) app=%dx%d\n",
		totalW, totalH, bsr.LeftInset, bsr.TopInset, winW, winH))

	// Initial draw.
	app.SetDC(dc)
	dc.SetColor(pal.Surface())
	dc.FillRectangle(0, 0, float64(winW), float64(winH))

	app.Draw(app, 0, 0, int64(winW), int64(winH))
	sendBlit(rachelSID)

	// Event loop.
	dirtyCh := attr.OnDirty()
	writeCh := io.WriteChannel()

	rawPuts("[linux-ui] entering event loop\n")

	textDirty := false
	var draws, dirtyTicks, writeEvts int64

	redraw := func() {
		app.Draw(app, 0, 0, int64(winW), int64(winH))
		sendBlit(rachelSID)
		draws++
	}

	// drainWrites consumes all buffered write channel messages.
	drainWrites := func() {
		for {
			select {
			case line := <-writeCh:
				for _, b := range line.Data {
					console.HandleByte(b, line.Fd)
				}
				textDirty = true
			default:
				return
			}
		}
	}

	for {
		runtime.Gosched()

		select {
		case line := <-writeCh:
			for _, b := range line.Data {
				console.HandleByte(b, line.Fd)
			}
			textDirty = true
			writeEvts++
			drainWrites()
			if textDirty {
				textDirty = false
				redraw()
			}

		case wmMsg := <-wmCh:
			switch wmMsg.(type) {
			case wm.KeyboardFocusGained:
				app.Focus()
				input.Focused = true
				redraw()
			case wm.KeyboardFocusLost:
				app.Unfocus()
				input.Focused = false
				redraw()
			}
			if app.DispatchAnimation(wmMsg) {
				if appLH.HasDamage() {
					redraw()
				}
			}
			if app.Input != nil {
				app.Input.DispatchWM(wmMsg)
				if appLH.HasDamage() {
					redraw()
				}
			}

		case <-dirtyCh:
			dirtyTicks++
			drainWrites()
			if textDirty {
				textDirty = false
				redraw()
			}
			if dirtyTicks%10 == 0 {
				rawPuts(fmt.Sprintf("[linux-ui] dirtyTicks=%d draws=%d writes=%d\n",
					dirtyTicks, draws, writeEvts))
			}
		}
	}
}

// rawBridge reads raw payload bytes from a channel, decodes them using the
// provided decoder function (running in the .maz's goroutine so types match),
// and forwards decoded messages to the output channel.
func rawBridge(rawCh <-chan []byte, decodedCh chan<- any, decode func([]byte) any) {
	for payload := range rawCh {
		if len(payload) < 4 {
			continue
		}
		decoded := decode(payload)
		if decoded != nil {
			decodedCh <- decoded
		}
	}
}

// rawPuts writes directly to UART, bypassing the delegation system.
func rawPuts(s string) {
	sys.UartWriteString(s)
}

func main() { MazarinMain() }
