
package main

import "mazzy/kmazarin/util"

// WakeThreadAction is an Action that wakes a sleeping thread.
// It stores the thread ID (TID) for stable identification.
type WakeThreadAction struct {
	tid ThreadId
}

// NewWakeThreadAction creates a new WakeThreadAction for the given thread ID.
//
//go:nosplit
func NewWakeThreadAction(tid ThreadId) *WakeThreadAction {
	return &WakeThreadAction{tid: tid}
}

// Run implements the util.Action interface.
// It wakes the thread if it's still sleeping.
//
//go:nosplit
func (a *WakeThreadAction) Run() {
	// Debug: show TID being woken
	Breadcrumb('[')
	Breadcrumb('W')
	Breadcrumb('k')
	Breadcrumb(':')
	Breadcrumb('0' + byte((a.tid/10)%10))
	Breadcrumb('0' + byte(a.tid%10))

	// Protect state modification from concurrent access
	savedDAIF := SaveAndDisableIRQs()

	// Find thread by TID - use FindByIdAll to include kernel threads
	t := threadList.FindByIdAll(int32(a.tid))
	if t == nil {
		Breadcrumb('?') // Thread not found
		Breadcrumb(']')
		RestoreIRQs(savedDAIF)
		return // Thread not found (may have exited)
	}

	if t.State == ThreadSleeping {
		t.State = ThreadReady
		// Add to ready queue
		readyQueue.Push(a.tid)
		Breadcrumb('!') // Successfully woken
	} else {
		Breadcrumb('x') // Not sleeping
	}
	Breadcrumb(']')

	RestoreIRQs(savedDAIF)
}

// Verify WakeThreadAction implements Action interface
var _ util.Action = (*WakeThreadAction)(nil)
