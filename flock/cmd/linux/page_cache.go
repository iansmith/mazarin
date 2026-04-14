package main

import (
	"sync/atomic"
	"unsafe"
)

// pageCacheBarrier is used for memory ordering on weakly-ordered architectures
// (ARM64). After writing to a cached page, a store-release ensures the writes
// are visible to other cores before we reply. Before reading from a cached
// page, a load-acquire ensures we see all stores from other cores.
var pageCacheBarrier uint32

// pageCache tracks mmap'd pages that are retained in the linux shepherd's
// address space for page cache coherence. Keyed by (sid, fd, pageOffset),
// the value is the handler-side VA of the page — the same physical page
// is also mapped into the faulting process.
//
// The page cache is the source of truth while a mapping is live:
// - read/pread return data from cached pages (no ext2 round-trip)
// - write/pwrite modify cached pages directly and mark them dirty
// - dirty pages are flushed to ext2 on munmap/msync/close
//
// All access is from the single delegate handler goroutine, so no locking
// is needed.
type pageCache struct {
	// data maps sid → fd → pageOffset → page info
	data map[int16]map[int]map[int64]cachedPage
}

// cachedPage holds the handler-side VA of a cached page and its dirty state.
type cachedPage struct {
	VA    uintptr
	Dirty bool
}

type pageCacheEntry struct {
	Offset int64
	VA     uintptr
	Dirty  bool
}

func newPageCache() *pageCache {
	return &pageCache{
		data: make(map[int16]map[int]map[int64]cachedPage),
	}
}

// Add records a cached page. Overwrites any existing entry for the same
// (sid, fd, offset) triple. New pages start clean (not dirty).
func (pc *pageCache) Add(sid int16, fd int, offset int64, va uintptr) {
	fds, ok := pc.data[sid]
	if !ok {
		fds = make(map[int]map[int64]cachedPage)
		pc.data[sid] = fds
	}
	pages, ok := fds[fd]
	if !ok {
		pages = make(map[int64]cachedPage)
		fds[fd] = pages
	}
	pages[offset] = cachedPage{VA: va, Dirty: false}
}

// Lookup returns the handler VA for a cached page, or 0 on cache miss.
func (pc *pageCache) Lookup(sid int16, fd int, offset int64) uintptr {
	if fds, ok := pc.data[sid]; ok {
		if pages, ok := fds[fd]; ok {
			return pages[offset].VA
		}
	}
	return 0
}

// LookupRange returns all cached pages overlapping [startOffset, startOffset+length).
func (pc *pageCache) LookupRange(sid int16, fd int, startOffset int64, length int) []pageCacheEntry {
	fds, ok := pc.data[sid]
	if !ok {
		return nil
	}
	pages, ok := fds[fd]
	if !ok {
		return nil
	}

	pageSize := int64(4096)
	firstPage := startOffset &^ (pageSize - 1)
	endOffset := startOffset + int64(length)

	var result []pageCacheEntry
	for off := firstPage; off < endOffset; off += pageSize {
		if cp, ok := pages[off]; ok {
			result = append(result, pageCacheEntry{Offset: off, VA: cp.VA, Dirty: cp.Dirty})
		}
	}
	return result
}

// MarkDirty marks all pages in the given entries as dirty.
func (pc *pageCache) MarkDirty(sid int16, fd int, entries []pageCacheEntry) {
	fds, ok := pc.data[sid]
	if !ok {
		return
	}
	pages, ok := fds[fd]
	if !ok {
		return
	}
	for _, e := range entries {
		if cp, ok := pages[e.Offset]; ok {
			cp.Dirty = true
			pages[e.Offset] = cp
		}
	}
}

// RemoveRangeBatch removes up to maxEntries cached pages for (sid, fd) and
// returns them. Used for round-based cleanup — if len(result) == maxEntries,
// there may be more pages to remove (call again).
func (pc *pageCache) RemoveRangeBatch(sid int16, fd int, maxEntries int) []pageCacheEntry {
	fds, ok := pc.data[sid]
	if !ok {
		return nil
	}
	pages, ok := fds[fd]
	if !ok {
		return nil
	}

	var result []pageCacheEntry
	for off, cp := range pages {
		if len(result) >= maxEntries {
			break
		}
		result = append(result, pageCacheEntry{Offset: off, VA: cp.VA, Dirty: cp.Dirty})
		delete(pages, off)
	}
	if len(pages) == 0 {
		delete(fds, fd)
		if len(fds) == 0 {
			delete(pc.data, sid)
		}
	}
	return result
}

