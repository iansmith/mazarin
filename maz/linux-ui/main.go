// linux-ui is a .maz module loaded by the linux shepherd that provides
// the mancini console UI. It receives console output lines via the LinuxIO
// WriteChannel and sends user input via ReadChannel.
//
// From the kernel's perspective, linux-ui IS the linux shepherd (same PID/SID).
package main

import (
	"golang.org/x/image/font"

	"mazzy/mazarin/attr"
	"mazzy/mazarin/linuxio"
	"mazzy/mazarin/mancini"
	"mazzy/mazarin/mancini/linuxapp"
	"mazzy/mazarin/mancini/std"
	"mazzy/mazarin/sys"
	mfont "mazzy/shared/font"
)

const (
	consoleCols = 80
	consoleRows = 12
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
var inj *linuxapp.Injection[linuxio.LinuxIO]

// MazarinShepherd receives the LinuxIO injection from the linux shepherd.
//
//go:noinline
func MazarinShepherd(injected interface{}) error {
	var err error
	inj, err = linuxapp.HandleInjection[linuxio.LinuxIO](injected, func(io linuxio.LinuxIO) linuxapp.SetupResult {
		wmRawCh := make(chan []byte, 8)
		fontRawCh := make(chan []byte, 8)
		// notifyCh: 1-deep, non-blocking poke from shepherd's line
		// accumulator wakes runLoop's select so drain() can run and
		// the console redraws as new stdout/stderr lines arrive.
		notifyCh := make(chan struct{}, 1)
		io.SetChannels(
			make(chan []byte, 64),
			make(chan linuxio.LineLine, 64),
			wmRawCh,
			fontRawCh,
			notifyCh,
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
	rawPuts("[linux-ui] MazarinMain() entered\n")

	if inj == nil {
		rawPuts("[linux-ui] FATAL: inj is nil (MazarinShepherd not called?)\n")
		return
	}

	linuxapp.Bootstrap(inj, linuxapp.AppConfig[linuxio.LinuxIO]{
		Title: "Linux Console",
		WinW:  800,
		WinH:  400,
		Place: func(_, sh, _, wh int) (int, int) { return 0, sh/2 - wh/2 },
		BuildUI: buildUI,
	})
}

// buildUI creates the console interactor tree and returns a drain function
// that processes WriteChannel messages.
func buildUI(a *linuxapp.App[linuxio.LinuxIO]) linuxapp.BuildResult {
	fonts := a.Fonts
	pal := a.Pal
	fontSize := a.FontSize
	appWin := a.AppWindow

	// Column: width/height mirror AppWindow's dimensions.
	colLH := mancini.NewLayoutAttributesBase("column", "AppWindow")
	appWURI := mancini.LayoutURI("AppWindow", mancini.DataTypeInt64, mancini.LayoutWidth)
	appHURI := mancini.LayoutURI("AppWindow", mancini.DataTypeInt64, mancini.LayoutHeight)
	colLH.Width = attr.ConstraintI64(
		mancini.LayoutURI("column", mancini.DataTypeInt64, mancini.LayoutWidth),
		mancini.EqualI64(appWURI))
	colLH.Height = attr.ConstraintI64(
		mancini.LayoutURI("column", mancini.DataTypeInt64, mancini.LayoutHeight),
		mancini.EqualI64(appHURI))
	colLH.InitBounds("column")
	col := std.NewColumnWithLayout(colLH, pal, mancini.AxisMinimum, 0, 1, false)
	_ = col

	// Console.
	console := std.NewConsole("console", "column", pal, fonts, fontSize,
		consoleCols, consoleRows)

	// Input row: label + text field side by side.
	const inputRowSpacing = int64(4)
	const inputRowHPadding = int64(1)
	inputRow := std.NewRow("inputRow", "column", pal, 0, mancini.AxisMiddle, inputRowHPadding)
	inputRow.SetSpacing(float64(inputRowSpacing))

	// Build font resolver for label/input theme usage.
	resolver := func(family string, feature mancini.Feature, size int64) font.Face {
		style := mfont.Regular
		if feature == mancini.Bold {
			style = mfont.Bold
		}
		return a.FC.OpenFaceByName(family, style, size)
	}
	_ = resolver

	inputLabel := std.NewLabelNamed("inputLabel", "inputRow", a.Theme,
		"Stdio Input", fontSize)
	inputLabel.Transparent = true

	// Input field width = console width - label width - fixed offset.
	consoleWidthURI := mancini.LayoutURI("console", mancini.DataTypeInt64, mancini.LayoutWidth)
	labelWidthURI := mancini.LayoutURI("inputLabel", mancini.DataTypeInt64, mancini.LayoutWidth)
	fixedOffsetURI := attr.ShepherdURI("int64", "inputFixedOffset")
	attr.ValueI64(fixedOffsetURI, inputRowSpacing+2*inputRowHPadding)

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
	input := std.NewSingleLineText(inputLH, a.Theme, "", fontSize)
	input.Hint = "linux stdin text goes here..."
	_ = inputLabel

	// Wire stdin: on Enter, send text + \n to the shepherd's ReadChannel.
	readCh := a.Injected.ReadChannel()
	input.OnSubmit = func(text string) {
		line := []byte(text + "\n")
		select {
		case readCh <- line:
		default:
			rawPuts("[linux-ui] ReadChannel full, dropping input line\n")
		}
	}

	// Swap sequence so inputRow appears above console in the Column.
	mancini.SwapSequence("inputRow", "console")

	input.AppWindow = appWin

	// Initialize input dispatch and set keyboard focus to input field.
	_, _, keyAgent := appWin.InitInput()
	keyAgent.SetFocus(input)
	input.Focused = true

	// Return drain function that processes console output, plus the
	// notify channel the shepherd pokes after each writeCh send so
	// runLoop's select wakes up.
	writeCh := a.Injected.WriteChannel()
	return linuxapp.BuildResult{
		Drain: func() bool {
			dirty := false
			for {
				select {
				case line := <-writeCh:
					for _, b := range line.Data {
						console.HandleByte(b, line.Fd)
					}
					dirty = true
				default:
					return dirty
				}
			}
		},
		NotifyCh: a.Injected.NotifyChannel(),
	}
}

// rawPuts writes directly to UART, bypassing the delegation system.
func rawPuts(s string) {
	sys.UartWriteString(s)
}

func main() { MazarinMain() }
