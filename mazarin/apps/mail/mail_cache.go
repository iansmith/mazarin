package main

import (
	"fmt"
	"time"
	"unsafe"

	"mazzy/mazarin/mem"
	"mazzy/mazarin/uring"
	"mazzy/shared/mailproto"
)

const cacheReadAhead = 2

// MailCache is a sliding-window cache of KeyHeaderEntry records for one
// maildb collection. It owns all KeyHeaders IPC traffic; the UI layer
// calls Get synchronously and receives nil (show "…") for uncached rows.
//
// Rebalance must be called whenever the grid's visible range changes.
// HandleResponse must be called for every message arriving on mailRespCh.
type MailCache struct {
	maildbSID int

	collId   uint32
	collSize uint32

	entries  map[uint32]*mailproto.KeyHeaderEntry
	windowLo uint32
	windowHi uint32

	inFlight    bool
	inFlightId  [16]byte
	inFlightLo  uint32
	inFlightHi  uint32

	// OnUpdated is called after entries change so the grid can redraw.
	OnUpdated func()
	// OnExpired is called when maildb returns ErrCollectionExpired.
	OnExpired func(collId uint32)

	reqCounter uint64
}

// SetCollection resets the cache for a new collection.
func (c *MailCache) SetCollection(collId, collSize uint32) {
	c.collId = collId
	c.collSize = collSize
	c.entries = make(map[uint32]*mailproto.KeyHeaderEntry)
	c.windowLo = 0
	c.windowHi = 0
	c.inFlight = false
}

// Get returns the cached entry for msgNum, or nil if not yet loaded.
func (c *MailCache) Get(msgNum uint32) *mailproto.KeyHeaderEntry {
	return c.entries[msgNum]
}

// CollectionSize returns the current known collection size.
func (c *MailCache) CollectionSize() uint32 { return c.collSize }

// Rebalance recomputes the cache window from the current visible range and
// fires a fetch for any uncached entries in the new window.
func (c *MailCache) Rebalance(first, last, visCount int64) {
	if c.collSize == 0 || visCount == 0 {
		return
	}
	prefetch := int64(cacheReadAhead) * visCount
	lo := uint32(max(int64(0), first-prefetch))
	hi := uint32(min(int64(c.collSize)-1, last+prefetch))

	// Evict entries outside the new window.
	for k := range c.entries {
		if k < lo || k > hi {
			delete(c.entries, k)
		}
	}
	c.windowLo = lo
	c.windowHi = hi

	// Fetch if anything is missing.
	for k := lo; k <= hi; k++ {
		if c.entries[k] == nil {
			c.fetchRange(lo, hi)
			return
		}
	}
}

func (c *MailCache) fetchRange(lo, hi uint32) {
	if c.inFlight && c.inFlightLo == lo && c.inFlightHi == hi {
		return
	}
	reqId := c.nextReqId()
	req := mailproto.KeyHeadersReq{
		RequestId: reqId,
		CollId:    c.collId,
		From:      lo,
		To:        hi,
	}
	msg := mailproto.EncodeKeyHeaders(&req)
	if err := uring.Send(c.maildbSID, &msg); err != nil {
		fmt.Printf("[cache] fetchRange send failed: %v\n", err)
		return
	}
	c.inFlight = true
	c.inFlightId = reqId
	c.inFlightLo = lo
	c.inFlightHi = hi
}

// HandleResponse routes a decoded mailproto message into the cache.
// RespCreateCollection is NOT handled here; all others are.
func (c *MailCache) HandleResponse(v any) {
	switch resp := v.(type) {
	case mailproto.RespKeyHeaders:
		c.handleKeyHeaders(&resp)
	case mailproto.CollectionAdd:
		c.handleCollectionAdd(&resp)
	case mailproto.CollectionRemove:
		c.handleCollectionRemove(&resp)
	}
}

func (c *MailCache) handleKeyHeaders(resp *mailproto.RespKeyHeaders) {
	if resp.RequestId != c.inFlightId {
		return
	}
	c.inFlight = false

	switch resp.ErrCode {
	case mailproto.ErrCollectionExpired:
		c.entries = make(map[uint32]*mailproto.KeyHeaderEntry)
		if c.OnExpired != nil {
			c.OnExpired(c.collId)
		}
		return
	case mailproto.ErrNone:
	default:
		fmt.Printf("[cache] KeyHeaders err=%d\n", resp.ErrCode)
		return
	}

	if resp.Count == 0 || resp.TargetVA == 0 {
		return
	}

	pages := unsafe.Slice((*byte)(unsafe.Pointer(uintptr(resp.TargetVA))), int(resp.NumBytes))
	for i := 0; i < int(resp.Count); i++ {
		off := i * mailproto.KeyHeaderEntrySize
		e := *(*mailproto.KeyHeaderEntry)(unsafe.Pointer(&pages[off]))
		eCopy := e
		c.entries[e.MsgNum] = &eCopy
	}
	numPages := (int(resp.NumBytes) + 4095) / 4096
	if err := mem.FreePages(unsafe.Pointer(uintptr(resp.TargetVA)), numPages); err != nil {
		fmt.Printf("[cache] FreePages: %v\n", err)
	}

	if c.OnUpdated != nil {
		c.OnUpdated()
	}
}

func (c *MailCache) handleCollectionAdd(notif *mailproto.CollectionAdd) {
	if notif.CollId != c.collId {
		return
	}
	c.collSize++
	// Entries at or after the insertion point have shifted — evict them.
	for k := range c.entries {
		if k >= notif.MsgNum {
			delete(c.entries, k)
		}
	}
	if c.OnUpdated != nil {
		c.OnUpdated()
	}
}

func (c *MailCache) handleCollectionRemove(notif *mailproto.CollectionRemove) {
	if notif.CollId != c.collId {
		return
	}
	if c.collSize > 0 {
		c.collSize--
	}
	// Entries at or after the removed position have shifted — evict them.
	for k := range c.entries {
		if k >= notif.MsgNum {
			delete(c.entries, k)
		}
	}
	if c.OnUpdated != nil {
		c.OnUpdated()
	}
}

func (c *MailCache) nextReqId() [16]byte {
	c.reqCounter++
	var id [16]byte
	n := uint64(time.Now().UnixNano())
	*(*uint64)(unsafe.Pointer(&id[0])) = n
	*(*uint64)(unsafe.Pointer(&id[8])) = c.reqCounter
	return id
}
