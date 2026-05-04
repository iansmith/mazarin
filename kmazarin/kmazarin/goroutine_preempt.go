package main

import _ "unsafe" // for go:linkname

// Kernel goroutine async preemption support.
//
// When the Go runtime (GC, sysmon) wants to preempt a kernel goroutine, it
// calls preemptM → signalM → tgkill(SIGURG). In kmazarin, tgkill sets a
// pending signal bit but doesn't actually interrupt the running goroutine.
// The Go runtime's m.signalPending flag is already set by preemptM before
// calling signalM.
//
// The timer IRQ handler checks m.signalPending and calls
// CheckKernelGoroutinePreempt to inject asyncPreempt into the exception
// frame if the interrupted code is at an async-safe point.
//
// This mechanism handles all three uses of SIGURG in the Go runtime:
//   1. STW (Stop the World) for GC phase transitions
//   2. Stack scanning of running goroutines
//   3. sysmon fairness preemption (goroutines running > 10ms)

// tryKernelAsyncPreempt calls into the runtime overlay to check whether the
// current kernel goroutine should be asynchronously preempted.
//
// Returns shouldPreempt=true with targetPC=asyncPreempt and resumePC if
// the interrupted code is at an async-safe point.
//
//go:linkname tryKernelAsyncPreempt runtime.TryKernelAsyncPreempt
func tryKernelAsyncPreempt(pc, sp, lr uintptr) (shouldPreempt bool, targetPC, resumePC uintptr)

// getKGPCounters returns the kernel goroutine preemption counters from the runtime.
//
//go:linkname getKGPCounters runtime.GetKGPCounters
func getKGPCounters() (seen, notWanted, unsafe_, injected uint64)

// scanCurrentGStackForFailed scans the current goroutine's stack for the
// Bug-B " failed " corruption pattern. Returns the number of hits found
// (capped at 16). Only writes to UART when hits are detected.
// Safe to call from any context.
//
//go:linkname scanCurrentGStackForFailed runtime.ScanCurrentGStackForFailed
func scanCurrentGStackForFailed() uintptr

// scanAllGStacksForFailed scans ALL goroutine stacks for the
// Bug-B " failed " corruption pattern. Returns the number of goroutines
// with at least one hit. Only writes to UART when hits are detected.
//
//go:linkname scanAllGStacksForFailed runtime.ScanAllGStacksForFailed
func scanAllGStacksForFailed() uintptr

// scanTestBugbDumpContext unconditionally calls bugbDumpContext on the
// current goroutine's stack to verify the UART output path works.
//
//go:linkname scanTestBugbDumpContext runtime.TestBugbDumpContext
func scanTestBugbDumpContext()

// scanHeapMetadataForFailed scans moduledata pclntable, ftab, findfunctab,
// text, data sections, and mspan headers for the Bug-B " failed "
// corruption pattern. Returns total hits (capped at 64).
//
//go:linkname scanHeapMetadataForFailed runtime.ScanHeapMetadataForFailed
func scanHeapMetadataForFailed() uintptr

// validateG0Stack reports g0's scheduler stack bounds and probes each
// page to detect unmapped stack pages (Pattern A hypothesis).
// Returns number of pages probed.
//
//go:linkname validateG0Stack runtime.ValidateG0Stack
func validateG0Stack() uintptr
