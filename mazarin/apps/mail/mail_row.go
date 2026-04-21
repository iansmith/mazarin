package main

import (
	"fmt"
	"unsafe"

	"mazzy/mazarin/mem"
	"mazzy/mazarin/uring"
	"mazzy/shared/mailproto"
)

type rowState int

const (
	rowLoading     rowState = 0 // KeyHeaders request in flight
	rowLoaded      rowState = 1 // header data received and cached
	rowCollExpired rowState = 2 // ErrCollectionExpired received
	rowError       rowState = 3 // other error
)

// MailRow is a single row in the mail grid backed by a live maildb collection.
// It fires a KeyHeaders request on construction and transitions from Loading to
// Loaded when the response arrives. It satisfies std.GridRow.
type MailRow struct {
	collId    uint32
	msgNum    uint32
	requestId [16]byte
	maildbSID int

	state   rowState
	headers mailproto.KeyHeaderEntry

	// OnLoaded is called after transitioning to rowLoaded so the caller can
	// mark the containing RowPercentage as damaged and trigger a redraw.
	OnLoaded            func()
	onCollectionExpired func(collId uint32)
	onRowSelected       func(collId, msgNum uint32)
}

// NewMailRow creates a MailRow and immediately fires a KeyHeaders request to
// maildb for (collId, msgNum). reqId must be a unique UUID for this request
// so the response can be matched back to this row.
func NewMailRow(maildbSID int, collId, msgNum uint32, reqId [16]byte,
	onCollExpired func(uint32), onRowSelected func(uint32, uint32)) *MailRow {

	r := &MailRow{
		collId:              collId,
		msgNum:              msgNum,
		requestId:           reqId,
		maildbSID:           maildbSID,
		state:               rowLoading,
		onCollectionExpired: onCollExpired,
		onRowSelected:       onRowSelected,
	}

	req := mailproto.KeyHeadersReq{
		RequestId: reqId,
		CollId:    collId,
		From:      msgNum,
		To:        msgNum,
	}
	msg := mailproto.EncodeKeyHeaders(&req)
	if err := uring.Send(maildbSID, &msg); err != nil {
		fmt.Printf("[mail] MailRow KeyHeaders send failed: %v\n", err)
		r.state = rowError
	}
	return r
}

// RequestId returns the UUID used for this row's KeyHeaders request.
func (r *MailRow) RequestId() [16]byte { return r.requestId }

// CollId returns the collection ID this row belongs to.
func (r *MailRow) CollId() uint32 { return r.collId }

// MsgNum returns the message number within the collection.
func (r *MailRow) MsgNum() uint32 { return r.msgNum }

// ShiftMsgNum adjusts the row's msgNum by delta. Called by the mail app when
// a CollectionAdd inserts a new message at a position that displaces this row.
func (r *MailRow) ShiftMsgNum(delta int) {
	if delta >= 0 {
		r.msgNum += uint32(delta)
	} else if uint32(-delta) <= r.msgNum {
		r.msgNum -= uint32(-delta)
	}
}

// IsLoading reports whether this row's header request is still in flight.
func (r *MailRow) IsLoading() bool { return r.state == rowLoading }

// RefreshRequest fires a new KeyHeaders request using the row's current msgNum.
// Returns the old requestId so the caller can remove it from the lookup table.
// Must only be called when IsLoading() is true — the old in-flight request had
// the pre-shift msgNum, so its response would be for the wrong message.
func (r *MailRow) RefreshRequest(newReqId [16]byte) [16]byte {
	old := r.requestId
	r.requestId = newReqId
	req := mailproto.KeyHeadersReq{
		RequestId: newReqId,
		CollId:    r.collId,
		From:      r.msgNum,
		To:        r.msgNum,
	}
	msg := mailproto.EncodeKeyHeaders(&req)
	if err := uring.Send(r.maildbSID, &msg); err != nil {
		fmt.Printf("[mail] MailRow RefreshRequest send failed: %v\n", err)
		r.state = rowError
	}
	return old
}

// HandleKeyHeadersResp processes a RespKeyHeaders message whose RequestId
// matches this row's requestId. It unpacks the first KeyHeaderEntry from the
// transferred pages and transitions to Loaded (or CollExpired / Error).
func (r *MailRow) HandleKeyHeadersResp(resp *mailproto.RespKeyHeaders) {
	switch resp.ErrCode {
	case mailproto.ErrCollectionExpired:
		r.state = rowCollExpired
		if r.onCollectionExpired != nil {
			r.onCollectionExpired(r.collId)
		}
		return
	case mailproto.ErrNone:
		// handled below
	default:
		fmt.Printf("[mail] MailRow KeyHeaders err=%d collId=%d msgNum=%d\n",
			resp.ErrCode, r.collId, r.msgNum)
		r.state = rowError
		return
	}

	if resp.Count == 0 || resp.TargetVA == 0 || resp.NumBytes < uint32(mailproto.KeyHeaderEntrySize) {
		r.state = rowError
		return
	}

	// Pages are already mapped into our address space by TransferAndUnmap.
	pages := unsafe.Slice((*byte)(unsafe.Pointer(uintptr(resp.TargetVA))), int(resp.NumBytes))
	r.headers = *(*mailproto.KeyHeaderEntry)(unsafe.Pointer(&pages[0]))

	numPages := (int(resp.NumBytes) + 4095) / 4096
	if freeErr := mem.FreePages(unsafe.Pointer(uintptr(resp.TargetVA)), numPages); freeErr != nil {
		fmt.Printf("[mail] MailRow FreePages: %v\n", freeErr)
	}

	r.state = rowLoaded
	if r.OnLoaded != nil {
		r.OnLoaded()
	}
}

// Select fires the onRowSelected callback. Called by the mail app on click.
func (r *MailRow) Select() {
	if r.onRowSelected != nil {
		r.onRowSelected(r.collId, r.msgNum)
	}
}

// --- std.GridRow interface ---

func (r *MailRow) Sender() string {
	if r.state != rowLoaded {
		return "…"
	}
	sender, _, _ := mailproto.UnpackKeyHeaderEntry(&r.headers)
	return sender
}

func (r *MailRow) Subject() string {
	if r.state != rowLoaded {
		return "…"
	}
	_, subject, _ := mailproto.UnpackKeyHeaderEntry(&r.headers)
	return subject
}

func (r *MailRow) Date() string {
	if r.state != rowLoaded {
		return ""
	}
	_, _, date := mailproto.UnpackKeyHeaderEntry(&r.headers)
	return date
}
