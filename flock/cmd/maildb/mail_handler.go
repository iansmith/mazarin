package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"unsafe"

	badger "github.com/dgraph-io/badger/v4"

	"mazzy/mazarin/mem"
	"mazzy/mazarin/sys"
	"mazzy/mazarin/uring"
	"mazzy/shared/mail"
	"mazzy/flock/cmd/maildb/shared"
)

// sharedBodyPage tracks pages shared with the mail shepherd for a GetBody
// response. When BodyConfirm arrives, the pages are freed.
type sharedBodyPage struct {
	ptr      unsafe.Pointer
	numPages int
}

// mailHandler processes mail protocol requests from the mail shepherd.
type mailHandler struct {
	db *badger.DB

	// mu guards sharedBodies.
	mu           sync.Mutex
	sharedBodies map[string]sharedBodyPage // messageId → shared page info
}

func newMailHandler(db *badger.DB) *mailHandler {
	return &mailHandler{
		db:           db,
		sharedBodies: make(map[string]sharedBodyPage),
	}
}

// handleMailReq is called by the uring dispatcher for each ProtoMailReq
// message. senderSID identifies the mail shepherd.
func (mh *mailHandler) handleMailReq(v any, senderSID int16) {
	mh.mu.Lock()
	db := mh.db
	mh.mu.Unlock()

	if db == nil {
		// Import hasn't finished yet — BodyConfirm can be silently dropped,
		// but data requests need an error.
		if _, ok := v.(mail.BodyConfirm); !ok {
			mh.sendError(int(senderSID), "maildb: import in progress, try again later")
		}
		return
	}

	switch req := v.(type) {
	case mail.GetHeaders:
		mh.handleGetHeaders(&req, int(senderSID))
	case mail.GetBody:
		mh.handleGetBody(&req, int(senderSID))
	case mail.BodyConfirm:
		mh.handleBodyConfirm(&req)
	default:
		fmt.Printf("[maildb] unknown mail request type: %T\n", v)
	}
}

// handleGetHeaders iterates the date index backwards from the given date
// and sends up to Limit HeaderEntry messages followed by a HeadersEnd.
func (mh *mailHandler) handleGetHeaders(req *mail.GetHeaders, targetSID int) {
	dateStr := string(req.Date[:])
	// Trim null bytes from fixed-size array.
	dateStr = strings.TrimRight(dateStr, "\x00")
	limit := int(req.Limit)
	if limit <= 0 || limit > 500 {
		limit = 50
	}

	fmt.Printf("[maildb] GetHeaders: date=%s limit=%d for SID=%d\n", dateStr, limit, targetSID)

	// Build a seek key. Date index keys are "date:<RFC3339Nano>:<messageId>".
	// We seek to just past the requested date so that reverse iteration
	// gives us messages on or before that date.
	seekKey := []byte(fmt.Sprintf("date:%sT23:59:59.999999999Z:~", dateStr))
	prefix := []byte("date:")

	count := 0
	err := mh.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.Prefix = prefix
		opts.Reverse = true
		opts.PrefetchValues = false // date keys have empty values

		it := txn.NewIterator(opts)
		defer it.Close()

		for it.Seek(seekKey); it.Valid() && count < limit; it.Next() {
			key := string(it.Item().Key())
			// Parse "date:<timestamp>:<messageId>"
			parts := strings.SplitN(key, ":", 3)
			if len(parts) < 3 {
				continue
			}
			messageId := parts[2]

			// Fetch the MailMessage metadata.
			item, err := txn.Get([]byte(messageId))
			if err != nil {
				continue
			}
			var msg shared.MailMessage
			if err := item.Value(func(val []byte) error {
				return json.Unmarshal(val, &msg)
			}); err != nil {
				continue
			}

			entry, _ := mail.PackHeaderEntry(
				msg.Timestamp.Unix(),
				int32(msg.BodyLen),
				msg.From,
				msg.Subject,
				msg.MessageId,
			)
			encoded := mail.EncodeHeaderEntry(&entry)
			if err := uring.Send(targetSID, &encoded); err != nil {
				fmt.Printf("[maildb] send HeaderEntry failed: %v\n", err)
				return err
			}
			count++
		}
		return nil
	})

	if err != nil {
		errMsg := mail.ErrorResult{}
		errMsg.MsgLen = int32(copy(errMsg.Msg[:], fmt.Sprintf("GetHeaders error: %v", err)))
		encoded := mail.EncodeErrorResult(&errMsg)
		_ = uring.Send(targetSID, &encoded)
		return
	}

	end := mail.HeadersEnd{Count: int32(count)}
	encoded := mail.EncodeHeadersEnd(&end)
	if err := uring.Send(targetSID, &encoded); err != nil {
		fmt.Printf("[maildb] send HeadersEnd failed: %v\n", err)
	}
	fmt.Printf("[maildb] GetHeaders: sent %d headers\n", count)
}

