package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/mail"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unsafe"

	badger "github.com/dgraph-io/badger/v4"

	"mazzy/maz/maildb/shared"
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

	queue        chan ftiQueueItem // incoming items to index
	done         chan struct{}     // closed when all items are indexed
	ftiSID       int
	ftiDied      chan struct{} // closed when fti shepherd dies
	ftiDiedOnce  sync.Once
	lastErrMsg   string // last error message sent to notify (dedup)
	lastErrCount int    // consecutive count of lastErrMsg
}

// MarkFTIDied signals that the fti shepherd has died. Safe to call multiple times.
func (t *ftiTracker) MarkFTIDied() {
	t.ftiDiedOnce.Do(func() { close(t.ftiDied) })
}

func newFTITracker(ftiRespCh <-chan any, ftiSID int, notify func(string)) *ftiTracker {
	t := &ftiTracker{
		pending: make(map[string]pendingIndex),
		queue:   make(chan ftiQueueItem, 256),
		done:    make(chan struct{}),
		ftiSID:  ftiSID,
		ftiDied: make(chan struct{}),
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

		// noise: per-doc fti-indexed timing trace disabled during scorch ENOENT investigation
		_ = bodyLen
		_ = elapsed
	}

	if count > 0 {
		fmt.Printf("[maildb] fti: complete — %d docs, %d bytes in %v (%.2f MB/s avg)\n",
			count, totalBytes, totalDur.Round(time.Millisecond),
			float64(totalBytes)/totalDur.Seconds()/(1024*1024))
	}
}

