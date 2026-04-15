// mail is a standalone shepherd that provides a mancini-based mail client.
// It connects to maildb via uring IPC to fetch headers and message bodies.
//
// UI: AppWindow with no menubar. A ThrobberRadialChooser in the top-left
// corner offers font size selection (Small/Medium/Large — not hooked up yet).
// The main area is a column with a message list (headers) on top and a
// message body view below.
package main

import (
	"fmt"
	"image"
	"time"
	"unsafe"

	"golang.org/x/image/font"

	"mazzy/mazarin/attr"
	"mazzy/mazarin/fontcache"
	"mazzy/mazarin/mancini"
	"mazzy/mazarin/mancini/std"
	mctheme "mazzy/mazarin/mancini/theme"
	"mazzy/mazarin/sys"
	"mazzy/mazarin/uring"
	mfont "mazzy/shared/font"
	"mazzy/shared/ipc"
	"mazzy/shared/mail"
	"mazzy/shared/wm"
)

var (
	rachelSID int
	maildbSID int
	app       *std.AppWindow
	wmCh      = make(chan any, 128)
	mailRespCh = make(chan any, 64)
)

func startUringDispatcher(fc *fontcache.FontCache) {
	d := uring.NewDispatcher()
	d.OnFunc(ipc.ProtoShepherdNotify, wm.DecodeShepherdNotify, func(v any) {
		wmCh <- v
	})
	d.On(ipc.ProtoFontResponse, wm.DecodeFontResponse, fc.ReplyCh)
	d.OnFunc(ipc.ProtoMailResp, mail.DecodeMailResp, func(v any) {
		mailRespCh <- v
	})
	d.OnDeath(func(deadSID int16) {
		fmt.Printf("[mail] shepherd %d died\n", deadSID)
	})
	d.Start()
}

