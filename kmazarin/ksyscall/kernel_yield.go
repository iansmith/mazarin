package ksyscall

import _ "unsafe" // for go:linkname

// KernelYield performs a real thread-level yield of the kernel thread (thread 0).
// It saves the current context, puts thread 0 at the back of the ready queue,
// and switches to the next ready thread. When thread 0 is scheduled back
// (by timer preemption or when no other thread needs CPU), execution resumes
// after this call.
//
// Use this instead of runtime.Gosched() in kernel goroutines that need to
// give CPU time to shepherd threads. Gosched() only yields within Go's
// goroutine scheduler and does not cause a real thread switch.
//
// If no other thread is ready, returns immediately without yielding.
//
// Provided via go:linkname from kmazarin/kmazarin.kernelYieldForKsyscall.
//
//go:nosplit
func KernelYield()

// KernelBlockSleep puts thread 0 to sleep until the given deadline (an absolute
// hardware counter value). The timer ISR wakes thread 0 when the deadline fires.
//
// Unlike KernelYield (which places thread 0 on the ready queue behind all other
// threads), KernelBlockSleep removes thread 0 from scheduling entirely until
// the deadline. This gives precise wakeup timing regardless of how many shepherd
// threads are active.
//
// The deadline and state change happen atomically under the scheduler lock inside
// SaveThread0AndYield, so no interrupts need to be disabled by the caller.
func KernelBlockSleep(deadline uint64) {
	setThread0PendingDeadline(deadline)
	KernelYield()
}

// setThread0PendingDeadline sets the pending deadline for the next KernelYield.
// Provided via go:linkname from kmazarin/kmazarin.
//
//go:nosplit
func setThread0PendingDeadline(deadline uint64)
