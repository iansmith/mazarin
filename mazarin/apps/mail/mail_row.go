package main

import "mazzy/shared/mailproto"

// MailRow is a virtual grid row backed by MailCache.
// The grid updates msgNum via SetMsgNum on every scroll; Sender/Subject/Date
// read from the cache synchronously, returning placeholders if not yet loaded.
type MailRow struct {
	msgNum uint32
	cache  *MailCache
}

func (r *MailRow) Sender() string {
	e := r.cache.Get(r.msgNum)
	if e == nil {
		return "…"
	}
	s, _, _ := mailproto.UnpackKeyHeaderEntry(e)
	return s
}

func (r *MailRow) Subject() string {
	e := r.cache.Get(r.msgNum)
	if e == nil {
		return "…"
	}
	_, s, _ := mailproto.UnpackKeyHeaderEntry(e)
	return s
}

func (r *MailRow) Date() string {
	e := r.cache.Get(r.msgNum)
	if e == nil {
		return ""
	}
	_, _, d := mailproto.UnpackKeyHeaderEntry(e)
	return d
}

func (r *MailRow) MsgNum() uint32       { return r.msgNum }
func (r *MailRow) SetMsgNum(n uint32)   { r.msgNum = n }
