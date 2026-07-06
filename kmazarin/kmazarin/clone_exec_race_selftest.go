package main

import (
	"mazzy/kmazarin/device/virtio/gpu"
	"mazzy/kmazarin/klog"
	"mazzy/kmazarin/kmem"
	"mazzy/kmazarin/proc"
)

// runCloneExecRaceSelfTest is a boot-gated diagnostic (MAZ-112) that proves the
// clone_exec intent-visibility race window is closed: a child created by
// CreateCloneExecThread has its race-sensitive startup state (ParentPID +
// buffered intent + chdir target) populated the MOMENT it becomes schedulable,
// not afterward.
//
// Enabled via the kernel.toml `clone_exec_race_test` flag; OFF by default so
// normal boots are unaffected. It MUST run at a quiescent point in boot —
// before launchEmbeddedFS, with IRQs masked by the caller (main.go) — so the
// synthetic child is created and reaped without the scheduler ever running it.
//
// How the race is made observable DETERMINISTICALLY (no probabilistic
// interleaving):
//
//	createCloneExecThreadImpl fires sf.StateCheck("create-userspace-thread-complete")
//	AFTER the child is enqueued to the ready queue, still under schedulerLock —
//	exactly the instant the child first becomes schedulable. The self-test
//	installs a SchedulerFunc whose StateCheck reads back the child's
//	ParentPID / NumStartupIntent / StartupCwdLen at that instant.
//	  - POST-FIX (SetStartupState runs before enqueue): fields populated → PASS.
//	  - PRE-FIX (fields set by the lock-free scan after the create call): zero
//	    at the hook → FAIL.
//
// The self-test creates a REAL child thread + shepherd and MUST reap it to
// leave the frame / PID / TID counts balanced (a leak would break the
// subsequent normal boot of all shepherds).
//
// Warmup: like the kmem leak self-test's cycle D, the FIRST create/reap cycle
// triggers one-time, legitimately permanent allocations (first-launch lazy
// paging init, the first constraint mapping's intermediate tables, Go-heap
// growth for klog formatting). Those would show as a non-leak "delta" on a
// single run. So the self-test runs one warmup cycle to absorb them, then
// measures the SECOND cycle — where only the per-cycle alloc/free is in play,
// so a clean run nets to delta=0.
func runCloneExecRaceSelfTest() {
	klog.Criticalf("[CR]", "[clone-exec-race] start\n")

	// Warmup cycle: absorb one-time allocations. Result is logged but not gated.
	warm := runCloneExecRaceCycle()

	// Measured cycle: the real RED→GREEN. The window is internally balanced
	// (build → create → reap), so a clean run nets delta=0.
	res := runCloneExecRaceCycle()

	klog.Criticalf("[CR]", "[clone-exec-race] warmup delta=%d (one-time allocs absorbed)\n", warm.delta)

	// PASS only if: the hook fired, every race-sensitive field was already
	// populated at the hook (window closed), and the child reaped cleanly
	// (delta==0, no leak) on the measured cycle.
	pass := res.observed &&
		res.gotParentPID == cloneExecRaceParentPID &&
		res.gotNumIntent == cloneExecRaceNumIntent &&
		res.gotCwdLen == cloneExecRaceCwdLen &&
		res.delta == 0

	if pass {
		klog.Criticalf("[CR]", "[clone-exec-race] PASS — fields populated before enqueue (parent=%d nIntent=%d cwdLen=%d) delta=%d\n",
			res.gotParentPID, res.gotNumIntent, res.gotCwdLen, res.delta)
	} else {
		klog.Criticalf("[CR]", "[clone-exec-race] FAIL — observed=%v parent=%d(want %d) nIntent=%d(want %d) cwdLen=%d(want %d) delta=%d\n",
			res.observed, res.gotParentPID, cloneExecRaceParentPID,
			res.gotNumIntent, cloneExecRaceNumIntent, res.gotCwdLen, cloneExecRaceCwdLen, res.delta)
	}
}

// Fixed self-test inputs. Package-level so both the cycle driver and the
// PASS/FAIL gate reference the same expected values (one source of truth).
const (
	// Arbitrary page-aligned user VAs for the synthetic child's entry and
	// stack pages. The self-test does NOT loadELF — CreateCloneExecThread only
	// needs a mapped entry VA + stack VA + the child L0PA; it never dereferences
	// them (the child never runs).
	cloneExecRaceEntryVA = uintptr(0x4000_0000)
	cloneExecRaceStackVA = uintptr(0x4001_0000)

	cloneExecRaceParentPID proc.ShepherdId = proc.MinPID + 7
	cloneExecRaceNumIntent uint32          = 2  // len(intent) below
	cloneExecRaceCwdLen    uint32          = 25 // len(cwd) below
)

// cloneExecRaceResult captures one create/reap cycle's observations.
type cloneExecRaceResult struct {
	observed     bool            // did the StateCheck hook fire?
	gotParentPID proc.ShepherdId // child ParentPID read at the hook
	gotNumIntent uint32          // child NumStartupIntent read at the hook
	gotCwdLen    uint32          // child StartupCwdLen read at the hook
	delta        int64           // free-frame delta across the balanced window (0 == no leak)
}

