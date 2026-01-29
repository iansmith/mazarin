
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
	// Protect state modification from concurrent access
	savedDAIF := SaveAndDisableIRQs()

	// Find thread by TID - use FindByIdAll to include kernel threads
	t := threadList.FindByIdAll(int32(a.tid))
	if t == nil {
		RestoreIRQs(savedDAIF)
		return // Thread not found (may have exited)
	}

	if t.State == ThreadSleeping {
		t.State = ThreadReady
		sleepingQueue.Pluck(a.tid)
		readyQueue.PushNoDuplicate(a.tid)
	} else if t.State == ThreadBlockedFutex {
		// Futex wait with timeout expired — wake the thread
		t.State = ThreadReady
		t.FutexAddr = 0
		blockedQueue.Pluck(a.tid)
		readyQueue.PushNoDuplicate(a.tid)
	}

	RestoreIRQs(savedDAIF)
}

// Verify WakeThreadAction implements Action interface
var _ util.Action = (*WakeThreadAction)(nil)
