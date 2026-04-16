// linuxapp provides a reusable framework for .maz UI modules that display
// a mancini window. It handles the common bootstrap ceremony: attr/mancini
// init, font cache creation, theme setup, AppWindow creation, WM handshake,
// backing store mapping, DrawContext creation, and the event loop.
//
// The framework is parameterized by the injection type T so different
// applications can use different injection interfaces without the framework
// depending on any specific one.
//
// The caller supplies an [AppConfig] describing the window and a BuildUI
// callback that creates the app-specific interactor tree. The event loop
// calls the returned [DrainFunc] to process app-specific channel traffic.
package linuxapp

import (
	"fmt"
	"image"
	"runtime"
	"unsafe"

	"golang.org/x/image/font"

	"mazzy/mazarin/attr"
	"mazzy/mazarin/fontcache"
	"mazzy/mazarin/mancini"
	"mazzy/mazarin/mancini/std"
	mctheme "mazzy/mazarin/mancini/theme"
	"mazzy/mazarin/sys"
	mfont "mazzy/shared/font"
	"mazzy/shared/wm"
)

// AppConfig describes the application to be bootstrapped.
type AppConfig[T any] struct {
	// Title is the window title shown by rachel.
	Title string

	// WinW, WinH are the requested initial window dimensions.
	// Rachel may adjust these in the BackingStoreReady response.
	WinW, WinH int

	// Place computes the requested screen position from screen and window
	// dimensions. If nil, the window is centered on screen.
	Place func(screenW, screenH, winW, winH int) (x, y int)

	// BuildUI is called after the theme and AppWindow are created but
	// before the WM handshake. It receives the fully configured App
	// (carrying the typed injection value) and must create the interactor
	// tree and return a BuildResult containing the DrainFunc and an
	// optional notification channel.
	//
	// The DrainFunc is called from the event loop whenever channel
	// traffic arrives or constraint attributes change. It should
	// drain app-specific channels (non-blocking) and return true
	// if a redraw is needed.
	BuildUI func(a *App[T]) BuildResult
}

// DrainFunc is called by the event loop to process app-specific messages.
// It must be non-blocking (use select/default). Return true if the
// display needs redrawing.
type DrainFunc func() bool

// BuildResult is returned by BuildUI to provide both the drain function
// and an optional notification channel. When NotifyCh is non-nil, the
// event loop includes it in its select so that app-driven messages
// (e.g. status updates from a background goroutine) wake the loop.
type BuildResult struct {
	Drain    DrainFunc
	NotifyCh <-chan struct{}
}

// App holds all the framework state created during bootstrap. BuildUI
// receives this and uses it to create interactors. The Injected field
// carries the app-specific typed injection value.
type App[T any] struct {
	// Injected is the app-specific injection value, typed by the caller.
	// For a console app this would be linuxio.LinuxIO; for a SQLite app
	// it would be a different interface.
	Injected T

	FC        *fontcache.FontCache
	Fonts     *mancini.FontConfig
	Pal       mancini.Palette
	Theme     mancini.Theme
	AppWindow *std.AppWindow
	FontSize  int64

	// RachelSID is the shepherd ID of rachel (window manager).
	RachelSID int

	// WMDecodedCh carries decoded WM messages in the .maz type namespace.
	WMDecodedCh chan any
}

