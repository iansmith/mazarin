package main

import (
	"fmt"
	"net/mail"
	"time"
	"unsafe"

	"github.com/blevesearch/bleve/v2"

	"mazzy/mazarin/sys"
	"mazzy/mazarin/uring"
	"mazzy/shared/fti"
)

// slowIndexThreshold triggers an on-demand kernel [status] dump when
// bleve.Index() takes longer than this. Diagnostic for the SCORCH
// ENOENT investigation — typical Index() runs are ~50–150ms; anything
// over 500ms means something else stalled the goroutine and we want
// to see what the kernel was doing at that moment.
const slowIndexThreshold = 500 * time.Millisecond

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

	// noise: per-doc indexDocument trace disabled during scorch ENOENT investigation

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
		sys.DumpKernelStatus()
		h.sendError(int(senderSID), docId, errMsg)
		return
	}

	elapsed := time.Since(t0)
	if elapsed > slowIndexThreshold {
		fmt.Printf("[fti] SLOW Index() %s (%v) — requesting kernel status dump\n", docId, elapsed)
		sys.DumpKernelStatus()
	}
	// noise: per-doc indexed-success trace disabled during scorch ENOENT investigation
	_ = elapsed

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
