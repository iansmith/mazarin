// Package maildbio defines the injection interface between the maildb
// shepherd and the mail-ui .maz module. The maildb shepherd owns the
// SQLite database and processes queries. The UI sends query strings
// via QueryChannel and reads responses (error or result stream) via
// ResponseChannel.
package maildbio

import "mazzy/maz/maildb/shared"

// StatusLine carries one status message from maildb to the UI.
// IsError=true → render in error color (red).
type StatusLine struct {
	Text    string
	IsError bool
}

// MailDBIO is the injection interface between the maildb shepherd and mail-ui.
//
// Channels are created by the .maz during MazarinShepherd and read back
// by the shepherd after the call returns. WM and font channels follow
// the same pattern as linuxio for mancini framework support.
//
// StatusChannel is created by the shepherd before injection and carries
// one-line status strings (shepherd -> UI) for display at the bottom
// of the console.
type MailDBIO interface {
	// QueryChannel returns the channel carrying query strings (UI -> shepherd).
	QueryChannel() chan string

	// ResponseChannel returns the channel carrying responses (shepherd -> UI).
	ResponseChannel() chan shared.Response

	// StatusChannel returns the channel carrying status lines
	// (shepherd -> UI). The shepherd creates this channel before injection.
	StatusChannel() <-chan StatusLine

	// WMChannel returns the channel carrying raw WM payload bytes
	// (shepherd uring dispatcher -> UI).
	WMChannel() chan []byte

	// FontReplyChannel returns the channel carrying raw font response
	// payload bytes (shepherd uring dispatcher -> UI).
	FontReplyChannel() chan []byte

	// SetChannels is called by the .maz during MazarinShepherd to fill
	// all channels. The shepherd reads them back via the getter methods.
	SetChannels(queryCh chan string, respCh chan shared.Response, wmCh chan []byte, fontReplyCh chan []byte)

	// NotifyChannel returns the channel used to wake the UI event loop
	// when status or response data is available. The shepherd pokes this
	// after sending on StatusChannel.
	NotifyChannel() <-chan struct{}

	// GetRachelSID returns rachel's shepherd ID for uring IPC.
	GetRachelSID() int
}

// MailDBIOInit is the concrete implementation of MailDBIO.
//
// Bidirectional init: the shepherd fills RachelSIDVal and StatusCh
// before injection. The .maz fills the remaining channel fields
// during MazarinShepherd.
type MailDBIOInit struct {
	// Filled by .maz during MazarinShepherd.
	QueryCh     chan string
	RespCh      chan shared.Response
	WMCh        chan []byte
	FontReplyCh chan []byte

	// Filled by shepherd before injection.
	RachelSIDVal int
	StatusCh     chan StatusLine
	NotifyCh     chan struct{} // poked after each statusCh send to wake UI event loop
}

func (m *MailDBIOInit) QueryChannel() chan string            { return m.QueryCh }
func (m *MailDBIOInit) ResponseChannel() chan shared.Response { return m.RespCh }
func (m *MailDBIOInit) StatusChannel() <-chan StatusLine       { return m.StatusCh }
func (m *MailDBIOInit) WMChannel() chan []byte               { return m.WMCh }
func (m *MailDBIOInit) FontReplyChannel() chan []byte        { return m.FontReplyCh }
func (m *MailDBIOInit) SetChannels(queryCh chan string, respCh chan shared.Response, wmCh chan []byte, fontReplyCh chan []byte) {
	m.QueryCh = queryCh
	m.RespCh = respCh
	m.WMCh = wmCh
	m.FontReplyCh = fontReplyCh
}
func (m *MailDBIOInit) NotifyChannel() <-chan struct{} { return m.NotifyCh }
func (m *MailDBIOInit) GetRachelSID() int              { return m.RachelSIDVal }
