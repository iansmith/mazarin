package main

import (
	"encoding/binary"

	badger "github.com/dgraph-io/badger/v4"
)

// Persistent counter keys in badger. Values are little-endian uint64.
const (
	keyCountAll    = "count:all"    // incremented on import, decremented on delete
	keyCountUnread = "count:unread" // incremented on import, decremented on markRead/delete-if-unread
)

// readCounter reads a persistent counter from badger.
// Returns 0 if the key does not exist.
func readCounter(db *badger.DB, key string) (uint64, error) {
	var val uint64
	err := db.View(func(txn *badger.Txn) error {
		var err error
		val, err = readCounterTxn(txn, key)
		return err
	})
	return val, err
}

// readCounterTxn reads a persistent counter within an existing transaction.
func readCounterTxn(txn *badger.Txn, key string) (uint64, error) {
	item, err := txn.Get([]byte(key))
	if err == badger.ErrKeyNotFound {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	var val uint64
	err = item.Value(func(b []byte) error {
		if len(b) >= 8 {
			val = binary.LittleEndian.Uint64(b)
		}
		return nil
	})
	return val, err
}

// setCounter writes a persistent counter in its own transaction.
func setCounter(db *badger.DB, key string, val uint64) error {
	return db.Update(func(txn *badger.Txn) error {
		return setCounterTxn(txn, key, val)
	})
}

// setCounterTxn writes a persistent counter within an existing transaction.
func setCounterTxn(txn *badger.Txn, key string, val uint64) error {
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], val)
	return txn.Set([]byte(key), buf[:])
}

// adjustCounterTxn increments or decrements a counter within an existing
// transaction. The counter never goes below zero.
func adjustCounterTxn(txn *badger.Txn, key string, delta int64) error {
	cur, err := readCounterTxn(txn, key)
	if err != nil {
		return err
	}
	next := int64(cur) + delta
	if next < 0 {
		next = 0
	}
	return setCounterTxn(txn, key, uint64(next))
}

// initCounters sets count:all and count:unread to the given values.
// Called once after mbox import completes.
func initCounters(db *badger.DB, total, unread int) error {
	return db.Update(func(txn *badger.Txn) error {
		if err := setCounterTxn(txn, keyCountAll, uint64(total)); err != nil {
			return err
		}
		return setCounterTxn(txn, keyCountUnread, uint64(unread))
	})
}
