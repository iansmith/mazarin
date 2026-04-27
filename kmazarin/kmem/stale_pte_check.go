// stale_pte_check.go - Diagnostic stale-PTE detector at allocation time.
//
// Bug-B-family Option B: when BuddyAllocTyped hands out a PA, sweep every
// live shepherd's userspace page table looking for a leaf PTE whose target
// PA falls inside the just-allocated range. Any hit is a stale-PTE bug —
// the previous holder did not tear down its mapping when the page was
// released. Once that PA is reissued, the previous holder can write
// through the stale PTE and corrupt whatever the new owner stores there
// (the mspan corruption symptom we are chasing).
//
// Off in production. Default true here ONLY for the active diagnostic;
// flip stalePTECheckEnabled to false (or remove this file) before merging
// unrelated work — every alloc walks every live shepherd's page table.
//
// nosplit discipline: the walker (verifyNoStalePTEs / scanShepherdPTForPARange)
// must stay nosplit because BuddyAllocTyped is called from exception-handler
// chains. The walker therefore must NOT call SerialPuts / SerialHex16 / klog
// (their nested call chains bust the nosplit budget). On a hit the walker
// records the first offender into package-level globals and returns; the
// non-nosplit stalePTEHalt then formats the message via klog.Criticalf and
// freezes the system.

package kmem

import (
	"mazzy/kmazarin/klog"
	"mazzy/kmazarin/proc"
	"sync/atomic"
	"unsafe"
)

// stalePTECheckEnabled toggles the verifier. Default false (Option B
// diagnostic run already completed and was silent across 5 × 180s; H-T2
// ruled out). Re-enable by flipping to true if a future hypothesis
// implicates stale PTEs in PT memory again.
var stalePTECheckEnabled = false

// SetStalePTECheckEnabled toggles the diagnostic at runtime.
func SetStalePTECheckEnabled(v bool) { stalePTECheckEnabled = v }

// StalePTECheckEnabled reports whether the verifier is currently active.
func StalePTECheckEnabled() bool { return stalePTECheckEnabled }

// Telemetry counters — atomic so status logging can read them safely.
var (
	stalePTEScans uint64 // Number of verifyNoStalePTEs invocations that scanned (i.e. enabled & pa != 0)
	stalePTEHits  uint64 // Number of stale-PTE hits detected before halt
)

// First-hit capture — populated by scanShepherdPTForPARange on the
// inaugural hit of a scan, consumed by stalePTEHalt for formatted logging.
// The walker stops at the first hit; we never read these concurrently with
// another scan because the system halts immediately after.
var (
	stalePTEHitSID    proc.ShepherdId
	stalePTEHitVA     uintptr
	stalePTEHitLeafPA uintptr
)

// StalePTEStats returns (scans, hits) for status output.
func StalePTEStats() (scans, hits uint64) {
	return atomic.LoadUint64(&stalePTEScans), atomic.LoadUint64(&stalePTEHits)
}

// verifyNoStalePTEs walks every live shepherd's userspace L0 page table
// looking for any leaf PTE whose PA lies in [pa, pa + 4096<<order). The
// verifier runs immediately after BuddyAllocTyped's SetPageDescriptor:
// the new owner has not yet inserted a PTE for this PA, so any existing
// PTE pointing into the range is by definition stale.
//
//go:nosplit
func verifyNoStalePTEs(pa uintptr, order int) {
	if !stalePTECheckEnabled || pa == 0 {
		return
	}
	atomic.AddUint64(&stalePTEScans, 1)
	rangeEnd := pa + (uintptr(PageSize) << uint(order))
	for i := 0; i < proc.MaxShepherds; i++ {
		if !proc.ShepherdListInUse[i] {
			continue
		}
		sh := &proc.ShepherdListData[i]
		l0PA := sh.PageTableL0PA
		if l0PA == 0 {
			continue
		}
		if scanShepherdPTForPARange(sh.PID, l0PA, pa, rangeEnd) {
			atomic.AddUint64(&stalePTEHits, 1)
			stalePTEHalt(pa, order)
			return
		}
	}
}

// scanShepherdPTForPARange walks the userspace half of one shepherd's L0
// hierarchy, checking every leaf entry. Returns true at the first leaf PTE
// targeting a PA in [paLo, paHi); on a hit, records sid/va/leafPA into the
// stalePTEHit* globals so stalePTEHalt can format them. Logs nothing
// itself (must stay nosplit-budget-safe).
//
//go:nosplit
func scanShepherdPTForPARange(sid proc.ShepherdId, l0PA, paLo, paHi uintptr) bool {
	l0VA := paToVAOrCache(l0PA)
	if l0VA == 0 {
		return false
	}
	maxEntry := platformUserL0MaxEntry()
	for i := 0; i < maxEntry; i++ {
		l0e := *(*uint64)(unsafe.Pointer(l0VA + uintptr(i)*8))
		if !pteIsValid(l0e) {
			continue
		}
		l1PA := pteExtractPA(l0e)
		l1VA := paToVAOrCache(l1PA)
		if l1VA == 0 {
			continue
		}
		for j := 0; j < 512; j++ {
			l1e := *(*uint64)(unsafe.Pointer(l1VA + uintptr(j)*8))
			if !pteIsValid(l1e) || isBlockEntry(l1e, 1) {
				continue
			}
			if i == 0 && platformIsSharedKernelL1(j) {
				continue
			}
			l2PA := pteExtractPA(l1e)
			l2VA := paToVAOrCache(l2PA)
			if l2VA == 0 {
				continue
			}
			for k := 0; k < 512; k++ {
				l2e := *(*uint64)(unsafe.Pointer(l2VA + uintptr(k)*8))
				if !pteIsValid(l2e) || isBlockEntry(l2e, 2) {
					continue
				}
				l3PA := pteExtractPA(l2e)
				l3VA := paToVAOrCache(l3PA)
				if l3VA == 0 {
					continue
				}
				for l := 0; l < 512; l++ {
					l3e := *(*uint64)(unsafe.Pointer(l3VA + uintptr(l)*8))
					if !pteIsValid(l3e) {
						continue
					}
					leafPA := pteExtractPA(l3e)
					if leafPA >= paLo && leafPA < paHi {
						stalePTEHitSID = sid
						stalePTEHitVA = uintptr(i)<<L0Shift |
							uintptr(j)<<L1Shift |
							uintptr(k)<<L2Shift |
							uintptr(l)<<L3Shift
						stalePTEHitLeafPA = leafPA
						return true
					}
				}
			}
		}
	}
	return false
}

// stalePTEHalt prints the captured first-hit details and spins, freezing
// the system so the diagnostic output is preserved on the serial log.
// NOT nosplit (drops out of the nosplit chain to use klog.Criticalf,
// mirroring buddyDoubleFreeHalt in buddy.go).
//
//go:noinline
func stalePTEHalt(pa uintptr, order int) {
	klog.Criticalf("S!P!",
		"[STALE-PTE] sid=%d va=0x%x leafPA=0x%x newAllocPA=0x%x order=%d — previous holder still maps the just-allocated PA\n",
		int(stalePTEHitSID),
		uint64(stalePTEHitVA),
		uint64(stalePTEHitLeafPA),
		uint64(pa),
		order)
	for {
	}
}
