// linux is a userspace shepherd that handles Linux file syscalls (open, read,
// write, close, seek, etc.) via delegation, and owns the serial port soft IRQ
// to render kernel console output inside a mancini AppWindow with a gradient
// purple title bar. Lines are displayed as ConsoleLabel interactors inside a
// ColumnOutsideIn container.
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
	"mazzy/mazarin/fsclient"
	"mazzy/mazarin/mazhost"
	mfont "mazzy/shared/font"
	"mazzy/mazarin/mancini"
	"mazzy/mazarin/mancini/std"
	mctheme "mazzy/mazarin/mancini/theme"
	"mazzy/mazarin/serial"
	"mazzy/mazarin/sys"
	"mazzy/mazarin/uring"
	"mazzy/shared/ipc"
	"mazzy/shared/sysid"
	"mazzy/shared/wm"
)

const (
	consoleCols  = 80
	consoleRows  = 12
)

// suppressSerialCopy is set via SUPPRESS_SERIAL_STDIO_COPY env var.
var suppressSerialCopy bool

// wmCh receives typed WM messages (wm.BackingStoreReady, wm.KeyboardFocusGained, etc.)
// from the uring Dispatcher.
var wmCh = make(chan any, 4)

// sendBlit tells rachel to copy our backing store to the framebuffer.
func sendBlit() {
	if linuxRachelSID < 0 {
		return
	}
	msg := wm.EncodeBlit(&wm.Blit{SID: int32(os.Getpid())})
	_ = uring.Send(linuxRachelSID, &msg)
}

// linuxRachelSID is rachel's SID, set during main().
var linuxRachelSID int

// delegateMsg carries processed delegate data to the main goroutine.
// The delegate goroutine replies immediately (unblocking callers) and
// forwards text data here for console state update + redraw.
type delegateMsg struct {
	fd   byte
	data []byte
}

// startUringDelegateHandler runs a goroutine that processes delegated syscalls
// received via uring Dispatcher (ProtoFSDelegateReq → sys.SyscallRequest).
// Write to fd 1/2 (stdout/stderr) is handled as console output: echo to UART,
// reply immediately, and forward text to the main goroutine for display.
// All other syscalls (including Write to fd 3+) are routed to the syscallHandler.
func startUringDelegateHandler(delegateCh <-chan any, handler *syscallHandler, suppressSerialCopy bool) <-chan delegateMsg {
	dataCh := make(chan delegateMsg, 32)
	go func() {
		for raw := range delegateCh {
			req, ok := raw.(sys.SyscallRequest)
			if !ok {
				continue
			}
			// Write to stdout/stderr → console display path.
			if req.SysID == sysid.Write {
				fd := byte(req.Arg0())
				if fd <= 2 {
					data := req.Data()
					if data == nil {
						req.Reply(0)
						continue
					}
					// Copy before Reply — kernel reclaims the data page on Reply.
					dataCopy := make([]byte, len(data))
					copy(dataCopy, data)
					// Echo to UART ring buffer (non-blocking).
					if fd == 2 || !suppressSerialCopy {
						sys.UartWrite(addCRBeforeLF(data))
					}
					req.Reply(int64(len(data)))
					// Forward to main goroutine for console state + redraw.
					select {
					case dataCh <- delegateMsg{fd: fd, data: dataCopy}:
					default:
					}
					continue
				}
			}
			// Everything else → syscallHandler.
			handler.handle(req)
		}
	}()
	return dataCh
}

// addCRBeforeLF inserts \r before each \n for serial terminal compatibility.
func addCRBeforeLF(data []byte) []byte {
	n := 0
	for _, b := range data {
		if b == '\n' {
			n++
		}
	}
	if n == 0 {
		return data
	}
	out := make([]byte, len(data)+n)
	j := 0
	for _, b := range data {
		if b == '\n' {
			out[j] = '\r'
			j++
		}
		out[j] = b
		j++
	}
	return out
}

// startUringDispatcher sets up the uring Dispatcher for WM, font, delegated
// syscall, and fs IPC response messages.
func startUringDispatcher(fc *fontcache.FontCache, fsClient *fsclient.Client, delegateCh chan any) {
	d := uring.NewDispatcher()
	d.On(ipc.ProtoShepherdNotify, wm.DecodeShepherdNotify, wmCh)
	d.On(ipc.ProtoFontResponse, wm.DecodeFontResponse, fc.ReplyCh)
	d.On(ipc.ProtoFSDelegateReq, sys.DecodeFSDelegateReq, delegateCh)
	d.On(ipc.ProtoFSIPCResp, fsclient.DecodeResp, fsClient.RespCh)
	d.OnDeath(func(deadSID int16) {
		sys.UartWriteString(fmt.Sprintf("[linux] shepherd %d died\n", deadSID))
	})
	d.Start()
}

