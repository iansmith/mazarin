package main

// [MAZ-179 probe — NOT FOR MERGE] Stale io_uring ring-page tripwire.
//
// Every IRQ top-half CQE producer (block, input ×2, net RX/TX) writes a
// 16-byte CQEntry plus the 4-byte CQTail word through a KVA captured at
// SysIOUringSetup time. If that KVA ever goes stale — page unpinned and
// reused while the kernel-side pointer is still non-nil — those stores
// scribble whatever now lives in the page: the kernel-computed-VA stray-
// store class MAZ-179's word-shaped evidence points at.
//
// SysIOUringSetup stamps CQRingMask=63 and CQCount=64 into the page; user-
// space never writes those fields. cqRingLive verifies the stamp before
// each push: a reused page will essentially never carry both constants, so
// a mismatch means the KVA no longer points at a live ring. The producer
// then SKIPS the write (neutralizing the scribble) and the trip is counted
// here, drained by the thread-0 status printer ([CQ_STALE] line).

import (
	"sync/atomic"
	"unsafe"

	"mazzy/kmazarin/asm"
	"mazzy/kmazarin/kirq"
	"mazzy/kmazarin/klog"
	"mazzy/kmazarin/kmem"
	"mazzy/kmazarin/ksyscall"
	"mazzy/kmazarin/proc"
	"mazzy/shared/iouring"
)

// [MAZ-179 probe — NOT FOR MERGE] Serial-guaranteed probe heartbeat.
//
// The [status] epoch print is produced by a bottom-half GOROUTINE that
// demonstrably starves mid-boot (tier-4b: five boots never printed once;
// others stopped at 30-130s), so probe counters attached to it are stale
// snapshots. This heartbeat is driven directly from KernelIdleLoop —
// thread 0's loop that never yields by design — and emits via Criticalf
// (rawWrite → PollWrite, unconditionally serial-visible). One line per
// ~20s plus one at every shepherd death for exact corruption correlation.
var maz179LastBeatTick uint64

