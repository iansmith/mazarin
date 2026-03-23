// stdio is a userspace shepherd that owns the serial port soft IRQ and
// renders kernel console output inside a mancini AppWindow with a
// gradient purple title bar. Lines are displayed as ConsoleLabel
// interactors inside a ColumnOutsideIn container.
package main

import (
	"fmt"
	"image/color"
	"os"
	"runtime"
	"strconv"
	"time"
	"unsafe"

	"golang.org/x/image/font"

	"github.com/fogleman/gg"

	"mazzy/mazarin/attr"
	"mazzy/mazarin/fontcache"
	"mazzy/mazarin/interactor"
	mfont "mazzy/shared/font"
	"mazzy/mazarin/mancini"
	"mazzy/mazarin/ringbuf"
	"mazzy/mazarin/serial"
	"mazzy/mazarin/sys"
	"mazzy/shared/sysid"
	"mazzy/shared/wm"
)

const (
	maxPoolLines = 10 // pre-allocated label pool size (limited by kernel attr node slots)
	fontSize     = 16.0
)

// Console text colors (natural RGB — gg context has SwapRB set).
var (
	nContent = color.NRGBA{40, 42, 48, 255}    // content well background
	nText    = color.NRGBA{200, 205, 215, 255}  // stdout text
	nStderr  = color.NRGBA{200, 80, 80, 255}    // stderr text
)

// lineData holds one line of console output.
type lineData struct {
	text  string
	color color.NRGBA
}

// console tracks the text state for the serial console.
type console struct {
	lines     [maxPoolLines]lineData
	lineCount int // number of lines currently in use
	maxCols   int
	lastFd    byte

	suppressSerialCopy bool
}

func (c *console) currentLineIdx() int {
	if c.lineCount == 0 {
		c.lineCount = 1
	}
	return c.lineCount - 1
}

// handleSerialByte processes a single serial byte, updating line state.
func (c *console) handleSerialByte(sb serial.SerialByte) {
	if sb.B == '\r' {
		return
	}
	if sb.B == '\n' {
		if c.lineCount < maxPoolLines {
			c.lineCount++
		} else {
			c.scroll()
		}
		return
	}

	lineIdx := c.currentLineIdx()

	// Force newline when fd changes mid-line so stdout/stderr
	// appear on separate lines.
	if c.lastFd != 0 && sb.Fd != c.lastFd && len(c.lines[lineIdx].text) > 0 {
		c.lastFd = sb.Fd
		c.handleSerialByte(serial.SerialByte{Fd: sb.Fd, B: '\n'})
		lineIdx = c.currentLineIdx()
	}
	c.lastFd = sb.Fd

	// Clamp line length.
	if len(c.lines[lineIdx].text) >= c.maxCols {
		return
	}

	c.lines[lineIdx].text += string(sb.B)
	if sb.Fd == 2 {
		c.lines[lineIdx].color = nStderr
	} else if len(c.lines[lineIdx].text) == 1 {
		// Set color on first char of line (whole-line coloring).
		c.lines[lineIdx].color = nText
	}
}

// scroll shifts all lines up by one, dropping the oldest.
func (c *console) scroll() {
	copy(c.lines[:], c.lines[1:])
	c.lines[maxPoolLines-1] = lineData{}
	// lineCount stays at maxPoolLines
}

// delegateMsg carries processed delegate data to the main goroutine.
// The delegate goroutine replies immediately (unblocking callers) and
// forwards text data here for console state update + redraw.
type delegateMsg struct {
	fd   byte
	data []byte
}

