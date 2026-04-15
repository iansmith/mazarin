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
	"time"
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
	display  string // short human-readable description (from — subject)
}

// ftiQueueItem holds the data needed to send one IndexDocument request to fti.
type ftiQueueItem struct {
	messageId string
	subject   string
	from      string
	sender    string
	date      string
	body      string
	display   string
}

// ftiTracker manages fti IndexDocument requests. It accepts items into a queue
// and sends them one at a time, waiting for each confirmation before sending
// the next.
type ftiTracker struct {
	mu      sync.Mutex
	pending map[string]pendingIndex // docId -> shared page info

	queue  chan ftiQueueItem // incoming items to index
	done   chan struct{}     // closed when all items are indexed
	ftiSID int
}

func newFTITracker(ftiRespCh <-chan any, ftiSID int, notify func(string)) *ftiTracker {
	t := &ftiTracker{
		pending: make(map[string]pendingIndex),
		queue:   make(chan ftiQueueItem, 256),
		done:    make(chan struct{}),
		ftiSID:  ftiSID,
	}
	go t.sendLoop(ftiRespCh, notify)
	return t
}

// enqueue adds an item to the indexing queue.
func (t *ftiTracker) enqueue(item ftiQueueItem) {
	t.queue <- item
}

// close signals that no more items will be enqueued. The send loop will
// drain remaining items and close t.done when finished.
func (t *ftiTracker) close() {
	close(t.queue)
}

// wait blocks until all queued items have been indexed.
func (t *ftiTracker) wait() {
	<-t.done
}

// sendLoop reads items from the queue one at a time, sends each to fti,
// and waits for the confirmation response before sending the next.
func (t *ftiTracker) sendLoop(ftiRespCh <-chan any, notify func(string)) {
	defer close(t.done)

	var totalBytes int64
	var totalDur time.Duration
	count := 0

	for item := range t.queue {
		bodyLen := len(item.body)
		t0 := time.Now()
		if err := fireToFTI(item.messageId, item.subject, item.from, item.sender, item.date, item.body, t.ftiSID, t, item.display); err != nil {
			fmt.Printf("[maildb] fti send failed (non-fatal): %v\n", err)
			continue
		}
		// Wait for fti to confirm this item before sending the next.
		t.waitForOne(ftiRespCh, notify)
		elapsed := time.Since(t0)

		count++
		totalBytes += int64(bodyLen)
		totalDur += elapsed

		mbps := float64(bodyLen) / elapsed.Seconds() / (1024 * 1024)
		fmt.Printf("[maildb] fti: indexed %d/%d (%d bytes in %v, %.2f MB/s, cumulative %.2f MB/s)\n",
			count, count, bodyLen, elapsed.Round(time.Microsecond),
			mbps, float64(totalBytes)/totalDur.Seconds()/(1024*1024))
	}

	if count > 0 {
		fmt.Printf("[maildb] fti: complete — %d docs, %d bytes in %v (%.2f MB/s avg)\n",
			count, totalBytes, totalDur.Round(time.Millisecond),
			float64(totalBytes)/totalDur.Seconds()/(1024*1024))
	}
}

// waitForOne blocks until one fti response arrives, frees its shared pages,
// and notifies the UI.
func (t *ftiTracker) waitForOne(ftiRespCh <-chan any, notify func(string)) {
	resp, ok := <-ftiRespCh
	if !ok {
		return
	}

	var docId string
	var errMsg string

	switch r := resp.(type) {
	case fti.IndexingCompleted:
		docId = fti.UnpackCompletedId(&r)
	case fti.IndexError:
		docId, errMsg = fti.UnpackIndexError(&r)
		fmt.Printf("[maildb] fti error: %s: %s\n", docId, errMsg)
		notify(fmt.Sprintf("Index error: %s", errMsg))
	default:
		fmt.Printf("[maildb] unexpected fti response: %T\n", resp)
		return
	}

	t.mu.Lock()
	if pi, ok := t.pending[docId]; ok {
		if errMsg == "" {
			notify(fmt.Sprintf("Indexed: %s", pi.display))
		}
		mem.FreePages(pi.pagePtr, pi.numPages)
		delete(t.pending, docId)
	}
	t.mu.Unlock()
}

// mboxImport parses the mbox file inside mboxDir and writes each message's
// headers + body into BadgerDB. Each message body is also enqueued for
// full-text indexing via the ftiTracker (sent one at a time).
//
// Returns the open db immediately after badger flush. FTI indexing continues
// in the background via the ftiTracker.
func mboxImport(mboxDir string, tracker *ftiTracker, notify func(string)) (*badger.DB, error) {
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
				if storeErr := storeParsedMessage(wb, headers, bodyStr, tracker, notify); storeErr != nil {
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

	// Signal that no more fti requests will be enqueued.
	tracker.close()

	return db, nil
}

// storeParsedMessage writes message metadata + body to BadgerDB, then
// enqueues the message for fti indexing. The tracker sends items one at
// a time and handles completion/page cleanup.
func storeParsedMessage(wb *badger.WriteBatch, headers map[string]string, body string, tracker *ftiTracker, notify func(string)) error {
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

	// Build a short display string for UI notifications.
	display := from
	if subject != "" {
		display += " — " + subject
	}

	fmt.Printf("[maildb] badger: stored %s (%s)\n", messageId, display)
	notify(fmt.Sprintf("Stored: %s", display))

	// Enqueue for fti indexing (tracker sends one at a time).
	tracker.enqueue(ftiQueueItem{
		messageId: messageId,
		subject:   subject,
		from:      from,
		sender:    sender,
		date:      date,
		body:      body,
		display:   display,
	})

	return nil
}

// fireToFTI allocates shared pages, writes the SharedPageHeader + fields +
// body, shares the pages with fti, and sends an IndexDocument request.
// Does NOT wait for a response — the ftiTracker handles that.
func fireToFTI(messageId, subject, from, sender, date, body string, ftiSID int, tracker *ftiTracker, display string) error {
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
	tracker.mu.Lock()
	tracker.pending[messageId] = pendingIndex{pagePtr: pagePtr, numPages: numPages, display: display}
	tracker.mu.Unlock()

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
		tracker.mu.Unlock()
		mem.FreePages(pagePtr, numPages)
		return fmt.Errorf("uring.Send: %w", err)
	}

	return nil
}
