package main

import (
	"fmt"
	"sync"
	"time"
	"unsafe"

	badger "github.com/dgraph-io/badger/v4"

	"mazzy/mazarin/mem"
	"mazzy/mazarin/sys"
	"mazzy/mazarin/uring"
	"mazzy/shared/ipc"
	"mazzy/shared/mailproto"
)

// mailHandler dispatches v2 mail protocol requests from the mail shepherd.
type mailHandler struct {
	mu sync.Mutex
	db *badger.DB
	ms *MessageStore
	cs *collectionStore
}

func newMailHandler(db *badger.DB) *mailHandler {
	return &mailHandler{db: db}
}

// setStores enables the v2 handlers once the badger DB and data structures
// are ready (called after mbox import completes).
func (mh *mailHandler) setStores(db *badger.DB, ms *MessageStore, cs *collectionStore) {
	mh.mu.Lock()
	mh.db = db
	mh.ms = ms
	mh.cs = cs
	mh.mu.Unlock()
}

// handleMailReq is called by the uring dispatcher for each ProtoMailReq message.
func (mh *mailHandler) handleMailReq(v any, senderSID int16) {
	mh.mu.Lock()
	db := mh.db
	ms := mh.ms
	cs := mh.cs
	mh.mu.Unlock()

	if db == nil {
		fmt.Printf("[maildb] mail request %T dropped: db not ready\n", v)
		return
	}

	switch req := v.(type) {
	case mailproto.MessageCountReq:
		mh.handleMessageCount(&req, int(senderSID), ms)
	case mailproto.CreateCollectionReq:
		mh.handleCreateCollection(&req, int(senderSID), cs)
	case mailproto.KeyHeadersReq:
		mh.handleKeyHeaders(&req, int(senderSID), ms, cs)
	case mailproto.AllHeadersReq:
		mh.handleAllHeaders(&req, int(senderSID), ms, cs)
	case mailproto.LatestUnreadReq:
		mh.handleLatestUnread(&req, int(senderSID), ms, cs)
	case mailproto.BodyReq:
		mh.handleBody(&req, int(senderSID), ms, cs)
	case mailproto.MarkReadReq:
		mh.handleMarkRead(&req, int(senderSID), ms, cs)
	case mailproto.MarkDeletedReq:
		mh.handleMarkDeleted(&req, int(senderSID), ms, cs)
	default:
		fmt.Printf("[maildb] unknown mail request type: %T\n", v)
	}
}

// handleMessageCount returns the total number of messages in the DB.
func (mh *mailHandler) handleMessageCount(req *mailproto.MessageCountReq, senderSID int, ms *MessageStore) {
	n, err := readCounter(ms.db, keyCountAll)
	var errCode uint32
	if err != nil {
		errCode = mailproto.ErrBadgerError
		n = 0
	}
	resp := mailproto.RespMessageCount{RequestId: req.RequestId, Count: n, ErrCode: errCode}
	msg := mailproto.EncodeRespMessageCount(&resp)
	sendMailMsg(senderSID, &msg)
}

// handleCreateCollection creates a new HOT collection and returns its id + size.
func (mh *mailHandler) handleCreateCollection(req *mailproto.CreateCollectionReq, senderSID int, cs *collectionStore) {
	coll, err := cs.createCollection(req.FilterType, req.SortOrder, req.FilterArg, int16(senderSID))
	if err != nil {
		fmt.Printf("[maildb] createCollection: %v\n", err)
		resp := mailproto.RespCreateCollection{RequestId: req.RequestId, ErrCode: mailproto.ErrFilterInvalid}
		msg := mailproto.EncodeRespCreateCollection(&resp)
		sendMailMsg(senderSID, &msg)
		return
	}
	resp := mailproto.RespCreateCollection{
		RequestId: req.RequestId,
		CollId:    coll.id,
		Size:      uint32(coll.totalSize),
		ErrCode:   mailproto.ErrNone,
	}
	msg := mailproto.EncodeRespCreateCollection(&resp)
	sendMailMsg(senderSID, &msg)
	fmt.Printf("[maildb] createCollection: collId=%d filter=%d size=%d\n", coll.id, req.FilterType, coll.totalSize)
}

