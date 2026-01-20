
package main

import "kmazarin/util"

// WakeThreadAction is an Action that wakes a sleeping thread.
// It stores the thread index (not a pointer) since we use a fixed array.
type WakeThreadAction struct {
	threadIdx int32
}

// NewWakeThreadAction creates a new WakeThreadAction for the given thread index.
//
//go:nosplit
func NewWakeThreadAction(threadIdx int32) *WakeThreadAction {
	return &WakeThreadAction{threadIdx: threadIdx}
}

// Run implements the util.Action interface.
// It wakes the thread if it's still sleeping.
//
//go:nosplit
func (a *WakeThreadAction) Run() {
	if a.threadIdx < 0 || a.threadIdx >= MaxThreads {
		return
	}
	// Protect state modification from concurrent access
	savedDAIF := SaveAndDisableIRQs()
	if threads[a.threadIdx].State == ThreadSleeping {
		threads[a.threadIdx].State = ThreadReady
		// Add to ready queue so it can be found efficiently
		if readyQueue != nil && threads[a.threadIdx].readyNode == nil {
			threads[a.threadIdx].readyNode = readyQueue.PushBack(&threads[a.threadIdx])
		}
	}
	RestoreIRQs(savedDAIF)
}

// Verify WakeThreadAction implements Action interface
var _ util.Action = (*WakeThreadAction)(nil)
