package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/mail"
	"os"
	"strings"
	"sync"
	"unsafe"

	badger "github.com/dgraph-io/badger/v4"

	"mazzy/flock/cmd/maildb/shared"
	"mazzy/mazarin/mem"
	"mazzy/mazarin/sys"
	"mazzy/mazarin/uring"
	"mazzy/shared/fti"
)

// pendingIndex tracks a shared page allocation for an outstanding fti request.
type pendingIndex struct {
	pagePtr  unsafe.Pointer
	numPages int
}

// ftiTracker manages outstanding fti IndexDocument requests. It runs a
// goroutine that drains completion responses from fti, frees the shared
// pages, and notifies the UI.
type ftiTracker struct {
	mu       sync.Mutex
	pending  map[string]pendingIndex // docId -> shared page info
	inflight int                     // number of outstanding requests
	done     chan struct{}           // closed when all inflight requests are drained
	closed   bool                   // set when no more requests will be sent
}

func newFTITracker(ftiRespCh <-chan any, notify func(string)) *ftiTracker {
	t := &ftiTracker{
		pending: make(map[string]pendingIndex),
		done:    make(chan struct{}),
	}
	go t.drainLoop(ftiRespCh, notify)
	return t
}

// track registers a pending IndexDocument request.
func (t *ftiTracker) track(docId string, pagePtr unsafe.Pointer, numPages int) {
	t.mu.Lock()
	t.pending[docId] = pendingIndex{pagePtr: pagePtr, numPages: numPages}
	t.inflight++
	t.mu.Unlock()
}

// close signals that no more requests will be sent. The drain goroutine
// will close t.done after all outstanding requests complete.
func (t *ftiTracker) close() {
	t.mu.Lock()
	t.closed = true
	if t.inflight == 0 {
		close(t.done)
	}
	t.mu.Unlock()
}

// wait blocks until all outstanding fti requests have completed.
func (t *ftiTracker) wait() {
	<-t.done
}

// drainLoop reads fti responses, frees shared pages, and notifies the UI.
func (t *ftiTracker) drainLoop(ftiRespCh <-chan any, notify func(string)) {
	for resp := range ftiRespCh {
		var docId string
		var errMsg string

		switch r := resp.(type) {
		case fti.IndexingCompleted:
			docId = fti.UnpackCompletedId(&r)
			fmt.Printf("[maildb] fti indexed: %s\n", docId)
			notify(fmt.Sprintf("Indexed: %s", docId))
		case fti.IndexError:
			docId, errMsg = fti.UnpackIndexError(&r)
			fmt.Printf("[maildb] fti error: %s: %s\n", docId, errMsg)
			notify(fmt.Sprintf("Index error: %s", errMsg))
		default:
			fmt.Printf("[maildb] unexpected fti response: %T\n", resp)
			continue
		}

		// Free the shared pages.
		t.mu.Lock()
		if pi, ok := t.pending[docId]; ok {
			mem.FreePages(pi.pagePtr, pi.numPages)
			delete(t.pending, docId)
		}
		t.inflight--
		shouldClose := t.closed && t.inflight == 0
		t.mu.Unlock()

		if shouldClose {
			close(t.done)
			return
		}
	}
}

// mboxImport parses the mbox file inside mboxDir and writes each message's
// headers + body into BadgerDB. Each message body is also sent (non-blocking)
// to the fti shepherd for full-text indexing via shared memory pages.
//
// Returns the open db immediately after badger flush. FTI indexing continues
// in the background via the ftiTracker.
func mboxImport(mboxDir string, ftiSID int, tracker *ftiTracker, notify func(string)) (*badger.DB, error) {
	mboxPath := mboxDir + "/mbox"

	fmt.Printf("[maildb] mboxImport: opening %s\n", mboxPath)
	f, err := os.Open(mboxPath)
	if err != nil {
		return nil, fmt.Errorf("open mbox %s: %w", mboxPath, err)
	}
	defer f.Close()
	fmt.Println("[maildb] mboxImport: mbox opened, opening badger")

	os.RemoveAll("/tmp/data/fti/badger")
	opts := badger.DefaultOptions("/tmp/data/fti/badger").WithLogger(nil)
	db, err := badger.Open(opts)
	if err != nil {
		return nil, fmt.Errorf("open badger: %w", err)
	}
	fmt.Println("[maildb] mboxImport: badger opened, starting parse")

	notify("Starting mbox import...")

	reader := bufio.NewReaderSize(f, 64*1024)
	count := 0
	inHeaders := false
	headers := make(map[string]string)
	var lastKey string
	var body strings.Builder

	wb := db.NewWriteBatch()

	for {
		line, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			wb.Cancel()
			db.Close()
			return nil, fmt.Errorf("read mbox: %w", err)
		}
		atEOF := err == io.EOF

		trimmed := strings.TrimRight(line, "\r\n")

		if strings.HasPrefix(trimmed, "From ") || atEOF {
			if len(headers) > 0 {
				bodyStr := body.String()
				count++
				fmt.Printf("[maildb] parse: msg %d id=%s subj=%q body=%d bytes\n",
					count, headers["message-id"], headers["subject"], len(bodyStr))
				if storeErr := storeParsedMessage(wb, headers, bodyStr, ftiSID, tracker); storeErr != nil {
					fmt.Printf("[maildb] store error: %v\n", storeErr)
					count--
				}
				headers = make(map[string]string)
				lastKey = ""
				body.Reset()
			}
			if atEOF {
				break
			}
			inHeaders = true
			continue
		}

		if !inHeaders {
			body.WriteString(line)
			continue
		}

		if trimmed == "" {
			inHeaders = false
			continue
		}

		if (trimmed[0] == ' ' || trimmed[0] == '\t') && lastKey != "" {
			headers[lastKey] += " " + strings.TrimSpace(trimmed)
			continue
		}

		if idx := strings.IndexByte(trimmed, ':'); idx > 0 {
			key := strings.ToLower(trimmed[:idx])
			val := strings.TrimSpace(trimmed[idx+1:])
			headers[key] = val
			lastKey = key
		}
	}

	if err := wb.Flush(); err != nil {
		db.Close()
		return nil, fmt.Errorf("badger flush: %w", err)
	}

	fmt.Printf("[maildb] mboxImport: badger flush complete, %d messages stored\n", count)
	notify(fmt.Sprintf("Import complete: %d messages in database", count))

	// Signal that no more fti requests will be sent.
	tracker.close()

	return db, nil
}

