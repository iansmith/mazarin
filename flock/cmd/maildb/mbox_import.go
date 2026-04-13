package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/mail"
	"os"
	"strings"

	badger "github.com/dgraph-io/badger/v4"
	"github.com/blevesearch/bleve/v2"
	"mazzy/flock/cmd/maildb/shared"
)

// bleveDoc is the document structure indexed by bleve.
type bleveDoc struct {
	MessageId string
	From      string
	Sender    string
	Subject   string
	Date      string
}

// mboxImport parses the mbox file inside mboxDir and writes each message's
// headers into a BadgerDB database at dbDir and a bleve full-text index
// at ftiDir. Progress strings are sent via notify so the UI can display them.
// Returns the open index and db for subsequent queries; caller must close them.
func mboxImport(mboxDir string, dbDir string, notify func(string)) (bleve.Index, *badger.DB, error) {
	mboxPath := mboxDir + "/mbox"

	fmt.Printf("[maildb] mboxImport: opening %s\n", mboxPath)
	f, err := os.Open(mboxPath)
	if err != nil {
		return nil, nil, fmt.Errorf("open mbox %s: %w", mboxPath, err)
	}
	defer f.Close()
	fmt.Println("[maildb] mboxImport: mbox opened, opening badger")

	opts := badger.DefaultOptions(dbDir).WithLogger(nil).WithBypassLockGuard(true)
	db, err := badger.Open(opts)
	if err != nil {
		return nil, nil, fmt.Errorf("open badger at %s: %w", dbDir, err)
	}
	fmt.Println("[maildb] mboxImport: badger opened, opening bleve")

	// In-memory bleve index. On-disk bbolt requires file-backed mmap
	// coherence (pwrite data visible through mmap), which is not yet
	// implemented. Since /tmp is a ramdisk and we rebuild from the mbox
	// on every boot, in-memory is the right fit.
	mapping := bleve.NewIndexMapping()
	index, err := bleve.NewMemOnly(mapping)
	if err != nil {
		db.Close()
		return nil, nil, fmt.Errorf("create bleve index: %w", err)
	}
	fmt.Println("[maildb] mboxImport: bleve ready, starting parse")

	notify("Starting mbox import...")

	reader := bufio.NewReaderSize(f, 64*1024)
	count := 0
	inHeaders := false
	headers := make(map[string]string)
	var lastKey string

	wb := db.NewWriteBatch()

	for {
		line, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			wb.Cancel()
			index.Close()
			db.Close()
			return nil, nil, fmt.Errorf("read mbox: %w", err)
		}
		atEOF := err == io.EOF

		// Trim trailing newline for easier comparison.
		trimmed := strings.TrimRight(line, "\r\n")

		if strings.HasPrefix(trimmed, "From ") || atEOF {
			// Flush previous message if we collected any headers.
			if len(headers) > 0 {
				if storeErr := storeParsedHeaders(wb, index, headers); storeErr == nil {
					count++
					sender := headers["from"]
					subject := headers["subject"]
					notify(fmt.Sprintf("%d: %s — %s", count, sender, subject))
				}
				headers = make(map[string]string)
				lastKey = ""
			}
			if atEOF {
				break
			}
			inHeaders = true
			continue
		}

		if !inHeaders {
			continue
		}

		// Blank line terminates headers.
		if trimmed == "" {
			inHeaders = false
			continue
		}

		// Header continuation line (leading whitespace).
		if (trimmed[0] == ' ' || trimmed[0] == '\t') && lastKey != "" {
			headers[lastKey] += " " + strings.TrimSpace(trimmed)
			continue
		}

		// New header field.
		if idx := strings.IndexByte(trimmed, ':'); idx > 0 {
			key := strings.ToLower(trimmed[:idx])
			val := strings.TrimSpace(trimmed[idx+1:])
			headers[key] = val
			lastKey = key
		}
	}

	if err := wb.Flush(); err != nil {
		index.Close()
		db.Close()
		return nil, nil, fmt.Errorf("badger flush: %w", err)
	}

	fmt.Printf("[maildb] mboxImport: flush complete, %d messages imported+indexed\n", count)
	notify(fmt.Sprintf("Import complete: %d messages indexed", count))
	return index, db, nil
}

// storeParsedHeaders converts a header map into a MailMessage, writes it
// to the BadgerDB WriteBatch keyed by Message-ID, and indexes it in bleve.
func storeParsedHeaders(wb *badger.WriteBatch, index bleve.Index, headers map[string]string) error {
	messageId := strings.Trim(headers["message-id"], "<> ")
	if messageId == "" {
		return fmt.Errorf("no message-id")
	}

	from := headers["from"]
	sender := headers["sender"]
	if sender == "" {
		sender = from
	}

	msg := shared.MailMessage{
		MessageId: messageId,
		From:      from,
		Sender:    sender,
		Subject:   headers["subject"],
	}
	if dateStr := headers["date"]; dateStr != "" {
		msg.Timestamp, _ = mail.ParseDate(dateStr)
	}

	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	if err := wb.Set([]byte(messageId), data); err != nil {
		return err
	}

	// Index in bleve for full-text search.
	doc := bleveDoc{
		MessageId: messageId,
		From:      from,
		Sender:    sender,
		Subject:   headers["subject"],
		Date:      headers["date"],
	}
	return index.Index(messageId, doc)
}