// handleKeyHeaders loads a window of KeyHeaderEntry records and transfers the
// pages to the requesting shepherd.
func (mh *mailHandler) handleKeyHeaders(req *mailproto.KeyHeadersReq, senderSID int, ms *MessageStore, cs *collectionStore) {
	coll, err := cs.lookupCollection(req.CollId)
	if err != nil {
		resp := mailproto.RespKeyHeaders{RequestId: req.RequestId, ErrCode: mailproto.ErrCollectionExpired}
		msg := mailproto.EncodeRespKeyHeaders(&resp)
		sendMailMsg(senderSID, &msg)
		return
	}

	if err := cs.loadWindow(coll, req.From, req.To); err != nil {
		fmt.Printf("[maildb] loadWindow: %v\n", err)
		resp := mailproto.RespKeyHeaders{RequestId: req.RequestId, ErrCode: mailproto.ErrBadgerError}
		msg := mailproto.EncodeRespKeyHeaders(&resp)
		sendMailMsg(senderSID, &msg)
		return
	}

	// Count available entries in range.
	count := 0
	for i := req.From; i <= req.To; i++ {
		if msgId := coll.entries[i]; msgId != "" {
			count++
		}
	}

	if count == 0 {
		resp := mailproto.RespKeyHeaders{RequestId: req.RequestId, ErrCode: mailproto.ErrNone}
		msg := mailproto.EncodeRespKeyHeaders(&resp)
		sendMailMsg(senderSID, &msg)
		return
	}

	numBytes := count * mailproto.KeyHeaderEntrySize
	numPages := (numBytes + 4095) / 4096
	pages, allocErr := mem.AllocPagesSlice(numPages, mem.PageShared)
	if allocErr != nil {
		fmt.Printf("[maildb] KeyHeaders AllocPages(%d): %v\n", numPages, allocErr)
		resp := mailproto.RespKeyHeaders{RequestId: req.RequestId, ErrCode: mailproto.ErrBadgerError}
		msg := mailproto.EncodeRespKeyHeaders(&resp)
		sendMailMsg(senderSID, &msg)
		return
	}

	// Pack entries into pages.
	idx := 0
	for i := req.From; i <= req.To; i++ {
		msgId := coll.entries[i]
		if msgId == "" {
			continue
		}
		headers, hErr := ms.LoadHeaders(msgId)
		if hErr != nil {
			continue
		}
		isRead, _, _ := ms.LoadFlags(msgId)
		var flags uint32
		if isRead {
			flags |= 1
		}
		date := ""
		if !headers.Timestamp.IsZero() {
			date = headers.Timestamp.UTC().Format(time.RFC3339)
		}
		entry := mailproto.PackKeyHeaderEntry(i, flags, headers.Sender, headers.Subject, date)
		*(*mailproto.KeyHeaderEntry)(unsafe.Pointer(&pages[idx*mailproto.KeyHeaderEntrySize])) = entry
		idx++
	}

	pagePtr := unsafe.Pointer(&pages[0])
	targetVA, transErr := sys.TransferAndUnmap(senderSID, uintptr(pagePtr), numPages)
	if transErr != nil {
		_ = mem.FreePages(pagePtr, numPages)
		fmt.Printf("[maildb] KeyHeaders TransferAndUnmap: %v\n", transErr)
		resp := mailproto.RespKeyHeaders{RequestId: req.RequestId, ErrCode: mailproto.ErrBadgerError}
		msg := mailproto.EncodeRespKeyHeaders(&resp)
		sendMailMsg(senderSID, &msg)
		return
	}

	resp := mailproto.RespKeyHeaders{
		RequestId: req.RequestId,
		TargetVA:  uint64(targetVA),
		NumBytes:  uint32(idx * mailproto.KeyHeaderEntrySize),
		Count:     uint32(idx),
		ErrCode:   mailproto.ErrNone,
	}
	msg := mailproto.EncodeRespKeyHeaders(&resp)
	sendMailMsg(senderSID, &msg)
}

