
package main

import "kmazarin/golang/util"

// WakeThreadAction is an Action that wakes a sleeping thread.
// It stores the thread ID (TID = slot index) for stable identification.
type WakeThreadAction struct {
	tid int16
}

// NewWakeThreadAction creates a new WakeThreadAction for the given thread ID.
//
//go:nosplit
func NewWakeThreadAction(tid int16) *WakeThreadAction {
	return &WakeThreadAction{tid: tid}
}

// Run implements the util.Action interface.
// It wakes the thread if it's still sleeping.
//
//go:nosplit
func (a *WakeThreadAction) Run() {
	// Protect state modification from concurrent access
	savedDAIF := SaveAndDisableIRQs()

	// Find thread by TID
	t := threadList.FindById(a.tid)
	if t == nil {
		RestoreIRQs(savedDAIF)
		return // Thread not found (may have exited)
	}

	if t.State == ThreadSleeping {
		t.State = ThreadReady
		// Add to ready queue
		readyQueue.Push(a.tid)
	}

	RestoreIRQs(savedDAIF)
}

// Verify WakeThreadAction implements Action interface
var _ util.Action = (*WakeThreadAction)(nil)
