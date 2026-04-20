// mail is a standalone shepherd that provides a mancini-based mail client.
// It connects to maildb via uring IPC using the v2 mailproto HOT-collection
// protocol. On startup it creates a FilterAll collection and populates the
// grid with MailRow interactors that load their headers asynchronously.
//
// UI hierarchy:
//
//	AppWindow
//	└─ C1 (ColumnPercentage [30%, 10%, -1])
//	   ├─ G  (GridFrame — Sender | Subject | Date)
//	   ├─ SC (Panel — small controls, throbber at far right)
//	   └─ T  (Panel — message body placeholder)
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
	"mazzy/shared/mailproto"
	"mazzy/shared/wm"
)

var (
	rachelSID  int
	maildbSID  int
	app        *std.AppWindow
	wmCh       = make(chan any, 128)
	mailRespCh = make(chan any, 64)

	// Grid frame — set in main, used by response handlers.
	gridFrame *std.GridFrame

	// Active collection state.
	activeCollId uint32
	mailRows     []*MailRow
	rowByReqId   = make(map[[16]byte]*MailRow)
	reqCounter   uint64

)

// nextReqId returns a new unique [16]byte request ID using a counter + timestamp.
func nextReqId() [16]byte {
	reqCounter++
	var id [16]byte
	n := uint64(time.Now().UnixNano())
	*(*uint64)(unsafe.Pointer(&id[0])) = n
	*(*uint64)(unsafe.Pointer(&id[8])) = reqCounter
	return id
}

