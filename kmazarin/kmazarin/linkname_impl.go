package main

import (
	"mazzy/shared/hid"
	"unsafe" // for go:linkname and unsafe.Pointer
)

// This file contains go:linkname implementations that provide functions to
// other packages (kirq, ksyscall, kmem). These are excluded during test builds
// to avoid conflicting with stub implementations.

// getRuntimeConfigForKmem provides runtime config to kmem package via linkname
//
//go:linkname getRuntimeConfigForKmem mazzy/kmazarin/kmem.getRuntimeConfig
//go:nosplit
func getRuntimeConfigForKmem() any {
	return getFullConfig()
}

// processDeadlinesForKirq provides deadline processing to kirq package via linkname
//
//go:linkname processDeadlinesForKirq mazzy/kmazarin/kirq.processDeadlines
//go:nosplit
func processDeadlinesForKirq() {
	ProcessDeadlines()
}

// getRuntimeConfigForKsyscall provides runtime config to ksyscall package via linkname
//
//go:linkname getRuntimeConfigForKsyscall mazzy/kmazarin/ksyscall.getRuntimeConfig
//go:nosplit
func getRuntimeConfigForKsyscall() any {
	return getFullConfig()
}

// ============================================================================
// Linknames for ksyscall functions defined in threads.go
// These wrappers provide the linkname binding while keeping the implementations
// in their original files.
// ============================================================================

// GetSyscallELR wrapper
//
//go:linkname getSyscallELRForKsyscall mazzy/kmazarin/ksyscall.GetSyscallELR
//go:noinline
func getSyscallELRForKsyscall() uint64 {
	return GetSyscallELR()
}

// GetSyscallSPSR wrapper
//
//go:linkname getSyscallSPSRForKsyscall mazzy/kmazarin/ksyscall.GetSyscallSPSR
//go:noinline
func getSyscallSPSRForKsyscall() uint64 {
	return GetSyscallSPSR()
}

// GetSyscallCloneRegs wrapper - returns saved R12(fn), R13(mp), R9(gp) for AMD64 clone
//
//go:linkname getSyscallCloneRegsForKsyscall mazzy/kmazarin/ksyscall.GetSyscallCloneRegs
//go:noinline
func getSyscallCloneRegsForKsyscall() (uint64, uint64, uint64) {
	return GetSyscallCloneRegs()
}

// SetSyscallCloneTLS wrapper - stores CLONE_SETTLS newtls value for AMD64 clone
//
//go:linkname setSyscallCloneTLSForKsyscall mazzy/kmazarin/ksyscall.SetSyscallCloneTLS
//go:nosplit
func setSyscallCloneTLSForKsyscall(tls uint64) {
	SetSyscallCloneTLS(tls)
}

// RecordDelegateBlock wrapper — records sysID + tick on the current thread
// just before it blocks on a delegated syscall, so printEpochStatus can show
// stuck-delegate diagnostics.
//
//go:linkname recordDelegateBlockForKsyscall mazzy/kmazarin/ksyscall.RecordDelegateBlock
//go:nosplit
func recordDelegateBlockForKsyscall(sysID uint16) {
	RecordDelegateBlock(sysID)
}

// AddDeadline wrapper
// Note: signature mismatch - main uses int16, ksyscall expects int32
//
//go:linkname addDeadlineForKsyscall mazzy/kmazarin/ksyscall.AddDeadline
//go:nosplit
func addDeadlineForKsyscall(deadline uint64, threadIdx int32) bool {
	return AddDeadline(deadline, int16(threadIdx))
}

// AddDeadlineStatic wrapper — uses the static (nosplit-safe) deadline queue
//
//go:linkname addDeadlineStaticForKsyscall mazzy/kmazarin/ksyscall.AddDeadlineStatic
//go:nosplit
func addDeadlineStaticForKsyscall(deadline uint64, tid int32) {
	AddDeadlineStatic(deadline, int16(tid))
}

// CloneThread wrapper
// Note: signature mismatch - main has extra sf *SchedulerFunc param
// We pass nil and let CloneThread use direct function calls internally.
//
//go:linkname cloneThreadForKsyscall mazzy/kmazarin/ksyscall.CloneThread
//go:nosplit
func cloneThreadForKsyscall(stack, returnAddr, spsr, mp, gp, fn uint64) int16 {
	return CloneThread(nil, stack, returnAddr, spsr, mp, gp, fn)
}

