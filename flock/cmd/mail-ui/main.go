// mail-ui is a .maz module loaded by the maildb shepherd that provides
// the mancini UI for displaying mail database query results.
//
// Interactor hierarchy:
//   AppWindow
//     Column (width = max(320, parent), height = max(240, parent))
//       ScrollerVertical (scroller + scrollbar)
//         Console (80 cols x 200 rows, scrollable)
//
// From the kernel's perspective, mail-ui IS the maildb shepherd (same PID/SID).
package main

import (
	"fmt"

	"mazzy/mazarin/attr"
	"mazzy/mazarin/maildbio"
	"mazzy/mazarin/mancini"
	"mazzy/mazarin/mancini/linuxapp"
	"mazzy/mazarin/mancini/std"
	"mazzy/flock/cmd/maildb/shared"
)

const (
	consoleCols = 80
	consoleRows = 200
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

// inj holds the injection result from MazarinShepherd.
var inj *linuxapp.Injection[maildbio.MailDBIO]

// MazarinShepherd receives the MailDBIO injection from the maildb shepherd.
//
//go:noinline
func MazarinShepherd(injected interface{}) error {
	var err error
	inj, err = linuxapp.HandleInjection[maildbio.MailDBIO](injected, func(io maildbio.MailDBIO) linuxapp.SetupResult {
		wmRawCh := make(chan []byte, 8)
		fontRawCh := make(chan []byte, 8)
		io.SetChannels(
			make(chan string, 16),
			make(chan shared.Response, 16),
			wmRawCh,
			fontRawCh,
		)
		return linuxapp.SetupResult{
			RachelSID: io.GetRachelSID(),
			WMRawCh:   wmRawCh,
			FontRawCh: fontRawCh,
		}
	})
	return err
}

// MazarinMain is the .maz entry point.
//
//go:noinline
func MazarinMain() {
	rawPuts("[mail-ui] MazarinMain() entered\n")

	if inj == nil {
		rawPuts("[mail-ui] FATAL: inj is nil (MazarinShepherd not called?)\n")
		return
	}

	linuxapp.Bootstrap(inj, linuxapp.AppConfig[maildbio.MailDBIO]{
		Title:   "MailDB",
		WinW:    800,
		WinH:    600,
		BuildUI: buildUI,
	})
}

// buildUI creates the mail UI interactor tree and returns a drain function
// that processes ResponseChannel messages.
func buildUI(a *linuxapp.App[maildbio.MailDBIO]) linuxapp.DrainFunc {
	fonts := a.Fonts
	pal := a.Pal
	fontSize := a.FontSize

	// Column: width = max(320, AppWindow width), height = max(240, AppWindow height).
	appWURI := mancini.LayoutURI("AppWindow", mancini.DataTypeInt64, mancini.LayoutWidth)
	appHURI := mancini.LayoutURI("AppWindow", mancini.DataTypeInt64, mancini.LayoutHeight)

	colLH := mancini.NewLayoutAttributesBase("column", "AppWindow")
	colLH.Width = attr.ConstraintI64(
		mancini.LayoutURI("column", mancini.DataTypeInt64, mancini.LayoutWidth),
		mancini.MaxI64(appWURI, 320))
	colLH.Height = attr.ConstraintI64(
		mancini.LayoutURI("column", mancini.DataTypeInt64, mancini.LayoutHeight),
		mancini.MaxI64(appHURI, 240))
	colLH.InitBounds("column")
	col := std.NewColumnWithLayout(colLH, pal, mancini.AxisMinimum, 0, 1, false)
	_ = col

	// ScrollerVertical: width/height constrained to column.
	colWidthURI := mancini.LayoutURI("column", mancini.DataTypeInt64, mancini.LayoutWidth)
	colHeightURI := mancini.LayoutURI("column", mancini.DataTypeInt64, mancini.LayoutHeight)
	const scrollbarTrackWidth = int64(16)

	sv := std.NewScrollerVertical("sv", "column",
		a.Theme, pal,
		colWidthURI, colHeightURI,
		scrollbarTrackWidth,
		std.ScrollbarLight)

	// Console: parent is the scroller, width constrained to scroller width.
	console := std.NewConsole("console", sv.ScrollerName(), pal, fonts, fontSize,
		consoleCols, consoleRows)

	// The scroller's VirtualWidth/VirtualHeight are driven by the console's
	// natural dimensions (set internally by NewScrollerVertical + Console).

	// Initialize input dispatch (for future keyboard/mouse handling).
	a.AppWindow.InitInput()

	// Response drainer: reads responses and displays results in console.
	respCh := a.Injected.ResponseChannel()
	statusCh := a.Injected.StatusChannel()
	var pending []<-chan shared.MailMessage

	return func() bool {
		dirty := false

		// Drain status messages from the shepherd (import progress, etc.).
		for {
			select {
			case s := <-statusCh:
				console.AddLine(s, pal.AnsiColor(6)) // cyan
				dirty = true
			default:
				goto drainResponses
			}
		}

	drainResponses:
		// Check for new responses.
		for {
			select {
			case resp := <-respCh:
				if resp.Error != "" {
					console.AddLine("ERROR: "+resp.Error, pal.AnsiColor(1))
					dirty = true
				} else if resp.Results != nil {
					pending = append(pending, resp.Results)
				}
			default:
				goto drainResults
			}
		}

	drainResults:
		// Drain pending result channels.
		still := pending[:0]
		for _, ch := range pending {
			for {
				select {
				case msg, ok := <-ch:
					if !ok {
						goto nextPending
					}
					line := fmt.Sprintf("%s  %s  %s", msg.From, msg.Subject, msg.MessageId)
					console.AddLine(line, pal.Text())
					dirty = true
				default:
					still = append(still, ch)
					goto nextPending
				}
			}
		nextPending:
		}
		pending = still

		if dirty {
			sv.Scroller.MarkContentDirty()
		}

		return dirty
	}
}

func rawPuts(s string) {
	fmt.Print(s)
}

func main() { MazarinMain() }
