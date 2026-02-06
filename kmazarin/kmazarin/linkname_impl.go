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

// breadcrumbStringForKirq provides direct UART output to kirq package via linkname
// This is used for panic messages to avoid console recursion
//
//go:linkname breadcrumbStringForKirq mazzy/kmazarin/kirq.breadcrumbString
//go:nosplit
func breadcrumbStringForKirq(s string) {
	BreadcrumbString(s)
}

// breadcrumbHexForKirq writes a 64-bit hex value directly to UART for kirq package
//
//go:linkname breadcrumbHexForKirq mazzy/kmazarin/kirq.breadcrumbHex
//go:nosplit
func breadcrumbHexForKirq(val uint64) {
	hexChars := "0123456789ABCDEF"
	for i := 60; i >= 0; i -= 4 {
		nibble := (val >> i) & 0xF
		Breadcrumb(hexChars[nibble])
	}
}

// getRuntimeConfigForKsyscall provides runtime config to ksyscall package via linkname
//
//go:linkname getRuntimeConfigForKsyscall mazzy/kmazarin/ksyscall.getRuntimeConfig
//go:nosplit
func getRuntimeConfigForKsyscall() interface{} {
	return getFullConfig()
}

// uartPutHex64Direct writes a 64-bit hex value to UART
// Used by ksyscall, kthread, and kmem packages via linkname
//
//go:linkname uartPutHex64Direct mazzy/kmazarin/ksyscall.uartPutHex64Direct
//go:linkname uartPutHex64DirectForKmem mazzy/kmazarin/kmem.uartPutHex64Direct
//go:nosplit
func uartPutHex64Direct(val uint64) {
	hexChars := "0123456789ABCDEF"
	for i := 60; i >= 0; i -= 4 {
		nibble := (val >> i) & 0xF
		uartPutc(hexChars[nibble])
	}
}

// Alias for kmem package linkname
//
//go:nosplit
func uartPutHex64DirectForKmem(val uint64) {
	uartPutHex64Direct(val)
}

// uartPutHex32Direct writes a 32-bit hex value to UART
// Used by ksyscall and kthread packages via linkname
//
//go:linkname uartPutHex32Direct mazzy/kmazarin/ksyscall.uartPutHex32Direct
//go:nosplit
func uartPutHex32Direct(val uint32) {
	hexChars := "0123456789ABCDEF"
	for i := 28; i >= 0; i -= 4 {
		nibble := (val >> i) & 0xF
		uartPutc(hexChars[nibble])
	}
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
