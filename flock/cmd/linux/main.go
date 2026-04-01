// linux is a userspace shepherd that handles Linux file syscalls (open, read,
// write, close, seek, etc.) via delegation, and owns the serial port soft IRQ
// to render kernel console output inside a mancini AppWindow with a gradient
// purple title bar. Lines are displayed as ConsoleLabel interactors inside a
// ColumnOutsideIn container.
package main

import (
	"fmt"
	"image"
	"image/color"
	"os"
	"runtime"
	"unsafe"

	"golang.org/x/image/font"

	"mazzy/mazarin/attr"
	"mazzy/mazarin/fontcache"
	mfont "mazzy/shared/font"
	"mazzy/mazarin/mancini"
	"mazzy/mazarin/mancini/std"
	mctheme "mazzy/mazarin/mancini/theme"
	"mazzy/mazarin/ringbuf"
	"mazzy/mazarin/serial"
	"mazzy/mazarin/sys"
	"mazzy/shared/fs/fsipc"
	"mazzy/shared/sysid"
	"mazzy/shared/wm"
)

const (
	maxPoolLines = 10 // pre-allocated label pool size (limited by kernel attr node slots)
	fontSize     int64 = 16
)

// Console text colors (pre-swapped for BGR framebuffer).
var (
	nContent = mancini.SwapRB(color.NRGBA{40, 42, 48, 255})    // content well background
	nText    = mancini.SwapRB(color.NRGBA{200, 205, 215, 255})  // stdout text
	nStderr  = mancini.SwapRB(color.NRGBA{200, 80, 80, 255})    // stderr text
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
	inEscape  bool // inside an ANSI escape sequence

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
		c.inEscape = false // reset escape state on newline
		if c.lineCount < maxPoolLines {
			c.lineCount++
		} else {
			c.scroll()
		}
		return
	}

	// Filter ANSI escape sequences: ESC [ ... letter
	if sb.B == 0x1B {
		c.inEscape = true
		return
	}
	if c.inEscape {
		// Consume bytes until we see a letter (the terminator).
		if (sb.B >= 'A' && sb.B <= 'Z') || (sb.B >= 'a' && sb.B <= 'z') {
			c.inEscape = false
		}
		return
	}

	// Drop other non-printable control characters.
	if sb.B < 0x20 && sb.B != '\t' {
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

// wmRb is the ring buffer for sending messages to rachel. Created once
// during announceToWM and reused for MsgBlit in the main loop.
var wmRb *ringbuf.RingBuffer

// backingStoreReadyCh delivers the BackingStoreReady message from the mailbox loop.
var backingStoreReadyCh = make(chan wm.BackingStoreReadyMsg, 1)

// sendBlit tells rachel to copy our backing store to the framebuffer.
func sendBlit() {
	if wmRb == nil {
		return
	}
	var msg wm.BlitMsg
	msg.Type = wm.MsgBlit
	msg.SID = int64(os.Getpid())
	wmRb.Push(unsafe.Pointer(&msg))
	_ = sys.MailboxSend(linuxRachelSID, wm.WMNotify, wmRb.Addr())
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

// startDelegateHandler runs a goroutine that processes delegated syscalls.
// Write to fd 1/2 (stdout/stderr) is handled as console output: echo to UART,
// reply immediately, and forward text to the main goroutine for display.
// All other syscalls (including Write to fd 3+) are routed to the syscallHandler.
func startDelegateHandler(delegateCh <-chan sys.SyscallRequest, handler *syscallHandler, suppressSerialCopy bool) <-chan delegateMsg {
	dataCh := make(chan delegateMsg, 32)
	go func() {
		for req := range delegateCh {
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

// mailboxRecvLoop receives notifications from rachel (font responses, WM messages)
// and from the fs shepherd (IPC responses).
func mailboxRecvLoop(fc *fontcache.FontCache, ipc *fsIPCClient) {
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
				case wm.MsgBackingStoreReady:
					bsr := *(*wm.BackingStoreReadyMsg)(unsafe.Pointer(&raw[0]))
					sys.UartWriteString(fmt.Sprintf("[linux:mailbox] BackingStoreReady: VA=0x%x total=%dx%d inset=(%d,%d) app=%dx%d pos=(%d,%d)\n",
						bsr.BackingStoreAddr, bsr.TotalWidth, bsr.TotalHeight,
						bsr.LeftInset, bsr.TopInset, bsr.AppWidth, bsr.AppHeight,
						bsr.AppX, bsr.AppY))
					backingStoreReadyCh <- bsr
				default:
					// Other messages — drain and ignore.
				}
			}
		case fsipc.NotifyReady:
			ipc.handleReady(notif)
		case fsipc.NotifyResponse:
			ipc.handleResponse()
		}
	}
}

// announceToWM sends AppStart to rachel with window dimensions.
func announceToWM(x, y, w, h int32) {
	if linuxRachelSID < 0 {
		return
	}
	var err error
	wmRb, err = ringbuf.New(linuxRachelSID, 0, wm.SizeWMMessage, wm.DefaultSlotCount)
	if err != nil {
		sys.UartWriteString("[linux] ring buffer creation failed: " + err.Error() + "\n")
		return
	}
	var msg wm.AppStartMsg
	msg.Type = wm.MsgAppStart
	msg.SID = int64(os.Getpid())
	msg.X = x
	msg.Y = y
	msg.Width = w
	msg.Height = h
	wmRb.Push(unsafe.Pointer(&msg))
	if err := sys.MailboxSend(linuxRachelSID, wm.WMNotify, wmRb.Addr()); err != nil {
		sys.UartWriteString("[linux] MailboxSend failed: " + err.Error() + "\n")
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
	ipcClient := newFsIPCClient(fsSID)
	go mailboxRecvLoop(fc, ipcClient)
	ipcClient.sendInit()
	fonts := &mancini.FontConfig{
		LoadFace: func(bold bool, size int64) font.Face {
			return fc.OpenFaceByName(mfont.DefaultMono, mfont.Regular, size)
		},
		FontRegular: mfont.DefaultMono,
		FontBold:    mfont.DefaultMono,
	}
	pal := mctheme.NewDefaultPaletteSwapRB()

	// Theme for ConsoleLabel (monospaced font, console colors).
	theme := mctheme.NewTheme(mctheme.NewDefaultPaletteWithColors(nContent, nText), mctheme.NewDefaultNeumorphicParams(), mfont.DefaultMono, fontSize,
		func(family string, feature mancini.Feature, size int64) font.Face {
			return fc.OpenFaceByName(family, mfont.Regular, size)
		})
	// 3. Console state.
	const maxCols = 120
	con := &console{
		maxCols:            maxCols,
		suppressSerialCopy: os.Getenv("SUPPRESS_SERIAL_STDIO_COPY") == "1",
	}

	// 4. Build UI tree: AppWindow → ColumnOutsideIn → ConsoleLabels.
	// ConsoleLabels are children of "console_col" via the constraint network.
	labels := make([]*std.ConsoleLabel, maxPoolLines)
	for i := range labels {
		idx := i
		labels[i] = std.NewConsoleLabel(fmt.Sprintf("line_%d", i), "console_col",
			theme, fontSize, nContent, maxCols)
		labels[i].TextFunc = func() string {
			if idx < con.lineCount {
				return con.lines[idx].text
			}
			return ""
		}
		labels[i].ColorFunc = func() color.NRGBA {
			if idx < con.lineCount {
				return con.lines[idx].color
			}
			return nText
		}
	}

	// Height: grows with children, clamped to [1 line .. all lines + spacing].
	lineH := fontSize
	minH := lineH
	maxH := int64(maxPoolLines)*lineH + int64(maxPoolLines-1)
	content := std.NewColumnOutsideIn("console_col", "AppWindow", nContent, minH, maxH)

	gt := std.NewGradientTitle(pal, fonts, "Serial Console", 18, 8)
	app := std.NewAppWindow(nil, pal, *mctheme.NewDefaultNeumorphicParams().Heavy(), fonts, "Serial Console", 26, 900, gt.TitleDraw)
	app.Focused = false // wait for rachel to grant focus

	// 6. Constraint-computed sizing (no framebuffer access needed).
	screenWProg := mancini.BindStrings(mancini.ProgIdentityI64,
		"_source_", "attr:///kernel/int64/screen/width")
	screenWAttr := attr.ConstraintI64(attr.ShepherdURI("int64", "screen_w"), screenWProg)
	screenHProg := mancini.BindStrings(mancini.ProgIdentityI64,
		"_source_", "attr:///kernel/int64/screen/height")
	screenHAttr := attr.ConstraintI64(attr.ShepherdURI("int64", "screen_h"), screenHProg)
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
	contentLH := content.GetLayout()
	_ = contentLH.Width.Get()
	_ = contentLH.Height.Get()

	// Force Bounds evaluation for rachel.
	_ = appLH.Bounds.Get()

	// 7. Announce to rachel with our desired position and size.
	linuxRachelSID = rachelSID
	initX := screenW/2 - winW/2
	initY := screenH/2 - winH/2
	announceToWM(int32(initX), int32(initY), int32(winW), int32(winH))

	// 8. Wait for rachel to allocate backing store and share it with us.
	sys.UartWriteString("[linux] waiting for BackingStoreReady...\n")
	bsr := <-backingStoreReadyCh

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

	// Wait for fs IPC handshake before registering as syscall handler.
	<-ipcClient.readyCh

	handler := newSyscallHandler(ipcClient)

	delegateCh, delegateErr := sys.HandleSyscalls(
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
		sys.UartWriteString(fmt.Sprintf("[linux] HandleSyscalls failed: %v\n", delegateErr))
	}

	// Delegate handler goroutine — replies immediately to unblock callers,
	// forwards text data to delegateDataCh for the main goroutine.
	var delegateDataCh <-chan delegateMsg
	if delegateErr == nil {
		delegateDataCh = startDelegateHandler(delegateCh, handler, con.suppressSerialCopy)
	}

	// 10. Event loop — main goroutine owns console state + redraw.
	// Signal readiness AFTER delegate handler is running, so other shepherds
	// that wait on our Ready can immediately send us delegates.
	dirtyCh := attr.OnDirty()

	readyAttr.Set(true)
	sys.SetReady(true)
	sys.UartWriteString("[linux] Ready=true\n")
	sys.UartWriteString("[linux] Entering event loop\n")

	// textDirty tracks whether console state changed since last redraw.
	// Serial bytes and delegate writes set this flag; the constraint dirty
	// tick (~10Hz) triggers the actual redraw, batching many updates into
	// one draw+blit cycle to avoid flashing.
	textDirty := false

	redraw := func() {
		app.Draw(app, 0, 0, int64(winW), int64(winH))
		sendBlit()
	}

	// drainSerial consumes all buffered serial bytes without redrawing.
	drainSerial := func() {
		for {
			select {
			case sb := <-serialCh:
				con.handleSerialByte(sb)
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
					con.handleSerialByte(serial.SerialByte{Fd: msg.fd, B: b})
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
			con.handleSerialByte(sb)
			textDirty = true
			drainSerial()

		case msg := <-delegateDataCh:
			for _, b := range msg.data {
				con.handleSerialByte(serial.SerialByte{Fd: msg.fd, B: b})
			}
			textDirty = true
			drainDelegates()

		case <-dirtyCh:
			// Drain any pending text before redrawing.
			drainSerial()
			drainDelegates()
			if textDirty {
				textDirty = false
				redraw()
			}
		}
	}
}
