package main

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	badger "github.com/dgraph-io/badger/v4"

	"mazzy/shared/mailproto"
)

const (
	maxCollections  = 16
	maxWindowLoad   = 128 // max entries loaded per loadWindow call
	dateKeyPrefixLen = 36  // "date:" (5) + "2006-01-02T15:04:05.000000000Z" (30) + ":" (1)
)

// errCollectionExpired is returned when a collId is not in the live set.
var errCollectionExpired = errors.New("collection expired")

// collection is a HOT named result set: a sparse array of msgNum → messageId.
// Only entries that have been explicitly loaded via loadWindow are held in memory.
// totalSize is always known from the persistent counter (All/Unread) or a key scan.
type collection struct {
	id         uint32
	filterType uint32
	sortOrder  uint32
	filterArg  [64]byte
	totalSize  int
	lastUsed   time.Time
	subscribers []int16 // SIDs to notify on CollectionAdd/Remove

	// Sparse: only loaded entries present.
	entries    map[uint32]string // msgNum → messageId
	msgIdToNum map[string]uint32 // reverse: messageId → msgNum
}

// collectionNotify carries the data needed to send a CollectionRemove notification.
// The caller (uring handler in Phase 3) sends one per affected subscription.
type collectionNotify struct {
	collId  uint32
	msgNum  uint32
	newSize int
	msgId   string
	reqId   [16]byte
	sids    []int16
}

// collectionStore is a fixed-size (16-slot) LRU pool of live collections.
type collectionStore struct {
	mu     sync.Mutex
	db     *badger.DB
	store  *MessageStore
	slots  [maxCollections]*collection
	nextId uint32
}

func newCollectionStore(db *badger.DB, store *MessageStore) *collectionStore {
	return &collectionStore{
		db:     db,
		store:  store,
		nextId: 1,
	}
}

// createCollection creates a new collection with the given filter and sort order.
// For FilterAll and FilterUnread, totalSize is read from the persistent counter.
// For FilterFrom and FilterSubject, totalSize is 0 (fti count query happens in Phase 3).
// The collection is added to the LRU pool; the oldest is evicted if the pool is full.
// subscriberSID is the SID of the client that will receive CollectionAdd/Remove notifications.
func (cs *collectionStore) createCollection(filterType, sortOrder uint32, filterArg [64]byte, subscriberSID int16) (*collection, error) {
	var totalSize int
	switch filterType {
	case mailproto.FilterAll:
		n, err := readCounter(cs.db, keyCountAll)
		if err != nil {
			return nil, fmt.Errorf("createCollection: count:all: %w", err)
		}
		totalSize = int(n)
	case mailproto.FilterUnread:
		n, err := readCounter(cs.db, keyCountUnread)
		if err != nil {
			return nil, fmt.Errorf("createCollection: count:unread: %w", err)
		}
		totalSize = int(n)
	case mailproto.FilterFrom, mailproto.FilterSubject:
		// TODO Phase 3: send SearchMail{Size=0} to fti to get total count.
		totalSize = 0
	default:
		return nil, fmt.Errorf("createCollection: unknown filterType %d", filterType)
	}

	cs.mu.Lock()
	defer cs.mu.Unlock()

	// Find a free slot; if none, evict the LRU.
	slot := cs.findFreeSlotLocked()
	if slot < 0 {
		slot = cs.evictLRULocked()
	}

	coll := &collection{
		id:          cs.nextId,
		filterType:  filterType,
		sortOrder:   sortOrder,
		filterArg:   filterArg,
		totalSize:   totalSize,
		lastUsed:    time.Now(),
		subscribers: []int16{subscriberSID},
		entries:     make(map[uint32]string),
		msgIdToNum:  make(map[string]uint32),
	}
	cs.nextId++
	cs.slots[slot] = coll
	return coll, nil
}

// lookupCollection returns the collection for collId, updating its lastUsed.
// Returns errCollectionExpired if the collId is not in the live set.
func (cs *collectionStore) lookupCollection(collId uint32) (*collection, error) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	for _, coll := range cs.slots {
		if coll != nil && coll.id == collId {
			coll.lastUsed = time.Now()
			return coll, nil
		}
	}
	return nil, errCollectionExpired
}

// loadWindow ensures that entries [from, to] are loaded in coll.entries.
// It performs a key-only badger scan: no values are fetched from badger,
// only the date-index keys (which encode the messageId).
//
// For FilterAll and FilterUnread: uses the date: key index in badger.
// For FilterFrom and FilterSubject: returns an error (fti path — Phase 3).
//
// Each loaded messageId is registered in ms.Ensure so the MessageStore
// can lazy-fetch headers/flags/body when requested.
//
// The window size is capped at maxWindowLoad (128) entries per call.
// Must be called with cs.mu NOT held (it takes no lock itself; coll must
// not be accessed concurrently).
func (cs *collectionStore) loadWindow(coll *collection, from, to uint32) error {
	if to < from {
		return nil
	}
	if to-from+1 > maxWindowLoad {
		to = from + maxWindowLoad - 1
	}

	switch coll.filterType {
	case mailproto.FilterAll:
		return cs.loadWindowAll(coll, from, to)
	case mailproto.FilterUnread:
		return cs.loadWindowUnread(coll, from, to)
	case mailproto.FilterFrom, mailproto.FilterSubject:
		// TODO Phase 3: send SearchMail to fti, receive SearchResultEntry page.
		return fmt.Errorf("loadWindow: fti-backed filters not yet implemented (Phase 3)")
	default:
		return fmt.Errorf("loadWindow: unknown filterType %d", coll.filterType)
	}
}