func maz179ProbeHeartbeatLine(tag string, sid int64) {
	var up uint64
	if freq := uint64(kirq.GetTimerFrequency()); freq > 0 && kernelBootTick > 0 {
		up = (kirq.ReadCounterValue() - kernelBootTick) / freq
	}
	ftxWait, ftxBlk, ftxEag, ftxWake := ksyscall.PrintFutexStats()
	klog.Criticalf("[MAZ179HB] ",
		"[MAZ179HB] %s up=%ds sid=%d blk: compl=%d submits=%d dma: frees=%d blkhits=%d nethits=%d race=%d deaths=%d cq: stale=%d gid: chk=%d mis=%d unr=%d first_tid=%d g=0x%x m=0x%x want=0x%x grant: ovl=%d first_sid=%d va=0x%x len=0x%x fixed=%d hintfb=%d mfix: res=%d pages=%d offband=%d first_va=0x%x first_res=%d ftx: wait=%d blk=%d eag=%d wake=%d woken=%d w0=%d lost=%d lta=0x%x ltid=%d dlw=%d pidmis=%d ctx: sav=%d res=%d s=%d elr=0x%x spsr=0x%x tid=%d pw: chk=%d sw=%d el1h=%d svc=%d noctx=%d tmr: sw=%d skl1h=%d sksvc=%d eret: nest=%d nelr=0x%x nsite=%d leak=%d nr1=%d nrL=%d yld: n=%d hnd=%d lr=0x%x sp=0x%x daif=0x%x chain: d=%d max=%d conc=%d spmis=%d span: full=%d drop=%d resv=%d occhw=%d va=0x%x len=0x%x dlg: fail=%d sys=%d sid=%d ring=%d dci: tot=%d unal=%d va=0x%x len=%d kw: tot=%d kern=%d other=%d ova=0x%x dlgw: gone=%d ringfull=%d\n",
		tag, up, sid,
		atomic.LoadUint32(&proc.BlkComplTotal),
		atomic.LoadUint32(&proc.BlkSubmitTotal),
		atomic.LoadUint32(&proc.DmaClumpFrees),
		atomic.LoadUint32(&proc.DmaFreeBlkHits),
		atomic.LoadUint32(&proc.DmaFreeNetHits),
		atomic.LoadUint32(&proc.BlkFreeRace),
		atomic.LoadUint32(&proc.ShepherdDeaths),
		atomic.LoadUint32(&dbgCQStaleTrips),
		atomic.LoadUint32(&proc.GIdentChecks),
		atomic.LoadUint32(&proc.GIdentMismatch),
		atomic.LoadUint32(&proc.GIdentUnreadable),
		atomic.LoadUint64(&proc.GIdentFirstTID),
		atomic.LoadUint64(&proc.GIdentFirstG),
		atomic.LoadUint64(&proc.GIdentFirstM),
		atomic.LoadUint64(&proc.GIdentFirstWantM),
		atomic.LoadUint32(&proc.GrantOverlapTrips),
		atomic.LoadInt32(&proc.GrantOverlapFirstSID),
		atomic.LoadUint64(&proc.GrantOverlapFirstVA),
		atomic.LoadUint64(&proc.GrantOverlapFirstLen),
		atomic.LoadUint32(&proc.GrantFixedMaps),
		atomic.LoadUint32(&proc.GrantHintFallbacks),
		atomic.LoadUint32(&proc.MFixResidentTrips),
		atomic.LoadUint32(&proc.MFixResidentPages),
		atomic.LoadUint32(&proc.MFixOffBandResident),
		atomic.LoadUint64(&proc.MFixFirstVA),
		atomic.LoadUint32(&proc.MFixFirstResident),
		ftxWait, ftxBlk, ftxEag, ftxWake,
		atomic.LoadUint64(&proc.FtxWokenTotal),
		atomic.LoadUint64(&proc.FtxWakeZero),
		atomic.LoadUint64(&proc.FtxLostWake),
		atomic.LoadUint64(&proc.FtxLostWakeAddr),
		atomic.LoadUint64(&proc.FtxLostWakeTID),
		atomic.LoadUint64(&proc.FtxDeadlineWakes),
		atomic.LoadUint64(&DbgFutexPIDMismatch),
		atomic.LoadUint64(&proc.CtxBadSaves),
		atomic.LoadUint64(&proc.CtxBadResumes),
		atomic.LoadUint32(&proc.CtxBadFirstSite),
		atomic.LoadUint64(&proc.CtxBadFirstELR),
		atomic.LoadUint64(&proc.CtxBadFirstSPSR),
		atomic.LoadUint64(&proc.CtxBadFirstTID),
		atomic.LoadUint32(&dbgPWakeChecked),
		atomic.LoadUint32(&dbgPWakeSwitched),
		atomic.LoadUint32(&dbgPWakeEL1h),
		atomic.LoadUint32(&dbgPWakeSVC),
		atomic.LoadUint32(&dbgPWakeNoCtx),
		atomic.LoadUint64(&timerCtxSwitchCount),
		atomic.LoadUint64(&dbgTimerSkipEL1h),
		atomic.LoadUint64(&dbgTimerSkipSVC),
		atomic.LoadUint64(&proc.EretNestedResumes),
		atomic.LoadUint64(&proc.EretNestedFirstELR),
		atomic.LoadUint32(&proc.EretNestedFirstSite),
		atomic.LoadUint64(&proc.DispatchIrqLeaks),
		atomic.LoadUint64(&proc.DispatchLeakFirstNr),
		atomic.LoadUint64(&proc.DispatchLeakLastNr),
		atomic.LoadUint64(&proc.YieldCalls),
		atomic.LoadUint64(&proc.YieldFromHandler),
		atomic.LoadUint64(&proc.YieldHandlerFirstLR),
		atomic.LoadUint64(&proc.YieldHandlerFirstSP),
		atomic.LoadUint64(&proc.YieldHandlerFirstDAIF),
		atomic.LoadUint64(&proc.YieldChainDepth),
		atomic.LoadUint64(&proc.YieldChainMax),
		atomic.LoadUint64(&proc.YieldChainConcurrent),
		atomic.LoadUint64(&proc.YieldSPEL1Mismatch),
		atomic.LoadUint64(&proc.SpanAddFull),
		atomic.LoadUint64(&proc.SpanSplitDrop),
		atomic.LoadUint64(&proc.SpanReserveFull),
		atomic.LoadUint64(&proc.SpanOccHigh),
		atomic.LoadUint64(&proc.SpanLossFirstVA),
		atomic.LoadUint64(&proc.SpanLossFirstLen),
		atomic.LoadUint64(&proc.DlgSendFail),
		atomic.LoadUint64(&proc.DlgFailFirstSys),
		atomic.LoadUint64(&proc.DlgFailFirstSID),
		atomic.LoadUint64(&proc.DlgFailFirstRing),
		atomic.LoadUint64(&asm.DCacheInvTotal),
		atomic.LoadUint64(&asm.DCacheInvUnaligned),
		atomic.LoadUint64(&asm.DCacheInvFirstVA),
		atomic.LoadUint64(&asm.DCacheInvFirstLen),
		atomic.LoadUint64(&proc.KwBypassTotal),
		atomic.LoadUint64(&proc.KwBypassKern),
		atomic.LoadUint64(&proc.KwBypassOther),
		atomic.LoadUint64(&proc.KwBypassFirstOther),
		atomic.LoadUint64(&proc.DlgFailShepGone),
		atomic.LoadUint64(&proc.DlgFailRingFull))
}

