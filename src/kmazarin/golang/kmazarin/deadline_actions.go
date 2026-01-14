//go:build qemuvirt && aarch64

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
	if threads[a.threadIdx].State == ThreadSleeping {
		threads[a.threadIdx].State = ThreadReady
	}
}

// Verify WakeThreadAction implements Action interface
var _ util.Action = (*WakeThreadAction)(nil)
