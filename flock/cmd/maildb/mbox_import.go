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
	"mazzy/flock/cmd/maildb/shared"
)

// mboxImport parses the mbox file inside mboxDir and writes each message's
// headers into a BadgerDB database at dbDir. Progress strings are sent on
// statusCh so the UI can display them.
func mboxImport(mboxDir string, dbDir string, statusCh chan<- string) error {
	mboxPath := mboxDir + "/mbox"

	f, err := os.Open(mboxPath)
	if err != nil {
		return fmt.Errorf("open mbox %s: %w", mboxPath, err)
	}
	defer f.Close()

	opts := badger.DefaultOptions(dbDir).WithLogger(nil).WithBypassLockGuard(true)
	db, err := badger.Open(opts)
	if err != nil {
		return fmt.Errorf("open badger at %s: %w", dbDir, err)
	}
	defer db.Close()

	statusCh <- "Starting mbox import..."

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
			return fmt.Errorf("read mbox: %w", err)
		}
		atEOF := err == io.EOF

		// Trim trailing newline for easier comparison.
		trimmed := strings.TrimRight(line, "\r\n")

		if strings.HasPrefix(trimmed, "From ") || atEOF {
			// Flush previous message if we collected any headers.
			if len(headers) > 0 {
				if storeErr := storeParsedHeaders(wb, headers); storeErr == nil {
					count++
					if count%100 == 0 {
						statusCh <- fmt.Sprintf("Imported %d messages...", count)
					}
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
		return fmt.Errorf("badger flush: %w", err)
	}

	statusCh <- fmt.Sprintf("Import complete: %d messages", count)
	return nil
}

// storeParsedHeaders converts a header map into a MailMessage and writes it
// to the BadgerDB WriteBatch keyed by Message-ID.
func storeParsedHeaders(wb *badger.WriteBatch, headers map[string]string) error {
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
	return wb.Set([]byte(messageId), data)
}