// handleAllHeaders returns the full headers for one message as an AllHeaderEntry page.
// Fields not stored in badger (To, CC, ContentType) are returned as empty strings.
func (mh *mailHandler) handleAllHeaders(req *mailproto.AllHeadersReq, senderSID int, ms *MessageStore, cs *collectionStore) {
	coll, err := cs.lookupCollection(req.CollId)
	if err != nil {
		resp := mailproto.RespAllHeaders{RequestId: req.RequestId, ErrCode: mailproto.ErrCollectionExpired}
		msg := mailproto.EncodeRespAllHeaders(&resp)
		sendMailMsg(senderSID, &msg)
		return
	}

	msgId, ok := coll.entries[req.MsgNum]
	if !ok || msgId == "" {
		// Try to load the single entry on demand.
		if loadErr := cs.loadWindow(coll, req.MsgNum, req.MsgNum); loadErr != nil {
			resp := mailproto.RespAllHeaders{RequestId: req.RequestId, ErrCode: mailproto.ErrInvalidMsgNumber}
			msg := mailproto.EncodeRespAllHeaders(&resp)
			sendMailMsg(senderSID, &msg)
			return
		}
		msgId = coll.entries[req.MsgNum]
		if msgId == "" {
			resp := mailproto.RespAllHeaders{RequestId: req.RequestId, ErrCode: mailproto.ErrInvalidMsgNumber}
			msg := mailproto.EncodeRespAllHeaders(&resp)
			sendMailMsg(senderSID, &msg)
			return
		}
	}

	headers, hErr := ms.LoadHeaders(msgId)
	if hErr != nil {
		fmt.Printf("[maildb] AllHeaders LoadHeaders(%s): %v\n", msgId, hErr)
		resp := mailproto.RespAllHeaders{RequestId: req.RequestId, ErrCode: mailproto.ErrMessageNotFound}
		msg := mailproto.EncodeRespAllHeaders(&resp)
		sendMailMsg(senderSID, &msg)
		return
	}
	isRead, isDeleted, _ := ms.LoadFlags(msgId)
	var flags uint32
	if isRead {
		flags |= 1
	}
	if isDeleted {
		flags |= 2
	}

	pages, allocErr := mem.AllocPagesSlice(1, mem.PageShared)
	if allocErr != nil {
		resp := mailproto.RespAllHeaders{RequestId: req.RequestId, ErrCode: mailproto.ErrBadgerError}
		msg := mailproto.EncodeRespAllHeaders(&resp)
		sendMailMsg(senderSID, &msg)
		return
	}

	date := ""
	if !headers.Timestamp.IsZero() {
		date = headers.Timestamp.UTC().Format(time.RFC3339)
	}
	entry := mailproto.PackAllHeaderEntry(req.MsgNum, flags,
		headers.From, "", "", headers.Subject, date, headers.MessageId, "")
	*(*mailproto.AllHeaderEntry)(unsafe.Pointer(&pages[0])) = entry

	pagePtr := unsafe.Pointer(&pages[0])
	targetVA, transErr := sys.TransferAndUnmap(senderSID, uintptr(pagePtr), 1)
	if transErr != nil {
		_ = mem.FreePages(pagePtr, 1)
		resp := mailproto.RespAllHeaders{RequestId: req.RequestId, ErrCode: mailproto.ErrBadgerError}
		msg := mailproto.EncodeRespAllHeaders(&resp)
		sendMailMsg(senderSID, &msg)
		return
	}

	resp := mailproto.RespAllHeaders{
		RequestId: req.RequestId,
		TargetVA:  uint64(targetVA),
		NumBytes:  uint32(mailproto.AllHeaderEntrySize),
		ErrCode:   mailproto.ErrNone,
	}
	msg := mailproto.EncodeRespAllHeaders(&resp)
	sendMailMsg(senderSID, &msg)
}

