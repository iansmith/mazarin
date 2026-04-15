// mail-ui is a .maz module loaded by the maildb shepherd that provides
// the mancini UI for displaying mail headers in a scrollable list.
//
// Interactor hierarchy:
//   AppWindow
//     ScrollerVertical (scroller + scrollbar)
//       Label row 0 (From | Date | Subject)
//       Label row 1 ...
//       ...
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
	rowHeight  int64 = 20 // pixels per message row
	trackWidth int64 = 16 // scrollbar width
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

// messageRow tracks one label row in the message list.
type messageRow struct {
	label *std.Label
	msg   shared.MailMessage
}

// buildUI creates the mail UI interactor tree and returns a drain function
// that processes ResponseChannel messages.
func buildUI(a *linuxapp.App[maildbio.MailDBIO]) linuxapp.BuildResult {
	theme := a.Theme
	pal := a.Pal
	fontSize := a.FontSize

	// ScrollerVertical fills the AppWindow.
	appWURI := mancini.LayoutURI("AppWindow", mancini.DataTypeInt64, mancini.LayoutWidth)
	appHURI := mancini.LayoutURI("AppWindow", mancini.DataTypeInt64, mancini.LayoutHeight)

	sv := std.NewScrollerVertical("msglist", "AppWindow",
		theme, pal, appWURI, appHURI,
		trackWidth, std.ScrollbarStandard)

	scrollerParent := sv.ScrollerName()
	scrollerWidthURI := sv.ScrollerWidthURI()

	// Initialize input dispatch.
	a.AppWindow.InitInput()

	// State for dynamic row creation.
	var rows []messageRow
	importDone := false
	listRequested := false

	// Channels from injection.
	queryCh := a.Injected.QueryChannel()
	respCh := a.Injected.ResponseChannel()
	statusCh := a.Injected.StatusChannel()
	var pending []<-chan shared.MailMessage
	var pendingTotal int // total from the Response that started the current pending drain

	// addRow creates a new label row as child of the scroller, threaded
	// to the predecessor's bottom.
	addRow := func(msg shared.MailMessage) {
		idx := len(rows)
		name := fmt.Sprintf("mr_%d", idx)

		// Layout: width = scroller width, height = rowHeight.
		lh := mancini.NewLayoutAttributesBase(name, scrollerParent)
		lh.Width = attr.ConstraintI64(
			mancini.LayoutURI(name, mancini.DataTypeInt64, mancini.LayoutWidth),
			mancini.EqualI64(scrollerWidthURI))
		lh.Height = attr.ValueI64(
			mancini.LayoutURI(name, mancini.DataTypeInt64, mancini.LayoutHeight),
			rowHeight)
		lh.InitBounds(name)

		// Thread Y to predecessor's bottom.
		var y int64
		if idx > 0 {
			prev := rows[idx-1].label
			prevLH := prev.GetLayout()
			y = prevLH.Y.Get() + prevLH.Height.Get() + 1
		}
		lh.Y.Set(y)
		lh.X.Set(0)

		// Format: "date  sender  subject"
		dateStr := ""
		if !msg.Timestamp.IsZero() {
			dateStr = msg.Timestamp.Format("Jan 02 15:04")
		}
		text := fmt.Sprintf("%s  %s  %s", dateStr, msg.From, msg.Subject)

		label := std.NewLabel(lh, theme, text, fontSize)
		label.Transparent = true
		rows = append(rows, messageRow{label: label, msg: msg})

		// Update virtual height.
		totalH := y + rowHeight
		sv.Scroller.VirtualHeight.Set(totalH)
	}

	drain := func() bool {
		dirty := false

		// Drain status messages. When import completes, request the list.
		for {
			select {
			case s := <-statusCh:
				rawPuts("[mail-ui] status: " + s + "\n")
				dirty = true
				if !importDone && len(s) > 7 && s[:7] == "Import " {
					importDone = true
				}
			default:
				goto drainResponses
			}
		}

	drainResponses:
		// Once import is done, request the full message list.
		if importDone && !listRequested {
			listRequested = true
			queryCh <- "list"
		}

		// Check for new responses.
		for {
			select {
			case resp := <-respCh:
				if resp.Error != "" {
					rawPuts("[mail-ui] error: " + resp.Error + "\n")
					dirty = true
				} else if resp.Results != nil {
					pending = append(pending, resp.Results)
					pendingTotal = resp.Total
				}
			default:
				goto drainResults
			}
		}

	drainResults:
		// Drain pending result channels — create rows as messages arrive.
		still := pending[:0]
		for _, ch := range pending {
			for {
				select {
				case msg, ok := <-ch:
					if !ok {
						goto nextPending
					}
					addRow(msg)
					dirty = true
				default:
					still = append(still, ch)
					goto nextPending
				}
			}
		nextPending:
		}
		pending = still

		// If we know the total and have all rows, set final virtual height.
		if pendingTotal > 0 && len(rows) >= pendingTotal && len(pending) == 0 {
			sv.Scroller.VirtualHeight.Set(int64(len(rows)) * (rowHeight + 1))
		}

		return dirty
	}

	return linuxapp.BuildResult{
		Drain:    drain,
		NotifyCh: a.Injected.NotifyChannel(),
	}
}

func rawPuts(s string) {
	fmt.Print(s)
}

func main() { MazarinMain() }