// waitForOne blocks until one fti response arrives (or fti dies), frees its
// shared pages, and notifies the UI.
func (t *ftiTracker) waitForOne(ftiRespCh <-chan any, notify func(string)) {
	var resp any
	var ok bool

	select {
	case resp, ok = <-ftiRespCh:
		if !ok {
			return
		}
	case <-t.ftiDied:
		fmt.Println("[maildb] fti died while waiting for index confirmation, skipping")
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
		if errMsg != t.lastErrMsg {
			t.lastErrMsg = errMsg
			t.lastErrCount = 1
			notify(fmt.Sprintf("Index error: %s", errMsg))
		} else {
			t.lastErrCount++
			if t.lastErrCount%50 == 0 {
				notify(fmt.Sprintf("Index error (%dx): %s", t.lastErrCount, errMsg))
			}
		}
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

// mailImport detects whether mailPath is a classic mbox file (single file
// containing concatenated RFC822 messages separated by "From " lines) or an
// Apple Mail emlx mailbox (a directory tree of one-message-per-file .emlx
// files), and dispatches to the appropriate parser.
//
// Both parsers ultimately call storeParsedMessage for each message, so the
// onFirstCommit / onMessage / FTI fan-out semantics are identical.
//
// onFirstCommit is called after the very first message is committed. The
// caller uses this to signal shepherd readiness while import continues.
//
// onMessage is called after each message is committed. The caller uses this
// to send incremental CollectionAdd notifications to live collections.
//
// Returns the open db immediately after badger flush. FTI indexing continues
// in the background via the ftiTracker.
func mailImport(mailPath string, tracker *ftiTracker, notify func(string),
	onFirstCommit func(db *badger.DB), onMessage func(msgId string, ts time.Time)) (*badger.DB, error) {
	info, err := os.Stat(mailPath)
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", mailPath, err)
	}

	db, err := openImportBadger()
	if err != nil {
		return nil, err
	}

	state := &importState{
		db:            db,
		tracker:       tracker,
		notify:        notify,
		onFirstCommit: onFirstCommit,
		onMessage:     onMessage,
	}

	if info.IsDir() {
		fmt.Printf("[maildb] mailImport: %s is a directory — using emlx walker\n", mailPath)
		notify("Starting emlx import...")
		if err := emlxImport(mailPath, state); err != nil {
			db.Close()
			return nil, err
		}
	} else {
		fmt.Printf("[maildb] mailImport: %s is a file — using mbox parser\n", mailPath)
		notify("Starting mbox import...")
		if err := mboxImport(mailPath, state); err != nil {
			db.Close()
			return nil, err
		}
	}

	fmt.Printf("[maildb] mailImport: import complete, %d messages stored\n", state.count)
	notify(fmt.Sprintf("Import complete: %d messages in database", state.count))

	// Initialise persistent counters. All newly imported messages are unread.
	if err := initCounters(db, state.count, state.count); err != nil {
		fmt.Printf("[maildb] WARNING: initCounters failed: %v\n", err)
	}

	// Signal that no more fti requests will be enqueued.
	tracker.close()

	return db, nil
}

// importState carries everything per-message storage needs into the format-
// specific parsers so they only have to focus on splitting the input into
// RFC822 byte ranges.
type importState struct {
	db            *badger.DB
	tracker       *ftiTracker
	notify        func(string)
	onFirstCommit func(db *badger.DB)
	onMessage     func(msgId string, ts time.Time)
	count         int
}

// importBadgerDir is the absolute filesystem path used by openImportBadger.
// Set once at maildb startup from sys.SetupScratchDir's return value, then
// used unchanged for the lifetime of the shepherd. We must use an absolute
// path because badger.Open calls os.Getwd internally to construct the pid
// lock file path, and the Mazzy linux delegate's getwd returns EFAULT
// even after a successful Chdir — passing "." or "./badger" makes badger's
// internal Getwd fail with "bad address".
var importBadgerDir string

// openImportBadger wipes any previous index directory and opens a fresh
// badger DB inside the maildb shepherd's scratch dir. The storage stays
// self-contained — no dependency on rachel's old /data → /tmp/data mirror.
// Shared by both mbox and emlx import paths.
func openImportBadger() (*badger.DB, error) {
	if importBadgerDir == "" {
		return nil, fmt.Errorf("importBadgerDir not set; call SetImportBadgerDir at startup")
	}
	os.RemoveAll(importBadgerDir)
	opts := badger.DefaultOptions(importBadgerDir).WithLogger(nil)
	db, err := badger.Open(opts)
	if err != nil {
		return nil, fmt.Errorf("open badger: %w", err)
	}
	return db, nil
}

// SetImportBadgerDir records the absolute path where openImportBadger
// will create the badger index. Call once at maildb startup with
// scratchDir + "/badger" (where scratchDir comes from sys.SetupScratchDir).
func SetImportBadgerDir(absPath string) {
	importBadgerDir = absPath
}

// storeRawRFC822 parses one RFC822 byte range into headers + body and hands
// it to storeParsedMessage, updating import state on success.
func storeRawRFC822(raw []byte, state *importState) {
	headers, body := parseRFC822Message(raw)
	if len(headers) == 0 {
		return
	}
	state.count++
	// noise: per-message parse trace disabled during scorch ENOENT investigation

	var storedMsgId string
	var storedTs time.Time
	if storeErr := storeParsedMessage(state.db, headers, body, state.tracker,
		state.notify, &storedMsgId, &storedTs); storeErr != nil {
		fmt.Printf("[maildb] store error: %v\n", storeErr)
		state.count--
		return
	}
	if state.count == 1 && state.onFirstCommit != nil {
		state.onFirstCommit(state.db)
	}
	if storedMsgId != "" && state.onMessage != nil {
		state.onMessage(storedMsgId, storedTs)
	}
}

// parseRFC822Message walks raw mail bytes once and returns the case-folded
// header map (continuation lines folded in) and the body string. The body
// starts immediately after the first blank line.
func parseRFC822Message(raw []byte) (map[string]string, string) {
	headers := make(map[string]string)
	var lastKey string
	r := bufio.NewReaderSize(bytes.NewReader(raw), 64*1024)
	inHeaders := true
	var body strings.Builder

	for {
		line, err := r.ReadString('\n')
		atEOF := err == io.EOF
		trimmed := strings.TrimRight(line, "\r\n")

		if inHeaders {
			if trimmed == "" {
				inHeaders = false
			} else if (trimmed[0] == ' ' || trimmed[0] == '\t') && lastKey != "" {
				headers[lastKey] += " " + strings.TrimSpace(trimmed)
			} else if idx := strings.IndexByte(trimmed, ':'); idx > 0 {
				key := strings.ToLower(trimmed[:idx])
				val := strings.TrimSpace(trimmed[idx+1:])
				headers[key] = val
				lastKey = key
			}
		} else {
			body.WriteString(line)
		}

		if atEOF {
			break
		}
		if err != nil {
			break
		}
	}
	return headers, body.String()
}

// mboxImport parses a classic mbox file, splitting on "From " separator
// lines and handing each message off to storeRawRFC822 via the shared
// importState.
func mboxImport(mboxPath string, state *importState) error {
	fmt.Printf("[maildb] mboxImport: opening %s\n", mboxPath)
	f, err := os.Open(mboxPath)
	if err != nil {
		return fmt.Errorf("open mbox %s: %w", mboxPath, err)
	}
	defer f.Close()
	fmt.Println("[maildb] mboxImport: mbox opened, starting parse")

	reader := bufio.NewReaderSize(f, 64*1024)
	var msgBuf bytes.Buffer

	flush := func() {
		if msgBuf.Len() > 0 {
			storeRawRFC822(msgBuf.Bytes(), state)
			msgBuf.Reset()
		}
	}

	for {
		line, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			return fmt.Errorf("read mbox: %w", err)
		}
		atEOF := err == io.EOF

		trimmed := strings.TrimRight(line, "\r\n")

		if strings.HasPrefix(trimmed, "From ") || atEOF {
			flush()
			if atEOF {
				break
			}
			continue
		}
		msgBuf.WriteString(line)
	}
	return nil
}

