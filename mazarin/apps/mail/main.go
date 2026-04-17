// mail is a standalone shepherd that provides a mancini-based mail client.
// It connects to maildb via uring IPC to fetch headers and message bodies.
//
// UI hierarchy:
//   AppWindow
//   └─ C1 (ColumnPercentage [30%, 10%, -1])
//      ├─ G  (GridTable — Sender | Subject | Date)
//      ├─ SC (Panel — small controls, throbber at far right)
//      └─ T  (WebInteractor — message body placeholder)
package main

import (
	"fmt"
	"image"
	"image/color"
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
	rachelSID  int
	maildbSID  int
	app        *std.AppWindow
	wmCh       = make(chan any, 128)
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

	// 2. Wait for core services, then maildb.
	tDep := time.Now()
	if err := sys.WaitForCoreServices(20); err != nil {
		panic(fmt.Sprintf("[mail] FATAL: core services: %v", err))
	}
	if err := sys.WaitForShepherdReady("maildb", 20); err != nil {
		panic(fmt.Sprintf("[mail] FATAL: maildb: %v", err))
	}
	scratch, err := sys.SetupScratchDir(true)
	if err != nil {
		panic(fmt.Sprintf("[mail] FATAL: scratchdir: %v", err))
	}
	fmt.Printf("[mail] scratch dir: %s\n", scratch)
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

	// 7. Create interactor hierarchy.
	// Font sizes for the grid: Small=12, Medium=16, Large=20.
	gridFontSizes := []int64{12, 16, 20}
	initialFontIdx := 0 // Small

	// C1 — ColumnPercentage [30%, 3%, -1] child of AppWindow.
	c1 := std.NewColumnPercentage("mail_c1", "AppWindow", pal,
		int64(winW), int64(winH), []float64{30, 3, -1})
	_ = c1

	// Create chooser first (with future parent name) to get ValueURI,
	// then register C1 children in correct order: grid(30%), SC margin(3%), T(-1).
	chooser := std.NewCornerRadialChooser(
		"mail_chooser", "mail_sc", theme, pal,
		rachelSID,
		[]string{"Small", "Medium", "Large"}, gridFontSizes, initialFontIdx,
	)
	chooser.OnSelect = func(idx int) {
		fmt.Printf("[mail] grid font size changed to %d\n", gridFontSizes[idx])
	}

	// Column split attributes — Sender gets 35%, Date gets 10%, Subject gets remainder.
	senderPct := attr.ValueI64(mancini.LayoutURI("mail_grid", mancini.DataTypeInt64,
		mancini.LayoutProp("col/sender_pct")), 35)
	datePct := attr.ValueI64(mancini.LayoutURI("mail_grid", mancini.DataTypeInt64,
		mancini.LayoutProp("col/date_pct")), 10)

	// G — wrapped in a Raised LightWeight NeuBox for visual depth.
	// A MarginParent insets the NeuBox by the shadow-pad amount on the
	// sides so the soft shadows have room to render. Top and bottom
	// margins are larger to provide a "bezel" area where Divider markers
	// can overhang the grid edge.
	gridPad := int64(theme.Style().Pad(mancini.LightWeight))
	bezelH := int64(4) // matches Divider.Overhang; room for triangle marker
	gridMargin := std.NewMarginParent("mail_grid_margin", "mail_c1",
		bezelH, gridPad, bezelH, gridPad, 0, "", theme, pal)
	_ = gridMargin

	gridBoxLH := mancini.NewLayoutAttributes("mail_grid_box", "mail_grid_margin")
	gridBox := std.NewNeuBoxStyled(gridBoxLH, theme,
		mancini.Raised, mancini.LightWeight, 8)
	_ = gridBox

	grid := std.NewGridTable("mail_grid", "mail_grid_box", pal, theme,
		chooser.ValueURI(),
		[]string{"Sender", "Subject", "Date"},
		[]float64{35, -1, 10},
		[]string{senderPct.URI(), datePct.URI()},
		[]*attr.Attribute[int64]{senderPct, datePct})

	// SC band — MarginParent (4px on all sides) child of C1 (gets 3%),
	// with the SC Panel as its single child. Chooser is parented to mail_sc.
	// Use the soft lavender/salmon tint (the color the WebInteractor used
	// for its empty-HTML background) so the controls strip stands out.
	// NOTE: passed without SwapRB to preserve the visual seen previously.
	scPal := mctheme.NewDefaultPaletteWithColors(
		color.NRGBA{R: 232, G: 230, B: 244, A: 255},
		mancini.SwapRB(color.NRGBA{R: 30, G: 30, B: 30, A: 255}))
	scMargin := std.NewMarginParent("mail_sc_margin", "mail_c1",
		4, 4, 4, 4, 0, "", theme, pal)
	_ = scMargin
	sc := std.NewPanel("mail_sc", "mail_sc_margin", scPal, int64(winW), 0)
	_ = sc

	// Populate with test data.
	for _, row := range testMailRows() {
		grid.AddRow(row)
	}
	_ = grid

	// T — Panel placeholder for message body, child of C1. Uses the
	// shared pal so the background matches the grid above. Will be
	// swapped for a WebInteractor once a render engine is wired in.
	bodyPanel := std.NewPanel("mail_web", "mail_c1", pal, int64(winW), 0)
	_ = bodyPanel

	// 8. Publish Ready and announce to rachel.
	_ = appLH.Bounds.Get()
	readyAttr := attr.ValueBool(wm.ReadyURI(attr.SID()), true)
	_ = readyAttr

	app.AnnounceToWM(1000, 0, int32(winW), int32(winH))
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

	// Update C1 dimensions to match actual window size.
	c1LH := c1.GetLayout()
	c1LH.Width.Set(int64(winW))
	c1LH.Height.Set(int64(winH))

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

	// Tell the chooser where the app is on screen and how big the screen is.
	chooser.SetAppScreenPos(int32(bsr.AppX), int32(bsr.AppY))
	chooser.SetScreenSize(int32(screenW), int32(screenH))

	_ = bsr.AppX // screen position tracked by chooser

	// 11. Initialize input dispatch pipeline.
	disp, clickAgent, _ := app.InitInput()
	mancini.SetScreenOrigin(int64(bsr.AppX), int64(bsr.AppY))
	disp.Tag = "mail"
	disp.Debug = true

	// Position chooser at far right of SC panel.
	// SC band is 3% of winH starting at 30% of winH, inset by 4px on all
	// sides by MarginParent. chooser is 28x28, centered vertically in
	// SC's inner area, 4px from SC's right edge (matching MarginParent).
	chooserLH := chooser.GetLayout()
	scInnerY := int64(winH)*30/100 + 4
	scInnerH := int64(winH)*3/100 - 8
	chooserLH.X.Set(int64(winW) - 32) // 4px right margin + 28px chooser width
	chooserLH.Y.Set(scInnerY + (scInnerH-28)/2)
	chooserLH.Width.Set(28)
	chooserLH.Height.Set(28)

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
			case wm.MousePress:
				fmt.Printf("[mail:click] background X=%d Y=%d Button=%d (app pos %d,%d size %dx%d)\n",
					m.X, m.Y, m.Button, bsr.AppX, bsr.AppY, winW, winH)
				disp.DispatchWM(msg)
			case wm.MouseMove, wm.MouseRelease:
				disp.DispatchWM(msg)
			case wm.OverlayReady:
				if !chooser.OverlayActive() {
					chooser.HandleOverlayReady(m)
				}
				needsRedraw = false
			case wm.OverlayReleased:
				chooser.HandleOverlayReleased(m)
				needsRedraw = false
			case wm.WindowMoved:
				chooser.SetAppScreenPos(m.AppX, m.AppY)
			default:
				disp.DispatchWM(msg)
			}
			if needsRedraw {
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
			chooser.Tick()
			chooser.FullDamage()
			redraw("chooser-tick")

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

// testRow implements std.GridRow for static test data.
type testRow struct {
	sender  string
	subject string
	date    string
}

func (r testRow) Sender() string  { return r.sender }
func (r testRow) Subject() string { return r.subject }
func (r testRow) Date() string    { return r.date }

func testMailRows() []testRow {
	return []testRow{
		{"Alice <alice@example.com>", "Meeting notes from Monday", "Apr 14"},
		{"Bob Smith <bob@corp.net>", "Q2 budget proposal attached", "Apr 13"},
		{"GitHub <noreply@github.com>", "[mazarin] PR #42 merged", "Apr 12"},
		{"Carol <carol@univ.edu>", "Re: constraint solver bug", "Apr 11"},
		{"Dave's Bakery <news@daves.com>", "Your order is ready for pickup", "Apr 10"},
		{"Eve <eve@security.io>", "TLS certificate expiring soon", "Apr 09"},
	}
}