// handleLatestUnread creates an ephemeral FilterUnread+SortDesc collection,
// loads the first entry, and returns its full headers in an AllHeaderEntry page.
func (mh *mailHandler) handleLatestUnread(req *mailproto.LatestUnreadReq, senderSID int, ms *MessageStore, cs *collectionStore) {
	var filterArg [64]byte
	coll, err := cs.createCollection(mailproto.FilterUnread, mailproto.SortDesc, filterArg, int16(senderSID))
	if err != nil {
		fmt.Printf("[maildb] LatestUnread createCollection: %v\n", err)
		resp := mailproto.RespLatestUnread{RequestId: req.RequestId, ErrCode: mailproto.ErrBadgerError}
		msg := mailproto.EncodeRespLatestUnread(&resp)
		sendMailMsg(senderSID, &msg)
		return
	}

	if coll.totalSize == 0 {
		resp := mailproto.RespLatestUnread{
			RequestId: req.RequestId,
			CollId:    coll.id,
			ErrCode:   mailproto.ErrMessageNotFound,
		}
		msg := mailproto.EncodeRespLatestUnread(&resp)
		sendMailMsg(senderSID, &msg)
		return
	}

	if loadErr := cs.loadWindow(coll, 0, 0); loadErr != nil {
		resp := mailproto.RespLatestUnread{RequestId: req.RequestId, ErrCode: mailproto.ErrBadgerError}
		msg := mailproto.EncodeRespLatestUnread(&resp)
		sendMailMsg(senderSID, &msg)
		return
	}

	msgId := coll.entries[0]
	if msgId == "" {
		resp := mailproto.RespLatestUnread{RequestId: req.RequestId, ErrCode: mailproto.ErrMessageNotFound}
		msg := mailproto.EncodeRespLatestUnread(&resp)
		sendMailMsg(senderSID, &msg)
		return
	}

	headers, hErr := ms.LoadHeaders(msgId)
	if hErr != nil {
		resp := mailproto.RespLatestUnread{RequestId: req.RequestId, ErrCode: mailproto.ErrMessageNotFound}
		msg := mailproto.EncodeRespLatestUnread(&resp)
		sendMailMsg(senderSID, &msg)
		return
	}

	pages, allocErr := mem.AllocPagesSlice(1, mem.PageShared)
	if allocErr != nil {
		resp := mailproto.RespLatestUnread{RequestId: req.RequestId, ErrCode: mailproto.ErrBadgerError}
		msg := mailproto.EncodeRespLatestUnread(&resp)
		sendMailMsg(senderSID, &msg)
		return
	}

	date := ""
	if !headers.Timestamp.IsZero() {
		date = headers.Timestamp.UTC().Format(time.RFC3339)
	}
	entry := mailproto.PackAllHeaderEntry(0, 0,
		headers.From, "", "", headers.Subject, date, headers.MessageId, "")
	*(*mailproto.AllHeaderEntry)(unsafe.Pointer(&pages[0])) = entry

	pagePtr := unsafe.Pointer(&pages[0])
	targetVA, transErr := sys.TransferAndUnmap(senderSID, uintptr(pagePtr), 1)
	if transErr != nil {
		_ = mem.FreePages(pagePtr, 1)
		resp := mailproto.RespLatestUnread{RequestId: req.RequestId, ErrCode: mailproto.ErrBadgerError}
		msg := mailproto.EncodeRespLatestUnread(&resp)
		sendMailMsg(senderSID, &msg)
		return
	}

	resp := mailproto.RespLatestUnread{
		RequestId: req.RequestId,
		CollId:    coll.id,
		MsgNum:    0,
		TargetVA:  uint64(targetVA),
		NumBytes:  uint32(mailproto.AllHeaderEntrySize),
		ErrCode:   mailproto.ErrNone,
	}
	msg := mailproto.EncodeRespLatestUnread(&resp)
	sendMailMsg(senderSID, &msg)
}

