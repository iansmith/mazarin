// MAZZY USERSPACE OVERLAY: Scheduler diagnostics for exitsyscall investigation.
// TODO: DIAGNOSTIC — remove after exitsyscall P-reacquisition bug is fixed.
//
// Exports P0 status and global run queue size so the syscall overlay can
// print scheduler state around exitsyscall.

//go:build linux && arm64

package runtime

import _ "unsafe" // for go:linkname

// diagSchedPStatus returns allp[0].status as a uint32.
// P status values: 0=_Pidle, 1=_Prunning, 2=_Psyscall, 3=_Pgcstop, 4=_Pdead.
//
//go:linkname diagSchedPStatus
//go:nosplit
func diagSchedPStatus() uint32 {
	if len(allp) == 0 {
		return 0xFF
	}
	return uint32(allp[0].status)
}

// diagSchedRunqSize returns sched.runq.size (global run queue depth).
//
//go:linkname diagSchedRunqSize
//go:nosplit
func diagSchedRunqSize() int32 {
	return sched.runq.size
}

// diagSchedNmspinning returns sched.nmspinning (number of spinning Ms).
//
//go:linkname diagSchedNmspinning
//go:nosplit
func diagSchedNmspinning() int32 {
	return sched.nmspinning.Load()
}

// diagSchedNpidle returns sched.npidle (number of idle Ps).
//
//go:linkname diagSchedNpidle
//go:nosplit
func diagSchedNpidle() int32 {
	return sched.npidle.Load()
}

// diagSchedLastPoll returns 1 if sched.lastpoll != 0 (netpoll initialized), 0 otherwise.
//
//go:linkname diagSchedLastPoll
//go:nosplit
func diagSchedLastPoll() int32 {
	if sched.lastpoll.Load() != 0 {
		return 1
	}
	return 0
}

// diagSchedMHasP returns whether the current M has a P (1=yes, 0=no).
//
//go:linkname diagSchedMHasP
//go:nosplit
func diagSchedMHasP() int32 {
	gp := getg()
	if gp == nil || gp.m == nil {
		return -1
	}
	if gp.m.p.ptr() != nil {
		return 1
	}
	return 0
}

// diagNetpollWaiters returns the current netpollWaiters count.
// This is incremented by netpollblockcommit when a goroutine parks waiting
// for I/O on a pollDesc. Non-zero means findRunnable's netpoll condition
// can pass even when pollUntil==0.
//
//go:linkname diagNetpollWaiters
//go:nosplit
func diagNetpollWaiters() int32 {
	return int32(netpollWaiters.Load())
}