func startUringDispatcher(fc *fontcache.FontCache) {
	d := uring.NewDispatcher()
	d.OnFunc(ipc.ProtoShepherdNotify, wm.DecodeShepherdNotify, func(v any) {
		wmCh <- v
	})
	d.On(ipc.ProtoFontResponse, wm.DecodeFontResponse, fc.ReplyCh)
	d.OnFunc(ipc.ProtoMailResp, mailproto.DecodeMailResp, func(v any) {
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
	if err := sys.WaitForShepherdReady("maildb", 75); err != nil {
		fmt.Printf("[mail] WARNING: maildb not ready (%v), continuing without mail data\n", err)
		maildbSID = -1
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

	// G — GridFrame wraps the grid, its NeuBox, and column Divider markers.
	bezelH := int64(32)
	gridFrame = std.NewGridFrame("mail_grid", "mail_c1", pal, theme,
		bezelH, chooser.ValueURI(),
		[]string{"Sender", "Subject", "Date"},
		[]float64{35, -1, 10},
		[]string{senderPct.URI(), datePct.URI()},
		[]*attr.Attribute[int64]{senderPct, datePct})

	// SC band — MarginParent (4px on all sides) child of C1 (gets 3%),
	// with the SC Panel as its single child. Chooser is parented to mail_sc.
	scPal := mctheme.NewDefaultPaletteWithColors(
		color.NRGBA{R: 232, G: 230, B: 244, A: 255},
		mancini.SwapRB(color.NRGBA{R: 30, G: 30, B: 30, A: 255}))
	scMargin := std.NewMarginParent("mail_sc_margin", "mail_c1",
		4, 4, 4, 4, 0, "", theme, pal)
	_ = scMargin
	sc := std.NewPanel("mail_sc", "mail_sc_margin", scPal, int64(winW), 0)
	_ = sc

	// T — Panel placeholder for message body, child of C1.
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
	chooserLH := chooser.GetLayout()
	scInnerY := int64(winH)*30/100 + 4
	scInnerH := int64(winH)*3/100 - 8
	chooserLH.X.Set(int64(winW) - 32)
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

	// 13. Request initial mail collection from maildb.
	go requestCreateCollection()

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
	throbTicker := time.NewTicker(100 * time.Millisecond)
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

// requestCreateCollection sends CreateCollection(FilterAll, SortDesc) to maildb.
func requestCreateCollection() {
	if maildbSID <= 0 {
		fmt.Println("[mail] maildb not available, skipping CreateCollection")
		return
	}
	reqId := nextReqId()
	req := mailproto.CreateCollectionReq{
		RequestId:  reqId,
		FilterType: mailproto.FilterAll,
		SortOrder:  mailproto.SortDesc,
	}
	msg := mailproto.EncodeCreateCollection(&req)
	if err := uring.Send(maildbSID, &msg); err != nil {
		fmt.Printf("[mail] CreateCollection send failed: %v\n", err)
	}
	fmt.Println("[mail] sent CreateCollection(FilterAll, SortDesc)")
}

// handleMailResponse dispatches a decoded mailproto message to the right handler.
func handleMailResponse(v any) {
	switch resp := v.(type) {
	case mailproto.RespCreateCollection:
		handleCreateCollectionResp(&resp)
	case mailproto.RespKeyHeaders:
		handleKeyHeadersResp(&resp)
	case mailproto.CollectionAdd:
		handleCollectionAdd(&resp)
	case mailproto.CollectionRemove:
		handleCollectionRemove(&resp)
	case mailproto.RespMarkRead:
		fmt.Printf("[mail] MarkRead ack: errCode=%d\n", resp.ErrCode)
	case mailproto.RespMarkDeleted:
		fmt.Printf("[mail] MarkDeleted ack: errCode=%d newSize=%d\n", resp.ErrCode, resp.NewSize)
	default:
		fmt.Printf("[mail] unhandled mail response: %T\n", v)
	}
}

// handleCreateCollectionResp receives the RespCreateCollection and populates
// the grid with MailRow instances for the first 50 messages.
func handleCreateCollectionResp(resp *mailproto.RespCreateCollection) {
	if resp.ErrCode != mailproto.ErrNone {
		fmt.Printf("[mail] CreateCollection failed: errCode=%d\n", resp.ErrCode)
		return
	}
	activeCollId = resp.CollId
	fmt.Printf("[mail] collection created: collId=%d size=%d\n", resp.CollId, resp.Size)

	limit := int(resp.Size)
	if limit > 50 {
		limit = 50
	}
	for i := 0; i < limit; i++ {
		reqId := nextReqId()
		row := NewMailRow(maildbSID, activeCollId, uint32(i), reqId,
			onCollectionExpired, onRowSelected)
		mailRows = append(mailRows, row)
		rowByReqId[reqId] = row
		row.OnLoaded = gridFrame.AddRow(row)
	}
	fmt.Printf("[mail] added %d MailRows to grid\n", limit)
}

// handleKeyHeadersResp routes a RespKeyHeaders to the matching MailRow and
// marks the grid dirty so the new text is displayed on the next draw.
func handleKeyHeadersResp(resp *mailproto.RespKeyHeaders) {
	row, ok := rowByReqId[resp.RequestId]
	if !ok {
		fmt.Printf("[mail] RespKeyHeaders: no matching row for requestId\n")
		return
	}
	delete(rowByReqId, resp.RequestId)
	row.HandleKeyHeadersResp(resp) // fires row.OnLoaded → label FullDamage if successful
	fmt.Printf("[mail] KeyHeaders loaded msgNum=%d sender=%q\n", row.MsgNum(), row.Sender())
}

// handleCollectionAdd handles an unsolicited CollectionAdd notification.
func handleCollectionAdd(notif *mailproto.CollectionAdd) {
	if notif.CollId != activeCollId {
		return
	}
	fmt.Printf("[mail] CollectionAdd: msgNum=%d newSize=%d\n", notif.MsgNum, notif.NewSize)
	if len(mailRows) < 50 {
		reqId := nextReqId()
		row := NewMailRow(maildbSID, activeCollId, notif.MsgNum, reqId,
			onCollectionExpired, onRowSelected)
		mailRows = append(mailRows, row)
		rowByReqId[reqId] = row
		row.OnLoaded = gridFrame.AddRow(row)
	}
}

// handleCollectionRemove handles an unsolicited CollectionRemove notification.
// The row is removed from the app tracking list. Visual removal from GridTable
// is deferred until GridTable supports row removal.
func handleCollectionRemove(notif *mailproto.CollectionRemove) {
	if notif.CollId != activeCollId {
		return
	}
	fmt.Printf("[mail] CollectionRemove: msgNum=%d newSize=%d\n", notif.MsgNum, notif.NewSize)
	for i, row := range mailRows {
		if row.MsgNum() == notif.MsgNum {
			delete(rowByReqId, row.RequestId())
			mailRows = append(mailRows[:i], mailRows[i+1:]...)
			break
		}
	}
}

// onCollectionExpired is called by a MailRow when ErrCollectionExpired arrives.
// It rebuilds the collection from scratch.
func onCollectionExpired(collId uint32) {
	fmt.Printf("[mail] collection %d expired, re-requesting\n", collId)
	mailRows = mailRows[:0]
	rowByReqId = make(map[[16]byte]*MailRow)
	go requestCreateCollection()
}

// onRowSelected is called by a MailRow when the user selects it.
func onRowSelected(collId, msgNum uint32) {
	fmt.Printf("[mail] row selected: collId=%d msgNum=%d\n", collId, msgNum)
}
