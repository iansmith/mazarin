package main

import (
	"fmt"

	"mazzy/mazarin/maildbio"
)

var (
	mlogCh       chan<- maildbio.StatusLine
	mlogNotifyCh chan<- struct{}
)

func mlogSetSink(ch chan<- maildbio.StatusLine, notify chan<- struct{}) {
	mlogCh = ch
	mlogNotifyCh = notify
}

// mlogInfo sends an informational message to the maildb console only.
// Drops silently if the channel is full or the sink is not yet set.
// Never falls back to fmt.Printf — the whole point is to keep the
// linux console quiet for routine progress messages.
func mlogInfo(format string, args ...any) {
	if mlogCh == nil {
		return
	}
	msg := fmt.Sprintf(format, args...)
	select {
	case mlogCh <- maildbio.StatusLine{Text: msg}:
	default:
	}
	select {
	case mlogNotifyCh <- struct{}{}:
	default:
	}
}

// mlogErrorf sends an error message to both the maildb console (red)
// and stdout (linux console + serial log).
func mlogErrorf(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	fmt.Println(msg)
	if mlogCh == nil {
		return
	}
	select {
	case mlogCh <- maildbio.StatusLine{Text: msg, IsError: true}:
	default:
	}
	select {
	case mlogNotifyCh <- struct{}{}:
	default:
	}
}
