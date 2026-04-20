// shepherd_text_cache.go — single-slot kernel cache for read-only PT_LOAD
// segments of a repeatedly-launched ET_EXEC shepherd binary (shepherd.elf).
//
// Problem: every shepherd launch allocated+copied the full ELF into fresh
// physical frames, duplicating ~6.5 MiB of shepherd.elf × 7 concurrent
// shepherds → ~28 MiB of avoidable userspace frames, contributing to the
// kmem buddy OOM observed on ARM64 HVF (UserHeap 524 MiB / 756 MiB pool).
//
// Design (approved 2026-04-19):
//   - One cache slot keyed by (size, FNV-1a of first 4 KiB).
//   - First load populates the cache; subsequent matching loads reuse
//     the cached physical frames by mapping them into the new shepherd's
//     page table instead of alloc+copy.
//   - Only PT_LOAD segments without PF_W are cached. Writable segments
//     (Go runtime .data/.bss, firstmoduledata) must stay per-process.
//   - Pinning is achieved via PageDescriptor.RefCount: the cache holds
//     +1 ref per cached page, each mapping holds +1 ref. CleanupShepherdPages
//     decrements via releasePageByPA, which only frees to buddy at ref=0.
//   - ET_EXEC fixed VA is load-bearing: every shepherd's TTBR0 maps the
//     same VA → same bytes, which is the precondition for sharing .text.
//
// Concurrency: all loadELF calls run on the single runshepherd kernel
// worker goroutine, so reads + the populate step are naturally serialized.
// The lock is kept for correctness and future multi-worker scenarios.
package ksyscall

import (
	"mazzy/kmazarin/klog"
	"mazzy/kmazarin/kmem"
	"sync"
)

// sharedSegment describes one R-only PT_LOAD from the cached ELF.
type sharedSegment struct {
	startVA uint64    // page-aligned VA (startPage from loadSegment)
	memsz   uint64    // page-rounded size in bytes
	flags   uint32    // PF_R, PF_X; never PF_W
	pas     []uintptr // physical frames in VA order (one per 4 KiB)
}

// shepherdTextCache holds read-only PT_LOAD frames from shepherd.elf.
// Only one cache slot — populated on first load, never evicted.
type shepherdTextCache struct {
	lock        sync.Mutex
	populated   bool
	sizeBytes   uint64
	fingerprint uint64
	segments    []sharedSegment
}

var globalTextCache shepherdTextCache

// isCacheableSegment reports whether an ELF segment flag set is eligible
// for cross-process sharing. Any PF_W disqualifies the segment because
// each shepherd needs its own writable view of Go runtime globals.
func isCacheableSegment(flags uint32) bool {
	return flags&PF_W == 0
}

// isCacheableLoad reports whether a given loadELF call should consult the
// shared-text cache. Only plugin-host launches (shepherd.elf loading a .maz)
// qualify — those are identified by the presence of a plugin path in
// extraArgs. The embedded fs shepherd (LaunchFromMemory, no args) and any
// legacy ET_EXEC launches (sys.RunShepherd with no plugin path) take the
// normal alloc+copy path and leave the cache slot alone.
//
// This keeps scope narrow: the cache holds /shepherd.elf exactly because
// shepherd.elf is the single binary that is re-launched across every .maz
// plugin start; no other ELF repeats today.
func isCacheableLoad(extraArgs []string) bool {
	return len(extraArgs) > 0
}

// fnv1aFingerprint returns an FNV-1a 64-bit hash over the input bytes.
func fnv1aFingerprint(data []byte) uint64 {
	const offset64 = 14695981039346656037
	const prime64 = 1099511628211
	h := uint64(offset64)
	for _, b := range data {
		h ^= uint64(b)
		h *= prime64
	}
	return h
}

// computeELFFingerprint hashes the first 4 KiB of the ELF (or the whole
// file if smaller). Combined with the file size (tracked separately),
// this is enough to distinguish distinct shepherd binaries without
// full-file hashing.
func computeELFFingerprint(data []byte) uint64 {
	n := len(data)
	if n > 4096 {
		n = 4096
	}
	return fnv1aFingerprint(data[:n])
}

// lookupCachedSegment returns the cached segment matching (startVA, memsz, flags)
// or nil if none. Must be called with c.lock held and c.populated true.
func (c *shepherdTextCache) lookupCachedSegment(startVA, memsz uint64, flags uint32) *sharedSegment {
	for i := range c.segments {
		seg := &c.segments[i]
		if seg.startVA == startVA && seg.memsz == memsz && seg.flags == flags {
			return seg
		}
	}
	return nil
}

// cacheSnapshot is what loadELF receives after consulting the cache: either
// a hit (populated matching entry) or a miss (caller will populate).
type cacheSnapshot struct {
	hit       bool
	sizeBytes uint64
	fp        uint64
	// For hit path, segments is the active cache; loadELF reads it without
	// the lock because populated slots are never mutated.
	segments []sharedSegment
}

// snapshotCache looks up the cache for this ELF and returns whether to take
// the hit path. The returned snapshot is read-only.
func snapshotCache(data []byte) cacheSnapshot {
	snap := cacheSnapshot{
		sizeBytes: uint64(len(data)),
		fp:        computeELFFingerprint(data),
	}
	globalTextCache.lock.Lock()
	defer globalTextCache.lock.Unlock()
	if globalTextCache.populated &&
		globalTextCache.sizeBytes == snap.sizeBytes &&
		globalTextCache.fingerprint == snap.fp {
		snap.hit = true
		snap.segments = globalTextCache.segments
	}
	return snap
}

// populateCache installs freshly-loaded R-only segments as the canonical
// shared copy, bumping RefCount on every page so the cache itself holds
// a ref independent of any shepherd mapping.
//
// No-op if the cache is already populated (a concurrent loader won the
// race) — the caller's per-process mappings are already in place and
// will be cleaned up normally on shepherd death.
func populateCache(sizeBytes, fp uint64, segs []sharedSegment) {
	globalTextCache.lock.Lock()
	defer globalTextCache.lock.Unlock()
	if globalTextCache.populated {
		return
	}
	for i := range segs {
		for _, pa := range segs[i].pas {
			kmem.BumpPageRefCount(pa)
		}
	}
	globalTextCache.sizeBytes = sizeBytes
	globalTextCache.fingerprint = fp
	globalTextCache.segments = segs
	globalTextCache.populated = true

	totalPages := 0
	for i := range segs {
		totalPages += len(segs[i].pas)
	}
	klog.Logf("[text-cache] populated: size=%d fp=%x segs=%d pages=%d\n",
		sizeBytes, fp, len(segs), totalPages)
}

// mapSharedSegment maps every cached PA for a single segment into the
// new shepherd's page table. Bumps RefCount for each page via
// MapExistingUserPageWithL0 so CleanupShepherdPages will only free when
// the last reference drops.
func mapSharedSegment(seg *sharedSegment, l0PA uintptr) error {
	const pageSize = uint64(4096)
	for i, pa := range seg.pas {
		pageVA := uintptr(seg.startVA + uint64(i)*pageSize)
		if !kmem.MapExistingUserPageWithL0(pageVA, pa, seg.flags, l0PA) {
			return &elfError{"failed to map cached text page"}
		}
	}
	return nil
}