// handleBody loads the raw body bytes for a message and transfers them as pages.
func (mh *mailHandler) handleBody(req *mailproto.BodyReq, senderSID int, ms *MessageStore, cs *collectionStore) {
	coll, err := cs.lookupCollection(req.CollId)
	if err != nil {
		resp := mailproto.RespBody{RequestId: req.RequestId, ErrCode: mailproto.ErrCollectionExpired}
		msg := mailproto.EncodeRespBody(&resp)
		sendMailMsg(senderSID, &msg)
		return
	}

	msgId, ok := coll.entries[req.MsgNum]
	if !ok || msgId == "" {
		if loadErr := cs.loadWindow(coll, req.MsgNum, req.MsgNum); loadErr != nil {
			resp := mailproto.RespBody{RequestId: req.RequestId, ErrCode: mailproto.ErrInvalidMsgNumber}
			msg := mailproto.EncodeRespBody(&resp)
			sendMailMsg(senderSID, &msg)
			return
		}
		msgId = coll.entries[req.MsgNum]
		if msgId == "" {
			resp := mailproto.RespBody{RequestId: req.RequestId, ErrCode: mailproto.ErrInvalidMsgNumber}
			msg := mailproto.EncodeRespBody(&resp)
			sendMailMsg(senderSID, &msg)
			return
		}
	}

	bodyBytes, bErr := ms.LoadBody(msgId)
	if bErr != nil {
		fmt.Printf("[maildb] Body LoadBody(%s): %v\n", msgId, bErr)
		resp := mailproto.RespBody{RequestId: req.RequestId, ErrCode: mailproto.ErrMessageNotFound}
		msg := mailproto.EncodeRespBody(&resp)
		sendMailMsg(senderSID, &msg)
		return
	}

	if len(bodyBytes) == 0 {
		resp := mailproto.RespBody{RequestId: req.RequestId, ErrCode: mailproto.ErrMessageNotFound}
		msg := mailproto.EncodeRespBody(&resp)
		sendMailMsg(senderSID, &msg)
		return
	}

	numPages := (len(bodyBytes) + 4095) / 4096
	pages, allocErr := mem.AllocPagesSlice(numPages, mem.PageShared)
	if allocErr != nil {
		resp := mailproto.RespBody{RequestId: req.RequestId, ErrCode: mailproto.ErrBadgerError}
		msg := mailproto.EncodeRespBody(&resp)
		sendMailMsg(senderSID, &msg)
		return
	}

	copy(pages, bodyBytes)

	pagePtr := unsafe.Pointer(&pages[0])
	targetVA, transErr := sys.TransferAndUnmap(senderSID, uintptr(pagePtr), numPages)
	if transErr != nil {
		_ = mem.FreePages(pagePtr, numPages)
		resp := mailproto.RespBody{RequestId: req.RequestId, ErrCode: mailproto.ErrBadgerError}
		msg := mailproto.EncodeRespBody(&resp)
		sendMailMsg(senderSID, &msg)
		return
	}

	resp := mailproto.RespBody{
		RequestId: req.RequestId,
		TargetVA:  uint64(targetVA),
		NumBytes:  uint32(len(bodyBytes)),
		ErrCode:   mailproto.ErrNone,
	}
	msg := mailproto.EncodeRespBody(&resp)
	sendMailMsg(senderSID, &msg)
}

