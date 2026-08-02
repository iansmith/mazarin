package main

import (
	"mazzy/kmazarin/klog"
	"mazzy/kmazarin/kmem"
)

// runPageTrackerSelfTest is a boot-gated diagnostic (MAZ-163) that exercises
// the kmem page tracker's shed-on-free path. Modeled directly on
// runKmemLeakSelfTest (MAZ-108, kmem_leak_selftest.go).
//
// Enabled via the kernel.toml `page_tracker_test` flag; OFF by default so
// normal boots are unaffected. Runs at the same quiescent boot point as the
// kmem leak selftest — before launchEmbeddedFS and before IRQs/timer are
// enabled — so no other thread allocates/frees a page mid-measurement.
//
// Drains the deferred queue once up front, before either cycle's baseline
// is taken, to isolate each cycle's own alloc/free behavior from ordinary
// boot-time queueing that hasn't been drained yet (see commit history for
// why this matters and what it measured).
//
// Two cycles:
//
//	A. alloc/free balance — repeatedly build a process page table, map
//	   pagesPerProcA user pages via AllocAndMapUserPageWithL0, then tear the
//	   table down. Measures kmem.TrackedPageCount() before/after the whole
//	   loop; delta must be exactly 0. RED today: delta > 0 (nothing calls
//	   UntrackPage for demand-paged user pages or PT pages).
//	B. saturation — a single process page table, built once and freed once
//	   (bracketing the whole cycle, like cycle A brackets each iteration),
//	   with itersB (> 2*kmem.MaxTrackedPages) repetitions of: map one leaf
//	   page at a fixed VA (DemandMapUserPage, a real per-leaf tracking site,
//	   unlike AllocAndMapUserPageWithL0), unmap it (UnmapUserPageWithL0),
//	   release its frame (ReleasePageByPA — the real BuddyFreeTyped path).
//	   Reuses a single L0/VA across the whole loop rather than rebuilding
//	   per iteration, to avoid churning through page-table pages at a scale
//	   that saturates the separate, pre-existing ptVACache (paging.go) —
//	   not a page-tracker bug, out of scope here. Verifies
//	   kmem.GetTrackerFullWarnings() doesn't advance and the tracked count
//	   returns to baseline. RED today: the warning fires once the tracker
//	   hits its cap and never lets go.
func runPageTrackerSelfTest() {
	const (
		itersA        = 64
		pagesPerProcA = 8
		baseVAA       = uintptr(0x1000_0000)

		itersB  = 2*kmem.MaxTrackedPages + 512 // > 2*MaxTrackedPages
		leafVAB = uintptr(0x2000_0000)
	)

	klog.Criticalf("[PT]", "[page-tracker-test] start itersA=%d pagesPerProcA=%d itersB=%d\n",
		itersA, pagesPerProcA, itersB)

	preDrainCount := kmem.TrackedPageCount()
	preDrainOverflows := kmem.GetDeferredOverflows()
	preDrainUntrackOverflows := kmem.GetDeferredUntrackOverflows()
	kmem.ProcessDeferredRecords()
	klog.Criticalf("[PT]", "[page-tracker-test] initial drain: pre-count=%d post-count=%d overflows=%d untrackOverflows=%d\n",
		preDrainCount, kmem.TrackedPageCount(), preDrainOverflows, preDrainUntrackOverflows)

	// --- Cycle A: alloc/free balance ---
	beforeA := kmem.TrackedPageCount()
	for i := 0; i < itersA; i++ {
		l0 := kmem.CreateProcessPageTable()
		if l0 == 0 {
			klog.Errf("[page-tracker-test] A: CreateProcessPageTable failed at i=%d (out of frames?)\n", i)
			return
		}
		ok := true
		for p := 0; p < pagesPerProcA; p++ {
			fpa, _ := kmem.AllocAndMapUserPageWithL0(baseVAA+uintptr(p)*4096, kmem.ELF_PF_R|kmem.ELF_PF_W, l0)
			if fpa == 0 {
				klog.Errf("[page-tracker-test] A: AllocAndMapUserPageWithL0 failed i=%d p=%d — aborting\n", i, p)
				ok = false
				break
			}
		}
		kmem.ProcessDeferredRecords()
		if !ok {
			// Setup failure: free the partial table and abort the whole
			// self-test rather than letting a half-populated cycle still
			// balance to delta=0 and report a false PASS.
			kmem.FreeProcessPageTable(l0)
			kmem.ProcessDeferredRecords()
			return
		}
		kmem.FreeProcessPageTable(l0)
		kmem.ProcessDeferredRecords()
	}
	afterA := kmem.TrackedPageCount()
	deltaA := int64(afterA) - int64(beforeA)
	klog.Criticalf("[PT]", "[page-tracker-test] A balance  : before=%d after=%d delta=%d\n",
		beforeA, afterA, deltaA)

	// --- Cycle B: saturation ---
	// Bracket the whole cycle (L0 create through L0 free) the same way
	// cycle A brackets each of its iterations, so deltaB reflects the
	// entire cycle balancing to zero rather than leaving l0B's own
	// (correctly still-live) PT-page records counted as part of "after".
	beforeB := kmem.TrackedPageCount()
	warningsBeforeB := kmem.GetTrackerFullWarnings()

	l0B := kmem.CreateProcessPageTable()
	if l0B == 0 {
		klog.Errf("[page-tracker-test] B: CreateProcessPageTable failed (out of frames?) — aborting\n")
		return
	}
	kmem.ProcessDeferredRecords()

	completed := 0
	for i := 0; i < itersB; i++ {
		fpa := kmem.DemandMapUserPage(leafVAB, l0B)
		if fpa == 0 {
			klog.Errf("[page-tracker-test] B: DemandMapUserPage failed i=%d — aborting\n", i)
			break
		}
		kmem.ProcessDeferredRecords()

		freedPA := kmem.UnmapUserPageWithL0(leafVAB, l0B)
		if freedPA != fpa {
			klog.Errf("[page-tracker-test] B: UnmapUserPageWithL0 mismatch i=%d got=%x want=%x — aborting\n",
				i, uint64(freedPA), uint64(fpa))
			break
		}
		kmem.ReleasePageByPA(freedPA)
		kmem.ProcessDeferredRecords()
		completed++
	}

	kmem.FreeProcessPageTable(l0B)
	kmem.ProcessDeferredRecords()

	afterB := kmem.TrackedPageCount()
	deltaB := int64(afterB) - int64(beforeB)
	warningsAfterB := kmem.GetTrackerFullWarnings()
	warningsDeltaB := warningsAfterB - warningsBeforeB
	untrackOverflows := kmem.GetDeferredUntrackOverflows()

	klog.Criticalf("[PT]", "[page-tracker-test] B saturate : before=%d after=%d delta=%d completed=%d/%d warnings=%d untrackOverflows=%d\n",
		beforeB, afterB, deltaB, completed, itersB, warningsDeltaB, untrackOverflows)

	if deltaA == 0 && deltaB == 0 && warningsDeltaB == 0 && completed == itersB && untrackOverflows == 0 {
		klog.Criticalf("[PT]", "[page-tracker-test] PASS — tracker sheds records on free; no saturation warnings\n")
	} else {
		klog.Criticalf("[PT]", "[page-tracker-test] LEAK/FAIL — deltaA=%d deltaB=%d warnings=%d completed=%d/%d untrackOverflows=%d\n",
			deltaA, deltaB, warningsDeltaB, completed, itersB, untrackOverflows)
	}
}