// runCloneExecRaceCycle builds a synthetic child address space, drives the real
// createCloneExecThreadImpl against it while reading the race-sensitive fields
// at the StateCheck hook, then reaps the child — all inside one balanced
// free-frame measurement window. A setup failure returns a zero result (the
// gate then reports FAIL).
func runCloneExecRaceCycle() cloneExecRaceResult {
	var res cloneExecRaceResult

	_, beforeFrames := kmem.GetFrameStats()

	// --- Build a minimal child address space (the cheap way kmem cycle B/C
	// does): a fresh page table + framebuffer/constraint shared pages (so it
	// matches a real clone_exec child) + an entry page + a stack page. ---
	childL0PA := kmem.CreateProcessPageTable()
	if childL0PA == 0 {
		klog.Errf("[clone-exec-race] CreateProcessPageTable failed\n")
		return res
	}
	fbPA := gpu.GetFramebufferPA()
	fbSize := uintptr(gpu.GetFramebufferSize())
	if !kmem.MapUserFramebufferWithL0(fbPA, fbSize, childL0PA) {
		klog.Errf("[clone-exec-race] MapUserFramebufferWithL0 failed — aborting\n")
		kmem.FreeProcessPageTable(childL0PA)
		return res
	}
	if !kmem.MapUserConstraintPagesWithL0(childL0PA) {
		klog.Errf("[clone-exec-race] MapUserConstraintPagesWithL0 failed — aborting\n")
		kmem.FreeProcessPageTable(childL0PA)
		return res
	}
	if fpa, _ := kmem.AllocAndMapUserPageWithL0(cloneExecRaceEntryVA, kmem.ELF_PF_R|kmem.ELF_PF_X, childL0PA); fpa == 0 {
		klog.Errf("[clone-exec-race] map entry page failed — aborting\n")
		kmem.FreeProcessPageTable(childL0PA)
		return res
	}
	if fpa, _ := kmem.AllocAndMapUserPageWithL0(cloneExecRaceStackVA, kmem.ELF_PF_R|kmem.ELF_PF_W, childL0PA); fpa == 0 {
		klog.Errf("[clone-exec-race] map stack page failed — aborting\n")
		kmem.FreeProcessPageTable(childL0PA)
		return res
	}

	// The intent + cwd the child should be created with. Non-empty so the
	// readback can distinguish populated (PASS) from unset (FAIL). Lengths are
	// pinned by the cloneExecRaceNumIntent / cloneExecRaceCwdLen constants.
	intent := []proc.CloneExecIntentOp{
		{Kind: proc.IntentDup3, Arg0: 3, Arg1: 1, Arg2: 0},
		{Kind: proc.IntentClose, Arg0: 4},
	}
	cwd := []byte("/clone-exec-race-selftest")

	// --- Install a SchedulerFunc whose StateCheck reads the child's
	// race-sensitive fields at the instant it becomes schedulable. ---
	selftestSF := NormalSchedulerFunc // copy real DAIF primitives
	selftestSF.StateCheck = func(checkpoint string) {
		if checkpoint != "create-userspace-thread-complete" {
			return
		}
		// Find the just-created child by its L0PA. It is the only shepherd
		// being allocated here, and we run single-threaded with IRQs masked.
		proc.Shepherds.ForEach(func(p *proc.Shepherd) bool {
			if p.PageTableL0PA != childL0PA {
				return true // keep iterating
			}
			res.observed = true
			res.gotParentPID = p.ParentPID
			res.gotNumIntent = p.NumStartupIntent
			res.gotCwdLen = p.StartupCwdLen
			return false // found it
		})
	}

	// --- Drive the real create primitive. ---
	// No reserved PID for this test (0 = allocate fresh, non-vfork path).
	childTID, childPID := createCloneExecThreadImpl(&selftestSF,
		uint64(cloneExecRaceEntryVA), uint64(cloneExecRaceStackVA),
		childL0PA, cloneExecRaceParentPID, 0, intent, cwd)

	// --- Reap the real child so frame / PID / TID counts stay balanced. The
	// child must be plucked from the ready queue BEFORE its slot is released so
	// the scheduler never runs it. ---
	reapSelfTestChild(childTID, childPID, childL0PA)

	_, afterFrames := kmem.GetFrameStats()
	res.delta = int64(beforeFrames) - int64(afterFrames)
	return res
}

// reapSelfTestChild tears down the synthetic child created by the MAZ-112
// self-test, balancing the thread / PID / TID counts. It deliberately does NOT
// reuse releaseShepherdSchedLockHeld: the synthetic child has no DMA clumps and
// no live ASID mappings the scheduler will touch (it never ran), so the full
// production teardown's TLB shootdown + DMA cleanup + Spans-driven page reclaim
// is unnecessary. Instead it plucks the child thread, releases the thread + ID
// + shepherd slot under schedulerLock, then frees the whole child page table
// (which reclaims the entry/stack leaf frames and all PT pages; the PD_PINNED
// framebuffer/constraint leaves are left intact, exactly like the failure
// branches in DoCloneExecWork).
func reapSelfTestChild(childTID int16, childPID proc.ShepherdId, childL0PA uintptr) {
	savedDAIF := SaveAndDisableIRQs()
	schedulerLock.Lock()

	if t := threadLookupByTID(int32(childTID)); t != nil {
		t.State = ThreadExited
		pluckFromAllQueues(t.TID)
		threadIdAllocator.Release(t.TID)
		threadList.Release(int(threadToIdx(t)))
	}

	if _, ok := proc.Shepherds.Get(childPID); ok {
		proc.Shepherds.Release(childPID)
		shepherdIdAllocator.Free(childPID)
	}

	schedulerLock.Unlock()
	RestoreIRQs(savedDAIF)

	// Free the child address space outside the lock (FreeProcessPageTable is the
	// heavy, splittable teardown walk — must not run under the scheduler
	// spinlock). This reclaims the entry/stack leaf frames + every PT page.
	kmem.FreeProcessPageTable(childL0PA)
}