// storeParsedMessage writes message metadata + body to BadgerDB, then
// fires off a non-blocking IndexDocument to fti. The tracker goroutine
// handles completion and page cleanup.
func storeParsedMessage(wb *badger.WriteBatch, headers map[string]string, body string, ftiSID int, tracker *ftiTracker) error {
	messageId := strings.Trim(headers["message-id"], "<> ")
	if messageId == "" {
		return fmt.Errorf("no message-id")
	}

	from := headers["from"]
	sender := headers["sender"]
	if sender == "" {
		sender = from
	}
	subject := headers["subject"]
	date := headers["date"]

	msg := shared.MailMessage{
		MessageId: messageId,
		From:      from,
		Sender:    sender,
		Subject:   subject,
		BodyLen:   len(body),
	}
	if date != "" {
		msg.Timestamp, _ = mail.ParseDate(date)
	}

	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	// Write metadata to badger.
	if err := wb.Set([]byte(messageId), data); err != nil {
		return fmt.Errorf("badger meta: %w", err)
	}

	// Write body to badger.
	if len(body) > 0 {
		if err := wb.Set([]byte("body:"+messageId), []byte(body)); err != nil {
			return fmt.Errorf("badger body: %w", err)
		}
	}

	// Write date index key.
	if !msg.Timestamp.IsZero() {
		dateKey := fmt.Sprintf("date:%s:%s", msg.Timestamp.UTC().Format("2006-01-02T15:04:05.000000000Z"), messageId)
		if err := wb.Set([]byte(dateKey), nil); err != nil {
			return fmt.Errorf("badger date: %w", err)
		}
	}

	fmt.Printf("[maildb] badger: stored %s (%s — %s)\n", messageId, from, subject)

	// Send to fti (non-blocking — tracker handles completion).
	if err := fireToFTI(messageId, subject, from, sender, date, body, ftiSID, tracker); err != nil {
		// FTI send failure is non-fatal — message is in badger, just not indexed.
		fmt.Printf("[maildb] fti send failed (non-fatal): %v\n", err)
	}

	return nil
}

// fireToFTI allocates shared pages, writes the SharedPageHeader + fields +
// body, shares the pages with fti, and sends an IndexDocument request.
// Does NOT wait for a response — the ftiTracker handles that.
func fireToFTI(messageId, subject, from, sender, date, body string, ftiSID int, tracker *ftiTracker) error {
	headerAndFields := fti.SharedPageHeaderSize + len(subject) + len(from) + len(sender) + len(date)
	totalBytes := headerAndFields + len(body)
	numPages := (totalBytes + 4095) / 4096
	if numPages == 0 {
		numPages = 1
	}

	pages, err := mem.AllocPagesSlice(numPages, mem.PageShared)
	if err != nil {
		return fmt.Errorf("AllocPages(%d): %w", numPages, err)
	}
	pagePtr := unsafe.Pointer(&pages[0])

	bodyOffset := fti.WriteSharedPageHeader(pages, subject, from, sender, date)
	copy(pages[bodyOffset:], body)

	targetVA, err := sys.SharePagesWithTarget(ftiSID, uintptr(pagePtr), numPages)
	if err != nil {
		mem.FreePages(pagePtr, numPages)
		return fmt.Errorf("SharePagesWithTarget: %w", err)
	}

	// Register with tracker BEFORE sending so the response can't arrive first.
	tracker.track(messageId, pagePtr, numPages)

	doc := fti.PackIndexDocument(
		fti.IndexTypeMailMessage,
		messageId,
		uint32(len(body)),
		uint32(numPages),
		uint64(targetVA),
	)
	ipcMsg := fti.EncodeIndexDocument(&doc)
	if err := uring.Send(ftiSID, &ipcMsg); err != nil {
		// Undo the track — we never sent.
		tracker.mu.Lock()
		delete(tracker.pending, messageId)
		tracker.inflight--
		tracker.mu.Unlock()
		mem.FreePages(pagePtr, numPages)
		return fmt.Errorf("uring.Send: %w", err)
	}

	return nil
}
