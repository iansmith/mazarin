package ksyscall

// [MAZ-179 probe — NOT FOR MERGE] Starvation-proof heartbeat, syscall-driven.
//
// Emits the probe-counter line from DelegateSyscall (splittable, called
// constantly by every shepherd), rate-limited to one line per ~20s via a
// CAS claim. Criticalf → rawWrite → PollWrite is unconditionally
// serial-visible. Timestamps are raw counter seconds (counter≈0 at boot),
// comparable with the [MAZ179HB] idle-loop/at-death lines from package main.
//
// Tier-12 layout: blk = tier-7 completion guards (exercised proof + stale
// witnesses), dma = tier-12 clump-free descriptor-liveness probe, race =
// suspect-#1 TOCTOU witness, deaths = TerminateShepherd census (the wm
// "cleaning up dead shepherd" serial line only covers windowed shepherds;
// this counts every death, execve teardown included).

import (
	"sync/atomic"

	"mazzy/kmazarin/kirq"
	"mazzy/kmazarin/klog"
	"mazzy/kmazarin/proc"
)

var maz179HBLastTick uint64

func maz179SyscallHeartbeat() {
	freq := uint64(kirq.GetTimerFrequency())
	if freq == 0 {
		return
	}
	now := kirq.ReadCounterValue()
	last := atomic.LoadUint64(&maz179HBLastTick)
	if last != 0 && now-last < 10*freq {
		return
	}
	if !atomic.CompareAndSwapUint64(&maz179HBLastTick, last, now) {
		return // another thread claimed this interval
	}
	klog.Criticalf("[MAZ179HB] ",
		"[MAZ179HB] svc t=%ds blk: compl=%d submits=%d dma: frees=%d blkhits=%d nethits=%d race=%d deaths=%d gid: chk=%d mis=%d unr=%d first_tid=%d g=0x%x m=0x%x want=0x%x grant: ovl=%d first_sid=%d va=0x%x len=0x%x fixed=%d hintfb=%d mfix: res=%d pages=%d offband=%d first_va=0x%x first_res=%d\n",
		now/freq,
		atomic.LoadUint32(&proc.BlkComplTotal),
		atomic.LoadUint32(&proc.BlkSubmitTotal),
		atomic.LoadUint32(&proc.DmaClumpFrees),
		atomic.LoadUint32(&proc.DmaFreeBlkHits),
		atomic.LoadUint32(&proc.DmaFreeNetHits),
		atomic.LoadUint32(&proc.BlkFreeRace),
		atomic.LoadUint32(&proc.ShepherdDeaths),
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
		atomic.LoadUint32(&proc.MFixFirstResident))
}