// startDelegateHandler runs a goroutine that processes delegated syscalls.
// It replies immediately (so callers never block on stdio's redraw) and
// forwards Write data to the returned channel for the main goroutine.
func startDelegateHandler(delegateCh <-chan sys.SyscallRequest, suppressSerialCopy bool) <-chan delegateMsg {
	dataCh := make(chan delegateMsg, 32)
	go func() {
		for req := range delegateCh {
			switch req.SysID {
			case sysid.Write:
				data := req.Data()
				if data == nil {
					req.Reply(0)
					continue
				}
				fd := byte(req.Arg0())
				// Copy before Reply — kernel reclaims the data page on Reply.
				dataCopy := make([]byte, len(data))
				copy(dataCopy, data)
				// Echo to UART ring buffer (non-blocking).
				if fd == 2 || !suppressSerialCopy {
					sys.UartWrite(addCRBeforeLF(data))
				}
				req.Reply(int64(len(data)))
				// Forward to main goroutine for console state + redraw.
				// Non-blocking: drop display update if main goroutine is behind.
				// Caller is already unblocked and UART echo is done.
				select {
				case dataCh <- delegateMsg{fd: fd, data: dataCopy}:
				default:
				}

			case sysid.Openat:
				path := req.PathString()
				sys.UartWriteString("[stdio] openat: " + path + "\n")
				if path == "/dev/random" {
					panic("[stdio] /dev/random not implemented yet")
				}
				req.Reply(-38) // ENOSYS

			default:
				req.Reply(-38) // ENOSYS
			}
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

// mailboxRecvLoop receives notifications from rachel (font responses, WM messages).
func mailboxRecvLoop(fc *fontcache.FontCache) {
	for {
		notif, err := sys.MailboxRecv()
		if err != nil {
			continue
		}
		switch notif.Code {
		case wm.FontResponse:
			fc.HandleNotification(notif)
		case wm.ShepherdNotify:
			rb := ringbuf.Open(uintptr(notif.RingAddr))
			var raw [wm.SizeWMMessage]byte
			for rb.Pop(unsafe.Pointer(&raw[0])) {
				msgType := *(*int64)(unsafe.Pointer(&raw[0]))
				switch msgType {
				case wm.MsgYouHaveFocus:
					sys.UartWriteString("[stdio] received YouHaveFocus\n")
				case wm.MsgYouLostFocus:
					sys.UartWriteString("[stdio] received YouLostFocus\n")
				}
			}
		}
	}
}

// announceToWM sends AppStart to rachel so we get positioned.
func announceToWM(rachelSID int) {
	if rachelSID < 0 {
		return
	}
	rb, err := ringbuf.New(rachelSID, 0, wm.SizeWMMessage, wm.DefaultSlotCount)
	if err != nil {
		sys.UartWriteString("[stdio] ring buffer creation failed: " + err.Error() + "\n")
		return
	}
	var msg wm.AppStartMsg
	msg.Type = wm.MsgAppStart
	msg.SID = int64(os.Getpid())
	rb.Push(unsafe.Pointer(&msg))
	if err := sys.MailboxSend(rachelSID, wm.WMNotify, rb.Addr()); err != nil {
		sys.UartWriteString("[stdio] MailboxSend failed: " + err.Error() + "\n")
		return
	}
	sys.UartWriteString("[stdio] sent AppStart to rachel\n")
}

var startTime time.Time

func main() {
	startTime = time.Now()
	sys.UartWriteString("[stdio] main() entered\n")

	// 1. Initialize constraint system.
	attr.Init()
	interactor.Init()
	mancini.Init()
	sys.UartWriteString(fmt.Sprintf("[stdio] attr + interactor + mancini init done, SID=%s (T+%v)\n", attr.SID(), time.Since(startTime)))

	// Publish Ready=false until setup is complete.
	readyHandle := attr.ValueBool(wm.ReadyURI(attr.SID()), false)

	// 2. Wait for rachel (fontsvc) and disk (fs.maz) before creating fontcache.
	// Without this, OpenFace may fail because fs.maz hasn't registered for
	// LoadFile yet. Caching a nil font would then prevent the event loop
	// from ever loading it, or retrying during redraw would deadlock (stdio
	// blocked on font reply, fontsvc blocked on Write delegation back to stdio).
	sys.UartWriteString(fmt.Sprintf("[stdio] waiting for rachel + disk ready... (T+%v)\n", time.Since(startTime)))
	if !sys.WaitForReady("rachel", 10*time.Second) {
		panic("[stdio] FATAL: rachel not ready after 10s")
	}
	if !sys.WaitForReady("disk", 10*time.Second) {
		panic("[stdio] FATAL: disk not ready after 10s")
	}
	sys.UartWriteString(fmt.Sprintf("[stdio] rachel + disk ready (T+%v)\n", time.Since(startTime)))

	rachelSID := sys.MustGetShepherdByName("rachel")
	fc := fontcache.New(rachelSID)
	go mailboxRecvLoop(fc)
	sys.UartWriteString(fmt.Sprintf("[stdio] fontcache created, rachel SID=%d (T+%v)\n", rachelSID, time.Since(startTime)))

	fonts := &mancini.FontConfig{
		LoadFace: func(bold bool, size float64) font.Face {
			return fc.OpenFaceByName(mfont.DefaultMono, mfont.Regular, size)
		},
	}
	sys.UartWriteString(fmt.Sprintf("[stdio] FontConfig ready (T+%v)\n", time.Since(startTime)))

	pal := mancini.DefaultPalette()
	pal.SwapRB = true

	// 3. Probe font metrics (this triggers the first OpenFace RPC to rachel).
	sys.UartWriteString(fmt.Sprintf("[stdio] probing font metrics... (T+%v)\n", time.Since(startTime)))
	probeFace := fonts.LoadFace(false, fontSize)
	charW := 10 // fallback
	if probeFace != nil {
		adv, ok := probeFace.GlyphAdvance('M')
		if ok {
			charW = adv.Ceil()
		}
	}
	// Approximate content width: MaxWidth(900) - shadow(~40) - chrome(~32) - textpad(8)
	approxContentW := 900 - 40 - 32 - 8
	maxCols := approxContentW / charW
	if maxCols < 40 {
		maxCols = 40
	}
	sys.UartWriteString(fmt.Sprintf("[stdio] charW=%d maxCols=%d (T+%v)\n", charW, maxCols, time.Since(startTime)))

	// 4. Console state.
	con := &console{
		maxCols:            maxCols,
		suppressSerialCopy: os.Getenv("SUPPRESS_SERIAL_STDIO_COPY") == "1",
	}

	// 5. Build UI tree: AppWindow → ColumnOutsideIn → ConsoleLabels.
	labels := make([]*mancini.ConsoleLabel, maxPoolLines)
	drawers := make([]mancini.Drawer, maxPoolLines)
	for i := range labels {
		idx := i
		labels[i] = &mancini.ConsoleLabel{
			Pal:      pal,
			Fonts:    fonts,
			Name:     fmt.Sprintf("line_%d", i),
			FontSize: fontSize,
			BgColor:  nContent,
			TextFunc: func() string {
				if idx < con.lineCount {
					return con.lines[idx].text
				}
				return ""
			},
			ColorFunc: func() color.NRGBA {
				if idx < con.lineCount {
					return con.lines[idx].color
				}
				return nText
			},
		}
		drawers[i] = labels[i]
	}

	content := &mancini.ColumnOutsideIn{
		Pal:      pal,
		Name:     "console_col",
		Children: drawers,
		BgColor:  nContent,
	}

	titleBar := mancini.GradientTitleFace(fonts, pal, "Serial Console", 18, 8)

	app := &mancini.AppWindow{
		Pal:      pal,
		Fonts:    fonts,
		Name:     "AppWindow",
		Title:    "Serial Console",
		Focused:  true,
		TitleBar: titleBar,
		Content:  content,
		MaxWidth: 900,
	}

	// InitLayout bottom-up: labels → column → app.
	for _, label := range labels {
		label.InitLayout("console_col")
	}
	content.InitLayout("AppWindow")
	app.InitLayout("")
	sys.UartWriteString(fmt.Sprintf("[stdio] UI tree built (T+%v)\n", time.Since(startTime)))

	// 6. Screen dimensions and draw context.
	screenWProg := interactor.BindStrings(interactor.ProgIdentityI64,
		"attr:///kernel/int64/screen/width")
	screenWHandle := attr.ConstraintI64(attr.ShepherdURI("int64", "screen_w"), screenWProg)
	screenHProg := interactor.BindStrings(interactor.ProgIdentityI64,
		"attr:///kernel/int64/screen/height")
	screenHHandle := attr.ConstraintI64(attr.ShepherdURI("int64", "screen_h"), screenHProg)
	screenW := int(screenWHandle.Get())
	screenH := int(screenHHandle.Get())
	sys.UartWriteString(fmt.Sprintf("[stdio] screen: %dx%d\n", screenW, screenH))

	drawCtx := interactor.NewDrawContext(nil, 0, 0, screenW, screenH)
	fbImage := drawCtx.Image()
	ggCtx := gg.NewContextForRGBA(fbImage)
	ggCtx.SwapRB = true
	sys.UartWriteString(fmt.Sprintf("[stdio] draw context created (T+%v)\n", time.Since(startTime)))

	// 7. Initial sizing draw at small default.
	initW, initH := 800.0, 400.0
	initX := float64(screenW)/2 - initW/2
	initY := float64(screenH)/2 - initH/2
	sys.UartWriteString("[stdio] sizing draw...\n")
	app.Draw(ggCtx, initX, initY, initW, initH)

	// Read constraint-computed size.
	rawW := app.Layout.Width.Get()
	rawH := app.Layout.Height.Get()
	contentW := content.Layout.Width.Get()
	contentH := content.Layout.Height.Get()
	sys.UartWriteString(fmt.Sprintf("[stdio] raw constraint: W=%d H=%d contentW=%d contentH=%d\n", rawW, rawH, contentW, contentH))
	winW := float64(rawW)
	winH := float64(rawH)
	if winW < 100 {
		winW = initW
	}
	if winH < 50 {
		winH = initH
	}
	sys.UartWriteString(fmt.Sprintf("[stdio] constraint size: %.0fx%.0f (T+%v)\n", winW, winH, time.Since(startTime)))

	// Force Bounds evaluation for rachel.
	sys.UartWriteString(fmt.Sprintf("[stdio] evaluating Bounds... (T+%v)\n", time.Since(startTime)))
	_ = app.Layout.Bounds.Get()
	sys.UartWriteString(fmt.Sprintf("[stdio] Bounds evaluated (T+%v)\n", time.Since(startTime)))

	// 8. Rachel is already confirmed ready (step 2b). Announce to WM.
	announceToWM(rachelSID)
	sys.UartWriteString(fmt.Sprintf("[stdio] WM announced (T+%v)\n", time.Since(startTime)))

	var posXHandle, posYHandle *attr.Handle[int64]
	rachelSIDStr := strconv.Itoa(rachelSID)
	vaXURI := "attr:///shepherd/" + rachelSIDStr + "/int64/visibleArea/x"
	vaYURI := "attr:///shepherd/" + rachelSIDStr + "/int64/visibleArea/y"

	// Stdio left-aligns: X = visibleArea.x, Y = visibleArea.y.
	xProg := interactor.BindStrings(interactor.ProgIdentityI64, vaXURI)
	posXHandle = attr.ConstraintI64(attr.ShepherdURI("int64", "pos/x"), xProg)

	yProg := interactor.BindStrings(interactor.ProgIdentityI64, vaYURI)
	posYHandle = attr.ConstraintI64(attr.ShepherdURI("int64", "pos/y"), yProg)

	winX := float64(posXHandle.Get())
	winY := float64(posYHandle.Get())
	sys.UartWriteString(fmt.Sprintf("[stdio] position constraints: x=%.0f y=%.0f (T+%v)\n", winX, winY, time.Since(startTime)))

	// Clear sizing ghost and draw at final position.
	ggCtx.SetColor(pal.Surface)
	clearX0, clearY0 := initX, initY
	clearX1, clearY1 := initX+initW, initY+initH
	if winX < clearX0 {
		clearX0 = winX
	}
	if winY < clearY0 {
		clearY0 = winY
	}
	if winX+winW > clearX1 {
		clearX1 = winX + winW
	}
	if winY+winH > clearY1 {
		clearY1 = winY + winH
	}
	ggCtx.FillRectangle(clearX0, clearY0, clearX1-clearX0, clearY1-clearY0)

	app.Draw(ggCtx, winX, winY, winW, winH)
	drawCtx.Flush(int32(winX), int32(winY), int32(winX+winW), int32(winY+winH))
	sys.UartWriteString(fmt.Sprintf("[stdio] initial draw done at (%.0f,%.0f) (T+%v)\n", winX, winY, time.Since(startTime)))

	// 9. Signal readiness.
	readyHandle.Set(true)
	sys.SetReady(true)
	sys.UartWriteString(fmt.Sprintf("[stdio] Ready=true (T+%v)\n", time.Since(startTime)))

	// 10. Serial channel and delegated syscalls.
	sys.UartWriteString(fmt.Sprintf("[stdio] setting up serial + delegate channels (T+%v)\n", time.Since(startTime)))
	serialCh, err := serial.Chars()
	if err != nil {
		sys.UartWriteString(fmt.Sprintf("[stdio] serial.Chars failed: %v\n", err))
		return
	}

	delegateCh, delegateErr := sys.HandleSyscalls(sysid.Write, sysid.Openat)
	if delegateErr != nil {
		sys.UartWriteString(fmt.Sprintf("[stdio] HandleSyscalls failed: %v\n", delegateErr))
	} else {
		sys.UartWriteString("[stdio] Registered as Write+Openat handler\n")
	}

	// Delegate handler goroutine — replies immediately to unblock callers,
	// forwards text data to delegateDataCh for the main goroutine.
	var delegateDataCh <-chan delegateMsg
	if delegateErr == nil {
		delegateDataCh = startDelegateHandler(delegateCh, con.suppressSerialCopy)
	}

	// 11. Event loop — main goroutine owns console state + redraw.
	dirtyCh := attr.OnDirty()
	sys.UartWriteString("[stdio] Entering event loop\n")

	redraw := func() {
		winW = float64(app.Layout.Width.Get())
		winH = float64(app.Layout.Height.Get())
		winX = float64(posXHandle.Get())
		winY = float64(posYHandle.Get())
		app.Draw(ggCtx, winX, winY, winW, winH)
		drawCtx.Flush(int32(winX), int32(winY), int32(winX+winW), int32(winY+winH))
	}

	for {
		runtime.Gosched()

		select {
		case sb := <-serialCh:
			con.handleSerialByte(sb)
			// Drain buffered chars.
			done := false
			for !done {
				select {
				case sb = <-serialCh:
					con.handleSerialByte(sb)
				default:
					done = true
				}
			}
			redraw()

		case msg := <-delegateDataCh:
			for _, b := range msg.data {
				con.handleSerialByte(serial.SerialByte{Fd: msg.fd, B: b})
			}
			// Drain queued messages before redrawing.
			drained := false
			for !drained {
				select {
				case msg = <-delegateDataCh:
					for _, b := range msg.data {
						con.handleSerialByte(serial.SerialByte{Fd: msg.fd, B: b})
					}
				default:
					drained = true
				}
			}
			redraw()

		case <-dirtyCh:
			redraw()
		}
	}
}