// RemoveAllBatch removes up to maxEntries cached pages for a given sid
// across ALL fds. Used for death cleanup rounds.
func (pc *pageCache) RemoveAllBatch(sid int16, maxEntries int) []pageCacheEntry {
	fds, ok := pc.data[sid]
	if !ok {
		return nil
	}

	var result []pageCacheEntry
	for fd, pages := range fds {
		for off, cp := range pages {
			if len(result) >= maxEntries {
				return result
			}
			result = append(result, pageCacheEntry{Offset: off, VA: cp.VA, Dirty: cp.Dirty})
			delete(pages, off)
		}
		if len(pages) == 0 {
			delete(fds, fd)
		}
	}
	if len(fds) == 0 {
		delete(pc.data, sid)
	}
	return result
}

// RemoveRange removes all cached pages in [startOffset, startOffset+length)
// and returns them for cleanup.
func (pc *pageCache) RemoveRange(sid int16, fd int, startOffset int64, length int64) []pageCacheEntry {
	fds, ok := pc.data[sid]
	if !ok {
		return nil
	}
	pages, ok := fds[fd]
	if !ok {
		return nil
	}

	endOffset := startOffset + length
	var result []pageCacheEntry
	for off, cp := range pages {
		if off >= startOffset && off < endOffset {
			result = append(result, pageCacheEntry{Offset: off, VA: cp.VA, Dirty: cp.Dirty})
			delete(pages, off)
		}
	}
	if len(pages) == 0 {
		delete(fds, fd)
	}
	return result
}

// RemoveFD removes all cached pages for a given (sid, fd) and returns
// the handler VAs for cleanup.
func (pc *pageCache) RemoveFD(sid int16, fd int) []uintptr {
	fds, ok := pc.data[sid]
	if !ok {
		return nil
	}
	pages, ok := fds[fd]
	if !ok {
		return nil
	}

	var vas []uintptr
	for _, cp := range pages {
		vas = append(vas, cp.VA)
	}
	delete(fds, fd)
	if len(fds) == 0 {
		delete(pc.data, sid)
	}
	return vas
}

// RemoveAll removes all cached pages for a given sid and returns the
// handler VAs for cleanup.
func (pc *pageCache) RemoveAll(sid int16) []uintptr {
	fds, ok := pc.data[sid]
	if !ok {
		return nil
	}

	var vas []uintptr
	for _, pages := range fds {
		for _, cp := range pages {
			vas = append(vas, cp.VA)
		}
	}
	delete(pc.data, sid)
	return vas
}

// FlushAllPagesForFD writes all cached pages for (sid, fd) to the
// filesystem. Used during flush+cleanup on munmap.
//
// We flush ALL pages, not just those marked dirty, because user writes
// through the mmap VA bypass syscalls entirely — the handler has no way
// to know which pages were modified. Without MMU dirty-bit access, the
// only safe option is to write everything back on munmap.
func (pc *pageCache) FlushAllPagesForFD(sid int16, fd int, write func(offset int64, data []byte) (int, error)) (int64, error) {
	fds, ok := pc.data[sid]
	if !ok {
		return 0, nil
	}
	pages, ok := fds[fd]
	if !ok {
		return 0, nil
	}

	// Load-acquire barrier before reading cached pages.
	_ = atomic.LoadUint32(&pageCacheBarrier)

	var totalFlushed int64
	for off, cp := range pages {
		pageData := unsafe.Slice((*byte)(unsafe.Pointer(cp.VA)), 4096)
		written, err := write(off, pageData)
		if err != nil {
			return totalFlushed, err
		}
		totalFlushed += int64(written)
	}
	return totalFlushed, nil
}

// FlushAllPagesForSID writes all cached pages for a given sid
// across all fds. Used during death cleanup.
func (pc *pageCache) FlushAllPagesForSID(sid int16, writeForFD func(fd int, offset int64, data []byte) (int, error)) (int64, error) {
	fds, ok := pc.data[sid]
	if !ok {
		return 0, nil
	}

	// Load-acquire barrier before reading cached pages.
	_ = atomic.LoadUint32(&pageCacheBarrier)

	var totalFlushed int64
	for fd, pages := range fds {
		for off, cp := range pages {
			pageData := unsafe.Slice((*byte)(unsafe.Pointer(cp.VA)), 4096)
			written, err := writeForFD(fd, off, pageData)
			if err != nil {
				return totalFlushed, err
			}
			totalFlushed += int64(written)
		}
	}
	return totalFlushed, nil
}

