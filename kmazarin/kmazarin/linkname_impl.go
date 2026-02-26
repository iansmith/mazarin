//go:build !test_stubs

package main

import (
	"unsafe" // for go:linkname and unsafe.Pointer
)

// This file contains go:linkname implementations that provide functions to
// other packages (kirq, ksyscall, kmem). These are excluded during test builds
// to avoid conflicting with stub implementations.

// getRuntimeConfigForKmem provides runtime config to kmem package via linkname
//
//go:linkname getRuntimeConfigForKmem mazzy/kmazarin/kmem.getRuntimeConfig
//go:nosplit
func getRuntimeConfigForKmem() interface{} {
	return getFullConfig()
}

// getAsyncPreemptAddrForKirq provides asyncPreempt address to kirq package via linkname
//
//go:linkname getAsyncPreemptAddrForKirq mazzy/kmazarin/kirq.getAsyncPreemptAddr
//go:nosplit
func getAsyncPreemptAddrForKirq() uintptr {
	return GetAsyncPreemptAddr()
}

// getReadyForAsyncPreemptAddrForKirq provides readyForAsyncPreempt flag address to kirq package via linkname
//
//go:linkname getReadyForAsyncPreemptAddrForKirq mazzy/kmazarin/kirq.getReadyForAsyncPreemptAddr
//go:nosplit
func getReadyForAsyncPreemptAddrForKirq() uintptr {
	return GetReadyForAsyncPreemptAddr()
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
func getRuntimeConfigForKsyscall() interface{} {
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
	t := threadLookupByTID(tid)
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

//go:linkname restoreFromSignalFrameForKsyscall mazzy/kmazarin/ksyscall.RestoreFromSignalFrame
//go:nosplit
func restoreFromSignalFrameForKsyscall(threadPtr uintptr) {
	t := (*Thread)(unsafe.Pointer(threadPtr))
	RestoreFromSignalFrame(t)
	t.InSignalHandler = 0
	t.SigreturnPending = 1
}

// ============================================================================
// Per-CPU Accessors for kirq package
// ============================================================================

// GetPerCPUNeedsAsyncPreempt returns the per-CPU NeedsAsyncPreempt value.
// This is used by kirq.ProcessTimerIRQ to check if preemption is needed.
//
//go:linkname getPerCPUNeedsAsyncPreempt mazzy/kmazarin/kirq.getPerCPUNeedsAsyncPreempt
//go:nosplit
func getPerCPUNeedsAsyncPreempt() uint32 {
	return GetPerCPU().NeedsAsyncPreempt
}

// SetPerCPUNeedsAsyncPreempt sets the per-CPU NeedsAsyncPreempt value.
// This is used by kirq to clear the flag after processing.
//
//go:linkname setPerCPUNeedsAsyncPreempt mazzy/kmazarin/kirq.setPerCPUNeedsAsyncPreempt
//go:nosplit
func setPerCPUNeedsAsyncPreempt(val uint32) {
	GetPerCPU().NeedsAsyncPreempt = val
}

// SyncGlobalNeedsAsyncPreemptToPerCPU copies the global NeedsAsyncPreempt
// value to the per-CPU struct. Called from timer handler to ensure per-CPU
// is up to date after assembly sets the global.
//
//go:linkname syncGlobalNeedsAsyncPreemptToPerCPU mazzy/kmazarin/kirq.syncGlobalNeedsAsyncPreemptToPerCPU
//go:nosplit
func syncGlobalNeedsAsyncPreemptToPerCPU(globalVal uint32) {
	GetPerCPU().NeedsAsyncPreempt = globalVal
}