// handleGetBody fetches the body from BadgerDB, allocates shared pages,
// writes the body directly into them, shares them with the mail shepherd,
// and sends a BodyResult message.
func (mh *mailHandler) handleGetBody(req *mail.GetBody, targetSID int) {
	messageId := mail.UnpackGetBody(req)
	fmt.Printf("[maildb] GetBody: id=%s for SID=%d\n", messageId, targetSID)

	// First get the body length from the metadata.
	var bodyLen int
	err := mh.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get([]byte(messageId))
		if err != nil {
			return fmt.Errorf("message not found: %w", err)
		}
		var msg shared.MailMessage
		return item.Value(func(val []byte) error {
			if err := json.Unmarshal(val, &msg); err != nil {
				return err
			}
			bodyLen = msg.BodyLen
			return nil
		})
	})
	if err != nil {
		mh.sendError(targetSID, fmt.Sprintf("GetBody metadata: %v", err))
		return
	}

	if bodyLen == 0 {
		mh.sendError(targetSID, "message has no body")
		return
	}

	// Allocate pages for the body.
	numPages := (bodyLen + 4095) / 4096
	pages, err := mem.AllocPagesSlice(numPages, mem.PageShared)
	if err != nil {
		mh.sendError(targetSID, fmt.Sprintf("AllocPages(%d): %v", numPages, err))
		return
	}
	pagePtr := unsafe.Pointer(&pages[0])

	// Read body directly into the allocated pages.
	err = mh.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get([]byte("body:" + messageId))
		if err != nil {
			return fmt.Errorf("body not found: %w", err)
		}
		// ValueCopy writes directly into dst.
		_, err = item.ValueCopy(pages)
		return err
	})
	if err != nil {
		mem.FreePages(pagePtr, numPages)
		mh.sendError(targetSID, fmt.Sprintf("GetBody read: %v", err))
		return
	}

	// Share pages with the mail shepherd.
	targetVA, err := sys.SharePagesWithTarget(targetSID, uintptr(pagePtr), numPages)
	if err != nil {
		mem.FreePages(pagePtr, numPages)
		mh.sendError(targetSID, fmt.Sprintf("SharePagesWithTarget: %v", err))
		return
	}

	// Track the shared pages for cleanup on BodyConfirm.
	mh.mu.Lock()
	mh.sharedBodies[messageId] = sharedBodyPage{ptr: pagePtr, numPages: numPages}
	mh.mu.Unlock()

	// Send the BodyResult.
	var result mail.BodyResult
	result.BodyLen = int32(bodyLen)
	result.NumPages = int32(numPages)
	result.TargetVA = uint64(targetVA)
	n := copy(result.MessageId[:], messageId)
	result.IdLen = int32(n)

	encoded := mail.EncodeBodyResult(&result)
	if err := uring.Send(targetSID, &encoded); err != nil {
		fmt.Printf("[maildb] send BodyResult failed: %v\n", err)
	}
	fmt.Printf("[maildb] GetBody: shared %d pages at target VA 0x%x\n", numPages, targetVA)
}

// handleBodyConfirm frees the shared pages for a message body.
func (mh *mailHandler) handleBodyConfirm(req *mail.BodyConfirm) {
	messageId := mail.UnpackBodyConfirm(req)
	fmt.Printf("[maildb] BodyConfirm: id=%s\n", messageId)

	mh.mu.Lock()
	sb, ok := mh.sharedBodies[messageId]
	if ok {
		delete(mh.sharedBodies, messageId)
	}
	mh.mu.Unlock()

	if ok {
		mem.FreePages(sb.ptr, sb.numPages)
	}
}

func (mh *mailHandler) sendError(targetSID int, msg string) {
	fmt.Printf("[maildb] error for SID %d: %s\n", targetSID, msg)
	var errResult mail.ErrorResult
	errResult.MsgLen = int32(copy(errResult.Msg[:], msg))
	encoded := mail.EncodeErrorResult(&errResult)
	_ = uring.Send(targetSID, &encoded)
}