// ThreadBlockSleep wrapper
// Note: main function takes sf *SchedulerFunc param, we provide NormalSchedulerFunc
//
//go:linkname threadBlockSleepForKsyscall mazzy/kmazarin/ksyscall.ThreadBlockSleep
//go:nosplit
func threadBlockSleepForKsyscall() uintptr {
	return ThreadBlockSleep(&NormalSchedulerFunc)
}

// GetCurrentThread wrapper - returns pointer to current thread
//
//go:linkname getCurrentThreadForKsyscall mazzy/kmazarin/ksyscall.GetCurrentThread
//go:nosplit
func getCurrentThreadForKsyscall() uintptr {
	return uintptr(unsafe.Pointer(GetCurrentThread()))
}

// GetCurrentThreadTID wrapper
//
//go:linkname getCurrentThreadTIDForKsyscall mazzy/kmazarin/ksyscall.GetCurrentThreadTID
//go:nosplit
func getCurrentThreadTIDForKsyscall() int16 {
	return int16(GetCurrentThreadTID())
}

// IsCurrentThreadUserspace returns true if the current thread belongs to a
// userspace shepherd (PageTableL0PA != 0).
//
//go:linkname isCurrentThreadUserspaceForKsyscall mazzy/kmazarin/ksyscall.IsCurrentThreadUserspace
//go:nosplit
func isCurrentThreadUserspaceForKsyscall() bool {
	t := GetCurrentThread()
	return t != nil && t.PageTableL0PA != 0
}

// ============================================================================
// Signal functions for ksyscall package
// ============================================================================

//go:linkname getSignalActionForKsyscall mazzy/kmazarin/ksyscall.GetSignalAction
//go:nosplit
func getSignalActionForKsyscall(sig int) SignalAction {
	return GetSignalAction(sig)
}

//go:linkname setSignalActionForKsyscall mazzy/kmazarin/ksyscall.SetSignalAction
//go:nosplit
func setSignalActionForKsyscall(sig int, handler, flags, restorer, mask uint64) {
	sa := SignalAction{Handler: handler, Flags: flags, Restorer: restorer, Mask: mask}
	SetSignalAction(sig, &sa)
}

//go:linkname threadLookupByTIDForKsyscall mazzy/kmazarin/ksyscall.ThreadLookupByTID
//go:nosplit
func threadLookupByTIDForKsyscall(tid int32) uintptr {
	// Refuse to look up kernel-reserved TIDs from ksyscall context.
	if tid < int32(ReservedKernelThreads) {
		return 0
	}
	t := threadLookupByTID(tid)
	if t == nil {
		return 0
	}
	return uintptr(unsafe.Pointer(t))
}

//go:linkname threadLookupByPIDForKsyscall mazzy/kmazarin/ksyscall.ThreadLookupByPID
//go:nosplit
func threadLookupByPIDForKsyscall(pid int16) uintptr {
	// Refuse to look up kernel-reserved PIDs from ksyscall context.
	if pid < int16(ReservedKernelShepherds) {
		return 0
	}
	t := threadLookupByPID(pid)
	if t == nil {
		return 0
	}
	return uintptr(unsafe.Pointer(t))
}

//go:linkname setThreadPendingSignalForKsyscall mazzy/kmazarin/ksyscall.SetThreadPendingSignal
//go:nosplit
func setThreadPendingSignalForKsyscall(threadPtr uintptr, signum int) {
	t := (*Thread)(unsafe.Pointer(threadPtr))
	bit := uint64(1) << uint(signum-1)
	atomicOrUint64(&t.PendingSignals, bit)
}

//go:linkname getThreadSignalStackForKsyscall mazzy/kmazarin/ksyscall.GetThreadSignalStack
//go:nosplit
func getThreadSignalStackForKsyscall(threadPtr uintptr) (base, sp, size uint64) {
	t := (*Thread)(unsafe.Pointer(threadPtr))
	return t.SignalStackBase, t.SignalSP, t.SignalStackSize
}