func main() {
	t0 := time.Now()
	fmt.Println("[mail] main() entered")

	// 1. Initialize constraint system.
	attr.Init()
	mancini.Init()
	fmt.Printf("[mail:timing] attr+mancini init: %v\n", time.Since(t0))

	// 2. Wait for dependencies.
	tDep := time.Now()
	if err := sys.WaitForShepherdReady("fs", 10); err != nil {
		panic(fmt.Sprintf("[mail] FATAL: fs: %v", err))
	}
	if err := sys.WaitForShepherdReady("rachel", 10); err != nil {
		panic(fmt.Sprintf("[mail] FATAL: rachel: %v", err))
	}
	if err := sys.WaitForShepherdReady("maildb", 10); err != nil {
		panic(fmt.Sprintf("[mail] FATAL: maildb: %v", err))
	}
	fmt.Printf("[mail:timing] wait deps: %v\n", time.Since(tDep))

	rachelSID = sys.MustGetShepherdByName("rachel")
	maildbSID = sys.MustGetShepherdByName("maildb")
	fc := fontcache.New(rachelSID)

	// Start uring dispatcher.
	startUringDispatcher(fc)

	// 3. Theme and palette.
	resolver := func(family string, feature mancini.Feature, size int64) font.Face {
		style := mfont.Regular
		if feature == mancini.Bold {
			style = mfont.Bold
		}
		return fc.OpenFaceByName(family, style, size)
	}
	pal := mctheme.NewDefaultPaletteSwapRB()
	neu := mctheme.NewDefaultNeumorphicParams()
	theme := mctheme.NewTheme(pal, neu, mfont.DefaultSans, 14, resolver)
	theme.SetStyle(std.NewFlatStyle(15, 1.0))

	// 4. Read screen dimensions.
	screenWProg := mancini.BindStrings(mancini.ProgIdentityI64,
		"_source_", "attr:///kernel/int64/screen/width")
	screenWAttr := attr.ConstraintI64(attr.ShepherdURI("int64", "screen_w"), screenWProg)
	screenHProg := mancini.BindStrings(mancini.ProgIdentityI64,
		"_source_", "attr:///kernel/int64/screen/height")
	screenHAttr := attr.ConstraintI64(attr.ShepherdURI("int64", "screen_h"), screenHProg)
	screenW := int(screenWAttr.Get())
	screenH := int(screenHAttr.Get())
	fmt.Printf("[mail] screen dimensions: %dx%d\n", screenW, screenH)

	// 5. Create AppWindow — no menubar.
	app = std.NewAppWindow(pal, "Mail")
	app.RachelSID = rachelSID
	app.Focused = false

	// 6. Window sizing — request 50% of screen width, full height.
	winW := screenW * 50 / 100
	winH := screenH
	appLH := app.GetLayout()
	appLH.X.Set(0)
	appLH.Y.Set(0)
	appLH.Width.Set(int64(winW))
	appLH.Height.Set(int64(winH))

	appWidthURI := appLH.Width.URI()

	// 7. Create interactor hierarchy:
	//   AppWindow
	//   └─ Column
	//      ├─ headerLabel ("Mail - message list")
	//      └─ bodyLabel ("Select a message to view")
	//   ThrobberRadialChooser (top-left corner, overlaid)

	// Column to hold the message list and body areas.
	col := std.NewColumn("mail_col", "AppWindow", pal,
		int64(winH), mancini.AxisMinimum, 4, 8, false)
	colLH := col.GetLayout()

	// Bind column width to AppWindow width.
	colWidthProg := mancini.BindStrings(mancini.ProgIdentityI64,
		"_source_", appWidthURI)
	colLH.Width = attr.ConstraintI64(
		attr.ShepherdURI("int64", "mail_col/width"), colWidthProg)

	// Header area — a simple label for now; will become a scrollable list.
	headerLabel := std.NewLabelNamed("mail_hdr", "mail_col", theme,
		"Mail - message list", 16)
	headerLH := headerLabel.GetLayout()
	headerLH.Height.Set(int64(winH / 2))
	// Bind header width to column width.
	hdrWidthProg := mancini.BindStrings(mancini.ProgIdentityI64,
		"_source_", appWidthURI)
	headerLH.Width = attr.ConstraintI64(
		attr.ShepherdURI("int64", "mail_hdr/width"), hdrWidthProg)

	// Body area — a simple label for now; will display message bodies.
	bodyLabel := std.NewLabelNamed("mail_body", "mail_col", theme,
		"Select a message to view", 14)
	bodyLH := bodyLabel.GetLayout()
	bodyLH.Height.Set(int64(winH / 2))
	bodyWidthProg := mancini.BindStrings(mancini.ProgIdentityI64,
		"_source_", appWidthURI)
	bodyLH.Width = attr.ConstraintI64(
		attr.ShepherdURI("int64", "mail_body/width"), bodyWidthProg)

	// Throbber with font size chooser (top-left corner, not hooked up yet).
	// mainDC will be set after BackingStoreReady.
	throbber := std.NewThrobberRadialChooser(
		"mail_throb", "AppWindow", theme, pal,
		rachelSID, nil, // mainDC set later
		[]string{"Small", "Medium", "Large"}, 1, // default Medium
	)
	_ = throbber // used after backing store is ready

	_ = headerLabel
	_ = bodyLabel

	// 8. Publish Ready and announce to rachel.
	_ = appLH.Bounds.Get()
	readyAttr := attr.ValueBool(wm.ReadyURI(attr.SID()), true)
	_ = readyAttr

	app.AnnounceToWM(0, 0, int32(winW), int32(winH))
	fmt.Printf("[mail:timing] announce: %v\n", time.Since(t0))

	// 9. Wait for BackingStoreReady.
	var bsr wm.BackingStoreReady
	for {
		raw := <-wmCh
		if b, ok := raw.(wm.BackingStoreReady); ok {
			bsr = b
			break
		}
	}

	// Update dimensions from rachel's actual allocation.
	appLH.Width.Set(int64(bsr.AppWidth))
	appLH.Height.Set(int64(bsr.AppHeight))
	winW = int(bsr.AppWidth)
	winH = int(bsr.AppHeight)
	fmt.Printf("[mail] backing store ready: app=%dx%d at (%d,%d)\n",
		winW, winH, bsr.AppX, bsr.AppY)

	// 10. Create DrawContext over the shared backing store.
	totalW := int(bsr.TotalWidth)
	totalH := int(bsr.TotalHeight)
	totalStride := int(bsr.TotalStride)
	bsSlice := unsafe.Slice((*byte)(unsafe.Pointer(uintptr(bsr.BackingStoreAddr))), totalStride*totalH)

	provider := fontcache.NewFontSvcGlyphProvider(fc)
	bsImg := &image.RGBA{
		Pix:    bsSlice,
		Stride: totalStride,
		Rect:   image.Rect(0, 0, totalW, totalH),
	}
	dc := mancini.NewDrawContextForImage(bsImg, provider)

	// Translate origin so (0,0) is the app area, and clip to app bounds.
	dc.Push()
	dc.Translate(float64(bsr.LeftInset), float64(bsr.TopInset))
	dc.DrawRectangle(0, 0, float64(winW), float64(winH))
	dc.Clip()

	// Set mainDC on the throbber now that DrawContext exists.
	throbber.SetMainDC(dc)

	appScreenX := int32(bsr.AppX)
	appScreenY := int32(bsr.AppY)

	// 11. Initialize input dispatch pipeline.
	disp, clickAgent, _ := app.InitInput()
	mancini.SetScreenOrigin(int64(bsr.AppX), int64(bsr.AppY))
	disp.Tag = "mail"
	disp.Debug = false

	// Wire throbber focus.
	throbber.FocusParent = nil // no focus parent for now

	// 12. Initial draw.
	appLH.X.Set(0)
	appLH.Y.Set(0)
	dc.SetColor(pal.Surface())
	dc.FillRectangle(0, 0, float64(winW), float64(winH))
	app.SetDC(dc)
	app.Draw(app, 0, 0, int64(winW), int64(winH), image.Rect(0, 0, winW, winH))
	app.SendBlit()
	fmt.Printf("[mail:timing] initial draw: %v\n", time.Since(t0))
	fmt.Printf("[mail:timing] TOTAL startup: %v\n", time.Since(t0))

	// 13. Request initial headers from maildb.
	go requestInitialHeaders()

	// 14. Main event loop.
	eagerCh := attr.OnEager()

	redraw := func(reason string) {
		x0, y0, x1, y1 := appLH.GetDamageRect()
		damage := image.Rect(int(x0), int(y0), int(x1), int(y1))
		if damage.Empty() {
			return
		}
		app.Draw(app, 0, 0, int64(winW), int64(winH), damage)
		app.SendBlit()
	}

	var clickTimer <-chan time.Time
	throbTicker := time.NewTicker(100 * time.Millisecond) // 10Hz throbber animation
	defer throbTicker.Stop()

	for {
		// Drain pending dirty events before blocking.
	drainDirty:
		for {
			select {
			case <-eagerCh:
			default:
				break drainDirty
			}
		}

		select {
		case msg := <-wmCh:
			needsRedraw := true
			switch m := msg.(type) {
			case wm.KeyboardFocusGained:
				app.Focus()
			case wm.KeyboardFocusLost:
				app.Unfocus()
			case wm.YouHaveFocus:
				app.Focus()
			case wm.YouLostFocus:
				app.Unfocus()
			case wm.MouseFocusGained, wm.MouseFocusLost:
				needsRedraw = false
			case wm.MouseMove:
				if throbber.HandleMouseMove(m, appScreenX, appScreenY) {
					needsRedraw = false
				} else {
					disp.DispatchWM(msg)
				}
			case wm.MouseRelease:
				if throbber.HandleMouseRelease(m, appScreenX, appScreenY) {
					needsRedraw = false
				} else {
					disp.DispatchWM(msg)
					clickTimer = time.After(clickAgent.ClickTimeout + 10*time.Millisecond)
				}
			case wm.OverlayReady:
				if !throbber.OverlayActive() {
					throbber.HandleOverlayReady(m)
				}
				needsRedraw = false
			case wm.OverlayReleased:
				throbber.HandleOverlayReleased(m)
				needsRedraw = false
			case wm.WindowMoved:
				appScreenX = m.AppX
				appScreenY = m.AppY
			case wm.KeyPress:
				disp.DispatchWM(msg)
			default:
				disp.DispatchWM(msg)
			}
			if clickAgent.CheckTimer() {
				redraw("click-timer")
			} else if needsRedraw {
				redraw("wm-event")
			}

		case resp := <-mailRespCh:
			handleMailResponse(resp)
			redraw("mail-resp")

		case <-clickTimer:
			clickTimer = nil
			if clickAgent.CheckTimer() {
				redraw("click-expire")
			}

		case <-eagerCh:
			redraw("eagerCh")

		case <-throbTicker.C:
			throbber.Tick()
			throbber.FullDamage()
			redraw("throbber-tick")

		case <-time.After(500 * time.Millisecond):
		}
	}
}