// maz179ProbeHeartbeat is called every KernelIdleLoop iteration; rate-limits
// itself to one line per ~20s. Thread-0 only — no synchronization needed on
// maz179LastBeatTick.
func maz179ProbeHeartbeat() {
	freq := uint64(kirq.GetTimerFrequency())
	if freq == 0 {
		return
	}
	now := kirq.ReadCounterValue()
	if maz179LastBeatTick != 0 && now-maz179LastBeatTick < 10*freq {
		return
	}
	maz179LastBeatTick = now
	maz179ProbeHeartbeatLine("tick", -1)
}

var dbgCQStaleTrips uint32     // pushes skipped because the stamp was gone
var dbgCQStaleFirstMask uint32 // CQRingMask value seen on the first trip
var dbgCQStaleFirstKVA uint64  // ring KVA of the first trip

// [MAZ-179 probe — NOT FOR MERGE] BuildSignalFrame stale-SignalSP tripwire.
//
// thread.SignalSP/SignalStackBase/SignalStackSize are read out of USER memory
// (sigaltstack args, or m→gsignal→stack at clone) and then trusted forever.
// BuildSignalFrame zeroes a ~4.7KB frame and writes ~35 words at
// SignalSP-4704 with every WriteUser* return DISCARDED — a stale/garbage
// SignalSP silently scribbles whatever pages happen to be mapped there,
// reproducing BOTH MAZ-179 evidence shapes (page of zeros; single 8-byte
// slot = page-aligned heap VA, since the ss_sp store's VALUE is
// SignalStackBase). Invariant checked here: on both capture paths,
// SignalSP == SignalStackBase + SignalStackSize by construction, and every
// frame page must already be mapped (sigaltstack pre-faults the top two
// pages; WriteUser*WithL0 does not demand-fault). Violation ⇒ delivery is
// SKIPPED (neutralized) and counted; drained by the status printer.
var dbgSigFrameCalls uint32     // total BuildSignalFrame invocations
var dbgSigFrameBadSP uint32     // invariant violations (delivery skipped)
var dbgSigFrameUnmapped uint32  // frames with an unmapped page (skipped)
var dbgSigFrameStale uint32     // saved bounds != live m→gsignal bounds (skipped)
var dbgSigFrameReadFail uint32  // live gsignal bounds unreadable (NOT skipped)
var dbgSigFrameFirstTID uint64  // TID of the first trip (either kind)
var dbgSigFrameFirstSP uint64   // SignalSP of the first trip
var dbgSigFrameFirstBase uint64 // SignalStackBase of the first trip
var dbgSigFrameFirstSize uint64 // SignalStackSize of the first trip
var dbgSigFrameFirstSig uint64  // signum of the first trip

// sigFrameGsignalStale re-reads the LIVE m→gsignal→stack bounds out of user
// memory (same chain the clone-time capture used) and compares them with the
// thread's saved SignalSP/SignalStackBase. A mismatch means the gsignal
// stack moved or was freed since capture — the true staleness case that the
// SignalSP==Base+Size invariant cannot catch (a self-consistent triple still
// passes it after the region is freed and reused). Returns 1 = stale,
// 0 = matches, -1 = live bounds unreadable (no verdict).
//
//go:nosplit
func sigFrameGsignalStale(thread *Thread) int {
	if thread.MPtr == 0 {
		return -1
	}
	l0PA := thread.PageTableL0PA
	gsig, ok := kmem.ReadUserUint64WithL0(uintptr(thread.MPtr)+kirq.PreemptMGsignalOffset, l0PA)
	if !ok || gsig == 0 {
		return -1
	}
	hi, ok1 := kmem.ReadUserUint64WithL0(uintptr(gsig)+kirq.PreemptStackHiOffset, l0PA)
	lo, ok2 := kmem.ReadUserUint64WithL0(uintptr(gsig)+kirq.PreemptStackLoOffset, l0PA)
	if !ok1 || !ok2 {
		return -1
	}
	if hi != thread.SignalSP || lo != thread.SignalStackBase {
		return 1
	}
	return 0
}

