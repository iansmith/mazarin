package main

import (
	"kmazarin/kthread"
	"kmazarin/ksyscall"
)

// MaybeInjectEventWrapper is called from the exception return path (before ERET)
// to check if there are pending async events that should be delivered to the
// current priest.
//
// This function is designed to be called from assembly with the exception frame
// pointer. If an event is pending, it modifies the frame to redirect execution
// to the event handler.
//
// TODO: Assembly integration point (exceptions_arm64.s sync_return):
//   Before the ERET at sync_return, add:
//     MOVD  SP, R0          // Pass frame pointer
//     BL    ·MaybeInjectEventWrapper(SB)
//
//go:nosplit
func MaybeInjectEventWrapper(framePtr uintptr) {
	pcb := kthread.GetCurrentPCB()
	if pcb == nil {
		return // No priest context, nothing to inject
	}

	// Check if event handler is registered
	if pcb.AsyncEventHandler == 0 {
		return // Priest hasn't registered an event handler
	}

	// Try to inject an event
	ksyscall.MaybeInjectEvent(pcb, framePtr)
}

// EventTrampoline is a small piece of code that priests must provide.
// After the event handler returns, execution continues at this address,
// which should call the EventHandled syscall.
//
// The priest's event handler signature should be:
//   func(eventType uint32, eventData uint64)
//
// And the trampoline should be:
//   func EventTrampoline() {
//       syscall.Syscall(SysEventHandled, 0, 0, 0)
//       // The syscall restores the saved context and continues
//   }
//
// When an event is injected:
//   1. Current ELR, X0, X1 are saved to PCB
//   2. ELR is set to AsyncEventHandler
//   3. X0 = event type, X1 = event data
//   4. LR is set to AsyncEventTrampoline
//   5. ERET transfers control to event handler
//   6. Event handler processes event
//   7. Event handler returns (to trampoline via LR)
//   8. Trampoline calls EventHandled syscall
//   9. Syscall restores saved ELR, X0, X1
//   10. ERET returns to original execution point
