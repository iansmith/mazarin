package textshape

import (
	"container/list"
	"sync"
)

// ShapeCacheSize is the maximum number of shaped runs held in the LRU cache.
//
// TODO: add a rasterized glyph cache keyed on (fontID, GID) → (GlyphInfo, []byte)
// so the same glyph bitmap is not re-rasterized on every call to LayoutText.
//
// TODO: add sub-word granularity so a single changed character does not
// invalidate the shaped run for an entire paragraph.
const ShapeCacheSize = 16384

type shapeCacheKey struct {
	text      string
	fontID    int32
	direction Direction
	script    Script
	language  string
}

type shapeCacheEntry struct {
	key shapeCacheKey
	run ShapedRun
}

type shapeCache struct {
	mu      sync.Mutex
	items   map[shapeCacheKey]*list.Element
	lru     list.List
	maxSize int
}

func newShapeCache(maxSize int) *shapeCache {
	return &shapeCache{
		items:   make(map[shapeCacheKey]*list.Element),
		maxSize: maxSize,
	}
}

func (c *shapeCache) get(key shapeCacheKey) (ShapedRun, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[key]
	if !ok {
		return ShapedRun{}, false
	}
	c.lru.MoveToFront(el)
	return el.Value.(*shapeCacheEntry).run, true
}

func (c *shapeCache) put(key shapeCacheKey, run ShapedRun) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok {
		c.lru.MoveToFront(el)
		el.Value.(*shapeCacheEntry).run = run
		return
	}
	if c.lru.Len() >= c.maxSize {
		back := c.lru.Back()
		if back != nil {
			c.lru.Remove(back)
			delete(c.items, back.Value.(*shapeCacheEntry).key)
		}
	}
	entry := &shapeCacheEntry{key: key, run: run}
	el := c.lru.PushFront(entry)
	c.items[key] = el
}