// sigFrameRecordTrip records identifying detail for the first trip only.
//
//go:nosplit
func sigFrameRecordTrip(thread *Thread, signum int) {
	if atomic.AddUint32(&dbgSigFrameBadSP, 0)+atomic.AddUint32(&dbgSigFrameUnmapped, 0)+atomic.AddUint32(&dbgSigFrameStale, 0) != 1 {
		return
	}
	atomic.StoreUint64(&dbgSigFrameFirstTID, uint64(thread.TID))
	atomic.StoreUint64(&dbgSigFrameFirstSP, thread.SignalSP)
	atomic.StoreUint64(&dbgSigFrameFirstBase, thread.SignalStackBase)
	atomic.StoreUint64(&dbgSigFrameFirstSize, thread.SignalStackSize)
	atomic.StoreUint64(&dbgSigFrameFirstSig, uint64(signum))
}

// [MAZ-179 probe — NOT FOR MERGE, tier 13a] g↔m coherence check, called
// from SaveCurrentThreadContext on every full context save. EL0 saves
// only: kernel-mode (EL1) saves run on kernel g's where thread.MPtr does
// not apply. See proc/maz179_counters.go (GIdent*) for the invariant.
//
//go:nosplit
func maz179GIdentityCheck(t *Thread, x28, spsr uint64) {
	if spsr&0xF != 0 {
		return // not an EL0t save
	}
	if t.MPtr == 0 || x28 == 0 || t.PageTableL0PA == 0 {
		return
	}
	atomic.AddUint32(&proc.GIdentChecks, 1)
	m, ok := kmem.ReadUserUint64WithL0(uintptr(x28)+kirq.PreemptGMOffset, t.PageTableL0PA)
	if !ok {
		atomic.AddUint32(&proc.GIdentUnreadable, 1)
		return
	}
	if m != t.MPtr {
		if atomic.AddUint32(&proc.GIdentMismatch, 1) == 1 {
			atomic.StoreUint64(&proc.GIdentFirstTID, uint64(t.TID))
			atomic.StoreUint64(&proc.GIdentFirstG, x28)
			atomic.StoreUint64(&proc.GIdentFirstM, m)
			atomic.StoreUint64(&proc.GIdentFirstWantM, t.MPtr)
		}
	}
}

// [MAZ-179 probe — NOT FOR MERGE, tier 16] Futex wake-side contract
// witness. Called from both wake paths (ThreadWakeFutexWithSwitch,
// threadWakeFutexImpl) with schedulerLock HELD, right after the
// blockedQueue scan. A wake that found nothing while a thread still sits
// in ThreadBlockedFutex on exactly this (uaddr, shepherd) is a LOST WAKE:
// the waiter is stranded (only a timeout can save it) and the waker's
// runtime.unlock believes the handoff happened — the silent two-in-a-
// critical-section shape that scribbles in place. See proc.Ftx* counters.
//
//go:nosplit
func maz179FtxWakeAccount(woken int32, maxWake int32, futexAddr uint64, callerSID ShepherdId) {
	if woken > 0 {
		atomic.AddUint64(&proc.FtxWokenTotal, uint64(woken))
		return
	}
	atomic.AddUint64(&proc.FtxWakeZero, 1)
	if maxWake == 0 {
		return // wake(0) wakes nothing by definition — not a violation
	}
	for i := int32(0); i < MaxThreads; i++ {
		if !threadListInUse[i] {
			continue
		}
		t := &threadListData[i]
		if t.State == ThreadBlockedFutex && t.FutexAddr == futexAddr && t.PID == callerSID {
			if atomic.AddUint64(&proc.FtxLostWake, 1) == 1 {
				atomic.StoreUint64(&proc.FtxLostWakeAddr, futexAddr)
				atomic.StoreUint64(&proc.FtxLostWakeTID, uint64(t.TID))
			}
			return
		}
	}
}

// cqRingLive reports whether the shared ring page still carries the
// setup-time CQ constants. Called from nosplit IRQ top-half paths.
//
//go:nosplit
//go:noinline
func cqRingLive(ring *iouring.IORing) bool {
	if ring.CQRingMask == iouring.CQMask && ring.CQCount == iouring.CQCapacity {
		return true
	}
	if atomic.AddUint32(&dbgCQStaleTrips, 1) == 1 {
		atomic.StoreUint32(&dbgCQStaleFirstMask, ring.CQRingMask)
		atomic.StoreUint64(&dbgCQStaleFirstKVA, uint64(uintptr(unsafe.Pointer(ring))))
	}
	return false
}
