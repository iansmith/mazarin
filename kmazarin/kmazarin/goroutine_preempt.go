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