// Bootstrap performs the full .maz UI setup: framework init, theme
// creation, AppWindow, WM handshake, backing store, and enters the
// event loop (never returns).
//
// inj is the Injection returned by [HandleInjection].
// cfg describes the application.
func Bootstrap[T any](inj *Injection[T], cfg AppConfig[T]) {
	rawPuts(fmt.Sprintf("[linuxapp] Bootstrap entered: %s\n", cfg.Title))

	rachelSID := inj.RachelSID

	// Initialize constraint system.
	attr.Init()
	mancini.Init()

	// Font config using the caller's FontCache.
	fc := inj.FC
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

	rawPuts(fmt.Sprintf("[linuxapp] TextSize=%d\n", fontSize))

	// Build Theme.
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

	// Create AppWindow.
	appWin := std.NewAppWindow(pal, cfg.Title)
	appWin.Focused = false
	appWin.RachelSID = rachelSID

	app := &App[T]{
		Injected:    inj.Val,
		FC:          fc,
		Fonts:       fonts,
		Pal:         pal,
		Theme:       theme,
		AppWindow:   appWin,
		FontSize:    fontSize,
		RachelSID:   rachelSID,
		WMDecodedCh: inj.WMDecodedCh,
	}

	// Let the caller build the interactor tree.
	br := cfg.BuildUI(app)
	drain := br.Drain

	// Screen size attributes.
	screenWURI := "attr:///kernel/int64/screen/width"
	screenHURI := "attr:///kernel/int64/screen/height"
	screenWProg := mancini.BindStrings(mancini.ProgIdentityI64,
		"_source_", screenWURI)
	screenWAttr := attr.ConstraintI64(attr.ShepherdURI("int64", "screen_w"), screenWProg)
	screenHProg := mancini.BindStrings(mancini.ProgIdentityI64,
		"_source_", screenHURI)
	screenHAttr := attr.ConstraintI64(attr.ShepherdURI("int64", "screen_h"), screenHProg)

	// Force Bounds evaluation and publish Ready.
	appLH := appWin.GetLayout()
	appLH.X.Set(0)
	appLH.Y.Set(0)
	_ = appLH.Bounds.Get()
	readyAttr := attr.ValueBool(wm.ReadyURI(attr.SID()), true)
	_ = readyAttr

	winW := cfg.WinW
	winH := cfg.WinH
	if winW <= 0 {
		winW = 800
	}
	if winH <= 0 {
		winH = 400
	}

	screenW := int(screenWAttr.Get())
	screenH := int(screenHAttr.Get())
	var initX, initY int
	if cfg.Place != nil {
		initX, initY = cfg.Place(screenW, screenH, winW, winH)
	} else {
		initX = screenW/2 - winW/2
		initY = screenH/2 - winH/2
	}
	appWin.AnnounceToWM(int32(initX), int32(initY), int32(winW), int32(winH))

	// Wait for BackingStoreReady from rachel.
	rawPuts("[linuxapp] waiting for BackingStoreReady...\n")
	wmCh := inj.WMDecodedCh
	var bsr wm.BackingStoreReady
	for {
		raw := <-wmCh
		if b, ok := raw.(wm.BackingStoreReady); ok {
			bsr = b
			break
		}
	}

	// Use actual dimensions from rachel.
	winW = int(bsr.AppWidth)
	winH = int(bsr.AppHeight)
	if winW < 100 {
		winW = 800
	}
	if winH < 50 {
		winH = 400
	}
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

	// Create DrawContext.
	provider := fontcache.NewFontSvcGlyphProvider(fc)
	dc := mancini.NewDrawContextForImage(bsImg, provider)

	leftInset := float64(bsr.LeftInset)
	topInset := float64(bsr.TopInset)
	dc.Push()
	dc.Translate(leftInset, topInset)
	dc.DrawRectangle(0, 0, float64(winW), float64(winH))
	dc.Clip()

	rawPuts(fmt.Sprintf("[linuxapp] backing store ready: total=%dx%d inset=(%d,%d) app=%dx%d\n",
		totalW, totalH, bsr.LeftInset, bsr.TopInset, winW, winH))

	// Initial draw.
	appWin.SetDC(dc)
	dc.SetColor(pal.Surface())
	dc.FillRectangle(0, 0, float64(winW), float64(winH))

	appWin.Draw(appWin, 0, 0, int64(winW), int64(winH), image.Rect(0, 0, winW, winH))
	appWin.SendBlit()

	// Enter event loop.
	runLoop(appWin, wmCh, drain, br.NotifyCh, winW, winH)
}

// runLoop is the main event loop. It processes WM messages, constraint
// change notifications, app notifications, and calls drain for app-specific traffic.
func runLoop(appWin *std.AppWindow, wmCh chan any, drain DrainFunc, notifyCh <-chan struct{}, winW, winH int) {
	eagerCh := attr.OnEager()
	appLH := appWin.GetLayout()

	redraw := func() {
		appWin.Draw(appWin, 0, 0, int64(winW), int64(winH), image.Rect(0, 0, winW, winH))
		appWin.SendBlit()
	}

	drainAndRedraw := func() {
		if drain() {
			redraw()
		}
	}

	var dirtyTicks int64

	// If no notify channel, use a nil channel (never fires, costs nothing in select).
	if notifyCh == nil {
		notifyCh = make(chan struct{})
	}

	for {
		runtime.Gosched()

		dirty := false

		select {
		case wmMsg := <-wmCh:
			switch wmMsg.(type) {
			case wm.KeyboardFocusGained:
				appWin.Focus()
				dirty = true
			case wm.KeyboardFocusLost:
				appWin.Unfocus()
				dirty = true
			}
			if appWin.DispatchAnimation(wmMsg) {
				if appLH.HasDamage() {
					dirty = true
				}
			}
			if appWin.Input != nil {
				appWin.Input.DispatchWM(wmMsg)
				if appLH.HasDamage() {
					dirty = true
				}
			}
			// Also drain app messages while we're here.
			if drain() {
				dirty = true
			}
			if dirty {
				redraw()
			}

		case <-eagerCh:
			dirtyTicks++
			if drain() {
				dirty = true
			}
			if dirty {
				redraw()
			}
			if dirtyTicks%10 == 0 {
				rawPuts(fmt.Sprintf("[linuxapp] dirtyTicks=%d\n", dirtyTicks))
			}

		case <-notifyCh:
			drainAndRedraw()
		}
	}
}

// rawPuts writes directly to UART, bypassing the delegation system.
func rawPuts(s string) {
	sys.UartWriteString(s)
}
