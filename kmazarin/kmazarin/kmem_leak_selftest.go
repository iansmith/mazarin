package main

import (
	"mazzy/kmazarin/device/virtio/gpu"
	"mazzy/kmazarin/klog"
	"mazzy/kmazarin/kmem"
)

// runKmemLeakSelfTest is a boot-gated diagnostic (MAZ-108) that exercises the
// process-address-space teardown primitive (kmem.FreeProcessPageTable) and
// asserts the kernel free-frame count returns to baseline after every
// build-then-tear-down cycle — i.e. no leak.
//
// Enabled via the kernel.toml `kmem_leak_test` flag; OFF by default so normal
// boots are unaffected. It MUST run at a quiescent point in boot — before
// launchEmbeddedFS and before IRQs/timer are enabled — so no other thread
// allocates/frees frames while it measures.
//
// Three cycles, each a distinct teardown shape:
//
//	A. bare create/free — CreateProcessPageTable + FreeProcessPageTable.
//	   Exercises L0 alloc + the teardown walk on an otherwise-empty table.
//	B. populated create/free — additionally maps several user pages, which
//	   allocates on-demand L1/L2/L3 intermediate tables + leaf frames. This is
//	   the shape a partially-loaded process leaves behind when loadELF fails
//	   partway: live segment/stack PTEs + the page-table pages built to hold
//	   them. FreeProcessPageTable must reclaim ALL of it.
//	C. framebuffer + constraint mapped, then freed — the real failure-branch
//	   shape. Verifies the PT pages holding those mappings reclaim while the
//	   PD_PINNED framebuffer/constraint LEAF frames are left intact.
//
// delta (before-after of the free-frame count) == 0 for all cycles == GREEN;
// any delta > 0 is a leak.
func runKmemLeakSelfTest() {
	const (
		iters        = 64
		pagesPerProc = 8
		// Arbitrary page-aligned user VA region for cycle B's mappings.
		baseVA = uintptr(0x1000_0000)
	)

	klog.Criticalf("[LT]", "[kmem-leaktest] start iters=%d pagesPerProc=%d\n", iters, pagesPerProc)

	// --- Cycle A: bare create/free ---
	_, beforeA := kmem.GetFrameStats()
	for i := 0; i < iters; i++ {
		l0 := kmem.CreateProcessPageTable()
		if l0 == 0 {
			klog.Errf("[kmem-leaktest] A: CreateProcessPageTable failed at i=%d (out of frames?)\n", i)
			return
		}
		kmem.FreeProcessPageTable(l0)
	}
	_, afterA := kmem.GetFrameStats()
	deltaA := int64(beforeA) - int64(afterA)
	klog.Criticalf("[LT]", "[kmem-leaktest] A bare      : before=%d after=%d delta=%d\n",
		beforeA, afterA, deltaA)

	// --- Cycle B: populated create/free (intermediate tables + leaf frames) ---
	_, beforeB := kmem.GetFrameStats()
	for i := 0; i < iters; i++ {
		l0 := kmem.CreateProcessPageTable()
		if l0 == 0 {
			klog.Errf("[kmem-leaktest] B: CreateProcessPageTable failed at i=%d\n", i)
			return
		}
		for p := 0; p < pagesPerProc; p++ {
			fpa, _ := kmem.AllocAndMapUserPageWithL0(baseVA+uintptr(p)*4096, kmem.ELF_PF_R|kmem.ELF_PF_W, l0)
			if fpa == 0 {
				klog.Errf("[kmem-leaktest] B: AllocAndMapUserPageWithL0 failed i=%d p=%d\n", i, p)
				break
			}
		}
		kmem.FreeProcessPageTable(l0)
	}
	_, afterB := kmem.GetFrameStats()
	deltaB := int64(beforeB) - int64(afterB)
	klog.Criticalf("[LT]", "[kmem-leaktest] B populated : before=%d after=%d delta=%d\n",
		beforeB, afterB, deltaB)

	// --- Cycle C: framebuffer + constraint mapped, then freed ---
	// This is the real teardown shape the DoCloneExecWork / DoRunShepherdWork
	// failure branches reclaim: a page table with the framebuffer + constraint
	// shared pages mapped in. It verifies (a) FreeProcessPageTable reclaims the
	// PT pages built to hold those mappings, and (b) it does NOT free the
	// PD_PINNED framebuffer/constraint LEAF frames (releasePTPage only unpins
	// PT pages, not leaves) — a regression there would corrupt the display /
	// constraint block, which the continued clean boot below would expose.
	fbPA := gpu.GetFramebufferPA()
	fbSize := uintptr(gpu.GetFramebufferSize())
	_, beforeC := kmem.GetFrameStats()
	for i := 0; i < iters; i++ {
		l0 := kmem.CreateProcessPageTable()
		if l0 == 0 {
			klog.Errf("[kmem-leaktest] C: CreateProcessPageTable failed at i=%d\n", i)
			return
		}
		if !kmem.MapUserFramebufferWithL0(fbPA, fbSize, l0) {
			klog.Errf("[kmem-leaktest] C: MapUserFramebufferWithL0 failed i=%d\n", i)
		}
		if !kmem.MapUserConstraintPagesWithL0(l0) {
			klog.Errf("[kmem-leaktest] C: MapUserConstraintPagesWithL0 failed i=%d\n", i)
		}
		kmem.FreeProcessPageTable(l0)
	}
	_, afterC := kmem.GetFrameStats()
	deltaC := int64(beforeC) - int64(afterC)
	klog.Criticalf("[LT]", "[kmem-leaktest] C fb+constr : before=%d after=%d delta=%d\n",
		beforeC, afterC, deltaC)

	if deltaA == 0 && deltaB == 0 && deltaC == 0 {
		klog.Criticalf("[LT]", "[kmem-leaktest] PASS — no leak (all cycles flat)\n")
	} else {
		klog.Criticalf("[LT]", "[kmem-leaktest] LEAK DETECTED — A delta=%d B delta=%d C delta=%d\n",
			deltaA, deltaB, deltaC)
	}
}