// loadWindowAll loads entries [from, to] for a FilterAll collection.
// Iterates the date: key index in reverse (SortDesc) or forward (SortAsc) order.
func (cs *collectionStore) loadWindowAll(coll *collection, from, to uint32) error {
	return cs.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.PrefetchValues = false
		opts.Prefix = []byte("date:")
		opts.Reverse = coll.sortOrder == mailproto.SortDesc

		it := txn.NewIterator(opts)
		defer it.Close()

		if coll.sortOrder == mailproto.SortDesc {
			it.Seek([]byte("date:\xff"))
		} else {
			it.Seek([]byte("date:"))
		}

		var idx uint32
		for ; it.Valid(); it.Next() {
			if idx > to {
				break
			}
			msgId := extractMsgIdFromDateKey(string(it.Item().Key()))
			if msgId == "" {
				continue
			}
			if idx >= from {
				if _, loaded := coll.entries[idx]; !loaded {
					coll.entries[idx] = msgId
					coll.msgIdToNum[msgId] = idx
					cs.store.Ensure(msgId)
				}
			}
			idx++
		}
		return nil
	})
}

// loadWindowUnread loads entries [from, to] for a FilterUnread collection.
// Iterates the date: key index and checks read:<msgId> presence for each entry.
func (cs *collectionStore) loadWindowUnread(coll *collection, from, to uint32) error {
	return cs.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.PrefetchValues = false
		opts.Prefix = []byte("date:")
		opts.Reverse = coll.sortOrder == mailproto.SortDesc

		it := txn.NewIterator(opts)
		defer it.Close()

		if coll.sortOrder == mailproto.SortDesc {
			it.Seek([]byte("date:\xff"))
		} else {
			it.Seek([]byte("date:"))
		}

		var unreadIdx uint32
		for ; it.Valid(); it.Next() {
			if unreadIdx > to {
				break
			}
			msgId := extractMsgIdFromDateKey(string(it.Item().Key()))
			if msgId == "" {
				continue
			}
			// Skip messages that have been read.
			_, readErr := txn.Get([]byte("read:" + msgId))
			if readErr == nil {
				continue // has read: key → skip
			}
			if unreadIdx >= from {
				if _, loaded := coll.entries[unreadIdx]; !loaded {
					coll.entries[unreadIdx] = msgId
					coll.msgIdToNum[msgId] = unreadIdx
					cs.store.Ensure(msgId)
				}
			}
			unreadIdx++
		}
		return nil
	})
}

// removeMessage removes msgId from all collections that have it loaded.
// Returns a list of notifications to send (one per affected collection).
// Does NOT send any uring messages — that is the caller's responsibility.
func (cs *collectionStore) removeMessage(msgId string, reqId [16]byte) []collectionNotify {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	var notifs []collectionNotify
	for _, coll := range cs.slots {
		if coll == nil {
			continue
		}
		msgNum, ok := coll.msgIdToNum[msgId]
		if !ok {
			continue
		}
		coll.entries[msgNum] = "" // tombstone: keep slot, clear messageId
		delete(coll.msgIdToNum, msgId)
		coll.totalSize--
		if coll.totalSize < 0 {
			coll.totalSize = 0
		}
		if len(coll.subscribers) > 0 {
			sids := make([]int16, len(coll.subscribers))
			copy(sids, coll.subscribers)
			notifs = append(notifs, collectionNotify{
				collId:  coll.id,
				msgNum:  msgNum,
				newSize: coll.totalSize,
				msgId:   msgId,
				reqId:   reqId,
				sids:    sids,
			})
		}
	}
	return notifs
}

// findFreeSlotLocked returns the index of a nil slot, or -1 if all are occupied.
// Must be called with cs.mu held.
func (cs *collectionStore) findFreeSlotLocked() int {
	for i, s := range cs.slots {
		if s == nil {
			return i
		}
	}
	return -1
}

// evictLRULocked evicts the least-recently-used collection and returns its slot index.
// Also evicts MessageStore records that are no longer referenced by any collection.
// Must be called with cs.mu held.
func (cs *collectionStore) evictLRULocked() int {
	oldest := 0
	for i := 1; i < maxCollections; i++ {
		if cs.slots[i] != nil && cs.slots[oldest] != nil &&
			cs.slots[i].lastUsed.Before(cs.slots[oldest].lastUsed) {
			oldest = i
		}
	}
	if cs.slots[oldest] == nil {
		return 0
	}
	evicted := cs.slots[oldest]
	cs.slots[oldest] = nil

	// Evict MessageStore records that are no longer in any live collection.
	for msgId := range evicted.msgIdToNum {
		if !cs.isLoadedInAnySlotLocked(msgId) {
			cs.store.Evict(msgId)
		}
	}
	return oldest
}

// isLoadedInAnySlotLocked returns true if msgId appears in any live collection's
// msgIdToNum map other than the one being evicted. Must be called with cs.mu held.
func (cs *collectionStore) isLoadedInAnySlotLocked(msgId string) bool {
	for _, coll := range cs.slots {
		if coll == nil {
			continue
		}
		if _, ok := coll.msgIdToNum[msgId]; ok {
			return true
		}
	}
	return false
}

// extractMsgIdFromDateKey extracts the messageId from a "date:<timestamp>:<msgId>" key.
// The timestamp is always exactly 30 bytes ("2006-01-02T15:04:05.000000000Z"),
// so the messageId starts at byte offset 36 (5 + 30 + 1).
func extractMsgIdFromDateKey(key string) string {
	if len(key) <= dateKeyPrefixLen {
		return ""
	}
	if !strings.HasPrefix(key, "date:") {
		return ""
	}
	return key[dateKeyPrefixLen:]
}