// emlxImport walks an Apple Mail mailbox tree (a directory containing
// per-message .emlx files in nested Data/N/N/N/Messages/ subdirectories),
// strips each file's length-prefix header and trailing property list, and
// hands the RFC822 payload to storeRawRFC822.
//
// emlx file layout:
//
//	"<decimal byte count>\n"   ← length of the message in bytes
//	<exactly that many bytes> ← the RFC822 message
//	<optional XML plist>      ← Apple Mail metadata, ignored here
func emlxImport(rootDir string, state *importState) error {
	fmt.Printf("[maildb] emlxImport: walking %s\n", rootDir)
	walked := 0
	parsed := 0

	err := filepath.Walk(rootDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			fmt.Printf("[maildb] emlxImport: walk error at %s: %v\n", path, err)
			return nil // continue walking siblings
		}
		if info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(path), ".emlx") {
			return nil
		}
		walked++

		raw, rerr := readEmlxMessage(path)
		if rerr != nil {
			fmt.Printf("[maildb] emlxImport: skip %s: %v\n", path, rerr)
			return nil
		}
		storeRawRFC822(raw, state)
		parsed++
		return nil
	})
	if err != nil {
		return fmt.Errorf("walk %s: %w", rootDir, err)
	}
	fmt.Printf("[maildb] emlxImport: walked %d .emlx files, parsed %d\n", walked, parsed)
	return nil
}

// readEmlxMessage opens an Apple Mail .emlx file, parses the leading
// decimal-length header, and returns exactly that many bytes of RFC822
// payload. The trailing property list is discarded.
func readEmlxMessage(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	reader := bufio.NewReaderSize(f, 64*1024)
	header, err := reader.ReadString('\n')
	if err != nil {
		return nil, fmt.Errorf("read length prefix: %w", err)
	}
	header = strings.TrimSpace(header)
	n, err := strconv.Atoi(header)
	if err != nil {
		return nil, fmt.Errorf("bad length prefix %q: %w", header, err)
	}
	if n <= 0 {
		return nil, fmt.Errorf("non-positive length %d", n)
	}

	buf := make([]byte, n)
	if _, err := io.ReadFull(reader, buf); err != nil {
		return nil, fmt.Errorf("read %d bytes: %w", n, err)
	}
	return buf, nil
}

// storeParsedMessage writes message metadata + body to BadgerDB in a single
// transaction, then enqueues the message for fti indexing. Using db.Update()
// (one transaction per message) instead of WriteBatch so each commit is
// immediately visible to subsequent reads (e.g. the date-index scan in addMessage).
// On success, *outMsgId is set to the stored message ID and *outTs to the timestamp.
func storeParsedMessage(db *badger.DB, headers map[string]string, body string, tracker *ftiTracker, notify func(string), outMsgId *string, outTs *time.Time) error {
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

	// MIME-parse the body once at import time. Both variants are decoded
	// (quoted-printable / base64) and stored separately so BodyReq can return
	// pre-decoded UTF-8 without re-parsing on each request.
	textBody, htmlBody := extractBodyVariants(headers, body)

	msg := shared.MailMessage{
		MessageId: messageId,
		From:      from,
		Sender:    sender,
		Subject:   subject,
		BodyLen:   len(textBody),
	}
	if date != "" {
		msg.Timestamp, _ = mail.ParseDate(date)
	}

	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	// Commit metadata + body variants + date-index in one transaction.
	// db.Update() creates a fresh read-write transaction, commits it, and
	// returns; the keys are immediately visible to subsequent db.View() calls.
	if err := db.Update(func(txn *badger.Txn) error {
		if err := txn.Set([]byte(messageId), data); err != nil {
			return fmt.Errorf("meta: %w", err)
		}
		if len(textBody) > 0 {
			if err := txn.Set([]byte("body:text:"+messageId), []byte(textBody)); err != nil {
				return fmt.Errorf("body:text: %w", err)
			}
		}
		if len(htmlBody) > 0 {
			if err := txn.Set([]byte("body:html:"+messageId), []byte(htmlBody)); err != nil {
				return fmt.Errorf("body:html: %w", err)
			}
		}
		if !msg.Timestamp.IsZero() {
			dateKey := fmt.Sprintf("date:%s:%s", msg.Timestamp.UTC().Format("2006-01-02T15:04:05.000000000Z"), messageId)
			if err := txn.Set([]byte(dateKey), nil); err != nil {
				return fmt.Errorf("date: %w", err)
			}
		}
		return nil
	}); err != nil {
		return fmt.Errorf("badger commit: %w", err)
	}

	// Signal the stored messageId and timestamp to the caller.
	if outMsgId != nil {
		*outMsgId = messageId
	}
	if outTs != nil {
		*outTs = msg.Timestamp
	}

	// Build a short display string for UI notifications.
	display := from
	if subject != "" {
		display += " — " + subject
	}

	// noise: per-message badger-store trace disabled during scorch ENOENT investigation
	_ = display
	notify(fmt.Sprintf("Stored: %s", display))

	// Enqueue for fti indexing (tracker sends one at a time).
	// Send the decoded text/plain variant — never the raw MIME envelope —
	// so the index sees real words instead of boundary markers, =3D escapes,
	// and HTML tags. Fall back to the HTML variant stripped to plain text
	// only if no text/plain part was extracted.
	ftiBody := textBody
	if ftiBody == "" {
		ftiBody = htmlBody
	}
	tracker.enqueue(ftiQueueItem{
		messageId: messageId,
		subject:   subject,
		from:      from,
		sender:    sender,
		date:      date,
		body:      ftiBody,
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