//go:linkname setThreadSignalStackForKsyscall mazzy/kmazarin/ksyscall.SetThreadSignalStack
//go:nosplit
func setThreadSignalStackForKsyscall(threadPtr uintptr, base, sp, size uint64) {
	t := (*Thread)(unsafe.Pointer(threadPtr))
	t.SignalStackBase = base
	t.SignalSP = sp
	t.SignalStackSize = size
}

//go:linkname getThreadPIDForKsyscall mazzy/kmazarin/ksyscall.GetThreadPID
//go:nosplit
func getThreadPIDForKsyscall(threadPtr uintptr) int16 {
	t := (*Thread)(unsafe.Pointer(threadPtr))
	return int16(t.PID)
}

//go:linkname restoreFromSignalFrameForKsyscall mazzy/kmazarin/ksyscall.RestoreFromSignalFrame
//go:nosplit
func restoreFromSignalFrameForKsyscall(threadPtr uintptr) {
	t := (*Thread)(unsafe.Pointer(threadPtr))
	RestoreFromSignalFrame(t)
	t.InSignalHandler = 0
	t.SigreturnPending = 1
}

//go:linkname wakeThreadForSignalForKsyscall mazzy/kmazarin/ksyscall.WakeThreadForSignal
//go:nosplit
func wakeThreadForSignalForKsyscall(threadPtr uintptr) {
	t := (*Thread)(unsafe.Pointer(threadPtr))
	WakeThreadForSignal(t)
}

//go:linkname getThreadTIDForKsyscall mazzy/kmazarin/ksyscall.GetThreadTID
//go:nosplit
func getThreadTIDForKsyscall(threadPtr uintptr) int32 {
	t := (*Thread)(unsafe.Pointer(threadPtr))
	return int32(t.TID)
}

//go:linkname reservedKernelTIDsForKsyscall mazzy/kmazarin/ksyscall.ReservedKernelTIDs
//go:nosplit
func reservedKernelTIDsForKsyscall() int32 {
	return int32(ReservedKernelThreads)
}

//go:linkname reservedKernelPIDsForKsyscall mazzy/kmazarin/ksyscall.ReservedKernelPIDs
//go:nosplit
func reservedKernelPIDsForKsyscall() int16 {
	return int16(ReservedKernelShepherds)
}

// WakeNetpollThread wrapper — wakes a sleeping thread by TID.
// Used by SyscallWrite(eventfd) to implement Go's netpollBreak mechanism.
//
//go:linkname wakeNetpollThreadForKsyscall mazzy/kmazarin/ksyscall.WakeNetpollThread
//go:nosplit
func wakeNetpollThreadForKsyscall(tid int32) {
	ThreadWakeSleeper(&NormalSchedulerFunc, uintptr(tid))
}

// ForEachThreadForKsyscall iterates over threads belonging to a shepherd and
// populates the ThreadIDs array in a ShepherdInfoEntry.
//
//go:linkname forEachThreadForKsyscall mazzy/kmazarin/ksyscall.ForEachThread
func forEachThreadForKsyscall(shepherdSID int16, entry *hid.ShepherdInfoEntry) {
	tidIdx := 0
	for i := 0; i < threadArraySize; i++ {
		if threadListInUse[i] && int16(threadListData[i].PID) == shepherdSID {
			if tidIdx < len(entry.ThreadIDs) {
				entry.ThreadIDs[tidIdx] = int16(threadListData[i].TID)
				tidIdx++
			}
		}
	}
}

// kernelYieldForKsyscall provides KernelYield to the ksyscall package.
// Saves thread 0's context, puts it at the back of the ready queue, and
// switches to the next ready thread. Returns when thread 0 is scheduled back.
//
//go:linkname kernelYieldForKsyscall mazzy/kmazarin/ksyscall.KernelYield
func kernelYieldForKsyscall() {
	YieldToReadyThread()
}

// setThread0PendingDeadlineForKsyscall sets thread0PendingDeadline so that the
// next KernelYield puts thread 0 to sleep with a deadline instead of placing it
// on the ready queue. The deadline is consumed atomically inside
// SaveThread0AndYield under the scheduler lock.
//
//go:linkname setThread0PendingDeadlineForKsyscall mazzy/kmazarin/ksyscall.setThread0PendingDeadline
//go:nosplit
func setThread0PendingDeadlineForKsyscall(deadline uint64) {
	thread0PendingDeadline = deadline
}
