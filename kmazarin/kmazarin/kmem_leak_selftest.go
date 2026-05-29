package main

import (
	"mazzy/kmazarin/device/virtio/gpu"
	"mazzy/kmazarin/klog"
	"mazzy/kmazarin/kmem"
	"mazzy/kmazarin/ksyscall"
	"mazzy/kmazarin/proc"
	"unsafe"
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
//	D. end-to-end real failure: drives an actual DoRunShepherdWork with a
//	   valid-header-but-truncated ELF so it fails at loadELF — AFTER
//	   CreateProcessPageTable + framebuffer/constraint mapping — exercising the
//	   real failure-branch wiring (the kmem.FreeProcessPageTable call in
//	   runshepherd.go), not a replica. Asserts the call returns an error (the
//	   branch fired) and the frame count is balanced across a window that also
//	   tears down the synthetic caller scaffold.
//
// delta (before-after of the free-frame count) == 0 for cycles A–D == GREEN;
// any delta > 0 is a leak. (Cycle D's window is balanced: it brackets the
// caller-scaffold alloc, the DoRunShepherdWork failure, and the scaffold
// teardown, so a clean run nets to zero.)
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
				// Setup failure: free the partial table and abort the whole
				// self-test rather than letting a half-populated cycle still
				// balance to delta=0 and report a false PASS.
				klog.Errf("[kmem-leaktest] B: AllocAndMapUserPageWithL0 failed i=%d p=%d — aborting\n", i, p)
				kmem.FreeProcessPageTable(l0)
				return
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
			// Setup failure: abort rather than report a false PASS (see cycle B).
			klog.Errf("[kmem-leaktest] C: MapUserFramebufferWithL0 failed i=%d — aborting\n", i)
			kmem.FreeProcessPageTable(l0)
			return
		}
		if !kmem.MapUserConstraintPagesWithL0(l0) {
			klog.Errf("[kmem-leaktest] C: MapUserConstraintPagesWithL0 failed i=%d — aborting\n", i)
			kmem.FreeProcessPageTable(l0)
			return
		}
		kmem.FreeProcessPageTable(l0)
	}
	_, afterC := kmem.GetFrameStats()
	deltaC := int64(beforeC) - int64(afterC)
	klog.Criticalf("[LT]", "[kmem-leaktest] C fb+constr : before=%d after=%d delta=%d\n",
		beforeC, afterC, deltaC)

	// --- Cycle D: end-to-end real DoRunShepherdWork failure ---
	// Drives the actual shepherd-launch worker function with a valid-header but
	// truncated ELF (the real fs ELF's 64-byte header, body cut off) so it
	// passes header validation, builds the child page table, maps framebuffer +
	// constraint, then fails inside loadELF at the program-header-bounds check —
	// firing the real `kmem.FreeProcessPageTable(processL0PA)` teardown branch in
	// runshepherd.go. This exercises the wiring on the live system, not a replica.
	//
	// The measurement window brackets EVERYTHING (caller-scaffold alloc →
	// DoRunShepherdWork → scaffold teardown) so a clean run nets to zero:
	//   - callerL0 + its on-demand PT pages + the ELF leaf are allocated here,
	//   - DoRunShepherdWork frees the ELF leaf (unmapUserPages) and allocs+frees
	//     the child page table (the branch under test),
	//   - FreeProcessPageTable(callerL0) frees the caller scaffold.
	// Warmup run: the FIRST DoRunShepherdWork triggers one-time, legitimately
	// permanent allocations (InitKernelAttrManager's first-launch init, initial
	// Go-heap growth for the copy buffer / symbol-table map / log formatting) —
	// those would show as a one-frame "delta" that is NOT a per-call leak. Run
	// once to absorb them, then measure the SECOND run, where only the failure
	// branch's FreeProcessPageTable reclaim is in play.
	retWarm, _ := runForcedRunShepherdFailure()
	retD, deltaD := runForcedRunShepherdFailure()
	klog.Criticalf("[LT]", "[kmem-leaktest] D runshep-fail: retWarm=%d ret=%d delta=%d (ret!=0 means the failure branch fired)\n",
		retWarm, retD, deltaD)

	if deltaA == 0 && deltaB == 0 && deltaC == 0 && deltaD == 0 && retD != 0 {
		klog.Criticalf("[LT]", "[kmem-leaktest] PASS — no leak (all cycles flat; real failure branch reclaimed)\n")
	} else {
		klog.Criticalf("[LT]", "[kmem-leaktest] LEAK/FAIL — A=%d B=%d C=%d D=%d retD=%d\n",
			deltaA, deltaB, deltaC, deltaD, retD)
	}
}

// runForcedRunShepherdFailure builds a synthetic caller address space holding a
// valid-header-but-truncated ELF, drives a real DoRunShepherdWork against it
// (which fails at loadELF and runs the FreeProcessPageTable teardown branch),
// tears the scaffold down, and returns DoRunShepherdWork's result plus the
// free-frame delta across the whole balanced window (0 == no leak). retD != 0
// confirms the failure branch was reached.
func runForcedRunShepherdFailure() (retD int64, deltaD int64) {
	const (
		dVA  = uintptr(0x2000_0000) // arbitrary page-aligned user VA for the ELF
		dLen = 64                   // ELF64 header only → loadELF fails at the program-header bounds check
	)
	if len(EmbeddedFSElf) < dLen {
		klog.Errf("[kmem-leaktest] D: embedded fs ELF too small (%d)\n", len(EmbeddedFSElf))
		return 0, 0
	}

	_, beforeD := kmem.GetFrameStats()

	callerL0 := kmem.CreateProcessPageTable()
	if callerL0 == 0 {
		klog.Errf("[kmem-leaktest] D: CreateProcessPageTable (caller) failed\n")
		return 0, 0
	}
	_, scratch := kmem.AllocAndMapUserPageWithL0(dVA, kmem.ELF_PF_R|kmem.ELF_PF_W, callerL0)
	if scratch == 0 {
		klog.Errf("[kmem-leaktest] D: AllocAndMapUserPageWithL0 (caller) failed\n")
		kmem.FreeProcessPageTable(callerL0)
		return 0, 0
	}
	// Write the valid-but-truncated ELF header into the caller page via its
	// kernel scratch mapping.
	copy(unsafe.Slice((*byte)(unsafe.Pointer(scratch)), dLen), EmbeddedFSElf[:dLen])

	req := ksyscall.RunShepherdWorkRequest{
		Name:           "leaktest-badelf",
		StartVA:        dVA,
		NumPages:       1,
		TotalBytes:     dLen,
		CallerShepherd: &proc.KernelShepherd,
		CallerL0PA:     callerL0,
	}
	retD = ksyscall.DoRunShepherdWork(&req) // fails at loadELF → FreeProcessPageTable(childL0PA)

	// Tear down the caller scaffold (DoRunShepherdWork's unmapUserPages already
	// released the ELF leaf; this frees callerL0 + its PT pages).
	kmem.FreeProcessPageTable(callerL0)

	_, afterD := kmem.GetFrameStats()
	return retD, int64(beforeD) - int64(afterD)
}
