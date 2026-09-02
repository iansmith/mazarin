package main

import "mazzy/kmazarin/klog"

// badResumeHalt reports a context that failed badResumeRIP and halts forever.
//
// Deliberately NOT nosplit: the scheduler resume sites that call it sit deep in
// the exception-vector nosplit chain, and the diagnostic print's argument temps
// pushed that chain over the 792-byte limit when inlined there. As a normal
// (morestack-checked) function it terminates the nosplit chain, and since this
// path never returns — the kernel is provably corrupt — the morestack hazard is
// no worse than the pre-existing direct klog.Criticalf calls at the same sites.
//
//go:noinline
func badResumeHalt(next *Thread, site string) {
	klog.Criticalf("[BUG] ", "BAD RESUME RIP=%#x PS=%#x TID=%d SP=%#x g=%#x (%s)\n",
		next.Context.GetPC(), next.Context.GetProcessorState(), next.TID,
		next.Context.GetSP(), next.Context.GetGRegister(), site)
	for {
		WaitForInterrupt()
	}
}