// handleMarkRead marks a message as read and decrements the count:unread counter.
func (mh *mailHandler) handleMarkRead(req *mailproto.MarkReadReq, senderSID int, ms *MessageStore, cs *collectionStore) {
	coll, err := cs.lookupCollection(req.CollId)
	if err != nil {
		resp := mailproto.RespMarkRead{RequestId: req.RequestId, ErrCode: mailproto.ErrCollectionExpired}
		msg := mailproto.EncodeRespMarkRead(&resp)
		sendMailMsg(senderSID, &msg)
		return
	}

	msgId, ok := coll.entries[req.MsgNum]
	if !ok || msgId == "" {
		resp := mailproto.RespMarkRead{RequestId: req.RequestId, ErrCode: mailproto.ErrInvalidMsgNumber}
		msg := mailproto.EncodeRespMarkRead(&resp)
		sendMailMsg(senderSID, &msg)
		return
	}

	if err := ms.MarkRead(msgId); err != nil {
		fmt.Printf("[maildb] MarkRead(%s): %v\n", msgId, err)
		resp := mailproto.RespMarkRead{RequestId: req.RequestId, ErrCode: mailproto.ErrBadgerError}
		msg := mailproto.EncodeRespMarkRead(&resp)
		sendMailMsg(senderSID, &msg)
		return
	}

	resp := mailproto.RespMarkRead{RequestId: req.RequestId, ErrCode: mailproto.ErrNone}
	msg := mailproto.EncodeRespMarkRead(&resp)
	sendMailMsg(senderSID, &msg)
}

// handleMarkDeleted marks a message as deleted, fans out CollectionRemove to all
// collections that have the message loaded, and replies to the requesting client.
func (mh *mailHandler) handleMarkDeleted(req *mailproto.MarkDeletedReq, senderSID int, ms *MessageStore, cs *collectionStore) {
	coll, err := cs.lookupCollection(req.CollId)
	if err != nil {
		resp := mailproto.RespMarkDeleted{RequestId: req.RequestId, ErrCode: mailproto.ErrCollectionExpired}
		msg := mailproto.EncodeRespMarkDeleted(&resp)
		sendMailMsg(senderSID, &msg)
		return
	}

	msgId, ok := coll.entries[req.MsgNum]
	if !ok || msgId == "" {
		resp := mailproto.RespMarkDeleted{RequestId: req.RequestId, ErrCode: mailproto.ErrInvalidMsgNumber}
		msg := mailproto.EncodeRespMarkDeleted(&resp)
		sendMailMsg(senderSID, &msg)
		return
	}

	if err := ms.MarkDeleted(msgId); err != nil {
		fmt.Printf("[maildb] MarkDeleted(%s): %v\n", msgId, err)
		resp := mailproto.RespMarkDeleted{RequestId: req.RequestId, ErrCode: mailproto.ErrBadgerError}
		msg := mailproto.EncodeRespMarkDeleted(&resp)
		sendMailMsg(senderSID, &msg)
		return
	}

	// Fan-out CollectionRemove to all collections that have the message loaded.
	notifs := cs.removeMessage(msgId, req.RequestId)
	var newSize uint32
	for _, n := range notifs {
		var msgIdBytes [64]byte
		copy(msgIdBytes[:], n.msgId)
		notif := mailproto.CollectionRemove{
			CollId:    n.collId,
			MsgNum:    n.msgNum,
			NewSize:   uint32(n.newSize),
			MsgId:     msgIdBytes,
			RequestId: n.reqId,
		}
		encoded := mailproto.EncodeCollectionRemove(&notif)
		for _, sid := range n.sids {
			if sendErr := uring.Send(int(sid), &encoded); sendErr != nil {
				fmt.Printf("[maildb] CollectionRemove to SID %d: %v\n", sid, sendErr)
			}
		}
		if n.collId == req.CollId {
			newSize = uint32(n.newSize)
		}
	}

	resp := mailproto.RespMarkDeleted{
		RequestId: req.RequestId,
		ErrCode:   mailproto.ErrNone,
		NewSize:   newSize,
	}
	msg := mailproto.EncodeRespMarkDeleted(&resp)
	sendMailMsg(senderSID, &msg)
}

func sendMailMsg(targetSID int, msg *ipc.UringIPCMsg) {
	if err := uring.Send(targetSID, msg); err != nil {
		fmt.Printf("[maildb] send to SID %d failed: %v\n", targetSID, err)
	}
}
