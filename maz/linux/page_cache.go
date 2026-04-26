package main

import (
	"fmt"
	"sync/atomic"
	"unsafe"
)

// pageCacheBarrier is used for memory ordering on weakly-ordered architectures
// (ARM64). After writing to a cached page, a store-release ensures the writes
// are visible to other cores before we reply. Before reading from a cached
// page, a load-acquire ensures we see all stores from other cores.
var pageCacheBarrier uint32

// pageCache tracks mmap'd pages that are retained in the linux shepherd's
// address space for page cache coherence. Keyed by (sid, inum, pageOffset)
// — NOT by fd — because Linux semantics keep mmap'd pages alive across
// close(). Keying by inode means an fd that gets closed-then-reused doesn't
// collide its new file's pages with stale entries from the previous file.
//
// The page cache is the source of truth while a mapping is live:
//   - read/pread return data from cached pages (no ext2 round-trip)
//   - write/pwrite modify cached pages directly and mark them dirty
//   - dirty pages are flushed to ext2 on munmap/msync (NOT close — the
//     mapping survives close per Linux semantics)
//
// All access is from the single delegate handler goroutine, so no locking
// is needed.
type pageCache struct {
	// data maps sid → inum → pageOffset → page info
	data map[int16]map[uint32]map[int64]cachedPage
}

// cachedPage holds the handler-side VA of a cached page and its dirty state.
// Handle is the fs.maz-side handle used for writeback. Multiple opens of the
// same inode share cache entries; we keep whichever handle was current at
// add time so writeback can still go through after the originator closes.
type cachedPage struct {
	VA     uintptr
	Handle uint32
	Dirty  bool
}

type pageCacheEntry struct {
	Offset int64
	VA     uintptr
	Handle uint32
	Dirty  bool
}

func newPageCache() *pageCache {
	return &pageCache{
		data: make(map[int16]map[uint32]map[int64]cachedPage),
	}
}

// Add records a cached page. Overwrites any existing entry for the same
// (sid, inum, offset) triple. New pages start clean (not dirty).
//
// An overwrite indicates the kernel demand-faulted the same (sid, inum, offset)
// twice — possible if the caller's PTE was cleared between faults (e.g. via
// MAP_FIXED unmap on an overlapping range). The previous (VA, Handle) tuple
// is dropped from the cache, orphaning its handler-side PTE: the kernel never
// learns to unmap it, and the underlying physical page can be released through
// other paths and reused while the linux PTE still points at it. Suspected
// corruption source — log so we can correlate with the GC crash.
func (pc *pageCache) Add(sid int16, inum uint32, offset int64, va uintptr, handle uint32) {
	inodes, ok := pc.data[sid]
	if !ok {
		inodes = make(map[uint32]map[int64]cachedPage)
		pc.data[sid] = inodes
	}
	pages, ok := inodes[inum]
	if !ok {
		pages = make(map[int64]cachedPage)
		inodes[inum] = pages
	}
	if old, present := pages[offset]; present && old.VA != va {
		fmt.Printf("[pageCache:OVERWRITE] sid=%d inum=%d off=%d oldVA=%x newVA=%x oldHandle=%d newHandle=%d (handler PTE for oldVA orphaned)\n",
			sid, inum, offset, uint64(old.VA), uint64(va), old.Handle, handle)
	}
	pages[offset] = cachedPage{VA: va, Handle: handle, Dirty: false}
}

// Lookup returns the handler VA for a cached page, or 0 on cache miss.
func (pc *pageCache) Lookup(sid int16, inum uint32, offset int64) uintptr {
	if inodes, ok := pc.data[sid]; ok {
		if pages, ok := inodes[inum]; ok {
			return pages[offset].VA
		}
	}
	return 0
}

// LookupRange returns all cached pages overlapping [startOffset, startOffset+length).
func (pc *pageCache) LookupRange(sid int16, inum uint32, startOffset int64, length int) []pageCacheEntry {
	inodes, ok := pc.data[sid]
	if !ok {
		return nil
	}
	pages, ok := inodes[inum]
	if !ok {
		return nil
	}

	pageSize := int64(4096)
	firstPage := startOffset &^ (pageSize - 1)
	endOffset := startOffset + int64(length)

	var result []pageCacheEntry
	for off := firstPage; off < endOffset; off += pageSize {
		if cp, ok := pages[off]; ok {
			result = append(result, pageCacheEntry{Offset: off, VA: cp.VA, Handle: cp.Handle, Dirty: cp.Dirty})
		}
	}
	return result
}