// requestInitialHeaders sends a GetHeaders request to maildb for today's
// date, requesting the 50 most recent messages.
func requestInitialHeaders() {
	var req mail.GetHeaders
	req.Limit = 50
	today := time.Now().UTC().Format("2006-01-02")
	copy(req.Date[:], today)

	msg := mail.EncodeGetHeaders(&req)
	if err := uring.Send(maildbSID, &msg); err != nil {
		fmt.Printf("[mail] GetHeaders send failed: %v\n", err)
	}
	fmt.Printf("[mail] sent GetHeaders date=%s limit=%d\n", today, req.Limit)
}

// handleMailResponse processes responses from maildb.
func handleMailResponse(v any) {
	switch resp := v.(type) {
	case mail.HeaderEntry:
		from, subject, _ := mail.UnpackHeaderEntry(&resp)
		fmt.Printf("[mail] header: %s — %s (body=%d bytes)\n", from, subject, resp.BodyLen)
	case mail.HeadersEnd:
		fmt.Printf("[mail] headers complete: %d messages\n", resp.Count)
	case mail.BodyResult:
		fmt.Printf("[mail] body received: %d bytes in %d pages at VA 0x%x\n",
			resp.BodyLen, resp.NumPages, resp.TargetVA)
	case mail.ErrorResult:
		errStr := string(resp.Msg[:resp.MsgLen])
		fmt.Printf("[mail] error from maildb: %s\n", errStr)
	default:
		fmt.Printf("[mail] unknown mail response: %T\n", v)
	}
}
