package main

import (
	"encoding/json"
	"sync"

	badger "github.com/dgraph-io/badger/v4"

	"mazzy/maz/maildb/shared"
)

// MessageRecord is one entry in the MessageStore.
// Fields are loaded lazily from badger on first access.
type MessageRecord struct {
	mu        sync.Mutex
	messageId string

	headers   *shared.MailMessage
	bodyText  []byte
	bodyHTML  []byte
	isRead    *bool // nil = not yet checked
	isDeleted *bool
}

// MessageStore is a lazy data cache keyed by messageId. It deduplicates
// badger reads when the same message is accessed across multiple collections.
// It holds no membership tracking — that lives in collection.msgIdToNum.
type MessageStore struct {
	mu      sync.RWMutex
	db      *badger.DB
	records map[string]*MessageRecord
}

func newMessageStore(db *badger.DB) *MessageStore {
	return &MessageStore{
		db:      db,
		records: make(map[string]*MessageRecord),
	}
}

// Ensure creates a MessageRecord for msgId if one does not already exist.
func (ms *MessageStore) Ensure(msgId string) {
	ms.mu.Lock()
	if _, ok := ms.records[msgId]; !ok {
		ms.records[msgId] = &MessageRecord{messageId: msgId}
	}
	ms.mu.Unlock()
}

// record returns the MessageRecord for msgId, creating it if absent.
func (ms *MessageStore) record(msgId string) *MessageRecord {
	ms.mu.Lock()
	r, ok := ms.records[msgId]
	if !ok {
		r = &MessageRecord{messageId: msgId}
		ms.records[msgId] = r
	}
	ms.mu.Unlock()
	return r
}

// LoadHeaders returns the metadata headers for msgId, fetching from badger
// on first call. The returned pointer is owned by the store; do not modify.
func (ms *MessageStore) LoadHeaders(msgId string) (*shared.MailMessage, error) {
	r := ms.record(msgId)
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.headers != nil {
		return r.headers, nil
	}
	var msg shared.MailMessage
	err := ms.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get([]byte(msgId))
		if err != nil {
			return err
		}
		return item.Value(func(val []byte) error {
			return json.Unmarshal(val, &msg)
		})
	})
	if err != nil {
		return nil, err
	}
	r.headers = &msg
	return r.headers, nil
}

// LoadFlags returns the read/deleted status for msgId.
// Caches the result; subsequent calls return the cached value.
func (ms *MessageStore) LoadFlags(msgId string) (isRead, isDeleted bool, err error) {
	r := ms.record(msgId)
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.isRead != nil && r.isDeleted != nil {
		return *r.isRead, *r.isDeleted, nil
	}
	var read, deleted bool
	err = ms.db.View(func(txn *badger.Txn) error {
		_, e := txn.Get([]byte("read:" + msgId))
		read = e == nil
		_, e = txn.Get([]byte("deleted:" + msgId))
		deleted = e == nil
		return nil
	})
	if err != nil {
		return false, false, err
	}
	r.isRead = &read
	r.isDeleted = &deleted
	return read, deleted, nil
}

// LoadBodyVariant returns the decoded body bytes for one variant
// (mailproto.BodyVariantText or BodyVariantHTML). The first call fetches
// from badger and caches in the MessageRecord; subsequent calls are served
// from memory. The returned slice is owned by the store; treat as read-only.
//
// Returns badger.ErrKeyNotFound if the requested variant doesn't exist for
// this message (e.g. a plain-text-only message has no html variant).
func (ms *MessageStore) LoadBodyVariant(msgId string, variant uint32) ([]byte, error) {
	r := ms.record(msgId)
	r.mu.Lock()
	defer r.mu.Unlock()

	cached := variantSlot(r, variant)
	if *cached != nil {
		return *cached, nil
	}

	key := variantKey(variant, msgId)
	var body []byte
	err := ms.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get([]byte(key))
		if err != nil {
			return err
		}
		body, err = item.ValueCopy(nil)
		return err
	})
	if err != nil {
		return nil, err
	}
	*cached = body
	return body, nil
}

// variantSlot returns the cache pointer for the given variant within a
// MessageRecord so LoadBodyVariant can read/write either field uniformly.
func variantSlot(r *MessageRecord, variant uint32) *[]byte {
	if variant == 1 { // BodyVariantHTML — avoid importing mailproto for one constant
		return &r.bodyHTML
	}
	return &r.bodyText
}

// variantKey returns the badger key for the given variant + message id.
// Mirrors the keys written by storeParsedMessage in mbox_import.go.
func variantKey(variant uint32, msgId string) string {
	if variant == 1 {
		return "body:html:" + msgId
	}
	return "body:text:" + msgId
}

// MarkRead marks the message as read and persists `read:<msgId>` to badger.
// Also decrements the count:unread counter if the message was previously unread.
func (ms *MessageStore) MarkRead(msgId string) error {
	r := ms.record(msgId)
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.isRead != nil && *r.isRead {
		return nil
	}
	t := true
	r.isRead = &t
	return ms.db.Update(func(txn *badger.Txn) error {
		_, readErr := txn.Get([]byte("read:" + msgId))
		wasUnread := readErr == badger.ErrKeyNotFound
		if err := txn.Set([]byte("read:"+msgId), nil); err != nil {
			return err
		}
		if wasUnread {
			return adjustCounterTxn(txn, keyCountUnread, -1)
		}
		return nil
	})
}

// MarkDeleted marks the message as deleted and persists `deleted:<msgId>` to
// badger. Decrements count:all, and count:unread if the message was unread.
// Callers are responsible for iterating collections and sending CollectionRemove
// notifications.
func (ms *MessageStore) MarkDeleted(msgId string) error {
	r := ms.record(msgId)
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.isDeleted != nil && *r.isDeleted {
		return nil
	}
	t := true
	r.isDeleted = &t
	return ms.db.Update(func(txn *badger.Txn) error {
		_, readErr := txn.Get([]byte("read:" + msgId))
		wasUnread := readErr == badger.ErrKeyNotFound
		if err := txn.Set([]byte("deleted:"+msgId), nil); err != nil {
			return err
		}
		if err := adjustCounterTxn(txn, keyCountAll, -1); err != nil {
			return err
		}
		if wasUnread {
			return adjustCounterTxn(txn, keyCountUnread, -1)
		}
		return nil
	})
}

// Evict removes the MessageRecord for msgId from the cache.
// Called when all collections that referenced msgId have been evicted.
func (ms *MessageStore) Evict(msgId string) {
	ms.mu.Lock()
	delete(ms.records, msgId)
	ms.mu.Unlock()
}
