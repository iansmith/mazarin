//go:build amd64

package main

import (
	"sync/atomic"
	"unsafe"

	"mazzy/kmazarin/klog"
	"mazzy/kmazarin/ksyscall"
)

// MAZ-141 — priority-wake diagnostic ring (amd64).
//
// The amd64 priority-wake fast switch (exceptions_amd64.s, device_pwake_allowed)
// routes through PriorityWakeSwitch instead of CheckThreadPreemption so we can
// record the interrupted context at every fast switch. If the dual-home-`g`
// `morestack on g0` family ever fires on the woken side, the ring — dumped fresh
// when a shepherd dies abnormally (ksyscall.OnShepherdExit) — names the unsafe
// resume context: correlate the dying `E<pid>` shepherd against the pwake event
// that switched TO it (next=tid/pid), then addr2line that event's rip.
//
// amd64-only BY DESIGN (documented, not silent — see
// feedback_amd64_arch_divergence.md): ARM64's priority-wake switch is inline asm
// in exceptions_arm64.s with a single g home (X28) and therefore no dual-home
// morestack hazard; it tracks switch counts via dbgPWakeSwitched. The
// ksyscall.OnShepherdExit hook simply stays nil on ARM64 (see
// pwake_trace_arm64.go).

// PriorityWakeSwitch records the interrupted context, runs the identical
// scheduler switch (checkThreadPreemptionImpl — the same entry
// CheckThreadPreemption uses), records the chosen thread, and returns the new
// ThreadContext pointer (0 = no switch). Diagnostic only; switch behaviour is
// unchanged. Implemented as a tail-call stub in abi_stubs_amd64.s.
func PriorityWakeSwitch(framePtr uint64) uint64

// pwakeEvent records one priority-wake fast SWITCH (no-switch firings are not
// ringed — they carry no woken-thread context; dbgPWakeNoCtx counts them).
type pwakeEvent struct {
	seq     uint32
	cs      uint64
	rip     uint64
	oldTID  int32
	oldPID  int32
	nextTID int32
	nextPID int32
}

const pwakeRingSize = 64

var (
	pwakeRing [pwakeRingSize]pwakeEvent
	pwakeSeq  uint32
	// pendingPwake is the single in-flight event. The priority-wake path runs
	// IRQ-off inside the device-IRQ handler (before iretq), so pre/post recording
	// cannot be re-entered — one global slot is safe.
	pendingPwake pwakeEvent
)

//go:nosplit
func priorityWakeSwitchInternal(framePtr uint64) uint64 {
	recordPwakePre(framePtr)
	ctx := checkThreadPreemptionImpl(&NormalSchedulerFunc, framePtr)
	recordPwakePost(ctx)
	return ctx
}

//go:nosplit
func recordPwakePre(framePtr uint64) {
	// Frame offsets are shared with goroutine_preempt_amd64.go.
	frame := (*[40]uint64)(unsafe.Pointer(uintptr(framePtr)))
	pendingPwake.cs = frame[excFrameCS/8]
	pendingPwake.rip = frame[excFrameRIP/8]
	pid, tid := GetCurrentThreadPIDAndTID()
	pendingPwake.oldPID = int32(pid)
	pendingPwake.oldTID = int32(tid)
}

//go:nosplit
func recordPwakePost(ctx uint64) {
	if ctx == 0 {
		return // no switch — counted by dbgPWakeNoCtx; no woken context to ring
	}
	// checkThreadPreemptionImpl already made the woken thread current.
	pid, tid := GetCurrentThreadPIDAndTID()
	pendingPwake.nextPID = int32(pid)
	pendingPwake.nextTID = int32(tid)
	seq := atomic.AddUint32(&pwakeSeq, 1)
	pendingPwake.seq = seq
	pwakeRing[(seq-1)%pwakeRingSize] = pendingPwake
}

// dumpPwakeRing prints the most recent pwake events (newest last). Registered as
// ksyscall.OnShepherdExit so a crashing shepherd dumps the ring while it is still
// fresh (at block-IRQ rates a 64-entry ring spans only ~100ms, so a periodic
// status-line dump would already be stale). NOT nosplit — klog uses variadic
// args; the shepherd-exit call site (exitGroupImpl) is non-nosplit.
func dumpPwakeRing() {
	total := atomic.LoadUint32(&pwakeSeq) // == count of switches ringed
	klog.Criticalf("pwake", "[pwake] dump switches=%d checked=%d svc=%d noctx=%d\n",
		total, atomic.LoadUint32(&dbgPWakeChecked),
		atomic.LoadUint32(&dbgPWakeSVC), atomic.LoadUint32(&dbgPWakeNoCtx))
	n := min(uint32(16), total)
	for i := total - n; i < total; i++ {
		e := &pwakeRing[i%pwakeRingSize]
		klog.Criticalf("pwake", "  #%d cs=%#x rip=%#x old=%d/%d next=%d/%d\n",
			e.seq, e.cs, e.rip, e.oldTID, e.oldPID, e.nextTID, e.nextPID)
	}
	gspMismatch := atomic.LoadUint64(&dbgGSPMismatch)
	klog.Criticalf("gsp", "[gsp] mismatch=%d skipped=%d (MAZ-143: detector tripwire / fix-C guarded switches)\n",
		gspMismatch, atomic.LoadUint64(&dbgGSPSkipped))
	if gspMismatch != 0 {
		// The tripwire fired (should never happen post-fix C — the guard skips the
		// switch before SaveContextFromFrame). Surface the most-recent captured
		// unsafe context so a slipped-through case is diagnosable, not just counted.
		klog.Criticalf("gsp", "  last unsafe save: g=%#x sp=%#x rip=%#x g0.stack=[%#x,%#x)\n",
			atomic.LoadUint64(&dbgGSPLastG), atomic.LoadUint64(&dbgGSPLastSP),
			atomic.LoadUint64(&dbgGSPLastRIP), atomic.LoadUint64(&dbgGSPLastLo),
			atomic.LoadUint64(&dbgGSPLastHi))
	}
	dumpNestStats() // MAZ-139: nested-exception detector counters, same exit channel
}

func init() {
	ksyscall.OnShepherdExit = dumpPwakeRing
}