// readFromCachedPages reads data from cached pages into buf, covering
// the byte range [offset, offset+count). Returns (bytesRead, true) if
// ALL pages in the range are cached, or (0, false) if any page is missing.
// The fileSize parameter caps reads to avoid returning zero-fill beyond EOF.
func readFromCachedPages(entries []pageCacheEntry, offset int64, buf []byte, count int, fileSize int64) (int, bool) {
	if len(entries) == 0 {
		return 0, false
	}

	pageSize := int64(4096)
	firstPage := offset &^ (pageSize - 1)
	endOffset := offset + int64(count)

	// Cap at file size
	if fileSize > 0 && endOffset > fileSize {
		endOffset = fileSize
		count = int(endOffset - offset)
		if count <= 0 {
			return 0, true // at or past EOF
		}
	}

	// Check all required pages are present
	needed := int((endOffset - 1 - firstPage) / pageSize) + 1
	if needed < 0 {
		needed = 0
	}
	if len(entries) != needed {
		return 0, false
	}

	// Build a sorted check — entries must cover every page in range
	entryMap := make(map[int64]uintptr, len(entries))
	for _, e := range entries {
		entryMap[e.Offset] = e.VA
	}
	for pg := firstPage; pg < endOffset; pg += pageSize {
		if _, ok := entryMap[pg]; !ok {
			return 0, false
		}
	}

	// Load-acquire barrier: ensure we see all stores from other cores
	// (the caller writing through its mmap'd VA) before reading from
	// cached pages. On ARM64 this compiles to LDAR.
	_ = atomic.LoadUint32(&pageCacheBarrier)

	// Copy from cached pages to buf
	written := 0
	for pg := firstPage; pg < endOffset; pg += pageSize {
		va := entryMap[pg]
		// Page memory as a slice
		pageData := unsafe.Slice((*byte)(unsafe.Pointer(va)), 4096)

		// Compute overlap between [pg, pg+4096) and [offset, endOffset)
		srcStart := int(0)
		if pg < offset {
			srcStart = int(offset - pg)
		}
		srcEnd := 4096
		if pg+pageSize > endOffset {
			srcEnd = int(endOffset - pg)
		}

		n := copy(buf[written:], pageData[srcStart:srcEnd])
		written += n
	}

	return written, true
}

// writeToCachedPages copies write data into overlapping cached pages.
// Returns (bytesWritten, true) if ALL pages in the range are cached,
// or (0, false) if any page is missing. On success the affected pages
// are left dirty (caller must call MarkDirty).
func writeToCachedPages(entries []pageCacheEntry, offset int64, data []byte) (int, bool) {
	if len(entries) == 0 {
		return 0, false
	}

	pageSize := int64(4096)
	firstPage := offset &^ (pageSize - 1)
	endOffset := offset + int64(len(data))

	// Check all required pages are present
	needed := int((endOffset - 1 - firstPage) / pageSize) + 1
	if needed < 0 {
		needed = 0
	}
	if len(entries) != needed {
		return 0, false
	}
	entryMap := make(map[int64]uintptr, len(entries))
	for _, e := range entries {
		entryMap[e.Offset] = e.VA
	}
	for pg := firstPage; pg < endOffset; pg += pageSize {
		if _, ok := entryMap[pg]; !ok {
			return 0, false
		}
	}

	// Copy data into cached pages
	written := 0
	for pg := firstPage; pg < endOffset; pg += pageSize {
		va := entryMap[pg]
		pageData := unsafe.Slice((*byte)(unsafe.Pointer(va)), 4096)

		dstStart := int(0)
		if pg < offset {
			dstStart = int(offset - pg)
		}
		srcEnd := int64(4096)
		if pg+pageSize > endOffset {
			srcEnd = endOffset - pg
		}

		n := copy(pageData[dstStart:srcEnd], data[written:])
		written += n
	}

	// Store-release barrier: ensure all stores to cached pages are visible
	// to other cores (the caller reading through its mmap'd VA) before we
	// reply. On ARM64 this compiles to STLR.
	atomic.StoreUint32(&pageCacheBarrier, 0)

	return written, true
}

// updateCachedPages copies write data into any overlapping cached pages
// without requiring full coverage. Used as a fallback when the write goes
// through ext2 but some pages happen to be cached.
func updateCachedPages(entries []pageCacheEntry, offset int64, data []byte) {
	if len(entries) == 0 {
		return
	}

	pageSize := int64(4096)
	endOffset := offset + int64(len(data))

	for _, e := range entries {
		overlapStart := offset
		if e.Offset > overlapStart {
			overlapStart = e.Offset
		}
		overlapEnd := endOffset
		if e.Offset+pageSize < overlapEnd {
			overlapEnd = e.Offset + pageSize
		}
		if overlapStart >= overlapEnd {
			continue
		}

		pageData := unsafe.Slice((*byte)(unsafe.Pointer(e.VA)), 4096)
		srcStart := int(overlapStart - offset)
		dstStart := int(overlapStart - e.Offset)
		n := int(overlapEnd - overlapStart)
		copy(pageData[dstStart:dstStart+n], data[srcStart:srcStart+n])
	}

	// Store-release barrier after writing to cached pages.
	atomic.StoreUint32(&pageCacheBarrier, 0)
}
