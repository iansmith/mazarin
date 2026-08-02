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
// Drains the deferred queue once before either cycle's baseline is taken.
// By this point in boot, real kernel/PT allocations have already queued
// deferred track records that nothing has drained yet — the bottom-half
// goroutine that normally does this needs the scheduler running, which
// this quiescent point predates. Left undrained, that backlog (observed:
// ~1023 records, 75 already dropped by ring overflow before this self-test
// even starts) lands on whichever cycle calls ProcessDeferredRecords()
// first, corrupting its baseline. Draining it up front, before either
// cycle's "before" snapshot, isolates each cycle's own alloc/free
// behavior from ordinary boot noise.
//
// Two cycles:
//
//	A. alloc/free balance — repeatedly build a process page table, map
//	   pagesPerProcA user pages (draining the deferred queue after both the
//	   alloc and the free), then tear the table down. Measures
//	   kmem.TrackedPageCount() before/after the whole loop; delta must be
//	   exactly 0 — every mapped page's tracker record must be shed when its
//	   frame is freed. RED today: delta == itersA*pagesPerProcA-ish (nothing
//	   ever calls UntrackPage for demand-paged user pages or PT pages).
//	B. saturation — a single process page table, built once and freed once
//	   (both inside the measurement window, matching cycle A's bracket-the-
//	   whole-cycle shape), with itersB repetitions in between of: map one
//	   leaf page at a fixed VA (DemandMapUserPage), drain, unmap it
//	   (UnmapUserPageWithL0) and release its frame (ReleasePageByPA — the
//	   real BuddyFreeTyped path), drain again. itersB > 2*kmem.MaxTrackedPages,
//	   so the cumulative number of tracked allocations exceeds the tracker's
//	   capacity many times over. Verifies kmem.GetTrackerFullWarnings() does
//	   not advance (no "[kmem] WARN: page tracker full" during the run) and
//	   that the tracked count returns to its pre-cycle baseline once the L0
//	   itself is torn down. RED today: the warning fires once the 32768th
//	   record lands and then continuously for the rest of the cycle.
//
//	   Reuses one L0 across all itersB iterations (rather than cycle A's
//	   per-iteration CreateProcessPageTable/FreeProcessPageTable): a naive
//	   per-iteration rebuild, run itersB times, drives enough distinct
//	   L1/L2/L3 page-table-page churn to fill the unrelated, separately
//	   fixed-size ptVACache (paging.go, 2048 entries, append-only, never
//	   evicts — already close to full from ordinary boot-time page table
//	   setup by the time this quiescent-point selftest runs) and spam ITS
//	   own unbounded warning — not a page-tracker bug, but it made cycle B
//	   unable to finish at any iteration count. Reusing a single L0/VA means
//	   every iteration after the first walks straight to the existing L3
//	   leaf slot (paToVAOrCache, read-only) instead of allocating new PT
//	   pages, so this cycle's ptVACache footprint is O(1) regardless of
//	   itersB.
//
//	   DemandMapUserPage (not AllocAndMapUserPageWithL0, which cycle A
//	   uses) is deliberate: AllocAndMapUserPageWithL0's own frame-alloc path
//	   (AllocUserFrame + mapUserPageWithL0) never calls QueueDeferredRecord
//	   — cycle A's delta comes entirely from its on-demand L1/L2/L3
//	   page-table-page tracking, not its leaf pages. DemandMapUserPage is
//	   one of the five real QueueDeferredRecord sites (paging.go,
//	   Type=PageAllocUser) and, since leafVAB is unmapped at the top of
//	   every iteration (freed at the bottom), tracks a genuinely new record
//	   each time — the shape this cycle needs to actually drive the tracker
//	   to its cap.
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
	kmem.ProcessDeferredRecords()
	klog.Criticalf("[PT]", "[page-tracker-test] initial drain: pre-count=%d post-count=%d overflows=%d\n",
		preDrainCount, kmem.TrackedPageCount(), preDrainOverflows)

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

	klog.Criticalf("[PT]", "[page-tracker-test] B saturate : before=%d after=%d delta=%d completed=%d/%d warnings=%d\n",
		beforeB, afterB, deltaB, completed, itersB, warningsDeltaB)

	if deltaA == 0 && deltaB == 0 && warningsDeltaB == 0 && completed == itersB {
		klog.Criticalf("[PT]", "[page-tracker-test] PASS — tracker sheds records on free; no saturation warnings\n")
	} else {
		klog.Criticalf("[PT]", "[page-tracker-test] LEAK/FAIL — deltaA=%d deltaB=%d warnings=%d completed=%d/%d\n",
			deltaA, deltaB, warningsDeltaB, completed, itersB)
	}
}
