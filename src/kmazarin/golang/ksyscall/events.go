package ksyscall

import (
	"sync/atomic"

	"kmazarin/kthread"
)

// SyscallEventHandled signals that event handling is complete.
// Called by priest after processing an async event.
func SyscallEventHandled(arg0, arg1, arg2, arg3, arg4, arg5 uint64) int64 {
	pcb := kthread.GetCurrentPCB()
	if pcb == nil {
		return -22 // EINVAL
	}

	// Clear EventsInFlight flag
	atomic.StoreUint32(&pcb.EventsInFlight, 0)

	// TODO: This needs to modify the exception frame before ERET
	// The actual implementation requires access to the exception frame pointer
	// which is passed through the syscall dispatch mechanism
	//
	// Restore saved context:
	// frame.ELR = pcb.SavedELR
	// frame.X0 = pcb.SavedX0
	// frame.X1 = pcb.SavedX1
	//
	// Try to inject next event:
	// MaybeInjectEvent(pcb, frame)

	return 0
}

// QueueAsyncEvent adds an event to the priest's event queue.
// This is called internally by the kernel, not via syscall.
func QueueAsyncEvent(pcb *kthread.PCB, eventType uint32, eventData uint64) {
	pcb.Lock.Lock()
	defer pcb.Lock.Unlock()

	nextHead := (pcb.EventQueueHead + 1) % 16
	if nextHead == pcb.EventQueueTail {
		// Queue full - this should be a panic or at least logged
		// For now, silently drop the event
		return
	}

	pcb.EventQueue[pcb.EventQueueHead] = kthread.AsyncEvent{
		Type: eventType,
		Data: eventData,
	}
	pcb.EventQueueHead = nextHead
}

// MaybeInjectEvent injects an event before ERET (if any pending).
// Called from exception return path with frame pointer.
// Returns true if an event was injected.
func MaybeInjectEvent(pcb *kthread.PCB, framePtr uintptr) bool {
	// Try to atomically set EventsInFlight
	if !atomic.CompareAndSwapUint32(&pcb.EventsInFlight, 0, 1) {
		return false // Already handling an event
	}

	pcb.Lock.Lock()
	if pcb.EventQueueHead == pcb.EventQueueTail {
		pcb.Lock.Unlock()
		atomic.StoreUint32(&pcb.EventsInFlight, 0)
		return false // No events pending
	}

	event := pcb.EventQueue[pcb.EventQueueTail]
	pcb.EventQueueTail = (pcb.EventQueueTail + 1) % 16
	pcb.Lock.Unlock()

	// TODO: Modify exception frame to redirect to event handler
	// This requires knowing the frame layout and having framePtr be valid
	//
	// Save original context:
	// pcb.SavedELR = frame.ELR
	// pcb.SavedX0 = frame.X0
	// pcb.SavedX1 = frame.X1
	//
	// Redirect to event handler:
	// frame.ELR = pcb.AsyncEventHandler
	// frame.X0 = uint64(event.Type)
	// frame.X1 = event.Data
	// frame.X30 = pcb.AsyncEventTrampoline

	_ = framePtr
	_ = event
	return true
}
