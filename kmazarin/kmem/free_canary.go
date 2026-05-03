// free_canary.go - Sentinel-byte canary at buddy free/alloc time.
//
// Bug-B-family H-T3 Stage 1: detect kernel-side direct writes into a freed
// page. At BuddyFreeTyped, after the merge / insert sequence completes,
// paint the freed block (skipping the first 8 bytes used by the buddy
// free-list next-pointer) with a known sentinel pattern. At BuddyAllocTyped,
// immediately after popping from the free list and before splitting,
// verify the pattern is intact byte-for-byte. Any mismatch means a kernel
// path wrote into the page after we freed it — exactly the surviving
// suspect for the mspan corruption.
//
// Off in production via freeCanaryEnabled. Default true here ONLY for the
// active diagnostic; flip to false (or delete this file) before merging
// unrelated work — the fill at free time and read at alloc time scale
// linearly with block size.
//
// nosplit discipline (mirrors stale_pte_check.go): BuddyFreeTyped /
// BuddyAllocTyped are //go:nosplit and called from exception-handler
// chains. The fill and verify functions stay nosplit and never call
// SerialPuts/SerialHex16/klog directly. On a verify mismatch, the walker
// captures pa/offset/expected/found into package-level globals and returns
// — the non-nosplit canaryHalt then formats via klog.Criticalf and
// freezes.
//
// Layout note: buddyInsertFree writes a uintptr next-pointer into the
// first 8 bytes of the freed block. Fill skips offset 0..7. After merge,
// upper-half buddies that were on the free list retain their canary in
// bytes 8..end; their first 8 bytes (which were the popped next-pointer
// or canary, depending on history) are subsumed into the merged block —
// the post-merge fill rewrites them as sentinel anyway, except the first
// 8 bytes of the merged block which are the new next-pointer.

package kmem

import (
	"mazzy/kmazarin/klog"
	"sync/atomic"
	"unsafe"
)

// freeCanaryEnabled toggles the diagnostic. Default false (Stage 1 H-T3
// run already completed and was silent across 5 × 180s with ~1.5M+
// verifies; H-T3a "kernel write between free and reuse" ruled out).
// Re-enable by flipping to true if a future hypothesis again implicates
// the free→reuse window.
var freeCanaryEnabled = true

// SetFreeCanaryEnabled toggles the diagnostic at runtime.
func SetFreeCanaryEnabled(v bool) { freeCanaryEnabled = v }

// FreeCanaryEnabled reports whether the canary is active.
func FreeCanaryEnabled() bool { return freeCanaryEnabled }

// freeCanarySentinel is the 8-byte pattern written into freed pages. Chosen
// to be obviously distinct from zero, page-table bits, small ints, ASCII,
// and Go arena sentinels — any read that returns something else is a hit.
const freeCanarySentinel uint64 = 0xDEADBEEFDEADBEEF

// freeCanarySkipBytes is the size of the buddy free-list next-pointer
// stored at offset 0 of every freed block. The fill / verify both skip it.
const freeCanarySkipBytes uintptr = 8

// Telemetry counters — atomic so status logging can read them safely.
var (
	freeCanaryFills    uint64 // number of paint operations completed
	freeCanaryVerifies uint64 // number of verify operations completed
	freeCanaryHits     uint64 // number of mismatches detected before halt
)

// First-hit capture — populated by verifyFreeCanary on the inaugural
// mismatch, consumed by canaryHalt for formatted logging. The walker
// stops at the first hit; we never read these concurrently with another
// scan because the system halts immediately after.
var (
	canaryHitPA       uintptr
	canaryHitOffset   uintptr
	canaryHitExpected uint64
	canaryHitFound    uint64
)

// FreeCanaryStats returns (fills, verifies, hits) for status output.
func FreeCanaryStats() (fills, verifies, hits uint64) {
	return atomic.LoadUint64(&freeCanaryFills),
		atomic.LoadUint64(&freeCanaryVerifies),
		atomic.LoadUint64(&freeCanaryHits)
}

// fillFreeCanary paints the block of 2^order pages starting at pa with
// freeCanarySentinel, skipping the first 8 bytes (used by the buddy
// free-list next-pointer that buddyInsertFree just wrote). Caller must
// hold buddyAlloc.lock so the page isn't popped concurrently.
//
//go:nosplit
func fillFreeCanary(pa uintptr, order int) {
	if !freeCanaryEnabled || pa == 0 {
		return
	}
	pageVA := pa + buddyAlloc.kernelVAOffset
	blockSize := uintptr(PageSize) << uint(order)
	for off := freeCanarySkipBytes; off+8 <= blockSize; off += 8 {
		*(*uint64)(unsafe.Pointer(pageVA + off)) = freeCanarySentinel
	}
	atomic.AddUint64(&freeCanaryFills, 1)
}

// verifyFreeCanary checks every 8-byte word from offset 8 to blockSize
// matches freeCanarySentinel. On the first mismatch, captures the
// offending pa/offset/expected/found into the canaryHit* globals,
// increments freeCanaryHits, and triggers canaryHalt. Caller must hold
// buddyAlloc.lock.
//
//go:nosplit
func verifyFreeCanary(pa uintptr, order int) {
	if !freeCanaryEnabled || pa == 0 {
		return
	}
	atomic.AddUint64(&freeCanaryVerifies, 1)
	pageVA := pa + buddyAlloc.kernelVAOffset
	blockSize := uintptr(PageSize) << uint(order)
	for off := freeCanarySkipBytes; off+8 <= blockSize; off += 8 {
		v := *(*uint64)(unsafe.Pointer(pageVA + off))
		if v != freeCanarySentinel {
			canaryHitPA = pa
			canaryHitOffset = off
			canaryHitExpected = freeCanarySentinel
			canaryHitFound = v
			atomic.AddUint64(&freeCanaryHits, 1)
			canaryHalt(pa, order)
			return
		}
	}
}

// canaryHalt prints the captured first-hit details and spins, freezing
// the system so the diagnostic output is preserved on the serial log.
// NOT nosplit (drops out of the nosplit chain to use klog.Criticalf,
// mirroring buddyDoubleFreeHalt and stalePTEHalt).
//
//go:noinline
func canaryHalt(pa uintptr, order int) {
	klog.Criticalf("K!W!",
		"[FREE-CANARY] kernel write to freed page detected — pa=0x%x offset=0x%x expected=0x%x found=0x%x order=%d (something wrote into this page after BuddyFreeTyped painted it; the writer is the H-T3 corrupting path)\n",
		uint64(canaryHitPA),
		uint64(canaryHitOffset),
		canaryHitExpected,
		canaryHitFound,
		order)
	for {
	}
}
