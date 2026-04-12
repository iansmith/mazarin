// Package shared defines the types exchanged between the maildb shepherd
// and mail-ui .maz module over the injection channels.
package shared

import "time"

// MailMessage represents a single email message.
type MailMessage struct {
	MessageId string
	From      string
	Sender    string
	Subject   string
	Timestamp time.Time
}

// Response is sent from the maildb shepherd to mail-ui on the response
// channel. If Error is non-empty, the request was invalid and Results
// will be nil. Otherwise, the client reads MailMessage values from
// Results until the channel is closed.
type Response struct {
	Error   string
	Results <-chan MailMessage
}