// announceToWM sends AppStart to rachel with window dimensions via uring.
func announceToWM(x, y, w, h int32) {
	if linuxRachelSID < 0 {
		return
	}
	msg := wm.EncodeAppStart(&wm.AppStart{
		SID:    int32(os.Getpid()),
		X:      x,
		Y:      y,
		Width:  w,
		Height: h,
	})
	if err := uring.Send(linuxRachelSID, &msg); err != nil {
		sys.UartWriteString("[linux] uring.Send AppStart failed: " + err.Error() + "\n")
		return
	}
	sys.UartWriteString(fmt.Sprintf("[linux] sent AppStart to rachel: %dx%d at (%d,%d)\n", w, h, x, y))
}

func main() {
	sys.UartWriteString("[linux] main() entered\n")

	// 1. Initialize constraint system.
	attr.Init()
	
	mancini.Init()

	// Publish Ready=false until setup is complete.
	readyAttr := attr.ValueBool(wm.ReadyURI(attr.SID()), false)

	// 2. Wait for fs first (file operations), then rachel (window manager + fontsvc).
	// fs is ready earlier since rachel depends on fs for loading .maz files.
	if err := sys.WaitForShepherdReady("fs", 10); err != nil {
		panic(fmt.Sprintf("[linux] FATAL: fs: %v", err))
	}
	if err := sys.WaitForShepherdReady("rachel", 10); err != nil {
		panic(fmt.Sprintf("[linux] FATAL: rachel: %v", err))
	}
	rachelSID := sys.MustGetShepherdByName("rachel")
	fsSID := sys.MustGetShepherdByName("fs")
	fc := fontcache.New(rachelSID)
	fsClient := fsclient.New(fsSID)
	delegateCh := make(chan any, 8)
	startUringDispatcher(fc, fsClient, delegateCh)
	if err := fsClient.Connect(); err != nil {
		panic(fmt.Sprintf("[linux] FATAL: fsclient.Connect: %v", err))
	}
	sys.UartWriteString("[linux] fs IPC connected via uring\n")
	fonts := &mancini.FontConfig{
		LoadFace: func(bold bool, size int64) font.Face {
			return fc.OpenFaceByName(mfont.DefaultMono, mfont.Regular, size)
		},
		FontRegular: mfont.DefaultMono,
		FontBold:    mfont.DefaultMono,
	}
	pal := mctheme.NewDefaultPaletteSwapRB()

	suppressSerialCopy = os.Getenv("SUPPRESS_SERIAL_STDIO_COPY") == "1"

	// 3. Screen size attributes — must exist before AppWindow constraints.
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
	sys.UartWriteString(fmt.Sprintf("[linux] TextSize=%d (from rachel)\n", fontSize))

	// 4. Build UI tree: AppWindow → Column → [SingleLineText, Console].
	// Create a theme for SingleLineText (needs Theme, not just Palette).
	resolver := func(family string, feature mancini.Feature, size int64) font.Face {
		return fc.OpenFaceByName(family, feature.String(), size)
	}
	theme := mctheme.NewTheme(pal, mctheme.NewDefaultNeumorphicParams(),
		mfont.DefaultMono, fontSize, resolver)

	col := std.NewColumn("column", "AppWindow", pal, 0, mancini.AxisMinimum, 2, false)
	_ = col

	// Console first so its width attribute exists for the input constraint.
	console := std.NewConsole("console", "column", pal, fonts, fontSize,
		consoleCols, consoleRows)

	// Input row: label + text field side by side, width matches console.
	const inputRowSpacing = int64(4)
	const inputRowHMargin = int64(1)
	inputRow := std.NewRow("inputRow", "column", pal, 0, mancini.AxisMiddle, inputRowHMargin)
	inputRow.SetSpacing(float64(inputRowSpacing))

	// Label to the left of the input field (transparent so AppWindow
	// SurfaceTint background shows through).
	inputLabel := std.NewLabelNamed("inputLabel", "inputRow", theme,
		"Stdio Input", fontSize)
	inputLabel.Transparent = true

	// Input field — width = consoleWidth - labelWidth - (spacing + 2*hMargin).
	consoleWidthURI := mancini.LayoutURI("console", mancini.DataTypeInt64, mancini.LayoutWidth)
	labelWidthURI := mancini.LayoutURI("inputLabel", mancini.DataTypeInt64, mancini.LayoutWidth)
	fixedOffsetURI := attr.ShepherdURI("int64", "inputFixedOffset")
	attr.ValueI64(fixedOffsetURI, inputRowSpacing+2*inputRowHMargin)

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
	_ = input
	_ = inputLabel

	// Swap sequence so inputRow appears above console in the Column.
	mancini.SwapSequence("inputRow", "console")

	app := std.NewAppWindow(pal, "Serial Console", screenWURI, screenHURI)
	app.Focused = false // wait for rachel to grant focus
	app.RachelSID = rachelSID
	input.AppWindow = app

	// Initialize input dispatch pipeline and set keyboard focus to input field.
	// KeyAgent reads pre-translated Char/Action from the wire message —
	// rachel handles keymap translation globally.
	_, _, keyAgent := app.InitInput()
	keyAgent.SetFocus(input)
	input.Focused = true
	screenW := int(screenWAttr.Get())
	screenH := int(screenHAttr.Get())

	provider := fontcache.NewFontSvcGlyphProvider(fc)

	appLH := app.GetLayout()
	appLH.X.Set(0)
	appLH.Y.Set(0)

	// Use constraint-computed dimensions.
	winW := int(appLH.Width.Get())
	winH := int(appLH.Height.Get())
	if winW < 100 {
		winW = 800
	}
	if winH < 50 {
		winH = 400
	}

	// Force Bounds evaluation for rachel.
	_ = appLH.Bounds.Get()

	// 7. Announce to rachel with our desired position and size.
	linuxRachelSID = rachelSID
	initX := screenW/2 - winW/2
	initY := screenH/2 - winH/2
	announceToWM(int32(initX), int32(initY), int32(winW), int32(winH))

	// 8. Wait for rachel to allocate backing store and share it with us.
	sys.UartWriteString("[linux] waiting for BackingStoreReady...\n")
	var bsr wm.BackingStoreReady
	for {
		raw := <-wmCh
		if b, ok := raw.(wm.BackingStoreReady); ok {
			bsr = b
			break
		}
		// Drain other messages until we get BackingStoreReady.
	}

	totalW := int(bsr.TotalWidth)
	totalH := int(bsr.TotalHeight)
	totalStride := int(bsr.TotalStride)
	bsSlice := unsafe.Slice((*byte)(unsafe.Pointer(uintptr(bsr.BackingStoreAddr))), totalStride*totalH)

	// Create image.RGBA over the full buffer (including border areas).
	bsImg := &image.RGBA{
		Pix:    bsSlice,
		Stride: totalStride,
		Rect:   image.Rect(0, 0, totalW, totalH),
	}
	dc := mancini.NewDrawContextForImage(bsImg, provider)

	// Translate origin so (0,0) is the app area, and clip to app bounds.
	leftInset := float64(bsr.LeftInset)
	topInset := float64(bsr.TopInset)
	dc.Push()
	dc.Translate(leftInset, topInset)
	dc.DrawRectangle(0, 0, float64(winW), float64(winH))
	dc.Clip()

	sys.UartWriteString(fmt.Sprintf("[linux] backing store ready: total=%dx%d inset=(%d,%d) app=%dx%d\n",
		totalW, totalH, bsr.LeftInset, bsr.TopInset, winW, winH))

	// Initial draw at (0,0) — screen position is rachel's concern.
	app.SetDC(dc)
	dc.SetColor(pal.Surface())
	dc.FillRectangle(0, 0, float64(winW), float64(winH))

	app.Draw(app, 0, 0, int64(winW), int64(winH))
	sendBlit()

	// 9. Serial channel and delegated syscalls.
	// Set up delegate handling BEFORE signalling Ready, so that other shepherds
	// waiting on our Ready don't send us delegates before we're draining them.
	serialCh, err := serial.Chars()
	if err != nil {
		sys.UartWriteString(fmt.Sprintf("[linux] serial.Chars failed: %v\n", err))
		return
	}

	handler := newSyscallHandler(fsClient)

	// Register as delegate handler — requests now arrive via uring Dispatcher
	// on delegateCh (ProtoFSDelegateReq) instead of the old delegateRecvLoop.
	delegateErr := sys.RegisterSyscallHandlers(
		sysid.Write, sysid.Read, sysid.Openat, sysid.Close,
		sysid.Lseek, sysid.Fstat, sysid.Fstatat,
		sysid.Mkdirat, sysid.Unlinkat, sysid.Renameat,
		sysid.Ftruncate, sysid.Getdents64, sysid.Readlinkat,
		sysid.Faccessat, sysid.Fchmodat, sysid.Utimensat,
		sysid.Getcwd, sysid.Chdir, sysid.Fchdir,
		sysid.Ioctl, sysid.Writev, sysid.Readv,
		sysid.Statfs, sysid.Fstatfs, sysid.Fsync, sysid.Fdatasync,
	)
	if delegateErr != nil {
		sys.UartWriteString(fmt.Sprintf("[linux] RegisterSyscallHandlers failed: %v\n", delegateErr))
	}

	// Delegate handler goroutine — replies immediately to unblock callers,
	// forwards text data to delegateDataCh for the main goroutine.
	var delegateDataCh <-chan delegateMsg
	if delegateErr == nil {
		delegateDataCh = startUringDelegateHandler(delegateCh, handler, suppressSerialCopy)
	}

	// 10. Event loop — main goroutine owns console state + redraw.
	// Signal readiness AFTER delegate handler is running, so other shepherds
	// that wait on our Ready can immediately send us delegates.
	dirtyCh := attr.OnDirty()

	readyAttr.Set(true)
	sys.SetReady(true)
	sys.UartWriteString("[linux] Ready=true\n")

	// Launch helloworld.maz — tests .maz loading and Write syscall delegation.
	mazhost.LaunchMaz("helloworld")

	sys.UartWriteString("[linux] Entering event loop\n")

	// textDirty tracks whether console state changed since last redraw.
	// Serial bytes and delegate writes set this flag; the constraint dirty
	// tick (~10Hz) triggers the actual redraw, batching many updates into
	// one draw+blit cycle to avoid flashing.
	textDirty := false
	var linuxDraws, linuxDirtyTicks, linuxSerialEvts, linuxDelegateEvts int64

	redraw := func() {
		app.Draw(app, 0, 0, int64(winW), int64(winH))
		sendBlit()
		linuxDraws++
	}

	// drainSerial consumes all buffered serial bytes without redrawing.
	drainSerial := func() {
		for {
			select {
			case sb := <-serialCh:
				console.HandleByte(sb.B, sb.Fd)
				textDirty = true
			default:
				return
			}
		}
	}

	// drainDelegates consumes all buffered delegate messages without redrawing.
	drainDelegates := func() {
		for {
			select {
			case msg := <-delegateDataCh:
				for _, b := range msg.data {
					console.HandleByte(b, msg.fd)
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
		case sb := <-serialCh:
			console.HandleByte(sb.B, sb.Fd)
			textDirty = true
			linuxSerialEvts++
			drainSerial()
			if textDirty {
				textDirty = false
				redraw()
			}

		case msg := <-delegateDataCh:
			for _, b := range msg.data {
				console.HandleByte(b, msg.fd)
			}
			textDirty = true
			linuxDelegateEvts++
			drainDelegates()
			if textDirty {
				textDirty = false
				redraw()
			}

		case wmMsg := <-wmCh:
			// Handle focus changes to toggle app and input cursor state.
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
			// Dispatch animation messages to AppWindow.
			if app.DispatchAnimation(wmMsg) {
				if appLH.HasDamage() {
					redraw()
				}
			}
			// Dispatch WM messages (keyboard, mouse, focus) through the
			// Henry-Hudson pipeline. Keyboard events go to the focused
			// SingleLineText input via KeyAgent.
			if app.Input != nil {
				app.Input.DispatchWM(wmMsg)
				if appLH.HasDamage() {
					redraw()
				}
			}

		case <-dirtyCh:
			linuxDirtyTicks++
			// Drain any pending text before redrawing.
			drainSerial()
			drainDelegates()
			if textDirty {
				textDirty = false
				redraw()
			}
			if linuxDirtyTicks%10 == 0 {
				sys.UartWriteDirectString(fmt.Sprintf("[linux] dirtyTicks=%d draws=%d serial=%d delegate=%d\n",
					linuxDirtyTicks, linuxDraws, linuxSerialEvts, linuxDelegateEvts))
			}
		}
	}
}
