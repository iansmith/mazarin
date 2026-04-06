// Package linuxio defines the injection interface between the linux shepherd
// and the linux-ui .maz module. The linux shepherd handles kernel interactions
// (syscall delegation, serial IRQ) and forwards console output lines to the
// UI via WriteChannel. The UI sends user input back via ReadChannel.
package linuxio

// LineLine is a complete line of console output with its file descriptor.
// Data includes the terminating \n.
type LineLine struct {
	Fd   byte   // 1=stdout, 2=stderr
	Data []byte // complete line including \n
}

// LinuxIO is the injection interface between the linux shepherd and linux-ui.
//
// Channels (ReadCh, WriteCh, WMCh, FontReplyCh) are created by the .maz
// during MazarinShepherd and read back by the shepherd after the call returns.
//
// The shepherd's uring dispatcher routes ProtoShepherdNotify to WMChannel
// and ProtoFontResponse to FontReplyChannel. The .maz owns all font
// infrastructure (FontCache, GlyphProvider) internally.
type LinuxIO interface {
	// --- Channels (created by .maz, read back by shepherd) ---

	// ReadChannel returns the channel carrying user input lines (UI -> shepherd).
	// Each []byte is a complete line including \n.
	ReadChannel() chan []byte

	// WriteChannel returns the channel carrying console output (shepherd -> UI).
	// Each LineLine is a complete line with fd tag for coloring.
	WriteChannel() chan LineLine

	// WMChannel returns the channel carrying raw WM payload bytes
	// (shepherd uring dispatcher -> UI). The .maz decodes these in its
	// own type namespace via rawBridge. Uses []byte (not any) to avoid
	// cross-.maz type assertion failures on builtin slice types.
	WMChannel() chan []byte

	// FontReplyChannel returns the channel carrying raw font response
	// payload bytes (shepherd uring dispatcher -> UI). The .maz decodes
	// these in its own type namespace via rawBridge. Uses []byte (not any)
	// to avoid cross-.maz type assertion failures.
	FontReplyChannel() chan []byte

	// SetChannels is called by the .maz during MazarinShepherd to fill
	// the four channels. The shepherd reads them back via the getter methods.
	SetChannels(readCh chan []byte, writeCh chan LineLine, wmCh chan []byte, fontReplyCh chan []byte)

	// --- Config (filled by shepherd) ---

	// GetRachelSID returns rachel's shepherd ID for uring.Send (blit, AppStart).
	GetRachelSID() int
}

// LinuxIOInit is the concrete implementation of LinuxIO.
//
// Bidirectional init: the shepherd fills config fields (RachelSIDVal)
// before injection. The .maz fills channel fields (ReadCh, WriteCh, WMCh,
// FontReplyCh) during MazarinShepherd.
type LinuxIOInit struct {
	// Filled by .maz during MazarinShepherd.
	ReadCh      chan []byte
	WriteCh     chan LineLine
	WMCh        chan []byte
	FontReplyCh chan []byte

	// Filled by shepherd before injection.
	RachelSIDVal int
}

func (l *LinuxIOInit) ReadChannel() chan []byte     { return l.ReadCh }
func (l *LinuxIOInit) WriteChannel() chan LineLine   { return l.WriteCh }
func (l *LinuxIOInit) WMChannel() chan []byte        { return l.WMCh }
func (l *LinuxIOInit) FontReplyChannel() chan []byte { return l.FontReplyCh }
func (l *LinuxIOInit) SetChannels(readCh chan []byte, writeCh chan LineLine, wmCh chan []byte, fontReplyCh chan []byte) {
	l.ReadCh = readCh
	l.WriteCh = writeCh
	l.WMCh = wmCh
	l.FontReplyCh = fontReplyCh
}
func (l *LinuxIOInit) GetRachelSID() int { return l.RachelSIDVal }