// MarkDirty marks all pages in the given entries as dirty.
func (pc *pageCache) MarkDirty(sid int16, inum uint32, entries []pageCacheEntry) {
	inodes, ok := pc.data[sid]
	if !ok {
		return
	}
	pages, ok := inodes[inum]
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

// RemoveRangeBatch removes up to maxEntries cached pages for (sid, inum) and
// returns them. Used for round-based cleanup — if len(result) == maxEntries,
// there may be more pages to remove (call again).
func (pc *pageCache) RemoveRangeBatch(sid int16, inum uint32, maxEntries int) []pageCacheEntry {
	inodes, ok := pc.data[sid]
	if !ok {
		return nil
	}
	pages, ok := inodes[inum]
	if !ok {
		return nil
	}

	var result []pageCacheEntry
	for off, cp := range pages {
		if len(result) >= maxEntries {
			break
		}
		result = append(result, pageCacheEntry{Offset: off, VA: cp.VA, Handle: cp.Handle, Dirty: cp.Dirty})
		delete(pages, off)
	}
	if len(pages) == 0 {
		delete(inodes, inum)
		if len(inodes) == 0 {
			delete(pc.data, sid)
		}
	}
	return result
}

// RemoveRangeOffsetBatch removes up to maxEntries cached pages for (sid, inum)
// whose offsets fall in [startOffset, startOffset+length) and returns them.
// Used for partial-munmap cleanup rounds.
func (pc *pageCache) RemoveRangeOffsetBatch(sid int16, inum uint32, startOffset, length int64, maxEntries int) []pageCacheEntry {
	inodes, ok := pc.data[sid]
	if !ok {
		return nil
	}
	pages, ok := inodes[inum]
	if !ok {
		return nil
	}

	endOffset := startOffset + length
	var result []pageCacheEntry
	for off, cp := range pages {
		if off < startOffset || off >= endOffset {
			continue
		}
		if len(result) >= maxEntries {
			break
		}
		result = append(result, pageCacheEntry{Offset: off, VA: cp.VA, Handle: cp.Handle, Dirty: cp.Dirty})
		delete(pages, off)
	}
	if len(pages) == 0 {
		delete(inodes, inum)
		if len(inodes) == 0 {
			delete(pc.data, sid)
		}
	}
	return result
}

// RemoveAllBatch removes up to maxEntries cached pages for a given sid
// across ALL inodes. Used for death cleanup rounds.
func (pc *pageCache) RemoveAllBatch(sid int16, maxEntries int) []pageCacheEntry {
	inodes, ok := pc.data[sid]
	if !ok {
		return nil
	}

	var result []pageCacheEntry
	for inum, pages := range inodes {
		for off, cp := range pages {
			if len(result) >= maxEntries {
				return result
			}
			result = append(result, pageCacheEntry{Offset: off, VA: cp.VA, Handle: cp.Handle, Dirty: cp.Dirty})
			delete(pages, off)
		}
		if len(pages) == 0 {
			delete(inodes, inum)
		}
	}
	if len(inodes) == 0 {
		delete(pc.data, sid)
	}
	return result
}

// RemoveRange removes all cached pages in [startOffset, startOffset+length)
// for a given (sid, inum) and returns them for cleanup.
func (pc *pageCache) RemoveRange(sid int16, inum uint32, startOffset int64, length int64) []pageCacheEntry {
	inodes, ok := pc.data[sid]
	if !ok {
		return nil
	}
	pages, ok := inodes[inum]
	if !ok {
		return nil
	}

	endOffset := startOffset + length
	var result []pageCacheEntry
	for off, cp := range pages {
		if off >= startOffset && off < endOffset {
			result = append(result, pageCacheEntry{Offset: off, VA: cp.VA, Handle: cp.Handle, Dirty: cp.Dirty})
			delete(pages, off)
		}
	}
	if len(pages) == 0 {
		delete(inodes, inum)
	}
	return result
}

// HasPagesFor reports whether the cache holds any pages for (sid, inum).
// Used by sysClose to decide whether to keep the fs handle alive past
// close (Linux semantics: mmap survives close, so the handle must too,
// for writeback during the eventual munmap).
func (pc *pageCache) HasPagesFor(sid int16, inum uint32) bool {
	inodes, ok := pc.data[sid]
	if !ok {
		return false
	}
	pages, ok := inodes[inum]
	return ok && len(pages) > 0
}

// RemoveAll removes all cached pages for a given sid and returns the
// handler VAs for cleanup.
func (pc *pageCache) RemoveAll(sid int16) []uintptr {
	inodes, ok := pc.data[sid]
	if !ok {
		return nil
	}

	var vas []uintptr
	for _, pages := range inodes {
		for _, cp := range pages {
			vas = append(vas, cp.VA)
		}
	}
	delete(pc.data, sid)
	return vas
}

// FlushAllPagesForInum writes all cached pages for (sid, inum) back to the
// filesystem. Used during flush+cleanup on munmap.
//
// We flush ALL pages, not just those marked dirty, because user writes
// through the mmap VA bypass syscalls entirely — the handler has no way
// to know which pages were modified. Without MMU dirty-bit access, the
// only safe option is to write everything back on munmap.
func (pc *pageCache) FlushAllPagesForInum(sid int16, inum uint32, write func(handle uint32, offset int64, data []byte) (int, error)) (int64, error) {
	inodes, ok := pc.data[sid]
	if !ok {
		return 0, nil
	}
	pages, ok := inodes[inum]
	if !ok {
		return 0, nil
	}

	// Load-acquire barrier before reading cached pages.
	_ = atomic.LoadUint32(&pageCacheBarrier)

	var totalFlushed int64
	for off, cp := range pages {
		pageData := unsafe.Slice((*byte)(unsafe.Pointer(cp.VA)), 4096)
		written, err := write(cp.Handle, off, pageData)
		if err != nil {
			return totalFlushed, err
		}
		totalFlushed += int64(written)
	}
	return totalFlushed, nil
}

// FlushAllPagesForSID writes all cached pages for a given sid back to the
// filesystem across all inodes. Used during death cleanup.
func (pc *pageCache) FlushAllPagesForSID(sid int16, write func(handle uint32, offset int64, data []byte) (int, error)) (int64, error) {
	inodes, ok := pc.data[sid]
	if !ok {
		return 0, nil
	}

	// Load-acquire barrier before reading cached pages.
	_ = atomic.LoadUint32(&pageCacheBarrier)

	var totalFlushed int64
	for _, pages := range inodes {
		for off, cp := range pages {
			pageData := unsafe.Slice((*byte)(unsafe.Pointer(cp.VA)), 4096)
			written, err := write(cp.Handle, off, pageData)
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
