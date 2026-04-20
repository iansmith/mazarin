package main

import (
	"fmt"
	"net/mail"
	"time"
	"unsafe"

	"github.com/blevesearch/bleve/v2"

	"mazzy/mazarin/uring"
	"mazzy/shared/fti"
)

// indexHandler processes IndexDocument requests by reading content from
// shared memory pages and feeding it to bleve.
type indexHandler struct {
	index     bleve.Index
	corrupted bool // true after an unrecoverable bleve internal panic
}

func newIndexHandler(index bleve.Index) *indexHandler {
	return &indexHandler{index: index}
}

// handleIndexDocument reads the shared page header + body, builds a
// bleve document appropriate for the doc type, indexes it, and sends
// IndexingCompleted (or IndexError) back to the sender.
func (h *indexHandler) handleIndexDocument(req *fti.IndexDocument, senderSID int16) {
	docId := fti.UnpackDocId(req)

	if h.corrupted {
		fmt.Printf("[fti] index corrupted, dropping document %s\n", docId)
		h.sendError(int(senderSID), docId, "bleve index corrupted after internal panic")
		return
	}

	fmt.Printf("[fti] indexDocument: id=%s type=%d bodyLen=%d pages=%d from SID=%d\n",
		docId, req.DocType, req.BodyLen, req.NumPages, senderSID)

	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("[fti] PANIC in handleIndexDocument id=%s: %v — marking index corrupted\n", docId, r)
			h.corrupted = true
			h.sendError(int(senderSID), docId, fmt.Sprintf("bleve panic: %v", r))
		}
	}()

	t0 := time.Now()

	// Read content from shared pages.
	totalBytes := int(req.NumPages) * 4096
	src := unsafe.Slice((*byte)(unsafe.Pointer(uintptr(req.TargetVA))), totalBytes)

	subject, from, sender, date, bodyOffset := fti.ReadSharedPageHeader(src)
	bodyLen := int(req.BodyLen)
	body := string(src[bodyOffset : bodyOffset+bodyLen])

	// Build the bleve document based on type.
	var doc any
	var typeName string

	switch req.DocType {
	case fti.IndexTypeMailMessage:
		typeName = docTypeName(fti.IndexTypeMailMessage)
		var ts time.Time
		if date != "" {
			ts, _ = mail.ParseDate(date)
		}
		doc = bleveMailDoc{
			Subject: subject,
			From:    from,
			Sender:  sender,
			Body:    body,
			Date:    ts,
		}
	default:
		errMsg := fmt.Sprintf("unknown doc type %d", req.DocType)
		fmt.Printf("[fti] %s\n", errMsg)
		h.sendError(int(senderSID), docId, errMsg)
		return
	}

	_ = typeName // bleve uses struct field names, not the type name for now

	// Index the document.
	if err := h.index.Index(docId, doc); err != nil {
		errMsg := fmt.Sprintf("bleve.Index: %v", err)
		fmt.Printf("[fti] %s\n", errMsg)
		h.sendError(int(senderSID), docId, errMsg)
		return
	}

	elapsed := time.Since(t0)
	fmt.Printf("[fti] indexed %s (%v)\n", docId, elapsed)

	// Send completion.
	completed := fti.PackIndexingCompleted(docId)
	msg := fti.EncodeIndexingCompleted(&completed)
	if err := uring.Send(int(senderSID), &msg); err != nil {
		fmt.Printf("[fti] send IndexingCompleted failed: %v\n", err)
	}
}

func (h *indexHandler) sendError(targetSID int, docId, errMsg string) {
	e := fti.PackIndexError(docId, errMsg)
	msg := fti.EncodeIndexError(&e)
	if err := uring.Send(targetSID, &msg); err != nil {
		fmt.Printf("[fti] send IndexError failed: %v\n", err)
	}
}
