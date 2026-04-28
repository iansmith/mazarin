// mail-ui is a .maz module loaded by the maildb shepherd that provides
// the mancini UI for displaying maildb status in a scrolling console.
//
// From the kernel's perspective, mail-ui IS the maildb shepherd (same PID/SID).
package main

import (
	"fmt"

	"mazzy/mazarin/maildbio"
	"mazzy/mazarin/mancini/linuxapp"
	"mazzy/mazarin/mancini/std"
	"mazzy/maz/maildb/shared"
)

const (
	consoleCols int = 80
	consoleRows int = 12
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
		WinH:    400,
		Place:   func(_, sh, _, wh int) (int, int) { return 0, sh - wh },
		BuildUI: buildUI,
	})
}

// buildUI creates the mail UI interactor tree and returns a drain function
// that processes StatusChannel messages and displays them in a console.
func buildUI(a *linuxapp.App[maildbio.MailDBIO]) linuxapp.BuildResult {
	pal := a.Pal

	// Console fills the AppWindow — use same font size as linux-ui.
	console := std.NewConsole("console", "AppWindow", pal,
		a.Fonts, a.FontSize, consoleCols, consoleRows)

	// Initialize input dispatch.
	a.AppWindow.InitInput()

	// Channels from injection.
	statusCh := a.Injected.StatusChannel()

	drain := func() bool {
		dirty := false

		// Drain status messages into the console.
		for {
			select {
			case s := <-statusCh:
				color := pal.Text()
				if s.IsError {
					color = console.StderrColor()
				}
				console.AddLine(s.Text, color)
				dirty = true
			default:
				return dirty
			}
		}
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
