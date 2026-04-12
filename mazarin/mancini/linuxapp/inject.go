package linuxapp

import (
	"fmt"

	"mazzy/mazarin/fontcache"
	"mazzy/shared/wm"
)

// SetupResult is returned by the setup callback passed to [HandleInjection].
// It provides the common framework state that every mancini .maz needs,
// extracted from the app-specific injection type.
type SetupResult struct {
	// RachelSID is the shepherd ID of rachel (window manager).
	RachelSID int

	// WMRawCh receives raw WM message payloads from the shepherd's
	// uring dispatcher. The framework starts a bridge goroutine that
	// decodes these into typed messages.
	WMRawCh chan []byte

	// FontRawCh receives raw font response payloads. The framework
	// starts a bridge goroutine that decodes these into the FontCache's
	// ReplyCh.
	FontRawCh chan []byte
}

// Injection holds the typed injection value and the common framework
// state created during MazarinShepherd. Pass this to [Bootstrap].
type Injection[T any] struct {
	// Val is the app-specific injection value, typed by the caller.
	Val T

	// FC is the FontCache, created from RachelSID.
	FC *fontcache.FontCache

	// WMDecodedCh carries decoded WM messages in the .maz type namespace.
	WMDecodedCh chan any

	// RachelSID is cached from the setup result.
	RachelSID int
}

// HandleInjection performs the standard MazarinShepherd ceremony:
// type-asserts the injected value to T, then calls the setup callback
// to extract app-specific state. The framework creates a FontCache and
// starts WM/font bridge goroutines.
//
// The setup callback receives the typed injection value and should:
//   - Create the raw WM and font channels
//   - Set up any app-specific channels (e.g., read/write for console I/O)
//   - Return the RachelSID and raw channels
//
// Call this from MazarinShepherd. Store the returned Injection and
// pass it to Bootstrap from MazarinMain.
func HandleInjection[T any](injected interface{}, setup func(T) SetupResult) (*Injection[T], error) {
	if injected == nil {
		rawPuts("[linuxapp] HandleInjection: nil injection\n")
		return nil, fmt.Errorf("nil injection")
	}
	val, ok := injected.(T)
	if !ok {
		rawPuts("[linuxapp] HandleInjection: type assertion failed\n")
		return nil, fmt.Errorf("type assertion to %T failed", (*T)(nil))
	}

	res := setup(val)

	// Create FontCache from RachelSID.
	fc := fontcache.New(res.RachelSID)

	// Decoded WM channel — bridge goroutine fills this.
	wmDecodedCh := make(chan any, 8)

	// Bridge goroutines: decode raw payloads into typed messages
	// in the .maz's type namespace.
	go rawBridge(res.WMRawCh, wmDecodedCh, wm.DecodeShepherdNotifyFromPayload)
	go rawBridge(res.FontRawCh, fc.ReplyCh, wm.DecodeFontResponseFromPayload)

	rawPuts("[linuxapp] HandleInjection: channels created, injection complete\n")

	return &Injection[T]{
		Val:         val,
		FC:          fc,
		WMDecodedCh: wmDecodedCh,
		RachelSID:   res.RachelSID,
	}, nil
}

// rawBridge reads raw payload bytes from a channel, decodes them using the
// provided decoder function (running in the .maz's goroutine so types match),
// and forwards decoded messages to the output channel.
func rawBridge(rawCh <-chan []byte, decodedCh chan<- any, decode func([]byte) any) {
	for payload := range rawCh {
		if len(payload) < 4 {
			continue
		}
		decoded := decode(payload)
		if decoded != nil {
			decodedCh <- decoded
		}
	}
}
